package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type testRoutes struct{ handlers map[string]http.Handler }

func (r *testRoutes) Handle(pattern string, h http.Handler) { r.handlers[pattern] = h }

func TestUserTradeAPIStartupDefaultOffAndScopedRegistration(t *testing.T) {
	routes := &testRoutes{handlers: map[string]http.Handler{}}
	if err := configureUserTradeAPI(routes, nil, config.Config{}); err != nil || len(routes.handlers) != 0 {
		t.Fatal("default registered user API")
	}
	d, err := storage.Open(context.Background(), storage.TradeOwner, filepath.Join(t.TempDir(), "trade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := d.SQLDB(storage.TradeOwner)
	if err != nil {
		t.Fatal(err)
	}
	const address = "0x1000000000000000000000000000000000000001"
	if _, err = db.Exec(`INSERT INTO dry_run_wallets VALUES('wallet-01',4663,?,1)`, address); err != nil {
		t.Fatal(err)
	}
	const credential = "test-only-user-api-credential-32-characters"
	path := filepath.Join(t.TempDir(), "api.json")
	if err = os.WriteFile(path, []byte(`{"principal_id":"tg-bot","wallet_id":"wallet-01","wallet_address":"`+address+`","token":"`+credential+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = configureUserTradeAPI(routes, d, config.Config{TradeAPIConfigFile: path, InternalAuthSecret: credential}); err == nil || len(routes.handlers) != 0 {
		t.Fatal("reused IPC credential registered API")
	}
	if err = configureUserTradeAPI(routes, d, config.Config{TradeAPIConfigFile: path, InternalAuthSecret: "different-internal-secret"}); err != nil {
		t.Fatal(err)
	}
	if len(routes.handlers) != 1 || routes.handlers["/v1/"] == nil {
		t.Fatal("registered non-user routes")
	}
	r := httptest.NewRequest("GET", "/v1/capabilities", nil)
	w := httptest.NewRecorder()
	routes.handlers["/v1/"].ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("unauthenticated registration")
	}
	r.Header.Set("Authorization", "Bearer "+credential)
	w = httptest.NewRecorder()
	routes.handlers["/v1/"].ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"submission_enabled":false`) || !strings.Contains(w.Body.String(), `"live":false`) {
		t.Fatal("registration enabled sending")
	}
	queryRoutes := &testRoutes{handlers: map[string]http.Handler{}}
	// A non-listening local endpoint proves construction does not make RPC calls.
	if err = configureUserTradeAPI(queryRoutes, d, config.Config{TradeAPIConfigFile: path, InternalAuthSecret: "different-internal-secret", TradeAPIReadQueries: true, RPCURL: "http://127.0.0.1:1"}); err != nil {
		t.Fatal("query startup requires RPC health/authorization")
	}
	w = httptest.NewRecorder()
	queryRoutes.handlers["/v1/"].ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"balance_token_fee_queries":"READ_ONLY_REFERENCE"`) || !strings.Contains(w.Body.String(), `"submission_enabled":false`) {
		t.Fatal("read-only startup enabled sending")
	}
	intakeRoutes := &testRoutes{handlers: map[string]http.Handler{}}
	if err := configureUserTradeAPI(intakeRoutes, d, config.Config{TradeAPIManualIntake: true}); err == nil {
		t.Fatal("intake without auth config")
	}
	if err := configureUserTradeAPI(intakeRoutes, d, config.Config{TradeAPIConfigFile: path, InternalAuthSecret: "different-internal-secret", TradeAPIManualIntake: true}); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	intakeRoutes.handlers["/v1/"].ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"request_handling":"MANUAL_RECORD_ONLY"`) || !strings.Contains(w.Body.String(), `"submission_enabled":false`) {
		t.Fatal("intake startup overstates execution")
	}
	post := httptest.NewRequest("POST", "/v1/trades", strings.NewReader(`{"chain":"robinhood","wallet_id":"wallet-01","token":"0x2000000000000000000000000000000000000002","side":"buy","amount":"0.01","slippage_percent":"3"}`))
	post.Header.Set("Authorization", "Bearer "+credential)
	post.Header.Set("Content-Type", "application/json")
	post.Header.Set("Idempotency-Key", "production-local-intake")
	w = httptest.NewRecorder()
	intakeRoutes.handlers["/v1/"].ServeHTTP(w, post)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"status":"RECORDED"`) || !strings.Contains(w.Body.String(), `"submission_queued":false`) {
		t.Fatal(w.Body.String())
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transaction_attempts`).Scan(&n); err != nil || n != 0 {
		t.Fatal("intake startup created artifact")
	}
	validationRoutes := &testRoutes{handlers: map[string]http.Handler{}}
	cfg := config.Config{TradeAPIConfigFile: path, InternalAuthSecret: "different-internal-secret", TradeAPIValidateOrders: true, RPCURL: "http://127.0.0.1:1"}
	if err := configureUserTradeAPI(validationRoutes, d, cfg); err == nil {
		t.Fatal("validation without intake")
	}
	cfg.TradeAPIManualIntake = true
	if err := configureUserTradeAPI(validationRoutes, d, cfg); err == nil {
		t.Fatal("validation without policy version")
	}
	cfg.TradeAPIValidationPolicy = 1
	if err := configureUserTradeAPI(validationRoutes, d, cfg); err == nil || len(validationRoutes.handlers) != 0 {
		t.Fatal("validation without durable policy")
	}
	const policyJSON = `{"Version":1,"Protocol":"PONS_V2_CURVE","MaxOperation":"2000000000000000000","MaxWallet":"4000000000000000000","MaxToken":"4000000000000000000","MaxTotal":"8000000000000000000","MaxGas":"1000000","MaxFee":"3000000000","MaxTip":"1000000000","MinBalance":"1000000000000000","MaxSellBPS":10000,"MaxSlippageBPS":500,"MaxOperations":100,"WindowSeconds":3600,"MaxUnresolved":1}`
	if _, err := db.Exec(`INSERT INTO canary_policies VALUES(1,'PONS_V2_CURVE','2000000000000000000',10000,'4000000000000000000','4000000000000000000','8000000000000000000',100,3600,1,'1000000','3000000000','1000000000',500,'1000000000000000',?,'2026-01-01T00:00:00Z')`, fmt.Sprintf("%x", sha256.Sum256([]byte(policyJSON)))); err != nil {
		t.Fatal(err)
	}
	// A non-listening endpoint: construction must not dial RPC or require
	// runtime authorization. Policy and gate attestations already exist in DB.
	if err := configureUserTradeAPI(validationRoutes, d, cfg); err != nil {
		t.Fatal("validation startup queried RPC or required authorization", err)
	}
	w = httptest.NewRecorder()
	validationRoutes.handlers["/v1/"].ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"internal_order_validation":"INTERNAL_VALIDATION_NOT_EXECUTION_ADMISSION"`) || !strings.Contains(w.Body.String(), `"submission_enabled":false`) || !strings.Contains(w.Body.String(), `"live":false`) {
		t.Fatal(w.Body.String())
	}
	for _, table := range []string{"transaction_attempts", "canary_runtime_authorizations", "canary_risk_reservations", "canary_send_permits"} {
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("validation startup created economic authority", table, n, err)
		}
	}
	admissionRoutes := &testRoutes{handlers: map[string]http.Handler{}}
	cfg.TradeAPIManualAdmission = true
	if err := configureUserTradeAPI(admissionRoutes, d, cfg); err == nil || len(admissionRoutes.handlers) != 0 {
		t.Fatal("admission without server-owned scope registered")
	}
	// Server-owned expected identity is not authority. No grant/control/usage
	// is created and construction cannot contact even the local RPC endpoint.
	scopeJSON := `{"authorization_id":"not-granted","authorization_epoch":1,"manual_policy_id":"manual-local","environment":"local","build_sha":"` + strings.Repeat("1", 40) + `","release_id":"local-release","deployment_id":"local-deployment"}`
	if err := os.WriteFile(path, []byte(`{"principal_id":"tg-bot","wallet_id":"wallet-01","wallet_address":"`+address+`","token":"`+credential+`","manual_admission":`+scopeJSON+`}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := configureUserTradeAPI(admissionRoutes, d, cfg); err != nil || len(admissionRoutes.handlers) != 1 {
		t.Fatal("default-off admission composition required grant/RPC health", err)
	}
	w = httptest.NewRecorder()
	admissionRoutes.handlers["/v1/"].ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"manual_order_admission":"DURABLE_ADMISSION_NOT_EXECUTION_OR_SEND_PERMISSION"`) || !strings.Contains(w.Body.String(), `"submission_enabled":false`) {
		t.Fatal("admission startup overstates sending", w.Body.String())
	}
	post = httptest.NewRequest("POST", "/v1/trades", strings.NewReader(`{"chain":"robinhood","wallet_id":"wallet-01","token":"0x2000000000000000000000000000000000000002","side":"buy","amount":"0.01","slippage_percent":"3"}`))
	post.Header.Set("Authorization", "Bearer "+credential)
	post.Header.Set("Content-Type", "application/json")
	post.Header.Set("Idempotency-Key", "no-auth-admission")
	w = httptest.NewRecorder()
	admissionRoutes.handlers["/v1/"].ServeHTTP(w, post)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "MANUAL_ADMISSION_UNAVAILABLE") {
		t.Fatal("unavailable control allowed validation or admission", w.Body.String())
	}
	for _, table := range []string{"transaction_attempts", "transaction_submissions", "canary_runtime_authorizations", "canary_risk_reservations", "canary_send_permits", "canary_runtime_gate_snapshots", "manual_admission_commands"} {
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("admission startup fabricated economic authority", table, n, err)
		}
	}
	cfg.LiveEnabled = true
	if err := configureUserTradeAPI(&testRoutes{handlers: map[string]http.Handler{}}, d, cfg); err == nil {
		t.Fatal("admission wiring enabled live")
	}
}
