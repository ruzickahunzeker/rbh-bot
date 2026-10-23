package tradeapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

const testCredential = "test-only-credential-not-a-real-secret-32"
const testWallet = "0x1000000000000000000000000000000000000001"
const testToken = "0x2000000000000000000000000000000000000002"

var testNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T) (*storage.Database, *Store, *API, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trade.db")
	database, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = database.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO dry_run_wallets(id,chain_id,address,enabled) VALUES('wallet-01',4663,?,1)`, testWallet); err != nil {
		t.Fatal(err)
	}
	h, err := New(s, Identity{PrincipalID: "caller-01", WalletID: "wallet-01", WalletAddress: testWallet}, testCredential, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	h.now = func() time.Time { return testNow }
	return database, s, h, path
}

func buy() Request {
	return Request{Chain: "robinhood", WalletID: "wallet-01", Token: testToken, Side: "buy", Amount: "0.01", SlippagePercent: "3", Fee: &Fee{MaxGwei: "1", TipGwei: "0.1"}}
}

func call(h http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testCredential)
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func record(t *testing.T, w *httptest.ResponseRecorder) Record {
	t.Helper()
	if w.Code != 503 {
		t.Fatalf("expected disabled 503, got %d: %s", w.Code, w.Body.String())
	}
	var r Record
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "REJECTED" || r.Reason != "SUBMISSION_DISABLED" || r.OperationID != nil || r.SendAuthorized || r.SubmissionQueued || r.ContractDeadline || r.DeadlineCapability != "APPLICATION_TTL_ONLY" {
		t.Fatalf("unsafe response: %+v", r)
	}
	return r
}

func count(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUserHTTPTradeContract(t *testing.T) {
	_, s, h, _ := fixture(t)
	r := record(t, call(h, "POST", "/v1/trades", "buy-1", encode(buy())))
	if r.Duplicate || r.ExpiresAt != testNow.Add(30*time.Second).Format(time.RFC3339Nano) || r.Request.MinReceive != "" || r.Request.Fee.MaxTotalNative != "" {
		t.Fatalf("defaults changed: %+v", r)
	}
	if count(t, s.db, "user_http_trade_requests") != 1 || count(t, s.db, "canary_runtime_audit") != 1 || count(t, s.db, "canary_alert_outbox") != 1 {
		t.Fatal("request/audit/outbox not exactly once")
	}
	for _, table := range []string{"operations", "execution_steps", "transaction_attempts", "transaction_submissions", "execution_wallet_lanes", "execution_reservations", "canary_risk_reservations", "canary_runtime_authorizations", "canary_send_permits", "canary_authorization_operation_usage"} {
		if count(t, s.db, table) != 0 {
			t.Fatalf("economic side effect in %s", table)
		}
	}
	w := call(h, "GET", "/v1/trades/"+r.RequestID, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var fetched Record
	if err := json.Unmarshal(w.Body.Bytes(), &fetched); err != nil || !reflect.DeepEqual(r, fetched) {
		t.Fatalf("query differs: %v %s", err, w.Body.String())
	}
	w = call(h, "GET", "/v1/trades/by-idempotency-key", "buy-1", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), r.RequestID) {
		t.Fatal("cannot recover lost response by key")
	}
	w = call(h, "GET", "/v1/trades/"+r.RequestID+"/events", "", "")
	var events Events
	if err := json.Unmarshal(w.Body.Bytes(), &events); err != nil || len(events.Events) != 1 || events.Scope != "API_REJECTION_ONLY" {
		t.Fatalf("events: %v %s", err, w.Body.String())
	}
	w = call(h, "GET", "/v1/trades/"+r.RequestID+"/events?after="+strconv.FormatUint(events.NextCursor, 10), "", "")
	if err := json.Unmarshal(w.Body.Bytes(), &events); err != nil || len(events.Events) != 0 {
		t.Fatal("event cursor duplicates event")
	}
	w = call(h, "GET", "/v1/wallets", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), testWallet) {
		t.Fatal(w.Body.String())
	}
	w = call(h, "GET", "/v1/capabilities", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"submission_enabled":false`) || !strings.Contains(w.Body.String(), `"protocol_resolution":"NOT_CONNECTED"`) {
		t.Fatal(w.Body.String())
	}
	for _, secret := range []string{testCredential, "encrypted_raw_tx", "calldata", "private_key"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("sensitive capability data")
		}
	}
}

