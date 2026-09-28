package trade

import (
	"context"
	"errors"
	"testing"
	"time"
)

type controlledArtifactVerifier struct {
	want  CanaryArtifactIdentity
	calls int
	err   error
}

func (f *controlledArtifactVerifier) VerifyControlledArtifact(_ context.Context, v CanaryArtifactIdentity) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	if v != f.want {
		return ErrCanaryArtifactMismatch
	}
	return nil
}

type controlledRuntimeReader struct {
	address, hash string
	calls         int
	err           error
}

func (f *controlledRuntimeReader) VerifyControlledRuntime(_ context.Context, a, h string) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	if a != f.address || h != f.hash {
		return ErrStaleState
	}
	return nil
}

type controlledUnknownQuerier struct {
	evidence UnknownReplayEvidence
	calls    int
	err      error
	want     CanaryArtifactIdentity
}

func (f *controlledUnknownQuerier) QueryUnknown(_ context.Context, v CanaryArtifactIdentity) (UnknownReplayEvidence, error) {
	f.calls++
	if v != f.want {
		return UnknownReplayEvidence{}, ErrCanaryArtifactMismatch
	}
	return f.evidence, f.err
}

type orchestratorFixture struct {
	store             *Store
	orchestrator      *ControlledCanaryOrchestrator
	request           CanaryGateRequest
	artifact          CanaryArtifactIdentity
	address, codeHash string
	auth              RuntimeAuthorization
	verifier          *controlledArtifactVerifier
	runtime           *controlledRuntimeReader
	query             *controlledUnknownQuerier
	now               time.Time
}

