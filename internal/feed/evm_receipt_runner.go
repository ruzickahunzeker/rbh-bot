package feed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	// V2 Swap(address indexed sender, uint256 amount0In, uint256 amount1In, uint256 amount0Out, uint256 amount1Out, address indexed to)
	v2SwapTopic = crypto.Keccak256Hash([]byte("Swap(address,uint256,uint256,uint256,uint256,address)"))
	// V3 Swap(address indexed sender, address indexed recipient, int256 amount0, int256 amount1, uint160 sqrtPriceX96, uint128 liquidity, int24 tick)
	v3SwapTopic = crypto.Keccak256Hash([]byte("Swap(address,address,int256,int256,uint160,uint128,int24)"))
	// V4 PoolManager Swap(bytes32 indexed id, address indexed sender, int128 amount0, int128 amount1, uint160 sqrtPriceX96, uint128 liquidity, int24 tick, uint24 fee)
	v4SwapTopic = crypto.Keccak256Hash([]byte("Swap(bytes32,address,int128,int128,uint160,uint128,int24,uint24)"))
	// Four.meme V2 TokenPurchase / TokenSale
	fourMemePurchaseTopic = crypto.Keccak256Hash([]byte("TokenPurchase(address,address,uint256,uint256,uint256,uint256,uint256,uint256)"))
	fourMemeSaleTopic     = crypto.Keccak256Hash([]byte("TokenSale(address,address,uint256,uint256,uint256,uint256,uint256,uint256)"))
	// Flap.sh Portal TokenBought / TokenSold
	flapBoughtTopic = crypto.Keccak256Hash([]byte("TokenBought(uint256,address,address,uint256,uint256,uint256,uint256)"))
	flapSoldTopic   = crypto.Keccak256Hash([]byte("TokenSold(uint256,address,address,uint256,uint256,uint256,uint256)"))
	// Sniper events: PancakeV2 PairCreated & Four.meme LiquidityAdded (graduation migration)
	pairCreatedTopic    = crypto.Keccak256Hash([]byte("PairCreated(address,address,address,uint256)"))
	liquidityAddedTopic = crypto.Keccak256Hash([]byte("LiquidityAdded(address,uint256,address,uint256)"))
	// ERC-20 Transfer(address indexed from, address indexed to, uint256 value)
	erc20TransferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
)

// NormalizedSwapPayload is the JSON payload stored in feed_events/feed_outbox for EVM V2/V3 swaps.
type NormalizedSwapPayload struct {
	ChainID       uint64 `json:"chain_id"`
	BlockNumber   uint64 `json:"block_number"`
	TxHash        string `json:"tx_hash"`
	PoolAddress   string `json:"pool_address"`
	PoolVersion   string `json:"pool_version"` // "v2" | "v3"
	Trader        string `json:"trader"`
	Token         string `json:"token"`
	Side          string `json:"side"` // "buy" | "sell"
	TokenDeltaRaw string `json:"token_delta_raw"`
	TriggerID     string `json:"trigger_id"`
	Stage         string `json:"stage"` // "included"
}

// EVMReceiptRunner polls standard EVM JSON-RPC (eth_blockNumber + eth_getBlockReceipts)
// for chains like BSC (56) and Base (8453), decodes V2/V3 Swap receipts, filters by the
// persistent WatchlistStore, and commits matched observations to feed.db Outbox.
type EVMReceiptRunner struct {
	chainID       uint64
	wrappedNative common.Address
	endpoint      string
	store         *Store
	watchlist     *WatchlistStore
	http          *http.Client
	healthy       atomic.Bool
}

func NewEVMReceiptRunner(chainID uint64, wrappedNative common.Address, endpoint string, store *Store, watchlist *WatchlistStore, timeout time.Duration) (*EVMReceiptRunner, error) {
	if chainID == 0 || wrappedNative == (common.Address{}) || strings.TrimSpace(endpoint) == "" || store == nil {
		return nil, errors.New("invalid EVM receipt runner configuration")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &EVMReceiptRunner{
		chainID:       chainID,
		wrappedNative: wrappedNative,
		endpoint:      endpoint,
		store:         store,
		watchlist:     watchlist,
		http: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DisableKeepAlives:     false,
				MaxIdleConns:          16,
				MaxIdleConnsPerHost:   8,
				IdleConnTimeout:       60 * time.Second,
				ResponseHeaderTimeout: timeout,
			},
			Timeout: timeout,
		},
	}, nil
}

func (r *EVMReceiptRunner) Ready(ctx context.Context) bool {
	if r == nil || r.store == nil || !r.healthy.Load() {
		return false
	}
	p, err := r.store.Progress(ctx)
	return err == nil && !p.Degraded
}

type rpcReceipt struct {
	TransactionHash string `json:"transactionHash"`
	BlockNumber     string `json:"blockNumber"`
	From            string `json:"from"`
	Status          string `json:"status"`
	Logs            []struct {
		Address  string   `json:"address"`
		Topics   []string `json:"topics"`
		Data     string   `json:"data"`
		LogIndex string   `json:"logIndex"`
	} `json:"logs"`
}

