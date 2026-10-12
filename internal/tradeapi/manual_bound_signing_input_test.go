package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func boundSigningReader(t *testing.T, s *Store, scope manualAdmissionScope) *ManualBoundSigningInputReader {
	t.Helper()
	a, err := NewManualBoundSigningInputReader(s, *httpAdmissionScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return testNow }
	return a
}

func boundSigningFixture(t *testing.T, side string) (*storage.Database, *Store, *API, Record, manualAdmissionScope, string) {
	t.Helper()
	d, s, h, r, scope, path := bindingFixture(t, side)
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	return d, s, h, r, scope, path
}

func requireBoundInputDenied(t *testing.T, a *ManualBoundSigningInputReader, id Identity, request string) error {
	t.Helper()
	before := admittedDatabaseSnapshot(t, a.store.db)
	v, err := a.Load(context.Background(), id, request)
	if err == nil || !reflect.DeepEqual(v, ManualBoundSigningInput{}) {
		t.Fatal("denial returned candidate or permission", v, err)
	}
	if admittedDatabaseSnapshot(t, a.store.db) != before {
		t.Fatal("denied read repaired/released/consumed durable history")
	}
	return err
}

func TestHTTPM3B1ExactEnvelopeAndNoWrites(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, side)
			cmd, err := scanManualCommand(s.db.QueryRow(`SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE request_id=?`, r.RequestID))
			if err != nil {
				t.Fatal(err)
			}
			b := bindingCommand(t, s, r)
			a := boundSigningReader(t, s, scope)
			before := admittedDatabaseSnapshot(t, s.db)
			for i := 0; i < 3; i++ {
				v, err := a.Load(context.Background(), h.identity, r.RequestID)
				o := r.Validation.Output
				want := ManualUnsignedEnvelope{ChainID: 4663, From: o.From, To: o.To, Value: o.Value, Data: o.Data, GasLimit: o.GasLimit, GasFeeCap: o.GasFeeCap, GasTipCap: o.GasTipCap}
				if err != nil || v.Status != "BOUND_SIGNING_INPUT_CANDIDATE_NOT_PERMISSION" || encode(v.Contract) != cmd.ContractRaw || v.Contract.BindingHash != b.ContractHash || v.Contract.Output != o || v.Envelope != want || v.Envelope.GasTipCap != "0" || v.Contract.ExpiresAt != r.ExpiresAt || v.ExecutionStepID != b.ExecutionStep || v.BoundAt != b.CheckedAt || v.Scope != *httpAdmissionScope(scope) || v.ReservationID != cmd.Reservation || v.ReservationState != "reserved" || v.ManualPolicyHash != cmd.PolicyHash || v.GateEvidenceSetHash != cmd.GateHash || v.UsedOperations != 1 || v.UsedInput.Units != v.Contract.Input.Units {
					t.Fatal("saved input/envelope drift", v, err)
				}
				if v.ExecutionAuthorized || v.SigningAuthorized || v.SendAuthorized || v.Contract.AdmissionAuthorized || v.Contract.SendAuthorized {
					t.Fatal("candidate became permission")
				}
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("successful read changed durable state")
			}
			backend := h.validator.backend.(*validationBackend)
			if backend.resolves.Load() != 1 || backend.completes.Load() != 1 {
				t.Fatal("reader requoted/resolved/simulated")
			}
			assertBinding(t, s, r, 1)
			if _, err := admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID); err == nil {
				t.Fatal("M1 regained access to bound work")
			}
		})
	}
}

