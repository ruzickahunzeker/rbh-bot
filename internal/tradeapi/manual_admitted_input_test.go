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
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func admittedInputReader(t *testing.T, s *Store, scope manualAdmissionScope) *ManualAdmittedInputReader {
	t.Helper()
	a, err := NewManualAdmittedInputReader(s, *httpAdmissionScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return testNow }
	return a
}

// Compare every user-table row, not only counts/allowance. Read-only inspection
// must not write audit/metrics, change lifecycle or create a claim/permit either.
func admittedDatabaseSnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var all []string
	for _, name := range tables {
		rows, err := db.Query(`SELECT * FROM "` + strings.ReplaceAll(name, `"`, `""`) + `"`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var contents []string
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			contents = append(contents, encode(values))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		sort.Strings(contents)
		all = append(all, name+":"+encode(contents))
	}
	return hash([]byte(encode(all)))
}

func TestHTTPM1ExactAdmittedParametersAndNoWrites(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			_, s, h, r, scope, _ := manualInputsFixture(t, side)
			if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			cmd, err := scanManualCommand(s.db.QueryRow(`SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE request_id=?`, r.RequestID))
			if err != nil {
				t.Fatal(err)
			}
			a := admittedInputReader(t, s, scope)
			before := admittedDatabaseSnapshot(t, s.db)
			for i := 0; i < 3; i++ {
				v, err := a.Load(context.Background(), h.identity, r.RequestID)
				if err != nil || v.Status != "ADMITTED_INPUT_CANDIDATE_NOT_PERMISSION" || encode(v.Contract) != cmd.ContractRaw || v.Contract.BindingHash != cmd.ContractHash || v.Contract.AdmissionAuthorized || v.Contract.SendAuthorized || v.ExecutionAuthorized || v.SendAuthorized || v.Scope != *httpAdmissionScope(scope) || v.ReservationID != cmd.Reservation || v.ReservationState != "reserved" || v.ManualPolicyHash != cmd.PolicyHash || v.GateEvidenceSetHash != cmd.GateHash || v.UsedOperations != 1 || v.UsedInput.Units != v.Contract.Input.Units {
					t.Fatal("candidate changed saved inputs or became permission", v, err)
				}
				if v.Contract.Output.GasTipCap != "0" || v.Contract.ExpiresAt != r.ExpiresAt || v.Contract.Output != r.Validation.Output {
					t.Fatal("fee/calldata/quantity/expiry changed")
				}
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("candidate read wrote durable state")
			}
			b := h.validator.backend.(*validationBackend)
			if b.resolves.Load() != 1 || b.completes.Load() != 1 {
				t.Fatal("reader resolved/requoted/resimulated")
			}
			assertAtomicAdmission(t, s, r, 1)
		})
	}
}

func TestHTTPM1FullBudgetEqualityDoesNotConsumeAgain(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixtureBudget(t, "buy", 1, "10000000000000000")
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	a := admittedInputReader(t, s, scope)
	for i := 0; i < 5; i++ {
		if _, err := a.Load(context.Background(), h.identity, r.RequestID); err != nil {
			t.Fatal("already consumed cap equality rejected", err)
		}
	}
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("candidate double-consumed allowance")
	}
}

func TestHTTPM1SelectOnlyHelperKeepsCallerTransaction(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
		var before, after int64
		if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if _, err := loadManualAdmittedInputTx(ctx, conn, h.identity, r.RequestID, *httpAdmissionScope(scope), h.now); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil || before != after {
			t.Fatal("reader wrote SQL", before, after, err)
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			t.Fatal("reader ended caller transaction", err)
		}
	})
}

func TestHTTPM1ExistingAttemptIsNotInitialCandidate(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	// Even an inconsistent legacy attempt without a wallet lane is not initial
	// work. Test-only SQL does not create a signed artifact or allocate a nonce.
	if _, err := s.db.Exec(`INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) SELECT 'legacy-attempt',step_id,'0','failed',? FROM manual_http_order_sources WHERE id=?`, testNow.Format(time.RFC3339Nano), r.RequestID); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	if v, err := admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID); !errors.Is(err, ErrWallet) || v.Status != "" {
		t.Fatal("existing attempt exposed as new candidate", err)
	}
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("reader repaired existing attempt")
	}
}

