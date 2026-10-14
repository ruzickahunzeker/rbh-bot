package tradeapi

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type signingIntentCommand struct{ operation, owner, facts, digest, observation, observationHash, checked string }

func signingIntentFor(t *testing.T, s *Store, h *API, r Record, scope manualAdmissionScope) signingIntentCommand {
	t.Helper()
	facts, err := loadManualArtifactFacts(context.Background(), s.db, h.identity, r.RequestID, scope)
	if err != nil {
		t.Fatal(err)
	}
	raw, digest, err := facts.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	var sqlFacts string
	if err := s.db.QueryRow(`SELECT facts_json FROM manual_signing_facts WHERE operation_id=?`, facts.OperationID).Scan(&sqlFacts); err != nil || sqlFacts != raw {
		t.Fatal("Go/SQL facts differ", err, sqlFacts, raw)
	}
	b, err := scanManualExecutionBinding(s.db.QueryRow(`SELECT `+manualExecutionBindingColumns+` FROM manual_execution_bindings WHERE operation_id=?`, facts.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	q, err := readManualNonceClaim(context.Background(), s.db, r.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := verifyManualNonceClaim(context.Background(), s.db, q, h.identity, scope)
	if err != nil {
		t.Fatal(err)
	}
	o, err := (&localNonceObserver{nonce: facts.Nonce}).ObserveManualNonce(context.Background(), manualNonceFacts(c, b))
	if err != nil {
		t.Fatal(err)
	}
	observation := manualnonce.Encode(o)
	return signingIntentCommand{facts.OperationID, strings.Repeat("a", 64), raw, digest, observation, manualnonce.Digest(observation), testNow.Format(time.RFC3339Nano)}
}

func insertSigningIntent(s *Store, q signingIntentCommand, verb string) error {
	_, err := s.db.Exec(verb+` INTO manual_signing_intents(operation_id,owner_id,facts_json,facts_hash,observation_json,observation_hash,checked_at) VALUES(?,?,?,?,?,?,?)`, q.operation, q.owner, q.facts, q.digest, q.observation, q.observationHash, q.checked)
	return err
}

func TestHTTPM3B3IntentAtomicAuditHistoryNoSigningOrEconomicEffect(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			d, s, h, r, scope, path := boundSigningFixture(t, side)
			if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			q := signingIntentFor(t, s, h, r, scope)
			if err := insertSigningIntent(s, q, "INSERT"); err != nil {
				t.Fatal(err)
			}
			if count(t, s.db, "manual_signing_intent_complete") != 1 {
				t.Fatal("missing marker audit complete")
			}
			assertNonceClaim(t, s, r, "23")
			for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
				before := admittedDatabaseSnapshot(t, s.db)
				if err := insertSigningIntent(s, q, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("marker reused or re-audited", verb, err)
				}
			}
			if _, err := s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'; UPDATE canary_risk_reservations SET state='frozen'; UPDATE canary_authorization_operation_usage SET state='FROZEN'`); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			_, s = bindingReopen(t, d, path)
			v, err := loadManualSigningIntentHistory(context.Background(), s.db, h.identity, r.RequestID, scope)
			if err != nil || v.Status != "SIGNING_OUTCOME_UNKNOWN_NOT_PERMISSION" || v.OwnerID != q.owner || v.MarkedAt != q.checked || v.FactsHash != q.digest || v.Facts.ExpiresAt != r.ExpiresAt || v.ExecutionAuthorized || v.SigningAuthorized || v.SendAuthorized || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("ambiguity lost or became permission", v, err)
			}
			for _, sql := range []string{`UPDATE manual_signing_intents SET owner_id='other'`, `DELETE FROM manual_signing_intents`, `UPDATE transaction_attempts SET status='signed'`, `UPDATE execution_wallet_lanes SET state='idle',operation_id=NULL,step_id=NULL,reserved_nonce=NULL`, `UPDATE canary_risk_reservations SET state='released'`, `UPDATE canary_authorization_operation_usage SET state='RELEASED'`} {
				if _, err := s.db.Exec(sql); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("ambiguity released or signing guard opened", sql, err)
				}
			}
		})
	}
}

