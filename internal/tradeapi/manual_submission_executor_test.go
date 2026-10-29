package tradeapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

type manualFakeFirstTransport struct {
	calls    int
	borrowed []byte
	rawHash  string
	result   manualTransportResult
	err      error
	invoke   func(context.Context)
}

func (f *manualFakeFirstTransport) submitExact(ctx context.Context, raw []byte) (manualTransportResult, error) {
	f.calls++
	f.borrowed = raw
	f.rawHash = hash(raw)
	if f.invoke != nil {
		f.invoke(ctx)
	}
	return f.result, f.err
}

type manualSubmissionFixture struct {
	d       *storage.Database
	path    string
	e       *manualSubmissionExecutor
	fake    *manualFakeFirstTransport
	id      Identity
	request string
	permit  manualFirstBroadcastPermit
	clock   *runtimeTestClock
}

func newManualSubmissionFixture(t *testing.T, side string) manualSubmissionFixture {
	t.Helper()
	d, s, path, v, id, request, clock, _ := runtimeVerifierFixture(t, side)
	if _, err := v.capture(context.Background(), id, request); err != nil {
		t.Fatal(err)
	}
	var attempt, raw, txHash string
	if err := s.db.QueryRow(`SELECT q.attempt_id,json_extract(m.envelope_json,'$.raw_sha256'),json_extract(m.envelope_json,'$.tx_hash') FROM manual_signed_artifact_commands m JOIN manual_nonce_claims q ON q.operation_id=m.operation_id`).Scan(&attempt, &raw, &txHash); err != nil {
		t.Fatal(err)
	}
	p, err := (&manualFirstBroadcastIssuer{verifier: v}).issue(context.Background(), id, request, attempt, raw, txHash)
	if err != nil {
		t.Fatal(err)
	}
	store, err := trade.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	clock.move(p.IssuedAt.Add(time.Nanosecond))
	fake := &manualFakeFirstTransport{result: manualTransportResult{txHash: txHash}}
	return manualSubmissionFixture{d: d, path: path, e: &manualSubmissionExecutor{verifier: v, store: store, transport: fake, timeout: time.Second}, fake: fake, id: id, request: request, permit: p, clock: clock}
}

func TestManualSubmissionExecutorExactFirstOnly(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			f := newManualSubmissionFixture(t, side)
			sub, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID)
			if err != nil {
				t.Fatal(err)
			}
			if sub.State != "submitted" || sub.TxHash != f.permit.TxHash || sub.RPCHash != f.permit.TxHash || f.fake.calls != 1 || f.fake.rawHash != f.permit.ArtifactHash {
				t.Fatalf("wrong first outcome: %+v calls=%d", sub, f.fake.calls)
			}
			for _, b := range f.fake.borrowed {
				if b != 0 {
					t.Fatal("borrowed plaintext retained")
				}
			}
			if _, err = f.e.execute(context.Background(), f.id, f.request, f.permit.ID); err == nil {
				t.Fatal("duplicate accepted")
			}
			var rows int
			if err = f.e.verifier.store.db.QueryRow(`SELECT COUNT(*) FROM transaction_submissions`).Scan(&rows); err != nil || rows != 1 || f.fake.calls != 1 {
				t.Fatal("duplicate economic action", rows, f.fake.calls, err)
			}
		})
	}
}

func TestManualSubmissionExecutorOutcomeDistinctions(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		result      manualTransportResult
		err         error
	}{
		{"deterministic_rejection", "known_unsent", manualTransportResult{deterministicUnsent: true}, nil},
		{"ambiguous", "broadcast_unknown", manualTransportResult{}, errors.New("transport outcome unavailable")},
		{"contradiction", "manual_resolution", manualTransportResult{txHash: "unexpected"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManualSubmissionFixture(t, "buy")
			f.fake.result, f.fake.err = tc.result, tc.err
			sub, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID)
			if err != nil && tc.err == nil {
				t.Fatal(err)
			}
			if sub.State != tc.state || f.fake.calls != 1 {
				t.Fatal(sub.State, f.fake.calls)
			}
			var lane, reservation string
			if err = f.e.verifier.store.db.QueryRow(`SELECT l.state,r.state FROM execution_wallet_lanes l JOIN canary_risk_reservations r ON r.operation_id=l.operation_id WHERE l.operation_id=?`, f.permit.OperationID).Scan(&lane, &reservation); err != nil {
				t.Fatal(err)
			}
			if lane != "frozen" || reservation != "frozen" {
				t.Fatal("outcome released ownership", lane, reservation)
			}
		})
	}
}

