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

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func bindingWriter(s *Store) *manualExecutionBindingWriter {
	return &manualExecutionBindingWriter{store: s, now: func() time.Time { return testNow }}
}

func bindingFixture(t *testing.T, side string) (*storage.Database, *Store, *API, Record, manualAdmissionScope, string) {
	t.Helper()
	d, s, h, r, scope, path := manualInputsFixture(t, side)
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	return d, s, h, r, scope, path
}

func bindingCommand(t *testing.T, s *Store, r Record) manualExecutionBinding {
	t.Helper()
	cmd, err := scanManualCommand(s.db.QueryRow(`SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE request_id=?`, r.RequestID))
	if err != nil {
		t.Fatal(err)
	}
	c, err := verifyManualCommandTx(context.Background(), s.db, cmd)
	if err != nil {
		t.Fatal(err)
	}
	return executionBindingFor(c, cmd, testNow.Format(time.RFC3339Nano))
}

func insertBinding(s *Store, b manualExecutionBinding) error {
	_, err := s.db.Exec(`INSERT INTO manual_execution_bindings(`+manualExecutionBindingColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, b.arguments()...)
	return err
}

func assertBinding(t *testing.T, s *Store, r Record, want int) {
	t.Helper()
	for _, table := range []string{"manual_execution_bindings", "manual_execution_binding_complete"} {
		if count(t, s.db, table) != want {
			t.Fatal("binding incomplete", table, want)
		}
	}
	var steps, audits, alerts int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM execution_steps WHERE kind='manual_trade_execution'),
 (SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='MANUAL_EXECUTION_BOUND'),
 (SELECT COUNT(*) FROM canary_alert_outbox WHERE id LIKE 'manual-execution-alert:%')`).Scan(&steps, &audits, &alerts); err != nil || steps != want || audits != want || alerts != want {
		t.Fatal("binding half", steps, audits, alerts, err)
	}
	for _, table := range []string{"transaction_attempts", "transaction_submissions", "execution_reservations", "canary_send_permits", "canary_runtime_gate_snapshots"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("prohibited economic row", table)
		}
	}
	var used uint64
	var amount string
	if err := s.db.QueryRow(`SELECT operation_count,total_input FROM canary_authorization_usage`).Scan(&used, &amount); err != nil || used != 1 || amount != r.Validation.Output.Resolution.AmountIn {
		t.Fatal("budget changed", used, amount, err)
	}
	if count(t, s.db, "manual_admission_complete") != 1 {
		t.Fatal("historical admission erased")
	}
}

func TestHTTPM2ExactBindingClaimAndHistoryNoPermission(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			_, s, h, r, scope, _ := bindingFixture(t, side)
			b := bindingCommand(t, s, r)
			v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || v.Status != "BOUND_NOT_SIGNED_NOT_PERMISSION" || v.Duplicate || v.ExecutionAuthorized || v.SendAuthorized || v.ExecutionStepID != b.ExecutionStep || v.ExpiresAt != r.ExpiresAt {
				t.Fatal(v, err)
			}
			stored, err := scanManualExecutionBinding(s.db.QueryRow(`SELECT ` + manualExecutionBindingColumns + ` FROM manual_execution_bindings`))
			if err != nil || stored != b {
				t.Fatal("saved fields changed", stored, err)
			}
			var parameters, opStatus, stepStatus, lane string
			var nonce any
			if err = s.db.QueryRow(`SELECT s.parameters_json,o.status,v.status,l.state,l.reserved_nonce FROM execution_steps s JOIN operations o ON o.id=s.operation_id JOIN execution_steps v ON v.id=? JOIN execution_wallet_lanes l ON l.step_id=s.id WHERE s.id=?`, b.ValidationStep, b.ExecutionStep).Scan(&parameters, &opStatus, &stepStatus, &lane, &nonce); err != nil || opStatus != "manual_admission_succeeded" || stepStatus != "manual_admitted" || lane != "reserved" || nonce != nil {
				t.Fatal("lifecycle/nonce drift", err)
			}
			cmd, err := scanManualCommand(s.db.QueryRow(`SELECT ` + manualCommandColumns + ` FROM manual_admission_commands`))
			if err != nil || parameters != cmd.ContractRaw {
				t.Fatal("rebuild/requote", err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			for i := 0; i < 3; i++ {
				v, err = bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
				if err != nil || !v.Duplicate || v.Status != "HISTORICAL_EXECUTION_BINDING_NOT_PERMISSION" || v.ExecutionAuthorized || v.SendAuthorized {
					t.Fatal(v, err)
				}
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("duplicate mutated state")
			}
			if _, err = admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID); !errors.Is(err, ErrWallet) {
				t.Fatal("bound work is still initial M1 candidate", err)
			}
			if history, err := s.manualAdmissionHistory(context.Background(), h.identity, r.RequestID); err != nil || history == nil {
				t.Fatal("history unavailable", err)
			}
			assertBinding(t, s, r, 1)
			backend := h.validator.backend.(*validationBackend)
			if backend.resolves.Load() != 1 || backend.completes.Load() != 1 {
				t.Fatal("binding requoted")
			}
		})
	}
}

func TestHTTPM2FullBudgetEqualityAndIdleLaneReuse(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixtureBudget(t, "buy", 1, "10000000000000000")
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO execution_wallet_lanes(wallet_id,address,state,updated_at) VALUES(?,?,'idle',?)`, h.identity.WalletID, h.identity.WalletAddress, testNow.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	assertBinding(t, s, r, 1)
}