func TestHTTPM3B3IntentSourceGateAndFullByteIdentityDenials(t *testing.T) {
	_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
	if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	original := signingIntentFor(t, s, h, r, scope)
	baseline := admittedDatabaseSnapshot(t, s.db)
	schemaQuery := `SELECT json_group_array(json_object('name',name,'sql',sql)) FROM (SELECT name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name)`
	var baselineSchema string
	if err := s.db.QueryRow(schemaQuery).Scan(&baselineSchema); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"stop", "revoke", "expiry_equality", "auth_expiry", "frozen_risk", "frozen_usage", "wrong_runtime", "wrong_facts_hash", "nul_facts", "owner_nul", "stale_observation", "wrong_nonce", "different_deployment", "unresolved_blob_wallet", "control_time_nul", "retained_contract_deadline", "retained_capability"} {
		t.Run(kind, func(t *testing.T) {
			// All negative cases use the same fixed complete claim, but each
			// receives an isolated SQLite savepoint, including DDL. This avoids
			// reparsing 33 migrations per case under race instrumentation. Real
			// reopen, concurrency and upgrade tests keep independent file DBs.
			if _, err := s.db.Exec(`SAVEPOINT source_case`); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := s.db.Exec(`ROLLBACK TO source_case; RELEASE source_case`); err != nil {
					t.Fatal("case rollback failed", err)
				}
				if admittedDatabaseSnapshot(t, s.db) != baseline {
					t.Fatal("case leaked data into next denial")
				}
				var restoredSchema string
				if err := s.db.QueryRow(schemaQuery).Scan(&restoredSchema); err != nil || restoredSchema != baselineSchema {
					t.Fatal("case leaked schema/guard changes", err)
				}
			}()
			q := original
			switch kind {
			case "retained_contract_deadline", "retained_capability":
				var ddl string
				if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='manual_command_no_update'`).Scan(&ddl); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`DROP TRIGGER manual_command_no_update`); err != nil {
					t.Fatal(err)
				}
				query := `UPDATE manual_admission_commands SET contract_json=json_set(contract_json,'$.contract_deadline',json('true'))`
				if kind == "retained_capability" {
					query = `UPDATE manual_admission_commands SET contract_json=json_set(contract_json,'$.deadline_capability','CONTRACT')`
				}
				if _, err := s.db.Exec(query); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(ddl); err != nil {
					t.Fatal(err)
				}
			case "control_time_nul":
				if _, err := s.db.Exec(`UPDATE canary_control_state SET updated_at=?`, testNow.Format(time.RFC3339Nano)+"\x00"); err != nil {
					t.Fatal(err)
				}
			case "stop":
				if _, err := s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`); err != nil {
					t.Fatal(err)
				}
			case "revoke":
				if _, err := s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
					t.Fatal(err)
				}
			case "expiry_equality":
				q.checked = r.ExpiresAt
			case "auth_expiry":
				q.checked = testNow.Add(time.Hour).Format(time.RFC3339Nano)
			case "frozen_risk":
				if _, err := s.db.Exec(`UPDATE canary_risk_reservations SET state='frozen'`); err != nil {
					t.Fatal(err)
				}
			case "frozen_usage":
				if _, err := s.db.Exec(`UPDATE canary_authorization_operation_usage SET state='FROZEN'`); err != nil {
					t.Fatal(err)
				}
			case "wrong_runtime":
				q.observation = strings.Replace(q.observation, `"runtime_code_hash":"0x`, `"runtime_code_hash":"0x0`, 1)
				q.observationHash = manualnonce.Digest(q.observation)
			case "wrong_facts_hash":
				q.digest = strings.Repeat("b", 64)
			case "nul_facts":
				q.facts += "\x00"
			case "owner_nul":
				q.owner += "\x00"
			case "stale_observation":
				q.checked = testNow.Add(2*time.Second + time.Nanosecond).Format(time.RFC3339Nano)
			case "wrong_nonce":
				q.observation = strings.ReplaceAll(q.observation, `"23"`, `"24"`)
				q.observationHash = manualnonce.Digest(q.observation)
			case "different_deployment":
				q.facts = strings.Replace(q.facts, scope.DeploymentID, "other-deployment", 1)
			case "unresolved_blob_wallet":
				bindingLegacyReferences(t, s)
				if _, err := s.db.Exec(`UPDATE execution_steps SET wallet_id=CAST(? AS BLOB),status='nonce_reserved' WHERE id='alias-step'`, h.identity.WalletID); err != nil {
					t.Fatal(err)
				}
			}
			before := admittedDatabaseSnapshot(t, s.db)
			for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
				if err := insertSigningIntent(s, q, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before || count(t, s.db, "manual_signing_intents") != 0 {
					t.Fatal("invalid intent accepted or partially audited", kind, verb, err)
				}
			}
		})
	}
}

