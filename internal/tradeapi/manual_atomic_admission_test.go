package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

func atomicWriter(s *Store) *manualAdmissionWriter {
	return &manualAdmissionWriter{store: s, now: func() time.Time { return testNow }}
}

func assertAtomicAdmission(t *testing.T, s *Store, r Record, want uint64) {
	t.Helper()
	for _, table := range []string{"manual_admission_commands", "manual_admission_complete", "canary_risk_reservations", "manual_reservation_units", "canary_authorization_operation_usage", "canary_admission_decisions", "canary_admission_attempts", "canary_audit_events"} {
		if uint64(count(t, s.db, table)) != want {
			t.Fatal("incomplete ledger", table, want)
		}
	}
	var used uint64
	var total string
	if err := s.db.QueryRow(`SELECT operation_count,total_input FROM canary_authorization_usage`).Scan(&used, &total); err != nil || used != want {
		t.Fatal("usage count", used, want, err)
	}
	amount := "0"
	if want == 1 {
		amount = r.Validation.Output.Resolution.AmountIn
	}
	if total != amount {
		t.Fatal("usage sum", total, amount)
	}
	for _, table := range []string{"transaction_attempts", "transaction_submissions", "execution_reservations", "execution_wallet_lanes", "canary_send_permits", "canary_runtime_gate_snapshots"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("prohibited side effect", table)
		}
	}
}