func TestHTTPM3B1UnboundAndInvalidCompositionFailClosed(t *testing.T) {
	if _, err := NewManualBoundSigningInputReader(nil, ManualAdmissionScope{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	var absent *ManualBoundSigningInputReader
	if _, err := absent.Load(context.Background(), Identity{}, "request"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	d, s, h, r, scope, _ := bindingFixture(t, "buy")
	if _, err := NewManualBoundSigningInputReader(s, ManualAdmissionScope{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	a := boundSigningReader(t, s, scope)
	requireBoundInputDenied(t, a, h.identity, r.RequestID)
	//lint:ignore SA1012 Deliberately exercise the nil-context boundary.
	if _, err := a.Load(nil, h.identity, r.RequestID); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	v, err := a.Load(context.Background(), h.identity, r.RequestID)
	if !reflect.DeepEqual(v, ManualBoundSigningInput{}) {
		t.Fatal("closed DB produced output")
	}
	requireOperational503(t, err)
}

func TestHTTPM3B1CallerScopeAndDetachedValues(t *testing.T) {
	_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
	a := boundSigningReader(t, s, scope)
	for _, field := range []string{"PrincipalID", "WalletID", "WalletAddress"} {
		id := h.identity
		value := "outside"
		if field == "WalletAddress" {
			value = "0x" + strings.Repeat("9", 40)
		}
		reflect.ValueOf(&id).Elem().FieldByName(field).SetString(value)
		if err := requireBoundInputDenied(t, a, id, r.RequestID); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-caller input exposed", field, err)
		}
	}
	for _, field := range []string{"AuthorizationID", "AuthorizationEpoch", "ManualPolicyID", "Environment", "BuildSHA", "ReleaseID", "DeploymentID"} {
		wrong := *httpAdmissionScope(scope)
		f := reflect.ValueOf(&wrong).Elem().FieldByName(field)
		if field == "AuthorizationEpoch" {
			f.SetUint(2)
		} else if field == "BuildSHA" {
			f.SetString(strings.Repeat("2", 40))
		} else {
			f.SetString("outside")
		}
		reader, err := NewManualBoundSigningInputReader(s, wrong)
		if err != nil {
			t.Fatal(err)
		}
		reader.now = h.now
		if err := requireBoundInputDenied(t, reader, h.identity, r.RequestID); !errors.Is(err, ErrConflict) {
			t.Fatal("scope drift accepted", field, err)
		}
	}
	v, err := a.Load(context.Background(), h.identity, r.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	want := encode(v)
	v.Contract.Request.Fee.TipGwei = "777"
	v.Contract.Output.Data = "0x00"
	v.Envelope.GasFeeCap = "777"
	v.ManualPolicy.InputLimits[0].MaxOperation = "777"
	v.Scope.DeploymentID = "outside"
	fresh, err := a.Load(context.Background(), h.identity, r.RequestID)
	if err != nil || encode(fresh) != want {
		t.Fatal("returned object aliased durable parameters", err)
	}
}

func TestHTTPM3B1CapEqualityNoSecondConsumption(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixtureBudget(t, "buy", 1, "10000000000000000")
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	a := boundSigningReader(t, s, scope)
	before := admittedDatabaseSnapshot(t, s.db)
	for i := 0; i < 5; i++ {
		v, err := a.Load(context.Background(), h.identity, r.RequestID)
		if err != nil || v.UsedOperations != 1 || v.UsedInput.Units != "10000000000000000" {
			t.Fatal("already consumed cap equality rejected", v, err)
		}
	}
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("reader recharged/refunded budget")
	}
}

func TestHTTPM3B1TTLFinalClockAndStopPriority(t *testing.T) {
	_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
	expiry, err := canonicalManualTime(r.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		first, last time.Time
		accept      bool
	}{
		{"minus1ns", expiry.Add(-time.Nanosecond), expiry.Add(-time.Nanosecond), true},
		{"equal", expiry, expiry, false}, {"plus1ns", expiry.Add(time.Nanosecond), expiry.Add(time.Nanosecond), false},
		{"crossDuringRead", testNow, expiry, false}, {"reversal", testNow, testNow.Add(-time.Nanosecond), false},
		{"zero", time.Time{}, time.Time{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := boundSigningReader(t, s, scope)
			clocks := 0
			a.now = func() time.Time {
				clocks++
				if clocks == 1 {
					return tc.first
				}
				return tc.last
			}
			before := admittedDatabaseSnapshot(t, s.db)
			v, err := a.Load(context.Background(), h.identity, r.RequestID)
			if (err == nil) != tc.accept || !tc.accept && !reflect.DeepEqual(v, ManualBoundSigningInput{}) {
				t.Fatal("TTL/time fail-open", v, err)
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("TTL path changed expiry/usage/lane")
			}
		})
	}
	if _, err := s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
		t.Fatal(err)
	}
	a := boundSigningReader(t, s, scope)
	a.scope.DeploymentID = "outside"
	a.now = func() time.Time { return expiry }
	if err := requireBoundInputDenied(t, a, h.identity, r.RequestID); inputFailureCode(err) != "EMERGENCY_STOPPED" {
		t.Fatal("stop priority lost", err)
	}
}

func TestHTTPM3B1AuthorizationExpiryBoundary(t *testing.T) {
	_, s, h, r, scope, _ := bindingFixture(t, "buy")
	expiry := testNow.Add(10 * time.Second)
	if _, err := s.db.Exec(`DROP TRIGGER canary_runtime_authorization_identity_immutable; UPDATE canary_runtime_authorizations SET expires_at=?`, expiry.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		first, last time.Time
		accept      bool
	}{
		{expiry.Add(-time.Nanosecond), expiry.Add(-time.Nanosecond), true},
		{expiry, expiry, false}, {expiry.Add(time.Nanosecond), expiry.Add(time.Nanosecond), false},
		{testNow, expiry, false},
	} {
		a := boundSigningReader(t, s, scope)
		clocks := 0
		a.now = func() time.Time {
			clocks++
			if clocks == 1 {
				return tc.first
			}
			return tc.last
		}
		before := admittedDatabaseSnapshot(t, s.db)
		v, err := a.Load(context.Background(), h.identity, r.RequestID)
		if (err == nil) != tc.accept || !tc.accept && !reflect.DeepEqual(v, ManualBoundSigningInput{}) {
			t.Fatal("auth expiry fail-open", v, err)
		}
		if admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("auth expiry changed history")
		}
	}
}

func TestHTTPM3B1MutableControlsDenyHistoryRemains(t *testing.T) {
	for name, change := range map[string]string{
		"stop":       `UPDATE canary_control_state SET emergency_stopped=1`,
		"disabled":   `UPDATE canary_control_state SET mode='DISABLED'`,
		"revoke":     `UPDATE canary_runtime_authorizations SET state='REVOKED'`,
		"expired":    `UPDATE canary_runtime_authorizations SET state='EXPIRED'`,
		"exhausted":  `UPDATE canary_runtime_authorizations SET state='EXHAUSTED'`,
		"frozen":     `UPDATE canary_risk_reservations SET state='frozen'`,
		"released":   `UPDATE canary_risk_reservations SET state='released'`,
		"committed":  `UPDATE canary_risk_reservations SET state='committed'`,
		"usedFrozen": `UPDATE canary_authorization_operation_usage SET state='FROZEN'`,
		"usedDone":   `UPDATE canary_authorization_operation_usage SET state='CONSUMED'`,
		"usedFreed":  `UPDATE canary_authorization_operation_usage SET state='RELEASED'`,
		"walletOff":  `UPDATE dry_run_wallets SET enabled=0`,
	} {
		t.Run(name, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			if _, err := s.db.Exec(change); err != nil {
				t.Fatal(err)
			}
			requireBoundInputDenied(t, boundSigningReader(t, s, scope), h.identity, r.RequestID)
			before := admittedDatabaseSnapshot(t, s.db)
			if history, err := s.manualAdmissionHistory(context.Background(), h.identity, r.RequestID); err != nil || history == nil || history.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" {
				t.Fatal("denial blocked admission history", err)
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("history read changed state")
			}
		})
	}
}

