package feed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

type RPCWSOptions struct {
	WSURL, StartMode               string
	StartBlock, MaxCatchup, MaxLag uint64
	RequestInterval                time.Duration
	Logger                         *slog.Logger
}

// WS notifications are wakeups; only HTTP canonical blocks can advance a checkpoint.
// Step is single-writer. WS keeps a bounded notification queue independent of backfill.
type RPCWSRunner struct {
	receipt      *EVMReceiptRunner
	opts         RPCWSOptions
	parser       *parser.Parser
	connected    atomic.Bool
	healthy      atomic.Bool
	lastWS       atomic.Int64
	lastCommit   atomic.Int64
	stepMu       sync.Mutex
	nextRequest  time.Time
	lastIdentity time.Time
	wake         chan struct{}
}

type rpcHeader struct {
	Number       hexutil.Uint64 `json:"number"`
	Hash         common.Hash    `json:"hash"`
	ParentHash   common.Hash    `json:"parentHash"`
	Timestamp    hexutil.Uint64 `json:"timestamp"`
	Transactions []common.Hash  `json:"transactions"`
}

func NewRPCWSRunner(base *EVMReceiptRunner, value *parser.Parser, opts RPCWSOptions) (*RPCWSRunner, error) {
	if base == nil || opts.MaxCatchup == 0 || opts.MaxCatchup > 10000 || opts.MaxLag > opts.MaxCatchup || opts.RequestInterval < 0 {
		return nil, fmt.Errorf("invalid RPC WS options")
	}
	for _, endpoint := range []struct {
		value string
		ws    bool
	}{{opts.WSURL, true}, {base.endpoint, false}} {
		u, err := url.Parse(endpoint.value)
		if err != nil || u.Host == "" || (endpoint.ws && u.Scheme != "ws" && u.Scheme != "wss") || (!endpoint.ws && u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("invalid RPC endpoint scheme")
		}
	}
	if opts.StartMode != "latest" && opts.StartMode != "block" {
		return nil, fmt.Errorf("invalid RPC initial mode")
	}
	if opts.StartMode == "block" && opts.StartBlock == 0 {
		return nil, fmt.Errorf("RPC start block must be positive")
	}
	if value == nil {
		value = parser.New()
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &RPCWSRunner{receipt: base, opts: opts, parser: value, wake: make(chan struct{}, 1)}, nil
}

func (r *RPCWSRunner) call(ctx context.Context, method string, args []any, out any) error {
	if delay := time.Until(r.nextRequest); delay > 0 {
		if err := waitRPC(ctx, delay); err != nil {
			return err
		}
	}
	r.nextRequest = time.Now().Add(r.opts.RequestInterval)
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.receipt.rpcCall(reqCtx, method, args, out)
}

func (r *RPCWSRunner) header(ctx context.Context, tag string) (*rpcHeader, error) {
	var h *rpcHeader
	if err := r.call(ctx, "eth_getBlockByNumber", []any{tag, false}, &h); err != nil {
		return nil, err
	}
	if h == nil || h.Transactions == nil || h.Hash == (common.Hash{}) || (uint64(h.Number) > 0 && h.ParentHash == (common.Hash{})) || h.Timestamp == 0 || time.Unix(int64(h.Timestamp), 0).After(time.Now().Add(2*time.Second)) {
		return nil, fmt.Errorf("missing or invalid RPC block header")
	}
	return h, nil
}

func (r *RPCWSRunner) Ready(ctx context.Context) bool {
	now := time.Now()
	if !r.connected.Load() || !r.healthy.Load() || now.Sub(time.Unix(0, r.lastWS.Load())) > 10*time.Second || now.Sub(time.Unix(0, r.lastCommit.Load())) > 10*time.Second {
		return false
	}
	p, err := r.receipt.store.Progress(ctx)
	return err == nil && !p.Degraded
}

func (r *RPCWSRunner) halt(ctx context.Context, from, to uint64, reason string) error {
	r.healthy.Store(false)
	if err := r.receipt.store.haltRPC(ctx, r.receipt.chainID, from, to, reason); err != nil {
		return err
	}
	r.opts.Logger.Error("RPC feed halted", "chain_id", r.receipt.chainID, "from_block", from, "to_block", to, "reason", reason)
	return fmt.Errorf("RPC feed halted: %s", reason)
}

// Step processes at most one complete block. A nil success with advanced=false
// means caught up; it never reinterprets the sequencer message cursor as a height.
func (r *RPCWSRunner) Step(ctx context.Context) (advanced bool, err error) {
	r.stepMu.Lock()
	defer r.stepMu.Unlock()
	defer func() {
		if err != nil {
			r.healthy.Store(false)
		}
	}()
	p, err := r.receipt.store.Progress(ctx)
	if err != nil {
		return false, err
	}
	if p.Degraded {
		return false, fmt.Errorf("feed requires explicit degraded-state recovery")
	}
	if time.Since(r.lastIdentity) > 10*time.Second {
		var chain hexutil.Uint64
		if err = r.call(ctx, "eth_chainId", nil, &chain); err != nil {
			return false, err
		}
		if uint64(chain) != r.receipt.chainID {
			return false, r.halt(ctx, 0, 0, "HTTP chain identity mismatch")
		}
		r.lastIdentity = time.Now()
	}
	head, err := r.header(ctx, "latest")
	if err != nil {
		return false, err
	}
	cp, found, err := r.receipt.store.RPCCheckpoint(ctx, r.receipt.chainID)
	if err != nil {
		return false, err
	}
	if !found {
		start := uint64(head.Number)
		if r.opts.StartMode == "block" {
			start = r.opts.StartBlock
		}
		if start == 0 || start > uint64(head.Number) || uint64(head.Number)-start >= r.opts.MaxCatchup {
			return false, r.halt(ctx, start, uint64(head.Number), "initial block outside bounded catchup window")
		}
		baseline, e := r.header(ctx, hexutil.EncodeUint64(start-1))
		if e != nil {
			return false, e
		}
		if uint64(baseline.Number) != start-1 {
			return false, fmt.Errorf("wrong baseline block")
		}
		cp = RPCCheckpoint{ChainID: r.receipt.chainID, Number: start - 1, Hash: baseline.Hash}
		if err = r.receipt.store.initializeRPC(ctx, cp); err != nil {
			return false, err
		}
		r.opts.Logger.Info("RPC feed baseline recorded", "chain_id", cp.ChainID, "baseline_block", cp.Number)
	}
	n := uint64(head.Number)
	if n < cp.Number {
		return false, r.halt(ctx, n, cp.Number, "canonical head below checkpoint")
	}
	if n == cp.Number {
		if head.Hash != cp.Hash {
			return false, r.halt(ctx, n, n, "checkpoint block hash changed")
		}
		r.healthy.Store(time.Since(time.Unix(int64(head.Timestamp), 0)) <= 10*time.Second)
		r.lastCommit.Store(time.Now().UnixNano())
		return false, nil
	}
	if n-cp.Number > r.opts.MaxCatchup {
		return false, r.halt(ctx, cp.Number+1, n, "catchup limit exceeded; no blocks skipped")
	}
	block := head
	if n != cp.Number+1 {
		block, err = r.header(ctx, hexutil.EncodeUint64(cp.Number+1))
		if err != nil {
			return false, err
		}
	}
	if uint64(block.Number) != cp.Number+1 || block.ParentHash != cp.Hash {
		return false, r.halt(ctx, cp.Number+1, n, "parent hash continuity mismatch")
	}
	var raw json.RawMessage
	if err = r.call(ctx, "eth_getBlockReceipts", []any{hexutil.EncodeUint64(uint64(block.Number))}, &raw); err != nil {
		return false, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return false, fmt.Errorf("null block receipts")
	}
	var receipts []rpcReceipt
	if err = json.Unmarshal(raw, &receipts); err != nil {
		return false, err
	}
	verified, err := verifyRPCReceipts(block, receipts)
	if err != nil {
		return false, err
	}
	before := r.parser.Registry().Snapshot()
	committed := false
	defer func() {
		if !committed {
			if e := r.parser.Registry().Restore(before); e != nil {
				err = errors.Join(err, e)
			}
		}
	}()
	observations, err := r.blockObservations(ctx, block, receipts, verified)
	if err != nil {
		return false, err
	}
	canonical, err := r.header(ctx, hexutil.EncodeUint64(uint64(block.Number)))
	if err != nil {
		return false, err
	}
	if canonical.Hash != block.Hash {
		return false, r.halt(ctx, uint64(block.Number), n, "block changed during receipt collection")
	}
	commit := rpcBlockCommit{RPCCheckpoint: RPCCheckpoint{ChainID: r.receipt.chainID, Number: uint64(block.Number), Hash: block.Hash}, ParentHash: block.ParentHash}
	for _, v := range verified {
		reason := "receipt_successful"
		if v.Status != 1 {
			reason = "receipt_reverted"
		}
		commit.Audits = append(commit.Audits, ReceiptAudit{TransactionHash: v.TxHash, BlockNumber: uint64(block.Number), ReceiptStatus: v.Status, Reason: reason})
	}
	snapshot := r.parser.Registry().Snapshot()
	if _, err = r.receipt.store.commitBlock(ctx, observations, nil, &snapshot, &commit); err != nil {
		return false, err
	}
	committed = true
	r.lastCommit.Store(time.Now().UnixNano())
	r.healthy.Store(n-uint64(block.Number) <= r.opts.MaxLag && time.Since(time.Unix(int64(block.Timestamp), 0)) <= 10*time.Second)
	return true, nil
}

func (r *RPCWSRunner) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); r.connections(ctx) }()
	defer func() { cancel(); <-done }()
	retry := 250 * time.Millisecond
	for ctx.Err() == nil {
		if !r.connected.Load() {
			if err := waitRPC(ctx, 250*time.Millisecond); err != nil {
				break
			}
			continue
		}
		advanced, err := r.Step(ctx)
		if err != nil {
			// Endpoint errors can contain credentials; log classification only.
			r.opts.Logger.Warn("RPC feed collection unsuccessful", "chain_id", r.receipt.chainID, "error_type", fmt.Sprintf("%T", err))
			delay := retry
			var limit *rpcHTTPError
			if errors.As(err, &limit) && limit.RetryAfter > delay {
				delay = limit.RetryAfter
			}
			if e := waitRPC(ctx, delay); e != nil {
				break
			}
			if retry < 10*time.Second {
				retry *= 2
			}
			continue
		}
		retry = 250 * time.Millisecond
		if advanced {
			continue
		}
		select {
		case <-ctx.Done():
		case <-r.wake:
		case <-time.After(time.Second):
		}
	}
	r.healthy.Store(false)
	return nil
}

