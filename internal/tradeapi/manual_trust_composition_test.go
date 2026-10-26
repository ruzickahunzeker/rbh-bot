package tradeapi

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

type a3Fixture struct {
	d        *storage.Database
	s        *Store
	h        *API
	r        Record
	scope    manualAdmissionScope
	path     string
	b        *manualTrustComposition
	w        *manualLocalSigningWriter
	signer   *localCountingSigner
	clock    *runtimeTestClock
	identity *runtimeTestIdentity
}

func a3TrustFixture(t *testing.T) a3Fixture {
	t.Helper()
	d, s, h, r, scope, path, old, signer, nonce := localSigningFixture(t, "buy")
	// Inherited setup is synthetic; restore original no-arm BEFORE target work.
	raw, err := os.ReadFile("../storage/migrations/trade/018_manual_risk_units.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, guard, ok := strings.Cut(string(raw), "CREATE TRIGGER manual_b1_no_arm ")
	if !ok {
		t.Fatal("guard")
	}
	body, _, ok := strings.Cut(guard, "END;")
	if !ok {
		t.Fatal("terminator")
	}
	definition := "CREATE TRIGGER manual_b1_no_arm " + body + "END;"
	if _, err := s.db.Exec(definition); err != nil {
		t.Fatal(err)
	}
	var sqlText string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='manual_b1_no_arm'`).Scan(&sqlText); err != nil || sqlText+";" != definition {
		t.Fatal("no-arm", err)
	}
	stamp := testNow.Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`INSERT INTO canary_worker_leases VALUES('SUBMISSION',?,'a3-owner',1,?,?)`, scope.Environment, testNow.Add(time.Minute).Format(time.RFC3339Nano), stamp); err != nil {
		t.Fatal(err)
	}
	clock := &runtimeTestClock{wall: testNow}
	if err := initializeManualRuntimeClock(context.Background(), s, scope.Environment, clock, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	p := manualRuntimeProcess{scope: scope, binaryHash: strings.Repeat("3", 64), networkProfile: "a3-controlled", holder: "a3-owner", provider: "trusted-fixture", leaseEpoch: 1}
	if err := s.db.QueryRow(`SELECT quote_block_number,quote_block_hash FROM manual_execution_bindings WHERE request_id=?`, r.RequestID).Scan(&p.networkCheckpoint.Number, &p.networkCheckpoint.Hash); err != nil {
		t.Fatal(err)
	}
	p.manifestHash = runtimeManifestDigest(p)
	profile := manualRuntimeProfile{process: p, maximumError: time.Millisecond, queryTimeout: time.Second, maxAge: time.Second}
	id := &runtimeTestIdentity{process: p}
	observer := &runtimeTestObserver{environment: scope.Environment, profile: p.networkProfile}
	b, err := newManualTrustComposition(s, h.identity, profile, clock, id, nonce, observer, signer, old.cipher, old.cipher)
	if err != nil {
		t.Fatal(err)
	}
	return a3Fixture{d, s, h, r, scope, path, b, b.signingWriter(), signer, clock, id}
}

func a3ClockRows(t *testing.T, s *Store, want int, latched bool) {
	t.Helper()
	for _, table := range []string{"manual_gate_clock_events", "manual_gate_clock_complete"} {
		if count(t, s.db, table) != want {
			t.Fatal(table, count(t, s.db, table), want)
		}
	}
	for _, q := range []string{`SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='MANUAL_SUBMISSION_CLOCK'`, `SELECT COUNT(*) FROM canary_alert_outbox WHERE id LIKE 'manual-clock-alert:%'`} {
		var n int
		if err := s.db.QueryRow(q).Scan(&n); err != nil || n != want {
			t.Fatal("clock witness", n, want, err)
		}
	}
	var fault int
	if err := s.db.QueryRow(`SELECT fault_latched FROM manual_gate_clock_state`).Scan(&fault); err != nil || (fault == 1) != latched {
		t.Fatal("fault", fault, err)
	}
	for _, table := range []string{"canary_send_permits", "canary_runtime_gate_snapshots", "transaction_submissions", "receipt_observations", "position_effects"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("A3 side effect", table)
		}
	}
}

func TestA3PrivateBundleConstructionAndCopies(t *testing.T) {
	f := a3TrustFixture(t)
	before := admittedDatabaseSnapshot(t, f.s.db)
	profile := f.b.profile
	b, err := newManualTrustComposition(f.s, f.h.identity, profile, f.clock, f.identity, f.b.nonce, f.b.runtime, f.signer, f.b.cipher, f.b.decryptor)
	if err != nil {
		t.Fatal(err)
	}
	profile.process.scope.DeploymentID = "mutable-caller-copy"
	if b.profile == profile || before != admittedDatabaseSnapshot(t, f.s.db) || f.signer.calls.Load() != 0 || f.identity.calls != 0 {
		t.Fatal("constructor action/mutable input")
	}
	for _, bad := range []string{"nil_source", "typed_nil_source", "typed_nil_identity", "wrong_wallet", "bad_profile", "typed_nil_signer"} {
		owner, p, source, id, signer := f.h.identity, f.b.profile, f.b.timeSource, f.b.identity, f.b.signer
		switch bad {
		case "nil_source":
			source = nil
		case "typed_nil_source":
			source = (*runtimeTestClock)(nil)
		case "typed_nil_identity":
			id = (*runtimeTestIdentity)(nil)
		case "wrong_wallet":
			owner.WalletAddress = testToken
		case "bad_profile":
			p.maximumError = -1
		case "typed_nil_signer":
			signer = (*localCountingSigner)(nil)
		}
		if got, err := newManualTrustComposition(f.s, owner, p, source, id, f.b.nonce, f.b.runtime, signer, f.b.cipher, f.b.decryptor); err == nil || got != nil {
			t.Fatal("invalid bundle", bad)
		}
	}
	copy := *b
	copy.health.unhealthy.Store(true)
	if !b.health.unhealthy.Load() {
		t.Fatal("copy reset health")
	}
	if before != admittedDatabaseSnapshot(t, f.s.db) || f.signer.calls.Load() != 0 {
		t.Fatal("constructor wrote")
	}
}

func TestA3BoundedSigningTwoCommitsAndHistoryReopen(t *testing.T) {
	f := a3TrustFixture(t)
	got, err := f.w.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
	if err != nil || got.Duplicate || got.Artifact.TxHash == "" || f.signer.calls.Load() != 1 {
		t.Fatal("sign", got, err, f.signer.calls.Load())
	}
	a3ClockRows(t, f.s, 3, false)
	assertArtifactPhase(t, f.s, f.r)
	if _, err := f.s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, f.s.db)
	_, s := bindingReopen(t, f.d, f.path)
	reader := &manualLocalSigningWriter{store: s, cipher: f.b.cipher}
	history, err := reader.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
	if err != nil || !history.Duplicate || history.Artifact != got.Artifact || before != admittedDatabaseSnapshot(t, s.db) || f.signer.calls.Load() != 1 {
		t.Fatal("reopen renewed action", err)
	}
	a3ClockRows(t, s, 3, false)
}

func TestA3BundleVerifierIsDecryptOnlyAndFresh(t *testing.T) {
	f := a3TrustFixture(t)
	if _, err := f.w.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope); err != nil {
		t.Fatal(err)
	}
	foundation := submissionFoundationFor(t, f.s, *f.r.OperationID)
	if err := insertSubmissionFoundation(f.s.db, foundation, "INSERT"); err != nil {
		t.Fatal(err)
	}
	before := foundationEconomicSnapshot(t, f.s)
	v := f.b.verifier()
	for i := 0; i < 2; i++ {
		candidate, err := v.capture(context.Background(), f.h.identity, f.r.RequestID)
		if err != nil || candidate.sendAuthorized || candidate.signingAuthorized || !candidate.freshGateRequired {
			t.Fatal("verifier", err)
		}
	}
	if f.signer.calls.Load() != 1 || before != foundationEconomicSnapshot(t, f.s) {
		t.Fatal("verifier added economic action")
	}
	a3ClockRows(t, f.s, 5, false)
}

func TestA3TrustGateDenialsAndNanosecondBounds(t *testing.T) {
	for _, kind := range []string{"stop", "revoke", "owner", "epoch", "missing_clock", "identity", "provider_panic", "provider_mismatch", "uncertainty", "floor_reverse", "lower_causality", "upper_expiry", "expiry_equal", "expiry_minus1", "expiry_plus1"} {
		t.Run(kind, func(t *testing.T) {
			f := a3TrustFixture(t)
			wantFault := false
			switch kind {
			case "stop":
				if _, err := f.s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`); err != nil {
					t.Fatal(err)
				}
				f.clock.change = func(*runtimeTestClock) { panic("must not sample") }
			case "revoke":
				if _, err := f.s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
					t.Fatal(err)
				}
			case "owner":
				f.b.profile.process.holder = "other"
				f.b.profile.process.manifestHash = runtimeManifestDigest(f.b.profile.process)
				f.identity.process = f.b.profile.process
			case "epoch":
				f.b.profile.process.leaseEpoch = 2
				f.b.profile.process.manifestHash = runtimeManifestDigest(f.b.profile.process)
				f.identity.process = f.b.profile.process
			case "missing_clock":
				f.b.profile.process.scope.Environment = "missing"
				f.b.profile.process.manifestHash = runtimeManifestDigest(f.b.profile.process)
				f.scope = f.b.profile.process.scope
				f.identity.process = f.b.profile.process
			case "identity":
				f.identity.fail = true
			case "provider_panic":
				f.clock.change = func(*runtimeTestClock) { panic("provider") }
				wantFault = true
			case "provider_mismatch":
				f.b.profile.process.provider = "different"
				f.b.profile.process.manifestHash = runtimeManifestDigest(f.b.profile.process)
				f.identity.process = f.b.profile.process
				wantFault = true
			case "uncertainty":
				f.clock.uncertainty = 2 * time.Millisecond
				wantFault = true
			case "floor_reverse":
				f.clock.move(testNow.Add(-time.Nanosecond))
				wantFault = true
			case "lower_causality":
				f.clock.uncertainty = time.Nanosecond
			case "upper_expiry", "expiry_equal", "expiry_minus1", "expiry_plus1":
				expiry, _ := canonicalManualTime(f.r.ExpiresAt)
				at := expiry
				if kind == "upper_expiry" {
					at = expiry.Add(-time.Nanosecond)
					f.clock.uncertainty = time.Nanosecond
				}
				if kind == "expiry_minus1" {
					at = expiry.Add(-time.Nanosecond)
				}
				if kind == "expiry_plus1" {
					at = expiry.Add(time.Nanosecond)
				}
				f.clock.move(at)
				f.b.nonce.(*localNonceObserver).change = func(o *manualnonce.Observation) { o.ObservedAt = at.Format(time.RFC3339Nano) }
			}
			got, err := f.w.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
			if kind == "expiry_minus1" {
				if err != nil || got.Artifact.TxHash == "" || f.signer.calls.Load() != 1 {
					t.Fatal("minus 1ns rejected", err)
				}
				a3ClockRows(t, f.s, 3, false)
				return
			}
			if err == nil || got != (manualLocalSigningResult{}) || f.signer.calls.Load() != 0 {
				t.Fatal("denial signed", kind, err)
			}
			if kind == "stop" && inputFailureCode(err) != "EMERGENCY_STOPPED" {
				t.Fatal("stop priority", err)
			}
			want := 1
			if wantFault {
				want = 2
				if !f.b.health.unhealthy.Load() {
					t.Fatal("fault not shared")
				}
			}
			a3ClockRows(t, f.s, want, wantFault)
		})
	}
}

