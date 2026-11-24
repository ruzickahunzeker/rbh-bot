package evm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

func TestBatchRPCClientSinglePostAndLaggingBlockGuard(t *testing.T) {
	var httpPosts atomic.Int64
	var currentBlockHex atomic.Value
	currentBlockHex.Store("0x64") // block 100

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpPosts.Add(1)
		var batch []struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil || len(batch) != 4 {
			http.Error(w, "expected 4-call JSON-RPC batch", 400)
			return
		}
		resp := []map[string]any{
			{"jsonrpc": "2.0", "id": 1, "result": currentBlockHex.Load().(string)},
			{"jsonrpc": "2.0", "id": 2, "result": "0x1234"},
			{"jsonrpc": "2.0", "id": 3, "result": "0x5"},
			{"jsonrpc": "2.0", "id": 4, "result": "0x5"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewBatchRPCClient(srv.URL, 2*time.Second)
	mc3 := common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")
	wallet := common.HexToAddress("0x1000000000000000000000000000000000000001")

	// AT-22: Wallet's previous BUY confirmed at block 101; RPC node lags at block 100 -> ErrRPCStateLagging.
	if _, err := client.FetchMulticallAndNonces(context.Background(), mc3, []byte{0xaa}, wallet, 101); !errors.Is(err, ErrRPCStateLagging) {
		t.Fatalf("expected ErrRPCStateLagging when node at 100 < required 101, got %v", err)
	}
	if httpPosts.Load() != 1 {
		t.Fatalf("expected 1 single HTTP batch POST, got %d", httpPosts.Load())
	}

	// Once RPC catches up to block 101 (or required dynamically drops to 100 on Reorg), call succeeds.
	currentBlockHex.Store("0x65") // block 101
	snap, err := client.FetchMulticallAndNonces(context.Background(), mc3, []byte{0xaa}, wallet, 101)
	if err != nil {
		t.Fatalf("expected caught-up snapshot to succeed: %v", err)
	}
	if snap.BlockNumber != 101 || snap.LatestNonce != 5 || snap.PendingNonce != 5 || len(snap.MulticallData) != 2 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if httpPosts.Load() != 2 {
		t.Fatalf("expected 2 total HTTP batch POSTs, got %d", httpPosts.Load())
	}
}
