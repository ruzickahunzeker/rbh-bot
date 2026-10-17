package tradeapi

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type runtimeTestClock struct {
	mu                 sync.Mutex
	wall               time.Time
	ticks, uncertainty time.Duration
	calls              int
	change             func(*runtimeTestClock)
}

func (c *runtimeTestClock) sample(ctx context.Context) (manualRuntimeSample, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.change != nil {
		c.change(c)
	}
	c.ticks += time.Nanosecond
	return manualRuntimeSample{wall: c.wall.UTC().Format(time.RFC3339Nano), errorBound: c.uncertainty, ticks: c.ticks, provider: "trusted-fixture", session: "session-1"}, ctx.Err()
}

func (c *runtimeTestClock) move(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.wall = t }

type runtimeTestIdentity struct {
	process manualRuntimeProcess
	calls   int
	fail    bool
}

func (i *runtimeTestIdentity) identify(context.Context) (manualRuntimeProcess, error) {
	i.calls++
	if i.fail {
		return manualRuntimeProcess{}, ErrInvalid
	}
	return i.process, nil
}

type runtimeTestObserver struct {
	calls                int
	during               func()
	bad                  string
	environment, profile string
}

func (o *runtimeTestObserver) observe(ctx context.Context, f manualRuntimeChainFacts) (chainquery.SubmissionRuntimeObservation, error) {
	o.calls++
	if o.during != nil {
		o.during()
	}
	if o.bad == "unavailable" {
		return chainquery.SubmissionRuntimeObservation{}, ErrInvalid
	}
	r := chainquery.SubmissionRuntimeObservation{ChainID: 4663, ProfileID: o.profile, Environment: o.environment, Curve: f.curve, RuntimeCodeHash: f.runtimeHash, NetworkCheckpoint: f.quote, Quote: f.quote, Anchor: f.quote}
	switch o.bad {
	case "chain":
		r.ChainID = 56
	case "hash":
		r.RuntimeCodeHash = "0x" + strings.Repeat("f", 64)
	case "quote":
		r.Quote.Hash = "0x" + strings.Repeat("f", 64)
	case "profile":
		r.ProfileID = "other"
	case "old_anchor":
		r.Anchor.Number--
	case "environment":
		r.Environment = "other"
	}
	r.EvidenceHash = r.Digest()
	if o.bad == "digest" {
		r.EvidenceHash = strings.Repeat("0", 64)
	}
	return r, ctx.Err()
}