func TestHTTPM2ControlsIdentityAndTTLFailClosed(t *testing.T) {
	for name, sql := range map[string]string{
		"stop": `UPDATE canary_control_state SET emergency_stopped=1`, "disabled": `UPDATE canary_control_state SET mode='DISABLED'`,
		"revoke": `UPDATE canary_runtime_authorizations SET state='REVOKED'`, "expired": `UPDATE canary_runtime_authorizations SET state='EXPIRED'`,
		"frozen": `UPDATE canary_risk_reservations SET state='frozen'`, "released": `UPDATE canary_risk_reservations SET state='released'`,
		"committed": `UPDATE canary_risk_reservations SET state='committed'`, "usageFrozen": `UPDATE canary_authorization_operation_usage SET state='FROZEN'`,
	} {
		t.Run(name, func(t *testing.T) {
			_, s, h, r, scope, _ := bindingFixture(t, "buy")
			if _, err := s.db.Exec(sql); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			if v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err == nil || !reflect.DeepEqual(v, manualExecutionBindingResult{}) {
				t.Fatal(v, err)
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("rejection wrote")
			}
			assertBinding(t, s, r, 0)
		})
	}
	_, s, h, r, scope, _ := bindingFixture(t, "buy")
	for _, field := range []string{"AuthorizationID", "ManualPolicyID", "Environment", "BuildSHA", "ReleaseID", "DeploymentID", "AuthorizationEpoch"} {
		wrong := scope
		f := reflect.ValueOf(&wrong).Elem().FieldByName(field)
		if field == "AuthorizationEpoch" {
			f.SetUint(2)
		} else if field == "BuildSHA" {
			f.SetString(strings.Repeat("2", 40))
		} else {
			f.SetString("outside")
		}
		if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, wrong); !errors.Is(err, ErrConflict) {
			t.Fatal(field, err)
		}
	}
	expiry, _ := canonicalManualTime(r.ExpiresAt)
	for _, now := range []time.Time{expiry, expiry.Add(time.Nanosecond), time.Time{}} {
		w := bindingWriter(s)
		w.now = func() time.Time { return now }
		before := admittedDatabaseSnapshot(t, s.db)
		if _, err := w.bind(context.Background(), h.identity, r.RequestID, scope); err == nil {
			t.Fatal("expired/invalid time bound")
		}
		if admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("expiry wrote")
		}
	}
	other := h.identity
	other.PrincipalID = "outside"
	if _, err := bindingWriter(s).bind(context.Background(), other, r.RequestID, scope); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross caller", err)
	}
	assertBinding(t, s, r, 0)
}

