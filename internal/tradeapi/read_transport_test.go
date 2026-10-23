package tradeapi

import (
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
)

// Exercise the actual production read client through authenticated HTTP, not
// just a fake ReadQueries interface. No actual chain endpoint is contacted.
func TestHTTPBRealHTTPToControlledRPCAndDisabledPOST(t *testing.T) {
	_, s, initial, _ := fixture(t)
	header := &types.Header{Number: big.NewInt(100), Time: 1700000000, GasLimit: 10000000, Difficulty: big.NewInt(0), BaseFee: big.NewInt(1000000000)}
	var mu sync.Mutex
	methods := map[string]int{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			ID     uint64            `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			t.Error("invalid RPC request")
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("API credential forwarded")
		}
		mu.Lock()
		methods[q.Method]++
		mu.Unlock()
		var result any
		switch q.Method {
		case "eth_chainId":
			result = "0x1237"
		case "eth_getBlockByNumber":
			result = header
		case "eth_maxPriorityFeePerGas":
			result = "0x5f5e100"
		case "eth_getBalance":
			var address string
			_ = json.Unmarshal(q.Params[0], &address)
			if address != testWallet {
				t.Error("unscoped wallet read")
			}
			var pin struct {
				Hash      string `json:"blockHash"`
				Canonical bool   `json:"requireCanonical"`
			}
			_ = json.Unmarshal(q.Params[1], &pin)
			if pin.Hash != header.Hash().Hex() || !pin.Canonical {
				t.Error("unpinned balance")
			}
			result = "0x1bc16d674ec80000"
		default:
			t.Errorf("unexpected RPC capability %s", q.Method)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result})
	}))
	defer provider.Close()
	queries, err := chainquery.New(provider.URL)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewWithQueries(s, initial.identity, testCredential, DefaultPolicy(), queries)
	if err != nil {
		t.Fatal(err)
	}
	w := call(h, "GET", "/v1/fees?chain=robinhood", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"suggested_max_gwei":"2.1"`) {
		t.Fatal(w.Body.String())
	}
	w = call(h, "GET", "/v1/wallets/wallet-01/balances?chain=robinhood", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"native_amount":"2"`) || !strings.Contains(w.Body.String(), `"sellable_token_amount":null`) {
		t.Fatal(w.Body.String())
	}
	mu.Lock()
	before := 0
	for _, n := range methods {
		before += n
	}
	mu.Unlock()
	record(t, call(h, "POST", "/v1/trades", "never-send", encode(buy())))
	mu.Lock()
	defer mu.Unlock()
	after := 0
	for _, n := range methods {
		after += n
	}
	if after != before || methods["eth_sendRawTransaction"] != 0 || methods["eth_getTransactionCount"] != 0 {
		t.Fatal("POST reached RPC")
	}
	if count(t, s.db, "transaction_attempts") != 0 || count(t, s.db, "operations") != 0 || count(t, s.db, "canary_send_permits") != 0 {
		t.Fatal("query/POST created economic work")
	}
}