func TestHTTPC3B3AtomicSingleDuplicateRestartAndHistoricalOnly(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			d, s, h, r, scope, path := manualInputsFixture(t, side)
			w := atomicWriter(s)
			got, err := w.admit(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || got.Status != "ADMITTED_NOT_SIGNED_NOT_SUBMITTED" || got.Duplicate || got.ExpiresAt != r.ExpiresAt {
				t.Fatal(got, err)
			}
			assertAtomicAdmission(t, s, r, 1)
			legacy, err := trade.NewStore(d)
			if err != nil {
				t.Fatal(err)
			}
			if candidates, err := legacy.Recoverable(context.Background()); err != nil || len(candidates) != 0 {
				t.Fatal("legacy recovery adopted MANUAL", err)
			}
			// Current C3-A is still initial-only; admission does not renew its
			// parameter contract or expose it to legacy execution preparation.
			if _, err = contractReader(t, s).Load(context.Background(), h.identity, r.RequestID); err == nil {
				t.Fatal("initial reader accepted admitted lifecycle")
			}
			dup, err := w.admit(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || !dup.Duplicate || dup.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" || dup.ContractHash != got.ContractHash {
				t.Fatal(dup, err)
			}
			alias := h.identity
			alias.WalletAddress = strings.TrimPrefix(alias.WalletAddress, "0x")
			if dup, err = w.admit(context.Background(), alias, r.RequestID, scope); err != nil || !dup.Duplicate {
				t.Fatal("historical address equivalence changed", dup, err)
			}
			if _, err = s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
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
			if err = fresh.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			store, err := NewStore(fresh)
			if err != nil {
				t.Fatal(err)
			}
			w = atomicWriter(store)
			w.now = func() time.Time { return testNow.Add(2 * time.Hour) }
			dup, err = w.admit(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || !dup.Duplicate || dup.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" || dup.ExpiresAt != got.ExpiresAt {
				t.Fatal("restart/expiry/revoke renewed or erased history", dup, err)
			}
			assertAtomicAdmission(t, store, r, 1)
			if _, err = store.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`); err != nil {
				t.Fatal(err)
			}
			if rejected, err := w.admit(context.Background(), h.identity, r.RequestID, scope); inputFailureCode(err) != "EMERGENCY_STOPPED" || rejected.OperationID != "" {
				t.Fatal("stop priority", rejected, err)
			}
		})
	}
}

func TestHTTPC3B3AtomicDedupeConflictAndCorruptCompleteHistory(t *testing.T) {
	for _, fault := range []string{"scope_epoch", "scope_deployment", "scope_policy", "principal", "wallet", "parent_source_hash", "parent_quote_hash", "usage_amount", "usage_total", "command_hash", "alert_identity"} {
		t.Run(fault, func(t *testing.T) {
			_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
			w := atomicWriter(s)
			if _, err := w.admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			id := h.identity
			mutate := func(statement string) {
				t.Helper()
				if _, err := s.db.Exec(statement); err != nil {
					t.Fatal("corruption fixture failed", statement, err)
				}
			}
			switch fault {
			case "scope_epoch":
				scope.AuthorizationEpoch++
			case "scope_deployment":
				scope.DeploymentID = "another"
			case "scope_policy":
				scope.ManualPolicyID = "another"
			case "principal":
				id.PrincipalID = "another"
			case "wallet":
				id.WalletAddress = testToken
			case "parent_source_hash":
				mutate(`DROP TRIGGER manual_reservation_parent_identity`)
				mutate(`UPDATE canary_risk_reservations SET source_hash='` + strings.Repeat("0", 64) + `'`)
			case "parent_quote_hash":
				mutate(`DROP TRIGGER manual_reservation_parent_identity`)
				mutate(`UPDATE canary_risk_reservations SET quote_block_hash='` + strings.Repeat("0", 66) + `'`)
			case "usage_amount":
				mutate(`DROP TRIGGER manual_command_usage_identity`)
				mutate(`UPDATE canary_authorization_operation_usage SET amount='1'`)
			case "usage_total":
				mutate(`DROP TRIGGER manual_b1_no_usage_update`)
				mutate(`UPDATE canary_authorization_usage SET total_input='1'`)
			case "command_hash":
				mutate(`DROP TRIGGER manual_command_no_update`)
				mutate(`UPDATE manual_admission_commands SET contract_hash='` + strings.Repeat("0", 64) + `'`)
			case "alert_identity":
				mutate(`DROP TRIGGER manual_command_alert_identity`)
				mutate(`UPDATE canary_alert_outbox SET severity='WARNING' WHERE id LIKE 'manual-admission-alert:%'`)
			}
			before := count(t, s.db, "canary_runtime_audit")
			if got, err := w.admit(context.Background(), id, r.RequestID, scope); err == nil || got.OperationID != "" {
				t.Fatal("conflict/corruption accepted", got, err)
			}
			if count(t, s.db, "manual_admission_commands") != 1 || count(t, s.db, "canary_risk_reservations") != 1 || count(t, s.db, "canary_runtime_audit") != before {
				t.Fatal("failure repaired/rebuilt/refunded history")
			}
		})
	}
}

func TestHTTPC3B3AtomicFrozenHistorySurvivesStopExpiryWithoutPermission(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`UPDATE canary_risk_reservations SET state='frozen'`, `UPDATE canary_authorization_operation_usage SET state='FROZEN'`,
		`UPDATE canary_control_state SET emergency_stopped=1`, `UPDATE canary_runtime_authorizations SET state='REVOKED'`} {
		if _, err := s.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
		c, err := verifyManualContractParametersTx(ctx, conn, h.identity, r.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		p, _, err := loadManualPolicy(ctx, conn, scope.ManualPolicyID, c, testNow.Add(2*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		exposure, err := loadManualExposureTx(ctx, conn, h.identity.WalletID, p, testNow.Add(2*time.Hour))
		if err != nil || exposure.WalletActiveOperations != 1 || exposure.WalletWindowOperations != 0 {
			t.Fatal("expiry erased frozen exposure", exposure, err)
		}
		unit := manualrisk.Unit{ChainID: 4663, Asset: c.Input.Asset, Decimals: c.Input.Decimals, Kind: manualrisk.TradeInput}
		if used, total, err := verifyManualAuthorizationUsage(ctx, conn, scope.AuthorizationID, unit); err != nil || used != 1 || total.Units != c.Input.Units {
			t.Fatal("expiry refunded frozen allowance", used, total, err)
		}
	})
	// No signed/submitted work is created here to pretend to exercise recovery.
	// Immutable query availability is distinct from a recovery health assertion.
	assertAtomicAdmission(t, s, r, 1)
}

func TestHTTPC3B3AtomicStatementFaultWindowsRollbackEveryHalf(t *testing.T) {
	for _, point := range []struct{ table, action, filter string }{
		{"canary_risk_reservations", "INSERT", "1"}, {"manual_reservation_units", "INSERT", "1"},
		{"canary_authorization_operation_usage", "INSERT", "1"}, {"canary_authorization_usage", "UPDATE", "1"},
		{"canary_admission_decisions", "INSERT", "1"}, {"canary_admission_attempts", "INSERT", "1"},
		{"canary_audit_events", "INSERT", "1"}, {"operations", "UPDATE", "NEW.status='manual_admission_succeeded'"},
		{"execution_steps", "UPDATE", "NEW.status='manual_admitted'"},
		{"canary_runtime_audit", "INSERT", "NEW.event_type='MANUAL_ATOMIC_ADMISSION'"},
		{"canary_alert_outbox", "INSERT", "NEW.id LIKE 'manual-admission-alert:%'"},
	} {
		t.Run(point.table, func(t *testing.T) {
			d, s, h, r, scope, path := manualInputsFixture(t, "buy")
			beforeAudit, beforeAlert := count(t, s.db, "canary_runtime_audit"), count(t, s.db, "canary_alert_outbox")
			if _, err := s.db.Exec(`CREATE TRIGGER atomic_fault AFTER ` + point.action + ` ON ` + point.table + ` WHEN ` + point.filter + ` BEGIN SELECT RAISE(ABORT,'forced atomic fault'); END`); err != nil {
				t.Fatal(err)
			}
			if got, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err == nil || got.OperationID != "" {
				t.Fatal("provisional success", got, err)
			}
			assertAtomicAdmission(t, s, r, 0)
			var op, step string
			if err := s.db.QueryRow(`SELECT o.status,s.status FROM operations o JOIN execution_steps s ON s.operation_id=o.id`).Scan(&op, &step); err != nil || op != "manual_validation_succeeded" || step != "validation_succeeded" {
				t.Fatal("lifecycle half", op, step, err)
			}
			if count(t, s.db, "canary_runtime_audit") != beforeAudit || count(t, s.db, "canary_alert_outbox") != beforeAlert {
				t.Fatal("orphan audit/outbox")
			}
			// Actual file close/reopen, not a second object on an open handle.
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			fresh, err := storage.Open(context.Background(), storage.TradeOwner, path)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			s, err = NewStore(fresh)
			if err != nil {
				t.Fatal(err)
			}
			assertAtomicAdmission(t, s, r, 0)
			if _, err := s.db.Exec(`DROP TRIGGER atomic_fault`); err != nil {
				t.Fatal(err)
			}
			if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal("rollback poisoned retry", err)
			}
		})
	}
}

func TestHTTPC3B3AtomicOwnerRollbackAndFinalExpiry(t *testing.T) {
	for _, point := range []string{"before_command", "after_command", "before_commit", "expired_final", "backward_final", "expired_commit"} {
		t.Run(point, func(t *testing.T) {
			_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
			w := atomicWriter(s)
			w.checkpoint = func(stage string) error {
				if stage == point {
					return errors.New("owner fault")
				}
				if stage == "after_command" && point == "expired_final" {
					w.now = func() time.Time { return testNow.Add(30 * time.Second) }
				}
				if stage == "after_command" && point == "backward_final" {
					w.now = func() time.Time { return testNow.Add(-time.Nanosecond) }
				}
				if stage == "before_commit" && point == "expired_commit" {
					w.now = func() time.Time { return testNow.Add(30 * time.Second) }
				}
				return nil
			}
			if got, err := w.admit(context.Background(), h.identity, r.RequestID, scope); err == nil || got.OperationID != "" {
				t.Fatal("owner fault success", got, err)
			}
			assertAtomicAdmission(t, s, r, 0)
		})
	}
}

func TestHTTPC3B3AtomicDirectSQLHalfAndMutationBypassRejected(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
	for _, statement := range []string{
		`UPDATE operations SET status='manual_admission_succeeded'`, `UPDATE execution_steps SET status='manual_admitted'`,
		`UPDATE operations SET status='admitted'`, `UPDATE operations SET status='executing'`,
		`UPDATE canary_authorization_usage SET operation_count=1,total_input='1'`,
		`INSERT INTO canary_authorization_operation_usage VALUES('b2-auth','` + *r.OperationID + `','1','RESERVED','2026-10-07T12:00:00Z','2026-10-07T12:00:00Z')`,
	} {
		if _, err := s.db.Exec(statement); err == nil {
			t.Fatal("half bypass", statement)
		}
	}
	if err := exposureParent(context.Background(), s.db, r, h.identity.WalletID, "reserved"); err == nil {
		t.Fatal("standalone declared-auth reservation accepted")
	}
	assertAtomicAdmission(t, s, r, 0)
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE manual_admission_commands SET contract_hash='bad'`, `DELETE FROM manual_admission_commands`,
		`INSERT OR REPLACE INTO manual_admission_commands SELECT * FROM manual_admission_commands`,
		`UPDATE canary_authorization_operation_usage SET amount='0'`, `DELETE FROM canary_authorization_operation_usage`,
		`INSERT OR REPLACE INTO canary_authorization_operation_usage SELECT * FROM canary_authorization_operation_usage`,
		`UPDATE canary_authorization_usage SET operation_count=0,total_input='0'`,
		`UPDATE canary_authorization_usage SET operation_count=2,total_input=manual_u256_add(total_input,total_input)`,
		`UPDATE operations SET status='admitted'`, `UPDATE execution_steps SET status='validation_succeeded'`,
		`UPDATE canary_admission_decisions SET amount='0'`, `DELETE FROM canary_admission_decisions`,
		`INSERT OR REPLACE INTO canary_admission_decisions SELECT * FROM canary_admission_decisions`,
		`UPDATE canary_admission_attempts SET reason_code='bad'`, `DELETE FROM canary_admission_attempts`,
		`INSERT OR REPLACE INTO canary_admission_attempts SELECT * FROM canary_admission_attempts`,
		`INSERT OR REPLACE INTO canary_runtime_audit SELECT * FROM canary_runtime_audit WHERE event_type='MANUAL_ATOMIC_ADMISSION'`,
		`DELETE FROM canary_audit_events`, `UPDATE canary_audit_events SET reason_code='bad'`,
		`INSERT OR REPLACE INTO canary_audit_events SELECT * FROM canary_audit_events`,
		`DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-admission-alert:%'`,
		`UPDATE canary_alert_outbox SET audit_id='bad' WHERE id LIKE 'manual-admission-alert:%'`,
		`INSERT OR REPLACE INTO canary_alert_outbox SELECT * FROM canary_alert_outbox WHERE id LIKE 'manual-admission-alert:%'`,
	} {
		if _, err := s.db.Exec(statement); err == nil {
			t.Fatal("mutation bypass", statement)
		}
	}
	assertAtomicAdmission(t, s, r, 1)
}