func TestUserHTTPTradeModesAndOptionalLimits(t *testing.T) {
	_, _, h, _ := fixture(t)
	requests := []Request{buy(), buy(), buy(), buy()}
	requests[0].Fee = nil
	requests[1].Side, requests[1].Amount = "sell", "1500.5"
	requests[2].Side, requests[2].Amount, requests[2].SellPercent = "sell", "", "100"
	requests[3].Fee = &Fee{MaxTotalNative: "0.001"} // Auto fee + explicit total ceiling.
	requests[3].MinReceive = "1000"
	for i, r := range requests {
		result := record(t, call(h, "POST", "/v1/trades", "mode-"+strconv.Itoa(i), encode(r)))
		if !reflect.DeepEqual(result.Request, r) {
			t.Fatalf("user limits or units changed: %+v %+v", r, result.Request)
		}
	}
}

func TestUserHTTPTradeStrictJSONAndParameters(t *testing.T) {
	_, s, h, _ := fixture(t)
	body := encode(buy())
	bad := []string{
		`null`, `[]`, `{}`, body + ` {}`, strings.Replace(body, `"chain":"robinhood"`, `"chain":"robinhood","chain":"robinhood"`, 1),
		strings.Replace(body, `"chain"`, `"CHAIN"`, 1), strings.Replace(body, `"wallet_id"`, `"WALLET_ID"`, 1),
		strings.Replace(body, `"max_gwei":"1"`, `"max_gwei":"1","max_gwei":"2"`, 1),
		strings.Replace(body, `"max_gwei"`, `"MAX_GWEI"`, 1), strings.Replace(body, `"max_gwei":"1"`, `"nonce":"7"`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":0.01`, 1), strings.Replace(body, `"amount":"0.01"`, `"amount":"1e-2"`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":"00.01"`, 1), strings.Replace(body, `"amount":"0.01"`, `"amount":"0"`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":"0.0000000000000000001"`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":"0.01","sell_percent":"50"`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"sell_percent":"50"`, 1),
		strings.Replace(body, `"slippage_percent":"3"`, `"slippage_percent":"3.001"`, 1),
		strings.Replace(body, `"max_gwei":"1"`, `"max_gwei":"0.0000000001"`, 1),
		strings.Replace(body, `"tip_gwei":"0.1"`, `"tip_gwei":"2"`, 1),
		strings.Replace(body, `"max_gwei":"1",`, "", 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":"0.01","min_receive":null`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":"0.01","min_receive":""`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":"0.01","min_receive":"0"`, 1),
		strings.Replace(body, `"max_gwei":"1"`, `"max_gwei":"1","max_total_native":null`, 1),
		strings.Replace(body, `"max_gwei":"1"`, `"max_gwei":"1","max_total_native":""`, 1),
		strings.Replace(body, `"max_gwei":"1"`, `"max_gwei":"1","max_total_native":"0"`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":"0.01","ttl_seconds":null`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":"0.01","ttl_seconds":0`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":"0.01","ttl_seconds":3e1`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":"0.01","source":"copy"`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":"0.01","live":true`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":"0.01","policy_version":1`, 1),
		strings.Replace(body, `"amount":"0.01"`, `"amount":"0.01","expires_at":"2100-01-01T00:00:00Z"`, 1),
	}
	for i, b := range bad {
		w := call(h, "POST", "/v1/trades", "bad-"+strconv.Itoa(i), b)
		if w.Code != 400 {
			t.Fatalf("case %d expected 400, got %d: %s (%s)", i, w.Code, w.Body.String(), b)
		}
	}
	for _, r := range []Request{
		{Chain: "robinhood", WalletID: "wallet-01", Token: testToken, Side: "sell", SellPercent: "100.01", SlippagePercent: "3"},
		{Chain: "robinhood", WalletID: "wallet-01", Token: testToken, Side: "sell", SellPercent: "0", SlippagePercent: "3"},
	} {
		if w := call(h, "POST", "/v1/trades", "bad-sell", encode(r)); w.Code != 400 {
			t.Fatal(w.Body.String())
		}
	}
	if count(t, s.db, "user_http_trade_requests") != 0 {
		t.Fatal("invalid body persisted")
	}
}

func TestUserHTTPTradeAuthenticationScopeAndPolicy(t *testing.T) {
	_, s, h, _ := fixture(t)
	for _, auth := range []string{"", "Bearer wrong", "Basic " + testCredential, "Bearer " + testCredential + " "} {
		r := httptest.NewRequest("POST", "/v1/trades", strings.NewReader(encode(buy())))
		r.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("credential accepted")
		}
	}
	r := httptest.NewRequest("GET", "/v1/capabilities", nil)
	r.Header.Add("Authorization", "Bearer "+testCredential)
	r.Header.Add("Authorization", "Bearer "+testCredential)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("multiple auth headers accepted")
	}
	for _, tc := range []struct {
		r    Request
		code int
	}{
		{func() Request { r := buy(); r.WalletID = "other"; return r }(), 403},
		{func() Request { r := buy(); r.Chain = "bsc"; return r }(), 422},
		{func() Request { r := buy(); r.SlippagePercent = "6"; return r }(), 422},
		{func() Request { r := buy(); r.Fee = &Fee{MaxGwei: "101"}; return r }(), 422},
		{func() Request { r := buy(); ttl := uint64(301); r.TTLSeconds = &ttl; return r }(), 422},
	} {
		if w := call(h, "POST", "/v1/trades", "policy", encode(tc.r)); w.Code != tc.code {
			t.Fatalf("got %d: %s", w.Code, w.Body.String())
		}
	}
	if count(t, s.db, "user_http_trade_requests") != 0 {
		t.Fatal("unauthorized request persisted")
	}
	if _, err := s.db.Exec(`UPDATE dry_run_wallets SET enabled=0 WHERE id='wallet-01'`); err != nil {
		t.Fatal(err)
	}
	if w := call(h, "POST", "/v1/trades", "disabled-wallet", encode(buy())); w.Code != 403 {
		t.Fatal(w.Body.String())
	}
	if _, err := s.db.Exec(`UPDATE dry_run_wallets SET enabled=1,address='0x3000000000000000000000000000000000000003' WHERE id='wallet-01'`); err != nil {
		t.Fatal(err)
	}
	if w := call(h, "POST", "/v1/trades", "wallet-drift", encode(buy())); w.Code != 403 {
		t.Fatal(w.Body.String())
	}
}

