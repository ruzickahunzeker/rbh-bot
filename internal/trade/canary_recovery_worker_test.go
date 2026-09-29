package trade

import (
	"context"
	"errors"
	"math/big"
	"strconv"
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

func TestControlledRecoveryEmergencyStopDoesNotBlockExistingRecovery(t *testing.T) {
	store, worker, _, artifact, _, closeDB := recoveryWorkerFixture(t, "submitted", &types.Receipt{Status: 1})
	defer closeDB()
	seedCanary(t, store, 1)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var effects int
	store.db.QueryRow(`SELECT COUNT(*) FROM position_effects WHERE attempt_id=? AND state='active'`, artifact.AttemptID).Scan(&effects)
	if effects != 1 {
		t.Fatalf("effects=%d", effects)
	}
}

func TestRecoveryLeaseFenceCommitWindowSerializesTakeover(t *testing.T) {
	store, _, _, artifact, _, closeDB := recoveryWorkerFixture(t, "submitted", nil)
	defer closeDB()
	now := fixedTime()
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	oldFence := &RecoveryLeaseFence{Environment: "test", HolderID: "worker-1", Epoch: 1, clock: func() time.Time { return now }}
	if err = assertRecoveryLeaseTx(context.Background(), tx, oldFence); err != nil {
		t.Fatal(err)
	}
	takeoverDone := make(chan error, 1)
	go func() {
		takeoverDone <- store.AcquireCanaryWorkerLease(context.Background(), "RECOVERY", "test", "worker-2", 2, now.Add(3*time.Minute), now.Add(2*time.Minute))
	}()
	select {
	case err = <-takeoverDone:
		t.Fatalf("takeover crossed active fenced transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	receiptID := deterministicID(artifact.AttemptID, "commit-window")
	stamp := now.Format(time.RFC3339Nano)
	if _, err = tx.Exec(`INSERT INTO receipt_observations(id,attempt_id,chain_id,tx_hash,block_number,block_hash,receipt_status,canonical_state,reconciliation_version,observed_at,updated_at) VALUES(?,?,4663,?,100,?,1,'observed',1,?,?)`, receiptID, artifact.AttemptID, artifact.TxHash, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-takeoverDone; err != nil {
		t.Fatal(err)
	}
	var receipts int
	store.db.QueryRow(`SELECT COUNT(*) FROM receipt_observations WHERE id=?`, receiptID).Scan(&receipts)
	if receipts != 1 {
		t.Fatalf("committed receipts=%d", receipts)
	}
	staleTx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer staleTx.Rollback()
	if err = assertRecoveryLeaseTx(context.Background(), staleTx, oldFence); !errors.Is(err, ErrCanaryRecoveryLeaseLost) {
		t.Fatalf("old owner mutation fence err=%v", err)
	}
}

func TestEmergencyStopBroadcastUnknownRemainsQueryOnlyAndFrozen(t *testing.T) {
	store, worker, _, artifact, query, closeDB := recoveryWorkerFixture(t, "broadcast_unknown", nil)
	defer closeDB()
	seedCanary(t, store, 1)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var lane, reservation, submission, nonce string
	var attempts, submissions, consumedPermits int
	store.db.QueryRow(`SELECT state,reserved_nonce FROM execution_wallet_lanes WHERE wallet_id=?`, artifact.WalletID).Scan(&lane, &nonce)
	store.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id=?`, artifact.Operation).Scan(&reservation)
	store.db.QueryRow(`SELECT state FROM transaction_submissions WHERE attempt_id=? ORDER BY sequence DESC LIMIT 1`, artifact.AttemptID).Scan(&submission)
	store.db.QueryRow(`SELECT COUNT(*) FROM transaction_attempts WHERE step_id=?`, artifact.StepID).Scan(&attempts)
	store.db.QueryRow(`SELECT COUNT(*) FROM transaction_submissions WHERE attempt_id=?`, artifact.AttemptID).Scan(&submissions)
	store.db.QueryRow(`SELECT COUNT(*) FROM canary_send_permits WHERE state='CONSUMED'`).Scan(&consumedPermits)
	if query.calls != 1 || lane != "frozen" || reservation != "frozen" || submission != "broadcast_unknown" || attempts != 1 || submissions != 1 || consumedPermits != 0 || nonce != strconv.FormatUint(artifact.Nonce, 10) {
		t.Fatalf("queries=%d lane=%s reservation=%s submission=%s attempts=%d submissions=%d permits=%d nonce=%s", query.calls, lane, reservation, submission, attempts, submissions, consumedPermits, nonce)
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
	var leaseAlerts int
	store.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE reason_code='RECOVERY_LEASE_LOST'`).Scan(&leaseAlerts)
	if leaseAlerts != 2 {
		t.Fatalf("lease alerts=%d", leaseAlerts)
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
	var observations, alerts int
	store.db.QueryRow(`SELECT COUNT(*) FROM receipt_observations WHERE attempt_id=?`, artifact.AttemptID).Scan(&observations)
	store.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE reason_code='RECOVERY_LEASE_LOST'`).Scan(&alerts)
	if observations != 0 || alerts != 1 {
		t.Fatalf("observations=%d alerts=%d", observations, alerts)
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
