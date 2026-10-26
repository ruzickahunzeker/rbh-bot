package tradeapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestLocalPostExecutionFirstOrderThreeSides(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			f := newLocalPostFixture(t, side)
			response := call(f.h, http.MethodPost, "/v1/trades", "local-first", encode(f.request))
			r := localPostRecord(t, response, http.StatusAccepted)
			if r.Duplicate || r.Execution == nil || r.Execution.SubmissionState != "submitted" || r.Execution.ReceiptState != "" || r.Execution.EffectState != "" || response.Header().Get("Location") != "/v1/trades/"+r.RequestID {
				t.Fatal("POST did not submit exactly once", response.Body.String())
			}
			f.assertOne(t, r, 1)
			if f.backend.resolves.Load() != 1 || f.backend.completes.Load() != 1 {
				t.Fatal("one request revalidated")
			}
			before := admittedDatabaseSnapshot(t, f.store.db)
			duplicate := localPostRecord(t, call(f.h, http.MethodPost, "/v1/trades", "local-first", encode(f.request)), http.StatusAccepted)
			byID := localPostRecord(t, call(f.h, http.MethodGet, "/v1/trades/"+r.RequestID, "", ""), http.StatusOK)
			byKey := localPostRecord(t, call(f.h, http.MethodGet, "/v1/trades/by-idempotency-key", "local-first", ""), http.StatusOK)
			if !duplicate.Duplicate || !reflect.DeepEqual(duplicate.Execution, r.Execution) || !reflect.DeepEqual(byID.Execution, r.Execution) || !reflect.DeepEqual(byKey.Execution, r.Execution) || duplicate.ExpiresAt != r.ExpiresAt || admittedDatabaseSnapshot(t, f.store.db) != before {
				t.Fatal("duplicate/history changed result or budget")
			}
			f.assertOne(t, r, 1)
		})
	}
}

func TestLocalPostExecutionAuthenticationAndStrictInput(t *testing.T) {
	f := newLocalPostFixture(t, "buy")
	before := admittedDatabaseSnapshot(t, f.store.db)
	for _, tc := range []struct {
		name, body, auth string
		code             int
	}{
		{"missing_auth", encode(f.request), "", 401},
		{"wrong_auth", encode(f.request), "Bearer wrong", 401},
		{"injected_nonce", strings.TrimSuffix(encode(f.request), "}") + `,"nonce":"23"}`, "Bearer " + testCredential, 400},
		{"injected_permit", strings.TrimSuffix(encode(f.request), "}") + `,"permit_id":"mine"}`, "Bearer " + testCredential, 400},
		{"injected_authorization", strings.TrimSuffix(encode(f.request), "}") + `,"authorization_id":"b2-auth"}`, "Bearer " + testCredential, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/trades", strings.NewReader(tc.body))
			r.Header.Set("Authorization", tc.auth)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", "invalid")
			w := httptest.NewRecorder()
			f.h.ServeHTTP(w, r)
			if w.Code != tc.code || f.signer.calls.Load() != 0 || f.transport.calls.Load() != 0 || admittedDatabaseSnapshot(t, f.store.db) != before {
				t.Fatal("unauthorized/injected request reached execution", w.Code, w.Body.String())
			}
		})
	}
	other := f.request
	other.WalletID = "outside"
	if w := call(f.h, http.MethodPost, "/v1/trades", "outside", encode(other)); w.Code != 403 || f.signer.calls.Load() != 0 || admittedDatabaseSnapshot(t, f.store.db) != before {
		t.Fatal("wallet scope bypass", w.Code)
	}
	// The very same production handler/options do NOT invoke the local chain.
	r := localPostRecord(t, call(f.h.api, http.MethodPost, "/v1/trades", "product-not-composed", encode(f.request)), 202)
	if r.Execution == nil || r.Execution.AttemptStatus != "" || f.signer.calls.Load() != 0 || f.transport.calls.Load() != 0 || count(t, f.store.db, "manual_nonce_claims") != 0 {
		t.Fatal("production API unexpectedly composed")
	}
}