func TestManualSubmissionExecutorFaultDoesNotRecreatePermission(t *testing.T) {
	for _, stage := range []string{"after_claim", "after_intent", "before_invocation", "after_invocation"} {
		t.Run(stage, func(t *testing.T) {
			f := newManualSubmissionFixture(t, "buy")
			f.e.checkpoint = func(at string) error {
				if at == stage {
					return errors.New("injected local fault")
				}
				return nil
			}
			_, _ = f.e.execute(context.Background(), f.id, f.request, f.permit.ID)
			wantCalls := 0
			if stage == "after_invocation" {
				wantCalls = 1
			}
			if f.fake.calls != wantCalls {
				t.Fatal("wrong invocation count", f.fake.calls)
			}
			f.e.checkpoint = nil
			if _, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID); err == nil {
				t.Fatal("retry adopted consumed permit")
			}
			if f.fake.calls != wantCalls {
				t.Fatal("automatic resend")
			}
			var permits, submissions int
			if err := f.e.verifier.store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM canary_send_permits WHERE state='CONSUMED'),(SELECT COUNT(*) FROM transaction_submissions)`).Scan(&permits, &submissions); err != nil || permits != 1 || submissions != 1 {
				t.Fatal("partial claim", permits, submissions, err)
			}
		})
	}
}

func TestManualSubmissionExecutorStopAfterIntentPreventsInvocation(t *testing.T) {
	f := newManualSubmissionFixture(t, "buy")
	f.e.checkpoint = func(at string) error {
		if at == "after_intent" {
			_, err := f.e.verifier.store.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`)
			return err
		}
		return nil
	}
	if _, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID); err == nil {
		t.Fatal("stop ignored")
	}
	if f.fake.calls != 0 {
		t.Fatal("invoked despite stop")
	}
	var intent string
	if err := f.e.verifier.store.db.QueryRow(`SELECT send_intent_at FROM transaction_submissions`).Scan(&intent); err != nil || intent == "" {
		t.Fatal("lost ambiguity boundary", err)
	}
}

func TestManualSubmissionExecutorFinalMutableControlsDeny(t *testing.T) {
	for _, control := range []string{"revoke", "permit_expiry", "deployment", "rpc_unverifiable", "lease_takeover", "clock_reversal"} {
		t.Run(control, func(t *testing.T) {
			f := newManualSubmissionFixture(t, "buy")
			f.e.checkpoint = func(at string) error {
				if at != "after_intent" {
					return nil
				}
				switch control {
				case "revoke":
					return f.e.store.TransitionRuntimeAuthorization(context.Background(), f.permit.AuthorizationID, "ARMED", "REVOKED", f.permit.IssuedAt.Add(time.Nanosecond))
				case "permit_expiry":
					f.clock.move(f.permit.ExpiresAt)
				case "deployment":
					f.e.verifier.identity.(*runtimeTestIdentity).process.scope.DeploymentID = "different-deployment"
				case "rpc_unverifiable":
					f.e.verifier.observer.(*runtimeTestObserver).bad = "hash"
				case "lease_takeover":
					_, err := f.e.verifier.store.db.Exec(`UPDATE canary_worker_leases SET holder_id='other',lease_epoch=2 WHERE role='SUBMISSION'`)
					return err
				case "clock_reversal":
					f.clock.move(f.permit.IssuedAt.Add(-time.Second))
				}
				return nil
			}
			if _, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID); err == nil {
				t.Fatal("control drift accepted")
			}
			if f.fake.calls != 0 {
				t.Fatal("invocation after final control failure")
			}
			var count int
			if err := f.e.verifier.store.db.QueryRow(`SELECT COUNT(*) FROM transaction_submissions WHERE send_intent_at IS NOT NULL`).Scan(&count); err != nil || count != 1 {
				t.Fatal("lost durable work", count, err)
			}
		})
	}
}

func TestManualSubmissionExecutorTransportTimeoutAndPanicStayAmbiguous(t *testing.T) {
	for _, fault := range []string{"timeout", "panic"} {
		t.Run(fault, func(t *testing.T) {
			f := newManualSubmissionFixture(t, "buy")
			f.e.timeout = time.Millisecond
			f.fake.invoke = func(ctx context.Context) {
				if fault == "panic" {
					panic("local callback fault")
				}
				<-ctx.Done()
			}
			sub, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID)
			if err == nil || sub.State != "broadcast_unknown" || f.fake.calls != 1 {
				t.Fatal("lost ambiguity", sub.State, f.fake.calls, err)
			}
			for _, b := range f.fake.borrowed {
				if b != 0 {
					t.Fatal("plaintext retained after failure")
				}
			}
			if _, err = f.e.execute(context.Background(), f.id, f.request, f.permit.ID); err == nil || f.fake.calls != 1 {
				t.Fatal("automatic retry after uncertain invocation")
			}
		})
	}
}
