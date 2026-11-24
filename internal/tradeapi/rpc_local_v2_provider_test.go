package tradeapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
	pancakev2 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/pancakeswap/v2"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
	"github.com/ruzickahunzeker/rbh-bot/internal/registry"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestRPCLocalV2ProviderMulticallQuotesAndPositions(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.TradeOwner, filepath.Join(t.TempDir(), "trade-bsc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	walletAddr := common.HexToAddress("0x000000000000000000000000000000000000CAFE")
	tokenAddr := common.HexToAddress("0x000000000000000000000000000000000000BEEF")
	pairAddr := common.HexToAddress("0x000000000000000000000000000000000000FA12")
	currentBlockHex := "0x64" // block 100

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		// Batch JSON-RPC request (starts with '[').
		if len(raw) > 0 && raw[0] == '[' {
			aggOut, err := mc3ABI.Methods["aggregate3"].Outputs.Pack([]multicall3Result{
				{Success: true, ReturnData: common.LeftPadBytes(big.NewInt(5_000_000_000_000_000).Bytes(), 32)},
				{Success: true, ReturnData: common.LeftPadBytes(big.NewInt(950_000).Bytes(), 32)},
				{Success: true, ReturnData: common.LeftPadBytes([]byte{18}, 32)},
				{Success: true, ReturnData: common.LeftPadBytes(big.NewInt(100_000).Bytes(), 32)},
			})
			if err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"jsonrpc": "2.0", "id": 1, "result": currentBlockHex},
				{"jsonrpc": "2.0", "id": 2, "result": "0x" + common.Bytes2Hex(aggOut)},
				{"jsonrpc": "2.0", "id": 3, "result": "0x7"},
				{"jsonrpc": "2.0", "id": 4, "result": "0x7"},
			})
			return
		}
		var single struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(raw, &single)
		switch single.Method {
		case "eth_getBlockByNumber":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": 1,
				"result": map[string]any{"number": currentBlockHex, "hash": common.HexToHash("0x100").Hex()},
			})
		case "eth_getCode":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": "0x6080"})
		case "eth_call":
			var callObj struct {
				To   string `json:"to"`
				Data string `json:"data"`
			}
			_ = json.Unmarshal(single.Params[0], &callObj)
			to := common.HexToAddress(callObj.To)
			switch to {
			case bsc.PancakeV2Factory:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": 1,
					"result": "0x" + common.Bytes2Hex(common.LeftPadBytes(pairAddr.Bytes(), 32)),
				})
			case bsc.PancakeV2Router:
				// ABI-encoded uint256[] = [950000, 2000000000000000]
				outBytes, _ := abiUint256Slice([]*big.Int{big.NewInt(950_000), big.NewInt(2_000_000_000_000_000)})
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": 1,
					"result": "0x" + common.Bytes2Hex(outBytes),
				})
			case tokenAddr:
				// ERC20.allowance = 100_000 (< 950_000 -> NeedsApprove = true)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0", "id": 1,
					"result": "0x" + common.Bytes2Hex(common.LeftPadBytes(big.NewInt(100_000).Bytes(), 32)),
				})
			}
		}
	}))
	defer srv.Close()

	reg, err := registry.New(
		bsc.Profile(),
		[]ports.ProtocolAdapter{pancakev2.NewAdapter()},
		map[string]registry.TrustedSpender{pancakev2.ProtocolID: {Address: bsc.PancakeV2Router}},
	)
	if err != nil {
		t.Fatal(err)
	}
	prov := NewRPCLocalV2Provider(srv.URL, reg, db, walletAddr, 5*time.Second)

	// 1. QuotePreflight resolves PancakeV2 pair, quotes getAmountsOut, and detects allowance < amountIn -> NeedsApprove=true.
	qResp, err := prov.QuotePreflight(ctx, QuotePreflightRequest{
		Chain:       "bsc",
		WalletID:    "w-1",
		Protocol:    pancakev2.ProtocolID,
		Token:       tokenAddr.Hex(),
		Side:        "sell",
		AmountRaw:   "950000",
		SlippageBps: 100,
	}, bsc.PancakeV2Router)
	if err != nil {
		t.Fatalf("QuotePreflight failed: %v", err)
	}
	if !qResp.NeedsApprove || qResp.CurrentAllowance != "100000" || qResp.ExpectedOutRaw != "2000000000000000" {
		t.Fatalf("unexpected live RPC QuotePreflight: %+v", qResp)
	}

	// 2. ReadPosition unpacks Multicall3.aggregate3 at block 100; requiredBlock=101 returns ErrRPCStateLagging.
	if _, err := prov.ReadPosition(ctx, "w-1", walletAddr, tokenAddr, bsc.PancakeV2Router, 101); !errors.Is(err, evm.ErrRPCStateLagging) {
		t.Fatalf("expected ErrRPCStateLagging when node is at 100 < required 101, got %v", err)
	}
	pos, err := prov.ReadPosition(ctx, "w-1", walletAddr, tokenAddr, bsc.PancakeV2Router, 100)
	if err != nil {
		t.Fatalf("ReadPosition failed: %v", err)
	}
	if pos.TokenBalanceRaw != "950000" || pos.NativeBalanceRaw != "5000000000000000" || pos.AllowanceRaw != "100000" || pos.Decimals != 18 {
		t.Fatalf("unexpected ReadPosition snapshot: %+v", pos)
	}
}

func abiUint256Slice(vals []*big.Int) ([]byte, error) {
	t, err := abi.NewType("uint256[]", "", nil)
	if err != nil {
		return nil, err
	}
	args := abi.Arguments{{Type: t}}
	return args.Pack(vals)
}
