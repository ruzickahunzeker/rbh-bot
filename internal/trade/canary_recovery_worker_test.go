package trade

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type controlledRecoveryQueryFake struct {
	evidence ControlledRecoveryEvidence
	err      error
	calls    int
}

func (f *controlledRecoveryQueryFake) QueryRecovery(context.Context, ControlledRecoveryQuery) (ControlledRecoveryEvidence, error) {
	f.calls++
	return f.evidence, f.err
}

func recoveryWorkerFixture(t *testing.T, state string, receipt *types.Receipt) (*Store, *ControlledRecoveryWorker, *fakeReceiptBackend, SignedArtifact, *controlledRecoveryQueryFake, func()) {
	t.Helper()
	store, kernel, artifact, closeDB := seededSignedArtifact(t)
	now := fixedTime()
	sub, err := store.BeginSubmission(context.Background(), artifact, false, now)
	if err != nil {
		closeDB()
		t.Fatal(err)
	}
	if err = store.FinishSubmission(context.Background(), artifact, sub, state, artifact.TxHash, "", "", now); err != nil {
		closeDB()
		t.Fatal(err)
	}
	header := &types.Header{Number: big.NewInt(100), Extra: []byte("canonical")}
	if receipt != nil {
		receipt.TxHash = common.HexToHash(artifact.TxHash)
		receipt.BlockNumber = big.NewInt(100)
		receipt.BlockHash = header.Hash()
	}
	backend := &fakeReceiptBackend{receipt: receipt, canonical: header, latest: &types.Header{Number: big.NewInt(102)}}
	recovery, err := NewRecoveryService(store, kernel, backend, fakeEffectResolver{}, CanonicalPolicy{})
	if err != nil {
		closeDB()
		t.Fatal(err)
	}
	query := &controlledRecoveryQueryFake{evidence: ControlledRecoveryEvidence{NonceState: "RESERVED_UNRESOLVED", EvidenceHash: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", ObservedAt: now}}
	worker, err := NewControlledRecoveryWorker(store, recovery, query, "test", "worker-1", 1)
	if err != nil {
		closeDB()
		t.Fatal(err)
	}
	worker.now = func() time.Time { return now }
	if err = store.AcquireCanaryWorkerLease(context.Background(), "RECOVERY", "test", "worker-1", 1, now.Add(time.Minute), now); err != nil {
		closeDB()
		t.Fatal(err)
	}
	return store, worker, backend, artifact, query, closeDB
}

func TestControlledRecoveryDurableDiscoveryAndDuplicateScan(t *testing.T) {
	store, worker, _, artifact, _, closeDB := recoveryWorkerFixture(t, "submitted", nil)
	defer closeDB()
	for range 2 {
		if err := worker.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.ListControlledRecoveryItems(context.Background())
	if err != nil || len(items) != 1 || items[0].OperationID != artifact.Operation {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	var effects int
	store.db.QueryRow(`SELECT COUNT(*) FROM position_effects`).Scan(&effects)
	if effects != 0 {
		t.Fatalf("effects=%d", effects)
	}
}

func TestControlledRecoveryCanonicalReorgRecanonicalExactlyOnce(t *testing.T) {
	store, worker, backend, artifact, _, closeDB := recoveryWorkerFixture(t, "submitted", &types.Receipt{Status: 1})
	defer closeDB()
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	backend.canonical = &types.Header{Number: big.NewInt(100), Extra: []byte("replacement")}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	backend.canonical = &types.Header{Number: big.NewInt(100), Extra: []byte("canonical")}
	backend.receipt.BlockHash = backend.canonical.Hash()
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, transition := range []string{"apply", "rollback", "reapply"} {
		var count int
		store.db.QueryRow(`SELECT COUNT(*) FROM position_effect_history h JOIN position_effects e ON e.effect_id=h.effect_id WHERE e.attempt_id=? AND h.transition=?`, artifact.AttemptID, transition).Scan(&count)
		if count != 1 {
			t.Fatalf("%s=%d", transition, count)
		}
	}
}

func TestControlledRecoveryCanonicalRevertHasNoEffect(t *testing.T) {
	store, worker, _, artifact, _, closeDB := recoveryWorkerFixture(t, "submitted", &types.Receipt{Status: 0})
	defer closeDB()
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var effects int
	store.db.QueryRow(`SELECT COUNT(*) FROM position_effects WHERE attempt_id=?`, artifact.AttemptID).Scan(&effects)
	if effects != 0 {
		t.Fatalf("effects=%d", effects)
	}
}

func TestControlledRecoveryUnknownNeverSendsOrReleases(t *testing.T) {
	store, worker, _, artifact, query, closeDB := recoveryWorkerFixture(t, "broadcast_unknown", nil)
	defer closeDB()
	for _, evidence := range []ControlledRecoveryEvidence{
		{NonceState: "RESERVED_UNRESOLVED", EvidenceHash: query.evidence.EvidenceHash, ObservedAt: fixedTime()},
		{NonceState: "CONSUMED_UNKNOWN", EvidenceHash: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", ObservedAt: fixedTime()},
	} {
		query.evidence = evidence
		if err := worker.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var lane, reservation, submission string
	store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id=?`, artifact.WalletID).Scan(&lane)
	store.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id=?`, artifact.Operation).Scan(&reservation)
	store.db.QueryRow(`SELECT state FROM transaction_submissions WHERE attempt_id=? ORDER BY sequence DESC LIMIT 1`, artifact.AttemptID).Scan(&submission)
	if lane != "frozen" || reservation != "frozen" || submission != "broadcast_unknown" {
		t.Fatalf("lane=%s reservation=%s submission=%s", lane, reservation, submission)
	}
	var consumed int
	store.db.QueryRow(`SELECT COUNT(*) FROM canary_send_permits WHERE state='CONSUMED'`).Scan(&consumed)
	if consumed != 0 {
		t.Fatalf("consumed permits=%d", consumed)
	}
	var evidenceRows int
	store.db.QueryRow(`SELECT COUNT(*) FROM canary_recovery_evidence WHERE attempt_id=?`, artifact.AttemptID).Scan(&evidenceRows)
	if evidenceRows != 2 {
		t.Fatalf("evidence rows=%d", evidenceRows)
	}
}

func TestControlledRecoveryUnknownReceiptFoundReconcilesWithoutReplay(t *testing.T) {
	store, worker, _, artifact, query, closeDB := recoveryWorkerFixture(t, "broadcast_unknown", &types.Receipt{Status: 1})
	defer closeDB()
	query.evidence.TxFound = true
	query.evidence.ReceiptFound = true
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var effects, submissions, consumed int
	store.db.QueryRow(`SELECT COUNT(*) FROM position_effects WHERE attempt_id=? AND state='active'`, artifact.AttemptID).Scan(&effects)
	store.db.QueryRow(`SELECT COUNT(*) FROM transaction_submissions WHERE attempt_id=?`, artifact.AttemptID).Scan(&submissions)
	store.db.QueryRow(`SELECT COUNT(*) FROM canary_send_permits WHERE state='CONSUMED'`).Scan(&consumed)
	if effects != 1 || submissions != 1 || consumed != 0 {
		t.Fatalf("effects=%d submissions=%d consumed=%d", effects, submissions, consumed)
	}
}

func TestControlledRecoveryIgnoresExpiredEconomicControlsForExistingSubmission(t *testing.T) {
	store, worker, _, artifact, _, closeDB := recoveryWorkerFixture(t, "submitted", &types.Receipt{Status: 1})
	defer closeDB()
	seedCanary(t, store, 1)
	auth, err := store.CreateRuntimeAuthorization(context.Background(), runtimeAuthorization("recovery-revoked", fixedTime()), fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.TransitionRuntimeAuthorization(context.Background(), auth.ID, "PENDING", "REVOKED", fixedTime()); err != nil {
		t.Fatal(err)
	}
	expired := fixedTime().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err = store.db.Exec(`UPDATE operations SET expires_at=? WHERE id=?`, expired, artifact.Operation); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE execution_steps SET expires_at=? WHERE id=?`, expired, artifact.StepID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE transaction_attempts SET expires_at=? WHERE id=?`, expired, artifact.AttemptID); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var effects int
	store.db.QueryRow(`SELECT COUNT(*) FROM position_effects WHERE attempt_id=? AND state='active'`, artifact.AttemptID).Scan(&effects)
	if effects != 1 {
		t.Fatalf("effects=%d", effects)
	}
}

func TestControlledRecoveryLeaseFenceAndTakeover(t *testing.T) {
	store, worker, _, _, query, closeDB := recoveryWorkerFixture(t, "broadcast_unknown", nil)
	defer closeDB()
	now := fixedTime()
	if err := store.AcquireCanaryWorkerLease(context.Background(), "RECOVERY", "test", "worker-2", 2, now.Add(2*time.Minute), now); err == nil {
		t.Fatal("active lease takeover succeeded")
	}
	worker.afterQueryForTest = func(ControlledRecoveryItem) {
		worker.now = func() time.Time { return now.Add(2 * time.Minute) }
	}
	if err := worker.RunOnce(context.Background()); !errors.Is(err, ErrCanaryRecoveryLeaseLost) {
		t.Fatalf("err=%v", err)
	}
	if err := store.AcquireCanaryWorkerLease(context.Background(), "RECOVERY", "test", "worker-2", 2, now.Add(3*time.Minute), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	query.calls = 0
	if err := worker.RunOnce(context.Background()); !errors.Is(err, ErrCanaryRecoveryLeaseLost) || query.calls != 0 {
		t.Fatalf("stale worker err=%v calls=%d", err, query.calls)
	}
}

func TestControlledRecoveryLeaseLossBeforeCanonicalMutation(t *testing.T) {
	store, worker, _, artifact, _, closeDB := recoveryWorkerFixture(t, "submitted", &types.Receipt{Status: 1})
	defer closeDB()
	now := fixedTime()
	worker.beforeReconcileForTest = func(ControlledRecoveryItem) {
		worker.now = func() time.Time { return now.Add(2 * time.Minute) }
	}
	if err := worker.RunOnce(context.Background()); !errors.Is(err, ErrCanaryRecoveryLeaseLost) {
		t.Fatalf("err=%v", err)
	}
	var effects int
	store.db.QueryRow(`SELECT COUNT(*) FROM position_effects WHERE attempt_id=?`, artifact.AttemptID).Scan(&effects)
	if effects != 0 {
		t.Fatalf("effects=%d", effects)
	}
}

func TestControlledRecoveryRestartAfterLeaseLossRediscoversDurableWork(t *testing.T) {
	store, worker, backend, artifact, query, closeDB := recoveryWorkerFixture(t, "broadcast_unknown", &types.Receipt{Status: 1})
	defer closeDB()
	now := fixedTime()
	query.evidence.TxFound, query.evidence.ReceiptFound = true, true
	worker.afterQueryForTest = func(ControlledRecoveryItem) { worker.now = func() time.Time { return now.Add(2 * time.Minute) } }
	if err := worker.RunOnce(context.Background()); !errors.Is(err, ErrCanaryRecoveryLeaseLost) {
		t.Fatalf("err=%v", err)
	}
	if err := store.AcquireCanaryWorkerLease(context.Background(), "RECOVERY", "test", "worker-2", 2, now.Add(3*time.Minute), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	recovery, _ := NewRecoveryService(store, worker.recovery.kernel, backend, fakeEffectResolver{}, CanonicalPolicy{})
	restarted, _ := NewControlledRecoveryWorker(store, recovery, query, "test", "worker-2", 2)
	restarted.now = func() time.Time { return now.Add(2 * time.Minute) }
	if err := restarted.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var effects int
	store.db.QueryRow(`SELECT COUNT(*) FROM position_effects WHERE attempt_id=? AND state='active'`, artifact.AttemptID).Scan(&effects)
	if effects != 1 {
		t.Fatalf("effects=%d", effects)
	}
}

func TestControlledRecoveryFailureCreatesDurableAlert(t *testing.T) {
	store, worker, _, _, query, closeDB := recoveryWorkerFixture(t, "broadcast_unknown", nil)
	defer closeDB()
	query.err = errors.New("rpc unavailable")
	if err := worker.RunOnce(context.Background()); err == nil {
		t.Fatal("RPC failure accepted")
	}
	var audits, alerts int
	store.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE reason_code='RECOVERY_EVIDENCE_UNAVAILABLE'`).Scan(&audits)
	store.db.QueryRow(`SELECT COUNT(*) FROM canary_alert_outbox`).Scan(&alerts)
	if audits != 1 || alerts != 1 {
		t.Fatalf("audits=%d alerts=%d", audits, alerts)
	}
}

func TestControlledRecoveryContradictoryReceiptEvidenceFailsClosed(t *testing.T) {
	store, worker, _, _, query, closeDB := recoveryWorkerFixture(t, "broadcast_unknown", nil)
	defer closeDB()
	query.evidence.ReceiptFound = true
	if err := worker.RunOnce(context.Background()); !errors.Is(err, ErrReceiptPending) {
		t.Fatalf("err=%v", err)
	}
	var alerts int
	store.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE reason_code='RECOVERY_EVIDENCE_CONTRADICTORY'`).Scan(&alerts)
	if alerts != 1 {
		t.Fatalf("alerts=%d", alerts)
	}
}

func TestControlledRecoveryGracefulShutdownDoesNotStartAnotherScan(t *testing.T) {
	_, worker, _, _, query, closeDB := recoveryWorkerFixture(t, "broadcast_unknown", nil)
	defer closeDB()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.Run(ctx, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if query.calls != 0 {
		t.Fatalf("queries after shutdown=%d", query.calls)
	}
}

func runExpectPanic(t *testing.T, run func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected simulated crash")
		}
	}()
	run()
}

func TestControlledRecoveryQueryCrashWindowsRediscover(t *testing.T) {
	for _, stage := range []string{"before_query", "after_query_before_durable_evidence"} {
		t.Run(stage, func(t *testing.T) {
			store, worker, _, artifact, query, closeDB := recoveryWorkerFixture(t, "broadcast_unknown", &types.Receipt{Status: 1})
			defer closeDB()
			query.evidence.TxFound, query.evidence.ReceiptFound = true, true
			if stage == "before_query" {
				worker.beforeQuery = func(ControlledRecoveryItem) { panic("crash") }
			} else {
				worker.afterQueryForTest = func(ControlledRecoveryItem) { panic("crash") }
			}
			runExpectPanic(t, func() { _ = worker.RunOnce(context.Background()) })
			worker.beforeQuery, worker.afterQueryForTest = nil, nil
			if err := worker.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			var effects int
			store.db.QueryRow(`SELECT COUNT(*) FROM position_effects WHERE attempt_id=? AND state='active'`, artifact.AttemptID).Scan(&effects)
			if effects != 1 {
				t.Fatalf("effects=%d", effects)
			}
		})
	}
}

func TestControlledRecoveryCanonicalCrashWindowsRediscover(t *testing.T) {
	for _, stage := range []string{"after_receipt_observation", "before_canonical_effect_commit", "after_canonical_effect_commit"} {
		t.Run(stage, func(t *testing.T) {
			store, worker, _, artifact, _, closeDB := recoveryWorkerFixture(t, "submitted", &types.Receipt{Status: 1})
			defer closeDB()
			store.SetRecoveryHookForTest(func(got string) {
				if got == stage {
					panic("crash")
				}
			})
			runExpectPanic(t, func() { _ = worker.RunOnce(context.Background()) })
			store.SetRecoveryHookForTest(nil)
			if err := worker.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			var effects, applies int
			store.db.QueryRow(`SELECT COUNT(*) FROM position_effects WHERE attempt_id=? AND state='active'`, artifact.AttemptID).Scan(&effects)
			store.db.QueryRow(`SELECT COUNT(*) FROM position_effect_history h JOIN position_effects e ON e.effect_id=h.effect_id WHERE e.attempt_id=? AND h.transition='apply'`, artifact.AttemptID).Scan(&applies)
			if effects != 1 || applies != 1 {
				t.Fatalf("effects=%d applies=%d", effects, applies)
			}
		})
	}
}

func TestControlledRecoveryReorgCrashWindowsRediscover(t *testing.T) {
	for _, stage := range []string{"before_reorg_rollback_commit", "after_reorg_rollback_commit"} {
		t.Run(stage, func(t *testing.T) {
			store, worker, backend, artifact, _, closeDB := recoveryWorkerFixture(t, "submitted", &types.Receipt{Status: 1})
			defer closeDB()
			if err := worker.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			backend.canonical = &types.Header{Number: big.NewInt(100), Extra: []byte("replacement")}
			store.SetRecoveryHookForTest(func(got string) {
				if got == stage {
					panic("crash")
				}
			})
			runExpectPanic(t, func() { _ = worker.RunOnce(context.Background()) })
			store.SetRecoveryHookForTest(nil)
			if err := worker.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			var rollbacks int
			store.db.QueryRow(`SELECT COUNT(*) FROM position_effect_history h JOIN position_effects e ON e.effect_id=h.effect_id WHERE e.attempt_id=? AND h.transition='rollback'`, artifact.AttemptID).Scan(&rollbacks)
			if rollbacks != 1 {
				t.Fatalf("rollbacks=%d", rollbacks)
			}
		})
	}
}