func runtimeVerifierFixture(t *testing.T, side string) (*storage.Database, *Store, string, *manualRuntimeVerifier, Identity, string, *runtimeTestClock, *runtimeTestObserver) {
	t.Helper()
	d, s, h, _, scope, path, artifact, cipher := submissionFoundationFixture(t, side)
	if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
		t.Fatal(err)
	}
	f := submissionFoundationFor(t, s, artifact.operation)
	if err := insertSubmissionFoundation(s.db, f, "INSERT"); err != nil {
		t.Fatal(err)
	}
	stamp, err := canonicalManualTime(f.created)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO canary_worker_leases VALUES('SUBMISSION',?,'runtime-owner',1,?,?)`, scope.Environment, stamp.Add(time.Minute).Format(time.RFC3339Nano), f.created); err != nil {
		t.Fatal(err)
	}
	clock := &runtimeTestClock{wall: stamp}
	if err = initializeManualRuntimeClock(context.Background(), s, scope.Environment, clock, time.Second); err != nil {
		t.Fatal(err)
	}
	process := manualRuntimeProcess{scope: scope, binaryHash: strings.Repeat("1", 64), manifestHash: strings.Repeat("2", 64), networkProfile: "local-network", holder: "runtime-owner", provider: "trusted-fixture", leaseEpoch: 1}
	if err = s.db.QueryRow(`SELECT quote_block_number,quote_block_hash FROM manual_execution_bindings WHERE operation_id=?`, f.operation).Scan(&process.networkCheckpoint.Number, &process.networkCheckpoint.Hash); err != nil {
		t.Fatal(err)
	}
	process.manifestHash = runtimeManifestDigest(process)
	observer := &runtimeTestObserver{environment: scope.Environment, profile: process.networkProfile}
	v := &manualRuntimeVerifier{store: s, profile: manualRuntimeProfile{process: process, maximumError: time.Second, queryTimeout: time.Second, maxAge: time.Minute}, timeSource: clock, identity: &runtimeTestIdentity{process: process}, decryptor: cipher, observer: observer}
	var request string
	if err = s.db.QueryRow(`SELECT request_id FROM manual_execution_bindings WHERE operation_id=?`, f.operation).Scan(&request); err != nil {
		t.Fatal(err)
	}
	return d, s, path, v, h.identity, request, clock, observer
}

func TestHTTPM4B22ThreeSidesFreshDuplicateAndReopen(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			d, s, path, v, id, request, clock, observer := runtimeVerifierFixture(t, side)
			before := foundationEconomicSnapshot(t, s)
			for i := 0; i < 2; i++ {
				candidate, err := v.capture(context.Background(), id, request)
				if err != nil {
					t.Fatal(err)
				}
				if candidate.status != "VERIFIED_RUNTIME_CANDIDATE_NOT_PERMISSION" || candidate.operation != "manual_op_"+request || candidate.clockGeneration != int64(i+2) || !candidate.snapshotOnly || !candidate.freshGateRequired || candidate.executionAuthorized || candidate.signingAuthorized || candidate.sendAuthorized || candidate.replayAuthorized {
					t.Fatal("candidate/permissions", candidate)
				}
				if before != foundationEconomicSnapshot(t, s) {
					t.Fatal("economic identity changed")
				}
			}
			if observer.calls != 2 || count(t, s.db, "manual_submission_gate_evidence") != 1 {
				t.Fatal("duplicate cached authority or duplicate metadata")
			}
			_, s = bindingReopen(t, d, path)
			v.store = s
			clock.move(clock.wall.Add(time.Nanosecond))
			if _, err := v.capture(context.Background(), id, request); err != nil {
				t.Fatal("restart fresh verification", err)
			}
			if observer.calls != 3 || before != foundationEconomicSnapshot(t, s) {
				t.Fatal("restart changed identity or skipped observer")
			}
		})
	}
}

func TestHTTPM4B22StopPriorityAndDurableSourceDenial(t *testing.T) {
	for _, kind := range []string{"stop", "revoke", "frozen", "lease", "owner", "epoch", "deployment", "dirty", "uninitialized", "clock_corrupt"} {
		t.Run(kind, func(t *testing.T) {
			_, s, _, v, id, request, clock, observer := runtimeVerifierFixture(t, "buy")
			switch kind {
			case "stop":
				_, _ = s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`)
				request = "invalid"
				clock.change = func(*runtimeTestClock) { t.Fatal("stop consulted time provider") }
			case "revoke":
				_, _ = s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`)
			case "frozen":
				_, _ = s.db.Exec(`UPDATE canary_risk_reservations SET state='frozen'`)
			case "lease":
				_, _ = s.db.Exec(`DELETE FROM canary_worker_leases WHERE role='SUBMISSION'`)
			case "owner":
				v.profile.process.holder = "other"
				v.profile.process.manifestHash = runtimeManifestDigest(v.profile.process)
				v.identity.(*runtimeTestIdentity).process = v.profile.process
			case "epoch":
				v.profile.process.leaseEpoch = 2
				v.profile.process.manifestHash = runtimeManifestDigest(v.profile.process)
				v.identity.(*runtimeTestIdentity).process = v.profile.process
			case "deployment":
				v.identity.(*runtimeTestIdentity).process.scope.DeploymentID = "other"
			case "dirty":
				v.identity.(*runtimeTestIdentity).process.dirty = true
			case "uninitialized":
				v.profile.process.scope.Environment = "missing"
				v.profile.process.manifestHash = runtimeManifestDigest(v.profile.process)
				v.identity.(*runtimeTestIdentity).process = v.profile.process
			case "clock_corrupt":
				seedSigningWitnessCorruption(t, s, "canary_runtime_audit", `DELETE FROM canary_runtime_audit WHERE event_type='MANUAL_SUBMISSION_CLOCK'`)
			}
			before := foundationEconomicSnapshot(t, s)
			candidate, err := v.capture(context.Background(), id, request)
			if err == nil || candidate != (manualRuntimeCandidate{}) || observer.calls != 0 || before != foundationEconomicSnapshot(t, s) {
				t.Fatal("denial side effect", kind, err, candidate)
			}
			if kind == "stop" && !strings.Contains(err.Error(), "EMERGENCY_STOPPED") {
				t.Fatal("stop priority", err)
			}
		})
	}
}

func TestHTTPM4B22RPCDriftTTLAndFinalFence(t *testing.T) {
	for _, kind := range []string{"chain", "hash", "quote", "profile", "old_anchor", "environment", "digest", "unavailable", "stop_during_rpc", "revoke_during_rpc", "lease_takeover", "expired_after_rpc", "expired_after_crypto", "stale"} {
		t.Run(kind, func(t *testing.T) {
			_, s, _, v, id, request, clock, observer := runtimeVerifierFixture(t, "buy")
			var expiry string
			if err := s.db.QueryRow(`SELECT expires_at FROM manual_execution_bindings`).Scan(&expiry); err != nil {
				t.Fatal(err)
			}
			end, _ := canonicalManualTime(expiry)
			switch kind {
			case "stop_during_rpc":
				observer.during = func() {
					if _, err := s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`); err != nil {
						t.Fatal(err)
					}
				}
			case "revoke_during_rpc":
				observer.during = func() {
					if _, err := s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
						t.Fatal(err)
					}
				}
			case "lease_takeover":
				observer.during = func() {
					if _, err := s.db.Exec(`UPDATE canary_worker_leases SET holder_id='takeover',lease_epoch=2`); err != nil {
						t.Fatal(err)
					}
				}
			case "expired_after_rpc":
				observer.during = func() { clock.move(end) }
			case "expired_after_crypto":
				v.checkpoint = func(stage string) error {
					if stage == "before_final_time" {
						clock.move(end)
					}
					return nil
				}
			case "stale":
				v.profile.maxAge = time.Nanosecond
			default:
				observer.bad = kind
			}
			candidate, err := v.capture(context.Background(), id, request)
			if err == nil || candidate != (manualRuntimeCandidate{}) || count(t, s.db, "manual_submission_gate_evidence") != 0 || count(t, s.db, "canary_send_permits") != 0 || count(t, s.db, "transaction_submissions") != 0 {
				t.Fatal("RPC/time failure committed candidate", kind, err)
			}
			if count(t, s.db, "manual_gate_clock_events") != 1 {
				t.Fatal("denial advanced floor")
			}
		})
	}
}