func TestHTTPC3B3AtomicLifetimeBudgetEqualityPlusOneAndReleasedHistory(t *testing.T) {
	for _, caps := range []struct {
		operations uint64
		input      string
		admitted   int
	}{
		{2, "1000000000000000000", 2}, // operation count exhausted
		{10, "20000000000000000", 2},  // exact total input equality
		{10, "19999999999999999", 1},  // second input exceeds by one wei
	} {
		t.Run(fmt.Sprintf("%d-%s", caps.operations, caps.input), func(t *testing.T) {
			_, s, h, r, scope, _ := manualInputsFixtureBudget(t, "buy", caps.operations, caps.input)
			records := []Record{r, exposureOrder(t, h, "budget-second", "buy"), exposureOrder(t, h, "budget-third", "buy")}
			w := atomicWriter(s)
			for i, record := range records {
				got, err := w.admit(context.Background(), h.identity, record.RequestID, scope)
				if i >= caps.admitted {
					if err == nil || got.OperationID != "" || inputFailureCode(err) != "AUTHORIZATION_BUDGET_UNVERIFIABLE_OR_EXCEEDED" {
						t.Fatal("overcommit/unexplained acceptance", i, got, err)
					}
					continue
				}
				if err != nil || got.Duplicate {
					t.Fatal("exact limit rejected", i, got, err)
				}
				// Disposable known-unsent fixture: this disconnected graph has
				// no artifacts/nonce/submission. Releasing risk MUST NOT refund
				// authorization's cumulative allowance, irrespective of state.
				if _, err = s.db.Exec(`UPDATE canary_risk_reservations SET state='released' WHERE operation_id=?`, *record.OperationID); err != nil {
					t.Fatal(err)
				}
				if _, err = s.db.Exec(`UPDATE canary_authorization_operation_usage SET state='RELEASED' WHERE operation_id=?`, *record.OperationID); err != nil {
					t.Fatal(err)
				}
				dup, err := w.admit(context.Background(), h.identity, record.RequestID, scope)
				if err != nil || !dup.Duplicate || dup.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" {
					t.Fatal("released duplicate reconsumed", dup, err)
				}
			}
			var used, rows, complete int
			var total, sum string
			if err := s.db.QueryRow(`SELECT operation_count,total_input,(SELECT COUNT(*) FROM canary_authorization_operation_usage),
 (SELECT manual_u256_sum_json(json_group_array(amount)) FROM canary_authorization_operation_usage),(SELECT COUNT(*) FROM manual_admission_complete) FROM canary_authorization_usage`).Scan(&used, &total, &rows, &sum, &complete); err != nil || used != caps.admitted || rows != used || complete != used || sum != total {
				t.Fatal("lifetime conservation", used, rows, complete, total, sum, err)
			}
		})
	}
}

