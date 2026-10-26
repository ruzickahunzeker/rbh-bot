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
	"time"
)

func assertExecutionPublicJSON(t *testing.T, body string) {
	t.Helper()
	for _, private := range []string{`"encrypted_raw_tx"`, `"ciphertext"`, `"encryption_nonce"`, `"raw_transaction"`, `"raw_bytes"`, `"private_key"`, `"permit_id"`, `"nonce"`} {
		if strings.Contains(body, private) {
			t.Fatalf("execution presentation disclosed %s", private)
		}
	}
}

func TestHTTPCExecutionSignedHistoryAndDuplicateDoNotSubmit(t *testing.T) {
	_, store, original, order, scope, _, writer, signer, _ := localSigningFixture(t, "buy")
	if _, err := writer.sign(context.Background(), original.identity, order.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	h, err := NewWithOptions(store, original.identity, testCredential, original.policy, nil, Options{ManualIntake: true})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(context.Background(), h.identity, order.RequestID, false)
	if err != nil {
		t.Fatal(err)
	}
	var key string
	if err := store.db.QueryRow(`SELECT idempotency_key FROM manual_http_order_sources WHERE id=?`, order.RequestID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, store.db)
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/trades/" + order.RequestID, ""},
		{http.MethodGet, "/v1/trades/by-idempotency-key", ""},
		{http.MethodPost, "/v1/trades", encode(stored.Request)},
	} {
		response := call(h, request.method, request.path, key, request.body)
		code := http.StatusOK
		if request.method == http.MethodPost {
			code = http.StatusAccepted
		}
		got := progressResponse(t, response, code, "ADMISSION_RECORDED")
		if got.Execution == nil || got.Execution.AttemptStatus != "signed" || got.Execution.TxHash == "" || got.Execution.SubmissionState != "" || got.Execution.ReceiptState != "" || got.Execution.EffectState != "" {
			t.Fatalf("signed hash implied propagation or duplicate lost history: %+v", got.Execution)
		}
		if !reflect.DeepEqual(got.Execution, stored.Execution) || got.ExpiresAt != stored.ExpiresAt || got.Status != stored.Status || got.Reason != stored.Reason {
			t.Fatal("presentation changed durable identity, TTL or intake history")
		}
		if request.method == http.MethodPost && !got.Duplicate {
			t.Fatal("duplicate POST presented as new execution")
		}
		assertExecutionPublicJSON(t, response.Body.String())
	}
	if signer.calls.Load() != 1 || count(t, store.db, "transaction_submissions") != 0 || admittedDatabaseSnapshot(t, store.db) != before {
		t.Fatal("history or duplicate produced new signing, submission or accounting")
	}
}