func TestHTTPM4B22ClockBoundsReversalLatchAndRestart(t *testing.T) {
	for _, kind := range []string{"before", "equal", "after", "uncertainty", "reverse_recovered", "restart_floor"} {
		t.Run(kind, func(t *testing.T) {
			d, s, path, v, id, request, clock, _ := runtimeVerifierFixture(t, "buy")
			var raw string
			if err := s.db.QueryRow(`SELECT expires_at FROM manual_execution_bindings`).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			end, _ := canonicalManualTime(raw)
			switch kind {
			case "before":
				clock.move(end.Add(-time.Nanosecond))
			case "equal":
				clock.move(end)
			case "after":
				clock.move(end.Add(time.Nanosecond))
			case "uncertainty":
				clock.move(end.Add(-time.Nanosecond))
				clock.uncertainty = time.Nanosecond
			case "reverse_recovered":
				clock.change = func(c *runtimeTestClock) {
					if c.calls == 5 {
						c.wall = c.wall.Add(-time.Nanosecond)
					}
					if c.calls == 6 {
						c.wall = c.wall.Add(time.Second)
					}
				}
			case "restart_floor":
				clock.move(clock.wall.Add(-time.Nanosecond))
			}
			candidate, err := v.capture(context.Background(), id, request)
			if kind == "before" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || candidate != (manualRuntimeCandidate{}) {
				t.Fatal("clock fail open", kind, err)
			}
			if kind == "reverse_recovered" || kind == "restart_floor" {
				var latched int
				if e := s.db.QueryRow(`SELECT fault_latched FROM manual_gate_clock_state`).Scan(&latched); e != nil || latched != 1 {
					t.Fatal("no durable latch", latched, e)
				}
				_, s = bindingReopen(t, d, path)
				v.store = s
				v.unhealthy.Store(false)
				clock.change = nil
				clock.move(end.Add(-time.Nanosecond))
				if _, e := v.capture(context.Background(), id, request); e == nil {
					t.Fatal("restart cleared latch")
				}
			}
		})
	}
}