func TestHTTPC3B3AtomicOuterConflictIgnoreCannotSkipLedgerHalf(t *testing.T) {
	for _, table := range []string{"canary_risk_reservations", "manual_reservation_units", "canary_authorization_operation_usage", "canary_admission_decisions", "canary_admission_attempts", "canary_audit_events", "canary_runtime_audit", "canary_alert_outbox"} {
		t.Run(table, func(t *testing.T) {
			_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
			c, err := contractReader(t, s).Load(context.Background(), h.identity, r.RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var policyHash string
			if err = s.db.QueryRow(`SELECT policy_hash FROM manual_risk_policies WHERE id=?`, scope.ManualPolicyID).Scan(&policyHash); err != nil {
				t.Fatal(err)
			}
			gateHash, err := manualGateEvidenceHash(context.Background(), s.db, testNow)
			if err != nil {
				t.Fatal(err)
			}
			cmd := manualAdmissionCommand{Operation: c.OperationID, Request: c.RequestID, Step: c.StepID, Reservation: "manual-risk:" + c.OperationID, Scope: scope, PolicyHash: policyHash, GateHash: gateHash, ContractRaw: encode(c), ContractHash: c.BindingHash, CheckedAt: testNow.Format(time.RFC3339Nano)}
			if _, err = s.db.Exec(`CREATE TRIGGER atomic_ignore BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(IGNORE); END`); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`INSERT OR IGNORE INTO manual_admission_commands(`+manualCommandColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, cmd.arguments()...); err == nil {
				t.Fatal("ignored half accepted", table)
			}
			assertAtomicAdmission(t, s, r, 0)
		})
	}
}