func TestHTTPM1InvalidCompositionAndClosedDatabase(t *testing.T) {
	if _, err := NewManualAdmittedInputReader(nil, ManualAdmissionScope{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	var absent *ManualAdmittedInputReader
	if _, err := absent.Load(context.Background(), Identity{}, "request"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	d, s, h, r, scope, _ := manualInputsFixture(t, "buy")
	if _, err := NewManualAdmittedInputReader(s, ManualAdmissionScope{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	a := admittedInputReader(t, s, scope)
	//lint:ignore SA1012 Deliberately test the public invalid-context fail-closed boundary.
	if _, err := a.Load(nil, h.identity, r.RequestID); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := a.Load(context.Background(), h.identity, r.RequestID)
	requireOperational503(t, err)
}

func TestHTTPM1ScopeAndCallerBindingAndDetachedResults(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	a := admittedInputReader(t, s, scope)
	for _, field := range []string{"PrincipalID", "WalletID", "WalletAddress"} {
		id := h.identity
		value := "outside"
		if field == "WalletAddress" {
			value = "0x" + strings.Repeat("9", 40)
		}
		reflect.ValueOf(&id).Elem().FieldByName(field).SetString(value)
		if _, err := a.Load(context.Background(), id, r.RequestID); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-caller candidate exposed", field, err)
		}
	}
	for _, field := range []string{"AuthorizationID", "ManualPolicyID", "Environment", "BuildSHA", "ReleaseID", "DeploymentID", "AuthorizationEpoch"} {
		wrong := *httpAdmissionScope(scope)
		f := reflect.ValueOf(&wrong).Elem().FieldByName(field)
		if field == "AuthorizationEpoch" {
			f.SetUint(2)
		} else if field == "BuildSHA" {
			f.SetString(strings.Repeat("2", 40))
		} else {
			f.SetString("outside")
		}
		reader, err := NewManualAdmittedInputReader(s, wrong)
		if err != nil {
			t.Fatal(err)
		}
		reader.now = h.now
		if _, err := reader.Load(context.Background(), h.identity, r.RequestID); !errors.Is(err, ErrConflict) {
			t.Fatal("deployment/policy/auth drift accepted", field, err)
		}
	}
	v, err := a.Load(context.Background(), h.identity, r.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	v.Contract.Request.Fee.TipGwei = "777"
	v.ManualPolicy.InputLimits[0].MaxOperation = "777"
	v.Scope.DeploymentID = "outside"
	fresh, err := a.Load(context.Background(), h.identity, r.RequestID)
	if err != nil || fresh.Contract.Output.GasTipCap != "0" || fresh.Contract.Request.Fee.TipGwei == "777" || fresh.ManualPolicy.InputLimits[0].MaxOperation == "777" || fresh.Scope.DeploymentID != scope.DeploymentID {
		t.Fatal("returned values alias durable/config inputs", err)
	}
}

func TestHTTPM1MutableControlsAndReservationDenyButHistorySurvives(t *testing.T) {
	changes := map[string]string{
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
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
			if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(change); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			if v, err := admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID); err == nil || v.Status != "" {
				t.Fatal("noninitial/blocked candidate exposed", v, err)
			}
			if history, err := s.manualAdmissionHistory(context.Background(), h.identity, r.RequestID); err != nil || history == nil || history.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" {
				t.Fatal("candidate denial blocked history", err)
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("denial repaired/refunded/released state")
			}
		})
	}
}

func TestHTTPM1TTLBoundaryFinalClockAndStopPriority(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	expiry, err := canonicalManualTime(r.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		first  time.Time
		last   time.Time
		accept bool
	}{
		{"minus1ns", expiry.Add(-time.Nanosecond), expiry.Add(-time.Nanosecond), true},
		{"equal", expiry, expiry, false}, {"plus1ns", expiry.Add(time.Nanosecond), expiry.Add(time.Nanosecond), false},
		{"crossDuringRead", testNow, expiry, false}, {"reversal", testNow, testNow.Add(-time.Nanosecond), false},
		{"zero", time.Time{}, time.Time{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := admittedInputReader(t, s, scope)
			calls := 0
			a.now = func() time.Time {
				calls++
				if calls == 1 {
					return tc.first
				}
				return tc.last
			}
			before := admittedDatabaseSnapshot(t, s.db)
			v, err := a.Load(context.Background(), h.identity, r.RequestID)
			if (err == nil) != tc.accept || !tc.accept && v.Status != "" {
				t.Fatal("TTL/clock fail-open", v, err)
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("TTL denial released state")
			}
		})
	}
	if _, err := s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
		t.Fatal(err)
	}
	a := admittedInputReader(t, s, scope)
	a.scope.DeploymentID = "drift"
	a.now = func() time.Time { return expiry }
	if _, err := a.Load(context.Background(), h.identity, r.RequestID); inputFailureCode(err) != "EMERGENCY_STOPPED" {
		t.Fatal("stop lost priority", err)
	}
}

