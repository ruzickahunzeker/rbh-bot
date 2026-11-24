package trade

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// RelayMode controls whether raw signed transactions may ever touch public mempool RPCs.
type RelayMode string

const (
	// RelayModePrivateOnly enforces BSC MEV protection: broadcasts ONLY to configured private
	// builder/relay endpoints (e.g. 48Club, bloXroute, NodeReal MEV) and NEVER falls back to public RPC.
	RelayModePrivateOnly RelayMode = "private_only"
	// RelayModePublicRPC allows standard eth_sendRawTransaction to the chain RPC (e.g. Robinhood, Base).
	RelayModePublicRPC RelayMode = "public_rpc"
)

var (
	ErrPrivateRelayRequired  = errors.New("private_only relay mode requires at least one private relay endpoint; public fallback forbidden")
	ErrRelayBroadcastUnknown = errors.New("broadcast outcome unknown across relays; query-first recovery required")
)

// RelayBroadcastResult aggregates per-endpoint outcomes and the conservative merged state.
type RelayBroadcastResult struct {
	TxHash           common.Hash
	MergedState      MergedBroadcastState
	EndpointOutcomes map[string]RelayOutcome
	Errors           []string
}

// MultiRelayBroadcaster fans out eth_sendRawTransaction across private relays (or public RPC
// when explicitly permitted) with strict private_only fail-closed guarantees.
type MultiRelayBroadcaster struct {
	mode             RelayMode
	privateEndpoints []string
	publicEndpoint   string
	client           *http.Client
}

func NewMultiRelayBroadcaster(mode RelayMode, privateEndpoints []string, publicEndpoint string, timeout time.Duration) (*MultiRelayBroadcaster, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	var cleanPrivate []string
	for _, ep := range privateEndpoints {
		if trimmed := strings.TrimSpace(ep); trimmed != "" {
			cleanPrivate = append(cleanPrivate, trimmed)
		}
	}
	cleanPublic := strings.TrimSpace(publicEndpoint)

	switch mode {
	case RelayModePrivateOnly:
		if len(cleanPrivate) == 0 {
			return nil, ErrPrivateRelayRequired
		}
	case RelayModePublicRPC:
		if len(cleanPrivate) == 0 && cleanPublic == "" {
			return nil, errors.New("no broadcast RPC endpoints configured")
		}
	default:
		return nil, fmt.Errorf("unsupported relay mode %q", mode)
	}

	return &MultiRelayBroadcaster{
		mode:             mode,
		privateEndpoints: cleanPrivate,
		publicEndpoint:   cleanPublic,
		client: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          16,
				MaxIdleConnsPerHost:   8,
				IdleConnTimeout:       60 * time.Second,
				ResponseHeaderTimeout: timeout,
			},
			Timeout: timeout,
		},
	}, nil
}

// BroadcastRawTx sends raw signed tx bytes to all eligible endpoints in parallel and merges
// outcomes using the conservative AT-06/AT-19 rule (MergeMultiRelayOutcomes).
func (b *MultiRelayBroadcaster) BroadcastRawTx(ctx context.Context, txHash common.Hash, rawSignedTx []byte) (RelayBroadcastResult, error) {
	if b == nil || len(rawSignedTx) == 0 {
		return RelayBroadcastResult{}, errors.New("invalid broadcast request")
	}
	targets := b.privateEndpoints
	if b.mode == RelayModePrivateOnly {
		if len(targets) == 0 {
			return RelayBroadcastResult{}, ErrPrivateRelayRequired
		}
	} else if len(targets) == 0 {
		targets = []string{b.publicEndpoint}
	}

	rawHex := "0x" + hex.EncodeToString(rawSignedTx)
	outcomes := make([]RelayOutcome, len(targets))
	endpointMap := make(map[string]RelayOutcome, len(targets))
	var errs []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i, ep := range targets {
		wg.Add(1)
		go func(idx int, endpoint string) {
			defer wg.Done()
			outcome, err := b.sendOne(ctx, endpoint, rawHex)
			mu.Lock()
			outcomes[idx] = outcome
			endpointMap[endpoint] = outcome
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", endpoint, err))
			}
			mu.Unlock()
		}(i, ep)
	}
	wg.Wait()

	merged := MergeMultiRelayOutcomes(outcomes)
	res := RelayBroadcastResult{
		TxHash:           txHash,
		MergedState:      merged,
		EndpointOutcomes: endpointMap,
		Errors:           errs,
	}
	switch merged {
	case MergedSubmitted:
		return res, nil
	case MergedKnownUnsent:
		return res, ErrBroadcastRejected
	default:
		return res, ErrRelayBroadcastUnknown
	}
}