func newOrchestratorFixture(t *testing.T) orchestratorFixture {
	t.Helper()
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	cr := canaryRequest("w2", now)
	persistSource(t, s, cr)
	if d, e := s.AdmitControlledCanary(context.Background(), cr, now); e != nil || d.Decision != "ADMITTED" {
		t.Fatalf("admit %+v %v", d, e)
	}
	a, e := s.CreateRuntimeAuthorization(context.Background(), runtimeAuthorization("w2-auth", now), now)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.TransitionRuntimeAuthorization(context.Background(), a.ID, "PENDING", "ARMED", now); e != nil {
		t.Fatal(e)
	}
	r := CanaryGateRequest{OperationID: cr.OperationID, AuthorizationID: a.ID, AuthorizationEpoch: a.Epoch, PolicyVersion: 1, ChainID: ChainID, WalletID: cr.WalletID, Deployment: a.Deployment}
	x := CanaryArtifactIdentity{OperationID: cr.OperationID, AttemptID: "w2-attempt", TxHash: "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", ArtifactHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}
	r.AttemptID = x.AttemptID
	v := &controlledArtifactVerifier{want: x}
	rr := &controlledRuntimeReader{address: cr.ContractAddress, hash: cr.RuntimeCodeHash}
	q := &controlledUnknownQuerier{want: x, evidence: UnknownReplayEvidence{NonceState: "RESERVED_UNRESOLVED", EvidenceHash: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", QueriedAt: now}}
	o, e := NewControlledCanaryOrchestrator(s, v, rr, q)
	if e != nil {
		t.Fatal(e)
	}
	o.now = func() time.Time { return now }
	return orchestratorFixture{store: s, orchestrator: o, request: r, artifact: x, address: cr.ContractAddress, codeHash: cr.RuntimeCodeHash, auth: a, verifier: v, runtime: rr, query: q, now: now}
}
func (f orchestratorFixture) executionAndAttempt(t *testing.T) {
	t.Helper()
	if d, e := f.orchestrator.ExecutionAdmission(context.Background(), f.request); e != nil || d.Decision != "PASS" {
		t.Fatalf("execution %+v %v", d, e)
	}
	stamp := f.now.Format(time.RFC3339Nano)
	if _, e := f.store.db.Exec(`INSERT INTO execution_steps(id,operation_id,step_index,kind,status,created_at,updated_at,wallet_id) VALUES('w2-step',?,1,'pons_curve_execution','signed',?,?,'wallet-1')`, f.request.OperationID, stamp, stamp); e != nil {
		t.Fatal(e)
	}
	if _, e := f.store.db.Exec(`INSERT INTO transaction_attempts(id,step_id,nonce,tx_hash,status,created_at,chain_id,wallet_id,raw_tx_hash,updated_at) VALUES(?,'w2-step','1',?,'signed',?,4663,'wallet-1',?,?)`, f.artifact.AttemptID, f.artifact.TxHash, stamp, f.artifact.TxHash, stamp); e != nil {
		t.Fatal(e)
	}
}
func (f orchestratorFixture) broadcastUnknown(t *testing.T) {
	t.Helper()
	stamp := f.now.Format(time.RFC3339Nano)
	if _, e := f.store.db.Exec(`INSERT INTO transaction_submissions(id,attempt_id,tx_hash,sequence,state,created_at,updated_at) VALUES('w2-submission',?,?,0,'broadcast_unknown',?,?)`, f.artifact.AttemptID, f.artifact.TxHash, stamp, stamp); e != nil {
		t.Fatal(e)
	}
}

func TestControlledCanaryAllFiveGatesAndPermitSeparation(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.executionAndAttempt(t)
	if d, e := f.orchestrator.PreSign(context.Background(), f.request); e != nil || d.Decision != "PASS" {
		t.Fatalf("pre-sign %+v %v", d, e)
	}
	first, e := f.orchestrator.FirstBroadcast(context.Background(), f.request, f.artifact, f.address, f.codeHash)
	if e != nil || first.PermitID == "" {
		t.Fatalf("first %+v %v", first, e)
	}
	pre, e := f.orchestrator.ImmediatePreSend(context.Background(), f.request, first.PermitID, PermitFirstBroadcast, f.artifact, f.address, f.codeHash)
	if e != nil || pre.Decision != "PASS" {
		t.Fatalf("pre-send %+v %v", pre, e)
	}
	if _, e = f.orchestrator.ImmediatePreSend(context.Background(), f.request, first.PermitID, PermitFirstBroadcast, f.artifact, f.address, f.codeHash); e == nil {
		t.Fatal("one-shot permit reused")
	}
	var snapshots int
	if e = f.store.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_gate_snapshots WHERE operation_id=?`, f.request.OperationID).Scan(&snapshots); e != nil || snapshots != 4 {
		t.Fatalf("snapshots=%d err=%v", snapshots, e)
	}
}

func TestImmediatePreSendRereadsEmergencyAndAuthorization(t *testing.T) {
	t.Run("emergency", func(t *testing.T) {
		f := newOrchestratorFixture(t)
		f.executionAndAttempt(t)
		first, e := f.orchestrator.FirstBroadcast(context.Background(), f.request, f.artifact, f.address, f.codeHash)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.store.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1,updated_at=? WHERE singleton=1`, f.now.Add(time.Second).Format(time.RFC3339Nano)); e != nil {
			t.Fatal(e)
		}
		d, e := f.orchestrator.ImmediatePreSend(context.Background(), f.request, first.PermitID, PermitFirstBroadcast, f.artifact, "wrong", "wrong")
		if !errors.Is(e, ErrCanaryGateRejected) || d.ReasonCode != "EMERGENCY_STOPPED" {
			t.Fatalf("%+v %v", d, e)
		}
		var state string
		f.store.db.QueryRow(`SELECT state FROM canary_send_permits WHERE id=?`, first.PermitID).Scan(&state)
		if state != "ISSUED" {
			t.Fatalf("permit=%s", state)
		}
	})
	t.Run("revoke", func(t *testing.T) {
		f := newOrchestratorFixture(t)
		f.executionAndAttempt(t)
		first, e := f.orchestrator.FirstBroadcast(context.Background(), f.request, f.artifact, f.address, f.codeHash)
		if e != nil {
			t.Fatal(e)
		}
		if e = f.store.TransitionRuntimeAuthorization(context.Background(), f.auth.ID, "ARMED", "REVOKED", f.now.Add(time.Second)); e != nil {
			t.Fatal(e)
		}
		d, e := f.orchestrator.ImmediatePreSend(context.Background(), f.request, first.PermitID, PermitFirstBroadcast, f.artifact, f.address, f.codeHash)
		if !errors.Is(e, ErrCanaryGateRejected) || d.ReasonCode != "AUTHORIZATION_NOT_ARMED" {
			t.Fatalf("%+v %v", d, e)
		}
	})
}

