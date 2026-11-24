package feed

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestEVMReceiptRunnerV2V3SwapDecodeAndWatchlistFilter(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.FeedOwner, filepath.Join(t.TempDir(), "feed-bsc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := NewWatchlistStore(db)
	if err != nil {
		t.Fatal(err)
	}

	watchedTrader := common.HexToAddress("0x000000000000000000000000000000000000CAFE")
	noiseTrader := common.HexToAddress("0x0000000000000000000000000000000000009999")
	token := common.HexToAddress("0x000000000000000000000000000000000000BEEF")
	noiseToken := common.HexToAddress("0x0000000000000000000000000000000000008888")
	poolV2 := common.HexToAddress("0x000000000000000000000000000000000000FA12")
	poolV3 := common.HexToAddress("0x000000000000000000000000000000000000F003")
	wbnb := common.HexToAddress("0xbb4CdB9CBd36B01bD1cBaEBF2De08d9173bc095c")

	if err := ws.Replace(ctx, WatchlistSnapshot{
		Version: 1,
		Wallets: []string{watchedTrader.Hex()},
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_blockNumber":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": "0x2710"}) // block 10000
		case "eth_getBlockReceipts":
			// Receipt 1: watchedTrader splits buy across Pancake V2 + V3 pools in 1 tx.
			// Receipt 2: unrelated noiseTrader buys noiseToken (must be filtered out!).
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1,
				"result": []any{
					map[string]any{
						"transactionHash": common.HexToHash("0xaaa1").Hex(),
						"blockNumber":     "0x2710",
						"from":            watchedTrader.Hex(),
						"status":          "0x1",
						"logs": []any{
							map[string]any{
								"address":  token.Hex(),
								"topics":   []string{erc20TransferTopic.Hex(), common.BytesToHash(poolV2.Bytes()).Hex(), common.BytesToHash(watchedTrader.Bytes()).Hex()},
								"data":     "0x" + common.Bytes2Hex(common.LeftPadBytes(big.NewInt(400_000).Bytes(), 32)),
								"logIndex": "0x0",
							},
							map[string]any{
								"address":  poolV2.Hex(),
								"topics":   []string{v2SwapTopic.Hex(), common.BytesToHash(watchedTrader.Bytes()).Hex(), common.BytesToHash(watchedTrader.Bytes()).Hex()},
								"data":     "0x" + common.Bytes2Hex(make([]byte, 128)),
								"logIndex": "0x1",
							},
							map[string]any{
								"address":  token.Hex(),
								"topics":   []string{erc20TransferTopic.Hex(), common.BytesToHash(poolV3.Bytes()).Hex(), common.BytesToHash(watchedTrader.Bytes()).Hex()},
								"data":     "0x" + common.Bytes2Hex(common.LeftPadBytes(big.NewInt(550_000).Bytes(), 32)),
								"logIndex": "0x2",
							},
							map[string]any{
								"address":  poolV3.Hex(),
								"topics":   []string{v3SwapTopic.Hex(), common.BytesToHash(watchedTrader.Bytes()).Hex(), common.BytesToHash(watchedTrader.Bytes()).Hex()},
								"data":     "0x" + common.Bytes2Hex(make([]byte, 160)),
								"logIndex": "0x3",
							},
						},
					},
					map[string]any{
						"transactionHash": common.HexToHash("0xbbb2").Hex(),
						"blockNumber":     "0x2710",
						"from":            noiseTrader.Hex(),
						"status":          "0x1",
						"logs": []any{
							map[string]any{
								"address":  noiseToken.Hex(),
								"topics":   []string{erc20TransferTopic.Hex(), common.BytesToHash(poolV2.Bytes()).Hex(), common.BytesToHash(noiseTrader.Bytes()).Hex()},
								"data":     "0x" + common.Bytes2Hex(common.LeftPadBytes(big.NewInt(999_999).Bytes(), 32)),
								"logIndex": "0x4",
							},
							map[string]any{
								"address":  poolV2.Hex(),
								"topics":   []string{v2SwapTopic.Hex(), common.BytesToHash(noiseTrader.Bytes()).Hex(), common.BytesToHash(noiseTrader.Bytes()).Hex()},
								"data":     "0x" + common.Bytes2Hex(make([]byte, 128)),
								"logIndex": "0x5",
							},
						},
					},
				},
			})
		}
	}))
	defer srv.Close()

	runner, err := NewEVMReceiptRunner(56, wbnb, srv.URL, store, ws, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err := runner.PollOnce(ctx, time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("PollOnce failed: %v", err)
	}
	// Only watchedTrader's 2 swap legs (V2 + V3) are inserted; noiseTrader is filtered out!
	if res.InsertedObservations != 2 || !runner.Ready(ctx) {
		t.Fatalf("expected 2 inserted observations (noise filtered) and runner Ready=true, got %+v", res)
	}
	items, err := store.ReadOutboxAfter(ctx, 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("ReadOutboxAfter failed: items=%d err=%v", len(items), err)
	}
	var p1, p2 NormalizedSwapPayload
	_ = json.Unmarshal(items[0].PayloadJSON, &p1)
	_ = json.Unmarshal(items[1].PayloadJSON, &p2)
	// Both legs share the exact same net TriggerID so bot-service collapses them to 1 copy-trade!
	if p1.TriggerID == "" || p1.TriggerID != p2.TriggerID || p1.TokenDeltaRaw != "950000" {
		t.Fatalf("unexpected swap payloads: p1=%+v p2=%+v", p1, p2)
	}
}

