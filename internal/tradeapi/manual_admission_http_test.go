package tradeapi

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func httpAdmissionScope(s manualAdmissionScope) *ManualAdmissionScope {
	return &ManualAdmissionScope{s.AuthorizationID, s.AuthorizationEpoch, s.ManualPolicyID, s.Environment, s.BuildSHA, s.ReleaseID, s.DeploymentID}
}

func admissionHTTP(t *testing.T, store *Store, original *API, scope manualAdmissionScope, backend *validationBackend) *API {
	t.Helper()
	h, err := NewWithOptions(store, original.identity, testCredential, original.policy, nil, Options{ManualIntake: true,
		ValidationBackend: backend, ValidationPolicyVersion: 1, ManualAdmission: true, AdmissionScope: httpAdmissionScope(scope)})
	if err != nil {
		t.Fatal(err)
	}
	h.now = original.now
	h.validator.now = func() time.Time { return h.now() }
	return h
}

func admissionRecord(t *testing.T, w *httptest.ResponseRecorder, code int) Record {
	t.Helper()
	var r Record
	if w.Code != code || json.Unmarshal(w.Body.Bytes(), &r) != nil || r.Admission == nil || r.Validation == nil || r.SendAuthorized || r.SubmissionQueued || r.ContractDeadline || r.Admission.ExecutionAuthorized || r.Admission.SendAuthorized || r.Admission.ExpiresAt != r.ExpiresAt {
		t.Fatalf("unsafe/incomplete admission HTTP: code=%d body=%s", w.Code, w.Body.String())
	}
	return r
}

func TestHTTPC3COnePostBuyFixedPercentageSellAndHistoricalQueries(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			_, s, original, source, scope, _ := manualInputsFixture(t, side)
			b := &validationBackend{}
			h := admissionHTTP(t, s, original, scope, b)
			r := admissionRecord(t, call(h, "POST", "/v1/trades", "one-post", encode(source.Request)), 202)
			if r.Duplicate || r.Admission.Status != "ADMITTED_NOT_SIGNED_NOT_SUBMITTED" || r.Validation.Output.GasTipCap != "0" || r.Admission.ReservationState != "reserved" || b.resolves.Load() != 1 || b.completes.Load() != 1 {
				t.Fatal("first HTTP not one bounded admission", r)
			}
			assertAtomicAdmission(t, s, r, 1)
			b.fault = "resolve_failure"
			duplicate := admissionRecord(t, call(h, "POST", "/v1/trades", "one-post", encode(source.Request)), 202)
			if !duplicate.Duplicate || duplicate.Admission.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" || duplicate.Admission.ContractHash != r.Admission.ContractHash || b.resolves.Load() != 1 || b.completes.Load() != 1 {
				t.Fatal("duplicate requoted or granted permission", duplicate)
			}
			for _, path := range []string{"/v1/trades/" + r.RequestID, "/v1/trades/by-idempotency-key"} {
				historical := admissionRecord(t, call(h, "GET", path, "one-post", ""), 200)
				if historical.Admission.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" || historical.Admission.ContractHash != r.Admission.ContractHash {
					t.Fatal("GET renewed authority")
				}
			}
			assertAtomicAdmission(t, s, r, 1)
		})
	}
}

