// Package honeypot implements Step 4 sequential Buy -> Approve -> Sell simulation
// via eth_simulateV1 with state overrides to detect honeypots, sell-blocked tokens,
// gas-griefing hooks, and excessive buy/sell transfer taxes before admitting a trade.
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
	"strconv"
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
	VerdictSafe                Verdict = "SAFE"
	VerdictBuyReverted         Verdict = "BUY_REVERTED"
	VerdictApproveReverted     Verdict = "APPROVE_REVERTED"
	VerdictSellReverted        Verdict = "SELL_REVERTED"
	VerdictExcessiveTax        Verdict = "EXCESSIVE_TAX"
	VerdictZeroTokensBought    Verdict = "ZERO_TOKENS_BOUGHT"
	VerdictZeroNativeRecovered Verdict = "ZERO_NATIVE_RECOVERED"
	VerdictGasGriefing         Verdict = "GAS_GRIEFING"
)

const (
	DefaultMaxRoundtripLossBps uint64 = 3000      // 30% safety ceiling when caller leaves 0
	DefaultSimulatedCallGasCap uint64 = 2_000_000 // per-call gas limit in eth_simulateV1
	DefaultMaxLegGasUsed       uint64 = 1_500_000 // reject gas-griefing token hooks above 1.5M gas
)

type SimulationRequest struct {
	ProbeWallet         common.Address
	Router              common.Address
	Token               common.Address
	WrappedNative       common.Address
	ProbeNativeWei      *big.Int
	BuyCalldata         []byte
	ApproveCalldata     []byte
	SellCalldata        []byte
	// BuildSellCalldata optionally builds the exact post-buy-tax Sell calldata from the net
	// tokens received in the simulated Buy leg (prevents false-positive reverts on FoT tax tokens).
	BuildSellCalldata   func(tokensBoughtRaw *big.Int) ([]byte, error)
	MaxRoundtripLossBps uint64
	MaxLegGasUsed       uint64
}