func TestHTTPM3B3IntentAuditAlertFaultsRollbackAndVictims(t *testing.T) {
	for _, fault := range []string{"audit_fail", "audit_ignore", "alert_fail", "alert_ignore"} {
		t.Run(fault, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			q := signingIntentFor(t, s, h, r, scope)
			table, when, action := "canary_runtime_audit", `NEW.event_type='MANUAL_SIGNING_INTENT'`, `RAISE(ABORT,'forced audit failure')`
			if strings.HasPrefix(fault, "alert") {
				table = "canary_alert_outbox"
				when = `NEW.id LIKE 'manual-sign-intent-alert:%'`
			}
			if strings.HasSuffix(fault, "ignore") {
				action = "RAISE(IGNORE)"
			}
			if _, err := s.db.Exec(`CREATE TRIGGER forced_signing_fault BEFORE INSERT ON ` + table + ` WHEN ` + when + ` BEGIN SELECT ` + action + `; END`); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
				if err := insertSigningIntent(s, q, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("half marker audit committed", fault, verb, err)
				}
			}
			if _, err := s.db.Exec(`DROP TRIGGER forced_signing_fault`); err != nil {
				t.Fatal(err)
			}
			if err := insertSigningIntent(s, q, "INSERT"); err != nil {
				t.Fatal(err)
			}
			before = admittedDatabaseSnapshot(t, s.db)
			for _, sql := range []string{
				`INSERT OR REPLACE INTO canary_runtime_audit(rowid,id,event_type,reason_code,details_json,created_at) SELECT rowid,'overwrite','OTHER','x','{}',created_at FROM canary_runtime_audit WHERE event_type='MANUAL_SIGNING_INTENT'`,
				`INSERT OR REPLACE INTO canary_alert_outbox(rowid,id,audit_id,severity,state,created_at) SELECT rowid,'overwrite-alert',audit_id,'WARNING','PENDING',created_at FROM canary_alert_outbox WHERE id LIKE 'manual-sign-intent-alert:%'`,
				`DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-sign-intent-alert:%'`,
				`UPDATE canary_alert_outbox SET severity='INFO' WHERE id LIKE 'manual-sign-intent-alert:%'`,
			} {
				if _, err := s.db.Exec(sql); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("marker witness victim erased", sql, err)
				}
			}
		})
	}
}

func TestHTTPM3B3IntentTwoHandleOneMarkerAndHistoricalNoPermission(t *testing.T) {
	d, s, h, r, scope, path := boundSigningFixture(t, "buy")
	if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	q := signingIntentFor(t, s, h, r, scope)
	// Open another real SQLite handle without closing the original.
	fresh, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	s2, err := NewStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	start := make(chan struct{})
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			cmd := q
			cmd.owner = fmt.Sprintf("%064x", i+1)
			store := s
			if i%2 == 1 {
				store = s2
			}
			errs <- insertSigningIntent(store, cmd, "INSERT")
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	accepted := 0
	for err := range errs {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 || count(t, s.db, "manual_signing_intent_complete") != 1 {
		t.Fatal("multiple marker owners", accepted)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	_, s = bindingReopen(t, d, path)
	result, err := loadManualSigningIntentHistory(context.Background(), s.db, h.identity, r.RequestID, scope)
	if err != nil || result.Status != "SIGNING_OUTCOME_UNKNOWN_NOT_PERMISSION" || result.SigningAuthorized || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("restart permits reinvocation", err)
	}
	if reflect.DeepEqual(result, manualSigningIntentHistory{}) {
		t.Fatal("history lost")
	}
}

func TestHTTPM3B3IntentRetainedOwnerAndWitnessCorruptionNoRepair(t *testing.T) {
	for _, field := range []string{"facts_json", "facts_hash", "owner_id", "observation_hash", "checked_at", "operation_id"} {
		t.Run(field, func(t *testing.T) {
			d, s, h, r, scope, path := boundSigningFixture(t, "buy")
			if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			q := signingIntentFor(t, s, h, r, scope)
			if err := insertSigningIntent(s, q, "INSERT"); err != nil {
				t.Fatal(err)
			}
			s.db.SetMaxOpenConns(1)
			var guard string
			if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='manual_signing_intent_no_update'`).Scan(&guard); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`DROP TRIGGER manual_signing_intent_no_update; PRAGMA foreign_keys=OFF; PRAGMA ignore_check_constraints=ON`); err != nil {
				t.Fatal(err)
			}
			query := `UPDATE manual_signing_intents SET ` + field + `=CAST(` + field + ` AS BLOB)`
			if field == "checked_at" {
				query = `UPDATE manual_signing_intents SET checked_at=checked_at||char(0)`
			}
			if _, err := s.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`PRAGMA foreign_keys=ON; PRAGMA ignore_check_constraints=OFF; ` + guard); err != nil {
				t.Fatal(err)
			}
			for _, phase := range []string{"restored", "reopen"} {
				before := admittedDatabaseSnapshot(t, s.db)
				if phase == "reopen" {
					_, s = bindingReopen(t, d, path)
					if admittedDatabaseSnapshot(t, s.db) != before {
						t.Fatal("reopen repaired retained marker")
					}
				}
				v, err := loadManualSigningIntentHistory(context.Background(), s.db, h.identity, r.RequestID, scope)
				if err == nil || !reflect.DeepEqual(v, manualSigningIntentHistory{}) || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("corrupt marker accepted", field, phase, err)
				}
				for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
					if err := insertSigningIntent(s, q, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
						t.Fatal("retained marker adopted or replaced", field, verb, err)
					}
				}
				if _, err := s.db.Exec(`DELETE FROM manual_signing_intents`); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("retained marker released", err)
				}
			}
		})
	}
}