func TestHTTPC3CDefaultOffAndConstructorScopeDetached(t *testing.T) {
	_, s, original, source, scope, _ := manualInputsFixture(t, "buy")
	options := Options{ManualIntake: true, ValidationBackend: &validationBackend{}, ValidationPolicyVersion: 1}
	disabled, err := NewWithOptions(s, original.identity, testCredential, original.policy, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	disabled.now, disabled.validator.now = original.now, original.now
	w := call(disabled, "POST", "/v1/trades", "default-off", encode(source.Request))
	var r Record
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &r) != nil || r.Admission != nil || count(t, s.db, "manual_admission_commands") != 0 {
		t.Fatal("default enabled admission", w.Body.String())
	}
	for _, invalid := range []Options{
		{ManualAdmission: true},
		{ManualIntake: true, ManualAdmission: true, AdmissionScope: httpAdmissionScope(scope)},
		{ManualIntake: true, ValidationBackend: &validationBackend{}, ValidationPolicyVersion: 1, ManualAdmission: true},
		{ManualIntake: true, ValidationBackend: &validationBackend{}, ValidationPolicyVersion: 1, AdmissionScope: httpAdmissionScope(scope)},
	} {
		if _, err = NewWithOptions(s, original.identity, testCredential, original.policy, nil, invalid); err == nil {
			t.Fatal("incomplete admission config accepted")
		}
	}
	options.ManualAdmission, options.AdmissionScope = true, httpAdmissionScope(scope)
	h, err := NewWithOptions(s, original.identity, testCredential, original.policy, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	h.now, h.validator.now = original.now, original.now
	options.AdmissionScope.DeploymentID = "changed-after-construction"
	result := admissionRecord(t, call(h, "POST", "/v1/trades", "b2-inputs", encode(source.Request)), 202)
	if result.Admission.Status != "ADMITTED_NOT_SIGNED_NOT_SUBMITTED" {
		t.Fatal("constructor retained mutable caller scope")
	}
	cap := call(h, "GET", "/v1/capabilities", "", "")
	for _, text := range []string{`"manual_order_admission":"DURABLE_ADMISSION_NOT_EXECUTION_OR_SEND_PERMISSION"`, `"send_authorized":false`, `"live":false`, `"submission_enabled":false`, `"events_scope":"MANUAL_ORDER_INTAKE_ONLY"`} {
		if cap.Code != 200 || !strings.Contains(cap.Body.String(), text) {
			t.Fatal("capability overclaim", cap.Body.String())
		}
	}
}

func TestHTTPC3CPrivateConfigStrictScopeAndNoAutomaticGrant(t *testing.T) {
	d, s, original, source, scope, _ := manualInputsFixture(t, "buy")
	path := filepath.Join(t.TempDir(), "api.json")
	base := `{"principal_id":"caller-01","wallet_id":"wallet-01","wallet_address":"` + testWallet + `","token":"` + testCredential + `","manual_admission":`
	good := encode(httpAdmissionScope(scope))
	options := Options{ManualIntake: true, ValidationBackend: &validationBackend{}, ValidationPolicyVersion: 1, ManualAdmission: true}
	// A declared authorization in a disposable fixture can be revoked, never
	// rearmed by a config file/constructor. Constructor does not require a grant.
	if _, err := s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []string{
		strings.Replace(good, `"authorization_epoch":1`, `"authorization_epoch":1,"authorization_epoch":1`, 1),
		strings.Replace(good, `"authorization_epoch":1`, `"Authorization_epoch":1`, 1),
		strings.Replace(good, `"authorization_epoch":1`, `"authorization_epoch":9223372036854775808`, 1),
		strings.Replace(good, `"authorization_epoch":1`, `"authorization_epoch":1e0`, 1),
		strings.Replace(good, `"authorization_epoch":1`, `"authorization_epoch":"1"`, 1),
		strings.Replace(good, `"authorization_epoch":1`, `"authorization_epoch":null`, 1),
		strings.Replace(good, `"deployment_id":"fixture-deployment"`, `"deployment_id":""`, 1),
		strings.Replace(good, `"deployment_id":"fixture-deployment"`, `"deployment_id":"fixture-deployment","live":true`, 1),
		`null`, `{}`, `[]`,
	} {
		if err := os.WriteFile(path, []byte(base+malformed+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFileWithOptions(d, path, "different-internal-token", nil, options); err == nil {
			t.Fatal("malformed scope accepted", malformed)
		}
	}
	if err := os.WriteFile(path, []byte(base+good+`}`), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := LoadFileWithOptions(d, path, "different-internal-token", nil, options)
	if err != nil || h.admissionScope == nil || *h.admissionScope != scope {
		t.Fatal("valid scope configuration rejected", err)
	}
	h.now, h.validator.now = original.now, original.now
	if w := call(h, "POST", "/v1/trades", "b2-inputs", encode(source.Request)); w.Code == 202 {
		t.Fatal("config rearmed revoked authorization")
	}
	if _, err := LoadFileWithOptions(d, path, "different-internal-token", nil, Options{}); err == nil {
		t.Fatal("scope accepted while disabled")
	}
	var state string
	if err := s.db.QueryRow(`SELECT state FROM canary_runtime_authorizations`).Scan(&state); err != nil || state != "REVOKED" || count(t, s.db, "manual_admission_commands") != 0 {
		t.Fatal("configuration created authority", state, err)
	}
}

func TestHTTPC3CStopRevokeExpiryAndDeploymentDenyWithoutRenewal(t *testing.T) {
	for _, fault := range []string{"stop", "revoke", "expiry", "auth_missing", "deployment", "epoch", "clock"} {
		t.Run(fault, func(t *testing.T) {
			_, s, original, source, scope, _ := manualInputsFixture(t, "buy")
			b := &validationBackend{}
			h := admissionHTTP(t, s, original, scope, b)
			switch fault {
			case "stop":
				_, _ = s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`)
			case "revoke":
				_, _ = s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`)
			case "expiry":
				e, _ := time.Parse(time.RFC3339Nano, source.ExpiresAt)
				h.now = func() time.Time { return e }
			case "auth_missing":
				h.admissionScope.AuthorizationID = "missing-auth"
			case "deployment":
				h.admissionScope.DeploymentID = "another-deployment"
			case "epoch":
				h.admissionScope.AuthorizationEpoch++
			case "clock":
				h.now = func() time.Time { return time.Time{} }
			}
			w := call(h, "POST", "/v1/trades", "b2-inputs", encode(source.Request))
			if w.Code == 202 || strings.Contains(w.Body.String(), `"admission":`) || !strings.Contains(w.Header().Get("Location"), source.RequestID) || b.resolves.Load() != 0 || b.completes.Load() != 0 {
				t.Fatal("invalid controls gave admission or requote", w.Code, w.Body.String())
			}
			if fault == "stop" && (w.Code != 503 || !strings.Contains(w.Body.String(), "MANUAL_ADMISSION_UNAVAILABLE")) {
				t.Fatal("stop not highest priority", w.Body.String())
			}
			assertAtomicAdmission(t, s, source, 0)
			get := call(h, "GET", "/v1/trades/"+source.RequestID, "", "")
			if get.Code != 200 || !strings.Contains(get.Body.String(), source.ExpiresAt) {
				t.Fatal("failed admission erased history")
			}
		})
	}
}

func TestHTTPC3CHistorySurvivesRealReopenRevokeExpiryAndDisabledComposition(t *testing.T) {
	d, s, original, source, scope, path := manualInputsFixture(t, "buy")
	h := admissionHTTP(t, s, original, scope, &validationBackend{})
	r := admissionRecord(t, call(h, "POST", "/v1/trades", "b2-inputs", encode(source.Request)), 202)
	if _, err := s.db.Exec(`UPDATE canary_risk_reservations SET state='frozen'; UPDATE canary_runtime_authorizations SET state='REVOKED'; UPDATE canary_control_state SET emergency_stopped=1`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	store, err := NewStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := New(store, original.identity, testCredential, original.policy)
	if err != nil {
		t.Fatal(err)
	}
	disabled.now = func() time.Time { t.Fatal("history consulted expiry clock"); return time.Time{} }
	got := admissionRecord(t, call(disabled, "GET", "/v1/trades/"+r.RequestID, "", ""), 200)
	if got.Admission.ReservationState != "frozen" || got.Admission.ContractHash != r.Admission.ContractHash || got.ExpiresAt != r.ExpiresAt || got.Admission.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" {
		t.Fatal("reopen/disabled/stop erased freeze/history")
	}
	disabledDuplicate := admissionRecord(t, call(disabled, "POST", "/v1/trades", "b2-inputs", encode(source.Request)), 202)
	if disabledDuplicate.Admission.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" || !disabledDuplicate.Duplicate {
		t.Fatal("disabled duplicate concealed history")
	}
	validationOnly, err := NewWithOptions(store, original.identity, testCredential, original.policy, nil, Options{ManualIntake: true, ValidationBackend: &validationBackend{fault: "resolve_failure"}, ValidationPolicyVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	validationOnly.now = disabled.now
	validationOnly.validator.now = disabled.now
	if observed := admissionRecord(t, call(validationOnly, "POST", "/v1/trades", "b2-inputs", encode(source.Request)), 202); observed.Admission.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" {
		t.Fatal("validation-only composition renewed permission")
	}
	restarted := admissionHTTP(t, store, original, scope, &validationBackend{fault: "resolve_failure"})
	restarted.now = func() time.Time { return testNow.Add(2 * time.Hour) }
	if w := call(restarted, "POST", "/v1/trades", "b2-inputs", encode(source.Request)); w.Code != 503 {
		t.Fatal("stop did not block admission command")
	}
	if _, err = store.db.Exec(`UPDATE canary_control_state SET emergency_stopped=0`); err != nil {
		t.Fatal(err)
	}
	dup := admissionRecord(t, call(restarted, "POST", "/v1/trades", "b2-inputs", encode(source.Request)), 202)
	if !dup.Duplicate || dup.Admission.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" || dup.ExpiresAt != r.ExpiresAt {
		t.Fatal("restart/revoke/expiry renewed TTL or permission")
	}
	assertAtomicAdmission(t, store, r, 1)
}

func TestHTTPC3CStopOrRevokeAfterValidationBeforeAdmission(t *testing.T) {
	for _, change := range []string{`UPDATE canary_control_state SET emergency_stopped=1`, `UPDATE canary_runtime_authorizations SET state='REVOKED'`} {
		t.Run(change, func(t *testing.T) {
			_, s, original, source, scope, _ := manualInputsFixture(t, "buy")
			b := &validationBackend{during: func() {
				if _, err := s.db.Exec(change); err != nil {
					t.Fatal(err)
				}
			}}
			h := admissionHTTP(t, s, original, scope, b)
			w := call(h, "POST", "/v1/trades", "control-window", encode(source.Request))
			if w.Code == 202 || b.resolves.Load() != 1 || b.completes.Load() != 1 || count(t, s.db, "manual_trade_validations") != 2 {
				t.Fatal("late stop/revoke not independently enforced", w.Body.String())
			}
			assertAtomicAdmission(t, s, source, 0)
		})
	}
}

func TestHTTPC3CHistoricalScopeAndCorruptUsageFailClosed(t *testing.T) {
	_, s, original, source, scope, _ := manualInputsFixture(t, "buy")
	h := admissionHTTP(t, s, original, scope, &validationBackend{})
	r := admissionRecord(t, call(h, "POST", "/v1/trades", "b2-inputs", encode(source.Request)), 202)
	wrong := original.identity
	wrong.PrincipalID = "outside-caller"
	outside, err := New(s, wrong, testCredential, original.policy)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(outside, "GET", "/v1/trades/"+r.RequestID, "", ""); w.Code != 404 || strings.Contains(w.Body.String(), r.Admission.ContractHash) {
		t.Fatal("cross-principal admission leaked")
	}
	h.admissionScope.DeploymentID = "drifted"
	if w := call(h, "POST", "/v1/trades", "b2-inputs", encode(source.Request)); w.Code != 409 {
		t.Fatal("duplicate deployment drift did not conflict", w.Body.String())
	}
	// Explicit corruption fixture only: remove schema guard to prove projection
	// refuses corrupt history rather than hiding it or resetting lifetime usage.
	if _, err = s.db.Exec(`DROP TRIGGER manual_b1_no_usage_update; UPDATE canary_authorization_usage SET total_input='0'`); err != nil {
		t.Fatal(err)
	}
	if w := call(h, "GET", "/v1/trades/"+r.RequestID, "", ""); w.Code == 200 || strings.Contains(w.Body.String(), r.Admission.ContractHash) {
		t.Fatal("corrupt history returned as healthy")
	}
	var total string
	if err = s.db.QueryRow(`SELECT total_input FROM canary_authorization_usage`).Scan(&total); err != nil || total != "0" {
		t.Fatal("read silently repaired budget")
	}
}

func TestHTTPC3CUnavailableDatabaseAndCanceledRequestFailClosed(t *testing.T) {
	d, s, original, source, scope, _ := manualInputsFixture(t, "buy")
	h := admissionHTTP(t, s, original, scope, &validationBackend{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("POST", "/v1/trades", strings.NewReader(encode(source.Request))).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+testCredential)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "b2-inputs")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 503 || strings.Contains(w.Body.String(), `"admission":`) {
		t.Fatal("canceled request accepted")
	}
	assertAtomicAdmission(t, s, source, 0)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST"} {
		path := "/v1/trades"
		if method == "GET" {
			path += "/" + source.RequestID
		}
		if w = call(h, method, path, "b2-inputs", encode(source.Request)); w.Code != 503 {
			t.Fatal("closed database gave success", method, w.Body.String())
		}
	}
}

func TestHTTPC3CAuthenticationAndSecurityFieldInjectionNeverReachAdmission(t *testing.T) {
	_, s, original, source, scope, _ := manualInputsFixture(t, "buy")
	h := admissionHTTP(t, s, original, scope, &validationBackend{})
	unauthenticated := httptest.NewRequest("POST", "/v1/trades", strings.NewReader(encode(source.Request)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, unauthenticated)
	if w.Code != 401 {
		t.Fatal("missing credential reached admission")
	}
	for _, field := range []string{`"authorization_id":"b2-auth"`, `"authorization_epoch":1`, `"policy_version":1`, `"build_sha":"fake"`, `"raw_tx":"0x01"`, `"expires_at":"2100-01-01T00:00:00Z"`, `"live":true`, `"manual_admission":{}`} {
		body := strings.TrimSuffix(encode(source.Request), "}") + "," + field + "}"
		if w = call(h, "POST", "/v1/trades", "inject", body); w.Code != 400 {
			t.Fatal("user-controlled security field accepted", field, w.Body.String())
		}
	}
	other := source.Request
	other.WalletID = "another-wallet"
	if w = call(h, "POST", "/v1/trades", "outside", encode(other)); w.Code != 403 {
		t.Fatal("wallet scope bypass")
	}
	for _, path := range []string{"/v1/trades/admit", "/v1/trades/replay", "/v1/send", "/v1/trades?authorization_id=b2-auth"} {
		if w = call(h, "POST", path, "bypass", encode(source.Request)); w.Code == 202 {
			t.Fatal("new bypass endpoint")
		}
	}
	assertAtomicAdmission(t, s, source, 0)
}

func TestHTTPC3CChangedRequestConflictsAndHistoricalRejectionNotReactivated(t *testing.T) {
	_, s, original, source, scope, _ := manualInputsFixture(t, "buy")
	h := admissionHTTP(t, s, original, scope, &validationBackend{})
	r := admissionRecord(t, call(h, "POST", "/v1/trades", "b2-inputs", encode(source.Request)), 202)
	changed := source.Request
	ttl := uint64(31)
	changed.TTLSeconds = &ttl
	if w := call(h, "POST", "/v1/trades", "b2-inputs", encode(changed)); w.Code != 409 {
		t.Fatal("different TTL did not conflict")
	}
	assertAtomicAdmission(t, s, r, 1)
	_, rejectedStore, rejectedOriginal, _ := fixture(t)
	rejected := record(t, call(rejectedOriginal, "POST", "/v1/trades", "never-reactivate", encode(buy())))
	validationSeed(t, rejectedStore, validationPolicy{})
	enabled := admissionHTTP(t, rejectedStore, rejectedOriginal, scope, &validationBackend{})
	w := call(enabled, "POST", "/v1/trades", "never-reactivate", encode(buy()))
	if w.Code != 503 || !strings.Contains(w.Body.String(), rejected.RequestID) || count(t, rejectedStore.db, "manual_http_order_sources") != 0 || count(t, rejectedStore.db, "manual_admission_commands") != 0 {
		t.Fatal("old rejected identity reactivated", w.Body.String())
	}
}

func TestHTTPC3CAuditFaultRollsBackAdmissionButRetainsIntakeValidation(t *testing.T) {
	for _, table := range []string{"canary_runtime_audit", "canary_alert_outbox"} {
		t.Run(table, func(t *testing.T) {
			_, s, original, source, scope, _ := manualInputsFixture(t, "buy")
			h := admissionHTTP(t, s, original, scope, &validationBackend{})
			if _, err := s.db.Exec(`CREATE TRIGGER c3c_fault BEFORE INSERT ON ` + table + ` WHEN NEW.id LIKE 'manual-admission%' BEGIN SELECT RAISE(ABORT,'secret internal fault'); END`); err != nil {
				t.Fatal(err)
			}
			w := call(h, "POST", "/v1/trades", "fault-post", encode(source.Request))
			if w.Code != 503 || strings.Contains(w.Body.String(), "secret internal fault") || w.Header().Get("Location") == "" {
				t.Fatal("fault escaped safely bounded API", w.Body.String())
			}
			assertAtomicAdmission(t, s, source, 0)
			if count(t, s.db, "manual_trade_validations") != 2 || count(t, s.db, "manual_http_order_sources") != 2 {
				t.Fatal("falsely rolled back previously durable phases")
			}
			if _, err := s.db.Exec(`DROP TRIGGER c3c_fault`); err != nil {
				t.Fatal(err)
			}
			r := admissionRecord(t, call(h, "POST", "/v1/trades", "fault-post", encode(source.Request)), 202)
			assertAtomicAdmission(t, s, r, 1)
		})
	}
}

type lostAdmissionResponse struct {
	beforeWrite func()
	headers     http.Header
}

func (w *lostAdmissionResponse) Header() http.Header { return w.headers }
func (w *lostAdmissionResponse) WriteHeader(int) {
	if w.beforeWrite != nil {
		w.beforeWrite()
		w.beforeWrite = nil
	}
}
func (w *lostAdmissionResponse) Write([]byte) (int, error) {
	return 0, errors.New("client disconnected")
}

func TestHTTPC3CResponseLossAfterCommitExactRetryNoSecondConsumption(t *testing.T) {
	_, s, original, source, scope, _ := manualInputsFixture(t, "buy")
	b := &validationBackend{}
	h := admissionHTTP(t, s, original, scope, b)
	req := httptest.NewRequest("POST", "/v1/trades", strings.NewReader(encode(source.Request)))
	req.Header.Set("Authorization", "Bearer "+testCredential)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "b2-inputs")
	w := &lostAdmissionResponse{headers: http.Header{}, beforeWrite: func() { assertAtomicAdmission(t, s, source, 1) }}
	h.ServeHTTP(w, req)
	got := admissionRecord(t, call(h, "POST", "/v1/trades", "b2-inputs", encode(source.Request)), 202)
	if got.Admission.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" || !got.Duplicate || b.resolves.Load() != 0 || b.completes.Load() != 0 {
		t.Fatal("lost response regenerated work")
	}
	assertAtomicAdmission(t, s, got, 1)
}