func TestHTTPCExecutionSubmissionOutcomesRemainHistoricalAndFrozen(t *testing.T) {
	for _, tc := range []struct {
		name, state  string
		result       manualTransportResult
		transportErr error
	}{
		{name: "submitted", state: "submitted"},
		{name: "known_unsent_retained", state: "known_unsent", result: manualTransportResult{deterministicUnsent: true}},
		{name: "ambiguous", state: "broadcast_unknown", transportErr: errors.New("local ambiguous transport")},
		{name: "contradictory", state: "manual_resolution", result: manualTransportResult{txHash: "unexpected"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManualSubmissionFixture(t, "buy")
			if tc.state != "submitted" {
				f.fake.result, f.fake.err = tc.result, tc.transportErr
			}
			sub, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID)
			if sub.State != tc.state || f.fake.calls != 1 {
				t.Fatalf("local outcome fixture: %+v calls=%d err=%v", sub, f.fake.calls, err)
			}
			store := f.e.verifier.store
			h, err := NewWithOptions(store, f.id, testCredential, DefaultPolicy(), nil, Options{ManualIntake: true})
			if err != nil {
				t.Fatal(err)
			}
			stored, err := store.Get(context.Background(), f.id, f.request, false)
			if err != nil {
				t.Fatal(err)
			}
			var key string
			if err := store.db.QueryRow(`SELECT idempotency_key FROM manual_http_order_sources WHERE id=?`, f.request).Scan(&key); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
				t.Fatal(err)
			}
			expires, err := time.Parse(time.RFC3339Nano, stored.ExpiresAt)
			if err != nil {
				t.Fatal(err)
			}
			h.now = func() time.Time { return expires.Add(time.Hour) }
			before := admittedDatabaseSnapshot(t, store.db)
			for _, request := range []struct{ method, path, body string }{
				{http.MethodGet, "/v1/trades/" + f.request, ""},
				{http.MethodGet, "/v1/trades/by-idempotency-key", ""},
				{http.MethodPost, "/v1/trades", encode(stored.Request)},
			} {
				response := call(h, request.method, request.path, key, request.body)
				code := http.StatusOK
				if request.method == http.MethodPost {
					code = http.StatusAccepted
				}
				got := progressResponse(t, response, code, "ADMISSION_RECORDED")
				if got.Execution == nil || got.Execution.SubmissionState != tc.state || got.Execution.TxHash != sub.TxHash || got.Execution.ReceiptState != "" || got.Execution.EffectState != "" {
					t.Fatalf("outcome lost or promoted to canonical success: %+v", got.Execution)
				}
				if !reflect.DeepEqual(got.Execution, stored.Execution) || got.ExpiresAt != stored.ExpiresAt || got.Status != stored.Status || got.Reason != stored.Reason {
					t.Fatal("historical query/duplicate changed outcome, TTL or intake history")
				}
				assertExecutionPublicJSON(t, response.Body.String())
			}
			var lane, reservation string
			if err := store.db.QueryRow(`SELECT l.state,r.state FROM execution_wallet_lanes l JOIN canary_risk_reservations r ON r.operation_id=l.operation_id WHERE l.operation_id=?`, f.permit.OperationID).Scan(&lane, &reservation); err != nil {
				t.Fatal(err)
			}
			if lane != "frozen" || reservation != "frozen" || f.fake.calls != 1 || admittedDatabaseSnapshot(t, store.db) != before {
				t.Fatal("history/duplicate released frozen ownership or caused economic work", lane, reservation, f.fake.calls)
			}
			capabilities := call(h, http.MethodGet, "/v1/capabilities", "", "")
			var flags map[string]any
			if capabilities.Code != http.StatusOK || json.Unmarshal(capabilities.Body.Bytes(), &flags) != nil {
				t.Fatal("capabilities unavailable")
			}
			for _, name := range []string{"submission_enabled", "send_authorized", "live", "release_ready"} {
				if flags[name] != false {
					t.Fatal("historical outcome granted capability", name)
				}
			}
		})
	}
}

// These are serialization contracts only. They deliberately do not bypass
// recovery schema guards or claim that an HTTP request performs reconciliation.
func TestHTTPCExecutionReceiptAndReorgFactsAreIndependent(t *testing.T) {
	for _, tc := range []struct {
		name, receipt, effect string
	}{
		{"observed_not_canonical", "observed", ""},
		{"canonical_applied", "canonical_success", "active"},
		{"canonical_reverted", "canonical_revert", ""},
		{"orphan_rollback", "orphaned", "rolled_back"},
		{"canonical_reapplied", "canonical_success", "active"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Record{Status: "RECORDED", Reason: "VALIDATION_NOT_CONNECTED", Admission: &ManualAdmission{}, Execution: &ExecutionSnapshot{OperationStatus: "historical", StepStatus: "historical", SubmissionState: "submitted", TxHash: "0x01", ReceiptState: tc.receipt, EffectState: tc.effect}}
			r.projectProgress()
			response := httptest.NewRecorder()
			writeJSON(response, http.StatusOK, r)
			got := progressResponse(t, response, http.StatusOK, "ADMISSION_RECORDED")
			if !reflect.DeepEqual(got.Execution, r.Execution) || got.Status != r.Status || got.Reason != r.Reason {
				t.Fatal("serialization replaced independent durable facts with a success milestone")
			}
			if tc.effect == "" && strings.Contains(response.Body.String(), `"position_effect_state"`) {
				t.Fatal("missing effect presented as applied")
			}
			for _, invented := range []string{`"success"`, `"retry_allowed"`, `"replay_allowed"`, `"finalized"`} {
				if strings.Contains(response.Body.String(), invented) {
					t.Fatalf("presentation invented permission/result %s", invented)
				}
			}
			assertExecutionPublicJSON(t, response.Body.String())
		})
	}
}