func TestA3ClockFaultRollbackReleaseLatchNoResign(t *testing.T) {
	for _, stage := range []string{"before_marker", "after_marker_commit", "after_fence", "before_sign", "after_sign", "before_artifact_commit"} {
		t.Run(stage, func(t *testing.T) {
			f := a3TrustFixture(t)
			released := false
			f.w.checkpoint = func(at string) error {
				if at == stage {
					f.clock.move(testNow.Add(-time.Nanosecond))
				}
				if at == "trust_connection_released" {
					released = true
					// This acquisition proves the original transaction rolled back
					// AND checked-out pool connection was released before latch.
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					if err := f.s.db.PingContext(ctx); err != nil {
						return err
					}
					var fault int
					if err := f.s.db.QueryRowContext(ctx, `SELECT fault_latched FROM manual_gate_clock_state`).Scan(&fault); err != nil || fault != 0 {
						return fmt.Errorf("latch ran before release: %d %v", fault, err)
					}
				}
				return nil
			}
			_, err := f.w.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
			if err == nil || !released || !f.b.health.unhealthy.Load() {
				t.Fatal("fault cleanup", err, released)
			}
			wantSign := int64(0)
			if stage == "after_sign" || stage == "before_artifact_commit" {
				wantSign = 1
			}
			if f.signer.calls.Load() != wantSign || count(t, f.s.db, "manual_signed_artifact_commands") != 0 {
				t.Fatal("fault economic result", f.signer.calls.Load())
			}
			wantIntents := 1
			wantClock := 3
			if stage == "before_marker" {
				wantIntents = 0
				wantClock = 2
			}
			if count(t, f.s.db, "manual_signing_intents") != wantIntents {
				t.Fatal("lost intent")
			}
			a3ClockRows(t, f.s, wantClock, true)
			_, s := bindingReopen(t, f.d, f.path)
			fresh := &manualLocalSigningWriter{store: s, cipher: f.b.cipher}
			_, _ = fresh.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
			if f.signer.calls.Load() != wantSign {
				t.Fatal("restart resigned")
			}
		})
	}
}