func TestHTTPM4B22CaptureAuditFaultRollbackAndCancellation(t *testing.T) {
	for _, kind := range []string{"audit", "alert", "cancel", "decrypt"} {
		t.Run(kind, func(t *testing.T) {
			_, s, _, v, id, request, _, _ := runtimeVerifierFixture(t, "buy")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "audit":
				_, _ = s.db.Exec(`CREATE TRIGGER runtime_fault BEFORE INSERT ON canary_runtime_audit WHEN NEW.event_type='MANUAL_SUBMISSION_GATE_EVIDENCE' BEGIN SELECT RAISE(FAIL,'forced audit'); END`)
			case "alert":
				_, _ = s.db.Exec(`CREATE TRIGGER runtime_fault BEFORE INSERT ON canary_alert_outbox WHEN NEW.id LIKE 'manual-gate-alert:%' BEGIN SELECT RAISE(FAIL,'forced alert'); END`)
			case "cancel":
				v.checkpoint = func(stage string) error {
					if stage == "before_final_time" {
						cancel()
					}
					return nil
				}
			case "decrypt":
				v.decryptor = runtimeBrokenDecryptor{}
			}
			before := foundationEconomicSnapshot(t, s)
			candidate, err := v.capture(ctx, id, request)
			if err == nil || candidate != (manualRuntimeCandidate{}) || count(t, s.db, "manual_gate_clock_events") != 1 || count(t, s.db, "manual_submission_gate_evidence") != 0 || count(t, s.db, "manual_gate_evidence_scopes") != 0 || before != foundationEconomicSnapshot(t, s) {
				t.Fatal("capture failure retained metadata/economic effect", kind, err)
			}
		})
	}
}

type runtimeBrokenDecryptor struct{}

func (runtimeBrokenDecryptor) KeyVersion() string { return "broken" }
func (runtimeBrokenDecryptor) Decrypt(string, []byte, []byte, []byte) ([]byte, error) {
	return nil, errors.New("fixture decrypt failed")
}

