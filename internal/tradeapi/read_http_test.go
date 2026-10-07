package tradeapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
)

type testReads struct {
	calls  int
	fault  error
	during func()
	max    string
	tip    string
}

func (q *testReads) Token(_ context.Context, value string) (chainquery.Token, error) {
	q.calls++
	if q.fault != nil {
		return chainquery.Token{}, q.fault
	}
	return chainquery.Token{Address: value, Decimals: 6, Protocol: "pons_v2_curve", QuoteAsset: "native"}, nil
}
func (q *testReads) Balances(_ context.Context, wallet, token string) (chainquery.Balances, error) {
	q.calls++
	if q.during != nil {
		q.during()
	}
	if q.fault != nil {
		return chainquery.Balances{}, q.fault
	}
	r := chainquery.Balances{NativeUnits: "2000000000000000000", NativeAmount: "2"}
	if token != "" {
		r.Token = &chainquery.Token{Address: token, Decimals: 6}
		r.TokenAmount = "1500.5"
		r.TokenUnits = "1500500000"
		r.AllowanceUnits = "1000000000"
		r.AllowanceAmount = "1000"
		r.AllowanceLimitedTokenAmount = "1000"
	}
	return r, nil
}
func (q *testReads) Fees(context.Context) (chainquery.Fees, error) {
	q.calls++
	if q.fault != nil {
		return chainquery.Fees{}, q.fault
	}
	max, tip := q.max, q.tip
	if max == "" {
		max = "2.1"
	}
	if tip == "" {
		tip = "0.1"
	}
	return chainquery.Fees{SuggestedMaxGwei: max, SuggestedTipGwei: tip, ReferenceOnly: true, TipSource: "PROVIDER_ESTIMATE_NOT_BLOCK_PINNED"}, nil
}

func queryAPI(t *testing.T) (*Store, *API, *testReads) {
	t.Helper()
	_, s, h, _ := fixture(t)
	q := &testReads{}
	fresh, err := NewWithQueries(s, h.identity, testCredential, DefaultPolicy(), q)
	if err != nil {
		t.Fatal(err)
	}
	return s, fresh, q
}

func TestHTTPBAuthenticatedQueryContract(t *testing.T) {
	s, h, q := queryAPI(t)
	for _, path := range []string{"/v1/tokens/" + testToken + "?chain=robinhood", "/v1/wallets/wallet-01/balances?chain=robinhood&token=" + testToken, "/v1/wallets/wallet-01/balances?chain=robinhood", "/v1/fees?chain=robinhood"} {
		w := call(h, "GET", path, "", "")
		if w.Code != 200 {
			t.Fatalf("%s %d %s", path, w.Code, w.Body.String())
		}
	}
	if q.calls != 4 {
		t.Fatal("query count")
	}
	w := call(h, "GET", "/v1/wallets/wallet-01/balances?chain=robinhood&token="+testToken, "", "")
	if !strings.Contains(w.Body.String(), `"sellable_token_amount":"1000"`) || !strings.Contains(w.Body.String(), `"unreserved_token_balance":"1500.5"`) || !strings.Contains(w.Body.String(), `"available_native":null`) || !strings.Contains(w.Body.String(), `"send_authorized":false`) {
		t.Fatal(w.Body.String())
	}
	w = call(h, "GET", "/v1/capabilities", "", "")
	if !strings.Contains(w.Body.String(), `"contract_stage":"HTTP_B_READY_FOR_REVIEW"`) || !strings.Contains(w.Body.String(), `"fee_execution_binding":"NOT_CONNECTED"`) || !strings.Contains(w.Body.String(), `"submission_enabled":false`) {
		t.Fatal("query capability overstated sending")
	}
	for _, table := range []string{"operations", "transaction_attempts", "canary_send_permits", "canary_runtime_audit", "canary_alert_outbox", "user_http_trade_requests"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("query mutated durable rows: " + table)
		}
	}
	w = call(h, "POST", "/v1/trades", "still-disabled", encode(buy()))
	record(t, w)
	if q.calls != 5 {
		t.Fatal("submission request reached query/execution")
	}
}