func TestHTTPM2FaultWindowsRollbackAllEffects(t *testing.T) {
	for _, stage := range []string{"before_binding", "after_binding", "before_commit"} {
		t.Run(stage, func(t *testing.T) {
			_, s, h, r, scope, _ := bindingFixture(t, "buy")
			before := admittedDatabaseSnapshot(t, s.db)
			w := bindingWriter(s)
			w.checkpoint = func(at string) error {
				if at == stage {
					return context.DeadlineExceeded
				}
				return nil
			}
			v, err := w.bind(context.Background(), h.identity, r.RequestID, scope)
			if !errors.Is(err, context.DeadlineExceeded) || !reflect.DeepEqual(v, manualExecutionBindingResult{}) {
				t.Fatal(v, err)
			}
			requireOperational503(t, err)
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("half commit", stage)
			}
			assertBinding(t, s, r, 0)
			if _, err = bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal("retry lost durable admission", err)
			}
			assertBinding(t, s, r, 1)
		})
	}
}

func TestHTTPM2DirectSQLAtomicAuditAndForcedHalfRollback(t *testing.T) {
	for _, fixture := range []string{"success", "step", "lane", "audit", "alert", "ignoreHalf"} {
		t.Run(fixture, func(t *testing.T) {
			_, s, _, r, _, _ := bindingFixture(t, "buy")
			b := bindingCommand(t, s, r)
			var sql string
			switch fixture {
			case "step":
				sql = `CREATE TRIGGER m2_fault BEFORE INSERT ON execution_steps WHEN NEW.kind='manual_trade_execution' BEGIN SELECT RAISE(ABORT,'fault'); END`
			case "lane":
				sql = `CREATE TRIGGER m2_fault BEFORE INSERT ON execution_wallet_lanes BEGIN SELECT RAISE(ABORT,'fault'); END`
			case "audit":
				sql = `CREATE TRIGGER m2_fault BEFORE INSERT ON canary_runtime_audit WHEN NEW.event_type='MANUAL_EXECUTION_BOUND' BEGIN SELECT RAISE(ABORT,'fault'); END`
			case "alert":
				sql = `CREATE TRIGGER m2_fault BEFORE INSERT ON canary_alert_outbox WHEN NEW.id LIKE 'manual-execution-alert:%' BEGIN SELECT RAISE(ABORT,'fault'); END`
			case "ignoreHalf":
				sql = `CREATE TRIGGER m2_fault BEFORE INSERT ON canary_alert_outbox WHEN NEW.id LIKE 'manual-execution-alert:%' BEGIN SELECT RAISE(IGNORE); END`
			}
			if sql != "" {
				if _, err := s.db.Exec(sql); err != nil {
					t.Fatal(err)
				}
			}
			before := admittedDatabaseSnapshot(t, s.db)
			err := insertBinding(s, b)
			if fixture == "success" {
				if err != nil {
					t.Fatal(err)
				}
				assertBinding(t, s, r, 1)
			} else {
				if err == nil {
					t.Fatal("fault/ignore half committed")
				}
				if admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("statement did not rollback")
				}
				assertBinding(t, s, r, 0)
			}
		})
	}
}

