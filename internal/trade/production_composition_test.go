package trade

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestW4ADisabledProductionCompositionFailsClosed(t *testing.T) {
	store := canaryStore(t)
	composition, err := NewProductionComposition(store, ProductionModeDisabled)
	if err != nil {
		t.Fatal(err)
	}
	composition.now = func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }
	if err = composition.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	readiness := composition.Readiness()
	if readiness.CanaryAdmissionReady || readiness.SubmissionSendReady || readiness.RecoveryReady {
		t.Fatalf("W4-A must remain disabled: %#v", readiness)
	}
	if readiness.ReasonCode != "W4A_DISABLED_RECOVERY_NOT_WIRED" {
		t.Fatalf("unexpected readiness reason: %#v", readiness)
	}

	var audits, permits, submissions int
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='W4_PRODUCTION_STARTUP' AND reason_code='PRODUCTION_WIRING_DISABLED'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("durable disabled startup audit = %d, err = %v", audits, err)
	}
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM canary_send_permits WHERE state='CONSUMED'`).Scan(&permits); err != nil || permits != 0 {
		t.Fatalf("consumed permits = %d, err = %v", permits, err)
	}
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM transaction_submissions`).Scan(&submissions); err != nil || submissions != 0 {
		t.Fatalf("submission rows = %d, err = %v", submissions, err)
	}

	var metrics bytes.Buffer
	if err = composition.WriteMetrics(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	for _, metric := range []string{"rbh_canary_admission_ready 0", "rbh_submission_send_ready 0", "rbh_recovery_ready 0"} {
		if !strings.Contains(metrics.String(), metric) {
			t.Fatalf("missing metric %q in %q", metric, metrics.String())
		}
	}
}

func TestW4ARejectsAnyProductionEnablement(t *testing.T) {
	store := canaryStore(t)
	for _, mode := range []string{"", "CONTROLLED_CANARY", "UNRESTRICTED_LIVE"} {
		if _, err := NewProductionComposition(store, mode); err == nil {
			t.Fatalf("mode %q must fail closed", mode)
		}
	}
}

func TestW4AProductionCompositionHasNoEconomicDependencies(t *testing.T) {
	forbidden := []string{"TransactionSigner", "SubmissionService", "RawBroadcaster"}
	for _, typ := range []reflect.Type{reflect.TypeOf(ProductionComposition{}), reflect.TypeOf(ControlledRecoveryWorker{}), reflect.TypeOf(RecoveryService{}), reflect.TypeOf(ProductionRecoveryQuerier{}), reflect.TypeOf(ReadOnlyRecoveryRPC{})} {
		for i := 0; i < typ.NumField(); i++ {
			fieldType := typ.Field(i).Type.String()
			for _, name := range forbidden {
				if strings.Contains(fieldType, name) {
					t.Fatalf("%s dependency %q reaches prohibited %s", typ.Name(), typ.Field(i).Name, name)
				}
			}
		}
	}
	if _, ok := reflect.TypeOf(&ReadOnlyRecoveryRPC{}).MethodByName("SendRawTransaction"); ok {
		t.Fatal("read-only recovery RPC exposes SendRawTransaction")
	}
}

type productionRecoveryHealthFake struct {
	mu  sync.Mutex
	err error
}

