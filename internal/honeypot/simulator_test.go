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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpcReq struct {
			Params []struct {
				BlockStateCalls []struct {
					Calls []map[string]any `json:"calls"`
				} `json:"blockStateCalls"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&rpcReq)
		numCalls := len(rpcReq.Params[0].BlockStateCalls[0].Calls)

		sellStatus := "0x1"
		sellGas := "0x30d40" // 200,000
		recovered := big.NewInt(920_000_000_000_000) // 8% roundtrip loss
		switch mode {
		case "honeypot":
			sellStatus = "0x0"
		case "high_tax":
			recovered = big.NewInt(600_000_000_000_000) // 40% roundtrip loss
		case "zero_output":
			recovered = big.NewInt(0) // 100% tax non-reverting honeypot
		case "gas_grief":
			sellGas = "0x1b7740" // 1,800,000 > 1,500,000 limit
		}
		buyCallRes := map[string]any{
			"status":  "0x1",
			"gasUsed": "0x249f0",
			"logs": []any{
				map[string]any{
					"address": token.Hex(),
					"topics":  []string{erc20TransferTopic.Hex(), common.BytesToHash(router.Bytes()).Hex(), common.BytesToHash(wallet.Bytes()).Hex()},
					"data":    "0x" + common.Bytes2Hex(common.LeftPadBytes(big.NewInt(950_000).Bytes(), 32)),
				},
			},
		}
		var callsOut []any
		if numCalls == 1 {
			callsOut = []any{buyCallRes}
		} else {
			var sellLogs []any
			if recovered.Sign() > 0 {
				sellLogs = []any{
					map[string]any{
						"address": wbnb.Hex(),
						"topics":  []string{wbnbWithdrawalTopic.Hex(), common.BytesToHash(router.Bytes()).Hex()},
						"data":    "0x" + common.Bytes2Hex(common.LeftPadBytes(recovered.Bytes(), 32)),
					},
				}
			}
			callsOut = []any{
				buyCallRes,
				map[string]any{"status": "0x1", "gasUsed": "0xc350", "logs": []any{}},
				map[string]any{"status": sellStatus, "gasUsed": sellGas, "logs": sellLogs},
			}
		}
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  []any{map[string]any{"calls": callsOut}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	sim := NewSimulator(srv.URL, 5*time.Second)
	var dynamicBought *big.Int
	req := SimulationRequest{
		ProbeWallet:     wallet,
		Router:          router,
		Token:           token,
		WrappedNative:   wbnb,
		ProbeNativeWei:  big.NewInt(1_000_000_000_000_000),
		BuyCalldata:     []byte{0xb6, 0xf9, 0xde, 0x95},
		ApproveCalldata: []byte{0x09, 0x5e, 0xa7, 0xb3},
		BuildSellCalldata: func(bought *big.Int) ([]byte, error) {
			dynamicBought = new(big.Int).Set(bought)
			return []byte{0x79, 0x1a, 0xc9, 0x47}, nil
		},
		MaxRoundtripLossBps: 1500,
	}

	// 1. Dynamic post-tax BuildSellCalldata receives exact 950_000 tokens and passes SAFE (800 bps).
	res, err := sim.SimulateBuyApproveSell(context.Background(), req)
	if err != nil || !res.Safe || res.Verdict != VerdictSafe || res.RoundtripLossBps != 800 || dynamicBought.Cmp(big.NewInt(950_000)) != 0 {
		t.Fatalf("expected SAFE 800bps with dynamic bought=950000, got %+v dynamic=%v err=%v", res, dynamicBought, err)
	}

	// 2. Honeypot token (sell leg reverts).
	mode = "honeypot"
	req.SellCalldata = []byte{0x79, 0x1a, 0xc9, 0x47}
	res, err = sim.SimulateBuyApproveSell(context.Background(), req)
	if err != nil || res.Safe || res.Verdict != VerdictSellReverted {
		t.Fatalf("expected SELL_REVERTED honeypot, got %+v err=%v", res, err)
	}

	// 3. Zero-output non-reverting honeypot MUST fail even when MaxRoundtripLossBps == 0!
	mode = "zero_output"
	reqZeroMax := req
	reqZeroMax.MaxRoundtripLossBps = 0
	res, err = sim.SimulateBuyApproveSell(context.Background(), reqZeroMax)
	if err != nil || res.Safe || res.Verdict != VerdictZeroNativeRecovered {
		t.Fatalf("expected ZERO_NATIVE_RECOVERED fail-closed, got %+v err=%v", res, err)
	}

	// 4. Gas-griefing token hook (> 1.5M gas) must fail closed.
	mode = "gas_grief"
	res, err = sim.SimulateBuyApproveSell(context.Background(), req)
	if err != nil || res.Safe || res.Verdict != VerdictGasGriefing {
		t.Fatalf("expected GAS_GRIEFING verdict, got %+v err=%v", res, err)
	}

	// 5. Excessive tax token (40% roundtrip loss > 15%).
	mode = "high_tax"
	res, err = sim.SimulateBuyApproveSell(context.Background(), req)
	if err != nil || res.Safe || res.Verdict != VerdictExcessiveTax || res.RoundtripLossBps != 4000 {
		t.Fatalf("expected EXCESSIVE_TAX 4000bps, got %+v err=%v", res, err)
	}
}
