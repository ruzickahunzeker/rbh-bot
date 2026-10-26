package tradeapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

// A fresh composition can inspect original duplicate history, but no lost HTTP
// stack, signer continuation or one-shot invocation ticket can be reconstructed.
func (f localPostFixture) anotherHandle(t *testing.T, d *storage.Database, s *Store) *localPostHarness {
	t.Helper()
	api := admissionHTTP(t, s, f.h.api, f.h.scope, &validationBackend{wallet: f.h.api.identity.WalletAddress})
	v := f.h.verifier
	verifier := &manualRuntimeVerifier{store: s, profile: v.profile, timeSource: f.clock, identity: &runtimeTestIdentity{process: v.profile.process}, decryptor: v.decryptor,
		observer: &runtimeTestObserver{environment: f.h.scope.Environment, profile: v.profile.process.networkProfile}}
	store, err := trade.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	return &localPostHarness{api: api, scope: f.h.scope, binding: bindingWriter(s), nonce: nonceWriter(s, &localNonceObserver{nonce: "23"}),
		signing:  &manualLocalSigningWriter{store: s, observer: &localNonceObserver{nonce: "23"}, signer: f.signer, cipher: f.h.signing.cipher, now: func() time.Time { return testNow }},
		verifier: verifier, executor: &manualSubmissionExecutor{verifier: verifier, store: store, transport: f.transport, timeout: time.Second}}
}

func TestLocalPostExecutionConcurrentTwoHandleDuplicates(t *testing.T) {
	f := newLocalPostFixture(t, "buy")
	d, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	second := f.anotherHandle(t, d, s)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var reached sync.Once
	hook := func(at string) error {
		if at == "before_binding" {
			reached.Do(func() { close(entered) })
			<-release
		}
		return nil
	}
	f.h.checkpoint, second.checkpoint = hook, hook
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 6)
	for i := 0; i < 6; i++ {
		h := f.h
		if i%2 == 1 {
			h = second
		}
		go func() { <-start; results <- call(h, "POST", "/v1/trades", "concurrent", encode(f.request)) }()
	}
	close(start)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("fresh intake never reached composition")
	}
	// Five duplicates must return WITHOUT waiting for/executing the original.
	for i := 0; i < 5; i++ {
		select {
		case w := <-results:
			r := localPostRecord(t, w, 202)
			if !r.Duplicate {
				t.Fatal("more than one fresh intake")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("duplicate depends on in-memory execution queue")
		}
	}
	if f.signer.calls.Load() != 0 || f.transport.calls.Load() != 0 {
		t.Fatal("duplicate performed economic work")
	}
	once.Do(func() { close(release) })
	select {
	case w := <-results:
		r := localPostRecord(t, w, 202)
		if r.Duplicate || r.Execution.SubmissionState != "submitted" {
			t.Fatal("winner did not execute")
		}
		f.assertOne(t, r, 1)
	case <-time.After(20 * time.Second):
		t.Fatal("winner did not finish")
	}
}

func TestLocalPostExecutionFaultReopenNeverResumes(t *testing.T) {
	for _, stage := range []string{"after_binding", "after_nonce", "sign_after_marker_commit", "sign_after_sign", "after_artifact", "after_permit", "submit_after_claim", "submit_after_intent", "submit_after_invocation"} {
		t.Run(stage, func(t *testing.T) {
			f := newLocalPostFixture(t, "buy")
			fault := errors.New("controlled crash window")
			hook := func(at string) error {
				if at == stage {
					return fault
				}
				return nil
			}
			f.h.checkpoint = hook
			f.h.signing.checkpoint = func(at string) error { return hook("sign_" + at) }
			f.h.executor.checkpoint = func(at string) error { return hook("submit_" + at) }
			w := call(f.h, "POST", "/v1/trades", "crash", encode(f.request))
			if w.Code != 503 || w.Header().Get("Location") == "" {
				t.Fatal("fault dropped durable handle", w.Code, w.Body.String())
			}
			request := strings.TrimPrefix(w.Header().Get("Location"), "/v1/trades/")
			calls := int64(0)
			if stage == "submit_after_invocation" {
				calls = 1
			}
			signatures := int64(1)
			if stage == "after_binding" || stage == "after_nonce" || stage == "sign_after_marker_commit" {
				signatures = 0
			}
			if f.signer.calls.Load() != signatures || f.transport.calls.Load() != calls {
				t.Fatal("fault action counts", f.signer.calls.Load(), f.transport.calls.Load())
			}
			before := admittedDatabaseSnapshot(t, f.store.db)
			d, s := bindingReopen(t, f.database, f.path)
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("reopen silently repaired or lost work")
			}
			h := f.anotherHandle(t, d, s)
			duplicate := localPostRecord(t, call(h, "POST", "/v1/trades", "crash", encode(f.request)), 202)
			if !duplicate.Duplicate || duplicate.RequestID != request || admittedDatabaseSnapshot(t, s.db) != before || f.signer.calls.Load() != signatures || f.transport.calls.Load() != calls {
				t.Fatal("restart resumed or replaced original execution")
			}
			if strings.HasPrefix(stage, "submit_") {
				now := testNow.Add(2 * time.Minute)
				if err := h.executor.store.AcquireCanaryWorkerLease(context.Background(), "SUBMISSION", h.scope.Environment, "restart-only-classifier", 2, now.Add(time.Minute), now); err != nil {
					t.Fatal(err)
				}
				lease := trade.SubmissionLeaseFence{Environment: h.scope.Environment, HolderID: "restart-only-classifier", Epoch: 2}
				for i := 0; i < 2; i++ {
					if err := h.executor.store.RecoverManualFirstSubmissions(context.Background(), lease, now); err != nil {
						t.Fatal(err)
					}
				}
				got := localPostRecord(t, call(h, "GET", "/v1/trades/"+request, "", ""), 200)
				want := "broadcast_unknown"
				if stage == "submit_after_claim" {
					want = "known_unsent"
				}
				if got.Execution.SubmissionState != want || f.transport.calls.Load() != calls {
					t.Fatal("durable intent ambiguity semantics changed", got.Execution.SubmissionState)
				}
				var lane, risk string
				if err := s.db.QueryRow(`SELECT l.state,r.state FROM execution_wallet_lanes l JOIN canary_risk_reservations r ON r.operation_id=l.operation_id WHERE l.operation_id=?`, *got.OperationID).Scan(&lane, &risk); err != nil || lane != "frozen" || risk != "frozen" {
					t.Fatal("restart released retained ownership", lane, risk, err)
				}
			}
		})
	}
}