func TestHTTPC3CConcurrentTwoHandleHTTPDuplicatesOneAdmission(t *testing.T) {
	_, s, original, source, scope, path := manualInputsFixture(t, "buy")
	fresh, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	store, err := NewStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	backends := []*validationBackend{{}, {}}
	handlers := []*API{admissionHTTP(t, s, original, scope, backends[0]), admissionHTTP(t, store, original, scope, backends[1])}
	responses := make(chan *httptest.ResponseRecorder, 24)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			responses <- call(handlers[i%2], "POST", "/v1/trades", "b2-inputs", encode(source.Request))
		}(i)
	}
	wg.Wait()
	close(responses)
	first, historical := 0, 0
	for response := range responses {
		r := admissionRecord(t, response, 202)
		if r.Admission.Status == "ADMITTED_NOT_SIGNED_NOT_SUBMITTED" {
			first++
		} else if r.Admission.Status == "HISTORICAL_ADMISSION_NOT_PERMISSION" {
			historical++
		} else {
			t.Fatal("unexplained HTTP accept", response.Body.String())
		}
	}
	if first != 1 || historical != 23 || backends[0].resolves.Load()+backends[1].resolves.Load()+backends[0].completes.Load()+backends[1].completes.Load() != 0 {
		t.Fatal("HTTP duplicates overconsumed/revalidated", first, historical)
	}
	assertAtomicAdmission(t, s, source, 1)
}

