package honeypot

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

func TestSequentialBuyApproveSellHoneypotAndTaxSimulation(t *testing.T) {
	wallet := common.HexToAddress("0x000000000000000000000000000000000000CAFE")
	router := common.HexToAddress("0x10ED43C718714eb63d5aA57B78B54704E256024E")
	token := common.HexToAddress("0x000000000000000000000000000000000000BEEF")
	wbnb := common.HexToAddress("0xbb4CdB9CBd36B01bD1cBaEBF2De08d9173bc095c")

	mode := "safe"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sellStatus := "0x1"
		recovered := big.NewInt(920_000_000_000_000) // 8% roundtrip loss
		switch mode {
		case "honeypot":
			sellStatus = "0x0"
		case "high_tax":
			recovered = big.NewInt(600_000_000_000_000) // 40% roundtrip loss
		}
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": []any{
				map[string]any{
					"calls": []any{
						map[string]any{
							"status": "0x1",
							"logs": []any{
								map[string]any{
									"address": token.Hex(),
									"topics":  []string{erc20TransferTopic.Hex(), common.BytesToHash(router.Bytes()).Hex(), common.BytesToHash(wallet.Bytes()).Hex()},
									"data":    "0x" + common.Bytes2Hex(common.LeftPadBytes(big.NewInt(1_000_000).Bytes(), 32)),
								},
							},
						},
						map[string]any{"status": "0x1", "logs": []any{}},
						map[string]any{
							"status": sellStatus,
							"logs": []any{
								map[string]any{
									"address": wbnb.Hex(),
									"topics":  []string{wbnbWithdrawalTopic.Hex(), common.BytesToHash(router.Bytes()).Hex()},
									"data":    "0x" + common.Bytes2Hex(common.LeftPadBytes(recovered.Bytes(), 32)),
								},
							},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	sim := NewSimulator(srv.URL, 5*time.Second)
	req := SimulationRequest{
		ProbeWallet:         wallet,
		Router:              router,
		Token:               token,
		WrappedNative:       wbnb,
		ProbeNativeWei:      big.NewInt(1_000_000_000_000_000),
		BuyCalldata:         []byte{0xb6, 0xf9, 0xde, 0x95},
		ApproveCalldata:     []byte{0x09, 0x5e, 0xa7, 0xb3},
		SellCalldata:        []byte{0x79, 0x1a, 0xc9, 0x47},
		MaxRoundtripLossBps: 1500, // 15%
	}

	// 1. Safe token (8% roundtrip loss <= 15%).
	res, err := sim.SimulateBuyApproveSell(context.Background(), req)
	if err != nil || !res.Safe || res.Verdict != VerdictSafe || res.RoundtripLossBps != 800 {
		t.Fatalf("expected SAFE 800bps, got %+v err=%v", res, err)
	}

	// 2. Honeypot token (sell leg reverts).
	mode = "honeypot"
	res, err = sim.SimulateBuyApproveSell(context.Background(), req)
	if err != nil || res.Safe || res.Verdict != VerdictSellReverted {
		t.Fatalf("expected SELL_REVERTED honeypot, got %+v err=%v", res, err)
	}

	// 3. Excessive tax token (40% roundtrip loss > 15%).
	mode = "high_tax"
	res, err = sim.SimulateBuyApproveSell(context.Background(), req)
	if err != nil || res.Safe || res.Verdict != VerdictExcessiveTax || res.RoundtripLossBps != 4000 {
		t.Fatalf("expected EXCESSIVE_TAX 4000bps, got %+v err=%v", res, err)
	}
}