func TestHTTPM3B1UnverifiableSourcesFailClosed(t *testing.T) {
	// Guard removals inject corruption in disposable fixtures only. No schema
	// guard is changed in this slice, and no corrupted history is repaired.
	for name, change := range map[string]string{
		"controlMissing":     `DELETE FROM canary_control_state`,
		"walletAllowlistOff": `DROP TRIGGER canary_wallet_allowlist_no_update; UPDATE canary_wallet_allowlist SET enabled=0`,
		"contractOff":        `DROP TRIGGER canary_contract_allowlist_no_update; UPDATE canary_contract_allowlist SET enabled=0`,
		"runtimeDrift":       `DROP TRIGGER canary_contract_allowlist_no_update; UPDATE canary_contract_allowlist SET runtime_code_hash='0x'||printf('%064d',0)`,
		"tokenOff":           `DROP TRIGGER canary_token_allowlist_no_update; UPDATE canary_token_allowlist SET enabled=0`,
		"gateMissing":        `DROP TRIGGER canary_gate_attestation_no_delete; DELETE FROM canary_gate_attestations WHERE gate='C05'`,
		"expiryMalformed":    `DROP TRIGGER manual_http_source_no_update; UPDATE manual_http_order_sources SET expires_at='invalid'`,
		"expiryMissing":      `DROP TRIGGER manual_http_source_no_update; UPDATE manual_http_order_sources SET expires_at=''`,
		"expiryNoncanonical": `DROP TRIGGER manual_http_source_no_update; UPDATE manual_http_order_sources SET expires_at=replace(expires_at,'Z','+00:00')`,
		"bindingCorrupt":     `DROP TRIGGER manual_execution_binding_no_update; UPDATE manual_execution_bindings SET contract_hash='corrupt'`,
		"bindingMissing":     `DROP TRIGGER manual_execution_binding_no_delete; DELETE FROM manual_execution_bindings`,
		"auditMissing":       `PRAGMA foreign_keys=OFF; DROP TRIGGER canary_runtime_audit_no_delete; DELETE FROM canary_runtime_audit WHERE event_type='MANUAL_EXECUTION_BOUND'; PRAGMA foreign_keys=ON`,
		"integrityMissing":   `DROP VIEW manual_execution_binding_integrity`,
	} {
		t.Run(name, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			if _, err := s.db.Exec(change); err != nil {
				t.Fatal(err)
			}
			requireBoundInputDenied(t, boundSigningReader(t, s, scope), h.identity, r.RequestID)
		})
	}
}