func TestHTTPM2SchemaRejectsBypassMutationAndReplacement(t *testing.T) {
	_, s, h, r, scope, _ := bindingFixture(t, "buy")
	b := bindingCommand(t, s, r)
	for _, field := range []string{"Operation", "Request", "ValidationStep", "ExecutionStep", "Wallet", "Address", "ContractHash", "QuoteHash", "ExpiresAt", "PolicyHash", "GateHash", "CheckedAt"} {
		bad := b
		reflect.ValueOf(&bad).Elem().FieldByName(field).SetString("outside")
		before := admittedDatabaseSnapshot(t, s.db)
		if err := insertBinding(s, bad); err == nil {
			t.Fatal("direct SQL drift", field)
		}
		if admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("drift left halves", field)
		}
	}
	for _, field := range []string{"Chain", "PolicyVersion", "QuoteBlock"} {
		bad := b
		f := reflect.ValueOf(&bad).Elem().FieldByName(field)
		f.SetUint(f.Uint() + 1)
		if err := insertBinding(s, bad); err == nil {
			t.Fatal("numeric SQL drift accepted", field)
		}
	}
	for _, field := range []string{"AuthorizationID", "ManualPolicyID", "AuthorizationEpoch", "Environment", "BuildSHA", "ReleaseID", "DeploymentID"} {
		bad := b
		f := reflect.ValueOf(&bad.Scope).Elem().FieldByName(field)
		if field == "AuthorizationEpoch" {
			f.SetUint(f.Uint() + 1)
		} else {
			f.SetString("outside")
		}
		if err := insertBinding(s, bad); err == nil {
			t.Fatal("scope SQL drift accepted", field)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO execution_steps(id,operation_id,step_index,kind,status) VALUES(?,?,1,'manual_trade_execution','manual_execution_bound')`, b.ExecutionStep, b.Operation); err == nil {
		t.Fatal("orphan step accepted")
	}
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE manual_execution_bindings SET contract_hash='bad'`, `DELETE FROM manual_execution_bindings`, `INSERT OR REPLACE INTO manual_execution_bindings SELECT * FROM manual_execution_bindings`,
		`UPDATE execution_steps SET status='signed' WHERE kind='manual_trade_execution'`, `DELETE FROM execution_steps WHERE kind='manual_trade_execution'`,
		`INSERT OR REPLACE INTO execution_steps SELECT * FROM execution_steps WHERE kind='manual_trade_execution'`,
		`UPDATE execution_wallet_lanes SET state='idle'`, `UPDATE execution_wallet_lanes SET reserved_nonce='7'`, `DELETE FROM execution_wallet_lanes`,
		`INSERT OR REPLACE INTO execution_wallet_lanes SELECT * FROM execution_wallet_lanes`,
		`INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) SELECT 'attempt',execution_step_id,'7','prepared',checked_at FROM manual_execution_bindings`,
		`UPDATE canary_runtime_audit SET reason_code='bad' WHERE event_type='MANUAL_EXECUTION_BOUND'`,
		`DELETE FROM canary_runtime_audit WHERE event_type='MANUAL_EXECUTION_BOUND'`,
		`INSERT OR REPLACE INTO canary_runtime_audit SELECT * FROM canary_runtime_audit WHERE event_type='MANUAL_EXECUTION_BOUND'`,
		`UPDATE canary_alert_outbox SET severity='CRITICAL' WHERE id LIKE 'manual-execution-alert:%'`,
		`DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-execution-alert:%'`,
	} {
		before := admittedDatabaseSnapshot(t, s.db)
		if _, err := s.db.Exec(q); err == nil {
			t.Fatal("schema bypass", q)
		}
		if admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("failed SQL mutated", q)
		}
	}
	assertBinding(t, s, r, 1)
}

func TestHTTPM2MissingControlsMalformedSourceFailWithoutRepair(t *testing.T) {
	for _, change := range []string{
		`DELETE FROM canary_control_state`,
		`DROP TRIGGER canary_wallet_allowlist_no_update; UPDATE canary_wallet_allowlist SET enabled=0`,
		`DROP TRIGGER canary_contract_allowlist_no_update; UPDATE canary_contract_allowlist SET runtime_code_hash='0x'||printf('%064d',0)`,
		`DROP TRIGGER canary_gate_attestation_no_delete; DELETE FROM canary_gate_attestations WHERE gate='C05'`,
		`DROP TRIGGER manual_http_source_no_update; UPDATE manual_http_order_sources SET expires_at='invalid'`,
		`DROP TRIGGER manual_http_source_no_update; UPDATE manual_http_order_sources SET expires_at=replace(expires_at,'Z','+00:00')`,
		`DROP TRIGGER manual_http_source_no_update; UPDATE manual_http_order_sources SET expires_at=''`,
	} {
		t.Run(change, func(t *testing.T) {
			_, s, h, r, scope, _ := bindingFixture(t, "buy")
			if _, err := s.db.Exec(change); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			if v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err == nil || v.Status != "" {
				t.Fatal(v, err)
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("invalid source repaired")
			}
		})
	}
}

