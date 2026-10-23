package chainquery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const tokenAddress = "0x2000000000000000000000000000000000000002"
const curveAddress = "0x3000000000000000000000000000000000000003"
const walletAddress = "0x1000000000000000000000000000000000000001"

type rpcFixture struct {
	t       *testing.T
	mu      sync.Mutex
	fault   string
	methods map[string]int
	header  *types.Header
	server  *httptest.Server
}

func uintWord(value int64) []byte { return common.LeftPadBytes(big.NewInt(value).Bytes(), 32) }
func addrWord(address string) []byte {
	return common.LeftPadBytes(common.HexToAddress(address).Bytes(), 32)
}
func textWord(text string) []byte {
	result := append(uintWord(32), uintWord(int64(len(text)))...)
	return append(result, common.RightPadBytes([]byte(text), ((len(text)+31)/32)*32)...)
}

func setup(t *testing.T) (*Client, *rpcFixture) {
	t.Helper()
	f := &rpcFixture{t: t, methods: map[string]int{}, header: &types.Header{Number: big.NewInt(100), Time: 1700000000, GasLimit: 10000000, BaseFee: big.NewInt(1000000000), Difficulty: big.NewInt(0)}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	c, err := New(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Test-only fixture identities; production New always uses locked constants.
	c.factoryCodeHash = crypto.Keccak256Hash([]byte{1, 2, 3})
	c.curveCodeHash = crypto.Keccak256Hash([]byte{4, 5, 6})
	return c, f
}

func (f *rpcFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var req struct {
		JSONRPC string            `json:"jsonrpc"`
		ID      uint64            `json:"id"`
		Method  string            `json:"method"`
		Params  []json.RawMessage `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		f.t.Error("invalid outgoing JSON")
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	f.methods[req.Method]++
	if r.Header.Get("Authorization") != "" || req.JSONRPC != "2.0" {
		f.t.Error("credential forwarded to RPC or invalid version")
	}
	if f.fault == "timeout" {
		time.Sleep(50 * time.Millisecond)
	}
	if f.fault == "http_failure" {
		http.Error(w, "secret provider detail", http.StatusServiceUnavailable)
		return
	}
	if f.fault == "oversized" {
		_, _ = w.Write(bytes.Repeat([]byte{'x'}, (1<<20)+1))
		return
	}
	if f.fault == "redirect" {
		w.Header().Set("Location", f.server.URL+"/followed")
		w.WriteHeader(http.StatusFound)
		return
	}
	if f.fault == "rpc_error" {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -1, "message": "secret provider detail"}})
		return
	}
	var result any
	switch req.Method {
	case "eth_chainId":
		result = "0x1237"
		if f.fault == "wrong_chain" || f.fault == "chain_drift" && f.methods[req.Method] > 1 {
			result = "0x38"
		}
		if f.fault == "noncanonical_quantity" {
			result = "0x01237"
		}
	case "eth_getBlockByNumber":
		if len(req.Params) != 2 {
			f.t.Error("bad header params")
		}
		h := *f.header
		var number string
		_ = json.Unmarshal(req.Params[0], &number)
		if f.fault == "reorg" && number != "latest" {
			h.Extra = []byte{1}
		}
		if f.fault == "wrong_block" && number != "latest" {
			h.Number = big.NewInt(101)
		}
		if f.fault == "missing_basefee" {
			h.BaseFee = nil
		}
		raw, _ := json.Marshal(&h)
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		if f.fault == "wrong_hash" {
			fields["hash"] = common.HexToHash("0x99").Hex()
		}
		if f.fault == "duplicate_header" {
			raw = append(raw[:len(raw)-1], []byte(`,"number":"0x64"}`)...)
			result = json.RawMessage(raw)
		} else if f.fault == "header_case_alias" {
			fields["Number"] = fields["number"]
			result = fields
		} else if f.fault == "noncanonical_header_member" {
			fields["Number"] = fields["number"]
			delete(fields, "number")
			result = fields
		} else {
			result = fields
		}
	case "eth_getCode", "eth_getBalance", "eth_call":
		if len(req.Params) != 2 {
			f.t.Error("query missing pinned block")
			return
		}
		var pin struct {
			Hash      string `json:"blockHash"`
			Canonical bool   `json:"requireCanonical"`
		}
		if json.Unmarshal(req.Params[1], &pin) != nil || pin.Hash != f.header.Hash().Hex() || !pin.Canonical {
			f.t.Error("unbound or non-canonical read")
		}
		if req.Method == "eth_getBalance" {
			var address string
			_ = json.Unmarshal(req.Params[0], &address)
			if address != walletAddress {
				f.t.Error("wrong wallet balance")
			}
			result = "0x1bc16d674ec80000" // 2 native coins.
		} else if req.Method == "eth_getCode" {
			var address string
			_ = json.Unmarshal(req.Params[0], &address)
			result = "0x070809"
			if address == FactoryAddress {
				result = "0x010203"
				if f.fault == "factory_drift" {
					result = "0xdead"
				}
			}
			if address == curveAddress {
				result = "0x040506"
				if f.fault == "curve_drift" {
					result = "0xdead"
				}
			}
			if f.fault == "empty_token_code" && address == tokenAddress {
				result = "0x"
			}
		} else {
			var call struct {
				To   string `json:"to"`
				Data string `json:"data"`
			}
			_ = json.Unmarshal(req.Params[0], &call)
			decoded, err := hexutil.Decode(call.Data)
			if err != nil || len(decoded) < 4 {
				f.t.Error("invalid calldata")
				return
			}
			sig := hexutil.Encode(decoded[:4])
			var data []byte
			switch sig {
			case hexutil.Encode(selector("getLaunchedToken(address)")):
				if call.To != FactoryAddress || !bytes.Equal(decoded[4:], addrWord(tokenAddress)) {
					f.t.Error("caller-supplied factory/token")
				}
				data = make([]byte, 15*32)
				copy(data, addrWord(tokenAddress))
				copy(data[32:], addrWord(curveAddress))
				copy(data[14*32:], uintWord(1))
				switch f.fault {
				case "unregistered":
					copy(data[14*32:], uintWord(0))
				case "wrong_registered_token":
					copy(data, addrWord(walletAddress))
				case "non_native_quote":
					copy(data[4*32:], addrWord(walletAddress))
				case "migrated":
					copy(data[10*32:], uintWord(1))
				case "bad_phase":
					copy(data[10*32:], uintWord(256))
				case "bad_exists":
					copy(data[14*32:], uintWord(2))
				case "bad_tuple":
					data = data[:len(data)-1]
				case "bad_address_padding":
					data[0] = 1
				case "bad_pool_fee":
					data[6*32] = 1
				case "bad_tick_sign_extension":
					data[7*32] = 1
				case "bad_buyback_bool":
					copy(data[9*32:], uintWord(2))
				}
			case hexutil.Encode(selector("token()")):
				data = addrWord(tokenAddress)
				if f.fault == "curve_token_contradiction" {
					data = addrWord(walletAddress)
				}
			case hexutil.Encode(selector("factory()")):
				data = addrWord(FactoryAddress)
				if f.fault == "curve_factory_contradiction" {
					data = addrWord(walletAddress)
				}
			case hexutil.Encode(selector("pairToken()")):
				data = make([]byte, 32)
				if f.fault == "curve_pair_contradiction" {
					data = addrWord(walletAddress)
				}
			case hexutil.Encode(selector("graduated()")):
				data = uintWord(0)
				if f.fault == "graduated" {
					data = uintWord(1)
				}
				if f.fault == "bad_graduated_bool" {
					data = uintWord(2)
				}
			case hexutil.Encode(selector("isNativeQuote()")):
				data = uintWord(1)
				if f.fault == "curve_not_native" {
					data = uintWord(0)
				}
			case hexutil.Encode(selector("decimals()")):
				data = uintWord(6)
				if f.fault == "bad_decimals" {
					data = uintWord(256)
				}
				if f.fault == "short_decimals" {
					data = data[1:]
				}
			case hexutil.Encode(selector("name()")):
				data = textWord("Example Token")
				if f.fault == "bad_text_offset" {
					data[31] = 64
				}
				if f.fault == "bad_text_padding" {
					data[len(data)-1] = 1
				}
				if f.fault == "text_control" {
					data = textWord("Token\nName")
				}
			case hexutil.Encode(selector("symbol()")):
				data = textWord("EXAMPLE")
				if f.fault == "bytes32_symbol" {
					data = common.RightPadBytes([]byte("EXAMPLE"), 32)
				}
			case hexutil.Encode(selector("balanceOf(address)")):
				if call.To != tokenAddress || !bytes.Equal(decoded[4:], addrWord(walletAddress)) {
					f.t.Error("wrong balance call")
				}
				data = uintWord(1500500000)
				if f.fault == "short_balance" {
					data = data[:31]
				}
			case hexutil.Encode(selector("allowance(address,address)")):
				if call.To != tokenAddress || !bytes.Equal(decoded[4:], append(addrWord(walletAddress), addrWord(curveAddress)...)) {
					f.t.Error("wrong allowance spender/owner")
				}
				data = uintWord(1000000000)
				if f.fault == "zero_allowance" {
					data = uintWord(0)
				}
				if f.fault == "short_allowance" {
					data = data[:31]
				}
			default:
				f.t.Errorf("prohibited eth_call selector: %s", sig)
				return
			}
			result = hexutil.Encode(data)
		}
	case "eth_maxPriorityFeePerGas":
		result = "0x5f5e100" // 0.1 Gwei.
		if f.fault == "bad_tip" {
			result = "0x00"
		}
	default:
		f.t.Errorf("prohibited RPC method: %s", req.Method)
		return
	}
	if f.fault == "null_result" {
		result = nil
	}
	if f.fault == "duplicate_envelope" {
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"id":%d,"result":"0x1237"}`, req.ID, req.ID)
		return
	}
	if f.fault == "bad_id" {
		req.ID++
	}
	if f.fault == "result_and_error" {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result, "error": nil})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

