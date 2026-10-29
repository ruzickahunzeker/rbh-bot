package tradeapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func progressResponse(t *testing.T, w *httptest.ResponseRecorder, code int, stage string) Record {
	t.Helper()
	var r Record
	if w.Code != code || json.Unmarshal(w.Body.Bytes(), &r) != nil || r.Progress.Stage != stage || r.SendAuthorized || r.SubmissionQueued {
		t.Fatalf("unexpected HTTP progress: code=%d body=%s", w.Code, w.Body.String())
	}
	return r
}

func assertProgressQueries(t *testing.T, h *API, r Record, key, stage string) {
	t.Helper()
	for _, path := range []string{"/v1/trades/" + r.RequestID, "/v1/trades/by-idempotency-key"} {
		got := progressResponse(t, call(h, "GET", path, key, ""), 200, stage)
		if got.RequestID != r.RequestID || got.ExpiresAt != r.ExpiresAt || got.OperationID == nil && r.OperationID != nil {
			t.Fatal("query lost immutable intake identity")
		}
	}
}

func TestHTTPC1ProgressProjectionDefaultOffAndIntake(t *testing.T) {
	_, s, original, _ := fixture(t)
	rejected := progressResponse(t, call(original, "POST", "/v1/trades", "progress-rejected", encode(buy())), 503, "REJECTED")
	assertProgressQueries(t, original, rejected, "progress-rejected", "REJECTED")
	h := intakeAPI(t, s, original)
	intake := progressResponse(t, call(h, "POST", "/v1/trades", "progress-intake", encode(buy())), 202, "INTAKE_RECORDED")
	if intake.Validation != nil || intake.Admission != nil {
		t.Fatal("intake inferred validation or admission")
	}
	before := admittedDatabaseSnapshot(t, s.db)
	assertProgressQueries(t, h, intake, "progress-intake", "INTAKE_RECORDED")
	duplicate := progressResponse(t, call(h, "POST", "/v1/trades", "progress-intake", encode(buy())), 202, "INTAKE_RECORDED")
	if !duplicate.Duplicate || duplicate.ExpiresAt != intake.ExpiresAt || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("progress query/duplicate changed durable state or TTL")
	}
	noIntakeEconomicWork(t, s)
}

func TestHTTPC1ProgressProjectionValidationAndAdmission(t *testing.T) {
	_, s, validation, backend, _ := validationAPI(t)
	validated := progressResponse(t, call(validation, "POST", "/v1/trades", "progress-validation", encode(buy())), 202, "VALIDATION_RECORDED")
	if validated.Validation == nil || validated.Admission != nil {
		t.Fatal("validation progress inferred admission")
	}
	before := admittedDatabaseSnapshot(t, s.db)
	assertProgressQueries(t, validation, validated, "progress-validation", "VALIDATION_RECORDED")
	duplicate := progressResponse(t, call(validation, "POST", "/v1/trades", "progress-validation", encode(buy())), 202, "VALIDATION_RECORDED")
	if !duplicate.Duplicate || backend.resolves.Load() != 1 || backend.completes.Load() != 1 || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("progress replayed validation or wrote durable state")
	}
	disabled, err := NewWithOptions(s, validation.identity, testCredential, validation.policy, nil, Options{ManualIntake: true})
	if err != nil {
		t.Fatal(err)
	}
	withoutBackend := progressResponse(t, call(disabled, "POST", "/v1/trades", "progress-validation", encode(buy())), 202, "VALIDATION_RECORDED")
	if !withoutBackend.Duplicate || withoutBackend.Validation == nil || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("default-off duplicate lost durable validation or wrote state")
	}
	noIntakeEconomicWork(t, s)

	_, admittedStore, original, source, scope, _ := manualInputsFixture(t, "buy")
	admissionBackend := &validationBackend{}
	admission := admissionHTTP(t, admittedStore, original, scope, admissionBackend)
	committed := progressResponse(t, call(admission, "POST", "/v1/trades", "b2-inputs", encode(source.Request)), 202, "ADMISSION_RECORDED")
	if committed.Admission == nil || committed.Validation == nil || committed.Admission.Status != "ADMITTED_NOT_SIGNED_NOT_SUBMITTED" {
		t.Fatal("admission progress not backed by durable admission")
	}
	before = admittedDatabaseSnapshot(t, admittedStore.db)
	assertProgressQueries(t, admission, committed, "b2-inputs", "ADMISSION_RECORDED")
	duplicate = progressResponse(t, call(admission, "POST", "/v1/trades", "b2-inputs", encode(source.Request)), 202, "ADMISSION_RECORDED")
	if !duplicate.Duplicate || duplicate.Admission == nil || duplicate.Admission.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" || admittedDatabaseSnapshot(t, admittedStore.db) != before {
		t.Fatal("progress query/duplicate renewed admission or wrote durable state")
	}
	disabledAdmission, err := NewWithOptions(admittedStore, admission.identity, testCredential, admission.policy, nil, Options{ManualIntake: true})
	if err != nil {
		t.Fatal(err)
	}
	assertProgressQueries(t, disabledAdmission, committed, "b2-inputs", "ADMISSION_RECORDED")
	if admittedDatabaseSnapshot(t, admittedStore.db) != before {
		t.Fatal("default-off admission history query wrote durable state")
	}
	for _, table := range []string{"transaction_attempts", "transaction_submissions", "canary_send_permits", "manual_nonce_claims"} {
		if count(t, admittedStore.db, table) != 0 {
			t.Fatalf("HTTP progress created downstream economic work in %s", table)
		}
	}
	// Deliberately corrupt a guarded fixture: progress must not promote a
	// partial admission ledger into a healthy milestone.
	if _, err := admittedStore.db.Exec(`DROP TRIGGER manual_b1_no_usage_update; UPDATE canary_authorization_usage SET total_input='0'`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/trades/" + committed.RequestID, "/v1/trades/by-idempotency-key"} {
		w := call(disabledAdmission, "GET", path, "b2-inputs", "")
		if w.Code == 200 || strings.Contains(w.Body.String(), "ADMISSION_RECORDED") {
			t.Fatal("corrupt admission history became progress", w.Body.String())
		}
	}
}