func TestHTTPM2AuthorizationExpiryIndependentFinalGate(t *testing.T) {
	for _, stage := range []string{"entry", "after_binding", "before_commit"} {
		t.Run(stage, func(t *testing.T) {
			_, s, h, r, scope, _ := bindingFixture(t, "buy")
			expiry := testNow.Add(10 * time.Second)
			// Corruption/future-auth fixture only: earlier auth expiry than intent.
			if _, err := s.db.Exec(`DROP TRIGGER canary_runtime_authorization_identity_immutable; UPDATE canary_runtime_authorizations SET expires_at=?`, expiry.Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			w := bindingWriter(s)
			if stage == "entry" {
				w.now = func() time.Time { return expiry }
			}
			w.checkpoint = func(at string) error {
				if at == stage {
					w.now = func() time.Time { return expiry }
				}
				return nil
			}
			if v, err := w.bind(context.Background(), h.identity, r.RequestID, scope); err == nil || v.Status != "" {
				t.Fatal(v, err)
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("authorization expiry committed binding")
			}
			assertBinding(t, s, r, 0)
		})
	}
}

func TestHTTPM2SchemaRejectsAdoptingLegacyStepAttemptReservation(t *testing.T) {
	_, s, h, r, scope, _ := bindingFixture(t, "buy")
	if _, err := s.db.Exec(`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('legacy',4663,'wallet','legacy-key','fp','swap','prepared','2026-01-01T00:00:00Z');
 INSERT INTO execution_steps(id,operation_id,step_index,kind,status) VALUES('legacy-step','legacy',0,'curve_swap','prepared');
 INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('legacy-attempt','legacy-step','7','prepared','2026-01-01T00:00:00Z');
 INSERT INTO execution_reservations(operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at)
 SELECT 'legacy','legacy-step',wallet_id,'native','1','1','released',checked_at,checked_at FROM manual_admission_commands JOIN manual_http_order_sources USING(operation_id)`); err != nil {
		t.Fatal(err)
	}
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	b := bindingCommand(t, s, r)
	before := admittedDatabaseSnapshot(t, s.db)
	for _, q := range []string{
		`UPDATE execution_steps SET operation_id=?,step_index=2 WHERE id='legacy-step'`,
		`UPDATE transaction_attempts SET step_id=(SELECT execution_step_id FROM manual_execution_bindings WHERE operation_id=?) WHERE id='legacy-attempt'`,
		`UPDATE execution_reservations SET operation_id=? WHERE operation_id='legacy'`,
	} {
		if _, err := s.db.Exec(q, b.Operation); err == nil {
			t.Fatal("legacy adoption accepted", q)
		}
		if admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("legacy adoption mutated", q)
		}
	}
}

func TestHTTPM2RealRestartAndTwoHandleConcurrentDedupe(t *testing.T) {
	d, s, h, r, scope, path := bindingFixture(t, "sell_percent")
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
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
	other, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	otherStore, err := NewStore(other)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := freshStore
			if i%2 == 1 {
				s = otherStore
			}
			v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || !v.Duplicate || v.ExpiresAt != r.ExpiresAt || v.SendAuthorized {
				t.Errorf("restart dedupe: %+v %v", v, err)
			}
		}(i)
	}
	wg.Wait()
	if admittedDatabaseSnapshot(t, freshStore.db) != before {
		t.Fatal("restart/duplicate changed original claim or budget")
	}
	assertBinding(t, freshStore, r, 1)
}

func TestHTTPM2InitialConcurrentContestAndResponseLoss(t *testing.T) {
	_, s, h, r, scope, path := bindingFixture(t, "buy")
	other, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	otherStore, err := NewStore(other)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan manualExecutionBindingResult, 12)
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			st := s
			if i%2 == 1 {
				st = otherStore
			}
			v, err := bindingWriter(st).bind(context.Background(), h.identity, r.RequestID, scope)
			results <- v
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	first := 0
	for v := range results {
		if !v.Duplicate {
			first++
		}
		if v.ExecutionAuthorized || v.SendAuthorized {
			t.Fatal("claim became permission")
		}
	}
	if first != 1 {
		t.Fatal("unexplained first bindings", first)
	}
	// Caller lost all responses: original ID lookup still yields exact history.
	v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || !v.Duplicate {
		t.Fatal(v, err)
	}
	assertBinding(t, s, r, 1)
}

