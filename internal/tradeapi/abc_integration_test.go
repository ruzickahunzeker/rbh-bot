package tradeapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestHTTPABCIntakeProgressAndExecution(t *testing.T) {
	_, store, original, _ := fixture(t)
	h := intakeAPI(t, store, original)
	created := progressResponse(t, call(h, http.MethodPost, "/v1/trades", "abc-intake", encode(buy())), http.StatusAccepted, "INTAKE_RECORDED")
	before := admittedDatabaseSnapshot(t, store.db)
	for _, path := range []string{"/v1/trades/" + created.RequestID, "/v1/trades/by-idempotency-key"} {
		got := progressResponse(t, call(h, http.MethodGet, path, "abc-intake", ""), http.StatusOK, "INTAKE_RECORDED")
		if got.Execution == nil || got.Execution.OperationStatus != "manual_validation_pending" || got.Execution.StepStatus != "validation_pending" || got.Execution.TxHash != "" || got.Execution.SubmissionState != "" {
			t.Fatalf("combined intake projection: %+v", got.Execution)
		}
	}
	duplicate := progressResponse(t, call(h, http.MethodPost, "/v1/trades", "abc-intake", encode(buy())), http.StatusAccepted, "INTAKE_RECORDED")
	if !duplicate.Duplicate || duplicate.ExpiresAt != created.ExpiresAt || admittedDatabaseSnapshot(t, store.db) != before {
		t.Fatal("combined query/duplicate changed intake, TTL or accounting")
	}
	noIntakeEconomicWork(t, store)
}

func TestHTTPABCSignatureIsNotSubmission(t *testing.T) {
	_, store, h, order, scope, _, writer, signer, _ := localSigningFixture(t, "buy")
	if _, err := writer.sign(context.Background(), h.identity, order.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, store.db)
	got := progressResponse(t, call(h, http.MethodGet, "/v1/trades/"+order.RequestID, "", ""), http.StatusOK, "ADMISSION_RECORDED")
	if got.Execution == nil || got.Execution.AttemptStatus != "signed" || got.Execution.TxHash == "" || got.Execution.SubmissionState != "" || got.Execution.ReceiptState != "" || got.Execution.EffectState != "" {
		t.Fatalf("signed hash promoted to submission/settlement: %+v", got.Execution)
	}
	if admittedDatabaseSnapshot(t, store.db) != before || signer.calls.Load() != 1 || count(t, store.db, "transaction_submissions") != 0 {
		t.Fatal("historical signature query produced economic work")
	}
}

func TestHTTPABCSubmittedHistoryAfterStopAndRevoke(t *testing.T) {
	f := newManualSubmissionFixture(t, "buy")
	sub, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID)
	if err != nil || sub.State != "submitted" || f.fake.calls != 1 {
		t.Fatal("local fake submission fixture", sub.State, f.fake.calls, err)
	}
	store := f.e.verifier.store
	h, err := NewWithOptions(store, f.id, testCredential, DefaultPolicy(), nil, Options{ManualIntake: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
		t.Fatal(err)
	}
	var key string
	if err := store.db.QueryRow(`SELECT idempotency_key FROM manual_http_order_sources WHERE id=?`, f.request).Scan(&key); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, store.db)
	for _, path := range []string{"/v1/trades/" + f.request, "/v1/trades/by-idempotency-key"} {
		response := call(h, http.MethodGet, path, key, "")
		got := progressResponse(t, response, http.StatusOK, "ADMISSION_RECORDED")
		if got.Execution == nil || got.Execution.SubmissionState != "submitted" || got.Execution.TxHash != sub.TxHash || got.Execution.ReceiptState != "" || got.Execution.EffectState != "" {
			t.Fatalf("submitted history lost or promoted to settlement: %+v", got.Execution)
		}
		for _, private := range []string{`"encrypted_raw_tx"`, `"ciphertext"`, `"encryption_nonce"`, `"raw_transaction"`, `"private_key"`, `"permit_id"`, `"nonce"`} {
			if strings.Contains(response.Body.String(), private) {
				t.Fatalf("HTTP history disclosed %s", private)
			}
		}
	}
	if admittedDatabaseSnapshot(t, store.db) != before || f.fake.calls != 1 {
		t.Fatal("stop/revoke historical query mutated state or repeated invocation")
	}
	other := f.id
	other.PrincipalID = "different-principal"
	foreign, err := NewWithOptions(store, other, testCredential, DefaultPolicy(), nil, Options{ManualIntake: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := call(foreign, http.MethodGet, "/v1/trades/"+f.request, "", ""); got.Code != http.StatusNotFound {
		t.Fatal("combined projection crossed principal boundary", got.Code)
	}
}