func TestUserHTTPTradeHeaderBodyAndCursorBounds(t *testing.T) {
	_, s, h, _ := fixture(t)
	for _, k := range []string{"", strings.Repeat("a", 129), "key with space"} {
		if w := call(h, "POST", "/v1/trades", k, encode(buy())); w.Code != 400 {
			t.Fatal(w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", "/v1/trades", strings.NewReader(encode(buy())))
	r.Header.Set("Authorization", "Bearer "+testCredential)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Add("Idempotency-Key", "a")
	r.Header.Add("Idempotency-Key", "b")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("multiple keys accepted")
	}
	if w = call(h, "POST", "/v1/trades", "big", strings.Repeat(" ", 16385)); w.Code != 413 {
		t.Fatal(w.Body.String())
	}
	r.Header.Set("Idempotency-Key", "x")
	r.Header.Set("Content-Type", "text/plain")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal("non-json accepted")
	}
	if count(t, s.db, "user_http_trade_requests") != 0 {
		t.Fatal("invalid request persisted")
	}
	result := record(t, call(h, "POST", "/v1/trades", "cursor", encode(buy())))
	for _, q := range []string{"after=", "after=01", "after=-1", "after=1&after=2", "after=%31", "other=1", "after=9223372036854775808"} {
		if w = call(h, "GET", "/v1/trades/"+result.RequestID+"/events?"+q, "", ""); w.Code != 400 {
			t.Fatalf("accepted %s", q)
		}
	}
	for _, p := range []string{"/v1/trades?live=true", "/v1/wallets?chain=bsc", "/v1/capabilities?live=true"} {
		method := "GET"
		if strings.HasPrefix(p, "/v1/trades?") {
			method = "POST"
		}
		if w = call(h, method, p, "x", encode(buy())); w.Code != 400 {
			t.Fatal("query accepted")
		}
	}
}

func TestUserHTTPTradeIdempotencyAndExpiry(t *testing.T) {
	_, s, h, _ := fixture(t)
	original := record(t, call(h, "POST", "/v1/trades", "immutable", encode(buy())))
	h.now = func() time.Time { t.Fatal("duplicate generated new TTL"); return time.Time{} }
	h.policy.DefaultTTLSeconds = 300
	h.policy.MaxSlippageBPS = 0
	duplicate := record(t, call(h, "POST", "/v1/trades", "immutable", encode(buy())))
	if !duplicate.Duplicate || duplicate.ExpiresAt != original.ExpiresAt || duplicate.APIPolicyHash != original.APIPolicyHash {
		t.Fatal("duplicate changed binding")
	}
	variants := []Request{buy(), buy(), buy(), buy(), buy(), buy(), buy()}
	variants[0].Chain = "bsc"
	variants[1].Amount = "0.02"
	variants[2].SlippagePercent = "2"
	variants[3].Fee = &Fee{MaxGwei: "2"}
	variants[4].MinReceive = "100"
	variants[5].Fee = &Fee{MaxGwei: "1", TipGwei: "0.1", MaxTotalNative: "0.001"}
	ttl := uint64(31)
	variants[6].TTLSeconds = &ttl
	for _, v := range variants {
		if w := call(h, "POST", "/v1/trades", "immutable", encode(v)); w.Code != 409 {
			t.Fatalf("changed identity accepted: %d %s", w.Code, w.Body.String())
		}
	}
	equivalent := buy()
	equivalent.Amount = "0.0100"
	equivalent.Fee.MaxGwei = "1.0"
	if v := record(t, call(h, "POST", "/v1/trades", "immutable", encode(equivalent))); !v.Duplicate {
		t.Fatal("equivalent decimal minted request")
	}
	if count(t, s.db, "user_http_trade_requests") != 1 || count(t, s.db, "canary_runtime_audit") != 1 {
		t.Fatal("duplicate consumed audit")
	}
}

func TestUserHTTPTradeRestartAndTenantIsolation(t *testing.T) {
	database, _, h, path := fixture(t)
	original := record(t, call(h, "POST", "/v1/trades", "restart", encode(buy())))
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := New(s, h.identity, testCredential, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, now := range []time.Time{testNow.Add(30 * time.Second), testNow.Add(31 * time.Second), testNow.Add(24 * time.Hour)} {
		fresh.now = func() time.Time { return now }
		dup := record(t, call(fresh, "POST", "/v1/trades", "restart", encode(buy())))
		if !dup.Duplicate || dup.ExpiresAt != original.ExpiresAt || dup.RequestID != original.RequestID {
			t.Fatal("restart/expiry reset request")
		}
	}
	other := h.identity
	other.PrincipalID = "caller-02"
	h2, err := New(s, other, testCredential, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if w := call(h2, "GET", "/v1/trades/"+original.RequestID, "", ""); w.Code != 404 {
		t.Fatal("cross-tenant query exposed")
	}
	if w := call(h2, "GET", "/v1/trades/"+original.RequestID+"/events", "", ""); w.Code != 404 {
		t.Fatal("cross-tenant events exposed")
	}
	second := record(t, call(h2, "POST", "/v1/trades", "restart", encode(buy())))
	if second.RequestID == original.RequestID {
		t.Fatal("tenant key collision")
	}
	if _, err = s.db.Exec(`VACUUM`); err != nil {
		t.Fatal(err)
	}
	if w := call(fresh, "GET", "/v1/trades/"+original.RequestID+"/events?after=1", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"events":[]`) {
		t.Fatal("VACUUM changed cursor")
	}
}

func TestUserHTTPTradeConcurrentDedupeAcrossSQLiteHandles(t *testing.T) {
	_, s, h, path := fixture(t)
	d2, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	s2, err := NewStore(d2)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := New(s2, h.identity, testCredential, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	h2.now = h.now
	const n = 48
	var wg sync.WaitGroup
	results := make(chan Record, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			handler := h
			if i%2 == 1 {
				handler = h2
			}
			w := call(handler, "POST", "/v1/trades", "concurrent", encode(buy()))
			var r Record
			if w.Code != 503 {
				errs <- errors.New(w.Body.String())
				return
			}
			if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
				errs <- err
				return
			}
			results <- r
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	first, duplicates, total := 0, 0, 0
	for r := range results {
		total++
		if r.Duplicate {
			duplicates++
		} else {
			first++
		}
		if r.SendAuthorized || r.SubmissionQueued {
			t.Fatal("economic work accepted")
		}
	}
	if total != n || first != 1 || duplicates != n-1 {
		t.Fatalf("first=%d duplicates=%d total=%d", first, duplicates, total)
	}
	if count(t, s.db, "user_http_trade_requests") != 1 || count(t, s.db, "canary_runtime_audit") != 1 || count(t, s.db, "canary_alert_outbox") != 1 {
		t.Fatal("concurrent overcreation")
	}
}

func TestUserHTTPTradeAtomicAuditFaultsAndSchema(t *testing.T) {
	_, s, h, _ := fixture(t)
	for _, table := range []string{"canary_runtime_audit", "canary_alert_outbox"} {
		if _, err := s.db.Exec(`CREATE TRIGGER forced_user_http_failure BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'forced failure'); END`); err != nil {
			t.Fatal(err)
		}
		w := call(h, "POST", "/v1/trades", "fault", encode(buy()))
		if w.Code != 503 || strings.Contains(w.Body.String(), "SUBMISSION_DISABLED") || strings.Contains(w.Body.String(), "forced failure") {
			t.Fatal("failed durability reported as completed")
		}
		for _, tbl := range []string{"user_http_trade_requests", "canary_runtime_audit", "canary_alert_outbox"} {
			if count(t, s.db, tbl) != 0 {
				t.Fatal("partial commit")
			}
		}
		if _, err := s.db.Exec(`DROP TRIGGER forced_user_http_failure`); err != nil {
			t.Fatal(err)
		}
	}
	r := record(t, call(h, "POST", "/v1/trades", "fault", encode(buy())))
	for _, statement := range []string{
		`UPDATE user_http_trade_requests SET expires_at='2100-01-01T00:00:00Z'`,
		`UPDATE user_http_trade_requests SET send_authorized=1`,
		`DELETE FROM user_http_trade_requests`,
		`UPDATE canary_runtime_audit SET reason_code='PASS' WHERE id='user-http-audit:` + r.RequestID + `'`,
		`DELETE FROM canary_runtime_audit WHERE id='user-http-audit:` + r.RequestID + `'`,
	} {
		if _, err := s.db.Exec(statement); err == nil {
			t.Fatal("direct mutation allowed")
		}
	}
	for _, change := range []string{"'ACCEPTED',reason_code,deadline_capability,contract_deadline,send_authorized,submission_queued", "outcome,reason_code,deadline_capability,contract_deadline,1,submission_queued", "outcome,reason_code,deadline_capability,contract_deadline,send_authorized,1"} {
		_, err := s.db.Exec(`INSERT INTO user_http_trade_requests SELECT sequence+100,'bypass',principal_id,'bypass',chain,wallet_id,wallet_address,source,request_fingerprint,request_json,api_policy_version,api_policy_hash,api_policy_json,expires_at,created_at,`+change+` FROM user_http_trade_requests WHERE id=?`, r.RequestID)
		if err == nil {
			t.Fatal("schema allowed accepted/authorized/queued")
		}
	}
	if count(t, s.db, "user_http_trade_requests") != 1 || count(t, s.db, "canary_runtime_audit") != 1 {
		t.Fatal("failed SQL mutation affected ledger")
	}
}

func TestUserHTTPTradePreservesAmbiguousArtifactAndFreeze(t *testing.T) {
	_, s, h, _ := fixture(t)
	statements := []string{
		`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('existing',4663,'wallet-01','existing','hash','swap','broadcast_unknown','2026-10-07T00:00:00Z')`,
		`INSERT INTO execution_steps(id,operation_id,step_index,kind,status,wallet_id) VALUES('step','existing',0,'swap','broadcast_unknown','wallet-01')`,
		`INSERT INTO transaction_attempts(id,step_id,nonce,tx_hash,status,created_at,encrypted_raw_tx,gas_fee_cap) VALUES('attempt','step','7','tx-hash','broadcast_unknown','2026-10-07T00:00:00Z',X'1234','100')`,
		`INSERT INTO execution_wallet_lanes(wallet_id,address,state,operation_id,step_id,reserved_nonce,freeze_reason,updated_at) VALUES('wallet-01','` + testWallet + `','frozen','existing','step','7','broadcast_unknown','2026-10-07T00:00:00Z')`,
		`INSERT INTO execution_reservations(operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at) VALUES('existing','step','wallet-01','native','1','10','frozen','2026-10-07T00:00:00Z','2026-10-07T00:00:00Z')`,
	}
	for _, q := range statements {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	record(t, call(h, "POST", "/v1/trades", "unrelated", encode(buy())))
	var lane, reservation, nonce, artifact string
	if err := s.db.QueryRow(`SELECT l.state,r.status,a.nonce,hex(a.encrypted_raw_tx) FROM execution_wallet_lanes l JOIN execution_reservations r ON r.operation_id=l.operation_id JOIN transaction_attempts a ON a.step_id=r.step_id`).Scan(&lane, &reservation, &nonce, &artifact); err != nil {
		t.Fatal(err)
	}
	if lane != "frozen" || reservation != "frozen" || nonce != "7" || artifact != "1234" || count(t, s.db, "transaction_attempts") != 1 || count(t, s.db, "operations") != 1 || count(t, s.db, "canary_send_permits") != 0 {
		t.Fatal("existing ambiguity modified")
	}
}

func TestUserHTTPHumanUnitsExactPrecision(t *testing.T) {
	for _, tc := range []struct {
		v    string
		dec  uint8
		want string
	}{
		{"0.01", 18, "10000000000000000"}, {"1500.5", 6, "1500500000"}, {"1", 0, "1"}, {"0.000000001", 9, "1"},
	} {
		got, err := HumanUnits(tc.v, tc.dec)
		if err != nil || got != tc.want {
			t.Fatalf("%s: %s %v", tc.v, got, err)
		}
	}
	for _, v := range []string{"0.0000001", "1e3", "-1", "+1", "1.", ".1", " 1", "01", strings.Repeat("9", 80)} {
		if _, err := HumanUnits(v, 6); err == nil {
			t.Fatalf("accepted %s", v)
		}
	}
}

func TestUserHTTPTradeHasNoEconomicDependencies(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.Contains(path, "/internal/trade") || strings.Contains(path, "trade-sdk") || strings.Contains(path, "ethclient") || strings.Contains(path, "/rpc") {
				t.Fatalf("economic dependency: %s", path)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "SignTransaction", "SendRawTransaction", "Prepare", "Submit", "ReplayUnknown", "PendingNonce", "ReserveExecution", "MarkControlledSendIntent", "ConsumeCanaryPermitAfterGate":
					t.Errorf("prohibited call reachable: %s", sel.Sel.Name)
				}
			}
			return true
		})
	}
}

func TestUserHTTPTradeLocalUDSTransport(t *testing.T) {
	_, _, h, _ := fixture(t)
	socket := filepath.Join(t.TempDir(), "api.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: h, ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(l) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	req, err := http.NewRequest("POST", "http://localhost/v1/trades", strings.NewReader(encode(buy())))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testCredential)
	req.Header.Set("Idempotency-Key", "uds")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 503 || !strings.Contains(string(body), "SUBMISSION_DISABLED") {
		t.Fatalf("UDS response %d: %s %v", resp.StatusCode, body, err)
	}
}

func TestUserHTTPTradePrivateConfiguration(t *testing.T) {
	database, _, _, _ := fixture(t)
	path := filepath.Join(t.TempDir(), "api.json")
	body := `{"principal_id":"caller-01","wallet_id":"wallet-01","wallet_address":"` + testWallet + `","token":"` + testCredential + `"}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(database, path, "different-internal-credential"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(database, path, testCredential); err == nil {
		t.Fatal("internal IPC credential reused as user API credential")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(database, path, ""); err == nil {
		t.Fatal("world readable credential accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(database, link, ""); err == nil {
		t.Fatal("symlink accepted")
	}
	for _, invalid := range []string{body + ` {}`, strings.Replace(body, `"principal_id"`, `"unknown"`, 1), strings.Replace(body, testCredential, "short", 1), strings.Replace(body, `"caller-01"`, `"bad identity"`, 1), strings.Replace(body, `"principal_id":"caller-01"`, `"principal_id":"caller-01","principal_id":"caller-02"`, 1), strings.Replace(body, `"token"`, `"TOKEN"`, 1), strings.Replace(body, `"principal_id":"caller-01"`, `"principal_id":"caller-01","api_policy":null`, 1)} {
		if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(database, path, ""); err == nil || strings.Contains(err.Error(), testCredential) {
			t.Fatal("invalid configuration or secret exposed")
		}
	}
}