func TestHTTPM2DependencyGraphAndClosedGuards(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "manual_execution_binding.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			switch s.Sel.Name {
			case "Prepare", "Submit", "ReplayUnknown", "SendRawTransaction", "SignTransaction", "PendingNonce", "ReserveExecution":
				t.Error("economic dependency", s.Sel.Name)
			}
		}
		return true
	})
	_, s, _, r, _, _ := bindingFixture(t, "buy")
	for _, name := range []string{"manual_b1_no_permit", "manual_b1_no_pass_snapshot", "manual_command_operation_no_reset", "manual_command_step_no_reset"} {
		var n int
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&n); err != nil || n != 1 {
			t.Fatal(name, err)
		}
	}
	assertBinding(t, s, r, 0)
}

func TestHTTPM2CommitWindowControlWriterCannotInterleave(t *testing.T) {
	for _, update := range []string{`UPDATE canary_control_state SET emergency_stopped=1`, `UPDATE canary_runtime_authorizations SET state='REVOKED'`} {
		t.Run(update, func(t *testing.T) {
			_, s, h, r, scope, path := bindingFixture(t, "buy")
			other, err := storage.Open(context.Background(), storage.TradeOwner, path)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			db, err := other.SQLDB(storage.TradeOwner)
			if err != nil {
				t.Fatal(err)
			}
			entered, allow := make(chan struct{}), make(chan struct{})
			var release sync.Once
			defer release.Do(func() { close(allow) })
			w := bindingWriter(s)
			w.checkpoint = func(stage string) error {
				if stage == "before_commit" {
					close(entered)
					<-allow
				}
				return nil
			}
			completed := make(chan error, 1)
			go func() { _, e := w.bind(context.Background(), h.identity, r.RequestID, scope); completed <- e }()
			<-entered
			conn, err := db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err = conn.ExecContext(context.Background(), `PRAGMA busy_timeout=0`); err != nil {
				t.Fatal(err)
			}
			if _, err = conn.ExecContext(context.Background(), update); err == nil || !strings.Contains(err.Error(), "SQLITE_BUSY") {
				t.Fatal("control writer interleaved with binding commit", err)
			}
			release.Do(func() { close(allow) })
			if err = <-completed; err != nil {
				t.Fatal(err)
			}
			if _, err = conn.ExecContext(context.Background(), update); err != nil {
				t.Fatal(err)
			}
			v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
			if strings.Contains(update, "emergency_stopped") {
				if inputFailureCode(err) != "EMERGENCY_STOPPED" || v.Status != "" {
					t.Fatal(v, err)
				}
			} else if err != nil || !v.Duplicate || v.ExecutionAuthorized || v.SendAuthorized {
				t.Fatal(v, err)
			}
			assertBinding(t, s, r, 1)
		})
	}
}

func TestHTTPM2FinalClockAndCancellationRollback(t *testing.T) {
	for _, mode := range []string{"after_binding_expiry", "before_commit_expiry", "reversal", "cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			_, s, h, r, scope, _ := bindingFixture(t, "buy")
			before := admittedDatabaseSnapshot(t, s.db)
			w := bindingWriter(s)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				// The public writer's own context expires while paused before
				// COMMIT; the checkpoint itself returns no fabricated error.
				w.checkpoint = func(stage string) error {
					if stage == "before_commit" {
						<-ctx.Done()
					}
					return nil
				}
			} else {
				w.checkpoint = func(stage string) error {
					if (mode == "after_binding_expiry" || mode == "reversal") && stage == "after_binding" || (mode == "before_commit_expiry" || mode == "cancel") && stage == "before_commit" {
						expiry, _ := canonicalManualTime(r.ExpiresAt)
						if mode != "cancel" {
							w.now = func() time.Time { return expiry }
						}
						if mode == "reversal" {
							w.now = func() time.Time { return testNow.Add(-time.Nanosecond) }
						}
						if mode == "cancel" {
							cancel()
						}
					}
					return nil
				}
			}
			v, err := w.bind(ctx, h.identity, r.RequestID, scope)
			if err == nil || !reflect.DeepEqual(v, manualExecutionBindingResult{}) {
				t.Fatal(v, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("deadline lost", err)
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("late failure committed claim")
			}
			assertBinding(t, s, r, 0)
		})
	}
}

