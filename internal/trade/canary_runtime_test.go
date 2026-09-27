package trade

import (
	"context"
	"errors"
	"testing"
	"time"
)

func runtimeAuthorization(id string, now time.Time) RuntimeAuthorization {
	return RuntimeAuthorization{
		ID: id, WalletID: "wallet-1", PolicyVersion: 1, MaxOperations: 2, MaxTotalInput: "10",
		GateEvidenceSetHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		AuthorizationRef:    "review:controlled-canary-only",
		Deployment:          DeploymentIdentity{Environment: "controlled-test", BuildSHA: "1111111111111111111111111111111111111111", ReleaseID: "release-1", DeploymentID: "deployment-1"},
		ExpiresAt:           now.Add(time.Hour),
	}
}

func TestRuntimeAuthorizationMonotonicIdentityAndBudget(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	r := canaryRequest("runtime-budget", now)
	persistSource(t, s, r)
	a, err := s.CreateRuntimeAuthorization(context.Background(), runtimeAuthorization("auth-1", now), now)
	if err != nil || a.Epoch != 1 || a.State != RuntimeAuthorizationPending {
		t.Fatalf("create %#v %v", a, err)
	}
	b, err := s.CreateRuntimeAuthorization(context.Background(), runtimeAuthorization("auth-2", now), now)
	if err != nil || b.Epoch != 2 {
		t.Fatalf("monotonic %#v %v", b, err)
	}
	if err = s.TransitionRuntimeAuthorization(context.Background(), a.ID, "PENDING", "ARMED", now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ValidateRuntimeAuthorization(context.Background(), a.ID, "wallet-1", 1, a.Deployment, now); err != nil {
		t.Fatal(err)
	}
	wrong := a.Deployment
	wrong.DeploymentID = "different"
	if _, err = s.ValidateRuntimeAuthorization(context.Background(), a.ID, "wallet-1", 1, wrong, now); !errors.Is(err, ErrCanaryRuntimeRejected) {
		t.Fatalf("deployment drift accepted: %v", err)
	}
	if _, err = s.db.Exec(`UPDATE canary_runtime_authorizations SET build_sha=? WHERE id=?`, "2222222222222222222222222222222222222222", a.ID); err == nil {
		t.Fatal("immutable deployment changed")
	}
	duplicate, err := s.ReserveRuntimeAuthorizationBudget(context.Background(), a.ID, r.OperationID, "6", now)
	if err != nil || duplicate {
		t.Fatalf("reserve duplicate=%v err=%v", duplicate, err)
	}
	duplicate, err = s.ReserveRuntimeAuthorizationBudget(context.Background(), a.ID, r.OperationID, "6", now)
	if err != nil || !duplicate {
		t.Fatalf("dedupe duplicate=%v err=%v", duplicate, err)
	}
	r2 := canaryRequest("runtime-budget-two", now)
	persistSource(t, s, r2)
	if _, err = s.ReserveRuntimeAuthorizationBudget(context.Background(), a.ID, r2.OperationID, "5", now); !errors.Is(err, ErrCanaryRuntimeRejected) {
		t.Fatalf("cap overrun accepted: %v", err)
	}
	var count uint64
	var total string
	if err = s.db.QueryRow(`SELECT operation_count,total_input FROM canary_authorization_usage WHERE authorization_id=?`, a.ID).Scan(&count, &total); err != nil || count != 1 || total != "6" {
		t.Fatalf("usage %d %s %v", count, total, err)
	}
}

func TestRuntimeGateSnapshotIsImmutable(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	r := canaryRequest("snapshot", now)
	persistSource(t, s, r)
	a, _ := s.CreateRuntimeAuthorization(context.Background(), runtimeAuthorization("auth-snapshot", now), now)
	v := RuntimeGateSnapshot{ID: "snapshot-1", OperationID: r.OperationID, Stage: "EXECUTION_ADMISSION", AuthorizationID: a.ID, AuthorizationEpoch: a.Epoch, Deployment: a.Deployment, PolicyVersion: 1, PolicyHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", GateEvidenceSetHash: a.GateEvidenceSetHash, Decision: "PASS", ReasonCode: "CONTROLLED_TEST", CheckedAt: now}
	if err := s.RecordRuntimeGateSnapshot(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE canary_runtime_gate_snapshots SET decision='REJECT' WHERE id='snapshot-1'`); err == nil {
		t.Fatal("snapshot mutation accepted")
	}
}

func TestSendPermitsArePurposeBoundAndOneShot(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	r := canaryRequest("permit", now)
	persistSource(t, s, r)
	step, attempt := "runtime-step", "runtime-attempt"
	stamp := now.Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`INSERT INTO execution_steps(id,operation_id,step_index,kind,status,created_at,updated_at,wallet_id) VALUES(?,?,1,'pons_curve_execution','signed',?,?,?)`, step, r.OperationID, stamp, stamp, r.WalletID); err != nil {
		t.Fatal(err)
	}
	txHash := "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if _, err := s.db.Exec(`INSERT INTO transaction_attempts(id,step_id,nonce,tx_hash,status,created_at,chain_id,wallet_id,raw_tx_hash,updated_at) VALUES(?,?,'1',?,'signed',?,4663,?,?,?)`, attempt, step, txHash, stamp, r.WalletID, txHash, stamp); err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateRuntimeAuthorization(context.Background(), runtimeAuthorization("auth-permit", now), now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.TransitionRuntimeAuthorization(context.Background(), a.ID, "PENDING", "ARMED", now); err != nil {
		t.Fatal(err)
	}
	base := SendPermit{ID: "permit-first", OperationID: r.OperationID, AttemptID: attempt, AuthorizationID: a.ID, AuthorizationEpoch: a.Epoch, Purpose: PermitFirstBroadcast, ArtifactHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", TxHash: txHash, IssuedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err = s.IssueSendPermit(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	if err = s.ConsumeSendPermit(context.Background(), base.ID, PermitUnknownReplay, base.ArtifactHash, now); !errors.Is(err, ErrCanaryRuntimeRejected) {
		t.Fatal("first permit reused for replay")
	}
	if err = s.ConsumeSendPermit(context.Background(), base.ID, PermitFirstBroadcast, base.ArtifactHash, now); err != nil {
		t.Fatal(err)
	}
	if err = s.ConsumeSendPermit(context.Background(), base.ID, PermitFirstBroadcast, base.ArtifactHash, now); !errors.Is(err, ErrCanaryRuntimeRejected) {
		t.Fatal("permit consumed twice")
	}
	replay := base
	replay.ID = "permit-replay"
	replay.Purpose = PermitUnknownReplay
	replay.QueryEvidenceHash = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	replay.QueriedAt = now
	if err = s.IssueSendPermit(context.Background(), replay); err != nil {
		t.Fatal(err)
	}
	var firstPurpose, replayPurpose string
	s.db.QueryRow(`SELECT purpose FROM canary_send_permits WHERE id=?`, base.ID).Scan(&firstPurpose)
	s.db.QueryRow(`SELECT purpose FROM canary_send_permits WHERE id=?`, replay.ID).Scan(&replayPurpose)
	if firstPurpose != PermitFirstBroadcast || replayPurpose != PermitUnknownReplay {
		t.Fatalf("purposes %s %s", firstPurpose, replayPurpose)
	}
}

func TestWorkerLeaseIsMonotonicAndFailClosed(t *testing.T) {
	s := canaryStore(t)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := s.AcquireCanaryWorkerLease(context.Background(), "RECOVERY", "controlled-test", "worker-1", 1, now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if err := s.AcquireCanaryWorkerLease(context.Background(), "RECOVERY", "controlled-test", "worker-2", 2, now.Add(time.Minute), now); !errors.Is(err, ErrCanaryRuntimeRejected) {
		t.Fatal("active lease stolen")
	}
	if err := s.AcquireCanaryWorkerLease(context.Background(), "RECOVERY", "controlled-test", "worker-2", 2, now.Add(2*time.Minute), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAlertAndAuditCommitTogether(t *testing.T) {
	s := canaryStore(t)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := s.RecordRuntimeAlert(context.Background(), "AUTHORIZATION_DRIFT", "", "", "", 0, "FAIL_CLOSED", "CRITICAL", now); err != nil {
		t.Fatal(err)
	}
	var audits, alerts int
	s.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit`).Scan(&audits)
	s.db.QueryRow(`SELECT COUNT(*) FROM canary_alert_outbox WHERE state='PENDING'`).Scan(&alerts)
	if audits != 1 || alerts != 1 {
		t.Fatalf("audits=%d alerts=%d", audits, alerts)
	}
}