func TestHTTPC3CConcurrentFreshValidationAdmissionHandoff(t *testing.T) {
	_, s, original, source, scope, path := manualInputsFixture(t, "buy")
	fresh, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	store, err := NewStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}, 2), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	backend := func() *validationBackend {
		return &validationBackend{during: func() { entered <- struct{}{}; <-release }}
	}
	handlers := []*API{admissionHTTP(t, s, original, scope, backend()), admissionHTTP(t, store, original, scope, backend())}
	responses := make(chan *httptest.ResponseRecorder, 2)
	for _, handler := range handlers {
		go func(handler *API) {
			responses <- call(handler, "POST", "/v1/trades", "fresh-handoff", encode(source.Request))
		}(handler)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("validation handoff barrier not reached")
		}
	}
	once.Do(func() { close(release) })
	first, history := 0, 0
	var admitted Record
	for i := 0; i < 2; i++ {
		admitted = admissionRecord(t, <-responses, 202)
		if admitted.Admission.Status == "ADMITTED_NOT_SIGNED_NOT_SUBMITTED" {
			first++
		} else if admitted.Admission.Status == "HISTORICAL_ADMISSION_NOT_PERMISSION" {
			history++
		}
	}
	if first != 1 || history != 1 {
		t.Fatal("fresh validation/admission handoff lost idempotency", first, history)
	}
	assertAtomicAdmission(t, s, admitted, 1)
	if count(t, s.db, "manual_trade_validations") != 2 {
		t.Fatal("second validation result minted")
	}
}