func TestEmergencyStopHighestPriorityAndReadinessSplit(t *testing.T) {
	f := newOrchestratorFixture(t)
	if _, e := f.store.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1 WHERE singleton=1`); e != nil {
		t.Fatal(e)
	}
	f.request.Deployment.DeploymentID = "wrong"
	d, e := f.orchestrator.ExecutionAdmission(context.Background(), f.request)
	if !errors.Is(e, ErrCanaryGateRejected) || d.ReasonCode != "EMERGENCY_STOPPED" {
		t.Fatalf("%+v %v", d, e)
	}
	ready := f.orchestrator.Readiness(context.Background(), f.request)
	if ready.CanaryAdmissionReady || !ready.RecoveryQueryAvailable || !ready.ReconciliationAvailable {
		t.Fatalf("%+v", ready)
	}
}

func TestUnknownReplayRequiresQueryAndExactArtifact(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.executionAndAttempt(t)
	f.broadcastUnknown(t)
	replay, e := f.orchestrator.UnknownReplay(context.Background(), f.request, f.artifact, f.address, f.codeHash)
	if e != nil || replay.PermitID == "" || f.query.calls != 1 {
		t.Fatalf("replay %+v calls=%d err=%v", replay, f.query.calls, e)
	}
	var purpose, hash string
	f.store.db.QueryRow(`SELECT purpose,artifact_hash FROM canary_send_permits WHERE id=?`, replay.PermitID).Scan(&purpose, &hash)
	if purpose != PermitUnknownReplay || hash != f.artifact.ArtifactHash {
		t.Fatalf("purpose=%s hash=%s", purpose, hash)
	}
}
func TestUnknownReplayQueryFindingPropagationIsReconciliationOnly(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.executionAndAttempt(t)
	f.broadcastUnknown(t)
	f.query.evidence.TxFound = true
	d, e := f.orchestrator.UnknownReplay(context.Background(), f.request, f.artifact, f.address, f.codeHash)
	if !errors.Is(e, ErrCanaryQueryOnly) || d.ReasonCode != "QUERY_REQUIRES_RECONCILIATION" {
		t.Fatalf("%+v %v", d, e)
	}
	var permits int
	f.store.db.QueryRow(`SELECT COUNT(*) FROM canary_send_permits`).Scan(&permits)
	if permits != 0 {
		t.Fatalf("permits=%d", permits)
	}
	ready := f.orchestrator.Readiness(context.Background(), f.request)
	if !ready.RecoveryQueryAvailable || !ready.ReconciliationAvailable {
		t.Fatalf("%+v", ready)
	}
}

func TestGateBindingDriftAndKnownUnsentFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*orchestratorFixture)
		reason string
	}{{"epoch", func(f *orchestratorFixture) { f.request.AuthorizationEpoch++ }, "AUTHORIZATION_EPOCH_MISMATCH"}, {"deployment", func(f *orchestratorFixture) {
		f.request.Deployment.BuildSHA = "2222222222222222222222222222222222222222"
	}, "DEPLOYMENT_IDENTITY_MISMATCH"}, {"chain", func(f *orchestratorFixture) { f.request.ChainID = 1 }, "CHAIN_MISMATCH"}, {"wallet", func(f *orchestratorFixture) { f.request.WalletID = "other" }, "WALLET_MISMATCH"}, {"policy", func(f *orchestratorFixture) { f.request.PolicyVersion = 2 }, "POLICY_MISMATCH"}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newOrchestratorFixture(t)
			tt.mutate(&f)
			d, e := f.orchestrator.ExecutionAdmission(context.Background(), f.request)
			if !errors.Is(e, ErrCanaryGateRejected) || d.ReasonCode != tt.reason {
				t.Fatalf("%+v %v", d, e)
			}
		})
	}
	f := newOrchestratorFixture(t)
	f.executionAndAttempt(t)
	f.runtime.err = ErrStaleState
	d, e := f.orchestrator.FirstBroadcast(context.Background(), f.request, f.artifact, f.address, f.codeHash)
	if !errors.Is(e, ErrCanaryGateRejected) || d.ReasonCode != "RUNTIME_IDENTITY_UNVERIFIABLE" {
		t.Fatalf("%+v %v", d, e)
	}
	var submissions int
	f.store.db.QueryRow(`SELECT COUNT(*) FROM transaction_submissions`).Scan(&submissions)
	if submissions != 0 {
		t.Fatalf("known-unsent became ambiguous: %d", submissions)
	}
}

func TestNoGateFailureCreatesExecutionSideEffects(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.executionAndAttempt(t)
	f.request.AuthorizationEpoch++
	_, _ = f.orchestrator.FirstBroadcast(context.Background(), f.request, f.artifact, f.address, f.codeHash)
	var permits, submissions int
	f.store.db.QueryRow(`SELECT COUNT(*) FROM canary_send_permits`).Scan(&permits)
	f.store.db.QueryRow(`SELECT COUNT(*) FROM transaction_submissions`).Scan(&submissions)
	if permits != 0 || submissions != 0 {
		t.Fatalf("permits=%d submissions=%d", permits, submissions)
	}
}

func TestGateSnapshotAndPermitAreAtomic(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.executionAndAttempt(t)
	if _, err := f.store.db.Exec(`CREATE TRIGGER test_reject_permit BEFORE INSERT ON canary_send_permits BEGIN SELECT RAISE(ABORT,'forced permit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.orchestrator.FirstBroadcast(context.Background(), f.request, f.artifact, f.address, f.codeHash); err == nil {
		t.Fatal("permit failure accepted")
	}
	var snapshots, permits int
	f.store.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_gate_snapshots WHERE operation_id=? AND stage='FIRST_BROADCAST'`, f.request.OperationID).Scan(&snapshots)
	f.store.db.QueryRow(`SELECT COUNT(*) FROM canary_send_permits`).Scan(&permits)
	if snapshots != 0 || permits != 0 {
		t.Fatalf("snapshots=%d permits=%d", snapshots, permits)
	}
}

func TestImmediateSnapshotFailureDoesNotConsumePermit(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.executionAndAttempt(t)
	first, err := f.orchestrator.FirstBroadcast(context.Background(), f.request, f.artifact, f.address, f.codeHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.db.Exec(`CREATE TRIGGER test_reject_presend_snapshot BEFORE INSERT ON canary_runtime_gate_snapshots WHEN NEW.stage='IMMEDIATE_PRE_SEND' BEGIN SELECT RAISE(ABORT,'forced snapshot failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.orchestrator.ImmediatePreSend(context.Background(), f.request, first.PermitID, PermitFirstBroadcast, f.artifact, f.address, f.codeHash); err == nil {
		t.Fatal("snapshot failure accepted")
	}
	var state string
	if err = f.store.db.QueryRow(`SELECT state FROM canary_send_permits WHERE id=?`, first.PermitID).Scan(&state); err != nil || state != "ISSUED" {
		t.Fatalf("state=%s err=%v", state, err)
	}
}

func TestAuthorizationExpiryFailsClosedWhileRecoveryRemainsAvailable(t *testing.T) {
	f := newOrchestratorFixture(t)
	f.orchestrator.now = func() time.Time { return f.now.Add(2 * time.Hour) }
	d, err := f.orchestrator.ExecutionAdmission(context.Background(), f.request)
	if !errors.Is(err, ErrCanaryGateRejected) || d.ReasonCode != "AUTHORIZATION_EXPIRED" {
		t.Fatalf("%+v %v", d, err)
	}
	ready := f.orchestrator.Readiness(context.Background(), f.request)
	if ready.CanaryAdmissionReady || !ready.RecoveryQueryAvailable || !ready.ReconciliationAvailable {
		t.Fatalf("%+v", ready)
	}
}