func TestLocalPostExecutionControlsAtIndependentBoundaries(t *testing.T) {
	for _, tc := range []struct {
		stage, change string
		signatures    int64
	}{
		{"before_binding", "stop", 0},
		{"after_nonce", "stop", 0},
		{"after_nonce", "expiry", 0},
		{"after_artifact", "revoke", 1},
		{"after_foundation", "deployment", 1},
		{"after_permit", "lease", 1},
		{"submit_after_intent", "stop", 1},
		{"submit_after_intent", "revoke", 1},
	} {
		t.Run(tc.stage+"_"+tc.change, func(t *testing.T) {
			f := newLocalPostFixture(t, "buy")
			mutate := func(at string) error {
				if at != tc.stage {
					return nil
				}
				switch tc.change {
				case "stop":
					_, err := f.store.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`)
					return err
				case "revoke":
					return f.h.executor.store.TransitionRuntimeAuthorization(context.Background(), f.h.scope.AuthorizationID, "ARMED", "REVOKED", testNow)
				case "expiry":
					f.h.signing.now = func() time.Time { return testNow.Add(time.Hour) }
				case "deployment":
					f.h.verifier.identity.(*runtimeTestIdentity).process.scope.DeploymentID = "different"
				case "lease":
					_, err := f.store.db.Exec(`UPDATE canary_worker_leases SET holder_id='takeover',lease_epoch=2 WHERE role='SUBMISSION'`)
					return err
				}
				return nil
			}
			f.h.checkpoint = mutate
			f.h.executor.checkpoint = func(at string) error { return mutate("submit_" + at) }
			w := call(f.h, "POST", "/v1/trades", "control", encode(f.request))
			if w.Code < 400 || f.signer.calls.Load() != tc.signatures || f.transport.calls.Load() != 0 {
				t.Fatal("mutable control failed open", w.Code, w.Body.String(), f.signer.calls.Load(), f.transport.calls.Load())
			}
			path := w.Header().Get("Location")
			if path == "" {
				t.Fatal("missing original query handle")
			}
			got := localPostRecord(t, call(f.h, "GET", path, "", ""), http.StatusOK)
			if got.Execution.SubmissionState == "submitted" {
				t.Fatal("failed control reported propagation")
			}
		})
	}
}

func TestLocalPostExecutionCancellationAfterClaim(t *testing.T) {
	f := newLocalPostFixture(t, "buy")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.h.executor.checkpoint = func(at string) error {
		if at == "after_claim" {
			cancel()
		}
		return nil
	}
	r := httptest.NewRequest("POST", "/v1/trades", strings.NewReader(encode(f.request))).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+testCredential)
	r.Header.Set("Idempotency-Key", "cancel")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 503 || f.signer.calls.Load() != 1 || f.transport.calls.Load() != 0 || count(t, f.store.db, "transaction_submissions") != 1 {
		t.Fatal("cancel erased committed claim or sent", w.Code)
	}
	before := admittedDatabaseSnapshot(t, f.store.db)
	got := localPostRecord(t, call(f.h, "POST", "/v1/trades", "cancel", encode(f.request)), 202)
	if !got.Duplicate || got.Execution.SubmissionState != "submitting" || admittedDatabaseSnapshot(t, f.store.db) != before || f.transport.calls.Load() != 0 {
		t.Fatal("retry after cancellation resumed send")
	}
}
