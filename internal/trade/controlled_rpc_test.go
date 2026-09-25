package trade

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type controlledRPC struct {
	mu       sync.Mutex
	mode     string
	accepted map[string][]byte
	sends    int
	receipt  bool
	server   *httptest.Server
}

type rpcRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      json.RawMessage   `json:"id"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

func newControlledRPC(t *testing.T, mode string) *controlledRPC {
	t.Helper()
	h := &controlledRPC{mode: mode, accepted: map[string][]byte{}}
	h.server = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.server.Close)
	return h
}

func (h *controlledRPC) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req rpcRequest
	if json.Unmarshal(body, &req) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	write := func(result any, rpcErr any) {
		response := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID)}
		if rpcErr != nil {
			response["error"] = rpcErr
		} else {
			response["result"] = result
		}
		_ = json.NewEncoder(w).Encode(response)
	}
	switch req.Method {
	case "eth_sendRawTransaction":
		var encoded string
		_ = json.Unmarshal(req.Params[0], &encoded)
		raw, err := hex.DecodeString(trim0x(encoded))
		if err != nil {
			write(nil, map[string]any{"code": -32602, "message": "invalid transaction"})
			return
		}
		var tx types.Transaction
		if tx.UnmarshalBinary(raw) != nil {
			write(nil, map[string]any{"code": -32602, "message": "invalid transaction"})
			return
		}
		hash := tx.Hash().Hex()
		h.mu.Lock()
		_, known := h.accepted[hash]
		h.sends++
		if h.mode != "deterministic_reject" && h.mode != "hash_mismatch" {
			h.accepted[hash] = append([]byte(nil), raw...)
		}
		h.mu.Unlock()
		if h.mode == "deterministic_reject" {
			write(nil, map[string]any{"code": -32000, "message": "insufficient funds for gas * price + value"})
			return
		}
		if h.mode == "accept_timeout" {
			if hijacker, ok := w.(http.Hijacker); ok {
				conn, _, _ := hijacker.Hijack()
				_ = conn.Close()
				return
			}
		}
		if h.mode == "hash_mismatch" {
			write(common.HexToHash("0xdead").Hex(), nil)
			return
		}
		if known {
			write(nil, map[string]any{"code": -32000, "message": "already known"})
			return
		}
		write(hash, nil)
	case "eth_getTransactionByHash":
		var hash string
		_ = json.Unmarshal(req.Params[0], &hash)
		h.mu.Lock()
		raw, ok := h.accepted[hash]
		h.mu.Unlock()
		if !ok {
			write(nil, nil)
			return
		}
		var tx types.Transaction
		_ = tx.UnmarshalBinary(raw)
		encoded, _ := tx.MarshalJSON()
		var response map[string]any
		_ = json.Unmarshal(encoded, &response)
		response["blockHash"] = nil
		response["blockNumber"] = nil
		response["transactionIndex"] = nil
		write(response, nil)
	case "eth_getTransactionReceipt":
		var hash string
		_ = json.Unmarshal(req.Params[0], &hash)
		h.mu.Lock()
		_, ok := h.accepted[hash]
		receiptReady := h.receipt
		h.mu.Unlock()
		if !ok || !receiptReady {
			write(nil, nil)
			return
		}
		write(map[string]any{"transactionHash": hash, "transactionIndex": "0x0", "blockHash": controlledHeader().Hash().Hex(), "blockNumber": "0x64", "from": common.Address{}.Hex(), "to": testCurve.Hex(), "cumulativeGasUsed": "0x186a0", "gasUsed": "0x186a0", "contractAddress": nil, "logs": []any{}, "logsBloom": "0x" + strings.Repeat("0", 512), "status": "0x1", "type": "0x2", "effectiveGasPrice": "0xa"}, nil)
	case "eth_getTransactionCount":
		write("0x7", nil)
	case "eth_getBlockByNumber":
		encoded, _ := controlledHeader().MarshalJSON()
		var block map[string]any
		_ = json.Unmarshal(encoded, &block)
		block["transactions"] = []any{}
		block["uncles"] = []any{}
		write(block, nil)
	case "eth_chainId":
		write("0x1237", nil)
	default:
		write(nil, map[string]any{"code": -32601, "message": "method not found"})
	}
}

func controlledHeader() *types.Header {
	return &types.Header{ParentHash: common.HexToHash("0xdef"), UncleHash: types.EmptyUncleHash, Root: common.Hash{}, TxHash: types.EmptyTxsHash, ReceiptHash: types.EmptyReceiptsHash, Difficulty: big.NewInt(0), Number: big.NewInt(100), GasLimit: 30_000_000, Time: 100, BaseFee: big.NewInt(1)}
}

func (h *controlledRPC) has(hash string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.accepted[hash]
	return ok
}
func (h *controlledRPC) raw(hash string) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]byte(nil), h.accepted[hash]...)
}
func (h *controlledRPC) enableReceipt() { h.mu.Lock(); h.receipt = true; h.mu.Unlock() }
func trim0x(s string) string {
	if len(s) >= 2 && s[:2] == "0x" {
		return s[2:]
	}
	return s
}

func artifactRaw(t *testing.T, store *Store, kernel *ExecutionKernel, a SignedArtifact) []byte {
	t.Helper()
	stored, found, err := store.LoadEncryptedArtifact(context.Background(), a.Operation)
	if err != nil || !found {
		t.Fatal(err)
	}
	raw, err := kernel.cipher.Decrypt(stored.KeyVersion, stored.Ciphertext, stored.EncryptionNonce, artifactAAD(stored.Operation, stored.StepID, stored.AttemptID))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestControlledRPCSubmissionScenarios(t *testing.T) {
	t.Run("normal_and_already_known", func(t *testing.T) {
		store, kernel, a, closeDB := seededSignedArtifact(t)
		defer closeDB()
		h := newControlledRPC(t, "normal")
		backend, err := DialRPCBackend(context.Background(), h.server.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer backend.Close()
		svc, _ := NewSubmissionService(store, kernel, backend)
		result, err := svc.Submit(context.Background(), a.Operation)
		if err != nil || result.State != "submitted" || !h.has(a.TxHash) {
			t.Fatalf("result=%#v err=%v", result, err)
		}
		raw := artifactRaw(t, store, kernel, a)
		if !bytes.Equal(raw, h.raw(a.TxHash)) {
			t.Fatal("captured raw mismatch")
		}
		hash, err := backend.SendRawTransaction(context.Background(), raw)
		if err != nil || hash != a.TxHash {
			t.Fatalf("already known hash=%s err=%v", hash, err)
		}
		assertAttemptCount(t, store, 1)
	})
	t.Run("hash_mismatch", func(t *testing.T) {
		store, kernel, a, closeDB := seededSignedArtifact(t)
		defer closeDB()
		h := newControlledRPC(t, "hash_mismatch")
		backend, _ := DialRPCBackend(context.Background(), h.server.URL)
		defer backend.Close()
		svc, _ := NewSubmissionService(store, kernel, backend)
		if _, err := svc.Submit(context.Background(), a.Operation); !errors.Is(err, ErrRPCHashMismatch) {
			t.Fatal(err)
		}
		var lane string
		_ = store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id=?`, a.WalletID).Scan(&lane)
		if lane != "frozen" {
			t.Fatalf("lane=%s", lane)
		}
	})
	t.Run("deterministic_rejection", func(t *testing.T) {
		store, kernel, a, closeDB := seededSignedArtifact(t)
		defer closeDB()
		h := newControlledRPC(t, "deterministic_reject")
		backend, _ := DialRPCBackend(context.Background(), h.server.URL)
		defer backend.Close()
		svc, _ := NewSubmissionService(store, kernel, backend)
		if _, err := svc.Submit(context.Background(), a.Operation); !errors.Is(err, ErrBroadcastRejected) {
			t.Fatal(err)
		}
		latest, _, _ := store.LatestSubmission(context.Background(), a.AttemptID)
		if latest.State == "broadcast_unknown" {
			t.Fatal("deterministic rejection classified ambiguous")
		}
	})
	t.Run("accept_then_disconnect_recover_replay", func(t *testing.T) {
		store, kernel, a, closeDB := seededSignedArtifact(t)
		defer closeDB()
		h := newControlledRPC(t, "accept_timeout")
		backend, _ := DialRPCBackend(context.Background(), h.server.URL)
		defer backend.Close()
		svc, _ := NewSubmissionService(store, kernel, backend)
		if _, err := svc.Submit(context.Background(), a.Operation); !errors.Is(err, ErrBroadcastAmbiguous) {
			t.Fatal(err)
		}
		if !h.has(a.TxHash) {
			t.Fatal("remote did not accept transaction")
		}
		remoteTx, pending, queryErr := backend.eth.TransactionByHash(context.Background(), common.HexToHash(a.TxHash))
		if queryErr != nil || remoteTx.Hash().Hex() != a.TxHash || !pending {
			t.Fatalf("query recovery pending=%v err=%v", pending, queryErr)
		}
		h.mode = "normal"
		restarted, _ := NewSubmissionService(store, kernel, backend)
		result, err := restarted.ReplayUnknown(context.Background(), a.Operation, true)
		if err != nil || result.State != "submitted" {
			t.Fatalf("result=%#v err=%v", result, err)
		}
		if !bytes.Equal(artifactRaw(t, store, kernel, a), h.raw(a.TxHash)) {
			t.Fatal("replay bytes changed")
		}
		h.enableReceipt()
		recovery, _ := NewRecoveryService(store, kernel, backend, fakeEffectResolver{}, CanonicalPolicy{})
		if err = recovery.Reconcile(context.Background(), a.Operation); err != nil {
			t.Fatalf("canonical recovery: %v", err)
		}
		var active int
		_ = store.db.QueryRow(`SELECT COUNT(*) FROM position_effects WHERE state='active'`).Scan(&active)
		if active != 1 {
			t.Fatalf("active effects=%d", active)
		}
		assertAttemptCount(t, store, 1)
	})
}
