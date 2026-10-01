package trade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"
)

type controlledSubmissionFixture struct {
	store        *Store
	kernel       *ExecutionKernel
	orchestrator *ControlledCanaryOrchestrator
	service      *SubmissionService
	worker       *ControlledSubmissionWorker
	broadcaster  *fakeBroadcaster
	request      CanaryGateRequest
	artifact     CanaryArtifactIdentity
	signed       SignedArtifact
	raw          []byte
	now          time.Time
}

func newControlledSubmissionFixture(t *testing.T, broadcaster *fakeBroadcaster) controlledSubmissionFixture {
	t.Helper()
	store, kernel, signed, closeDB := seededSignedArtifact(t)
	t.Cleanup(closeDB)
	seedCanary(t, store, 0)
	now := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	request := canaryRequest("w4b", now)
	request.OperationID = signed.Operation
	request.QuoteBlockNumber = signed.QuoteBlockNumber
	request.QuoteBlockHash = signed.QuoteBlockHash
	request.ExpiresAt = signed.ExpiresAt
	source := sourceFromRequest(request)
	stamp := now.Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`INSERT INTO canary_admission_sources VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, source.OperationID, source.WalletID, source.WalletAddress, source.Protocol, source.Direction, source.TokenAddress, source.ContractAddress, source.ContractRole, source.RuntimeCodeHash, source.Amount, source.GasLimit, source.GasFeeCap, source.GasTipCap, source.NativeBalance, source.SlippageBPS, source.SellBPS, source.PolicyVersion, source.QuoteBlockNumber, source.QuoteBlockHash, source.ExpiresAt, source.hash(), stamp); err != nil {
		t.Fatal(err)
	}
	if decision, err := store.AdmitControlledCanary(context.Background(), request, now); err != nil || decision.Decision != "ADMITTED" {
		t.Fatalf("admission=%+v err=%v", decision, err)
	}
	authorization, err := store.CreateRuntimeAuthorization(context.Background(), runtimeAuthorization("w4b-auth", now), now)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.TransitionRuntimeAuthorization(context.Background(), authorization.ID, RuntimeAuthorizationPending, RuntimeAuthorizationArmed, now); err != nil {
		t.Fatal(err)
	}
	stored, found, err := store.LoadEncryptedArtifact(context.Background(), signed.Operation)
	if err != nil || !found {
		t.Fatal(err)
	}
	raw, err := kernel.cipher.Decrypt(stored.KeyVersion, stored.Ciphertext, stored.EncryptionNonce, artifactAAD(stored.Operation, stored.StepID, stored.AttemptID))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	identity := CanaryArtifactIdentity{OperationID: signed.Operation, AttemptID: signed.AttemptID, TxHash: signed.TxHash, ArtifactHash: hex.EncodeToString(digest[:])}
	gate := CanaryGateRequest{OperationID: signed.Operation, AttemptID: signed.AttemptID, AuthorizationID: authorization.ID, AuthorizationEpoch: authorization.Epoch, PolicyVersion: 1, ChainID: ChainID, WalletID: signed.WalletID, Deployment: authorization.Deployment}
	verifier := &controlledArtifactVerifier{want: identity}
	runtime := &controlledRuntimeReader{address: request.ContractAddress, hash: request.RuntimeCodeHash}
	querier := &controlledUnknownQuerier{want: identity, evidence: UnknownReplayEvidence{NonceState: "RESERVED_UNRESOLVED", EvidenceHash: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", QueriedAt: now}}
	orchestrator, err := NewControlledCanaryOrchestrator(store, verifier, runtime, querier)
	if err != nil {
		t.Fatal(err)
	}
	orchestrator.now = func() time.Time { return now }
	if decision, err := orchestrator.ExecutionAdmission(context.Background(), gate); err != nil || decision.Decision != "PASS" {
		t.Fatalf("execution gate=%+v err=%v", decision, err)
	}
	if decision, err := orchestrator.PreSign(context.Background(), gate); err != nil || decision.Decision != "PASS" {
		t.Fatalf("pre-sign gate=%+v err=%v", decision, err)
	}
	if decision, err := orchestrator.FirstBroadcast(context.Background(), gate, identity); err != nil || decision.Decision != "PASS" {
		t.Fatalf("first gate=%+v err=%v", decision, err)
	}
	if broadcaster == nil {
		broadcaster = &fakeBroadcaster{hash: signed.TxHash}
	}
	service, err := NewSubmissionService(store, kernel, broadcaster)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AcquireCanaryWorkerLease(context.Background(), "SUBMISSION", "controlled-test", "worker-1", 1, now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	worker, err := NewControlledSubmissionWorker(store, orchestrator, service, "controlled-test", "worker-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return now }
	return controlledSubmissionFixture{store: store, kernel: kernel, orchestrator: orchestrator, service: service, worker: worker, broadcaster: broadcaster, request: gate, artifact: identity, signed: signed, raw: raw, now: now}
}

func TestControlledSubmissionFirstBroadcastExactlyOnce(t *testing.T) {
	f := newControlledSubmissionFixture(t, nil)
	if err := f.worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.broadcaster.calls != 1 || len(f.broadcaster.raws) != 1 || !bytes.Equal(f.raw, f.broadcaster.raws[0]) {
		t.Fatalf("calls=%d exact=%v", f.broadcaster.calls, len(f.broadcaster.raws) == 1 && bytes.Equal(f.raw, f.broadcaster.raws[0]))
	}
	latest, found, err := f.store.LatestSubmission(context.Background(), f.signed.AttemptID)
	if err != nil || !found || latest.State != "submitted" {
		t.Fatalf("latest=%+v found=%v err=%v", latest, found, err)
	}
}

func TestControlledSubmissionDeterministicKnownUnsent(t *testing.T) {
	f := newControlledSubmissionFixture(t, &fakeBroadcaster{err: ErrBroadcastRejected})
	if err := f.worker.RunOnce(context.Background()); !errors.Is(err, ErrBroadcastRejected) {
		t.Fatalf("err=%v", err)
	}
	latest, _, _ := f.store.LatestSubmission(context.Background(), f.signed.AttemptID)
	var lane, reservation string
	_ = f.store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id=?`, f.signed.WalletID).Scan(&lane)
	_ = f.store.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id=?`, f.signed.Operation).Scan(&reservation)
	if latest.State != "known_unsent" || lane != "idle" || reservation != "released" {
		t.Fatalf("state=%s lane=%s reservation=%s", latest.State, lane, reservation)
	}
}

func TestControlledSubmissionAmbiguousFreezesWithoutSecondSend(t *testing.T) {
	f := newControlledSubmissionFixture(t, &fakeBroadcaster{err: ErrBroadcastAmbiguous})
	if err := f.worker.RunOnce(context.Background()); !errors.Is(err, ErrBroadcastAmbiguous) {
		t.Fatalf("err=%v", err)
	}
	if err := f.worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	latest, _, _ := f.store.LatestSubmission(context.Background(), f.signed.AttemptID)
	var lane, reservation string
	_ = f.store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id=?`, f.signed.WalletID).Scan(&lane)
	_ = f.store.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id=?`, f.signed.Operation).Scan(&reservation)
	if latest.State != "broadcast_unknown" || lane != "frozen" || reservation != "frozen" || f.broadcaster.calls != 1 {
		t.Fatalf("state=%s lane=%s reservation=%s calls=%d", latest.State, lane, reservation, f.broadcaster.calls)
	}
}

func TestControlledSubmissionLeaseLossBlocksSend(t *testing.T) {
	f := newControlledSubmissionFixture(t, nil)
	f.worker.now = func() time.Time { return f.now.Add(2 * time.Minute) }
	if err := f.worker.RunOnce(context.Background()); !errors.Is(err, ErrCanarySubmissionLeaseLost) {
		t.Fatalf("err=%v", err)
	}
	if f.broadcaster.calls != 0 {
		t.Fatalf("broadcast calls=%d", f.broadcaster.calls)
	}
}

func TestControlledSubmissionUnknownReplayUsesExactArtifact(t *testing.T) {
	f := newControlledSubmissionFixture(t, &fakeBroadcaster{err: ErrBroadcastAmbiguous})
	if err := f.worker.RunOnce(context.Background()); !errors.Is(err, ErrBroadcastAmbiguous) {
		t.Fatalf("first err=%v", err)
	}
	f.broadcaster.err = nil
	f.broadcaster.hash = f.signed.TxHash
	decision, err := f.orchestrator.UnknownReplay(context.Background(), f.request, f.artifact)
	if err != nil || decision.Decision != "PASS" {
		t.Fatalf("replay gate=%+v err=%v", decision, err)
	}
	if err = f.worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.broadcaster.calls != 2 || !bytes.Equal(f.broadcaster.raws[0], f.raw) || !bytes.Equal(f.broadcaster.raws[1], f.raw) {
		t.Fatalf("calls=%d exact artifact replay failed", f.broadcaster.calls)
	}
	var attempts int
	_ = f.store.db.QueryRow(`SELECT COUNT(*) FROM transaction_attempts WHERE id=?`, f.signed.AttemptID).Scan(&attempts)
	if attempts != 1 {
		t.Fatalf("transaction attempts=%d", attempts)
	}
}

func TestControlledSubmissionEmergencyStopBeforeFinalGatePreservesPermit(t *testing.T) {
	f := newControlledSubmissionFixture(t, nil)
	if _, err := f.store.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1,updated_at=? WHERE singleton=1`, f.now.Add(time.Second).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.RunOnce(context.Background()); !errors.Is(err, ErrCanaryGateRejected) {
		t.Fatalf("err=%v", err)
	}
	var state string
	_ = f.store.db.QueryRow(`SELECT state FROM canary_send_permits LIMIT 1`).Scan(&state)
	if f.broadcaster.calls != 0 || state != "ISSUED" {
		t.Fatalf("calls=%d permit=%s", f.broadcaster.calls, state)
	}
}

