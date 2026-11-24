// Package honeypot implements Step 4 sequential Buy -> Approve -> Sell simulation
// via eth_simulateV1 with state overrides to detect honeypots, sell-blocked tokens,
// and excessive buy/sell transfer taxes before admitting a trade.
package honeypot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	erc20TransferTopic  = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	wbnbWithdrawalTopic = crypto.Keccak256Hash([]byte("Withdrawal(address,uint256)"))
)

type Verdict string

const (
	VerdictSafe              Verdict = "SAFE"
	VerdictBuyReverted       Verdict = "BUY_REVERTED"
	VerdictApproveReverted   Verdict = "APPROVE_REVERTED"
	VerdictSellReverted      Verdict = "SELL_REVERTED"
	VerdictExcessiveTax      Verdict = "EXCESSIVE_TAX"
	VerdictZeroTokensBought  Verdict = "ZERO_TOKENS_BOUGHT"
)

type SimulationRequest struct {
	ProbeWallet             common.Address
	Router                  common.Address
	Token                   common.Address
	WrappedNative           common.Address
	ProbeNativeWei          *big.Int
	BuyCalldata             []byte
	ApproveCalldata         []byte
	SellCalldata            []byte
	MaxRoundtripLossBps     uint64 // e.g. 1500 = 15% max combined buy+sell tax & price impact
}

type SimulationResult struct {
	Verdict          Verdict  `json:"verdict"`
	Safe             bool     `json:"safe"`
	TokensBoughtRaw  *big.Int `json:"tokens_bought_raw"`
	NativeRecovered  *big.Int `json:"native_recovered_wei"`
	RoundtripLossBps uint64   `json:"roundtrip_loss_bps"`
	Detail           string   `json:"detail"`
}

type Simulator struct {
	endpoint string
	http     *http.Client
}

func NewSimulator(endpoint string, timeout time.Duration) *Simulator {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &Simulator{
		endpoint: endpoint,
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
	}
}