func TestHTTPBQueryAuthAndInputBeforeRPC(t *testing.T) {
	_, h, q := queryAPI(t)
	for _, path := range []string{"/v1/tokens/" + testToken + "?chain=robinhood", "/v1/wallets/wallet-01/balances?chain=robinhood", "/v1/fees?chain=robinhood"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("unauthenticated query accepted")
		}
	}
	for _, path := range []string{
		"/v1/tokens/" + testToken, "/v1/tokens/" + testToken + "?chain=bsc", "/v1/tokens/invalid?chain=robinhood",
		"/v1/fees?chain=robinhood&chain=robinhood", "/v1/fees?chain=robinhood&rpc_url=http://localhost",
		"/v1/fees?chain=robinhood&method=eth_sendRawTransaction", "/v1/fees?chain=robinhood&token=" + testToken,
		"/v1/wallets/wallet-01/balances?chain=robinhood&token=invalid", "/v1/wallets/wallet-01/balances?chain=robinhood&token=",
		"/v1/wallets/wallet-01/balances?chain=robinhood&token=" + testToken + "&token=" + testToken,
	} {
		if w := call(h, "GET", path, "", ""); w.Code != 400 {
			t.Fatalf("bad query accepted %s: %s", path, w.Body.String())
		}
	}
	if w := call(h, "GET", "/v1/wallets/other/balances?chain=robinhood", "", ""); w.Code != 403 {
		t.Fatal("cross wallet query")
	}
	if q.calls != 0 {
		t.Fatal("rejected request reached RPC")
	}
}

func TestHTTPBUnwiredAndFailuresDoNotExposePartialData(t *testing.T) {
	_, _, h, _ := fixture(t)
	for _, path := range []string{"/v1/fees?chain=robinhood", "/v1/tokens/" + testToken + "?chain=robinhood", "/v1/wallets/wallet-01/balances?chain=robinhood"} {
		if w := call(h, "GET", path, "", ""); w.Code != 503 {
			t.Fatal("unwired query pretended available")
		}
	}
	_, wired, q := queryAPI(t)
	for _, tc := range []struct {
		err  error
		code int
	}{{chainquery.ErrUnavailable, 503}, {chainquery.ErrObservation, 502}, {chainquery.ErrUnsupported, 422}, {errors.New("secret provider detail"), 503}} {
		q.fault = tc.err
		for _, path := range []string{"/v1/fees?chain=robinhood", "/v1/tokens/" + testToken + "?chain=robinhood", "/v1/wallets/wallet-01/balances?chain=robinhood&token=" + testToken} {
			w := call(wired, "GET", path, "", "")
			if w.Code != tc.code || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "native_amount") {
				t.Fatal(w.Body.String())
			}
		}
	}
}

func TestHTTPBBudgetReferenceDoesNotClampOrBumpFees(t *testing.T) {
	_, h, q := queryAPI(t)
	for _, tc := range []struct{ max, tip string }{{"101", "0.1"}, {"1", "2"}, {"1e2", "0.1"}, {"0", "0"}, {"1", "0.0000000001"}} {
		q.max, q.tip = tc.max, tc.tip
		w := call(h, "GET", "/v1/fees?chain=robinhood", "", "")
		if w.Code != 422 {
			t.Fatal("unsafe fee suggestion accepted")
		}
	}
}