func TestHTTPM4B22PrivateDependenciesAndOriginalGuards(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, dir := range []string{"cmd", "internal"} {
		if err := filepath.WalkDir(filepath.Join(root, dir), func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			base := filepath.Base(path)
			owned := strings.HasPrefix(base, "manual_runtime_")
			if !owned && strings.Contains(string(raw), "manualRuntimeVerifier") {
				t.Errorf("runtime verifier escaped private files: %s", path)
			}
			if owned {
				for _, bad := range []string{"SubmissionService", "SendRawTransaction", "RawBroadcaster", "IssueCanary", "ConsumeCanary", "ReserveNonce", "manualsign", "func New", "type Manual"} {
					if strings.Contains(string(raw), bad) {
						t.Errorf("prohibited dependency %s in %s", bad, path)
					}
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, s, _, v, id, request, _, _ := runtimeVerifierFixture(t, "sell_percent")
	if _, err := v.capture(context.Background(), id, request); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"canary_send_permits", "canary_runtime_gate_snapshots", "transaction_submissions", "receipt_observations", "position_effects", "manual_gate_clock_scopes"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("prohibited path", table)
		}
	}
	for _, name := range []string{"manual_b1_no_arm", "manual_b1_no_permit", "manual_b1_no_pass_snapshot", "manual_nonce_no_submission_insert", "manual_nonce_risk_no_release"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&n); err != nil || n != 1 {
			t.Fatal("guard changed", name, err)
		}
	}
}

func TestHTTPM4B22WriterBarrierStopAndLeaseCannotCommitInsideFence(t *testing.T) {
	for _, kind := range []string{"stop", "lease"} {
		t.Run(kind, func(t *testing.T) {
			_, s, path, v, id, request, _, _ := runtimeVerifierFixture(t, "buy")
			d2, err := storage.Open(context.Background(), storage.TradeOwner, path)
			if err != nil {
				t.Fatal(err)
			}
			defer d2.Close()
			s2, err := NewStore(d2)
			if err != nil {
				t.Fatal(err)
			}
			attempted, finished := make(chan struct{}), make(chan error, 1)
			writerHeld, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			v.checkpoint = func(stage string) error {
				if stage == "writer_acquired" {
					go func() {
						ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
						defer cancel()
						close(attempted)
						query := `UPDATE canary_control_state SET emergency_stopped=1`
						if kind == "lease" {
							query = `UPDATE canary_worker_leases SET holder_id='takeover',lease_epoch=2`
						}
						_, e := s2.db.ExecContext(ctx, query)
						finished <- e
					}()
					<-attempted
					close(writerHeld)
					<-release
				}
				return nil
			}
			result := make(chan error, 1)
			go func() { _, e := v.capture(context.Background(), id, request); result <- e }()
			<-writerHeld
			if e := <-finished; e == nil {
				t.Fatal("competing mutable write committed inside fence")
			}
			once.Do(func() { close(release) })
			if err := <-result; err != nil {
				t.Fatal("metadata writer lost its intact fence", err)
			}
			if count(t, s.db, "manual_submission_gate_evidence") != 1 {
				t.Fatal("observation not committed")
			}
			// A post-COMMIT control mutation is allowed, but the old candidate
			// never grants action permission and a new verification must reject.
			query := `UPDATE canary_control_state SET emergency_stopped=1`
			if kind == "lease" {
				query = `UPDATE canary_worker_leases SET holder_id='takeover',lease_epoch=2`
			}
			if _, err := s2.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			v.checkpoint = nil
			if _, err := v.capture(context.Background(), id, request); err == nil {
				t.Fatal("old candidate reused after drift")
			}
		})
	}
}

func TestHTTPM4B22UncertaintyLeaseAuthNanosecondBoundaries(t *testing.T) {
	for _, kind := range []string{"lease_whole", "lease_fractional", "authorization", "source_uncertainty"} {
		t.Run(kind, func(t *testing.T) {
			_, s, _, v, id, request, clock, _ := runtimeVerifierFixture(t, "buy")
			start := clock.wall
			expiry := start.Add(5 * time.Second)
			switch kind {
			case "lease_whole", "lease_fractional":
				if kind == "lease_fractional" {
					expiry = expiry.Add(200 * time.Nanosecond)
				}
				if _, err := s.db.Exec(`UPDATE canary_worker_leases SET expires_at=?`, expiry.Format(time.RFC3339Nano)); err != nil {
					t.Fatal(err)
				}
			case "authorization":
				seedSigningWitnessCorruption(t, s, "canary_runtime_authorizations", `UPDATE canary_runtime_authorizations SET expires_at=?`, expiry.Format(time.RFC3339Nano))
			case "source_uncertainty":
				clock.uncertainty = time.Nanosecond
				if _, err := v.capture(context.Background(), id, request); err == nil {
					t.Fatal("source timestamp newer than lower uncertainty bound accepted")
				}
				return
			}
			for _, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
				clock.move(expiry.Add(offset))
				candidate, err := v.capture(context.Background(), id, request)
				if offset < 0 {
					if err != nil {
						t.Fatal("before expiry denied", err)
					}
				} else if err == nil || candidate != (manualRuntimeCandidate{}) {
					t.Fatal("expiry equality/after accepted", err)
				}
			}
		})
	}
}

