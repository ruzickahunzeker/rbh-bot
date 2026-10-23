package chainquery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math/big"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pons "github.com/0xfnzero/rbh-trade-sdk/adapters/pons"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

type validationFixture struct {
	fault                  string
	simulations, estimates atomic.Int64
	expired                atomic.Bool
	v                      *Validator
	expiry                 time.Time
}

func validationSetup(t *testing.T) (*Validator, *validationFixture, ValidationIntent, ValidationLimits) {
	c, f := setup(t)
	c.estimates = true
	v := &Validator{client: c, now: func() time.Time { return time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC) }}
	g := &validationFixture{v: v, expiry: v.now().Add(30 * time.Second)}
	original := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var q struct {
			ID     uint64            `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if json.Unmarshal(body, &q) != nil {
			t.Error("invalid RPC")
			return
		}
		var result any
		handled := false
		if q.Method == "eth_getBalance" && g.fault == "contradictory_balance" {
			handled, result = true, "0x1"
		}
		if q.Method == "eth_estimateGas" {
			g.estimates.Add(1)
			handled = true
			result = "0x186a0"
			if g.fault == "bad_gas" {
				result = "0x00"
			}
			if g.fault == "gas_cap" {
				result = "0x989680"
			}
		}
		if q.Method == "eth_call" {
			var call struct{ To, Data, From, Value, MaxFeePerGas, MaxPriorityFeePerGas, Gas string }
			_ = json.Unmarshal(q.Params[0], &call)
			data, _ := hexutil.Decode(call.Data)
			if len(data) >= 4 {
				sig := hexutil.Encode(data[:4])
				var out []byte
				switch sig {
				case hexutil.Encode(selector("getReserves()")):
					out = append(uintWord(1000000000000000000), uintWord(1000000000000)...)
					if g.fault == "post_quote_expiry" {
						g.expired.Store(true)
					}
				case hexutil.Encode(selector("feeBps()")):
					out = uintWord(100)
				case hexutil.Encode(selector("creatorTaxBps()")):
					out = uintWord(50)
				case hexutil.Encode(selector("sellableTokens()")):
					out = uintWord(500000000000)
					if g.fault == "partial_spend" {
						out = uintWord(1)
					}
				case hexutil.Encode(selector("currentSnipeTaxBps(address)")):
					out = uintWord(0)
					if !bytes.Equal(data[4:], addrWord(walletAddress)) {
						t.Error("wrong snipe recipient")
					}
				case hexutil.Encode(selector("buy(uint256,uint256,address)")), hexutil.Encode(selector("sell(uint256,uint256,address)")):
					g.simulations.Add(1)
					if len(data) != 100 || call.To != curveAddress || call.From != walletAddress || !bytes.Equal(data[68:], addrWord(walletAddress)) {
						t.Error("wrong fixed simulation")
					}
					if call.Gas != "0x186a0" {
						t.Error("simulation omitted bounded gas")
					}
					amount := new(big.Int).SetBytes(data[4:36])
					reserves := pons.CurveReserves{QuoteReserve: big.NewInt(1000000000000000000), TokenReserve: big.NewInt(1000000000000)}
					var output *big.Int
					if sig == hexutil.Encode(selector("buy(uint256,uint256,address)")) {
						quote, err := pons.QuoteBuyFromState(reserves, big.NewInt(500000000000), amount, big.NewInt(100), big.NewInt(50), big.NewInt(0))
						if err != nil {
							t.Error(err)
							return
						}
						output = quote.TokensOut
						if call.Value != hexutil.EncodeBig(amount) {
							t.Error("wrong buy value")
						}
					} else {
						quote, err := pons.QuoteSellFromState(reserves, amount, big.NewInt(100), big.NewInt(50))
						if err != nil {
							t.Error(err)
							return
						}
						output = quote.QuoteOut
						if call.Value != "0x0" {
							t.Error("sell native value")
						}
					}
					out = common.LeftPadBytes(output.Bytes(), 32)
					if g.fault == "wrong_simulation" {
						out = uintWord(1)
					}
					if g.fault == "malformed_simulation" {
						out = out[:31]
					}
					if g.fault == "post_sim_expiry" {
						g.expired.Store(true)
					}
				}
				if out != nil {
					handled = true
					result = hexutil.Encode(out)
					if g.fault == "bad_reserves" && sig == hexutil.Encode(selector("getReserves()")) {
						result = "0x00"
					}
				}
			}
		}
		if !handled {
			original.ServeHTTP(w, r)
			return
		}
		var pin struct {
			Hash      string `json:"blockHash"`
			Canonical bool   `json:"requireCanonical"`
		}
		if len(q.Params) != 2 || json.Unmarshal(q.Params[1], &pin) != nil || pin.Hash != f.header.Hash().Hex() || !pin.Canonical {
			t.Error("unbound validation RPC")
		}
		if g.fault == "rpc_unavailable" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result})
	})
	v.now = func() time.Time {
		if g.expired.Load() {
			return g.expiry
		}
		return g.expiry.Add(-30 * time.Second)
	}
	i := ValidationIntent{Wallet: walletAddress, Token: tokenAddress, Side: "buy", Amount: "0.01", SlippageBPS: 300, MaxFeeWei: "2000000000", TipWei: "100000000", ExpiresAt: g.expiry.Format(time.RFC3339Nano)}
	l := ValidationLimits{MaxInput: "2000000000000000000", MaxGas: "1000000", MaxFee: "3000000000", MaxTip: "1000000000", MinNative: "1000000000000000"}
	return v, g, i, l
}
func TestHTTPC2ControlledRPCValidation(t *testing.T) {
	for _, mode := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(mode, func(t *testing.T) {
			v, f, i, l := validationSetup(t)
			if mode != "buy" {
				i.Side = "sell"
				i.Amount = "1000"
			}
			if mode == "sell_percent" {
				i.Amount = ""
				i.SellBPS = 5000
			}
			r, err := v.Resolve(context.Background(), i)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "sell_percent" && r.AmountIn != "750250000" {
				t.Fatal("percent units")
			}
			o, err := v.Complete(context.Background(), i, r, l)
			if err != nil {
				t.Fatal(err)
			}
			if err = CheckValidation(i, r, l, o); err != nil {
				t.Fatal(err)
			}
			if o.GasFeeCap != "2000000000" || o.GasTipCap != "100000000" || o.GasBudget != "200000000000000" || o.Resolution != r || f.simulations.Load() != 1 {
				t.Fatal("binding mismatch")
			}
		})
	}
}
func TestHTTPC2ValidationFaultsAndTTLFailClosed(t *testing.T) {
	for _, fault := range []string{"contradictory_balance", "bad_reserves", "partial_spend", "bad_gas", "gas_cap", "wrong_simulation", "malformed_simulation", "post_quote_expiry", "post_sim_expiry", "rpc_unavailable"} {
		t.Run(fault, func(t *testing.T) {
			v, f, i, l := validationSetup(t)
			r, err := v.Resolve(context.Background(), i)
			if err != nil {
				t.Fatal(err)
			}
			f.fault = fault
			if _, err = v.Complete(context.Background(), i, r, l); err == nil {
				t.Fatal("fault accepted")
			}
			if fault == "post_quote_expiry" && (!errors.Is(err, ErrExpired) || f.estimates.Load() != 0 || f.simulations.Load() != 0) {
				t.Fatal("expired quote continued to estimate/simulate", err)
			}
		})
	}
	for _, expiry := range []string{"2026-10-08T01:00:00Z", "2026-10-08T01:00:00.000Z", "2026-10-08T09:00:30+08:00", "invalid", ""} {
		v, f, i, _ := validationSetup(t)
		i.ExpiresAt = expiry
		if _, err := v.Resolve(context.Background(), i); !errors.Is(err, ErrExpired) {
			t.Fatal("TTL not fail closed")
		}
		if f.simulations.Load() != 0 || f.estimates.Load() != 0 {
			t.Fatal("expired RPC")
		}
	}
}
func TestHTTPC2StrictAmountsFeesAndOptionalFloors(t *testing.T) {
	for _, mutate := range []func(*ValidationIntent, *ValidationLimits){
		func(i *ValidationIntent, l *ValidationLimits) { i.Side = "sell"; i.Amount = "1000.0000001" },
		func(i *ValidationIntent, l *ValidationLimits) { i.Side = "sell"; i.Amount = ""; i.SellBPS = 10000 },
		func(i *ValidationIntent, l *ValidationLimits) { i.Amount = "1e-2" },
		func(i *ValidationIntent, l *ValidationLimits) { i.MaxFeeWei = "1000000000" },
		func(i *ValidationIntent, l *ValidationLimits) { i.TipWei = "3000000000" },
		func(i *ValidationIntent, l *ValidationLimits) { i.MaxTotalWei = "1" },
		func(i *ValidationIntent, l *ValidationLimits) { i.MinReceive = "1000000000" },
		func(i *ValidationIntent, l *ValidationLimits) { l.MinNative = "2000000000000000000" },
		func(i *ValidationIntent, l *ValidationLimits) { l.MaxInput = "1" },
	} {
		v, f, i, l := validationSetup(t)
		mutate(&i, &l)
		r, err := v.Resolve(context.Background(), i)
		if err == nil {
			_, err = v.Complete(context.Background(), i, r, l)
		}
		if err == nil {
			t.Fatal("unsafe parameters accepted")
		}
		if f.simulations.Load() != 0 {
			t.Fatal("rejected limits reached simulation")
		}
	}
	v, _, i, l := validationSetup(t)
	i.MaxFeeWei = ""
	i.TipWei = ""
	r, err := v.Resolve(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	o, err := v.Complete(context.Background(), i, r, l)
	if err != nil || o.GasFeeCap != "2100000000" {
		t.Fatal("bounded automatic fee", err)
	}
	i.MinReceive = Units(new(big.Int).SetBytes(common.FromHex(o.ReturnData)), r.Token.Decimals)
	r, err = v.Resolve(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	o, err = v.Complete(context.Background(), i, r, l)
	if err != nil || o.MinimumOut != r.MinReceiveUnits {
		t.Fatal("user output floor", err)
	}
}
func TestHTTPC2ValidatorHasNoEconomicSurface(t *testing.T) {
	typeOf := reflect.TypeOf((*Validator)(nil))
	if typeOf.NumMethod() != 2 {
		t.Fatal("extra validator capability")
	}
	for index := 0; index < typeOf.NumMethod(); index++ {
		if name := typeOf.Method(index).Name; name != "Resolve" && name != "Complete" {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"validation.go", "validation_checks.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.Contains(path, "internal/trade") || strings.Contains(path, "ethclient") || strings.HasSuffix(path, "/rpc") {
				t.Fatal("broad dependency")
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if s, ok := n.(*ast.SelectorExpr); ok {
				switch s.Sel.Name {
				case "SignTransaction", "SendRawTransaction", "PendingNonce", "NewClient", "Dial", "Submit", "ReplayUnknown", "SignTx", "NewTx":
					t.Error("economic capability")
				}
			}
			return true
		})
	}
	c, _ := setup(t)
	if err := c.rpc(context.Background(), "eth_estimateGas", []any{}, new(string)); !errors.Is(err, ErrObservation) {
		t.Fatal("HTTP-B query client gained estimation")
	}
}