func TestHTTPBExistingAmbiguityAndEmergencyStopRemainFrozen(t *testing.T) {
	s, h, q := queryAPI(t)
	statements := []string{
		`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('existing',4663,'wallet-01','existing','hash','swap','broadcast_unknown','2026-10-07T00:00:00Z')`,
		`INSERT INTO execution_steps(id,operation_id,step_index,kind,status,wallet_id) VALUES('step','existing',0,'swap','broadcast_unknown','wallet-01')`,
		`INSERT INTO execution_wallet_lanes(wallet_id,address,state,operation_id,step_id,reserved_nonce,freeze_reason,updated_at) VALUES('wallet-01','` + testWallet + `','frozen','existing','step','7','broadcast_unknown','2026-10-07T00:00:00Z')`,
		`INSERT INTO execution_reservations(operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at) VALUES('existing','step','wallet-01','native','1','10','frozen','2026-10-07T00:00:00Z','2026-10-07T00:00:00Z')`,
		`INSERT INTO canary_control_state(singleton,chain_id,mode,policy_version,emergency_stopped,reason,authorization_ref,effective_at,updated_at) VALUES(1,4663,'DISABLED',1,1,'stop','not-granted','2026-10-07T00:00:00Z','2026-10-07T00:00:00Z')`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	w := call(h, "GET", "/v1/wallets/wallet-01/balances?chain=robinhood&token="+testToken, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"wallet_lane_blocked":true`) || !strings.Contains(w.Body.String(), `"sellable_token_amount":"0"`) || !strings.Contains(w.Body.String(), `"unreserved_token_balance":"0"`) || q.calls != 1 {
		t.Fatal(w.Body.String())
	}
	var state, nonce, reservation string
	if err := s.db.QueryRow(`SELECT state,reserved_nonce FROM execution_wallet_lanes WHERE wallet_id='wallet-01'`).Scan(&state, &nonce); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id='existing'`).Scan(&reservation); err != nil {
		t.Fatal(err)
	}
	if state != "frozen" || reservation != "frozen" || nonce != "7" || count(t, s.db, "operations") != 1 || count(t, s.db, "transaction_attempts") != 0 || count(t, s.db, "canary_runtime_authorizations") != 0 || count(t, s.db, "canary_send_permits") != 0 {
		t.Fatal("query changed economic freeze")
	}
}

func TestHTTPBWalletAndReservationRecheckedAfterRPC(t *testing.T) {
	for _, fault := range []string{"wallet_disabled", "wallet_address_drift", "new_risk_reservation"} {
		t.Run(fault, func(t *testing.T) {
			s, h, q := queryAPI(t)
			q.during = func() {
				var err error
				switch fault {
				case "wallet_disabled":
					_, err = s.db.Exec(`UPDATE dry_run_wallets SET enabled=0 WHERE id='wallet-01'`)
				case "wallet_address_drift":
					_, err = s.db.Exec(`UPDATE dry_run_wallets SET address='0x3000000000000000000000000000000000000003' WHERE id='wallet-01'`)
				case "new_risk_reservation":
					_, err = s.db.Exec(`INSERT INTO canary_risk_reservations(id,operation_id,wallet_id,token_address,policy_version,input_amount,gas_budget,state,created_at,updated_at) VALUES('risk','op','wallet-01',?,1,'1','1','frozen','2026-10-07T00:00:00Z','2026-10-07T00:00:00Z')`, testToken)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			w := call(h, "GET", "/v1/wallets/wallet-01/balances?chain=robinhood&token="+testToken, "", "")
			if fault == "new_risk_reservation" {
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"sellable_token_amount":"0"`) {
					t.Fatal(w.Body.String())
				}
			} else if w.Code != 403 || strings.Contains(w.Body.String(), "native_amount") {
				t.Fatal("wallet mutation exposed balance")
			}
		})
	}
}

func TestHTTPBReadOnlyDBAndNoAuthorizationPrerequisite(t *testing.T) {
	s, h, _ := queryAPI(t)
	if _, err := s.db.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	defer s.db.Exec(`PRAGMA query_only=OFF`)
	w := call(h, "GET", "/v1/wallets/wallet-01/balances?chain=robinhood&token="+testToken, "", "")
	if w.Code != 200 {
		t.Fatal("query depends on write/authorization")
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["send_authorized"] != false {
		t.Fatal("query authorized sending")
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	w = call(h, "GET", "/v1/wallets/wallet-01/balances?chain=robinhood", "", "")
	if w.Code != 503 {
		t.Fatal("DB failure did not fail closed")
	}
}