// SimulateBuyApproveSell executes a single atomic eth_simulateV1 RPC call with state override
// funding ProbeWallet and sequentially simulating [Buy, Approve, Sell] in one block.
func (s *Simulator) SimulateBuyApproveSell(ctx context.Context, req SimulationRequest) (SimulationResult, error) {
	if s == nil || s.endpoint == "" || req.ProbeWallet == (common.Address{}) || req.Router == (common.Address{}) ||
		req.Token == (common.Address{}) || req.ProbeNativeWei == nil || req.ProbeNativeWei.Sign() <= 0 ||
		len(req.BuyCalldata) < 4 || len(req.ApproveCalldata) < 4 || len(req.SellCalldata) < 4 {
		return SimulationResult{}, errors.New("invalid honeypot simulation request")
	}

	// Fund ProbeWallet with ProbeNativeWei * 10 (covers value + simulated gas).
	overrideBal := new(big.Int).Mul(req.ProbeNativeWei, big.NewInt(10))
	overrideBal.Add(overrideBal, big.NewInt(1_000_000_000_000_000_000)) // +1 native coin for gas

	rpcPayload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "eth_simulateV1",
		"params": []any{
			map[string]any{
				"blockStateCalls": []any{
					map[string]any{
						"stateOverrides": map[string]any{
							req.ProbeWallet.Hex(): map[string]any{
								"balance": "0x" + overrideBal.Text(16),
							},
						},
						"calls": []any{
							map[string]any{
								"from":  req.ProbeWallet.Hex(),
								"to":    req.Router.Hex(),
								"value": "0x" + req.ProbeNativeWei.Text(16),
								"data":  "0x" + common.Bytes2Hex(req.BuyCalldata),
							},
							map[string]any{
								"from": req.ProbeWallet.Hex(),
								"to":   req.Token.Hex(),
								"data": "0x" + common.Bytes2Hex(req.ApproveCalldata),
							},
							map[string]any{
								"from": req.ProbeWallet.Hex(),
								"to":   req.Router.Hex(),
								"data": "0x" + common.Bytes2Hex(req.SellCalldata),
							},
						},
					},
				},
				"validation": true,
			},
			"latest",
		},
	}

	body, err := json.Marshal(rpcPayload)
	if err != nil {
		return SimulationResult{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return SimulationResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(httpReq)
	if err != nil {
		return SimulationResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return SimulationResult{}, fmt.Errorf("eth_simulateV1 http status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return SimulationResult{}, err
	}

	var rpcResp struct {
		Result []struct {
			Calls []struct {
				Status string `json:"status"`
				Logs   []struct {
					Address string   `json:"address"`
					Topics  []string `json:"topics"`
					Data    string   `json:"data"`
				} `json:"logs"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error,omitempty"`
			} `json:"calls"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return SimulationResult{}, fmt.Errorf("decode eth_simulateV1 response: %w", err)
	}
	if rpcResp.Error != nil {
		return SimulationResult{}, fmt.Errorf("eth_simulateV1 rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if len(rpcResp.Result) == 0 || len(rpcResp.Result[0].Calls) != 3 {
		return SimulationResult{}, errors.New("unexpected eth_simulateV1 call result count")
	}

	calls := rpcResp.Result[0].Calls
	if !isSuccessStatus(calls[0].Status) {
		return SimulationResult{Verdict: VerdictBuyReverted, Safe: false, Detail: "buy leg reverted in simulation"}, nil
	}
	if !isSuccessStatus(calls[1].Status) {
		return SimulationResult{Verdict: VerdictApproveReverted, Safe: false, Detail: "ERC-20 approve leg reverted in simulation"}, nil
	}
	if !isSuccessStatus(calls[2].Status) {
		return SimulationResult{Verdict: VerdictSellReverted, Safe: false, Detail: "sell leg reverted (honeypot)"}, nil
	}

	// Measure net tokens received by ProbeWallet during Call 0 (Buy).
	tokensBought := big.NewInt(0)
	for _, lg := range calls[0].Logs {
		if common.HexToAddress(lg.Address) != req.Token || len(lg.Topics) != 3 || common.HexToHash(lg.Topics[0]) != erc20TransferTopic {
			continue
		}
		from := common.HexToAddress(lg.Topics[1])
		to := common.HexToAddress(lg.Topics[2])
		amt := new(big.Int).SetBytes(common.FromHex(lg.Data))
		if to == req.ProbeWallet {
			tokensBought.Add(tokensBought, amt)
		}
		if from == req.ProbeWallet {
			tokensBought.Sub(tokensBought, amt)
		}
	}
	if tokensBought.Sign() <= 0 {
		return SimulationResult{Verdict: VerdictZeroTokensBought, Safe: false, TokensBoughtRaw: tokensBought, Detail: "buy yielded zero net tokens"}, nil
	}

	// Measure native proceeds recovered during Call 2 (Sell) via WrappedNative Withdrawal or Transfer to ProbeWallet.
	nativeRecovered := big.NewInt(0)
	for _, lg := range calls[2].Logs {
		addr := common.HexToAddress(lg.Address)
		if addr != req.WrappedNative {
			continue
		}
		if len(lg.Topics) == 2 && common.HexToHash(lg.Topics[0]) == wbnbWithdrawalTopic {
			actor := common.HexToAddress(lg.Topics[1])
			if actor == req.Router || actor == req.ProbeWallet {
				nativeRecovered.Add(nativeRecovered, new(big.Int).SetBytes(common.FromHex(lg.Data)))
			}
		} else if len(lg.Topics) == 3 && common.HexToHash(lg.Topics[0]) == erc20TransferTopic {
			if common.HexToAddress(lg.Topics[2]) == req.ProbeWallet {
				nativeRecovered.Add(nativeRecovered, new(big.Int).SetBytes(common.FromHex(lg.Data)))
			}
		}
	}

	var lossBps uint64
	if nativeRecovered.Cmp(req.ProbeNativeWei) < 0 {
		diff := new(big.Int).Sub(req.ProbeNativeWei, nativeRecovered)
		diff.Mul(diff, big.NewInt(10000))
		diff.Div(diff, req.ProbeNativeWei)
		lossBps = diff.Uint64()
	}
	if req.MaxRoundtripLossBps > 0 && lossBps > req.MaxRoundtripLossBps {
		return SimulationResult{
			Verdict:          VerdictExcessiveTax,
			Safe:             false,
			TokensBoughtRaw:  tokensBought,
			NativeRecovered:  nativeRecovered,
			RoundtripLossBps: lossBps,
			Detail:           fmt.Sprintf("roundtrip loss %d bps exceeds limit %d bps", lossBps, req.MaxRoundtripLossBps),
		}, nil
	}
	return SimulationResult{
		Verdict:          VerdictSafe,
		Safe:             true,
		TokensBoughtRaw:  tokensBought,
		NativeRecovered:  nativeRecovered,
		RoundtripLossBps: lossBps,
		Detail:           "buy->approve->sell simulation passed",
	}, nil
}

func isSuccessStatus(s string) bool {
	return strings.EqualFold(strings.TrimSpace(s), "0x1")
}