func TestLocalPostExecutionOutcomeAndLostResponse(t *testing.T) {
	for _, outcome := range []string{"known_unsent", "unknown", "contradiction", "response_loss", "timeout"} {
		t.Run(outcome, func(t *testing.T) {
			f := newLocalPostFixture(t, "buy")
			f.transport.outcome = outcome
			if outcome == "response_loss" {
				f.h.checkpoint = func(stage string) error {
					if stage == "before_response" {
						return errors.New("local response lost")
					}
					return nil
				}
			}
			if outcome == "timeout" {
				f.transport.during = func(ctx context.Context) { <-ctx.Done() }
			}
			w := call(f.h, http.MethodPost, "/v1/trades", "outcome", encode(f.request))
			wantCode := 202
			if outcome == "unknown" || outcome == "response_loss" || outcome == "timeout" {
				wantCode = 503
			}
			if w.Code != wantCode || w.Header().Get("Location") == "" {
				t.Fatal("missing durable recovery handle", w.Code, w.Body.String())
			}
			request := strings.TrimPrefix(w.Header().Get("Location"), "/v1/trades/")
			r := localPostRecord(t, call(f.h, http.MethodGet, "/v1/trades/"+request, "", ""), 200)
			want := map[string]string{"known_unsent": "known_unsent", "unknown": "broadcast_unknown", "contradiction": "manual_resolution", "response_loss": "submitted", "timeout": "broadcast_unknown"}[outcome]
			if r.Execution == nil || r.Execution.SubmissionState != want {
				t.Fatal("outcome collapsed", encode(r.Execution))
			}
			f.assertOne(t, r, 1)
			f.h.checkpoint = nil
			before := admittedDatabaseSnapshot(t, f.store.db)
			duplicate := localPostRecord(t, call(f.h, http.MethodPost, "/v1/trades", "outcome", encode(f.request)), 202)
			if !duplicate.Duplicate || duplicate.Execution.SubmissionState != want || admittedDatabaseSnapshot(t, f.store.db) != before {
				t.Fatal("duplicate resumed execution")
			}
			if want != "submitted" {
				var lane, risk string
				if err := f.store.db.QueryRow(`SELECT l.state,r.state FROM execution_wallet_lanes l JOIN canary_risk_reservations r ON r.operation_id=l.operation_id WHERE l.operation_id=?`, *r.OperationID).Scan(&lane, &risk); err != nil || lane != "frozen" || risk != "frozen" {
					t.Fatal("ambiguity/rejection released retained ownership", lane, risk, err)
				}
			}
			f.assertOne(t, r, 1)
		})
	}
}

func TestLocalPostExecutionIdempotencyConflictAndWalletReuse(t *testing.T) {
	f := newLocalPostFixture(t, "buy")
	r := localPostRecord(t, call(f.h, "POST", "/v1/trades", "one", encode(f.request)), 202)
	before := admittedDatabaseSnapshot(t, f.store.db)
	changed := f.request
	changed.Amount = "0.02"
	if w := call(f.h, "POST", "/v1/trades", "one", encode(changed)); w.Code != 409 || admittedDatabaseSnapshot(t, f.store.db) != before {
		t.Fatal("changed payload adopted original execution", w.Code)
	}
	if w := call(f.h, "POST", "/v1/trades", "new-key", encode(changed)); w.Code != 422 || !strings.Contains(w.Body.String(), "MANUAL_WALLET_REUSE_UNSUPPORTED") {
		t.Fatal("wallet reused", w.Code, w.Body.String())
	}
	f.assertOne(t, r, 1)
	// New request intake may be retained, but no second economic identity exists.
	var used int
	if err := f.store.db.QueryRow(`SELECT operation_count FROM canary_authorization_usage`).Scan(&used); err != nil || used != 1 {
		t.Fatal("rejection consumed budget", used, err)
	}
	cap := call(f.h, http.MethodGet, "/v1/capabilities", "", "")
	var flags map[string]any
	if json.Unmarshal(cap.Body.Bytes(), &flags) != nil || flags["live"] != false || flags["submission_enabled"] != false || flags["send_authorized"] != false {
		t.Fatal("local harness promoted production readiness")
	}
}