func TestHTTPM2DistinctOperationCannotShareBoundWallet(t *testing.T) {
	_, s, h, r, scope, path := manualInputsFixture(t, "buy")
	second := exposureOrder(t, h, "m2-other-operation", "buy")
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	other, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	st, err := NewStore(other)
	if err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := s
			if i%2 == 1 {
				store = st
			}
			if _, e := atomicWriter(store).admit(context.Background(), h.identity, second.RequestID, scope); e == nil {
				t.Error("distinct operation admitted on busy wallet")
			}
			if v, e := bindingWriter(store).bind(context.Background(), h.identity, second.RequestID, scope); e == nil || v.Status != "" {
				t.Errorf("distinct operation bound: %+v %v", v, e)
			}
		}(i)
	}
	wg.Wait()
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("second operation changed claim or budget")
	}
	assertBinding(t, s, r, 1)
}

func TestHTTPM2DuplicateAfterExpiryRevokeIsOnlyHistory(t *testing.T) {
	_, s, h, r, scope, _ := bindingFixture(t, "buy")
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'; UPDATE canary_risk_reservations SET state='frozen'; UPDATE canary_authorization_operation_usage SET state='FROZEN'`); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	w := bindingWriter(s)
	w.now = func() time.Time { return testNow.Add(2 * time.Hour) }
	v, err := w.bind(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || !v.Duplicate || v.Status != "HISTORICAL_EXECUTION_BINDING_NOT_PERMISSION" || v.ExecutionAuthorized || v.SendAuthorized || v.ExpiresAt != r.ExpiresAt {
		t.Fatal(v, err)
	}
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("history revived expired binding")
	}
	if _, err = s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`); err != nil {
		t.Fatal(err)
	}
	wrong := scope
	wrong.DeploymentID = "outside"
	if _, err = w.bind(context.Background(), h.identity, r.RequestID, wrong); inputFailureCode(err) != "EMERGENCY_STOPPED" {
		t.Fatal("stop priority lost", err)
	}
	if _, err = s.manualAdmissionHistory(context.Background(), h.identity, r.RequestID); err != nil {
		t.Fatal("HTTP historical query blocked", err)
	}
	assertBinding(t, s, r, 1)
}

func TestHTTPM2CorruptBindingNoRepairAndOperationalFailures(t *testing.T) {
	for _, change := range []string{
		`DROP TRIGGER manual_execution_binding_no_update; UPDATE manual_execution_bindings SET contract_hash='corrupt'`,
		`DROP TRIGGER manual_execution_lane_no_delete; DELETE FROM execution_wallet_lanes`,
		`DROP TRIGGER manual_execution_alert_no_delete; DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-execution-alert:%'`,
	} {
		t.Run(change, func(t *testing.T) {
			_, s, h, r, scope, _ := bindingFixture(t, "buy")
			if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(change); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			if v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err == nil || v.Status != "" {
				t.Fatal(v, err)
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("corrupt binding repaired")
			}
			if _, err := admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID); err == nil {
				t.Fatal("damaged bound item readmitted")
			}
		})
	}
	for _, mode := range []string{"closed", "entryCanceled", "foreignKeysOff", "synchronousOff"} {
		t.Run(mode, func(t *testing.T) {
			d, s, h, r, scope, _ := bindingFixture(t, "buy")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "closed":
				if err := d.Close(); err != nil {
					t.Fatal(err)
				}
			case "entryCanceled":
				cancel()
			case "foreignKeysOff":
				if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
					t.Fatal(err)
				}
			case "synchronousOff":
				if _, err := s.db.Exec(`PRAGMA synchronous=OFF`); err != nil {
					t.Fatal(err)
				}
			}
			if v, err := bindingWriter(s).bind(ctx, h.identity, r.RequestID, scope); err == nil || v.Status != "" {
				t.Fatal(v, err)
			} else if mode == "closed" || mode == "entryCanceled" {
				requireOperational503(t, err)
			} else if inputFailureCode(err) != "WRITER_CONNECTION_UNVERIFIABLE" {
				t.Fatal("unsafe PRAGMA not rejected", err)
			}
			if mode != "closed" {
				assertBinding(t, s, r, 0)
			}
		})
	}
}
