package tradeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func intakeAPI(t *testing.T, s *Store, original *API) *API {
	t.Helper()
	h, err := NewWithOptions(s, original.identity, testCredential, original.policy, original.queries, Options{ManualIntake: true})
	if err != nil {
		t.Fatal(err)
	}
	h.now = original.now
	return h
}

func manualRecord(t *testing.T, w *httptest.ResponseRecorder) Record {
	t.Helper()
	var r Record
	if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &r) != nil || r.Status != "RECORDED" || r.Reason != "VALIDATION_NOT_CONNECTED" || r.OperationID == nil || r.SendAuthorized || r.SubmissionQueued || r.ContractDeadline || r.DeadlineCapability != "APPLICATION_TTL_ONLY" {
		t.Fatalf("invalid intake response %d: %s", w.Code, w.Body.String())
	}
	return r
}

func noIntakeEconomicWork(t *testing.T, s *Store) {
	t.Helper()
	for _, table := range []string{"dry_run_results", "transaction_attempts", "transaction_submissions", "execution_wallet_lanes", "execution_reservations", "canary_risk_reservations", "canary_admission_sources", "canary_admission_decisions", "canary_runtime_authorizations", "canary_send_permits", "canary_authorization_operation_usage"} {
		if count(t, s.db, table) != 0 {
			t.Fatalf("intake created economic work: %s", table)
		}
	}
}

func TestHTTPC1ManualIntakeContractAndQuery(t *testing.T) {
	_, s, original, _ := fixture(t)
	h := intakeAPI(t, s, original)
	r := manualRecord(t, call(h, "POST", "/v1/trades", "manual", encode(buy())))
	if r.ExpiresAt != testNow.Add(30*time.Second).Format(time.RFC3339Nano) || r.Request.MinReceive != "" || r.Request.Fee.MaxTotalNative != "" {
		t.Fatal("expiry/defaults changed")
	}
	var state, kind, source, raw string
	var policy any
	if err := s.db.QueryRow(`SELECT o.status,s.kind,m.source,o.request_json,o.policy_version FROM operations o JOIN execution_steps s ON s.operation_id=o.id JOIN manual_http_order_sources m ON m.operation_id=o.id`).Scan(&state, &kind, &source, &raw, &policy); err != nil {
		t.Fatal(err)
	}
	if state != "manual_validation_pending" || kind != "manual_trade_validation" || source != "MANUAL" || raw != encode(r.Request) || policy != nil || strings.Contains(raw, "source_event") || strings.Contains(raw, "copy_buy") {
		t.Fatal("fabricated copy source or execution policy")
	}
	for _, path := range []string{"/v1/trades/" + r.RequestID, "/v1/trades/by-idempotency-key", "/v1/trades/" + r.RequestID + "/events"} {
		w := call(h, "GET", path, "manual", "")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), r.RequestID) && !strings.Contains(w.Body.String(), "MANUAL_HTTP_ORDER_RECORDED") {
			t.Fatal(w.Body.String())
		}
	}
	if w := call(h, "GET", "/v1/trades/"+r.RequestID+"/events?after=1", "", ""); !strings.Contains(w.Body.String(), `"events":[]`) {
		t.Fatal("duplicate audit pagination")
	}
	if w := call(h, "GET", "/v1/capabilities", "", ""); !strings.Contains(w.Body.String(), `"request_handling":"MANUAL_RECORD_ONLY"`) || !strings.Contains(w.Body.String(), `"submission_enabled":false`) {
		t.Fatal("capabilities overstates execution")
	}
	if count(t, s.db, "manual_http_order_sources") != 1 || count(t, s.db, "operations") != 1 || count(t, s.db, "execution_steps") != 1 || count(t, s.db, "canary_runtime_audit") != 1 || count(t, s.db, "canary_alert_outbox") != 1 {
		t.Fatal("source/operation/step/audit/outbox not exactly once")
	}
	noIntakeEconomicWork(t, s)
}

