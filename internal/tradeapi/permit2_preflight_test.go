package tradeapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/permit2"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
	univ4 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/uniswap/v4"
	"github.com/ruzickahunzeker/rbh-bot/internal/registry"
)

type permitPreflightTransport func(*http.Request) (*http.Response, error)

func (f permitPreflightTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestQuotePreflightPermit2UsesBothLayersAndPinnedExpiry(t *testing.T) {
	for _, tc := range []struct {
		name                                    string
		tokenAllowance, routerAllowance, expiry int64
		want                                    string
		needs                                   bool
		bad                                     bool
	}{
		{"missing ERC20", 0, 100, 2000, "0", true, false},
		{"missing Permit2", 100, 0, 2000, "0", true, false},
		{"expired Permit2", 100, 100, 999, "0", true, false},
		{"expires at head", 100, 100, 1000, "0", true, false},
		{"ERC20 limited", 50, 100, 2000, "50", true, false},
		{"router limited", 100, 50, 2000, "50", true, false},
		{"ready", 100, 100, 2000, "100", false, false},
		{"malformed Permit2", 100, 100, 2000, "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token, wallet, block := common.HexToAddress("0x1234"), common.HexToAddress("0x5678"), common.HexToHash("0xabcd")
			reg, err := registry.New(bsc.Profile(), []ports.ProtocolAdapter{univ4.NewBSCAdapter()}, map[string]registry.TrustedSpender{univ4.ProtocolIDBSC: {Address: bsc.UniswapV4UniversalRouter}})
			if err != nil {
				t.Fatal(err)
			}
			p := NewRPCLocalV2Provider("http://rpc.test", reg, nil, wallet, time.Second)
			p.http.Transport = permitPreflightTransport(func(r *http.Request) (*http.Response, error) {
				var request struct {
					Method string            `json:"method"`
					Params []json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					return nil, err
				}
				var result any
				switch request.Method {
				case "eth_getBlockByNumber":
					result = map[string]any{"number": "0x64", "hash": block.Hex()}
				case "eth_getBlockByHash":
					var got string
					_ = json.Unmarshal(request.Params[0], &got)
					if got != block.Hex() {
						t.Fatal("timestamp must match quote block")
					}
					result = map[string]any{"timestamp": "0x3e8"}
				case "eth_getCode":
					result = "0x6080"
				case "eth_call":
					var call struct {
						To   string `json:"to"`
						Data string `json:"data"`
					}
					_ = json.Unmarshal(request.Params[0], &call)
					var pin struct {
						Hash string `json:"blockHash"`
					}
					_ = json.Unmarshal(request.Params[1], &pin)
					if pin.Hash != block.Hex() {
						t.Fatal("allowance/quote read must be pinned")
					}
					to := common.HexToAddress(call.To)
					var raw []byte
					switch to {
					case bsc.UniswapV4Quoter:
						raw = append(common.LeftPadBytes(big.NewInt(90).Bytes(), 32), common.LeftPadBytes(big.NewInt(220000).Bytes(), 32)...)
					case token:
						args, err := erc20ReadABI.Methods["allowance"].Inputs.Unpack(common.FromHex(call.Data)[4:])
						if err != nil || args[0].(common.Address) != wallet || args[1].(common.Address) != permit2.Address {
							t.Fatal("incorrect ERC20 allowance owner/spender")
						}
						raw = common.LeftPadBytes(big.NewInt(tc.tokenAllowance).Bytes(), 32)
					case permit2.Address:
						cd := common.FromHex(call.Data)
						if len(cd) != 100 || common.BytesToAddress(cd[4:36]) != wallet || common.BytesToAddress(cd[36:68]) != token || common.BytesToAddress(cd[68:100]) != bsc.UniswapV4UniversalRouter {
							t.Fatal("incorrect Permit2 allowance identity")
						}
						raw = append(common.LeftPadBytes(big.NewInt(tc.routerAllowance).Bytes(), 32), common.LeftPadBytes(big.NewInt(tc.expiry).Bytes(), 32)...)
						raw = append(raw, make([]byte, 32)...)
						if tc.bad {
							raw = nil
						}
					default:
						return nil, fmt.Errorf("unexpected read target %s", to)
					}
					result = "0x" + common.Bytes2Hex(raw)
				default:
					return nil, fmt.Errorf("unexpected method %s", request.Method)
				}
				body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
			})
			q, err := p.QuotePreflight(context.Background(), QuotePreflightRequest{Chain: "bsc", Protocol: univ4.ProtocolIDBSC, Token: token.Hex(), Side: "sell", AmountRaw: "100", SlippageBps: 100}, bsc.UniswapV4UniversalRouter)
			if tc.bad {
				if err == nil {
					t.Fatal("malformed allowance must fail closed")
				}
				return
			}
			if err != nil || q.CurrentAllowance != tc.want || q.NeedsApprove != tc.needs {
				t.Fatalf("quote=%+v err=%v", q, err)
			}
		})
	}
}