func TestA3HealthyClockWitnessAndCommitFaultsAtomic(t *testing.T) {
	for _, phase := range []string{"marker", "artifact"} {
		for _, fault := range []string{"audit", "alert", "commit"} {
			t.Run(phase+"/"+fault, func(t *testing.T) {
				f := a3TrustFixture(t)
				gen := 2
				if phase == "artifact" {
					gen = 3
				}
				switch fault {
				case "audit":
					if _, err := f.s.db.Exec(fmt.Sprintf(`CREATE TRIGGER a3_fault BEFORE INSERT ON canary_runtime_audit WHEN NEW.event_type='MANUAL_SUBMISSION_CLOCK' AND json_extract(NEW.details_json,'$.generation')=%d BEGIN SELECT RAISE(ABORT,'a3 audit failure'); END`, gen)); err != nil {
						t.Fatal(err)
					}
				case "alert":
					if _, err := f.s.db.Exec(fmt.Sprintf(`CREATE TRIGGER a3_fault BEFORE INSERT ON canary_alert_outbox WHEN NEW.id LIKE 'manual-clock-alert:%%' AND EXISTS(SELECT 1 FROM canary_runtime_audit a WHERE a.id=NEW.audit_id AND json_extract(a.details_json,'$.generation')=%d) BEGIN SELECT RAISE(ABORT,'a3 alert failure'); END`, gen)); err != nil {
						t.Fatal(err)
					}
				case "commit":
					if _, err := f.s.db.Exec(fmt.Sprintf(`CREATE TABLE a3_parent(id INTEGER PRIMARY KEY);CREATE TABLE a3_child(id INTEGER REFERENCES a3_parent(id) DEFERRABLE INITIALLY DEFERRED);CREATE TRIGGER a3_fault AFTER INSERT ON manual_gate_clock_events WHEN NEW.generation=%d BEGIN INSERT INTO a3_child VALUES(99); END;`, gen)); err != nil {
						t.Fatal(err)
					}
				}
				_, err := f.w.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
				if err == nil {
					t.Fatal("fault accepted")
				}
				if fault == "commit" && !strings.Contains(err.Error(), "FOREIGN KEY") {
					t.Fatal("not real commit fault", err)
				}
				intents, signs, clocks := 0, int64(0), 1
				if phase == "artifact" {
					intents = 1
					signs = 1
					clocks = 2
				}
				if count(t, f.s.db, "manual_signing_intents") != intents || count(t, f.s.db, "manual_signed_artifact_commands") != 0 || f.signer.calls.Load() != signs {
					t.Fatal("non-atomic outcome", err)
				}
				a3ClockRows(t, f.s, clocks, false)
				if phase == "artifact" {
					_, err = f.w.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
					if err != nil || f.signer.calls.Load() != 1 {
						t.Fatal("fault resigned", err)
					}
				}
			})
		}
	}
}