func TestHTTPC3B3AtomicTwoHandlesDistinctAndDuplicateWalletLane(t *testing.T) {
	_, s, h, r, scope, path := manualInputsFixture(t, "buy")
	// Validate a second distinct operation BEFORE reservation owns the lane.
	second := exposureOrder(t, h, "second-atomic", "buy")
	fresh, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	other, err := NewStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan manualAdmissionResult, 32)
	errorsC := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := s
			if i%2 == 1 {
				store = other
			}
			req := r.RequestID
			if i%3 == 0 {
				req = second.RequestID
			}
			got, err := atomicWriter(store).admit(context.Background(), h.identity, req, scope)
			if err != nil {
				errorsC <- err
			} else {
				results <- got
			}
		}(i)
	}
	wg.Wait()
	close(results)
	close(errorsC)
	accepted, duplicates, rejected := 0, 0, 0
	for got := range results {
		if got.Duplicate {
			duplicates++
		} else if got.Status == "ADMITTED_NOT_SIGNED_NOT_SUBMITTED" {
			accepted++
		} else {
			t.Fatal("unexplained accept", got)
		}
	}
	for range errorsC {
		rejected++
	}
	if accepted != 1 || duplicates == 0 || rejected == 0 || accepted+duplicates+rejected != 32 {
		t.Fatal("lane conservation", accepted, duplicates, rejected)
	}
	assertAtomicAdmission(t, s, r, 1)
}

