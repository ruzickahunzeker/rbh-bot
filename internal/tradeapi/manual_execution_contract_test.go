package tradeapi

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func contractReader(t *testing.T, s *Store) *ManualExecutionReader {
	t.Helper()
	a, err := NewManualExecutionReader(s)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return testNow }
	return a
}

func TestHTTPC3AExactManualContractAndAssetUnits(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			_, s, h, b, _ := validationAPI(t)
			req := buy()
			req.Fee.TipGwei = "0"
			req.Fee.MaxTotalNative = "0.0001"
			req.MinReceive = "0.98"
			if side != "buy" {
				req.Side, req.Amount, req.MinReceive = "sell", "750.25", "0.00098"
				if side == "sell_percent" {
					req.Amount, req.SellPercent = "", "50"
				}
			}
			r := manualRecord(t, call(h, "POST", "/v1/trades", "contract", encode(req)))
			a := contractReader(t, s)
			c, err := a.Load(context.Background(), h.identity, r.RequestID)
			if err != nil {
				t.Fatal(err)
			}
			if c.Output != r.Validation.Output || encode(c.Request) != encode(r.Request) || c.ExpiresAt != r.ExpiresAt || c.PolicyHash != r.Validation.PolicyHash || c.ResultHash != r.Validation.ResultHash || c.OperationID != *r.OperationID || c.StepID != "manual_step_"+r.RequestID || c.PrincipalID != h.identity.PrincipalID || c.WalletAddress != testWallet || c.ChainID != 4663 || c.Source != "MANUAL" || c.Status != "PARAMETER_CONTRACT_NOT_ADMITTED" || c.Output.GasTipCap != "0" || c.ContractDeadline || c.AdmissionAuthorized || c.SendAuthorized {
				t.Fatal("parameters/identity changed or permissions invented")
			}
			if c.Input.Units != c.Output.Resolution.AmountIn || c.QuotedOutput.Units != c.Output.QuotedOut || c.MinimumOutput.Units != c.Output.MinimumOut || c.NativeValue.Units != c.Output.Value || c.GasBudget != (AssetQuantity{4663, "native", 18, "100000000000000"}) {
				t.Fatal("measurement changed")
			}
			if side == "buy" {
				if c.Input != (AssetQuantity{4663, "native", 18, "10000000000000000"}) || c.MinimumOutput != (AssetQuantity{4663, testToken, 6, "980000"}) {
					t.Fatal("wrong buy units")
				}
			} else if c.Input != (AssetQuantity{4663, testToken, 6, "750250000"}) || c.MinimumOutput != (AssetQuantity{4663, "native", 18, "980000000000000"}) || c.NativeValue.Units != "0" {
				t.Fatal("wrong sell units")
			}
			gotHash := c.BindingHash
			c.BindingHash = ""
			if hash([]byte(encode(c))) != gotHash {
				t.Fatal("contract not completely bound")
			}
			if b.resolves.Load() != 1 || b.completes.Load() != 1 || count(t, s.db, "canary_runtime_audit") != 3 || count(t, s.db, "canary_alert_outbox") != 3 {
				t.Fatal("adapter queried backend or wrote audit")
			}
			noIntakeEconomicWork(t, s)
		})
	}
}