func (f *productionRecoveryHealthFake) CheckRecoveryHealth(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

type productionRecoveryQueryFake struct {
	mu       sync.Mutex
	evidence ControlledRecoveryEvidence
	err      error
	calls    int
	entered  chan struct{}
	release  chan struct{}
}

func (f *productionRecoveryQueryFake) QueryRecovery(context.Context, ControlledRecoveryQuery) (ControlledRecoveryEvidence, error) {
	f.mu.Lock()
	f.calls++
	entered, release, evidence, err := f.entered, f.release, f.evidence, f.err
	f.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	return evidence, err
}

func (f *productionRecoveryQueryFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func productionRecoveryCompositionFixture(t *testing.T, query *productionRecoveryQueryFake, health *productionRecoveryHealthFake) (*Store, *ProductionComposition, SignedArtifact, time.Time) {
	t.Helper()
	store, kernel, artifact, closeDB := seededSignedArtifact(t)
	t.Cleanup(closeDB)
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	if _, err := store.BeginSubmission(context.Background(), artifact, false, now); err != nil {
		t.Fatal(err)
	}
	latest, _, _ := store.LatestSubmission(context.Background(), artifact.AttemptID)
	if err := store.FinishSubmission(context.Background(), artifact, latest, "broadcast_unknown", "", "ambiguous_transport", "test", now); err != nil {
		t.Fatal(err)
	}
	backend := &fakeReceiptBackend{}
	recovery, err := NewRecoveryServiceWithCipher(store, kernel.cipher, backend, fakeEffectResolver{}, CanonicalPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	composition, err := NewProductionComposition(store, ProductionModeDisabled)
	if err != nil {
		t.Fatal(err)
	}
	composition.now = func() time.Time { return now }
	if query == nil {
		query = &productionRecoveryQueryFake{evidence: ControlledRecoveryEvidence{NonceState: "RESERVED_UNRESOLVED", EvidenceHash: strings.Repeat("d", 64), ObservedAt: now}}
	}
	if health == nil {
		health = &productionRecoveryHealthFake{}
	}
	if err = composition.ConfigureRecovery(recovery, query, health, ProductionRecoveryConfig{Environment: "production", HolderID: "trade-service-1", LeaseTTL: time.Minute, ScanInterval: 10 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	return store, composition, artifact, now
}

func TestW4CProductionRecoveryRunsWithoutAuthorizationOrSendReadiness(t *testing.T) {
	query := &productionRecoveryQueryFake{}
	store, composition, artifact, now := productionRecoveryCompositionFixture(t, query, nil)
	query.evidence = ControlledRecoveryEvidence{NonceState: "RESERVED_UNRESOLVED", EvidenceHash: strings.Repeat("e", 64), ObservedAt: now}
	if _, err := store.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1,updated_at=? WHERE singleton=1`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := composition.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ready := composition.Readiness()
	if ready.CanaryAdmissionReady || ready.SubmissionSendReady || !ready.RecoveryReady {
		t.Fatalf("readiness=%+v", ready)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- composition.Run(ctx) }()
	deadline := time.After(2 * time.Second)
	for query.callCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("durable broadcast_unknown work was not rediscovered")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var state, lane, reservation string
	_ = store.db.QueryRow(`SELECT state FROM transaction_submissions WHERE attempt_id=? ORDER BY sequence DESC LIMIT 1`, artifact.AttemptID).Scan(&state)
	_ = store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id=?`, artifact.WalletID).Scan(&lane)
	_ = store.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id=?`, artifact.Operation).Scan(&reservation)
	if state != "broadcast_unknown" || lane != "frozen" || reservation != "frozen" {
		t.Fatalf("state=%s lane=%s reservation=%s", state, lane, reservation)
	}
}

func TestW4CSubmittedRecoveryDoesNotRequireAuthorizationOrQueryPermit(t *testing.T) {
	query := &productionRecoveryQueryFake{}
	store, composition, artifact, _ := productionRecoveryCompositionFixture(t, query, nil)
	if _, err := store.db.Exec(`UPDATE transaction_submissions SET state='submitted',rpc_hash=tx_hash WHERE attempt_id=?`, artifact.AttemptID); err != nil {
		t.Fatal(err)
	}
	if err := composition.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := composition.worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if query.callCount() != 0 {
		t.Fatalf("submitted recovery unexpectedly used unknown querier: %d", query.callCount())
	}
	var authorizations, permits int
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_authorizations`).Scan(&authorizations)
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM canary_send_permits WHERE state='CONSUMED'`).Scan(&permits)
	if authorizations != 0 || permits != 0 {
		t.Fatalf("authorizations=%d consumed_permits=%d", authorizations, permits)
	}
}

func TestW4CRecoveryAndSubmissionLeasesAreIndependent(t *testing.T) {
	store, composition, _, now := productionRecoveryCompositionFixture(t, nil, nil)
	if err := composition.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.AcquireCanaryWorkerLease(context.Background(), "SUBMISSION", "production", "submission-worker", 1, now.Add(time.Minute), now); err != nil {
		t.Fatalf("independent SUBMISSION lease: %v", err)
	}
	var recovery, submission int
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM canary_worker_leases WHERE role='RECOVERY'`).Scan(&recovery)
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM canary_worker_leases WHERE role='SUBMISSION'`).Scan(&submission)
	if recovery != 1 || submission != 1 {
		t.Fatalf("recovery leases=%d submission leases=%d", recovery, submission)
	}
}

func TestW4CRecoveryLeaseTakeoverFencesStaleProductionOwner(t *testing.T) {
	store, composition, _, now := productionRecoveryCompositionFixture(t, nil, nil)
	if err := composition.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	takeoverAt := now.Add(2 * time.Minute)
	epoch, err := store.AcquireNextCanaryWorkerLease(context.Background(), "RECOVERY", "production", "trade-service-2", time.Minute, takeoverAt)
	if err != nil || epoch != 2 {
		t.Fatalf("takeover epoch=%d err=%v", epoch, err)
	}
	composition.worker.now = func() time.Time { return takeoverAt }
	if err = composition.worker.RunOnce(context.Background()); !errors.Is(err, ErrCanaryRecoveryLeaseLost) {
		t.Fatalf("stale owner err=%v", err)
	}
}

func TestW4CShutdownDrainsCurrentRecoveryAndRestartRediscovers(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	query := &productionRecoveryQueryFake{entered: entered, release: release}
	store, composition, artifact, now := productionRecoveryCompositionFixture(t, query, nil)
	query.evidence = ControlledRecoveryEvidence{NonceState: "RESERVED_UNRESOLVED", EvidenceHash: strings.Repeat("f", 64), ObservedAt: now}
	if err := composition.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- composition.Run(ctx) }()
	<-entered
	cancel()
	select {
	case err := <-done:
		t.Fatalf("shutdown returned before in-flight recovery drained: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	items, err := store.ListControlledRecoveryItems(context.Background())
	if err != nil || len(items) != 1 || items[0].OperationID != artifact.Operation {
		t.Fatalf("durable rediscovery items=%+v err=%v", items, err)
	}
}

func TestW4CRPCFailureDropsReadinessAndCreatesDurableAlert(t *testing.T) {
	query := &productionRecoveryQueryFake{err: errors.New("rpc unavailable")}
	store, composition, _, _ := productionRecoveryCompositionFixture(t, query, nil)
	if err := composition.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := composition.Run(context.Background())
	if err == nil || composition.Readiness().RecoveryReady {
		t.Fatalf("err=%v readiness=%+v", err, composition.Readiness())
	}
	var alerts int
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='CONTROLLED_RECOVERY_WORKER' AND reason_code='RECOVERY_EVIDENCE_UNAVAILABLE'`).Scan(&alerts)
	if alerts != 1 {
		t.Fatalf("durable recovery alerts=%d", alerts)
	}
}

func TestW4CStartupHealthFailureIsNotReadyAndAudited(t *testing.T) {
	health := &productionRecoveryHealthFake{err: errors.New("rpc unavailable")}
	store, composition, _, _ := productionRecoveryCompositionFixture(t, nil, health)
	if err := composition.Start(context.Background()); err == nil {
		t.Fatal("unhealthy recovery startup unexpectedly passed")
	}
	if composition.Readiness().RecoveryReady {
		t.Fatalf("readiness=%+v", composition.Readiness())
	}
	var alerts int
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='W4C_PRODUCTION_RECOVERY' AND reason_code='RECOVERY_HEALTH_UNAVAILABLE'`).Scan(&alerts)
	if alerts != 1 {
		t.Fatalf("startup alerts=%d", alerts)
	}
}

func TestPonsCurveEffectResolverDerivesCanonicalBuyEffect(t *testing.T) {
	store, _, artifact, closeDB := seededSignedArtifact(t)
	defer closeDB()
	dryRun, found, err := store.Result(context.Background(), artifact.Operation)
	if err != nil || !found {
		t.Fatalf("dry run found=%v err=%v", found, err)
	}
	word := func(value int64) []byte {
		out := make([]byte, 32)
		big.NewInt(value).FillBytes(out)
		return out
	}
	data := append(append(append(word(1000), word(2500)...), word(10)...), word(0)...)
	addressTopic := func(address common.Address) common.Hash {
		return common.BytesToHash(common.LeftPadBytes(address.Bytes(), 32))
	}
	receipt := &types.Receipt{TxHash: common.HexToHash(artifact.TxHash), Status: types.ReceiptStatusSuccessful, Logs: []*types.Log{{
		Address: dryRun.Route.Curve,
		Topics:  []common.Hash{parser.TopicPonsCurveBuy, addressTopic(common.HexToAddress(artifact.From)), addressTopic(common.HexToAddress(artifact.From))},
		Data:    data,
	}}}
	resolver, _ := NewPonsCurveEffectResolver(store)
	effect, err := resolver.ResolvePositionEffect(context.Background(), artifact, receipt)
	if err != nil || effect.Asset != dryRun.Route.Token.Hex() || effect.Delta != "2500" {
		t.Fatalf("effect=%+v err=%v", effect, err)
	}
}
