package tradeapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

// Isolated fixture: never real operator authorization or deployment/keys.
// Removing ONLY the no-arm guard tests future loader validation without
// enabling budget/permit/PASS-snapshot paths in migration 018.
func manualInputsFixture(t *testing.T, side string) (*storage.Database, *Store, *API, Record, manualAdmissionScope, string) {
	t.Helper()
	d, s, h, _, path := validationAPI(t)
	req := buy()
	req.Fee.TipGwei = "0"
	if side != "buy" {
		req.Side, req.Amount = "sell", "750.25"
		if side == "sell_percent" {
			req.Amount, req.SellPercent = "", "50"
		}
	}
	r := manualRecord(t, call(h, "POST", "/v1/trades", "b2-inputs", encode(req)))
	var sourceHash string
	if err := s.db.QueryRow(`SELECT policy_hash FROM canary_policies WHERE version=1`).Scan(&sourceHash); err != nil {
		t.Fatal(err)
	}
	p := manualrisk.Policy{ID: "manual-b2", SourcePolicyVersion: 1, SourcePolicyHash: sourceHash, ChainID: 4663, Protocol: "PONS_V2_CURVE",
		InputLimits: []manualrisk.InputLimit{
			{Unit: manualrisk.Unit{ChainID: 4663, Asset: testToken, Decimals: 6, Kind: manualrisk.TradeInput}, MaxOperation: "1000000000", MaxWalletActive: "2000000000", MaxGlobalActive: "3000000000"},
			{Unit: manualrisk.Unit{ChainID: 4663, Asset: "native", Decimals: 18, Kind: manualrisk.TradeInput}, MaxOperation: "1000000000000000000", MaxWalletActive: "2000000000000000000", MaxGlobalActive: "3000000000000000000"}},
		MaxNativeGasPerOperation: "100000000000000", MaxNativeWalletOutlay: "4000000000000000000", MaxNativeGlobalOutlay: "8000000000000000000", MinNativeBalance: "1000000000000000", MaxSellBPS: 5000,
		MaxWalletOperationsWindow: 10, MaxGlobalOperationsWindow: 100, WindowSeconds: 3600}
	raw, digest, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	stamp := testNow.Format(time.RFC3339Nano)
	if _, err = s.db.Exec(`INSERT INTO manual_risk_policies VALUES(?,?,?,?,?,?)`, p.ID, 1, sourceHash, raw, digest, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO canary_control_state VALUES(1,4663,'CONTROLLED_CANARY',1,0,'local fixture','test-only',?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	gateHash, err := manualGateEvidenceHash(context.Background(), s.db, testNow)
	if err != nil {
		t.Fatal(err)
	}
	scope := manualAdmissionScope{AuthorizationID: "b2-auth", AuthorizationEpoch: 1, ManualPolicyID: p.ID, Environment: "b2-local", BuildSHA: strings.Repeat("1", 40), ReleaseID: "fixture-release", DeploymentID: "fixture-deployment"}
	if _, err = s.db.Exec(`INSERT INTO canary_runtime_authorizations(id,epoch,mode,environment,chain_id,policy_version,wallet_id,build_sha,release_id,deployment_id,gate_evidence_set_hash,authorization_ref,max_operations,max_total_input,state,authorized_at,expires_at,created_at,updated_at)
 VALUES(?,1,'CONTROLLED_CANARY',?,4663,1,?,?,?,?,?,'dummy-local-not-a-real-grant',10,'1000000000000000000','PENDING',?,?,?,?)`, scope.AuthorizationID, scope.Environment, h.identity.WalletID, scope.BuildSHA, scope.ReleaseID, scope.DeploymentID, gateHash, stamp, testNow.Add(time.Hour).Format(time.RFC3339Nano), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO canary_authorization_usage VALUES(?,0,'0',?)`, scope.AuthorizationID, stamp); err != nil {
		t.Fatal(err)
	}
	asset, decimals := "native", 18
	if side != "buy" {
		asset, decimals = testToken, 6
	}
	if _, err = s.db.Exec(`INSERT INTO manual_authorization_units VALUES(?,1,?,?,'MANUAL',4663,?,?,'TRADE_INPUT',?)`, scope.AuthorizationID, p.ID, digest, asset, decimals, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER manual_b1_no_arm`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE canary_runtime_authorizations SET state='ARMED' WHERE id=?`, scope.AuthorizationID); err != nil {
		t.Fatal(err)
	}
	return d, s, h, r, scope, path
}

func withManualInputTransaction(t *testing.T, s *Store, f func(context.Context, *sql.Conn)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	f(ctx, conn)
}

func inputFailureCode(err error) string {
	var e *manualInputError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func assertB2NoEconomicWrites(t *testing.T, s *Store) {
	t.Helper()
	for _, table := range []string{"canary_risk_reservations", "canary_admission_decisions", "canary_admission_attempts", "canary_authorization_operation_usage", "canary_send_permits", "canary_runtime_gate_snapshots", "execution_reservations", "execution_wallet_lanes", "transaction_attempts", "transaction_submissions"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("input loading produced economic work", table)
		}
	}
	var used uint64
	var total string
	if err := s.db.QueryRow(`SELECT operation_count,total_input FROM canary_authorization_usage`).Scan(&used, &total); err != nil || used != 0 || total != "0" {
		t.Fatal("input loading consumed allowance", err)
	}
}

func TestHTTPC3B2SameWriterInputsExactBuyFixedSellPercentageSellZeroTip(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			_, s, h, r, scope, _ := manualInputsFixture(t, side)
			prior, err := contractReader(t, s).Load(context.Background(), h.identity, r.RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var before, after int64
			withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
				if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
					t.Fatal(err)
				}
				v, err := loadManualAdmissionInputsTx(ctx, conn, h.identity, r.RequestID, scope, func() time.Time { return testNow })
				if err != nil || encode(v.Contract) != encode(prior) || v.Input.Units != prior.Input.Units || v.Gas.Units != prior.GasBudget.Units || v.Contract.Output.GasTipCap != "0" || v.Contract.AdmissionAuthorized || v.Contract.SendAuthorized || v.Authorization.Epoch != 1 {
					t.Fatal("saved parameters or permission changed", err)
				}
				if side != "buy" && (v.Outlay.Units != v.Gas.Units || v.Input.Unit.Asset != testToken || v.Input.Unit.Decimals != 6) {
					t.Fatal("token sell input added to native outlay")
				}
				if side == "sell_amount" && (v.Contract.Request.Amount != "750.25" || v.Contract.Request.SellPercent != "") {
					t.Fatal("fixed SELL converted to percentage")
				}
				if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil || before != after {
					t.Fatal("loader wrote SQL", err, before, after)
				}
				// The loader did not COMMIT/ROLLBACK the caller's connection. The
				// sole DB connection remains held, so nested DB calls would timeout.
				if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
					t.Fatal("loader ended caller transaction", err)
				}
			})
			backend := h.validator.backend.(*validationBackend)
			if backend.resolves.Load() != 1 || backend.completes.Load() != 1 {
				t.Fatal("loader re-resolved/re-simulated durable parameters")
			}
			assertB2NoEconomicWrites(t, s)
		})
	}
}

func TestHTTPC3B2StopHighestPriorityAndMissingControlFailClosed(t *testing.T) {
	for _, tc := range []struct{ mutation, code string }{
		{`UPDATE canary_control_state SET emergency_stopped=1`, "EMERGENCY_STOPPED"},
		{`DELETE FROM canary_control_state`, "CONTROL_UNAVAILABLE"},
		{`UPDATE canary_control_state SET mode='DISABLED'`, "CANARY_DISABLED"},
		{`UPDATE canary_control_state SET updated_at='bad-time'`, "CONTROL_UNVERIFIABLE"},
		{`UPDATE canary_control_state SET effective_at='2026-10-07T12:01:00Z'`, "CONTROL_UNVERIFIABLE"},
	} {
		t.Run(tc.code+tc.mutation, func(t *testing.T) {
			_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
			if _, err := s.db.Exec(tc.mutation); err != nil {
				t.Fatal(err)
			}
			withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
				// Deliberately invalid secondary identity cannot hide a stop.
				if tc.code == "EMERGENCY_STOPPED" {
					scope.BuildSHA = "bad"
				}
				v, err := loadManualAdmissionInputsTx(ctx, conn, h.identity, r.RequestID, scope, func() time.Time { return testNow })
				if inputFailureCode(err) != tc.code || v.Contract.BindingHash != "" {
					t.Fatal("wrong priority/fail-closed result", err)
				}
			})
			// C3-A/historical reads are not policy blocked by stop/no auth.
			if _, err := contractReader(t, s).Load(context.Background(), h.identity, r.RequestID); err != nil {
				t.Fatal("stop blocked detached read", err)
			}
			assertB2NoEconomicWrites(t, s)
		})
	}
}

func TestHTTPC3B2ScopeEpochDeploymentAndSourceDriftRejected(t *testing.T) {
	for _, change := range []func(*manualAdmissionScope){
		func(s *manualAdmissionScope) { s.AuthorizationID = "absent" },
		func(s *manualAdmissionScope) { s.AuthorizationEpoch++ },
		func(s *manualAdmissionScope) { s.ManualPolicyID = "absent" },
		func(s *manualAdmissionScope) { s.Environment = "other" },
		func(s *manualAdmissionScope) { s.BuildSHA = strings.Repeat("2", 40) },
		func(s *manualAdmissionScope) { s.ReleaseID = "other" },
		func(s *manualAdmissionScope) { s.DeploymentID = "other" },
	} {
		_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
		change(&scope)
		withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
			if v, err := loadManualAdmissionInputsTx(ctx, conn, h.identity, r.RequestID, scope, func() time.Time { return testNow }); err == nil || v.Contract.BindingHash != "" {
				t.Fatal("scope drift accepted")
			}
		})
		assertB2NoEconomicWrites(t, s)
	}
}

func TestHTTPC3B2CorruptPolicyAuthorizationUnitAndBudgetRejected(t *testing.T) {
	for _, mutation := range [][]string{
		{`UPDATE canary_control_state SET policy_version=2`},
		{`UPDATE canary_runtime_authorizations SET state='REVOKED'`},
		{`DROP TRIGGER manual_authorization_unit_no_delete`, `DELETE FROM manual_authorization_units`},
		{`DROP TRIGGER manual_authorization_unit_no_update`, `UPDATE manual_authorization_units SET input_asset='` + testToken + `',input_decimals=6`},
		{`DROP TRIGGER manual_authorization_unit_no_update`, `UPDATE manual_authorization_units SET authorization_epoch=2`},
		{`DROP TRIGGER manual_risk_policy_no_update`, `UPDATE manual_risk_policies SET policy_hash='` + strings.Repeat("0", 64) + `'`},
		{`DROP TRIGGER canary_runtime_authorization_identity_immutable`, `UPDATE canary_runtime_authorizations SET gate_evidence_set_hash='` + strings.Repeat("0", 64) + `'`},
		{`DROP TRIGGER canary_runtime_authorization_identity_immutable`, `UPDATE canary_runtime_authorizations SET expires_at='2026-10-07T12:00:00Z'`},
		{`DROP TRIGGER canary_runtime_authorization_identity_immutable`, `UPDATE canary_runtime_authorizations SET expires_at='2026-10-07T13:00:00+00:00'`},
		{`DROP TRIGGER manual_b1_no_usage_update`, `UPDATE canary_authorization_usage SET operation_count=1`},
		{`DROP TRIGGER manual_b1_no_usage_update`, `UPDATE canary_authorization_usage SET total_input='1'`},
	} {
		_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
		// Corruption fixtures may deliberately bypass identity FKs/triggers.
		if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
			t.Fatal(err)
		}
		for _, statement := range mutation {
			if _, err := s.db.Exec(statement); err != nil {
				t.Fatal(statement, err)
			}
		}
		withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
			var before, after int64
			if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if v, err := loadManualAdmissionInputsTx(ctx, conn, h.identity, r.RequestID, scope, func() time.Time { return testNow }); err == nil || v.Contract.BindingHash != "" {
				t.Fatal("corrupt policy/auth/unit/budget accepted", mutation)
			}
			if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil || after != before {
				t.Fatal("corruption rejection wrote state", err)
			}
		})
	}
}

func TestHTTPC3B2FinalClockExpiryAndBackwardClockRejected(t *testing.T) {
	for _, final := range []time.Time{testNow.Add(30 * time.Second), testNow.Add(time.Hour), testNow.Add(-time.Nanosecond), {}} {
		_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
		withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
			calls := 0
			v, err := loadManualAdmissionInputsTx(ctx, conn, h.identity, r.RequestID, scope, func() time.Time {
				calls++
				if calls == 4 {
					return final
				}
				return testNow
			})
			if inputFailureCode(err) != "FINAL_TIME_INVALID_OR_EXPIRED" || calls != 4 || v.Contract.BindingHash != "" {
				t.Fatal("late expiry/backward clock granted inputs", err, calls)
			}
		})
		assertB2NoEconomicWrites(t, s)
	}
}

func TestHTTPC3B2LoaderOnlySelectsNoNestedTransactionOrEconomicSurface(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "manual_admission_inputs.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			switch sel.Sel.Name {
			case "Begin", "BeginTx", "Commit", "Rollback", "Exec", "ExecContext", "Load", "Sign", "Submit", "ReplayUnknown", "SendRawTransaction", "PendingNonce", "ReserveExecution":
				t.Fatal("loader gained nested transaction/write/economic call", sel.Sel.Name)
			}
		}
		return true
	})
}

func TestHTTPC3B2SameTransactionSeesUncommittedStopAndRollback(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
	withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
		if _, err := conn.ExecContext(ctx, `UPDATE canary_control_state SET emergency_stopped=1`); err != nil {
			t.Fatal(err)
		}
		if _, err := loadManualAdmissionInputsTx(ctx, conn, h.identity, r.RequestID, scope, func() time.Time { return testNow }); inputFailureCode(err) != "EMERGENCY_STOPPED" {
			t.Fatal("loader used another connection or stale snapshot", err)
		}
	})
	var stop int
	if err := s.db.QueryRow(`SELECT emergency_stopped FROM canary_control_state`).Scan(&stop); err != nil || stop != 0 {
		t.Fatal("loader committed caller's uncommitted write", err)
	}
	assertB2NoEconomicWrites(t, s)
}

func TestHTTPC3B2RestartPreservesExactInputsNoRPCOrNewExpiry(t *testing.T) {
	d, s, h, r, scope, path := manualInputsFixture(t, "sell_amount")
	var before manualAdmissionInputs
	withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
		var err error
		before, err = loadManualAdmissionInputsTx(ctx, conn, h.identity, r.RequestID, scope, func() time.Time { return testNow })
		if err != nil {
			t.Fatal(err)
		}
	})
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err = fresh.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	freshStore, err := NewStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	withManualInputTransaction(t, freshStore, func(ctx context.Context, conn *sql.Conn) {
		after, err := loadManualAdmissionInputsTx(ctx, conn, h.identity, r.RequestID, scope, func() time.Time { return testNow.Add(time.Second) })
		if err != nil || !reflect.DeepEqual(before.Contract, after.Contract) || before.Input != after.Input || before.PolicyHash != after.PolicyHash || before.Authorization != after.Authorization {
			t.Fatal("restart changed contract/policy/units/expiry or budget", err)
		}
	})
	assertB2NoEconomicWrites(t, freshStore)
}

func TestHTTPC3B2PolicyHashSQLShapeDoesNotBecomePermission(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
	var raw string
	if err := s.db.QueryRow(`SELECT policy_json FROM manual_risk_policies WHERE id=?`, scope.ManualPolicyID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	// A fresh, shape-valid policy is allowed by SQL with a false checksum;
	// no trigger is disabled. Go must reject this before authorization loading.
	newID := "manual-false-hash"
	newRaw := strings.Replace(raw, `"id":"manual-b2"`, `"id":"`+newID+`"`, 1)
	if _, err := s.db.Exec(`INSERT INTO manual_risk_policies SELECT ?,source_policy_version,source_policy_hash,?,?,created_at FROM manual_risk_policies WHERE id=?`, newID, newRaw, strings.Repeat("0", 64), scope.ManualPolicyID); err != nil {
		t.Fatal(err)
	}
	scope.ManualPolicyID = newID
	withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
		if _, err := loadManualAdmissionInputsTx(ctx, conn, h.identity, r.RequestID, scope, func() time.Time { return testNow }); inputFailureCode(err) != "MANUAL_POLICY_INTEGRITY_INVALID" {
			t.Fatal("SQL shape-valid false hash became trusted policy", err)
		}
	})
	assertB2NoEconomicWrites(t, s)
}

func TestHTTPC3B2AuthAndSourceClockBoundary(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
	for _, tc := range []struct {
		offset  time.Duration
		allowed bool
	}{{30*time.Second - time.Nanosecond, true}, {30 * time.Second, false}, {30*time.Second + time.Nanosecond, false}} {
		withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
			_, err := loadManualAdmissionInputsTx(ctx, conn, h.identity, r.RequestID, scope, func() time.Time { return testNow.Add(tc.offset) })
			if (err == nil) != tc.allowed {
				t.Fatal("source TTL boundary", tc.offset, err)
			}
		})
	}
	// Isolated corruption changes only immutable auth expiry to match source.
	if _, err := s.db.Exec(`DROP TRIGGER canary_runtime_authorization_identity_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE canary_runtime_authorizations SET expires_at=?`, r.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
		c, err := loadManualExecutionContractTx(ctx, conn, h.identity, r.RequestID, func() time.Time { return testNow })
		if err != nil {
			t.Fatal(err)
		}
		p, ph, err := loadManualPolicy(ctx, conn, scope.ManualPolicyID, c, testNow)
		if err != nil {
			t.Fatal(err)
		}
		gh, err := manualGateEvidenceHash(ctx, conn, testNow)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			offset  time.Duration
			allowed bool
		}{{30*time.Second - time.Nanosecond, true}, {30 * time.Second, false}, {30*time.Second + time.Nanosecond, false}} {
			_, err := loadManualAuthorization(ctx, conn, scope, c, p, ph, gh, testNow.Add(tc.offset))
			if (err == nil) != tc.allowed {
				t.Fatal("auth absolute TTL boundary", tc.offset, err)
			}
		}
	})
	assertB2NoEconomicWrites(t, s)
}

func TestHTTPC3B2TypedPolicyRiskBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, side, code string
		mutate           func(*manualrisk.Policy)
	}{
		{"input_plus_one", "buy", "MANUAL_OPERATION_INPUT_LIMIT", func(p *manualrisk.Policy) { p.InputLimits[1].MaxOperation = "9999999999999999" }},
		{"gas_plus_one", "buy", "MANUAL_GAS_LIMIT", func(p *manualrisk.Policy) { p.MaxNativeGasPerOperation = "99999999999999" }},
		{"retained", "buy", "MANUAL_RETAINED_BALANCE_LIMIT", func(p *manualrisk.Policy) { p.MinNativeBalance = "2000000000000000000" }},
		{"combined_outlay", "buy", "MANUAL_OPERATION_OUTLAY_LIMIT", func(p *manualrisk.Policy) { p.MaxNativeWalletOutlay = "100000000000000" }},
		{"fixed_sell", "sell_amount", "MANUAL_SELL_LIMIT", func(p *manualrisk.Policy) { p.MaxSellBPS = 4999 }},
		{"exact_limits", "buy", "", func(p *manualrisk.Policy) {
			p.InputLimits[1].MaxOperation = "10000000000000000"
			p.MaxNativeWalletOutlay = "10100000000000000"
			p.MinNativeBalance = "1989900000000000000"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, s, h, r, scope, _ := manualInputsFixture(t, tc.side)
			var raw string
			if err := s.db.QueryRow(`SELECT policy_json FROM manual_risk_policies`).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var p manualrisk.Policy
			if err := json.Unmarshal([]byte(raw), &p); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&p)
			newRaw, digest, err := p.Canonical()
			if err != nil {
				t.Fatal(err)
			}
			// Explicit controlled corruption of immutable metadata; never an
			// operator update API. Pure policy is canonical with its new digest.
			for _, sql := range []string{`DROP TRIGGER manual_risk_policy_no_update`, `DROP TRIGGER manual_authorization_unit_no_update`} {
				if _, err := s.db.Exec(sql); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(`UPDATE manual_risk_policies SET policy_json=?,policy_hash=?`, newRaw, digest); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`UPDATE manual_authorization_units SET manual_policy_hash=?`, digest); err != nil {
				t.Fatal(err)
			}
			withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
				_, err := loadManualAdmissionInputsTx(ctx, conn, h.identity, r.RequestID, scope, func() time.Time { return testNow })
				if tc.code == "" && err != nil || tc.code != "" && inputFailureCode(err) != tc.code {
					t.Fatal("typed risk boundary", err)
				}
			})
			assertB2NoEconomicWrites(t, s)
		})
	}
}