func TestControlledSubmissionRestartWithPreparedSendBecomesUnknown(t *testing.T) {
	f := newControlledSubmissionFixture(t, nil)
	work, err := f.store.ListControlledSubmissionWork(context.Background())
	if err != nil || len(work) != 1 {
		t.Fatalf("work=%d err=%v", len(work), err)
	}
	snapshot, _, err := f.orchestrator.immediatePreSendSnapshot(context.Background(), work[0].Request, work[0].PermitID, work[0].Purpose, work[0].Artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.store.BeginControlledSubmissionAfterGate(context.Background(), snapshot, work[0].Request, work[0].Artifact, work[0].PermitID, work[0].Purpose, f.worker.leaseFence(), f.now); err != nil {
		t.Fatal(err)
	}
	f.worker.now = func() time.Time { return f.now.Add(time.Second) }
	if err = f.worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	latest, _, _ := f.store.LatestSubmission(context.Background(), f.signed.AttemptID)
	if latest.State != "broadcast_unknown" || f.broadcaster.calls != 0 {
		t.Fatalf("state=%s calls=%d", latest.State, f.broadcaster.calls)
	}
}

func TestControlledSubmissionSendSerializesLeaseTakeover(t *testing.T) {
	started := make(chan struct{})
	takeover := make(chan error, 1)
	b := &fakeBroadcaster{}
	f := newControlledSubmissionFixture(t, b)
	b.hash = f.signed.TxHash
	b.before = func() {
		close(started)
		go func() {
			takeover <- f.store.AcquireCanaryWorkerLease(context.Background(), "SUBMISSION", "controlled-test", "worker-2", 2, f.now.Add(3*time.Minute), f.now.Add(2*time.Minute))
		}()
	}
	if err := f.worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := <-takeover; err != nil {
		t.Fatal(err)
	}
	var holder string
	_ = f.store.db.QueryRow(`SELECT holder_id FROM canary_worker_leases WHERE role='SUBMISSION' AND environment='controlled-test'`).Scan(&holder)
	if holder != "worker-2" || f.broadcaster.calls != 1 {
		t.Fatalf("holder=%s calls=%d", holder, f.broadcaster.calls)
	}
}

func TestControlledSubmissionSendSerializesEmergencyStop(t *testing.T) {
	stopResult := make(chan error, 1)
	b := &fakeBroadcaster{}
	f := newControlledSubmissionFixture(t, b)
	b.hash = f.signed.TxHash
	b.before = func() {
		go func() {
			_, err := f.store.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1,updated_at=? WHERE singleton=1`, f.now.Add(time.Second).Format(time.RFC3339Nano))
			stopResult <- err
		}()
	}
	if err := f.worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-stopResult; err != nil {
		t.Fatal(err)
	}
	var stopped int
	_ = f.store.db.QueryRow(`SELECT emergency_stopped FROM canary_control_state WHERE singleton=1`).Scan(&stopped)
	if stopped != 1 || f.broadcaster.calls != 1 {
		t.Fatalf("stopped=%d calls=%d", stopped, f.broadcaster.calls)
	}
}

func TestControlledSubmissionDuplicateWorkersOneSend(t *testing.T) {
	f := newControlledSubmissionFixture(t, nil)
	second, err := NewControlledSubmissionWorker(f.store, f.orchestrator, f.service, "controlled-test", "worker-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	second.now = func() time.Time { return f.now }
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, worker := range []*ControlledSubmissionWorker{f.worker, second} {
		wg.Add(1)
		go func(w *ControlledSubmissionWorker) {
			defer wg.Done()
			errs <- w.RunOnce(context.Background())
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err = range errs {
		if err != nil && !errors.Is(err, ErrCanaryGateRejected) && !errors.Is(err, ErrCanaryRuntimeRejected) {
			t.Fatalf("unexpected worker error: %v", err)
		}
	}
	if f.broadcaster.calls != 1 {
		t.Fatalf("broadcast calls=%d", f.broadcaster.calls)
	}
}