func (b *MultiRelayBroadcaster) sendOne(ctx context.Context, endpoint, rawHex string) (RelayOutcome, error) {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "eth_sendRawTransaction",
		"params":  []any{rawHex},
	})
	if err != nil {
		return RelayNetworkError, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return RelayNetworkError, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return RelayTimeout, err
		}
		return RelayNetworkError, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return RelayTimeout, fmt.Errorf("relay HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return RelayNetworkError, err
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return RelayNetworkError, err
	}
	if env.Error != nil {
		msgLower := strings.ToLower(env.Error.Message)
		if strings.Contains(msgLower, "already known") || strings.Contains(msgLower, "known transaction") {
			return RelayAlreadyKnown, nil
		}
		// Explicit deterministic rejection (insufficient funds, nonce too low, gas too low, execution reverted).
		return RelayRejected, fmt.Errorf("rpc %d: %s", env.Error.Code, env.Error.Message)
	}
	if len(env.Result) > 0 && string(env.Result) != "null" {
		return RelaySubmitted, nil
	}
	return RelayNetworkError, errors.New("empty rpc result")
}

// QueryFirstChainEvidence performs the mandatory read-only chain check before issuing an
// UNKNOWN_REPLAY permit (AT-06).
func QueryFirstChainEvidence(ctx context.Context, readRPCURL string, txHash common.Hash, wallet common.Address, attemptNonce uint64) (QueryFirstEvidence, error) {
	if strings.TrimSpace(readRPCURL) == "" || txHash == (common.Hash{}) || wallet == (common.Address{}) {
		return QueryFirstEvidence{}, errors.New("invalid query-first inputs")
	}
	client := &http.Client{Timeout: 8 * time.Second}
	call := func(method string, params []any, out any) error {
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, readRPCURL, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if err != nil {
			return err
		}
		var env struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return err
		}
		if env.Error != nil {
			return fmt.Errorf("rpc %s error %d: %s", method, env.Error.Code, env.Error.Message)
		}
		return json.Unmarshal(env.Result, out)
	}

	var blk struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := call("eth_getBlockByNumber", []any{"latest", false}, &blk); err != nil {
		return QueryFirstEvidence{}, err
	}
	blkNum, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(blk.Number), "0x"), 16, 64)
	if err != nil {
		return QueryFirstEvidence{}, err
	}

	var txObj json.RawMessage
	if err := call("eth_getTransactionByHash", []any{txHash.Hex()}, &txObj); err != nil {
		return QueryFirstEvidence{}, err
	}
	txFound := len(txObj) > 0 && string(bytes.TrimSpace(txObj)) != "null"

	var rcptObj json.RawMessage
	if err := call("eth_getTransactionReceipt", []any{txHash.Hex()}, &rcptObj); err != nil {
		return QueryFirstEvidence{}, err
	}
	rcptFound := len(rcptObj) > 0 && string(bytes.TrimSpace(rcptObj)) != "null"

	var nonceHex string
	if err := call("eth_getTransactionCount", []any{wallet.Hex(), "latest"}, &nonceHex); err != nil {
		return QueryFirstEvidence{}, err
	}
	onChainNonce, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(nonceHex), "0x"), 16, 64)
	if err != nil {
		return QueryFirstEvidence{}, err
	}

	return QueryFirstEvidence{
		TxFoundOnChain:      txFound,
		ReceiptFoundOnChain: rcptFound,
		WalletOnChainNonce:  onChainNonce,
		AttemptNonce:        attemptNonce,
		QueriedBlockNumber:  blkNum,
		QueriedBlockHash:    common.HexToHash(blk.Hash),
	}, nil
}