func TestHTTPM3B1AttemptAndOtherWalletWorkReject(t *testing.T) {
	for _, kind := range []string{"ownAttempt", "otherStep", "operationOnlyStep", "financialClaim"} {
		t.Run(kind, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			var query string
			switch kind {
			case "ownAttempt":
				// Inert failed legacy metadata, not nonce allocation or signing.
				query = `DROP TRIGGER manual_execution_no_attempt; INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at)
 SELECT 'stale-attempt',validation_step_id,'0','failed',checked_at FROM manual_execution_bindings`
			default:
				bindingLegacyReferences(t, s)
				if kind == "otherStep" {
					query = `UPDATE execution_steps SET wallet_id=(SELECT wallet_id FROM manual_execution_bindings),status='manual_resolution' WHERE id='alias-step'`
				} else if kind == "operationOnlyStep" {
					query = `UPDATE operations SET wallet_id=(SELECT wallet_id FROM manual_execution_bindings) WHERE id='alias-operation'; UPDATE execution_steps SET wallet_id=NULL,status='manual_resolution' WHERE id='alias-step'`
				} else {
					query = `UPDATE execution_reservations SET wallet_id=(SELECT wallet_id FROM manual_execution_bindings),status='frozen' WHERE operation_id='alias-operation'`
				}
			}
			if _, err := s.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			if err := requireBoundInputDenied(t, boundSigningReader(t, s, scope), h.identity, r.RequestID); !errors.Is(err, ErrWallet) {
				t.Fatal("attempt/other active wallet work accepted", kind, err)
			}
		})
	}
}

func TestHTTPM3B1RetainedPollutionNotRepaired(t *testing.T) {
	d, s, h, r, scope, path := bindingRetainedLaneFixture(t)
	requireBoundInputDenied(t, boundSigningReader(t, s, scope), h.identity, r.RequestID)
	before := admittedDatabaseSnapshot(t, s.db)
	_, s = bindingReopen(t, d, path)
	requireBoundInputDenied(t, boundSigningReader(t, s, scope), h.identity, r.RequestID)
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("reopen or reader repaired retained pollution")
	}
}

