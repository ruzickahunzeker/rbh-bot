package trade

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func requestExpiringAt(value time.Time) DryRunRequest {
	r := buyRequest()
	r.Intent.ExpiresAt = value.UTC().Format(time.RFC3339Nano)
	return r
}

func TestApplicationTTLRejectsMissingMalformedAndBoundary(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	for _, test := range []struct {
		name  string
		value string
		err   error
	}{
		{"missing", "", ErrTTLUnverifiable},
		{"malformed", "tomorrow", ErrTTLUnverifiable},
		{"non-canonical", "1970-01-01T00:16:41.000000000Z", ErrTTLUnverifiable},
		{"equal", now.Format(time.RFC3339Nano), ErrTTLExpired},
		{"past", now.Add(-time.Nanosecond).Format(time.RFC3339Nano), ErrTTLExpired},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := buyRequest()
			r.Intent.ExpiresAt = test.value
			if err := r.CheckTTL(now); !errors.Is(err, test.err) {
				t.Fatalf("err=%v want=%v", err, test.err)
			}
		})
	}
	if err := requestExpiringAt(now.Add(time.Nanosecond)).CheckTTL(now); err != nil {
		t.Fatalf("strictly-before boundary rejected: %v", err)
	}
}

func TestDryRunChecksTTLAfterSimulationBeforeSuccess(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := newFakeBackend()
	now := time.Unix(1_000, 0).UTC()
	request := requestExpiringAt(now.Add(time.Second))
	engine, _ := NewEngine(store, backend)
	engine.now = func() time.Time { return now }
	backend.simulateHook = func() { now = now.Add(time.Second) }
	result, err := engine.DryRun(context.Background(), request)
	if err != nil || result.Status != "fail_closed" || result.FailureCode != "TTL_EXPIRED" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRestartDoesNotExtendAdmittedTTL(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	created := time.Unix(1_000, 0).UTC()
	request := requestExpiringAt(created.Add(time.Second))
	if _, err := store.Admit(context.Background(), request, created); err != nil {
		t.Fatal(err)
	}
	engine, _ := NewEngine(store, newFakeBackend())
	engine.now = func() time.Time { return created.Add(time.Second) }
	results, err := engine.Recover(context.Background())
	if err != nil || len(results) != 1 || results[0].FailureCode != "TTL_EXPIRED" {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	var status, expiresAt string
	if err := store.db.QueryRow(`SELECT status,expires_at FROM operations WHERE id=?`, request.Intent.ID).Scan(&status, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if status != "dry_run_failed" || expiresAt != request.Intent.ExpiresAt {
		t.Fatalf("status=%s expiry=%s", status, expiresAt)
	}
}

func TestDuplicateCannotMintDifferentExpiry(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	now := time.Unix(1_000, 0).UTC()
	first := requestExpiringAt(now.Add(time.Minute))
	if _, err := store.Admit(context.Background(), first, now); err != nil {
		t.Fatal(err)
	}
	changed := first
	changed.Intent.ExpiresAt = now.Add(2 * time.Minute).Format(time.RFC3339Nano)
	if _, err := store.Admit(context.Background(), changed, now); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("err=%v", err)
	}
	var expiresAt string
	_ = store.db.QueryRow(`SELECT expires_at FROM operations WHERE id=?`, first.Intent.ID).Scan(&expiresAt)
	if expiresAt != first.Intent.ExpiresAt {
		t.Fatalf("expiry mutated=%s", expiresAt)
	}
}

func TestExecutionExpiryAfterReservationPreventsSigningAndReleasesKnownUnsent(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := newFakeBackend()
	signer, cipher := executionSecrets(t)
	if err := store.RegisterDryRunWallet(context.Background(), "wallet-1", signer.Address()); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_000, 0).UTC()
	request := requestExpiringAt(now.Add(time.Second))
	engine, _ := NewEngine(store, backend)
	engine.now = func() time.Time { return now }
	if result, err := engine.DryRun(context.Background(), request); err != nil || result.Status != "success" {
		t.Fatalf("dry-run=%+v err=%v", result, err)
	}
	kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
	kernel.now = func() time.Time { return now }
	kernel.SetHookForTest(func(stage string) {
		if stage == "after_nonce_reservation_commit" {
			now = now.Add(time.Second)
		}
	})
	if _, err := kernel.Prepare(context.Background(), request.Intent.ID, request.WalletID); !errors.Is(err, ErrTTLExpired) {
		t.Fatalf("err=%v", err)
	}
	var status string
	var txHash sql.NullString
	if err := store.db.QueryRow(`SELECT status,tx_hash FROM transaction_attempts`).Scan(&status, &txHash); err != nil {
		t.Fatal(err)
	}
	if status != "expired_prebroadcast" || txHash.Valid {
		t.Fatalf("status=%s txHash=%v", status, txHash)
	}
	var lane, reservation string
	_ = store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id='wallet-1'`).Scan(&lane)
	_ = store.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id=?`, request.Intent.ID).Scan(&reservation)
	if lane != "idle" || reservation != "released" {
		t.Fatalf("lane=%s reservation=%s", lane, reservation)
	}
}