func TestHTTPC3B3AtomicCommitWindowControlWriterCannotInterleave(t *testing.T) {
	for _, update := range []string{`UPDATE canary_control_state SET emergency_stopped=1`, `UPDATE canary_runtime_authorizations SET state='REVOKED'`,
		`UPDATE canary_control_state SET policy_version=2`, `UPDATE canary_runtime_authorizations SET epoch=2`,
		`UPDATE canary_runtime_authorizations SET deployment_id='another'`, `UPDATE canary_gate_attestations SET evidence_hash='` + strings.Repeat("0", 64) + `'`} {
		t.Run(update, func(t *testing.T) {
			_, s, h, r, scope, path := manualInputsFixture(t, "buy")
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
			w := atomicWriter(s)
			w.checkpoint = func(stage string) error {
				if stage == "before_commit" {
					close(entered)
					<-allow
				}
				return nil
			}
			admission := make(chan error, 1)
			go func() { _, err := w.admit(context.Background(), h.identity, r.RequestID, scope); admission <- err }()
			<-entered
			// busy_timeout=0 turns a real writer conflict into a deterministic
			// barrier assertion, not a sleep-based timing claim.
			conn, err := db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = conn.ExecContext(context.Background(), `PRAGMA busy_timeout=0`); err != nil {
				t.Fatal(err)
			}
			if _, err = conn.ExecContext(context.Background(), update); err == nil || !strings.Contains(err.Error(), "SQLITE_BUSY") {
				t.Fatal("mutable control committed before admission commit")
			}
			release.Do(func() { close(allow) })
			if err = <-admission; err != nil {
				t.Fatal(err)
			}
			_, err = conn.ExecContext(context.Background(), update)
			immutable := strings.Contains(update, "SET epoch") || strings.Contains(update, "SET deployment_id") || strings.Contains(update, "canary_gate_attestations")
			if immutable && err == nil || !immutable && err != nil {
				t.Fatal("post-commit control/immutability contract", err)
			}
			conn.Close()
			assertAtomicAdmission(t, s, r, 1)
		})
	}
}

func TestHTTPC3B3AtomicCanceledCommitAndConnectionControls(t *testing.T) {
	for _, kind := range []string{"canceled_commit", "foreign_keys_off", "synchronous_off", "replacement_connection"} {
		t.Run(kind, func(t *testing.T) {
			_, s, h, r, scope, _ := manualInputsFixture(t, "buy")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := atomicWriter(s)
			switch kind {
			case "canceled_commit":
				w.checkpoint = func(stage string) error {
					if stage == "before_commit" {
						cancel()
					}
					return nil
				}
			case "foreign_keys_off":
				if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
					t.Fatal(err)
				}
			case "synchronous_off":
				if _, err := s.db.Exec(`PRAGMA synchronous=OFF`); err != nil {
					t.Fatal(err)
				}
			case "replacement_connection":
				s.db.SetMaxIdleConns(0)
			}
			got, err := w.admit(ctx, h.identity, r.RequestID, scope)
			if err == nil || got.OperationID != "" {
				t.Fatal("commit/control failure escaped", got, err)
			}
			assertAtomicAdmission(t, s, r, 0)
		})
	}
}

func TestHTTPC3B3AtomicAdmissionDependencyGraphDisconnected(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "manual_atomic_admission.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, im := range f.Imports {
		path := strings.Trim(im.Path.Value, `"`)
		if strings.Contains(path, "/trade") || strings.Contains(path, "net/") || strings.Contains(path, "signer") {
			t.Fatal("prohibited dependency", path)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			for _, bad := range []string{"Submit", "ReplayUnknown", "SendRawTransaction", "Sign", "ReserveNonce", "Resolve", "Complete"} {
				if sel.Sel.Name == bad {
					t.Fatal("prohibited call", bad)
				}
			}
		}
		return true
	})
	// Internal composition intentionally is not exported for HTTP options.
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && ast.IsExported(fn.Name.Name) {
			t.Fatalf("admission exposure: %s", fn.Name.Name)
		}
	}
}