func TestA3GenerationGapAndPolicyDenialNotClockFault(t *testing.T) {
	for _, kind := range []string{"advance", "stop", "revoke", "takeover", "cancel", "begin_failure", "before_marker_cancel"} {
		t.Run(kind, func(t *testing.T) {
			f := a3TrustFixture(t)
			d2, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
			if err != nil {
				t.Fatal(err)
			}
			defer d2.Close()
			other, err := NewStore(d2)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.w.checkpoint = func(at string) error {
				if kind == "before_marker_cancel" && at == "before_marker" {
					cancel()
					return ctx.Err()
				}
				if at != "after_marker_commit" {
					return nil
				}
				switch kind {
				case "advance":
					stamp := testNow.Add(time.Nanosecond).Format(time.RFC3339Nano)
					conn, e := other.db.Conn(ctx)
					if e != nil {
						return e
					}
					defer conn.Close()
					if e = insertManualClockEvent(ctx, conn, f.scope.Environment, 3, stamp, 0, "", nil, stamp); e != nil {
						return e
					}
					f.clock.move(testNow.Add(time.Nanosecond))
				case "stop":
					_, err = other.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`)
				case "revoke":
					_, err = other.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`)
				case "takeover":
					_, err = other.db.Exec(`UPDATE canary_worker_leases SET holder_id='another',lease_epoch=2`)
				case "cancel":
					cancel()
				case "begin_failure":
					return errors.New("controlled pre-final-BEGIN failure")
				}
				return err
			}
			got, err := f.w.sign(ctx, f.h.identity, f.r.RequestID, f.scope)
			if kind == "advance" {
				if err != nil || got.Artifact.TxHash == "" || f.signer.calls.Load() != 1 {
					t.Fatal("legitimate floor advance", err)
				}
				a3ClockRows(t, f.s, 4, false)
				return
			}
			if err == nil || f.signer.calls.Load() != 0 {
				t.Fatal("denial invoked", err)
			}
			clocks := 2
			if kind == "before_marker_cancel" {
				clocks = 1
			}
			a3ClockRows(t, f.s, clocks, false)
		})
	}
}