func TestHTTPM1CorruptOrMissingAdmissionFailsWithoutRepair(t *testing.T) {
	for _, change := range []string{
		`DROP TRIGGER manual_command_no_delete; DELETE FROM manual_admission_commands`,
		`DROP TRIGGER manual_command_no_update; UPDATE manual_admission_commands SET contract_hash=printf('%064d',0)`,
		`DROP TRIGGER manual_command_legacy_audit_no_delete; DELETE FROM canary_audit_events`,
		`DROP TRIGGER manual_b1_no_usage_update; UPDATE canary_authorization_usage SET total_input='0'`,
		`DROP TRIGGER manual_resolved_step_snapshot; UPDATE execution_steps SET quote_block_hash='corrupt'`,
	} {
		t.Run(change, func(t *testing.T) {
			_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
			if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(change); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			if v, err := admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID); err == nil || v.Status != "" {
				t.Fatal("incomplete/corrupt candidate accepted", v, err)
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("read repaired corruption")
			}
		})
	}
}

func TestHTTPM1MissingControlsAllowlistAndMalformedExpiry(t *testing.T) {
	// Guard removal is corruption injection into disposable fixtures ONLY.
	// The implementation changes no schema or immutability protections.
	for name, change := range map[string]string{
		"missingControl":     `DELETE FROM canary_control_state`,
		"walletAllowlistOff": `DROP TRIGGER canary_wallet_allowlist_no_update; UPDATE canary_wallet_allowlist SET enabled=0`,
		"contractOff":        `DROP TRIGGER canary_contract_allowlist_no_update; UPDATE canary_contract_allowlist SET enabled=0`,
		"runtimeDrift":       `DROP TRIGGER canary_contract_allowlist_no_update; UPDATE canary_contract_allowlist SET runtime_code_hash='0x'||printf('%064d',0)`,
		"tokenOff":           `DROP TRIGGER canary_token_allowlist_no_update; UPDATE canary_token_allowlist SET enabled=0`,
		"gateMissing":        `DROP TRIGGER canary_gate_attestation_no_delete; DELETE FROM canary_gate_attestations WHERE gate='C05'`,
		"expiryMalformed":    `DROP TRIGGER manual_http_source_no_update; UPDATE manual_http_order_sources SET expires_at='invalid'`,
		"expiryNoncanonical": `DROP TRIGGER manual_http_source_no_update; UPDATE manual_http_order_sources SET expires_at=replace(expires_at,'Z','+00:00')`,
		"expiryMissing":      `DROP TRIGGER manual_http_source_no_update; UPDATE manual_http_order_sources SET expires_at=''`,
	} {
		t.Run(name, func(t *testing.T) {
			_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
			if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(change); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			if v, err := admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID); err == nil || v.Status != "" {
				t.Fatal("unverifiable input exposed", v, err)
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("reader repaired controls/expiry")
			}
		})
	}
}