func TestHTTPC3B4TenThousandMixedTwoHandleConservation(t *testing.T) {
	_, s, h, r, scope, path := manualInputsFixture(t, "buy")
	type candidate struct {
		id      Identity
		scope   manualAdmissionScope
		records []Record
	}
	groups := []candidate{{h.identity, scope, []Record{r}}}
	var policyHash, gateHash string
	if err := s.db.QueryRow(`SELECT policy_hash FROM manual_risk_policies WHERE id=?`, scope.ManualPolicyID).Scan(&policyHash); err != nil {
		t.Fatal(err)
	}
	gateHash, err := manualGateEvidenceHash(context.Background(), s.db, testNow)
	if err != nil {
		t.Fatal(err)
	}
	stamp := testNow.Format(time.RFC3339Nano)
	for i := 1; i < 8; i++ {
		walletID, address := fmt.Sprintf("mixed-wallet-%d", i), fmt.Sprintf("0x%040x", 100+i)
		id := Identity{PrincipalID: "caller-01", WalletID: walletID, WalletAddress: address}
		if _, err = s.db.Exec(`INSERT INTO dry_run_wallets VALUES(?,4663,?,1)`, walletID, address); err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`INSERT INTO canary_wallet_allowlist VALUES(?,?,4663,1,1,?)`, walletID, address, stamp); err != nil {
			t.Fatal(err)
		}
		api, err := NewWithOptions(s, id, testCredential, h.policy, nil, Options{ManualIntake: true, ValidationBackend: &validationBackend{wallet: address}, ValidationPolicyVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		api.now, api.validator.now = h.now, h.now
		sc := scope
		sc.AuthorizationID = fmt.Sprintf("mixed-auth-%d", i)
		sc.AuthorizationEpoch = uint64(i + 1)
		asset, decimals, side, cap := "native", 18, "buy", "1000000000000000000"
		if i%2 == 1 {
			asset, decimals, side, cap = testToken, 6, "sell", "1000000000"
		}
		if _, err = s.db.Exec(`INSERT INTO canary_runtime_authorizations(id,epoch,mode,environment,chain_id,policy_version,wallet_id,build_sha,release_id,deployment_id,gate_evidence_set_hash,authorization_ref,max_operations,max_total_input,state,authorized_at,expires_at,created_at,updated_at)
 VALUES(?,?,'CONTROLLED_CANARY',?,4663,1,?,?,?,?,?,'local-only-not-granted',1,?,'PENDING',?,?,?,?)`, sc.AuthorizationID, sc.AuthorizationEpoch, sc.Environment, id.WalletID, sc.BuildSHA, sc.ReleaseID, sc.DeploymentID, gateHash, cap, stamp, testNow.Add(time.Hour).Format(time.RFC3339Nano), stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`INSERT INTO canary_authorization_usage VALUES(?,0,'0',?)`, sc.AuthorizationID, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`INSERT INTO manual_authorization_units VALUES(?,?,?,?,'MANUAL',4663,?,?,'TRADE_INPUT',?)`, sc.AuthorizationID, sc.AuthorizationEpoch, sc.ManualPolicyID, policyHash, asset, decimals, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`UPDATE canary_runtime_authorizations SET state='ARMED' WHERE id=?`, sc.AuthorizationID); err != nil {
			t.Fatal(err)
		}
		group := candidate{id, sc, nil}
		for j := 0; j < 4; j++ {
			group.records = append(group.records, exposureOrder(t, api, fmt.Sprintf("mixed-%d-%d", i, j), side))
		}
		groups = append(groups, group)
	}
	for i := 1; i < 4; i++ {
		groups[0].records = append(groups[0].records, exposureOrder(t, h, fmt.Sprintf("mixed-0-%d", i), "buy"))
	}
	fresh, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	other, err := NewStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	var accepted, deduped, rejected, unexplained atomic.Int64
	jobs := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			store := s
			if worker%2 == 1 {
				store = other
			}
			w := atomicWriter(store)
			for n := range jobs {
				g := groups[n%len(groups)]
				request := g.records[(n/len(groups))%len(g.records)].RequestID
				sc := g.scope
				if n%11 == 0 {
					sc.DeploymentID = "unauthorized-deployment"
				}
				if n%17 == 0 {
					request = "missing-request"
				}
				got, err := w.admit(context.Background(), g.id, request, sc)
				if err != nil {
					rejected.Add(1)
					if got.OperationID != "" {
						unexplained.Add(1)
					}
					continue
				}
				if n%11 == 0 || n%17 == 0 {
					unexplained.Add(1)
				}
				if got.Duplicate && got.Status == "HISTORICAL_ADMISSION_NOT_PERMISSION" {
					deduped.Add(1)
				} else if !got.Duplicate && got.Status == "ADMITTED_NOT_SIGNED_NOT_SUBMITTED" {
					accepted.Add(1)
				} else {
					unexplained.Add(1)
				}
			}
		}(worker)
	}
	totalRequests := 10000
	if raceEnabled {
		totalRequests = 1000
	}
	for i := 0; i < totalRequests; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if accepted.Load() != 7 || deduped.Load() == 0 || rejected.Load() == 0 || accepted.Load()+deduped.Load()+rejected.Load() != int64(totalRequests) || unexplained.Load() != 0 {
		t.Fatal("mixed request conservation", accepted.Load(), deduped.Load(), rejected.Load(), unexplained.Load())
	}
	var durable, countUsage, countOps, countReservations, overcommit int64
	if err = s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM manual_admission_complete),
 (SELECT COALESCE(SUM(operation_count),0) FROM canary_authorization_usage),
 (SELECT COUNT(*) FROM canary_authorization_operation_usage),(SELECT COUNT(*) FROM canary_risk_reservations),
 (SELECT COUNT(*) FROM canary_authorization_usage u JOIN canary_runtime_authorizations a ON a.id=u.authorization_id
 WHERE u.operation_count>a.max_operations OR manual_u256_cmp(u.total_input,a.max_total_input)>0 OR u.total_input!=(SELECT manual_u256_sum_json(json_group_array(amount)) FROM canary_authorization_operation_usage WHERE authorization_id=a.id))`).Scan(&durable, &countUsage, &countOps, &countReservations, &overcommit); err != nil || durable != accepted.Load() || countUsage != durable || countOps != durable || countReservations != durable || overcommit != 0 {
		t.Fatal("durable budget conservation", durable, countUsage, countOps, countReservations, overcommit, err)
	}
	var doubled int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM (SELECT wallet_id,COUNT(*) n FROM canary_risk_reservations WHERE state IN ('reserved','frozen') GROUP BY wallet_id HAVING n>1)`).Scan(&doubled); err != nil || doubled != 0 {
		t.Fatal("wallet lane overcommit", doubled, err)
	}
	withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
		var raw string
		if err := conn.QueryRowContext(ctx, `SELECT policy_json FROM manual_risk_policies WHERE id=?`, scope.ManualPolicyID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		p, _, err := loadManualPolicy(ctx, conn, scope.ManualPolicyID, ManualExecutionContract{PolicyVersion: 1, PolicyHash: r.Validation.PolicyHash}, testNow)
		if err != nil {
			t.Fatal(err)
		}
		exposure, err := loadManualExposureTx(ctx, conn, h.identity.WalletID, p, testNow)
		if err != nil {
			t.Fatal(err)
		}
		if exposure.GlobalWindowOperations != uint64(durable) {
			t.Fatal("all-asset window conservation", exposure.GlobalWindowOperations, durable)
		}
		for unit, total := range exposure.GlobalInput {
			limit, err := p.Limit(unit)
			if err != nil || manualrisk.CheckCap(manualrisk.Quantity{Unit: unit, Units: "0"}, total, manualrisk.Quantity{Unit: unit, Units: limit.MaxGlobalActive}) != nil {
				t.Fatal("unit overcommit", unit, total, err)
			}
		}
	})
	for _, table := range []string{"transaction_attempts", "transaction_submissions", "execution_reservations", "execution_wallet_lanes", "canary_send_permits", "canary_runtime_gate_snapshots"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("prohibited work", table)
		}
	}
	t.Logf("requests=10000 accepted=%d deduped=%d rejected=%d unexplained=0 overcommit=0", accepted.Load(), deduped.Load(), rejected.Load())
}

// Keep the interface evidence explicit: no tx/connection escapes are supplied
// to fault hooks and no background write transaction survives this function.
var _ interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
} = (*sql.Conn)(nil)