func TestEVMReceiptRunnerBSCLaunchpadsDecode(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.FeedOwner, filepath.Join(t.TempDir(), "feed-launchpads.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := NewWatchlistStore(db)
	if err != nil {
		t.Fatal(err)
	}

	trader := common.HexToAddress("0x000000000000000000000000000000000000CAFE")
	fourTok := common.HexToAddress("0x0000000000000000000000000000000000004444")
	flapTok := common.HexToAddress("0x0000000000000000000000000000000000007777")
	fourMgr := common.HexToAddress("0x5c952063c7fc8610FFDB798152D69F0B9550762b")
	flapPortal := common.HexToAddress("0xe2cE6ab80874Fa9Fa2aAE65D277Dd6B8e65C9De0")
	wbnb := common.HexToAddress("0xbb4CdB9CBd36B01bD1cBaEBF2De08d9173bc095c")

	if err := ws.Replace(ctx, WatchlistSnapshot{Version: 1, Wallets: []string{trader.Hex()}}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_blockNumber":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": "0x2711"})
		case "eth_getBlockReceipts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1,
				"result": []any{
					map[string]any{
						"transactionHash": common.HexToHash("0x4444").Hex(),
						"blockNumber":     "0x2711",
						"from":            trader.Hex(),
						"status":          "0x1",
						"logs": []any{
							map[string]any{
								"address":  fourTok.Hex(),
								"topics":   []string{erc20TransferTopic.Hex(), common.BytesToHash(fourMgr.Bytes()).Hex(), common.BytesToHash(trader.Bytes()).Hex()},
								"data":     "0x" + common.Bytes2Hex(common.LeftPadBytes(big.NewInt(1_000_000).Bytes(), 32)),
								"logIndex": "0x0",
							},
							map[string]any{
								"address":  fourMgr.Hex(),
								"topics":   []string{fourMemePurchaseTopic.Hex()},
								"data":     "0x" + common.Bytes2Hex(make([]byte, 8*32)),
								"logIndex": "0x1",
							},
						},
					},
					map[string]any{
						"transactionHash": common.HexToHash("0x7777").Hex(),
						"blockNumber":     "0x2711",
						"from":            trader.Hex(),
						"status":          "0x1",
						"logs": []any{
							map[string]any{
								"address":  flapTok.Hex(),
								"topics":   []string{erc20TransferTopic.Hex(), common.BytesToHash(flapPortal.Bytes()).Hex(), common.BytesToHash(trader.Bytes()).Hex()},
								"data":     "0x" + common.Bytes2Hex(common.LeftPadBytes(big.NewInt(2_500_000).Bytes(), 32)),
								"logIndex": "0x2",
							},
							map[string]any{
								"address":  flapPortal.Hex(),
								"topics":   []string{flapBoughtTopic.Hex()},
								"data":     "0x" + common.Bytes2Hex(make([]byte, 7*32)),
								"logIndex": "0x3",
							},
						},
					},
				},
			})
		}
	}))
	defer srv.Close()

	runner, err := NewEVMReceiptRunner(56, wbnb, srv.URL, store, ws, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err := runner.PollOnce(ctx, time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if res.InsertedObservations != 2 {
		t.Fatalf("expected 2 launchpad observations, got %+v", res)
	}
	items, err := store.ReadOutboxAfter(ctx, 0, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("ReadOutboxAfter: len=%d err=%v", len(items), err)
	}
	var pFour, pFlap NormalizedSwapPayload
	_ = json.Unmarshal(items[0].PayloadJSON, &pFour)
	_ = json.Unmarshal(items[1].PayloadJSON, &pFlap)
	if pFour.PoolVersion != "four-meme" || pFour.Side != "buy" || pFour.TokenDeltaRaw != "1000000" {
		t.Fatalf("unexpected four-meme payload: %+v", pFour)
	}
	if pFlap.PoolVersion != "flap" || pFlap.Side != "buy" || pFlap.TokenDeltaRaw != "2500000" {
		t.Fatalf("unexpected flap payload: %+v", pFlap)
	}
}