func (r *EVMReceiptRunner) rpcCall(ctx context.Context, method string, params []any, out any) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("rpc %s status %d", method, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
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

// PollOnce fetches the latest block receipts (or next block after progress.ObservedSequence),
// decodes V2/V3 swaps, applies WatchlistStore filtering, and commits to feed.db.
func (r *EVMReceiptRunner) PollOnce(ctx context.Context, now time.Time) (CommitResult, error) {
	if r == nil || r.store == nil {
		return CommitResult{}, errors.New("evm receipt runner unavailable")
	}
	var headHex string
	if err := r.rpcCall(ctx, "eth_blockNumber", []any{}, &headHex); err != nil {
		r.healthy.Store(false)
		return CommitResult{}, err
	}
	headNum, err := parseHexQuantity(headHex)
	if err != nil {
		r.healthy.Store(false)
		return CommitResult{}, err
	}
	prog, err := r.store.Progress(ctx)
	if err != nil {
		return CommitResult{}, err
	}
	targetBlock := headNum
	if prog.ObservedSequence != nil {
		if *prog.ObservedSequence >= headNum {
			r.healthy.Store(true)
			return CommitResult{DurableEventOffset: prog.DurableEventOffset}, nil
		}
		targetBlock = *prog.ObservedSequence + 1
	}

	var receipts []rpcReceipt
	blockTag := fmt.Sprintf("0x%x", targetBlock)
	if err := r.rpcCall(ctx, "eth_getBlockReceipts", []any{blockTag}, &receipts); err != nil {
		r.healthy.Store(false)
		return CommitResult{}, err
	}
	obs := r.extractBlockObservations(targetBlock, receipts, now.UTC())
	res, err := r.store.CommitSequencer(ctx, obs, targetBlock)
	if err != nil {
		r.healthy.Store(false)
		return CommitResult{}, err
	}
	r.healthy.Store(true)
	return res, nil
}

func (r *EVMReceiptRunner) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_, _ = r.PollOnce(ctx, time.Now().UTC())
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *EVMReceiptRunner) extractBlockObservations(blockNum uint64, receipts []rpcReceipt, observedAt time.Time) []Observation {
	var out []Observation
	for _, rcpt := range receipts {
		if !strings.EqualFold(strings.TrimSpace(rcpt.Status), "0x1") || !common.IsHexAddress(rcpt.From) {
			continue
		}
		txHash := common.HexToHash(rcpt.TransactionHash)
		if txHash == (common.Hash{}) {
			continue
		}
		trader := common.HexToAddress(rcpt.From)
		traderHex := strings.ToLower(trader.Hex())

		// 1. Decode Sniper launch/migration logs (PairCreated / LiquidityAdded) before ERC-20 transfer filter,
		// because launch/migration receipts may not transfer tokens to tx.From.
		for idx, lg := range rcpt.Logs {
			if len(lg.Topics) == 0 {
				continue
			}
			top0 := common.HexToHash(lg.Topics[0])
			var (
				sniperToken common.Address
				sniperPool  string
				sniperVer   string
				sniperSide  string
			)
			switch top0 {
			case pairCreatedTopic:
				if len(lg.Topics) >= 3 {
					t0 := common.HexToAddress(lg.Topics[1])
					t1 := common.HexToAddress(lg.Topics[2])
					if t0 == r.wrappedNative && t1 != (common.Address{}) {
						sniperToken = t1
					} else if t1 == r.wrappedNative && t0 != (common.Address{}) {
						sniperToken = t0
					}
					rawData := common.FromHex(lg.Data)
					if len(rawData) >= 32 {
						sniperPool = strings.ToLower(common.BytesToAddress(rawData[:32]).Hex())
					} else {
						sniperPool = strings.ToLower(common.HexToAddress(lg.Address).Hex())
					}
					sniperVer = "sniper-launch"
					sniperSide = "launch"
				}
			case liquidityAddedTopic:
				rawData := common.FromHex(lg.Data)
				if len(rawData) >= 32 {
					sniperToken = common.BytesToAddress(rawData[:32])
					sniperPool = strings.ToLower(common.HexToAddress(lg.Address).Hex())
					sniperVer = "sniper-migration"
					sniperSide = "migration"
				}
			}
			if sniperToken == (common.Address{}) {
				continue
			}
			tokHex := strings.ToLower(sniperToken.Hex())
			if r.watchlist != nil && r.watchlist.Initialized() && !r.watchlist.Matches("", tokHex) {
				continue
			}
			logIdx := parseOptHexIndex(lg.LogIndex, idx)
			payloadObj := NormalizedSwapPayload{
				ChainID:       r.chainID,
				BlockNumber:   blockNum,
				TxHash:        strings.ToLower(txHash.Hex()),
				PoolAddress:   sniperPool,
				PoolVersion:   sniperVer,
				Trader:        traderHex,
				Token:         tokHex,
				Side:          sniperSide,
				TokenDeltaRaw: "1",
				TriggerID:     ComputeTriggerID(r.chainID, txHash.Hex(), sniperPool, tokHex, sniperSide),
				Stage:         "included",
			}
			if rawPayload, err := json.Marshal(payloadObj); err == nil {
				obs := Observation{
					ChainID:          r.chainID,
					Source:           SourceRPC,
					SourceSequence:   blockNum,
					TransactionHash:  txHash,
					StableActionPath: fmt.Sprintf("receipt/log/%06d/%s/%s", logIdx, sniperVer, sniperSide),
					PayloadJSON:      rawPayload,
					ObservedAt:       observedAt,
				}
				if obs.Validate() == nil {
					out = append(out, obs)
				}
			}
		}

		// Compute net ERC-20 token delta per non-WrappedNative token for trader in this receipt.
		tokenDelta := make(map[common.Address]*big.Int)
		var tokenOrder []common.Address
		for _, lg := range rcpt.Logs {
			if len(lg.Topics) != 3 || common.HexToHash(lg.Topics[0]) != erc20TransferTopic {
				continue
			}
			tok := common.HexToAddress(lg.Address)
			if tok == (common.Address{}) || tok == r.wrappedNative {
				continue
			}
			from := common.HexToAddress(lg.Topics[1])
			to := common.HexToAddress(lg.Topics[2])
			amt := new(big.Int).SetBytes(common.FromHex(lg.Data))
			if amt.Sign() <= 0 {
				continue
			}
			if from != trader && to != trader {
				continue
			}
			cur, exists := tokenDelta[tok]
			if !exists {
				cur = big.NewInt(0)
				tokenDelta[tok] = cur
				tokenOrder = append(tokenOrder, tok)
			}
			if to == trader {
				cur.Add(cur, amt)
			}
			if from == trader {
				cur.Sub(cur, amt)
			}
		}
		if len(tokenOrder) == 0 {
			continue
		}

		// Emit an observation for each V2/V3 Swap log in the receipt, associating it with the trader's net token action.
		for idx, lg := range rcpt.Logs {
			if len(lg.Topics) == 0 {
				continue
			}
			top0 := common.HexToHash(lg.Topics[0])
			var poolVer string
			switch top0 {
			case v2SwapTopic:
				poolVer = "v2"
			case v3SwapTopic:
				poolVer = "v3"
			case v4SwapTopic:
				poolVer = "v4"
			case fourMemePurchaseTopic, fourMemeSaleTopic:
				poolVer = "four-meme"
			case flapBoughtTopic, flapSoldTopic:
				poolVer = "flap"
			default:
				continue
			}
			poolAddr := strings.ToLower(common.HexToAddress(lg.Address).Hex())
			tok := tokenOrder[0]
			delta := tokenDelta[tok]
			if delta == nil || delta.Sign() == 0 {
				continue
			}
			side := "buy"
			absDelta := new(big.Int).Set(delta)
			if delta.Sign() < 0 {
				side = "sell"
				absDelta.Neg(absDelta)
			}
			tokHex := strings.ToLower(tok.Hex())

			// Filter unrelated swaps before SQLite outbox write if WatchlistStore is initialized.
			if r.watchlist != nil && r.watchlist.Initialized() && !r.watchlist.Matches(traderHex, tokHex) {
				continue
			}

			logIdx := parseOptHexIndex(lg.LogIndex, idx)
			payloadObj := NormalizedSwapPayload{
				ChainID:       r.chainID,
				BlockNumber:   blockNum,
				TxHash:        strings.ToLower(txHash.Hex()),
				PoolAddress:   poolAddr,
				PoolVersion:   poolVer,
				Trader:        traderHex,
				Token:         tokHex,
				Side:          side,
				TokenDeltaRaw: absDelta.String(),
				TriggerID:     ComputeTriggerID(r.chainID, txHash.Hex(), traderHex, tokHex, side),
				Stage:         "included",
			}
			rawPayload, err := json.Marshal(payloadObj)
			if err != nil {
				continue
			}
			obs := Observation{
				ChainID:          r.chainID,
				Source:           SourceRPC,
				SourceSequence:   blockNum,
				TransactionHash:  txHash,
				StableActionPath: fmt.Sprintf("receipt/log/%06d/dex-%s/swap/%s", logIdx, poolVer, side),
				PayloadJSON:      rawPayload,
				ObservedAt:       observedAt,
			}
			if obs.Validate() == nil {
				out = append(out, obs)
			}
		}
	}
	return out
}

func parseHexQuantity(s string) (uint64, error) {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
	if s == "" {
		return 0, errors.New("empty hex quantity")
	}
	return strconv.ParseUint(s, 16, 64)
}

func parseOptHexIndex(s string, fallback int) int {
	v, err := parseHexQuantity(s)
	if err != nil {
		return fallback
	}
	return int(v)
}