func TestA3FailedLatchAndConcurrentAlreadyLatched(t *testing.T) {
	for _, kind := range []string{"audit_failure", "cleanup_failure", "two_handles"} {
		t.Run(kind, func(t *testing.T) {
			f := a3TrustFixture(t)
			if kind == "two_handles" {
				d2, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
				if err != nil {
					t.Fatal(err)
				}
				defer d2.Close()
				s2, err := NewStore(d2)
				if err != nil {
					t.Fatal(err)
				}
				var wg sync.WaitGroup
				errorsOut := make(chan error, 2)
				for _, s := range []*Store{f.s, s2} {
					b := *f.b
					b.store = s
					session, err := b.session(context.Background(), f.h.identity, f.scope)
					if err != nil {
						t.Fatal(err)
					}
					session.clock.invalid = true
					session.clock.faultReason = "CLOCK_REVERSAL"
					session.clock.faultWall = testNow.Add(-time.Nanosecond).Format(time.RFC3339Nano)
					wg.Add(1)
					go func() {
						defer wg.Done()
						result := manualLocalSigningResult{}
						original := errors.New("original clock fault")
						err := error(original)
						session.finish(&result, &err)
						if err != original {
							errorsOut <- err
						}
					}()
				}
				wg.Wait()
				close(errorsOut)
				for e := range errorsOut {
					t.Fatal(e)
				}
				a3ClockRows(t, f.s, 2, true)
				return
			}
			if kind == "audit_failure" {
				if _, err := f.s.db.Exec(`CREATE TRIGGER a3_fault BEFORE INSERT ON canary_runtime_audit WHEN NEW.event_type='MANUAL_SUBMISSION_CLOCK' AND json_extract(NEW.details_json,'$.fault_latched')=1 BEGIN SELECT RAISE(ABORT,'a3 latch audit failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			f.w.checkpoint = func(at string) error {
				if at == "before_sign" {
					f.clock.move(testNow.Add(-time.Nanosecond))
				}
				if at == "trust_connection_released" && kind == "cleanup_failure" {
					return errors.New("injected cleanup release failure")
				}
				return nil
			}
			_, err := f.w.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
			if err == nil || !f.b.health.unhealthy.Load() || f.signer.calls.Load() != 0 {
				t.Fatal("latch failure opened action", err)
			}
			a3ClockRows(t, f.s, 2, false)
			if kind == "audit_failure" && !strings.Contains(err.Error(), "LATCH_NOT_DURABLE") {
				t.Fatal("hidden persistence failure", err)
			}
		})
	}
}

func TestA3PositiveUncertaintyAndStickyBundleFailure(t *testing.T) {
	f := a3TrustFixture(t)
	f.clock.uncertainty = time.Nanosecond
	f.clock.change = func(c *runtimeTestClock) { c.wall = c.wall.Add(10 * time.Nanosecond) }
	f.b.nonce.(*localNonceObserver).change = func(o *manualnonce.Observation) {
		f.clock.mu.Lock()
		defer f.clock.mu.Unlock()
		o.ObservedAt = f.clock.wall.Add(time.Nanosecond).Format(time.RFC3339Nano)
	}
	got, err := f.w.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
	if err != nil || got.Artifact.TxHash == "" || f.signer.calls.Load() != 1 {
		t.Fatal("positive uncertainty", err)
	}
	a3ClockRows(t, f.s, 3, false)
	copy := *f.b
	copy.health.unhealthy.Store(true)
	session, err := f.b.session(context.Background(), f.h.identity, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	calls := f.clock.calls
	if _, err := session.read(); err == nil || f.clock.calls != calls {
		t.Fatal("shared unhealthy flag sampled/action allowed", err)
	}
}

func TestA3PrivateGraphAndRecoveryIsolation(t *testing.T) {
	allowed := map[string]bool{"manual_trust_composition.go": true, "manual_presign_trust.go": true, "manual_local_signing.go": true}
	root := filepath.Join("..", "..")
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			owned := filepath.Dir(path) == filepath.Join(root, "internal", "tradeapi") && allowed[filepath.Base(path)]
			ast.Inspect(file, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					if (id.Name == "manualTrustComposition" || id.Name == "manualPreSignTrust" || id.Name == "manualTrustHealth" || id.Name == "newManualTrustComposition") && !owned {
						t.Errorf("bundle escaped: %s %s", path, id.Name)
					}
					if owned {
						for _, bad := range []string{"manualSubmissionExecutor", "manualFirstTransport", "SubmissionService", "RawBroadcaster", "SendRawTransaction", "IssueCanarySendPermit", "ConsumeCanaryPermitAfterGate", "NewLocalSigner", "RPCBackend"} {
							if id.Name == bad {
								t.Errorf("forbidden dependency %s %s", path, id.Name)
							}
						}
					}
				}
				if owned {
					if _, ok := n.(*ast.GoStmt); ok {
						t.Error("detached work")
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Runtime verifier fields structurally exclude the signing bundle/signer.
	file, err := parser.ParseFile(token.NewFileSet(), "manual_runtime_verifier.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && (id.Name == "manualTrustComposition" || id.Name == "Signer") {
			t.Error("verifier acquired signing dependency")
		}
		return true
	})
}

func TestA3FinalWriterBarrierAndTwoHandleSigning(t *testing.T) {
	for _, kind := range []string{"stop", "revoke", "takeover", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			f := a3TrustFixture(t)
			d2, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
			if err != nil {
				t.Fatal(err)
			}
			defer d2.Close()
			s2, err := NewStore(d2)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var once, releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			f.w.checkpoint = func(at string) error {
				if at == "before_sign" {
					once.Do(func() { close(entered) })
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			}
			type outcome struct {
				v   manualLocalSigningResult
				err error
			}
			done := make(chan outcome, 1)
			go func() { v, err := f.w.sign(ctx, f.h.identity, f.r.RequestID, f.scope); done <- outcome{v, err} }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("barrier not entered")
			}
			writerStarted := make(chan struct{})
			otherDone := make(chan outcome, 1)
			q := `UPDATE canary_control_state SET emergency_stopped=1`
			if kind == "revoke" {
				q = `UPDATE canary_runtime_authorizations SET state='REVOKED'`
			}
			if kind == "takeover" {
				q = `UPDATE canary_worker_leases SET holder_id='new-owner',lease_epoch=2`
			}
			if kind != "duplicate" {
				if _, err := s2.db.Exec(`PRAGMA busy_timeout=30`); err != nil {
					t.Fatal(err)
				}
			}
			go func() {
				close(writerStarted)
				if kind == "duplicate" {
					b := *f.b
					b.store = s2
					b.identity = &runtimeTestIdentity{process: b.profile.process}
					b.runtime = &runtimeTestObserver{environment: f.scope.Environment, profile: b.profile.process.networkProfile}
					v, err := b.signingWriter().sign(ctx, f.h.identity, f.r.RequestID, f.scope)
					otherDone <- outcome{v, err}
					return
				}
				_, err := s2.db.ExecContext(ctx, q)
				otherDone <- outcome{err: err}
			}()
			<-writerStarted
			if kind != "duplicate" {
				select {
				case result := <-otherDone:
					// A returned SQLITE_BUSY proves the competing write actually
					// attempted while the original final transaction was held.
					if result.err == nil || !strings.Contains(result.err.Error(), "SQLITE_BUSY") {
						t.Fatal("expected fenced control write", result.err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			releaseOnce.Do(func() { close(release) })
			select {
			case result := <-done:
				if result.err != nil || result.v.Artifact.TxHash == "" {
					t.Fatal("original signing", result.err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if kind == "duplicate" {
				select {
				case result := <-otherDone:
					if result.err != nil || (kind == "duplicate" && !result.v.Duplicate) {
						t.Fatal("other writer", result.err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			} else if _, err := s2.db.ExecContext(ctx, q); err != nil {
				t.Fatal("control write after original COMMIT", err)
			}
			if f.signer.calls.Load() != 1 {
				t.Fatal("second signature", f.signer.calls.Load())
			}
			a3ClockRows(t, f.s, 3, false)
		})
	}
}

func TestA3RuntimeObservationAndIdentityDriftBeforeSign(t *testing.T) {
	for _, kind := range []string{"chain", "environment", "profile", "hash", "digest", "old_anchor", "unavailable", "identity_before_sign", "identity_after_sign", "tick_reverse", "session_change"} {
		t.Run(kind, func(t *testing.T) {
			f := a3TrustFixture(t)
			switch kind {
			case "identity_before_sign", "identity_after_sign":
				stage := "before_sign"
				if kind == "identity_after_sign" {
					stage = "after_sign"
				}
				f.w.checkpoint = func(at string) error {
					if at == stage {
						f.identity.fail = true
					}
					return nil
				}
			case "tick_reverse":
				f.clock.change = func(c *runtimeTestClock) {
					if c.calls > 4 {
						c.ticks = -2 * time.Nanosecond
					}
				}
			case "session_change":
				f.b.timeSource = &a3SessionChangingClock{base: f.clock}
				f.w = f.b.signingWriter()
			default:
				f.b.runtime.(*runtimeTestObserver).bad = kind
			}
			_, err := f.w.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
			if err == nil {
				t.Fatal("bad trusted output accepted")
			}
			want := int64(0)
			if kind == "identity_after_sign" {
				want = 1
			}
			if f.signer.calls.Load() != want || count(t, f.s.db, "manual_signed_artifact_commands") != 0 {
				t.Fatal("wrong boundary", f.signer.calls.Load(), err)
			}
		})
	}
}

type a3SessionChangingClock struct {
	base  *runtimeTestClock
	calls int
}

func (c *a3SessionChangingClock) sample(ctx context.Context) (manualRuntimeSample, error) {
	s, err := c.base.sample(ctx)
	c.calls++
	if c.calls > 1 {
		s.session = "changed"
	}
	return s, err
}

type a3CrashSigner struct{ address common.Address }

func (s a3CrashSigner) Address() common.Address { return s.address }
func (a3CrashSigner) SignTransaction(context.Context, *types.Transaction, *big.Int) (*types.Transaction, error) {
	panic("crash probe must never invoke signer")
}

// Genuine abrupt process termination; no graceful cleanup defers run.
func TestA3ActualProcessClockCrashWindows(t *testing.T) {
	if path := os.Getenv("A3_TEST_CRASH_DB"); path != "" {
		ctx := context.Background()
		d, err := storage.Open(ctx, storage.TradeOwner, path)
		if err != nil {
			t.Fatal(err)
		}
		s, err := NewStore(d)
		if err != nil {
			t.Fatal(err)
		}
		var id Identity
		var request string
		if err := s.db.QueryRow(`SELECT principal_id,wallet_id,wallet_address,id FROM manual_http_order_sources LIMIT 1`).Scan(&id.PrincipalID, &id.WalletID, &id.WalletAddress, &request); err != nil {
			t.Fatal(err)
		}
		cmd, err := scanManualCommand(s.db.QueryRow(`SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE request_id=?`, request))
		if err != nil {
			t.Fatal(err)
		}
		// Synthetic expected identity is reconstructed ONLY inside this child
		// fixture. This is not an operator manifest loader or positive attestation.
		p := manualRuntimeProcess{scope: cmd.Scope, binaryHash: strings.Repeat("3", 64), networkProfile: "a3-controlled", holder: "a3-owner", provider: "trusted-fixture", leaseEpoch: 1}
		if err := s.db.QueryRow(`SELECT quote_block_number,quote_block_hash FROM manual_execution_bindings WHERE request_id=?`, request).Scan(&p.networkCheckpoint.Number, &p.networkCheckpoint.Hash); err != nil {
			t.Fatal(err)
		}
		p.manifestHash = runtimeManifestDigest(p)
		cipher, err := trade.NewAESGCMCipher("local-signing-fixture", []byte{7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7})
		if err != nil {
			t.Fatal(err)
		}
		clock := &runtimeTestClock{wall: testNow}
		b, err := newManualTrustComposition(s, id, manualRuntimeProfile{process: p, maximumError: time.Millisecond, queryTimeout: time.Second, maxAge: time.Second}, clock, &runtimeTestIdentity{process: p}, &localNonceObserver{nonce: "23"}, &runtimeTestObserver{environment: cmd.Scope.Environment, profile: p.networkProfile}, a3CrashSigner{common.HexToAddress(id.WalletAddress)}, cipher, cipher)
		if err != nil {
			t.Fatal(err)
		}
		w := b.signingWriter()
		mode := os.Getenv("A3_TEST_CRASH_WINDOW")
		w.checkpoint = func(at string) error {
			if mode == "marker_committed" && at == "after_marker_commit" {
				os.Exit(77)
			}
			if at == "before_sign" {
				clock.move(testNow.Add(-time.Nanosecond))
			}
			if mode == "before_latch" && at == "trust_connection_released" {
				os.Exit(77)
			}
			if mode == "after_latch" && at == "trust_cleanup_finished" {
				os.Exit(77)
			}
			return nil
		}
		_, err = w.sign(ctx, id, request, cmd.Scope)
		t.Fatal("crash checkpoint not reached", err)
	}
	for _, mode := range []string{"marker_committed", "before_latch", "after_latch"} {
		t.Run(mode, func(t *testing.T) {
			f := a3TrustFixture(t)
			if err := f.d.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestA3ActualProcessClockCrashWindows$")
			child.Env = append(os.Environ(), "A3_TEST_CRASH_DB="+f.path, "A3_TEST_CRASH_WINDOW="+mode)
			out, err := child.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 77 {
				t.Fatalf("expected exit77: %v %s", err, out)
			}
			d, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			s, err := NewStore(d)
			if err != nil {
				t.Fatal(err)
			}
			clocks := 2
			fault := false
			if mode == "after_latch" {
				clocks = 3
				fault = true
			}
			a3ClockRows(t, s, clocks, fault)
			if count(t, s.db, "manual_signing_intents") != 1 || count(t, s.db, "manual_signed_artifact_commands") != 0 {
				t.Fatal("lost ambiguity")
			}
			before := admittedDatabaseSnapshot(t, s.db)
			reader := &manualLocalSigningWriter{store: s, cipher: manualArtifactReadCipher{f.b.decryptor}}
			got, err := reader.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
			if err != nil || !got.Duplicate || before != admittedDatabaseSnapshot(t, s.db) || f.signer.calls.Load() != 0 {
				t.Fatal("crash resumed", err)
			}
			if fault {
				b := *f.b
				b.store = s
				b.health = &manualTrustHealth{}
				session, err := b.session(context.Background(), f.h.identity, f.scope)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := session.read(); err == nil {
					t.Fatal("new bundle ignored durable latch")
				}
			}
		})
	}
}

var _ manualartifact.Cipher = manualArtifactReadCipher{}

type a3FailCipher struct{ manualartifact.Cipher }

func (a3FailCipher) Encrypt([]byte, []byte) ([]byte, []byte, error) {
	return nil, nil, errors.New("controlled encryption failure")
}

func TestA3SignerCipherFailureRetainsOriginalMarker(t *testing.T) {
	for _, kind := range []string{"error", "panic", "wrong_nonce", "timeout", "cipher"} {
		t.Run(kind, func(t *testing.T) {
			f := a3TrustFixture(t)
			if kind == "cipher" {
				b, err := newManualTrustComposition(f.s, f.h.identity, f.b.profile, f.clock, f.identity, f.b.nonce, f.b.runtime, f.signer, a3FailCipher{f.b.cipher}, f.b.decryptor)
				if err != nil {
					t.Fatal(err)
				}
				f.w = b.signingWriter()
			} else {
				f.signer.invoke = func(ctx context.Context, tx *types.Transaction, chain *big.Int) (*types.Transaction, error) {
					switch kind {
					case "error":
						return nil, errors.New("controlled signer failure")
					case "panic":
						panic("controlled signer panic")
					case "timeout":
						<-ctx.Done()
						return nil, ctx.Err()
					}
					wrong := types.NewTx(&types.DynamicFeeTx{ChainID: tx.ChainId(), Nonce: tx.Nonce() + 1, To: tx.To(), Value: tx.Value(), Data: tx.Data(), Gas: tx.Gas(), GasFeeCap: tx.GasFeeCap(), GasTipCap: tx.GasTipCap()})
					return f.signer.signer.SignTransaction(ctx, wrong, chain)
				}
			}
			got, err := f.w.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
			if err == nil || got != (manualLocalSigningResult{}) || f.signer.calls.Load() != 1 || count(t, f.s.db, "manual_signed_artifact_commands") != 0 {
				t.Fatal("failed primitive escaped", err)
			}
			a3ClockRows(t, f.s, 2, false)
			if got, err := f.w.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope); err != nil || !got.Duplicate || f.signer.calls.Load() != 1 {
				t.Fatal("failure resigned", err)
			}
		})
	}
}

func TestA3DatabaseUnavailableDuringFaultPersistence(t *testing.T) {
	f := a3TrustFixture(t)
	f.w.checkpoint = func(at string) error {
		if at == "before_sign" {
			f.clock.move(testNow.Add(-time.Nanosecond))
		}
		if at == "trust_connection_released" {
			return f.d.Close()
		}
		return nil
	}
	_, err := f.w.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
	if err == nil || !strings.Contains(err.Error(), "database is closed") || !f.b.health.unhealthy.Load() || f.signer.calls.Load() != 0 {
		t.Fatal("unavailable database reported durable fault", err)
	}
	d, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	a3ClockRows(t, s, 2, false)
	if count(t, s.db, "manual_signing_intents") != 1 {
		t.Fatal("lost original marker")
	}
}

type a3AdvancingDecryptor struct {
	manualArtifactDecryptor
	after func()
}

func (d a3AdvancingDecryptor) Decrypt(v string, c, n, a []byte) ([]byte, error) {
	raw, err := d.manualArtifactDecryptor.Decrypt(v, c, n, a)
	d.after()
	return raw, err
}

func TestA3FinalClockSampleAfterArtifactAuthentication(t *testing.T) {
	f := a3TrustFixture(t)
	expiry, _ := canonicalManualTime(f.r.ExpiresAt)
	f.b.decryptor = a3AdvancingDecryptor{f.b.decryptor, func() { f.clock.move(expiry) }}
	_, err := f.w.sign(context.Background(), f.h.identity, f.r.RequestID, f.scope)
	if err == nil || f.signer.calls.Load() != 1 || count(t, f.s.db, "manual_signed_artifact_commands") != 0 {
		t.Fatal("accepted stale pre-decrypt clock sample", err)
	}
	a3ClockRows(t, f.s, 2, false)
}