type SimulationResult struct {
	Verdict          Verdict  `json:"verdict"`
	Safe             bool     `json:"safe"`
	TokensBoughtRaw  *big.Int `json:"tokens_bought_raw"`
	NativeRecovered  *big.Int `json:"native_recovered_wei"`
	RoundtripLossBps uint64   `json:"roundtrip_loss_bps"`
	MaxGasUsed       uint64   `json:"max_gas_used"`
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

type simCallResult struct {
	Status  string `json:"status"`
	GasUsed string `json:"gasUsed"`
	Logs    []struct {
		Address string   `json:"address"`
		Topics  []string `json:"topics"`
		Data    string   `json:"data"`
	} `json:"logs"`
}

// SimulateBuyApproveSell executes eth_simulateV1 with state override funding ProbeWallet
// and sequentially simulating [Buy, Approve, Sell] in one block. When BuildSellCalldata is
// supplied and SellCalldata is empty, it first simulates [Buy] to measure exact post-tax
// tokensBoughtRaw, then simulates the full [Buy, Approve, Sell(tokensBoughtRaw)] bundle.
func (s *Simulator) SimulateBuyApproveSell(ctx context.Context, req SimulationRequest) (SimulationResult, error) {
	if s == nil || s.endpoint == "" || req.ProbeWallet == (common.Address{}) || req.Router == (common.Address{}) ||
		req.Token == (common.Address{}) || req.WrappedNative == (common.Address{}) ||
		req.ProbeNativeWei == nil || req.ProbeNativeWei.Sign() <= 0 ||
		len(req.BuyCalldata) < 4 || len(req.ApproveCalldata) < 4 ||
		(len(req.SellCalldata) < 4 && req.BuildSellCalldata == nil) {
		return SimulationResult{}, errors.New("invalid honeypot simulation request")
	}

	maxLossBps := req.MaxRoundtripLossBps
	if maxLossBps == 0 {
		maxLossBps = DefaultMaxRoundtripLossBps
	}
	maxGas := req.MaxLegGasUsed
	if maxGas == 0 {
		maxGas = DefaultMaxLegGasUsed
	}

	sellData := req.SellCalldata
	if len(sellData) < 4 && req.BuildSellCalldata != nil {
		// Phase 1: probe exact post-tax tokensBoughtRaw on Buy leg.
		buyCalls, err := s.runSimulateRPC(ctx, req, [][]byte{req.BuyCalldata}, true)
		if err != nil {
			return SimulationResult{}, err
		}
		if !isSuccessStatus(buyCalls[0].Status) {
			return SimulationResult{Verdict: VerdictBuyReverted, Safe: false, Detail: "buy leg reverted in simulation"}, nil
		}
		bought := extractNetTokenBought(buyCalls[0], req.Token, req.ProbeWallet)
		if bought.Sign() <= 0 {
			return SimulationResult{Verdict: VerdictZeroTokensBought, Safe: false, TokensBoughtRaw: bought, Detail: "buy yielded zero net tokens"}, nil
		}
		sellData, err = req.BuildSellCalldata(bought)
		if err != nil || len(sellData) < 4 {
			return SimulationResult{}, fmt.Errorf("build dynamic sell calldata: %w", err)
		}
	}

	calls, err := s.runSimulateRPC(ctx, req, [][]byte{req.BuyCalldata, req.ApproveCalldata, sellData}, false)
	if err != nil {
		return SimulationResult{}, err
	}
	if !isSuccessStatus(calls[0].Status) {
		return SimulationResult{Verdict: VerdictBuyReverted, Safe: false, Detail: "buy leg reverted in simulation"}, nil
	}
	if !isSuccessStatus(calls[1].Status) {
		return SimulationResult{Verdict: VerdictApproveReverted, Safe: false, Detail: "ERC-20 approve leg reverted in simulation"}, nil
	}
	if !isSuccessStatus(calls[2].Status) {
		return SimulationResult{Verdict: VerdictSellReverted, Safe: false, Detail: "sell leg reverted (honeypot)"}, nil
	}

	var peakGas uint64
	for _, c := range calls {
		g := parseOptHexUint64(c.GasUsed)
		if g > peakGas {
			peakGas = g
		}
		if g > maxGas {
			return SimulationResult{
				Verdict:    VerdictGasGriefing,
				Safe:       false,
				MaxGasUsed: peakGas,
				Detail:     fmt.Sprintf("simulated leg gasUsed %d exceeds limit %d", g, maxGas),
			}, nil
		}
	}

	tokensBought := extractNetTokenBought(calls[0], req.Token, req.ProbeWallet)
	if tokensBought.Sign() <= 0 {
		return SimulationResult{Verdict: VerdictZeroTokensBought, Safe: false, TokensBoughtRaw: tokensBought, MaxGasUsed: peakGas, Detail: "buy yielded zero net tokens"}, nil
	}

	nativeRecovered := big.NewInt(0)
	for _, lg := range calls[2].Logs {
		if common.HexToAddress(lg.Address) != req.WrappedNative {
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
	if nativeRecovered.Sign() <= 0 {
		return SimulationResult{
			Verdict:          VerdictZeroNativeRecovered,
			Safe:             false,
			TokensBoughtRaw:  tokensBought,
			NativeRecovered:  nativeRecovered,
			RoundtripLossBps: 10000,
			MaxGasUsed:       peakGas,
			Detail:           "sell leg returned zero native proceeds (100% tax honeypot)",
		}, nil
	}

	var lossBps uint64
	if nativeRecovered.Cmp(req.ProbeNativeWei) < 0 {
		diff := new(big.Int).Sub(req.ProbeNativeWei, nativeRecovered)
		diff.Mul(diff, big.NewInt(10000))
		diff.Div(diff, req.ProbeNativeWei)
		lossBps = diff.Uint64()
	}
	if lossBps > maxLossBps {
		return SimulationResult{
			Verdict:          VerdictExcessiveTax,
			Safe:             false,
			TokensBoughtRaw:  tokensBought,
			NativeRecovered:  nativeRecovered,
			RoundtripLossBps: lossBps,
			MaxGasUsed:       peakGas,
			Detail:           fmt.Sprintf("roundtrip loss %d bps exceeds limit %d bps", lossBps, maxLossBps),
		}, nil
	}
	return SimulationResult{
		Verdict:          VerdictSafe,
		Safe:             true,
		TokensBoughtRaw:  tokensBought,
		NativeRecovered:  nativeRecovered,
		RoundtripLossBps: lossBps,
		MaxGasUsed:       peakGas,
		Detail:           "buy->approve->sell simulation passed",
	}, nil
}

func (s *Simulator) runSimulateRPC(ctx context.Context, req SimulationRequest, callPayloads [][]byte, buyOnly bool) ([]simCallResult, error) {
	overrideBal := new(big.Int).Mul(req.ProbeNativeWei, big.NewInt(10))
	overrideBal.Add(overrideBal, big.NewInt(1_000_000_000_000_000_000))
	gasHex := fmt.Sprintf("0x%x", DefaultSimulatedCallGasCap)

	var callObjs []any
	if buyOnly {
		callObjs = []any{
			map[string]any{
				"from":  req.ProbeWallet.Hex(),
				"to":    req.Router.Hex(),
				"gas":   gasHex,
				"value": "0x" + req.ProbeNativeWei.Text(16),
				"data":  "0x" + common.Bytes2Hex(callPayloads[0]),
			},
		}
	} else {
		callObjs = []any{
			map[string]any{
				"from":  req.ProbeWallet.Hex(),
				"to":    req.Router.Hex(),
				"gas":   gasHex,
				"value": "0x" + req.ProbeNativeWei.Text(16),
				"data":  "0x" + common.Bytes2Hex(callPayloads[0]),
			},
			map[string]any{
				"from": req.ProbeWallet.Hex(),
				"to":   req.Token.Hex(),
				"gas":  gasHex,
				"data": "0x" + common.Bytes2Hex(callPayloads[1]),
			},
			map[string]any{
				"from": req.ProbeWallet.Hex(),
				"to":   req.Router.Hex(),
				"gas":  gasHex,
				"data": "0x" + common.Bytes2Hex(callPayloads[2]),
			},
		}
	}

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
						"calls": callObjs,
					},
				},
				"validation": true,
			},
			"latest",
		},
	}

	body, err := json.Marshal(rpcPayload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("eth_simulateV1 http status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var rpcResp struct {
		Result []struct {
			Calls []simCallResult `json:"calls"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return nil, fmt.Errorf("decode eth_simulateV1 response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("eth_simulateV1 rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if len(rpcResp.Result) == 0 || len(rpcResp.Result[0].Calls) != len(callObjs) {
		return nil, errors.New("unexpected eth_simulateV1 call result count")
	}
	return rpcResp.Result[0].Calls, nil
}

func extractNetTokenBought(call simCallResult, token, probeWallet common.Address) *big.Int {
	net := big.NewInt(0)
	for _, lg := range call.Logs {
		if common.HexToAddress(lg.Address) != token || len(lg.Topics) != 3 || common.HexToHash(lg.Topics[0]) != erc20TransferTopic {
			continue
		}
		from := common.HexToAddress(lg.Topics[1])
		to := common.HexToAddress(lg.Topics[2])
		amt := new(big.Int).SetBytes(common.FromHex(lg.Data))
		if to == probeWallet {
			net.Add(net, amt)
		}
		if from == probeWallet {
			net.Sub(net, amt)
		}
	}
	return net
}

func parseOptHexUint64(s string) uint64 {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseUint(s, 16, 64)
	return v
}

func isSuccessStatus(s string) bool {
	return strings.EqualFold(strings.TrimSpace(s), "0x1")
}