func TestHTTPBControlledRPCQueries(t *testing.T) {
	c, f := setup(t)
	info, err := c.Token(context.Background(), tokenAddress)
	if err != nil || info.Decimals != 6 || info.Protocol != "pons_v2_curve" || info.Curve != curveAddress || info.SendAuthorized || info.Name != "Example Token" || info.Symbol != "EXAMPLE" || info.Block.Hash != f.header.Hash().Hex() {
		t.Fatalf("token %+v %v", info, err)
	}
	balances, err := c.Balances(context.Background(), walletAddress, tokenAddress)
	if err != nil || balances.NativeAmount != "2" || balances.TokenAmount != "1500.5" || balances.TokenUnits != "1500500000" || balances.Token.Block != balances.Block || balances.AllowanceAmount != "1000" || balances.AllowanceLimitedTokenAmount != "1000" {
		t.Fatalf("balances %+v %v", balances, err)
	}
	fees, err := c.Fees(context.Background())
	if err != nil || fees.BaseGwei != "1" || fees.SuggestedMaxGwei != "2.1" || fees.SuggestedTipGwei != "0.1" || fees.GasBudgetAvailable || !fees.ReferenceOnly || fees.TipSource != "PROVIDER_ESTIMATE_NOT_BLOCK_PINNED" {
		t.Fatalf("fees %+v %v", fees, err)
	}
	if _, err = c.Balances(context.Background(), walletAddress, ""); err != nil {
		t.Fatal(err)
	}
	if err = c.rpc(context.Background(), "eth_sendRawTransaction", []any{"0x00"}, new(string)); err == nil {
		t.Fatal("send escaped allowlist")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.methods["eth_sendRawTransaction"] != 0 || f.methods["eth_getTransactionCount"] != 0 {
		t.Fatal("send/nonce call occurred")
	}
}

func TestHTTPBObservationFaultsFailClosed(t *testing.T) {
	for _, fault := range []string{"wrong_chain", "chain_drift", "reorg", "wrong_block", "missing_basefee", "wrong_hash", "duplicate_header", "header_case_alias", "noncanonical_header_member", "noncanonical_quantity", "factory_drift", "curve_drift", "empty_token_code", "unregistered", "wrong_registered_token", "non_native_quote", "migrated", "bad_phase", "bad_exists", "bad_tuple", "bad_address_padding", "bad_pool_fee", "bad_tick_sign_extension", "bad_buyback_bool", "curve_token_contradiction", "curve_factory_contradiction", "curve_pair_contradiction", "graduated", "bad_graduated_bool", "curve_not_native", "bad_decimals", "short_decimals", "bad_text_offset", "bad_text_padding", "text_control", "null_result", "duplicate_envelope", "bad_id", "result_and_error", "http_failure", "rpc_error", "oversized", "redirect"} {
		t.Run(fault, func(t *testing.T) {
			c, f := setup(t)
			f.fault = fault
			r, err := c.Token(context.Background(), tokenAddress)
			if err == nil || r.Address != "" || strings.Contains(err.Error(), "secret") {
				t.Fatalf("partial or trusted fault: %+v %v", r, err)
			}
		})
	}
	for _, fault := range []string{"short_balance", "short_allowance", "chain_drift", "reorg", "wrong_hash"} {
		t.Run("balance_"+fault, func(t *testing.T) {
			c, f := setup(t)
			f.fault = fault
			r, err := c.Balances(context.Background(), walletAddress, tokenAddress)
			if err == nil || r.NativeAmount != "" {
				t.Fatal("partial balance exposed")
			}
		})
	}
	for _, fault := range []string{"bad_tip", "chain_drift", "reorg", "wrong_hash"} {
		t.Run("fee_"+fault, func(t *testing.T) {
			c, f := setup(t)
			f.fault = fault
			r, err := c.Fees(context.Background())
			if err == nil || r.SuggestedMaxGwei != "" {
				t.Fatal("unsafe fee exposed")
			}
		})
	}
}

func TestHTTPBTimeoutCancellationAndNoRedirect(t *testing.T) {
	c, f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Fees(ctx); err == nil {
		t.Fatal("cancellation ignored")
	}
	f.fault = "timeout"
	c.http.Timeout = 10 * time.Millisecond
	if _, err := c.Fees(context.Background()); err == nil {
		t.Fatal("timeout ignored")
	}
}

func TestHTTPBConcurrentReferenceQueries(t *testing.T) {
	c, _ := setup(t)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Fees(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestHTTPBPinnedBaselineMatchesRepository(t *testing.T) {
	body, err := os.ReadFile("../../configs/mainnet/pons-curve-feed.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Status    string `json:"status"`
		ChainID   int    `json:"chain_id"`
		Contracts []struct {
			Address        string `json:"address"`
			Role           string `json:"role"`
			Hash           string `json:"runtime_code_hash"`
			Trusted        bool   `json:"trusted_for_feed"`
			Classification string `json:"classification"`
		} `json:"contracts"`
	}
	if err = json.Unmarshal(body, &lock); err != nil || lock.Status != "C02_PONS_CURVE_PASS" || lock.ChainID != ChainID {
		t.Fatal("untrusted query baseline")
	}
	factory, curve := false, false
	for _, entry := range lock.Contracts {
		if entry.Role == "pons_factory" {
			factory = entry.Address == FactoryAddress && entry.Hash == FactoryCodeHash && entry.Trusted && entry.Classification == "direct_implementation"
		}
		if entry.Role == "pons_curve" {
			curve = entry.Hash == CurveCodeHash && entry.Trusted && entry.Classification == "direct_implementation"
		}
	}
	if !factory || !curve {
		t.Fatal("query identity drifted from locked baseline")
	}
	c, err := New("http://127.0.0.1:1")
	if err != nil || c.factoryCodeHash.Hex() != FactoryCodeHash || c.curveCodeHash.Hex() != CurveCodeHash {
		t.Fatal("production constructor does not pin identities")
	}
}

func TestHTTPBNoEconomicSurface(t *testing.T) {
	typeOf := reflect.TypeOf((*Client)(nil))
	for i := 0; i < typeOf.NumMethod(); i++ {
		switch typeOf.Method(i).Name {
		case "Balances", "Fees", "Token":
		default:
			t.Fatal("extra exported capability")
		}
	}
	for _, file := range []string{"client.go", "queries.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.Contains(path, "trade-sdk") || strings.Contains(path, "internal/trade") || strings.Contains(path, "ethclient") || strings.Contains(path, "/rpc") {
				t.Fatal("broad capability dependency")
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if s, ok := n.(*ast.SelectorExpr); ok {
				switch s.Sel.Name {
				case "SignTransaction", "SendRawTransaction", "SendTransaction", "SignTx", "NewTx", "ReserveExecution", "PendingNonceAt", "EstimateGas":
					t.Errorf("economic capability: %s", s.Sel.Name)
				}
			}
			return true
		})
	}
}

func TestHTTPBStrictUnitsAndMetadata(t *testing.T) {
	for _, tc := range []struct {
		x    int64
		d    uint8
		want string
	}{{0, 18, "0"}, {1, 18, "0.000000000000000001"}, {1500500000, 6, "1500.5"}, {1, 0, "1"}} {
		if got := Units(big.NewInt(tc.x), tc.d); got != tc.want {
			t.Fatalf("units %s", got)
		}
	}
	c, f := setup(t)
	f.fault = "bytes32_symbol"
	if r, err := c.Token(context.Background(), tokenAddress); err != nil || r.Symbol != "EXAMPLE" {
		t.Fatal("bytes32 metadata")
	}
	for _, endpoint := range []string{"", "file:///tmp/rpc", "ws://localhost/rpc", "http://user:pass@localhost", "https://localhost/#secret"} {
		if _, err := New(endpoint); err == nil {
			t.Fatal("invalid endpoint accepted")
		}
	}
	for _, address := range []string{"", strings.TrimPrefix(tokenAddress, "0x"), "0X" + tokenAddress[2:], "0x0000000000000000000000000000000000000000"} {
		if _, err := Address(address); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
}