func TestHTTPC3AOptionalUnsetAndDetachedResult(t *testing.T) {
	_, s, h, _, _ := validationAPI(t)
	r := manualRecord(t, call(h, "POST", "/v1/trades", "unset", encode(buy())))
	a := contractReader(t, s)
	one, err := a.Load(context.Background(), h.identity, r.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if one.Request.MinReceive != "" || one.Request.Fee.MaxTotalNative != "" || one.Output.Resolution.MinReceiveUnits != "" {
		t.Fatal("optional limit invented")
	}
	one.Request.Fee.MaxGwei = "999"
	one.Output.Data = "0x00"
	two, err := a.Load(context.Background(), h.identity, r.RequestID)
	if err != nil || two.Request.Fee.MaxGwei != "1" || two.Output != r.Validation.Output || one.BindingHash != two.BindingHash {
		t.Fatal("caller mutation changed durable result")
	}
	noIntakeEconomicWork(t, s)
}

func TestHTTPC3ARejectsPendingRejectedAndWrongIdentity(t *testing.T) {
	_, s, h, b, _ := validationAPI(t)
	b.fault = "complete_failure"
	call(h, "POST", "/v1/trades", "pending", encode(buy()))
	requestID := "req_" + hash([]byte(h.identity.PrincipalID+"\x00pending"))
	a := contractReader(t, s)
	if _, err := a.Load(context.Background(), h.identity, requestID); err == nil {
		t.Fatal("incomplete validation accepted")
	}
	b.fault = ""
	r := manualRecord(t, call(h, "POST", "/v1/trades", "success", encode(buy())))
	for _, id := range []Identity{
		{PrincipalID: "other", WalletID: h.identity.WalletID, WalletAddress: testWallet},
		{PrincipalID: h.identity.PrincipalID, WalletID: "other", WalletAddress: testWallet},
		{PrincipalID: h.identity.PrincipalID, WalletID: h.identity.WalletID, WalletAddress: testToken},
	} {
		if _, err := a.Load(context.Background(), id, r.RequestID); err == nil {
			t.Fatal("scoped identity bypass")
		}
	}
	_, rejectedStore, disabled, _ := fixture(t)
	old := record(t, call(disabled, "POST", "/v1/trades", "rejected", encode(buy())))
	if _, err := contractReader(t, rejectedStore).Load(context.Background(), disabled.identity, old.RequestID); err == nil {
		t.Fatal("historical rejection reactivated")
	}
	noIntakeEconomicWork(t, s)
	noIntakeEconomicWork(t, rejectedStore)
}

func TestHTTPC3ATTLBoundaryAndLateExpiry(t *testing.T) {
	_, s, h, _, _ := validationAPI(t)
	r := manualRecord(t, call(h, "POST", "/v1/trades", "ttl", encode(buy())))
	expiry, err := time.Parse(time.RFC3339Nano, r.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	a := contractReader(t, s)
	for _, tc := range []struct {
		now  time.Time
		pass bool
	}{{expiry.Add(-time.Nanosecond), true}, {expiry, false}, {expiry.Add(time.Nanosecond), false}, {time.Time{}, false}} {
		a.now = func() time.Time { return tc.now }
		_, err := a.Load(context.Background(), h.identity, r.RequestID)
		if tc.pass && err != nil || !tc.pass && !errors.Is(err, chainquery.ErrExpired) {
			t.Fatalf("boundary %v: %v", tc.now, err)
		}
	}
	calls := 0
	a.now = func() time.Time {
		calls++
		if calls == 1 {
			return testNow
		}
		return expiry
	}
	if _, err := a.Load(context.Background(), h.identity, r.RequestID); !errors.Is(err, chainquery.ErrExpired) || calls != 2 {
		t.Fatal("late expiry not rejected")
	}
	// Historical C2 GET still works after expiry; it is not an admission grant.
	if w := call(h, "GET", "/v1/trades/"+r.RequestID, "", ""); w.Code != 200 {
		t.Fatal("historical query blocked")
	}
	noIntakeEconomicWork(t, s)
}

func TestHTTPC3AFixedSellRiskLimitWithoutPercentageRewrite(t *testing.T) {
	for _, tc := range []struct {
		amount string
		pass   bool
	}{{"750.25", true}, {"750.250001", false}} {
		t.Run(tc.amount, func(t *testing.T) {
			_, s, original, _ := fixture(t)
			p := validationPolicy{Version: 1, Protocol: "PONS_V2_CURVE", MaxOperation: "2000000000000000000", MaxWallet: "4000000000000000000", MaxToken: "4000000000000000000", MaxTotal: "8000000000000000000", MaxGas: "1000000", MaxFee: "3000000000", MaxTip: "1000000000", MinBalance: "1000000000000000", MaxSellBPS: 5000, MaxSlippageBPS: 500, MaxOperations: 100, WindowSeconds: 3600, MaxUnresolved: 1}
			validationSeed(t, s, p)
			h, err := NewWithOptions(s, original.identity, testCredential, original.policy, nil, Options{ManualIntake: true, ValidationBackend: &validationBackend{}, ValidationPolicyVersion: 1})
			if err != nil {
				t.Fatal(err)
			}
			h.now, h.validator.now = original.now, original.now
			req := buy()
			req.Side, req.Amount = "sell", tc.amount
			r := manualRecord(t, call(h, "POST", "/v1/trades", "fixed", encode(req)))
			c, err := contractReader(t, s).Load(context.Background(), h.identity, r.RequestID)
			if tc.pass && (err != nil || c.Request.Amount != tc.amount || c.Request.SellPercent != "") || !tc.pass && !errors.Is(err, ErrPolicy) {
				t.Fatalf("fixed sell ratio check: %v", err)
			}
			noIntakeEconomicWork(t, s)
		})
	}
}

func TestHTTPC3ARestartAndConcurrentReadsConserveDurableState(t *testing.T) {
	d, s, h, b, path := validationAPI(t)
	req := buy()
	req.Side, req.Amount, req.SellPercent = "sell", "", "50"
	r := manualRecord(t, call(h, "POST", "/v1/trades", "restart", encode(req)))
	a := contractReader(t, s)
	first, err := a.Load(context.Background(), h.identity, r.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Close(); err != nil {
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
	reader := contractReader(t, store)
	reader.now = func() time.Time { return testNow.Add(time.Second) }
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := reader.Load(context.Background(), h.identity, r.RequestID)
			if err != nil || !reflect.DeepEqual(c, first) {
				t.Errorf("restart/concurrent read changed contract: %v", err)
			}
		}()
	}
	wg.Wait()
	if first.Input.Units != "750250000" || b.resolves.Load() != 1 || b.completes.Load() != 1 || count(t, store.db, "manual_trade_resolutions") != 1 || count(t, store.db, "manual_trade_validations") != 1 || count(t, store.db, "canary_runtime_audit") != 3 || count(t, store.db, "canary_alert_outbox") != 3 {
		t.Fatal("restart re-resolved or wrote new work")
	}
	noIntakeEconomicWork(t, store)
}

func TestHTTPC3AReadSnapshotIsNotAdmissionOrAuthorization(t *testing.T) {
	_, s, h, _, _ := validationAPI(t)
	r := manualRecord(t, call(h, "POST", "/v1/trades", "no-authority", encode(buy())))
	if _, err := s.db.Exec(`INSERT INTO canary_control_state VALUES(1,4663,'DISABLED',1,1,'test stop','','2026-10-07T12:00:00Z','2026-10-07T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	c, err := contractReader(t, s).Load(context.Background(), h.identity, r.RequestID)
	if err != nil || c.AdmissionAuthorized || c.SendAuthorized {
		t.Fatal("read contract confused with economic gate")
	}
	noIntakeEconomicWork(t, s)
}

func TestHTTPC3AReaderHasNoEconomicOrRPCSurface(t *testing.T) {
	typ := reflect.TypeOf((*ManualExecutionReader)(nil))
	if typ.NumMethod() != 1 || typ.Method(0).Name != "Load" || typ.Elem().NumField() != 2 {
		t.Fatal("reader gained execution dependencies")
	}
	f, err := parser.ParseFile(token.NewFileSet(), "manual_execution_contract.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				for _, forbidden := range []string{"Exec", "ExecContext", "Prepare", "Resolve", "Complete", "Sign", "Submit", "ReplayUnknown", "SendRawTransaction", "ReserveExecution", "IssueCanaryPermit", "ConsumeCanaryPermitAfterGate"} {
					if sel.Sel.Name == forbidden {
						t.Errorf("prohibited call %s", forbidden)
					}
				}
			}
		}
		return true
	})
	for _, imp := range f.Imports {
		if strings.Contains(imp.Path.Value, "/internal/trade\"") || strings.Contains(imp.Path.Value, "net/http") {
			t.Fatal("execution/RPC dependency added")
		}
	}
}

func TestHTTPC3ACorruptOrUnavailableBindingsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statements []string
	}{
		{"step_parameters", []string{`UPDATE execution_steps SET parameters_json='{}'`}},
		{"step_lifecycle", []string{`UPDATE execution_steps SET status='submitted'`}},
		{"operation_lifecycle", []string{`UPDATE operations SET status='manual_resolution'`}},
		{"wallet_disabled", []string{`UPDATE dry_run_wallets SET enabled=0`}},
		{"wallet_identity", []string{`UPDATE dry_run_wallets SET address='` + testToken + `'`}},
		{"lane_frozen", []string{`INSERT INTO execution_wallet_lanes(wallet_id,address,state,updated_at) VALUES('wallet-01','` + testWallet + `','frozen','2026-10-07T12:00:00Z')`}},
		// Corruption fixtures deliberately remove schema guards ONLY in isolated
		// test databases, to prove the reader independently verifies bindings.
		{"wrong_attestation", []string{`DROP TRIGGER canary_gate_attestation_no_update`, `UPDATE canary_gate_attestations SET evidence_hash='` + strings.Repeat("0", 64) + `' WHERE gate='C05'`}},
		{"runtime_drift", []string{`DROP TRIGGER canary_contract_allowlist_no_update`, `UPDATE canary_contract_allowlist SET runtime_code_hash='` + strings.Repeat("0", 66) + `'`}},
		{"token_disallowed", []string{`DROP TRIGGER canary_token_allowlist_no_update`, `UPDATE canary_token_allowlist SET enabled=0`}},
		{"wrong_result_hash", []string{`DROP TRIGGER manual_validation_no_update`, `UPDATE manual_trade_validations SET result_hash='` + strings.Repeat("0", 64) + `'`}},
		{"wrong_resolution_hash", []string{`DROP TRIGGER manual_resolution_no_update`, `UPDATE manual_trade_resolutions SET resolution_hash='` + strings.Repeat("0", 64) + `'`}},
		{"policy_hash", []string{`DROP TRIGGER canary_policy_no_update`, `UPDATE canary_policies SET max_gas_tip_cap='0'`}},
		{"malformed_expiry", []string{`DROP TRIGGER manual_http_source_no_update`, `UPDATE manual_http_order_sources SET expires_at='not-a-time'`}},
		{"noncanonical_expiry", []string{`DROP TRIGGER manual_http_source_no_update`, `UPDATE manual_http_order_sources SET expires_at='2026-10-07T12:00:30+00:00'`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, s, h, _, _ := validationAPI(t)
			r := manualRecord(t, call(h, "POST", "/v1/trades", "corruption", encode(buy())))
			for _, statement := range tc.statements {
				if _, err := s.db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			if c, err := contractReader(t, s).Load(context.Background(), h.identity, r.RequestID); err == nil || c.BindingHash != "" {
				t.Fatal("corrupt binding produced a contract")
			}
			if count(t, s.db, "canary_runtime_audit") != 3 || count(t, s.db, "canary_alert_outbox") != 3 {
				t.Fatal("read failure wrote new lifecycle/audit")
			}
		})
	}
}

func TestHTTPC3AInvalidDependenciesAndClosedDBFailClosed(t *testing.T) {
	if _, err := NewManualExecutionReader(nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil store accepted")
	}
	if _, err := NewManualExecutionReader(&Store{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil DB accepted")
	}
	d, s, h, _, _ := validationAPI(t)
	r := manualRecord(t, call(h, "POST", "/v1/trades", "dependencies", encode(buy())))
	a := contractReader(t, s)
	//lint:ignore SA1012 Deliberately test the adapter's nil-context fail-closed boundary.
	if _, err := a.Load(nil, h.identity, r.RequestID); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil context accepted")
	}
	a.now = nil
	if _, err := a.Load(context.Background(), h.identity, r.RequestID); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil clock accepted")
	}
	a.now = func() time.Time { return testNow }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Load(ctx, h.identity, r.RequestID); err == nil {
		t.Fatal("cancelled operation accepted")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Load(context.Background(), h.identity, r.RequestID); err == nil {
		t.Fatal("closed DB accepted")
	}
}

func TestHTTPC3ASingleReadSnapshotDoesNotAuthorizeAfterDrift(t *testing.T) {
	_, s, h, _, path := validationAPI(t)
	r := manualRecord(t, call(h, "POST", "/v1/trades", "snapshot", encode(buy())))
	other, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	otherDB, err := other.SQLDB(storage.TradeOwner)
	if err != nil {
		t.Fatal(err)
	}
	a := contractReader(t, s)
	reads := 0
	a.now = func() time.Time {
		reads++
		if reads == 1 {
			// The first source SELECT has already established the read snapshot.
			// A separate SQLite handle commits drift while that snapshot is open.
			if _, err := otherDB.Exec(`UPDATE dry_run_wallets SET enabled=0`); err != nil {
				t.Fatal(err)
			}
		}
		return testNow
	}
	c, err := a.Load(context.Background(), h.identity, r.RequestID)
	if err != nil || c.SendAuthorized || c.AdmissionAuthorized {
		t.Fatal("mixed snapshot or accidental authority", err)
	}
	a.now = func() time.Time { return testNow }
	if _, err = a.Load(context.Background(), h.identity, r.RequestID); err == nil {
		t.Fatal("fresh read accepted disabled wallet")
	}
	noIntakeEconomicWork(t, s)
}

func TestHTTPC3ASuccessAndFailurePerformZeroSQLWrites(t *testing.T) {
	_, s, h, _, _ := validationAPI(t)
	r := manualRecord(t, call(h, "POST", "/v1/trades", "zero-write", encode(buy())))
	var before, after int64
	if err := s.db.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	a := contractReader(t, s)
	if _, err := a.Load(context.Background(), h.identity, r.RequestID); err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return testNow.Add(time.Minute) }
	if _, err := a.Load(context.Background(), h.identity, r.RequestID); !errors.Is(err, chainquery.ErrExpired) {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("contract loading wrote to SQLite", before, after, err)
	}
	noIntakeEconomicWork(t, s)
}
