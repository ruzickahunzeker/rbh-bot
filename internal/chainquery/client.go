// Package chainquery exposes fixed read-only Robinhood queries. It has no SDK
// transaction client, signer, nonce allocator, arbitrary RPC or send method.
package chainquery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

var (
	ErrUnavailable = errors.New("read-only chain query unavailable")
	ErrObservation = errors.New("unverifiable chain observation")
	ErrUnsupported = errors.New("unsupported token route")
)

const ChainID = 4663
const QueryTimeout = 10 * time.Second

type Client struct {
	endpoint        string
	http            *http.Client
	next            atomic.Uint64
	factoryCodeHash common.Hash
	curveCodeHash   common.Hash
}

// New performs no network I/O. Only the deployment supplies an endpoint;
// HTTP users cannot choose provider URLs, RPC methods or call payloads.
func New(endpoint string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Fragment != "" || u.User != nil {
		return nil, ErrUnavailable
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DisableKeepAlives: true, ResponseHeaderTimeout: QueryTimeout}
	return &Client{endpoint: endpoint, http: &http.Client{Transport: transport, Timeout: QueryTimeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, factoryCodeHash: common.HexToHash(FactoryCodeHash), curveCodeHash: common.HexToHash(CurveCodeHash)}, nil
}

// rpc is private and has an explicit allowlist independent of call sites.
func (c *Client) rpc(ctx context.Context, method string, params any, out any) error {
	if c == nil || ctx == nil {
		return ErrUnavailable
	}
	switch method {
	case "eth_chainId", "eth_getBlockByNumber", "eth_getCode", "eth_call", "eth_getBalance", "eth_maxPriorityFeePerGas":
	default:
		return ErrObservation
	}
	id := c.next.Add(1)
	if id == 0 {
		return ErrObservation
	}
	body, _ := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      uint64 `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", id, method, params})
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return ErrUnavailable
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(r)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return ErrUnavailable
	}
	// Reject duplicate security fields instead of last-key-wins parsing.
	fields, err := object(data)
	if err != nil {
		return ErrObservation
	}
	var version string
	var returnedID uint64
	if json.Unmarshal(fields["jsonrpc"], &version) != nil || version != "2.0" || json.Unmarshal(fields["id"], &returnedID) != nil || returnedID != id {
		return ErrObservation
	}
	result, hasResult := fields["result"]
	_, hasError := fields["error"]
	if hasError {
		if hasResult {
			return ErrObservation
		}
		return ErrUnavailable
	}
	if !hasResult || bytes.Equal(bytes.TrimSpace(result), []byte("null")) || json.Unmarshal(result, out) != nil {
		return ErrObservation
	}
	return nil
}

func object(data []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil, ErrObservation
	}
	fields := map[string]json.RawMessage{}
	caseKeys := map[string]bool{}
	for d.More() {
		t, err = d.Token()
		if err != nil {
			return nil, ErrObservation
		}
		key, ok := t.(string)
		if !ok {
			return nil, ErrObservation
		}
		if _, exists := fields[key]; exists {
			return nil, ErrObservation
		}
		folded := strings.ToLower(key)
		if caseKeys[folded] {
			return nil, ErrObservation
		}
		caseKeys[folded] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return nil, ErrObservation
		}
		fields[key] = raw
	}
	if t, err = d.Token(); err != nil || t != json.Delim('}') {
		return nil, ErrObservation
	}
	if _, err = d.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrObservation
	}
	return fields, nil
}

func quantity(value string) (*big.Int, error) {
	if !strings.HasPrefix(value, "0x") || len(value) > 66 {
		return nil, ErrObservation
	}
	x, err := hexutil.DecodeBig(value)
	if err != nil || x.Sign() < 0 || x.BitLen() > 256 || hexutil.EncodeBig(x) != value {
		return nil, ErrObservation
	}
	return x, nil
}

type Block struct {
	Number uint64 `json:"number"`
	Hash   string `json:"hash"`
}

type snapshot struct {
	block Block
	base  *big.Int
}

func (c *Client) chain(ctx context.Context) error {
	var value string
	if err := c.rpc(ctx, "eth_chainId", []any{}, &value); err != nil {
		return err
	}
	x, err := quantity(value)
	if err != nil || x.Cmp(big.NewInt(ChainID)) != 0 {
		return ErrObservation
	}
	return nil
}

func (c *Client) header(ctx context.Context, number string) (snapshot, error) {
	var raw json.RawMessage
	if err := c.rpc(ctx, "eth_getBlockByNumber", []any{number, false}, &raw); err != nil {
		return snapshot{}, err
	}
	fields, err := object(raw)
	if err != nil {
		return snapshot{}, err
	}
	for _, key := range []string{"number", "timestamp", "baseFeePerGas", "hash"} {
		if _, ok := fields[key]; !ok {
			return snapshot{}, ErrObservation
		}
	}
	var h types.Header
	var claimed string
	if json.Unmarshal(raw, &h) != nil || h.Number == nil || !h.Number.IsUint64() || h.Number.Sign() <= 0 || h.Time == 0 || h.BaseFee == nil || h.BaseFee.Sign() < 0 || h.BaseFee.BitLen() > 256 || json.Unmarshal(fields["hash"], &claimed) != nil || claimed != h.Hash().Hex() {
		return snapshot{}, ErrObservation
	}
	if number != "latest" && hexutil.EncodeBig(h.Number) != number {
		return snapshot{}, ErrObservation
	}
	return snapshot{block: Block{Number: h.Number.Uint64(), Hash: claimed}, base: new(big.Int).Set(h.BaseFee)}, nil
}

func (c *Client) begin(ctx context.Context) (snapshot, error) {
	if err := c.chain(ctx); err != nil {
		return snapshot{}, err
	}
	return c.header(ctx, "latest")
}

func (c *Client) finish(ctx context.Context, s snapshot) error {
	if err := c.chain(ctx); err != nil {
		return err
	}
	h, err := c.header(ctx, hexutil.EncodeUint64(s.block.Number))
	if err != nil {
		return err
	}
	if h.block != s.block {
		return ErrObservation
	}
	return nil
}

func blockParam(s snapshot) any {
	return map[string]any{"blockHash": s.block.Hash, "requireCanonical": true}
}

func (c *Client) code(ctx context.Context, address common.Address, s snapshot) ([]byte, error) {
	var value string
	if err := c.rpc(ctx, "eth_getCode", []any{strings.ToLower(address.Hex()), blockParam(s)}, &value); err != nil {
		return nil, err
	}
	code, err := hexutil.Decode(value)
	if err != nil || len(code) == 0 {
		return nil, ErrObservation
	}
	return code, nil
}

func (c *Client) call(ctx context.Context, to common.Address, data []byte, s snapshot) ([]byte, error) {
	var value string
	if err := c.rpc(ctx, "eth_call", []any{map[string]any{"to": strings.ToLower(to.Hex()), "data": hexutil.Encode(data)}, blockParam(s)}, &value); err != nil {
		return nil, err
	}
	result, err := hexutil.Decode(value)
	if err != nil {
		return nil, ErrObservation
	}
	return result, nil
}

func Address(value string) (common.Address, error) {
	if len(value) != 42 || !strings.HasPrefix(value, "0x") || !common.IsHexAddress(value) || common.HexToAddress(value) == (common.Address{}) {
		return common.Address{}, ErrUnsupported
	}
	return common.HexToAddress(value), nil
}