func TestHTTPC1LegacyRejectionAndToggleNeverReactivate(t *testing.T) {
	_, s, disabled, _ := fixture(t)
	old := record(t, call(disabled, "POST", "/v1/trades", "legacy", encode(buy())))
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO user_http_trade_requests SELECT * FROM user_http_trade_requests`); err == nil {
		t.Fatal("SQL REPLACE rewrote legacy rejection")
	}
	enabled := intakeAPI(t, s, disabled)
	dup := record(t, call(enabled, "POST", "/v1/trades", "legacy", encode(buy())))
	if !dup.Duplicate || dup.ExpiresAt != old.ExpiresAt || count(t, s.db, "operations") != 0 {
		t.Fatal("old rejection reactivated")
	}
	newRecord := manualRecord(t, call(enabled, "POST", "/v1/trades", "new", encode(buy())))
	disabled.now = func() time.Time { t.Fatal("duplicate minted new clock/TTL"); return time.Time{} }
	dup = manualRecord(t, call(disabled, "POST", "/v1/trades", "new", encode(buy())))
	if !dup.Duplicate || dup.ExpiresAt != newRecord.ExpiresAt || *dup.OperationID != *newRecord.OperationID || count(t, s.db, "user_http_trade_requests") != 1 {
		t.Fatal("disabled toggle changed prior intake")
	}
	changed := buy()
	changed.Amount = "0.02"
	for _, handler := range []*API{enabled, disabled} {
		for _, key := range []string{"legacy", "new"} {
			if w := call(handler, "POST", "/v1/trades", key, encode(changed)); w.Code != http.StatusConflict {
				t.Fatal("cross-mode conflict lost")
			}
		}
	}
	noIntakeEconomicWork(t, s)
}

func TestHTTPC1RestartExpiryPolicyAndConflict(t *testing.T) {
	d, s, disabled, path := fixture(t)
	h := intakeAPI(t, s, disabled)
	first := manualRecord(t, call(h, "POST", "/v1/trades", "restart", encode(buy())))
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	freshDB, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer freshDB.Close()
	if err = freshDB.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	freshStore, err := NewStore(freshDB)
	if err != nil {
		t.Fatal(err)
	}
	fresh := intakeAPI(t, freshStore, disabled)
	fresh.policy.Version = 2
	fresh.policy.DefaultTTLSeconds = 1
	fresh.policy.MaxFeeGwei = "0.01"
	for _, clock := range []time.Time{testNow.Add(30 * time.Second), testNow.Add(24 * time.Hour)} {
		fresh.now = func() time.Time { return clock }
		dup := manualRecord(t, call(fresh, "POST", "/v1/trades", "restart", encode(buy())))
		if !dup.Duplicate || dup.ExpiresAt != first.ExpiresAt || dup.APIPolicyHash != first.APIPolicyHash || *dup.OperationID != *first.OperationID {
			t.Fatal("restart extended expiry/rebound policy")
		}
	}
	for _, change := range []func(*Request){
		func(r *Request) { n := uint64(30); r.TTLSeconds = &n },
		func(r *Request) { r.Fee.MaxGwei = "2" },
		func(r *Request) { r.MinReceive = "1" },
		func(r *Request) { r.Fee.MaxTotalNative = "0.01" },
		func(r *Request) { r.SlippagePercent = "2" },
	} {
		r := buy()
		change(&r)
		if w := call(fresh, "POST", "/v1/trades", "restart", encode(r)); w.Code != http.StatusConflict {
			t.Fatal("changed trading fields accepted")
		}
	}
	if count(t, freshStore.db, "operations") != 1 || count(t, freshStore.db, "canary_runtime_audit") != 1 {
		t.Fatal("restart duplicate work")
	}
	noIntakeEconomicWork(t, freshStore)
}

func TestHTTPC1AtomicFaultsAndSchemaIdentity(t *testing.T) {
	for _, table := range []string{"operations", "execution_steps", "manual_http_order_sources", "canary_runtime_audit", "canary_alert_outbox"} {
		t.Run(table, func(t *testing.T) {
			_, s, original, _ := fixture(t)
			h := intakeAPI(t, s, original)
			if _, err := s.db.Exec(`CREATE TRIGGER forced_failure BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'test fault'); END`); err != nil {
				t.Fatal(err)
			}
			if w := call(h, "POST", "/v1/trades", "fault", encode(buy())); w.Code != http.StatusServiceUnavailable {
				t.Fatal(w.Body.String())
			}
			for _, name := range []string{"operations", "execution_steps", "manual_http_order_sources", "canary_runtime_audit", "canary_alert_outbox"} {
				if count(t, s.db, name) != 0 {
					t.Fatal("partial commit: " + name)
				}
			}
			if _, err := s.db.Exec(`DROP TRIGGER forced_failure`); err != nil {
				t.Fatal(err)
			}
			manualRecord(t, call(h, "POST", "/v1/trades", "fault", encode(buy())))
		})
	}
	_, s, original, _ := fixture(t)
	h := intakeAPI(t, s, original)
	r := manualRecord(t, call(h, "POST", "/v1/trades", "immutable", encode(buy())))
	for _, query := range []string{
		`UPDATE manual_http_order_sources SET expires_at='2099-01-01T00:00:00Z'`,
		`UPDATE manual_http_order_sources SET request_json='{}'`,
		`DELETE FROM manual_http_order_sources`,
		`UPDATE operations SET expires_at='2099-01-01T00:00:00Z'`,
		`UPDATE operations SET request_json='{}'`,
		`UPDATE execution_steps SET expires_at='2099-01-01T00:00:00Z'`,
		`DELETE FROM operations`,
		`DELETE FROM execution_steps`,
		`INSERT OR REPLACE INTO manual_http_order_sources SELECT * FROM manual_http_order_sources`,
		`INSERT OR REPLACE INTO operations SELECT * FROM operations`,
		`INSERT OR REPLACE INTO execution_steps SELECT * FROM execution_steps`,
		`UPDATE manual_http_order_sources SET source='COPY'`,
		`INSERT INTO user_http_trade_requests(id,principal_id,idempotency_key,chain,wallet_id,wallet_address,source,request_fingerprint,request_json,api_policy_version,api_policy_hash,api_policy_json,expires_at,created_at,outcome,reason_code,deadline_capability,contract_deadline,send_authorized,submission_queued) SELECT id,principal_id,idempotency_key,chain,wallet_id,wallet_address,source,request_fingerprint,request_json,api_policy_version,api_policy_hash,api_policy_json,expires_at,created_at,'REJECTED','SUBMISSION_DISABLED',deadline_capability,0,0,0 FROM manual_http_order_sources`,
	} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatal("SQL bypass: " + query)
		}
	}
	dup := manualRecord(t, call(h, "POST", "/v1/trades", "immutable", encode(buy())))
	if dup.ExpiresAt != r.ExpiresAt {
		t.Fatal("mutated expiry")
	}
	noIntakeEconomicWork(t, s)
}

func TestHTTPC1ConcurrentIntakeAcrossHandles(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "enabled", true: "mixed_toggle"}[mixed], func(t *testing.T) {
			_, s, original, path := fixture(t)
			d2, err := storage.Open(context.Background(), storage.TradeOwner, path)
			if err != nil {
				t.Fatal(err)
			}
			defer d2.Close()
			s2, err := NewStore(d2)
			if err != nil {
				t.Fatal(err)
			}
			handlers := []*API{intakeAPI(t, s, original), intakeAPI(t, s2, original)}
			if mixed {
				handlers[1] = original
			}
			const n = 24
			var wg sync.WaitGroup
			responses := make(chan *httptest.ResponseRecorder, n)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					responses <- call(handlers[i%2], "POST", "/v1/trades", "race", encode(buy()))
				}(i)
			}
			wg.Wait()
			close(responses)
			created := 0
			var first Record
			for w := range responses {
				var r Record
				if w.Code == http.StatusAccepted {
					r = manualRecord(t, w)
				} else {
					r = record(t, w)
				}
				if !r.Duplicate {
					created++
				}
				if first.RequestID == "" {
					first = r
				}
				if first.Status != r.Status || first.ExpiresAt != r.ExpiresAt {
					t.Fatal("cross-mode duplicate diverged")
				}
			}
			if created != 1 || count(t, s.db, "manual_http_order_sources")+count(t, s.db, "user_http_trade_requests") != 1 || count(t, s.db, "canary_runtime_audit") != 1 || count(t, s.db, "canary_alert_outbox") != 1 {
				t.Fatal("concurrency overcreation")
			}
			want := 0
			if first.OperationID != nil {
				want = 1
			}
			if count(t, s.db, "operations") != want || count(t, s.db, "execution_steps") != want {
				t.Fatal("unexplained operation")
			}
			noIntakeEconomicWork(t, s)
		})
	}
}

func TestHTTPC1IntakeNeverQueriesAndStrictAdmission(t *testing.T) {
	s, original, q := queryAPI(t)
	h := intakeAPI(t, s, original)
	for i, r := range []Request{
		buy(),
		{Chain: "robinhood", WalletID: "wallet-01", Token: testToken, Side: "sell", Amount: "1500.5", SlippagePercent: "3"},
		{Chain: "robinhood", WalletID: "wallet-01", Token: testToken, Side: "sell", SellPercent: "50", SlippagePercent: "3"},
	} {
		manualRecord(t, call(h, "POST", "/v1/trades", []string{"buy", "sell", "percent"}[i], encode(r)))
	}
	if q.calls != 0 {
		t.Fatal("intake resolved balance/quote prematurely")
	}
	noIntakeEconomicWork(t, s)
	if _, err := s.db.Exec(`UPDATE dry_run_wallets SET enabled=0`); err != nil {
		t.Fatal(err)
	}
	if w := call(h, "POST", "/v1/trades", "disabled-wallet", encode(buy())); w.Code != http.StatusForbidden {
		t.Fatal(w.Body.String())
	}
	h.now = func() time.Time { return time.Time{} }
	if _, err := s.db.Exec(`UPDATE dry_run_wallets SET enabled=1`); err != nil {
		t.Fatal(err)
	}
	if w := call(h, "POST", "/v1/trades", "bad-clock", encode(buy())); w.Code != http.StatusBadRequest {
		t.Fatal(w.Body.String())
	}
	if count(t, s.db, "operations") != 3 {
		t.Fatal("invalid admission created work")
	}
}

func TestHTTPC1EnabledAuthenticationAndTenantIsolation(t *testing.T) {
	_, s, original, _ := fixture(t)
	h := intakeAPI(t, s, original)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/trades", strings.NewReader(encode(buy()))))
	if w.Code != http.StatusUnauthorized || count(t, s.db, "operations") != 0 {
		t.Fatal("unauthenticated intake")
	}
	r := manualRecord(t, call(h, "POST", "/v1/trades", "scoped", encode(buy())))
	otherIdentity := h.identity
	otherIdentity.PrincipalID = "caller-02"
	other, err := NewWithOptions(s, otherIdentity, testCredential, DefaultPolicy(), nil, Options{ManualIntake: true})
	if err != nil {
		t.Fatal(err)
	}
	other.now = h.now
	if w := call(other, "GET", "/v1/trades/"+r.RequestID, "", ""); w.Code != http.StatusNotFound {
		t.Fatal("cross-tenant read")
	}
	r2 := manualRecord(t, call(other, "POST", "/v1/trades", "scoped", encode(buy())))
	if *r.OperationID == *r2.OperationID || r.RequestID == r2.RequestID {
		t.Fatal("tenant namespace collision")
	}
	for _, body := range []string{
		`{"chain":"robinhood","wallet_id":"wallet-01","token":"` + testToken + `","side":"buy","amount":"0.01","slippage_percent":"3","source":"COPY"}`,
		`{"chain":"robinhood","wallet_id":"wallet-01","token":"` + testToken + `","side":"buy","amount":"0.01","slippage_percent":"3","policy_version":1}`,
		`{"chain":"robinhood","wallet_id":"wallet-01","token":"` + testToken + `","side":"buy","amount":"0.01","slippage_percent":"3","expires_at":"2099-01-01T00:00:00Z"}`,
	} {
		if w := call(h, "POST", "/v1/trades", "injected", body); w.Code != http.StatusBadRequest {
			t.Fatal("caller security field accepted")
		}
	}
	noIntakeEconomicWork(t, s)
}

func TestHTTPC1DirectSQLSourceBindingAndRejectedKey(t *testing.T) {
	for _, mismatch := range []string{"wallet_address", "request", "expiry", "legacy_key"} {
		t.Run(mismatch, func(t *testing.T) {
			_, s, original, _ := fixture(t)
			r := record(t, call(original, "POST", "/v1/trades", "legacy", encode(buy())))
			key := "new"
			if mismatch == "legacy_key" {
				key = "legacy"
			}
			id := "req_" + hash([]byte(original.identity.PrincipalID+"\x00"+key))
			op, step := "manual_op_"+id, "manual_step_"+id
			tx, err := s.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			_, err = tx.Exec(`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at,request_json,updated_at,deadline_capability,expires_at) VALUES(?,4663,?,?,?,'swap','manual_validation_pending',?,?,?,?,?)`, op, "wallet-01", "manual:"+id, hash([]byte(encode(buy()))), r.CreatedAt, encode(buy()), r.CreatedAt, r.DeadlineCapability, r.ExpiresAt)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(`INSERT INTO execution_steps(id,operation_id,step_index,kind,status,wallet_id,created_at,updated_at,expires_at) VALUES(?,?,0,'manual_trade_validation','validation_pending','wallet-01',?,?,?)`, step, op, r.CreatedAt, r.CreatedAt, r.ExpiresAt)
			if err != nil {
				t.Fatal(err)
			}
			address, request, expiry := testWallet, encode(buy()), r.ExpiresAt
			switch mismatch {
			case "wallet_address":
				address = testToken
			case "request":
				request = "{}"
			case "expiry":
				expiry = "2099-01-01T00:00:00Z"
			}
			_, err = tx.Exec(`INSERT INTO manual_http_order_sources(id,principal_id,idempotency_key,operation_id,step_id,chain,wallet_id,wallet_address,source,request_fingerprint,request_json,api_policy_version,api_policy_hash,api_policy_json,expires_at,created_at,deadline_capability,contract_deadline,send_authorized,submission_queued) VALUES(?,?,?,?,?,'robinhood','wallet-01',?,'MANUAL',?,?,1,?,?,?,?,'APPLICATION_TTL_ONLY',0,0,0)`, id, original.identity.PrincipalID, key, op, step, address, hash([]byte(request)), request, hash([]byte(encode(DefaultPolicy()))), encode(DefaultPolicy()), expiry, r.CreatedAt)
			if err == nil {
				t.Fatal("direct SQL bypass: " + mismatch)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if count(t, s.db, "operations") != 0 || count(t, s.db, "manual_http_order_sources") != 0 || count(t, s.db, "canary_runtime_audit") != 1 {
				t.Fatal("SQL mismatch committed work")
			}
		})
	}
}