func TestHTTPM3B1QueryOnlyAndCallerTransaction(t *testing.T) {
	_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
	if _, err := s.db.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	if _, err := boundSigningReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID); err != nil {
		t.Fatal("read attempted write", err)
	}
	ctx := context.Background()
	conn, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Rollback()
	{
		var old, last int64
		if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&old); err != nil {
			t.Fatal(err)
		}
		if _, err := loadManualBoundSigningInputTx(ctx, conn, h.identity, r.RequestID, *httpAdmissionScope(scope), h.now); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&last); err != nil || last != old {
			t.Fatal("helper wrote SQL", old, last, err)
		}
		if err := conn.Commit(); err != nil {
			t.Fatal("helper ended caller transaction", err)
		}
	}
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("query-only read changed database")
	}
}

func TestHTTPM3B1EveryReadOperationalCause(t *testing.T) {
	_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
	load := func(q queryRow) error {
		v, err := loadManualBoundSigningInputTx(context.Background(), q, h.identity, r.RequestID, *httpAdmissionScope(scope), h.now)
		if err != nil && !reflect.DeepEqual(v, ManualBoundSigningInput{}) {
			t.Fatal("operational fault returned output")
		}
		return err
	}
	counter := &operationalFaultQuery{db: s.db}
	if err := load(counter); err != nil {
		t.Fatal(err)
	}
	closed, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err = closed.Close(); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	for _, fault := range []string{"cancel", "deadline", "closed", "query"} {
		for nth := 1; nth <= counter.queries; nth++ {
			t.Run(fmt.Sprintf("%s/read_%d", fault, nth), func(t *testing.T) {
				requireOperational503(t, load(&operationalFaultQuery{db: s.db, failAt: nth, fault: fault, closed: closed}))
			})
		}
	}
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("fault changed durable history")
	}
}