func (r *RPCWSRunner) connections(ctx context.Context) {
	delay := 250 * time.Millisecond
	for ctx.Err() == nil {
		connectedAt := time.Now()
		r.subscription(ctx)
		r.connected.Store(false)
		r.healthy.Store(false)
		if ctx.Err() != nil {
			return
		}
		r.opts.Logger.Warn("RPC WS disconnected; retrying", "chain_id", r.receipt.chainID)
		if time.Since(connectedAt) > 10*time.Second {
			delay = 250 * time.Millisecond
		}
		if waitRPC(ctx, delay) != nil {
			return
		}
		if delay < 10*time.Second {
			delay *= 2
		}
	}
}

func (r *RPCWSRunner) subscription(ctx context.Context) {
	dialCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	client, err := rpc.DialContext(dialCtx, r.opts.WSURL)
	cancel()
	if err != nil {
		return
	}
	defer client.Close()
	checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
	var chain hexutil.Uint64
	err = client.CallContext(checkCtx, &chain, "eth_chainId")
	checkCancel()
	if err != nil {
		return
	}
	if uint64(chain) != r.receipt.chainID {
		_ = r.halt(ctx, 0, 0, "WS chain identity mismatch")
		return
	}
	heads := make(chan rpcHeader, 32)
	subCtx, subCancel := context.WithTimeout(ctx, 5*time.Second)
	sub, err := client.EthSubscribe(subCtx, heads, "newHeads")
	subCancel()
	if err != nil {
		return
	}
	defer sub.Unsubscribe()
	// Geth RPC transport supplies ping/pong deadlines. A quiet/stalled subscription
	// is additionally closed here so a live TCP socket cannot imply readiness.
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Err():
			return
		case <-timer.C:
			return
		case h := <-heads:
			if h.Hash == (common.Hash{}) || h.Number == 0 || h.Timestamp == 0 || time.Since(time.Unix(int64(h.Timestamp), 0)) > 10*time.Second {
				return
			}
			r.lastWS.Store(time.Now().UnixNano())
			r.connected.Store(true)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(10 * time.Second)
			select {
			case r.wake <- struct{}{}:
			default:
			}
		}
	}
}

func waitRPC(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
