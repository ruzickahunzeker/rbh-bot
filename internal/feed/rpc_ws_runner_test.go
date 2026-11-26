package feed

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

type rpcFixture struct {
	mu       sync.Mutex
	head     uint64
	chain    uint64
	blocks   map[uint64]*rpcHeader
	receipts map[uint64]any
	calls    int
}

func newRPCFixture(t *testing.T) (*rpcFixture, *httptest.Server) {
	t.Helper()
	f := &rpcFixture{head: 10, chain: 4663, blocks: map[uint64]*rpcHeader{}, receipts: map[uint64]any{}}
	for n := uint64(0); n < 50; n++ {
		f.blocks[n] = &rpcHeader{Number: hexutil.Uint64(n), Hash: common.BigToHash(new(big.Int).SetUint64(n + 100)), ParentHash: common.BigToHash(new(big.Int).SetUint64(n + 99)), Timestamp: hexutil.Uint64(time.Now().Unix()), Transactions: []common.Hash{}}
		f.receipts[n] = []any{}
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		var req struct {
			ID     any               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var result any
		switch req.Method {
		case "eth_chainId":
			result = hexutil.EncodeUint64(f.chain)
		case "eth_getBlockByNumber", "eth_getBlockReceipts":
			var tag string
			_ = json.Unmarshal(req.Params[0], &tag)
			n := f.head
			if tag != "latest" {
				n, _ = hexutil.DecodeUint64(tag)
			}
			if req.Method == "eth_getBlockByNumber" {
				result = f.blocks[n]
			} else {
				result = f.receipts[n]
			}
		default:
			t.Errorf("unexpected method %s", req.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(s.Close)
	return f, s
}

func fixtureRunner(t *testing.T, endpoint string) (*RPCWSRunner, *Store) {
	t.Helper()
	store, db := openFeedStore(t)
	t.Cleanup(func() { db.Close() })
	base, err := NewEVMReceiptRunner(4663, parser.DefaultAddressBook().WETH, endpoint, store, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRPCWSRunner(base, parser.New(), RPCWSOptions{WSURL: "ws://example.invalid", StartMode: "latest", MaxCatchup: 20, MaxLag: 2})
	if err != nil {
		t.Fatal(err)
	}
	return r, store
}

func TestRPCWSIndependentCursorCatchupAndRestart(t *testing.T) {
	ctx := context.Background()
	f, s := newRPCFixture(t)
	r, store := fixtureRunner(t, s.URL)
	if _, err := store.CommitSequencer(ctx, nil, 999999999); err != nil {
		t.Fatal(err)
	}
	if advanced, err := r.Step(ctx); err != nil || !advanced {
		t.Fatalf("initial %v %v", advanced, err)
	}
	p, _, _ := store.RPCCheckpoint(ctx, 4663)
	if p.Number != 10 {
		t.Fatal(p)
	}
	old, _ := store.Progress(ctx)
	if *old.ObservedSequence != 999999999 {
		t.Fatal("sequencer cursor overwritten")
	}
	f.head = 13
	for n := uint64(11); n <= 13; n++ {
		if ok, err := r.Step(ctx); !ok || err != nil {
			t.Fatal(n, err)
		}
	}
	before := f.calls
	if ok, err := r.Step(ctx); ok || err != nil {
		t.Fatal(ok, err)
	}
	if f.calls-before > 3 {
		t.Fatal("unbounded caught-up requests")
	}
	// Restart reuses the persisted block checkpoint, not the default latest mode.
	restarted, err := NewRPCWSRunner(r.receipt, parser.New(), r.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.head = 14
	if ok, err := restarted.Step(ctx); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if r.Ready(ctx) {
		t.Fatal("disconnected WS reported ready")
	}
	r.connected.Store(true)
	r.lastWS.Store(time.Now().UnixNano())
	r.healthy.Store(true)
	if !r.Ready(ctx) {
		t.Fatal("caught-up live WS not ready")
	}
	r.lastWS.Store(time.Now().Add(-time.Minute).UnixNano())
	if r.Ready(ctx) {
		t.Fatal("stalled WS ready")
	}
}

func TestRPCWSGapReorgNullAndIncompleteFailClosed(t *testing.T) {
	for _, kind := range []string{"gap", "reorg", "same-height-reorg", "null", "incomplete", "chain"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			f, s := newRPCFixture(t)
			r, store := fixtureRunner(t, s.URL)
			if _, err := r.Step(ctx); err != nil {
				t.Fatal(err)
			}
			f.head = 11
			switch kind {
			case "gap":
				f.head = 40
			case "reorg":
				f.blocks[11].ParentHash = common.HexToHash("0xdead")
			case "same-height-reorg":
				f.head = 10
				f.blocks[10].Hash = common.HexToHash("0xdead")
			case "null":
				f.receipts[11] = nil
			case "incomplete":
				f.blocks[11].Transactions = []common.Hash{common.HexToHash("0x1234")}
			case "chain":
				f.chain = 56
				r.lastIdentity = time.Time{}
			}
			before := f.calls
			if _, err := r.Step(ctx); err == nil {
				t.Fatal("unsafe response accepted")
			}
			p, _, _ := store.RPCCheckpoint(ctx, 4663)
			if p.Number != 10 {
				t.Fatal("failed response advanced cursor")
			}
			if f.calls-before > 5 {
				t.Fatal("unbounded catchup")
			}
			if r.Ready(ctx) {
				t.Fatal("failure ready")
			}
		})
	}
}

func TestRPCWSAtomicBlockFailureAndReplay(t *testing.T) {
	ctx := context.Background()
	f, s := newRPCFixture(t)
	r, store := fixtureRunner(t, s.URL)
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	block := rpcBlockCommit{RPCCheckpoint: RPCCheckpoint{ChainID: 4663, Number: 11, Hash: f.blocks[11].Hash}, ParentHash: f.blocks[11].ParentHash}
	obs := testObservation(11, 1, "receipt/log/1/test")
	obs.Source = SourceRPC
	if _, err := store.db.Exec(`CREATE TRIGGER reject_checkpoint BEFORE UPDATE ON feed_rpc_checkpoints BEGIN SELECT RAISE(ABORT,'crash window');END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.commitBlock(ctx, []Observation{obs}, nil, nil, &block); err == nil {
		t.Fatal("failure missing")
	}
	var rows int
	_ = store.db.QueryRow(`SELECT count(*) FROM feed_events`).Scan(&rows)
	if rows != 0 {
		t.Fatal("partial events committed")
	}
	p, _, _ := store.RPCCheckpoint(ctx, 4663)
	if p.Number != 10 {
		t.Fatal("partial checkpoint")
	}
	_, _ = store.db.Exec(`DROP TRIGGER reject_checkpoint`)
	result, err := store.commitBlock(ctx, []Observation{obs}, nil, nil, &block)
	if err != nil || result.InsertedObservations != 1 {
		t.Fatal(result, err)
	}
	replay, err := store.commitBlock(ctx, []Observation{obs}, nil, nil, &block)
	if err != nil || replay.InsertedObservations != 0 {
		t.Fatal(replay, err)
	}
}

func TestRPCWSPonsHistoricalNormalization(t *testing.T) {
	_, s := newRPCFixture(t)
	r, _ := fixtureRunner(t, s.URL)
	receipt := loadReceipt(t, filepath.Join("..", "..", "rbh-validation-package", "fixtures", "historical", "candidates", "0x45142a829ca4ce83909c0f87408b27f70da8a1853a101921fffc20ff5fc08b10.json"))
	events, err := r.parser.ParseReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var launch, trade bool
	for _, event := range events {
		p, ok := r.normalizeRBH(event, receipt, common.HexToAddress("0x1234"))
		if !ok {
			continue
		}
		if p.Side == "launch" {
			launch = true
		}
		if p.Side == "buy" {
			trade = true
			if p.TokenDeltaRaw == "0" {
				t.Fatal("zero trade")
			}
		}
	}
	if !launch || !trade {
		t.Fatalf("Pons launch/trade missing: %v %v", launch, trade)
	}
}

func TestRPCWSPonsBlockToDurableOutbox(t *testing.T) {
	f, s := newRPCFixture(t)
	runner, store := fixtureRunner(t, s.URL)
	path := filepath.Join("..", "..", "rbh-validation-package", "fixtures", "historical", "candidates", "0x45142a829ca4ce83909c0f87408b27f70da8a1853a101921fffc20ff5fc08b10.json")
	receipt := loadReceipt(t, path)
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var raw rpcReceipt
	if err = json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	_, sender := loadCapturedTransaction(t, path)
	h := f.blocks[10]
	h.Transactions = []common.Hash{receipt.TxHash}
	raw.From = sender.Hex()
	raw.BlockNumber = "0xa"
	raw.BlockHash = h.Hash.Hex()
	raw.TransactionIndex = "0x0"
	for i := range raw.Logs {
		lg := &raw.Logs[i]
		lg.BlockNumber = "0xa"
		lg.BlockHash = h.Hash.Hex()
		lg.TransactionHash = raw.TransactionHash
		lg.TransactionIndex = "0x0"
	}
	f.receipts[10] = []rpcReceipt{raw}
	if _, err := runner.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, err := store.ReadOutboxAfter(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var buy bool
	for _, item := range items {
		var payload NormalizedSwapPayload
		if err := json.Unmarshal(item.PayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.EventTime.IsZero() || payload.BlockHash != h.Hash.Hex() || payload.Stage != "included" {
			t.Fatal("lost canonical block metadata")
		}
		if payload.Side == "buy" {
			buy = true
			if !common.IsHexAddress(payload.Trader) || !common.IsHexAddress(payload.Token) {
				t.Fatal("invalid Pons economics")
			}
		}
	}
	if !buy {
		t.Fatal("Pons buy not published")
	}
	if _, ok, err := store.LoadRegistrySnapshot(context.Background()); err != nil || !ok {
		t.Fatal("registry not atomically persisted", err)
	}
}

type wsFixtureAPI struct {
	chain         uint64
	subscriptions atomic.Int64
}

func (a *wsFixtureAPI) ChainId() string { return hexutil.EncodeUint64(a.chain) }
func (a *wsFixtureAPI) NewHeads(ctx context.Context) (*rpc.Subscription, error) {
	notifier, ok := rpc.NotifierFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("no notifier")
	}
	sub := notifier.CreateSubscription()
	a.subscriptions.Add(1)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-sub.Err():
				return
			case <-ticker.C:
				_ = notifier.Notify(sub.ID, rpcHeader{Number: 10, Hash: common.HexToHash("0x1234"), Timestamp: hexutil.Uint64(time.Now().Unix())})
			}
		}
	}()
	return sub, nil
}

func TestRPCWSRealSubscriptionAndDisconnect(t *testing.T) {
	_, s := newRPCFixture(t)
	r, _ := fixtureRunner(t, s.URL)
	server := rpc.NewServer()
	if err := server.RegisterName("eth", &wsFixtureAPI{chain: 4663}); err != nil {
		t.Fatal(err)
	}
	ws := httptest.NewServer(server.WebsocketHandler([]string{"*"}))
	defer ws.Close()
	defer server.Stop()
	r.opts.WSURL = "ws" + ws.URL[4:]
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.subscription(ctx) }()
	deadline := time.After(3 * time.Second)
	for !r.connected.Load() {
		select {
		case <-deadline:
			t.Fatal("subscription never received headers")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WS shutdown blocked")
	}
}

func TestRPCWSReconnectResubscribes(t *testing.T) {
	_, s := newRPCFixture(t)
	r, _ := fixtureRunner(t, s.URL)
	server := rpc.NewServer()
	api := &wsFixtureAPI{chain: 4663}
	if err := server.RegisterName("eth", api); err != nil {
		t.Fatal(err)
	}
	// Force each transport closed shortly after it establishes its subscription.
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn := &closingHijacker{ResponseWriter: w}
		server.WebsocketHandler([]string{"*"}).ServeHTTP(conn, req)
	}))
	defer ws.Close()
	defer server.Stop()
	r.opts.WSURL = "ws" + ws.URL[4:]
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); r.connections(ctx) }()
	for api.subscriptions.Load() < 2 && ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if api.subscriptions.Load() < 2 {
		t.Fatal("did not resubscribe after transport loss")
	}
	if r.Ready(context.Background()) {
		t.Fatal("disconnected runner ready")
	}
}

type closingHijacker struct{ http.ResponseWriter }

func (w *closingHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, rw, err := w.ResponseWriter.(http.Hijacker).Hijack()
	if err == nil {
		time.AfterFunc(100*time.Millisecond, func() { c.Close() })
	}
	return c, rw, err
}

func TestRPCWSRateLimitMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Retry-After", "2"); w.WriteHeader(429) }))
	defer server.Close()
	runner, _ := fixtureRunner(t, server.URL)
	var out any
	err := runner.receipt.rpcCall(context.Background(), "eth_chainId", nil, &out)
	var limit *rpcHTTPError
	if !errors.As(err, &limit) || limit.Status != 429 || limit.RetryAfter != 2*time.Second {
		t.Fatal("lost rate limit metadata", err)
	}
}