func TestExpiredExecutionAdmissionCreatesNoNonceOrSignature(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := newFakeBackend()
	signer, cipher := executionSecrets(t)
	if err := store.RegisterDryRunWallet(context.Background(), "wallet-1", signer.Address()); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_000, 0).UTC()
	request := requestExpiringAt(now.Add(time.Second))
	engine, _ := NewEngine(store, backend)
	engine.now = func() time.Time { return now }
	if result, err := engine.DryRun(context.Background(), request); err != nil || result.Status != "success" {
		t.Fatalf("dry-run=%+v err=%v", result, err)
	}
	now = now.Add(time.Second)
	kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
	kernel.now = func() time.Time { return now }
	if _, err := kernel.Prepare(context.Background(), request.Intent.ID, request.WalletID); !errors.Is(err, ErrTTLExpired) {
		t.Fatalf("err=%v", err)
	}
	var attempts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transaction_attempts`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	var status string
	_ = store.db.QueryRow(`SELECT status FROM operations WHERE id=?`, request.Intent.ID).Scan(&status)
	if attempts != 0 || status != "expired_prebroadcast" {
		t.Fatalf("attempts=%d status=%s", attempts, status)
	}
}

func TestFirstBroadcastSecondTTLCheckRecordsKnownUnsent(t *testing.T) {
	store, kernel, artifact, closeDB := seededSignedArtifact(t)
	defer closeDB()
	expires, _ := parseCanonicalExpiry(artifact.ExpiresAt)
	calls := 0
	now := func() time.Time {
		calls++
		if calls >= 3 {
			return expires
		}
		return expires.Add(-time.Second)
	}
	b := &fakeBroadcaster{hash: artifact.TxHash}
	svc, _ := NewSubmissionService(store, kernel, b)
	svc.now = now
	if _, err := svc.Submit(context.Background(), artifact.Operation); !errors.Is(err, ErrTTLExpired) {
		t.Fatalf("err=%v", err)
	}
	if b.calls != 0 {
		t.Fatalf("broadcast calls=%d", b.calls)
	}
	var state, lane, reservation string
	_ = store.db.QueryRow(`SELECT state FROM transaction_submissions`).Scan(&state)
	_ = store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id=?`, artifact.WalletID).Scan(&lane)
	_ = store.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id=?`, artifact.Operation).Scan(&reservation)
	if state != "expired_prebroadcast" || lane != "idle" || reservation != "released" {
		t.Fatalf("state=%s lane=%s reservation=%s", state, lane, reservation)
	}
}

func TestExpiredBroadcastUnknownIsQueryOnlyAndRemainsFrozen(t *testing.T) {
	store, kernel, artifact, closeDB := seededSignedArtifact(t)
	defer closeDB()
	expires, _ := parseCanonicalExpiry(artifact.ExpiresAt)
	b := &fakeBroadcaster{err: ErrBroadcastAmbiguous}
	svc, _ := NewSubmissionService(store, kernel, b)
	svc.now = func() time.Time { return expires.Add(-time.Second) }
	_, _ = svc.Submit(context.Background(), artifact.Operation)
	svc.now = func() time.Time { return expires }
	if _, err := svc.ReplayUnknown(context.Background(), artifact.Operation, true); !errors.Is(err, ErrTTLExpired) {
		t.Fatalf("err=%v", err)
	}
	if b.calls != 1 {
		t.Fatalf("expired replay calls=%d", b.calls)
	}
	var lane, reservation string
	_ = store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id=?`, artifact.WalletID).Scan(&lane)
	_ = store.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id=?`, artifact.Operation).Scan(&reservation)
	if lane != "frozen" || reservation != "frozen" {
		t.Fatalf("lane=%s reservation=%s", lane, reservation)
	}
}

func TestSubmittedDuplicateRemainsReconcilableAfterExpiry(t *testing.T) {
	store, kernel, artifact, closeDB := seededSignedArtifact(t)
	defer closeDB()
	expires, _ := parseCanonicalExpiry(artifact.ExpiresAt)
	b := &fakeBroadcaster{hash: artifact.TxHash}
	svc, _ := NewSubmissionService(store, kernel, b)
	svc.now = func() time.Time { return expires.Add(-time.Second) }
	first, err := svc.Submit(context.Background(), artifact.Operation)
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return expires }
	second, err := svc.Submit(context.Background(), artifact.Operation)
	if err != nil || second.ID != first.ID || b.calls != 1 {
		t.Fatalf("second=%+v calls=%d err=%v", second, b.calls, err)
	}
}