func TestHTTPM3B1ReopenAndTwoHandleConcurrentReads(t *testing.T) {
	d, s, h, r, scope, path := boundSigningFixture(t, "sell_percent")
	before := admittedDatabaseSnapshot(t, s.db)
	first, err := boundSigningReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	_, s = bindingReopen(t, d, path)
	other, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	s2, err := NewStore(other)
	if err != nil {
		t.Fatal(err)
	}
	a, b := boundSigningReader(t, s, scope), boundSigningReader(t, s2, scope)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reader := a
			if i%2 != 0 {
				reader = b
			}
			v, err := reader.Load(context.Background(), h.identity, r.RequestID)
			if err != nil || encode(v) != encode(first) {
				t.Errorf("concurrent/restarted input changed: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("reopen/read minted expiry, claim or usage")
	}
	assertBinding(t, s, r, 1)
}

func TestHTTPM3B1SnapshotStopIsNotPermission(t *testing.T) {
	_, s, h, r, scope, path := boundSigningFixture(t, "buy")
	other, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	s2, err := NewStore(other)
	if err != nil {
		t.Fatal(err)
	}
	a := boundSigningReader(t, s, scope)
	clocks := 0
	var stoppedSnapshot string
	a.now = func() time.Time {
		clocks++
		if clocks == 2 {
			if _, err := s2.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`); err != nil {
				t.Fatal("separate WAL writer could not commit stop", err)
			}
			stoppedSnapshot = admittedDatabaseSnapshot(t, s2.db)
		}
		return testNow
	}
	v, err := a.Load(context.Background(), h.identity, r.RequestID)
	if err != nil || clocks != 2 || v.ExecutionAuthorized || v.SigningAuthorized || v.SendAuthorized || v.Contract.AdmissionAuthorized || v.Contract.SendAuthorized {
		t.Fatal("old snapshot became permission", v, err)
	}
	if admittedDatabaseSnapshot(t, s.db) != stoppedSnapshot {
		t.Fatal("snapshot reader wrote after stop")
	}
	a.now = h.now
	if err := requireBoundInputDenied(t, a, h.identity, r.RequestID); inputFailureCode(err) != "EMERGENCY_STOPPED" {
		t.Fatal("fresh snapshot ignored committed stop", err)
	}
}

func TestHTTPM3B1NoEconomicDependenciesOrReachability(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "manual_bound_signing_input.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		for _, prohibited := range []string{"internal/trade\"", "ethclient", "/rpc", "trade-sdk", "crypto/ecdsa", "core/types"} {
			if strings.Contains(imp.Path.Value, prohibited) {
				t.Fatal("economic import", imp.Path.Value)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			switch sel.Sel.Name {
			case "Exec", "ExecContext", "SignTransaction", "Prepare", "Submit", "ReplayUnknown", "SendRawTransaction", "ReserveExecution", "PendingNonce", "Encrypt", "Decrypt", "NewTx":
				t.Errorf("economic/write dependency: %s", sel.Sel.Name)
			}
		}
		return true
	})
	typ := reflect.TypeOf(ManualUnsignedEnvelope{})
	for _, name := range []string{"Nonce", "Signature", "Raw", "TxHash", "Authorization"} {
		if _, ok := typ.FieldByName(name); ok {
			t.Fatal("nonce/sign/send capability field", name)
		}
	}
	_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
	if _, err := s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'; CREATE TRIGGER manual_b1_no_arm BEFORE UPDATE OF state ON canary_runtime_authorizations WHEN NEW.state='ARMED' AND EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.id) BEGIN SELECT RAISE(ABORT,'manual not authorized'); END`); err != nil {
		t.Fatal(err)
	}
	requireBoundInputDenied(t, boundSigningReader(t, s, scope), h.identity, r.RequestID)
	for _, name := range []string{"manual_b1_no_arm", "manual_b1_no_permit", "manual_b1_no_pass_snapshot", "manual_execution_no_attempt", "manual_execution_lane_no_update", "manual_binding_lane_retained_update", "manual_binding_lane_retained_delete"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&n); err != nil || n != 1 {
			t.Fatal("closed guard removed", name, err)
		}
	}
	assertBinding(t, s, r, 1)
}

// The competing SELECT on a single-connection pool completes only after
// database/sql rolls back the canceled read transaction. No sleeps or writes.
func TestHTTPM3B1PublicLateCancellationPreservesCause(t *testing.T) {
	for _, kind := range []string{"cancel", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "sell_percent")
			s.db.SetMaxOpenConns(1)
			s.db.SetMaxIdleConns(1)
			if _, err := s.db.Exec(`PRAGMA query_only=ON`); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			a := boundSigningReader(t, s, scope)
			var ctx context.Context
			var cancel context.CancelFunc
			want := context.Canceled
			if kind == "deadline" {
				ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
				want = context.DeadlineExceeded
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			clocks := 0
			a.now = func() time.Time {
				clocks++
				if clocks == 2 {
					if kind == "cancel" {
						cancel()
					}
					<-ctx.Done()
					barrier, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					var one int
					if err := s.db.QueryRowContext(barrier, `SELECT 1`).Scan(&one); err != nil || one != 1 {
						t.Fatal("automatic rollback barrier", one, err)
					}
				}
				return testNow
			}
			v, err := a.Load(ctx, h.identity, r.RequestID)
			if clocks != 2 || !errors.Is(ctx.Err(), want) || !errors.Is(err, want) || !errors.Is(err, sql.ErrTxDone) || !reflect.DeepEqual(v, ManualBoundSigningInput{}) {
				t.Fatalf("commit cancellation cause/output: clocks=%d context=%v err=%v candidate=%+v", clocks, ctx.Err(), err, v)
			}
			requireOperational503(t, err)
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("canceled commit changed durable state")
			}
			a.now = h.now
			if _, err := a.Load(context.Background(), h.identity, r.RequestID); err != nil {
				t.Fatal("fresh read unavailable after cancellation", err)
			}
			assertBinding(t, s, r, 1)
		})
	}
}