func TestHTTPM1AuthorizationExpiryBoundaryAndFinalRecheck(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
	// Set the isolated future authorization to expire before the intent, so
	// rejection proves authorization expiry rather than just intent expiry.
	expiry := testNow.Add(10 * time.Second)
	if _, err := s.db.Exec(`DROP TRIGGER canary_runtime_authorization_identity_immutable; UPDATE canary_runtime_authorizations SET expires_at=?`, expiry.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		first, last time.Time
		accept      bool
	}{
		{expiry.Add(-time.Nanosecond), expiry.Add(-time.Nanosecond), true},
		{expiry, expiry, false},
		{expiry.Add(time.Nanosecond), expiry.Add(time.Nanosecond), false},
		{testNow, expiry, false},
	} {
		a := admittedInputReader(t, s, scope)
		calls := 0
		a.now = func() time.Time {
			calls++
			if calls == 1 {
				return tc.first
			}
			return tc.last
		}
		before := admittedDatabaseSnapshot(t, s.db)
		v, err := a.Load(context.Background(), h.identity, r.RequestID)
		if (err == nil) != tc.accept || !tc.accept && v.Status != "" {
			t.Fatal("authorization expiry fail-open", v, err)
		}
		if admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("authorization expiry changed ledger")
		}
	}
}

func TestHTTPM1OperationalFaultsPreserve503EveryRead(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	check := func(q queryRow) error {
		_, err := loadManualAdmittedInputTx(context.Background(), q, h.identity, r.RequestID, *httpAdmissionScope(scope), h.now)
		return err
	}
	countQueries := &operationalFaultQuery{db: s.db}
	if err := check(countQueries); err != nil {
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
		for nth := 1; nth <= countQueries.queries; nth++ {
			t.Run(fmt.Sprintf("%s/read_%d", fault, nth), func(t *testing.T) {
				err := check(&operationalFaultQuery{db: s.db, failAt: nth, fault: fault, closed: closed})
				requireOperational503(t, err)
			})
		}
	}
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("operational fault changed ledger")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := admittedInputReader(t, s, scope).Load(ctx, h.identity, r.RequestID); !errors.Is(err, context.Canceled) {
		t.Fatal("public reader cancellation lost", err)
	}
}

func TestHTTPM1RestartAndConcurrentReadsRediscoverUnchanged(t *testing.T) {
	d, s, h, r, scope, path := manualInputsFixture(t, "sell_percent")
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	first, err := admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Close(); err != nil {
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
	a := admittedInputReader(t, freshStore, scope)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := a.Load(context.Background(), h.identity, r.RequestID)
			if err != nil || encode(v) != encode(first) {
				t.Errorf("restart/concurrent candidate changed: %v", err)
			}
		}()
	}
	wg.Wait()
	if admittedDatabaseSnapshot(t, freshStore.db) != before {
		t.Fatal("restart reset usage or minted TTL")
	}
	assertAtomicAdmission(t, freshStore, r, 1)
}

func TestHTTPM1DisconnectedReaderAndSchemaGuards(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "manual_admitted_input.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			switch sel.Sel.Name {
			case "Exec", "ExecContext", "SignTransaction", "Prepare", "Submit", "ReplayUnknown", "SendRawTransaction", "ReserveExecution", "PendingNonce":
				t.Errorf("reader economic/write dependency: %s", sel.Sel.Name)
			}
		}
		return true
	})
	_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'; CREATE TRIGGER manual_b1_no_arm BEFORE UPDATE OF state ON canary_runtime_authorizations WHEN NEW.state='ARMED' AND EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.id) BEGIN SELECT RAISE(ABORT,'manual not authorized'); END`); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	if _, err := admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID); err == nil {
		t.Fatal("reader granted revoked authorization")
	}
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("reader modified durable controls")
	}
	for _, name := range []string{"manual_b1_no_arm", "manual_b1_no_permit", "manual_b1_no_pass_snapshot", "manual_command_operation_no_reset", "manual_command_step_no_reset"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&n); err != nil || n != 1 {
			t.Fatal("guard removed", name, err)
		}
	}
	assertAtomicAdmission(t, s, r, 1)
}