func TestHTTPC3CNoGrantPermitOrSigningDependencyAndNoArmGuardRetained(t *testing.T) {
	_, s, original, source, scope, _ := manualInputsFixture(t, "buy")
	// Restore the exact fixture-only dropped production guard. HTTP must NOT
	// remove it. With a revoked authorization, HTTP cannot arm it again.
	if _, err := s.db.Exec(`CREATE TRIGGER manual_b1_no_arm BEFORE UPDATE OF state ON canary_runtime_authorizations WHEN NEW.state='ARMED' AND EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.id) BEGIN SELECT RAISE(ABORT,'manual authorization arming not implemented'); END;
 UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
		t.Fatal(err)
	}
	h := admissionHTTP(t, s, original, scope, &validationBackend{})
	if w := call(h, "POST", "/v1/trades", "b2-inputs", encode(source.Request)); w.Code == 202 {
		t.Fatal("HTTP granted real authority")
	}
	assertAtomicAdmission(t, s, source, 0)
	for _, name := range []string{"manual_b1_no_arm", "manual_b1_no_permit", "manual_b1_no_pass_snapshot"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&n); err != nil || n != 1 {
			t.Fatal("production isolation guard lost", name, err)
		}
	}
	for _, file := range []string{"http.go", "manual_admission_http.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range f.Imports {
			path := strings.Trim(im.Path.Value, `"`)
			if strings.HasSuffix(path, "/trade") || strings.Contains(path, "signer") {
				t.Fatal("economic dependency", path)
			}
		}
		ast.Inspect(f, func(node ast.Node) bool {
			if call, ok := node.(*ast.SelectorExpr); ok {
				for _, bad := range []string{"Submit", "ReplayUnknown", "SendRawTransaction", "Sign", "ReserveNonce", "IssueCanarySendPermit", "ConsumeCanaryPermitAfterGate"} {
					if call.Sel.Name == bad {
						t.Fatal("prohibited call", bad)
					}
				}
			}
			return true
		})
	}
}
