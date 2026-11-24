package evm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

var (
	// ErrRPCStateLagging indicates the RPC node returned a block older than the wallet's
	// latest canonical inclusion block (retryable 503, never consumes Idempotency-Key).
	ErrRPCStateLagging = errors.New("rpc state lagging behind wallet canonical block")
)

// BatchSnapshot holds the atomically-routed Multicall3 output plus latest/pending nonces
// fetched in a single HTTP JSON-RPC Batch request so load-balanced proxies hit one backend node.
type BatchSnapshot struct {
	BlockNumber   uint64
	MulticallData []byte
	LatestNonce   uint64
	PendingNonce  uint64
}

type BatchRPCClient struct {
	endpoint string
	http     *http.Client
}

func NewBatchRPCClient(endpoint string, timeout time.Duration) *BatchRPCClient {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DisableKeepAlives:     false,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	return &BatchRPCClient{
		endpoint: endpoint,
		http:     &http.Client{Transport: transport, Timeout: timeout},
	}
}

// FetchMulticallAndNonces sends [eth_blockNumber, eth_call(multicall3, latest),
// eth_getTransactionCount(wallet, latest), eth_getTransactionCount(wallet, pending)]
// in ONE HTTP JSON-RPC Batch request, and validates snapshotBlock >= requiredCanonicalBlock.
func (c *BatchRPCClient) FetchMulticallAndNonces(
	ctx context.Context,
	multicall3 common.Address,
	calldata []byte,
	wallet common.Address,
	requiredCanonicalBlock uint64,
) (BatchSnapshot, error) {
	if c == nil || c.endpoint == "" {
		return BatchSnapshot{}, errors.New("batch rpc client not configured")
	}
	batchReq := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "eth_blockNumber", "params": []any{}},
		{"jsonrpc": "2.0", "id": 2, "method": "eth_call", "params": []any{
			map[string]any{"to": multicall3.Hex(), "data": "0x" + common.Bytes2Hex(calldata)},
			"latest",
		}},
		{"jsonrpc": "2.0", "id": 3, "method": "eth_getTransactionCount", "params": []any{wallet.Hex(), "latest"}},
		{"jsonrpc": "2.0", "id": 4, "method": "eth_getTransactionCount", "params": []any{wallet.Hex(), "pending"}},
	}
	payload, err := json.Marshal(batchReq)
	if err != nil {
		return BatchSnapshot{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return BatchSnapshot{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return BatchSnapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return BatchSnapshot{}, fmt.Errorf("batch rpc http status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return BatchSnapshot{}, err
	}
	var items []struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  any             `json:"error"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return BatchSnapshot{}, fmt.Errorf("decode batch rpc response: %w", err)
	}
	byID := make(map[int]json.RawMessage, len(items))
	for _, it := range items {
		if it.Error != nil {
			return BatchSnapshot{}, fmt.Errorf("batch rpc item %d error: %v", it.ID, it.Error)
		}
		byID[it.ID] = it.Result
	}
	blockNum, err := parseHexUint64(byID[1])
	if err != nil {
		return BatchSnapshot{}, fmt.Errorf("parse blockNumber: %w", err)
	}
	if err := CheckRequiredBlock(blockNum, requiredCanonicalBlock); err != nil {
		return BatchSnapshot{}, err
	}
	var callHex string
	if err := json.Unmarshal(byID[2], &callHex); err != nil {
		return BatchSnapshot{}, fmt.Errorf("parse multicall result: %w", err)
	}
	latestNonce, err := parseHexUint64(byID[3])
	if err != nil {
		return BatchSnapshot{}, fmt.Errorf("parse latest nonce: %w", err)
	}
	pendingNonce, err := parseHexUint64(byID[4])
	if err != nil {
		return BatchSnapshot{}, fmt.Errorf("parse pending nonce: %w", err)
	}
	return BatchSnapshot{
		BlockNumber:   blockNum,
		MulticallData: common.FromHex(callHex),
		LatestNonce:   latestNonce,
		PendingNonce:  pendingNonce,
	}, nil
}

// CheckRequiredBlock validates snapshotBlock against the wallet's dynamic MAX(inclusion_block_number).
func CheckRequiredBlock(snapshotBlock, requiredCanonicalBlock uint64) error {
	if requiredCanonicalBlock > 0 && snapshotBlock < requiredCanonicalBlock {
		return fmt.Errorf("%w: snapshot=%d < required=%d", ErrRPCStateLagging, snapshotBlock, requiredCanonicalBlock)
	}
	return nil
}

func parseHexUint64(raw json.RawMessage) (uint64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
	if s == "" {
		return 0, errors.New("empty hex quantity")
	}
	return strconv.ParseUint(s, 16, 64)
}
