package trade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type transientRecoveryHealth struct {
	mu     sync.Mutex
	calls  int
	failed chan struct{}
}

func (h *transientRecoveryHealth) CheckRecoveryHealth(context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	if h.calls == 2 {
		close(h.failed)
		return errors.New("temporary RPC failure")
	}
	return nil
}

func TestW4CTerminalHealthFailureCannotReviveReadiness(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	query := &productionRecoveryQueryFake{entered: entered, release: release}
	store, c, _, now := productionRecoveryCompositionFixture(t, query, nil)
	query.evidence = ControlledRecoveryEvidence{NonceState: "RESERVED_UNRESOLVED", EvidenceHash: strings.Repeat("f", 64), ObservedAt: now}
	probe := &transientRecoveryHealth{failed: make(chan struct{})}
	c.recovery.health = probe
	c.recovery.config.LeaseTTL = 120 * time.Millisecond
	c.now = func() time.Time { return now.Add(100 * time.Millisecond) }
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	<-entered
	if err := c.Run(ctx); !errors.Is(err, ErrProductionCompositionRejected) {
		t.Fatalf("duplicate lifecycle allowed: %v", err)
	}
	<-probe.failed
	// Keep the scan blocked across several would-be successful renewals.
	time.Sleep(150 * time.Millisecond)
	if c.Readiness().RecoveryReady {
		t.Error("terminal drain revived readiness")
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer waitCancel()
	if err := c.Wait(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("in-flight drain unexpectedly done: %v", err)
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("expected terminal failure")
	}
	if c.Readiness().RecoveryReady {
		t.Fatal("terminal exit left readiness true")
	}
	if err := c.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	var audits, alerts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE reason_code='RECOVERY_HEALTH_UNAVAILABLE'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM canary_alert_outbox o JOIN canary_runtime_audit a ON a.id=o.audit_id WHERE a.reason_code='RECOVERY_HEALTH_UNAVAILABLE'`).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if audits != 1 || alerts != 1 {
		t.Fatalf("audits=%d alerts=%d", audits, alerts)
	}
}

// Every fixture RPC observation is generated locally; this handler has no send path.
func receiptIdentityRPC(t *testing.T, receipt *types.Receipt) *ReadOnlyRecoveryRPC {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch req.Method {
		case "eth_getTransactionByHash":
			result = nil
		case "eth_getTransactionReceipt":
			result = receipt
		case "eth_chainId":
			result = "0x1237"
		default:
			t.Errorf("unexpected RPC (including prohibited send): %s", req.Method)
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "not allowed"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	rpc, err := DialReadOnlyRecoveryRPC(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rpc.Close)
	return rpc
}

func TestW4CContradictoryReceiptCannotResolveAmbiguity(t *testing.T) {
	for _, status := range []uint64{0, 1} {
		for _, identity := range []string{"wrong_hash", "zero_hash", "missing_block", "wrong_log_identity", "removed_log"} {
			t.Run(identity+string(rune('0'+status)), func(t *testing.T) {
				store, c, artifact, _ := productionRecoveryCompositionFixture(t, nil, nil)
				receipt := &types.Receipt{Status: status, TxHash: common.HexToHash(artifact.TxHash), BlockHash: common.HexToHash("0xabc"), BlockNumber: big.NewInt(100), Logs: []*types.Log{}}
				switch identity {
				case "wrong_hash":
					receipt.TxHash = common.HexToHash("0xdead")
				case "zero_hash":
					receipt.TxHash = common.Hash{}
				case "missing_block":
					receipt.BlockNumber = nil
				case "wrong_log_identity":
					receipt.Logs = []*types.Log{{TxHash: common.HexToHash("0xdead"), BlockHash: receipt.BlockHash, BlockNumber: 100}}
				case "removed_log":
					receipt.Logs = []*types.Log{{Removed: true, TxHash: receipt.TxHash, BlockHash: receipt.BlockHash, BlockNumber: 100}}
				}
				rpc := receiptIdentityRPC(t, receipt)
				c.recovery.recovery.backend = rpc
				querier, err := NewProductionRecoveryQuerier(store, c.recovery.recovery.cipher, rpc)
				if err != nil {
					t.Fatal(err)
				}
				c.recovery.querier = querier
				if err = c.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err = c.Run(context.Background()); err == nil {
					t.Fatal("contradictory receipt accepted")
				}
				if c.Readiness().RecoveryReady {
					t.Fatal("fault kept recovery ready")
				}
				assertFrozenRecovery(t, store, artifact)
				var effects, audits, alerts, evidence int
				for _, row := range []struct {
					sql  string
					dest *int
				}{
					{`SELECT COUNT(*) FROM position_effects`, &effects},
					{`SELECT COUNT(*) FROM canary_recovery_evidence`, &evidence},
					{`SELECT COUNT(*) FROM canary_runtime_audit WHERE reason_code='RECOVERY_EVIDENCE_UNAVAILABLE'`, &audits},
					{`SELECT COUNT(*) FROM canary_alert_outbox o JOIN canary_runtime_audit a ON a.id=o.audit_id WHERE a.reason_code='RECOVERY_EVIDENCE_UNAVAILABLE'`, &alerts},
				} {
					if err := store.db.QueryRow(row.sql).Scan(row.dest); err != nil {
						t.Fatal(err)
					}
				}
				if effects != 0 || evidence != 0 || audits != 1 || alerts != 1 {
					t.Fatalf("effects=%d evidence=%d audits=%d alerts=%d", effects, evidence, audits, alerts)
				}
			})
		}
	}
}

func assertFrozenRecovery(t *testing.T, store *Store, a SignedArtifact) {
	t.Helper()
	var lane, reservation string
	if err := store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id=?`, a.WalletID).Scan(&lane); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id=?`, a.Operation).Scan(&reservation); err != nil {
		t.Fatal(err)
	}
	if lane != "frozen" || reservation != "frozen" {
		t.Fatalf("lane=%s reservation=%s", lane, reservation)
	}
}

func TestW4CReceiptValidationRejectsMalformedEvidence(t *testing.T) {
	hash := common.HexToHash("0x1234")
	for _, kind := range []string{"zero_block_hash", "negative_block", "zero_block", "overflow_block", "invalid_status", "post_state", "nil_log"} {
		t.Run(kind, func(t *testing.T) {
			r := &types.Receipt{TxHash: hash, BlockHash: common.HexToHash("0xabc"), BlockNumber: big.NewInt(100)}
			if err := validateRecoveryReceipt(r, hash); err != nil {
				t.Fatalf("valid receipt rejected: %v", err)
			}
			switch kind {
			case "zero_block_hash":
				r.BlockHash = common.Hash{}
			case "negative_block":
				r.BlockNumber = big.NewInt(-1)
			case "zero_block":
				r.BlockNumber = new(big.Int)
			case "overflow_block":
				r.BlockNumber = new(big.Int).Lsh(big.NewInt(1), 64)
			case "invalid_status":
				r.Status = 2
			case "post_state":
				r.PostState = []byte{1}
			case "nil_log":
				r.Logs = []*types.Log{nil}
			}
			if err := validateRecoveryReceipt(r, hash); !errors.Is(err, ErrArtifactIntegrity) {
				t.Fatalf("malformed receipt accepted: %v", err)
			}
		})
	}
}

func TestW4CProductionLifecycleSQLiteReopen(t *testing.T) {
	for _, state := range []string{"submitted", "broadcast_unknown"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			store, kernel, a, closeDB := seededSignedArtifact(t)
			defer closeDB()
			cipher := kernel.cipher
			var seq int
			var name, path string
			if err := store.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
			sub, err := store.BeginSubmission(ctx, a, false, now)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.FinishSubmission(ctx, a, sub, state, a.TxHash, "", "", now); err != nil {
				t.Fatal(err)
			}
			before, found, err := store.LoadEncryptedArtifact(ctx, a.Operation)
			if err != nil || !found {
				t.Fatal(err)
			}
			makeComposition := func(s *Store, holder string, clock time.Time, backend ReceiptBackend) *ProductionComposition {
				t.Helper()
				r, err := NewRecoveryServiceWithCipher(s, cipher, backend, fakeEffectResolver{}, CanonicalPolicy{})
				if err != nil {
					t.Fatal(err)
				}
				c, err := NewProductionComposition(s, ProductionModeDisabled)
				if err != nil {
					t.Fatal(err)
				}
				c.now = func() time.Time { return clock }
				q := &productionRecoveryQueryFake{evidence: ControlledRecoveryEvidence{NonceState: "RESERVED_UNRESOLVED", EvidenceHash: strings.Repeat("d", 64), ObservedAt: clock}}
				if err = c.ConfigureRecovery(r, q, &productionRecoveryHealthFake{}, ProductionRecoveryConfig{Environment: "production", HolderID: holder, LeaseTTL: time.Minute, ScanInterval: time.Millisecond}); err != nil {
					t.Fatal(err)
				}
				if err = c.Start(ctx); err != nil {
					t.Fatal(err)
				}
				return c
			}
			first := makeComposition(store, "before-restart", now, &fakeReceiptBackend{})
			drainOneProductionScan(t, first)
			if err = store.db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := storage.Open(ctx, storage.TradeOwner, path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = db.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			reopened, err := NewStore(db)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = reopened.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1 WHERE singleton=1`); err != nil {
				t.Fatal(err)
			}
			backend := &fakeReceiptBackend{}
			second := makeComposition(reopened, "after-restart", now.Add(2*time.Minute), backend)
			if second.worker.epoch != first.worker.epoch+1 {
				t.Fatalf("epoch did not advance: %d->%d", first.worker.epoch, second.worker.epoch)
			}
			if err = first.worker.RunOnce(ctx); err == nil {
				t.Fatal("closed stale worker mutated")
			}
			items, err := reopened.ListControlledRecoveryItems(ctx)
			if err != nil || len(items) != 1 || items[0].OperationID != a.Operation {
				t.Fatalf("rediscovered=%v err=%v", items, err)
			}
			drainOneProductionScan(t, second)
			if state == "broadcast_unknown" {
				assertFrozenRecovery(t, reopened, a)
			}
			// The same restarted production worker can reconcile canonical/reorg
			// outcomes under its durable lease, independent of stopped controls.
			header := &types.Header{Number: big.NewInt(100), Extra: []byte("canonical")}
			backend.receipt = &types.Receipt{TxHash: common.HexToHash(a.TxHash), Status: 1, BlockNumber: big.NewInt(100), BlockHash: header.Hash()}
			backend.canonical = header
			backend.latest = &types.Header{Number: big.NewInt(102)}
			q := second.worker.querier.(*productionRecoveryQueryFake)
			q.evidence.TxFound = true
			q.evidence.ReceiptFound = true
			q.evidence.EvidenceHash = strings.Repeat("e", 64)
			for range 2 {
				if err = second.worker.RunOnce(ctx); err != nil {
					t.Fatal(err)
				}
			}
			backend.canonical = &types.Header{Number: big.NewInt(100), Extra: []byte("reorg")}
			if err = second.worker.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			backend.canonical = header
			for range 2 {
				if err = second.worker.RunOnce(ctx); err != nil {
					t.Fatal(err)
				}
			}
			for _, transition := range []string{"apply", "rollback", "reapply"} {
				var count int
				if err = reopened.db.QueryRow(`SELECT COUNT(*) FROM position_effect_history WHERE transition=?`, transition).Scan(&count); err != nil || count != 1 {
					t.Fatalf("%s=%d err=%v", transition, count, err)
				}
			}
			after, found, err := reopened.LoadEncryptedArtifact(ctx, a.Operation)
			if err != nil || !found || after.TxHash != before.TxHash || after.Nonce != before.Nonce || after.AttemptID != before.AttemptID || !bytes.Equal(after.Ciphertext, before.Ciphertext) || !bytes.Equal(after.EncryptionNonce, before.EncryptionNonce) {
				t.Fatalf("artifact changed: %v", err)
			}
			assertAttemptCount(t, reopened, 1)
			var authorizations, permits int
			if err = reopened.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_authorizations`).Scan(&authorizations); err != nil {
				t.Fatal(err)
			}
			if err = reopened.db.QueryRow(`SELECT COUNT(*) FROM canary_send_permits`).Scan(&permits); err != nil {
				t.Fatal(err)
			}
			if authorizations != 0 || permits != 0 {
				t.Fatalf("authorization=%d permits=%d", authorizations, permits)
			}
			if ready := second.Readiness(); ready.CanaryAdmissionReady || ready.SubmissionSendReady || ready.RecoveryReady {
				t.Fatalf("drained readiness=%+v", ready)
			}
		})
	}
}

type recoveryIdentityQueryFake struct {
	tx         *types.Transaction
	receipt    *types.Receipt
	nonceCalls int
}

func (f *recoveryIdentityQueryFake) TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error) {
	return f.tx, false, nil
}
func (f *recoveryIdentityQueryFake) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	return f.receipt, nil
}
func (f *recoveryIdentityQueryFake) PendingNonceAt(context.Context, common.Address) (uint64, error) {
	f.nonceCalls++
	return 7, nil
}

func TestW4CQuerierRejectsWrongObservationIdentity(t *testing.T) {
	for _, kind := range []string{"transaction", "receipt"} {
		t.Run(kind, func(t *testing.T) {
			store, c, a, _ := productionRecoveryCompositionFixture(t, nil, nil)
			fake := &recoveryIdentityQueryFake{}
			if kind == "transaction" {
				fake.tx = types.NewTx(&types.DynamicFeeTx{ChainID: new(big.Int).SetUint64(ChainID), Nonce: 999})
			} else {
				fake.receipt = &types.Receipt{TxHash: common.HexToHash("0xdead"), BlockHash: common.HexToHash("0xabc"), BlockNumber: big.NewInt(100)}
			}
			q, err := NewProductionRecoveryQuerier(store, c.recovery.recovery.cipher, fake)
			if err != nil {
				t.Fatal(err)
			}
			_, err = q.QueryRecovery(context.Background(), ControlledRecoveryQuery{OperationID: a.Operation, AttemptID: a.AttemptID, TxHash: a.TxHash})
			if !errors.Is(err, ErrArtifactIntegrity) || fake.nonceCalls != 0 {
				t.Fatalf("err=%v nonce_queries=%d", err, fake.nonceCalls)
			}
			assertFrozenRecovery(t, store, a)
		})
	}
}

func drainOneProductionScan(t *testing.T, c *ProductionComposition) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	c.worker.beforeQuery = func(ControlledRecoveryItem) { once.Do(func() { close(entered); <-release }) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	<-entered
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer waitCancel()
	if err := c.Wait(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("did not wait for fenced scan: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := c.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.worker.beforeQuery = nil
}
