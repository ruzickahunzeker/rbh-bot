package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

func manualSubmissionRequestFor(t *testing.T, f manualB24ConsumeFixture) trade.ManualFirstSubmissionRequest {
	t.Helper()
	return trade.ManualFirstSubmissionRequest{Gate: f.r, PermitID: f.permit.ID, ArtifactHash: f.raw, TxHash: f.permit.TxHash, Lease: trade.SubmissionLeaseFence{Environment: f.r.Deployment.Environment, HolderID: "runtime-owner", Epoch: 1}}
}

// Store-only tests rely on the real schema witness. End-to-end executor tests
// separately exercise the trusted runtime/artifact verifier on each fence.
func manualStoreTestRecheck(context.Context, *sql.Tx) error { return nil }

func TestManualSubmissionClaimAtomicAndStandaloneConsumeNotAdopted(t *testing.T) {
	for _, fault := range []string{"claim_audit", "claim_alert", "claim_commit", "already_consumed"} {
		t.Run(fault, func(t *testing.T) {
			f := manualB24Fixture(t)
			r := manualSubmissionRequestFor(t, f)
			now := f.permit.IssuedAt.Add(time.Nanosecond)
			before := manualB24EconomicSnapshot(t, f.s)
			switch fault {
			case "claim_audit":
				if _, err := f.s.db.Exec(`CREATE TRIGGER fail_a_claim BEFORE INSERT ON canary_runtime_audit WHEN NEW.event_type='MANUAL_FIRST_SUBMISSION_CLAIMED' BEGIN SELECT RAISE(ABORT,'test audit failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "claim_alert":
				if _, err := f.s.db.Exec(`CREATE TRIGGER fail_a_claim BEFORE INSERT ON canary_alert_outbox WHEN NEW.id LIKE 'manual-first-submission-claim-alert:%' BEGIN SELECT RAISE(ABORT,'test alert failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "claim_commit":
				f.trade.SetCanaryHookForTest(func(stage string) error {
					if stage == "manual_submission_claim_before_commit" {
						return errors.New("commit fault")
					}
					return nil
				})
			case "already_consumed":
				if _, err := f.trade.ConsumeManualFirstBroadcastPermit(context.Background(), f.r, f.permit.ID, f.raw, now); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.trade.BeginManualFirstSubmission(context.Background(), r, now, manualStoreTestRecheck); err == nil {
				t.Fatal("invalid/partial claim accepted")
			}
			if manualB24EconomicSnapshot(t, f.s) != before {
				t.Fatal("failed claim changed economic state")
			}
			var bindings, scopes int
			if err := f.s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM manual_first_submission_bindings),(SELECT COUNT(*) FROM manual_first_submission_scopes)`).Scan(&bindings, &scopes); err != nil || bindings != 0 || scopes != 0 {
				t.Fatal("partial claim retained", bindings, scopes, err)
			}
			var permit string
			if err := f.s.db.QueryRow(`SELECT state FROM canary_send_permits WHERE id=?`, f.permit.ID).Scan(&permit); err != nil {
				t.Fatal(err)
			}
			want := "ISSUED"
			if fault == "already_consumed" {
				want = "CONSUMED"
			}
			if permit != want {
				t.Fatal("permit mutation not atomic", permit)
			}
		})
	}
}

func TestManualSubmissionConcurrentClaimAcrossSQLiteHandles(t *testing.T) {
	f := manualB24Fixture(t)
	secondDB, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer secondDB.Close()
	second, err := trade.NewStore(secondDB)
	if err != nil {
		t.Fatal(err)
	}
	r := manualSubmissionRequestFor(t, f)
	start := make(chan struct{})
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(s *trade.Store) {
			defer wg.Done()
			<-start
			_, err := s.BeginManualFirstSubmission(context.Background(), r, f.permit.IssuedAt.Add(time.Nanosecond), manualStoreTestRecheck)
			results <- err
		}([]*trade.Store{f.trade, second}[i%2])
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatal("claim winners", wins)
	}
	var bindings, submissions, audits int
	if err = f.s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM manual_first_submission_bindings),(SELECT COUNT(*) FROM transaction_submissions),(SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='MANUAL_FIRST_SUBMISSION_CLAIMED')`).Scan(&bindings, &submissions, &audits); err != nil || bindings != 1 || submissions != 1 || audits != 1 {
		t.Fatal("claim conservation", bindings, submissions, audits, err)
	}
}

func TestManualSubmissionRestartClassifiesWithoutResend(t *testing.T) {
	for _, stage := range []string{"after_claim", "after_intent", "after_send_before_commit"} {
		t.Run(stage, func(t *testing.T) {
			f := newManualSubmissionFixture(t, "buy")
			f.e.checkpoint = func(at string) error {
				if at == stage {
					return errors.New("simulated process stop")
				}
				return nil
			}
			if stage == "after_send_before_commit" {
				f.e.store.SetCanaryHookForTest(func(at string) error {
					if at == "manual_submission_outcome_before_commit" {
						return errors.New("outcome commit unavailable")
					}
					return nil
				})
			}
			if _, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID); err == nil {
				t.Fatal("fault did not interrupt")
			}
			wantCalls := 0
			if stage == "after_send_before_commit" {
				wantCalls = 1
			}
			if f.fake.calls != wantCalls {
				t.Fatal("invocations", f.fake.calls)
			}
			if err := f.d.Close(); err != nil {
				t.Fatal(err)
			}
			fresh, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			db, err := fresh.SQLDB(storage.TradeOwner)
			if err != nil {
				t.Fatal(err)
			}
			store, err := trade.NewStore(fresh)
			if err != nil {
				t.Fatal(err)
			}
			now := f.permit.IssuedAt.Add(2 * time.Minute)
			env := f.e.verifier.profile.process.scope.Environment
			if _, err = db.Exec(`UPDATE canary_worker_leases SET holder_id='restart-owner',lease_epoch=2,updated_at=?,expires_at=? WHERE role='SUBMISSION' AND environment=?`, now.Format(time.RFC3339Nano), now.Add(time.Minute).Format(time.RFC3339Nano), env); err != nil {
				t.Fatal(err)
			}
			lease := trade.SubmissionLeaseFence{Environment: env, HolderID: "restart-owner", Epoch: 2}
			for i := 0; i < 2; i++ {
				if err = store.RecoverManualFirstSubmissions(context.Background(), lease, now); err != nil {
					t.Fatal(err)
				}
			}
			var state, lane, reservation string
			if err = db.QueryRow(`SELECT s.state,l.state,r.state FROM transaction_submissions s JOIN manual_first_submission_bindings b ON b.submission_id=s.id JOIN execution_wallet_lanes l ON l.operation_id=b.operation_id JOIN canary_risk_reservations r ON r.operation_id=b.operation_id`).Scan(&state, &lane, &reservation); err != nil {
				t.Fatal(err)
			}
			want := "broadcast_unknown"
			if stage == "after_claim" {
				want = "known_unsent"
			}
			if state != want || lane != "frozen" || reservation != "frozen" || f.fake.calls != wantCalls {
				t.Fatal("restart lost freeze", state, lane, reservation, f.fake.calls)
			}
			var audits int
			if err = db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='MANUAL_FIRST_SUBMISSION_OUTCOME'`).Scan(&audits); err != nil || audits != 1 {
				t.Fatal("duplicate restart outcome", audits, err)
			}
		})
	}
}

func TestManualSubmissionInvocationWriterExcludesControlAndTakeover(t *testing.T) {
	for _, kind := range []string{"stop", "takeover"} {
		t.Run(kind, func(t *testing.T) {
			f := newManualSubmissionFixture(t, "buy")
			second, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			db, err := second.SQLDB(storage.TradeOwner)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`PRAGMA busy_timeout=0`); err != nil {
				t.Fatal(err)
			}
			mutation := `UPDATE canary_control_state SET emergency_stopped=1`
			if kind == "takeover" {
				mutation = `UPDATE canary_worker_leases SET holder_id='takeover-owner',lease_epoch=lease_epoch+1 WHERE role='SUBMISSION'`
			}
			attempted := false
			f.fake.invoke = func(ctx context.Context) {
				attempted = true
				// A second physical SQLite writer attempts the actual control
				// COMMIT while invocation/outcome owns its writer transaction.
				_, err := db.ExecContext(ctx, mutation)
				if err == nil || (!strings.Contains(err.Error(), "SQLITE_BUSY") && !strings.Contains(err.Error(), "locked")) {
					t.Errorf("control committed inside invocation window: %v", err)
				}
			}
			sub, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID)
			if err != nil || !attempted || sub.State != "submitted" || f.fake.calls != 1 {
				t.Fatal("invocation fence", sub.State, attempted, f.fake.calls, err)
			}
			if _, err = db.Exec(mutation); err != nil {
				t.Fatal("control unavailable after outcome commit", err)
			}
		})
	}
}

func TestManualSubmissionOutcomeCommitFailureBurnsInvocationTicket(t *testing.T) {
	f := manualB24Fixture(t)
	r := manualSubmissionRequestFor(t, f)
	now := f.permit.IssuedAt.Add(time.Nanosecond)
	sub, err := f.trade.BeginManualFirstSubmission(context.Background(), r, now, manualStoreTestRecheck)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.trade.MarkManualFirstSendIntent(context.Background(), r, sub, now, manualStoreTestRecheck); err != nil {
		t.Fatal(err)
	}
	f.trade.SetCanaryHookForTest(func(stage string) error {
		if stage == "manual_submission_outcome_before_commit" {
			return errors.New("forced commit failure")
		}
		return nil
	})
	calls := 0
	callback := func(context.Context, *sql.Tx) (trade.ManualSubmissionOutcome, error) {
		calls++
		return trade.ManualSubmissionOutcome{State: "submitted", RPCHash: r.TxHash}, nil
	}
	if err = f.trade.WithManualFirstSubmissionOutcome(context.Background(), r, sub, now, manualStoreTestRecheck, callback); err == nil {
		t.Fatal("fault absent")
	}
	f.trade.SetCanaryHookForTest(nil)
	if err = f.trade.WithManualFirstSubmissionOutcome(context.Background(), r, sub, now, manualStoreTestRecheck, callback); err == nil {
		t.Fatal("burned ticket reconstructed")
	}
	if err = f.trade.MarkManualFirstSendIntent(context.Background(), r, sub, now, manualStoreTestRecheck); err == nil {
		t.Fatal("second intent created ticket")
	}
	if calls != 1 {
		t.Fatal("duplicate invocation", calls)
	}
	var state string
	var audits int
	if err = f.s.db.QueryRow(`SELECT state,(SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='MANUAL_FIRST_SUBMISSION_OUTCOME') FROM transaction_submissions WHERE id=?`, sub.ID).Scan(&state, &audits); err != nil || state != "submitting" || audits != 0 {
		t.Fatal("failed outcome partial commit", state, audits, err)
	}
}