func TestHTTPM4B22FaultLatchAuditUnavailableFailClosedAndPrimitiveFault(t *testing.T) {
	for _, kind := range []string{"latch_audit_failure", "identity_panic", "malformed_clock", "invalid_error_bound", "cancelled_clock"} {
		t.Run(kind, func(t *testing.T) {
			_, s, _, v, id, request, clock, observer := runtimeVerifierFixture(t, "buy")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "latch_audit_failure":
				if _, err := s.db.Exec(`CREATE TRIGGER fail_latch BEFORE INSERT ON canary_runtime_audit WHEN NEW.event_type='MANUAL_SUBMISSION_CLOCK' BEGIN SELECT RAISE(ABORT,'latch audit unavailable'); END`); err != nil {
					t.Fatal(err)
				}
				clock.move(clock.wall.Add(-time.Nanosecond))
			case "identity_panic":
				v.identity = runtimePanickingIdentity{}
			case "malformed_clock":
				clock.change = func(c *runtimeTestClock) { c.wall = time.Time{} }
			case "invalid_error_bound":
				clock.uncertainty = 2 * time.Second
			case "cancelled_clock":
				clock.change = func(c *runtimeTestClock) { cancel() }
			}
			candidate, err := v.capture(ctx, id, request)
			if err == nil || candidate != (manualRuntimeCandidate{}) || observer.calls != 0 || count(t, s.db, "manual_submission_gate_evidence") != 0 {
				t.Fatal("primitive failure produced candidate", kind, err)
			}
			if kind == "latch_audit_failure" && !v.unhealthy.Load() {
				t.Fatal("failed latch persistence did not stop component")
			}
			if kind == "cancelled_clock" {
				var fault int
				if e := s.db.QueryRow(`SELECT fault_latched FROM manual_gate_clock_state`).Scan(&fault); e != nil || fault != 0 {
					t.Fatal("normal cancellation latched clock fault", fault, e)
				}
			}
		})
	}
}

type runtimePanickingIdentity struct{}

func (runtimePanickingIdentity) identify(context.Context) (manualRuntimeProcess, error) {
	panic("fixture identity failure")
}

func TestHTTPM4B22MeasuredAdapterRejectsCopiedConfigAndMissingRPC(t *testing.T) {
	_, _, _, v, _, _, _, _ := runtimeVerifierFixture(t, "buy")
	// A configured expected revision is not measurement. The current test binary
	// cannot match this synthetic manifest; missing/dirty VCS metadata also rejects.
	measured := manualMeasuredRuntimeIdentity{manifest: v.profile.process}
	if got, err := measured.identify(context.Background()); err == nil || got != (manualRuntimeProcess{}) {
		t.Fatal("copied config accepted as binary proof", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := measured.identify(ctx); err == nil {
		t.Fatal("cancelled measurement accepted")
	}
	bridge := manualRuntimeRPC{}
	if got, err := bridge.observe(context.Background(), manualRuntimeChainFacts{curve: "0x3000000000000000000000000000000000000003", runtimeHash: "0x" + strings.Repeat("1", 64), quote: chainquery.Block{Number: 1, Hash: "0x" + strings.Repeat("2", 64)}}); err == nil || got != (chainquery.SubmissionRuntimeObservation{}) {
		t.Fatal("missing RPC adapter fell back", got, err)
	}
}
