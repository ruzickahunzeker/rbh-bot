package tradeapi

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

var bindingAuditGuards = []string{
	"manual_execution_alert_insert_victim", "manual_execution_alert_update_victim",
	"manual_execution_alert_identity_full", "manual_execution_alert_retained_no_delete",
	"manual_execution_audit_insert_victim",
	"manual_execution_audit_identity_insert",
	"manual_execution_alert_exact_effect",
}

// Only disposable 024 objects/journal are removed; restore the actual 022 view
// definition for the 023 predecessor. All landed 001..023 guards remain intact.
func bindingPredecessor023(t *testing.T, s *Store) {
	t.Helper()
	for _, name := range bindingAuditGuards {
		if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS ` + name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`DROP VIEW manual_execution_binding_integrity; DROP VIEW IF EXISTS manual_execution_binding_audit_integrity; DELETE FROM schema_migrations WHERE version=24`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../storage/migrations/trade/022_manual_binding_reference_integrity.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, view, ok := strings.Cut(string(raw), "CREATE VIEW manual_execution_binding_integrity AS")
	if !ok {
		t.Fatal("predecessor view missing")
	}
	if _, err := s.db.Exec("CREATE VIEW manual_execution_binding_integrity AS" + view); err != nil {
		t.Fatal(err)
	}
}

func bindingAuditFixture(t *testing.T) (*storage.Database, *Store, *API, Record, manualAdmissionScope, string) {
	t.Helper()
	d, s, h, r, scope, path := bindingFixture(t, "buy")
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO canary_runtime_audit(id,event_type,reason_code,details_json,created_at)
 VALUES('unrelated-audit','LOCAL_METADATA','TEST','{}','2026-01-01T00:00:00Z'),
 ('unused-audit','LOCAL_METADATA','TEST','{}','2026-01-01T00:00:00Z');
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('unrelated-alert','unrelated-audit','INFO','PENDING','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	return d, s, h, r, scope, path
}

func bindingAuditRows(t *testing.T, s *Store) string {
	t.Helper()
	var rows string
	if err := s.db.QueryRow(`SELECT json_object(
 'audits',(SELECT json_group_array(json_object('rowid',rowid,'id',id,'event',event_type,'op',operation_id,'attempt',attempt_id,'auth',authorization_id,'epoch',authorization_epoch,'reason',reason_code,'json',details_json,'created',created_at)) FROM canary_runtime_audit),
 'alerts',(SELECT json_group_array(json_object('rowid',rowid,'id',id,'audit',audit_id,'severity',severity,'state',state,'created',created_at,'delivered',delivered_at)) FROM canary_alert_outbox))`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func bindingAuditSnapshot(t *testing.T, s *Store) string {
	t.Helper()
	return bindingAuditRows(t, s) + admittedDatabaseSnapshot(t, s.db)
}

func assertAuditDuplicate(t *testing.T, s *Store, h *API, r Record, scope manualAdmissionScope, valid bool) {
	t.Helper()
	before := bindingAuditSnapshot(t, s)
	v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
	if valid {
		if err != nil || !v.Duplicate || v.ExecutionAuthorized || v.SendAuthorized || v.ExpiresAt != r.ExpiresAt {
			t.Fatal("history became permission/drift", v, err)
		}
	} else if err == nil || inputFailureCode(err) != "EXECUTION_BINDING_INCOMPLETE" || v != (manualExecutionBindingResult{}) {
		t.Fatal("invalid audit accepted/repaired", v, err)
	}
	if bindingAuditSnapshot(t, s) != before {
		t.Fatal("duplicate changed audit/alert/TTL/usage")
	}
	for _, table := range []string{"transaction_attempts", "transaction_submissions", "execution_reservations", "canary_send_permits", "canary_runtime_gate_snapshots"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("economic row", table)
		}
	}
	if _, err := admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID); err == nil {
		t.Fatal("bound history became M1 candidate")
	}
}

func auditConflictSQL(kind string) string {
	switch kind {
	case "alert_insert_id":
		return `INSERT OR REPLACE INTO canary_alert_outbox SELECT * FROM canary_alert_outbox WHERE id LIKE 'manual-execution-alert:%'`
	case "alert_insert_audit":
		return `INSERT OR REPLACE INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 SELECT 'fresh-alert',audit_id,severity,state,created_at FROM canary_alert_outbox WHERE id LIKE 'manual-execution-alert:%'`
	case "alert_insert_rowid":
		return `INSERT OR REPLACE INTO canary_alert_outbox(rowid,id,audit_id,severity,state,created_at)
 SELECT rowid,'fresh-alert','unused-audit',severity,state,created_at FROM canary_alert_outbox WHERE id LIKE 'manual-execution-alert:%'`
	case "alert_update_id":
		return `UPDATE OR REPLACE canary_alert_outbox SET id=(SELECT id FROM canary_alert_outbox WHERE id LIKE 'manual-execution-alert:%') WHERE id='unrelated-alert'`
	case "alert_update_audit":
		return `UPDATE OR REPLACE canary_alert_outbox SET audit_id=(SELECT audit_id FROM canary_alert_outbox WHERE id LIKE 'manual-execution-alert:%') WHERE id='unrelated-alert'`
	case "alert_update_rowid":
		return `UPDATE OR REPLACE canary_alert_outbox SET rowid=(SELECT rowid FROM canary_alert_outbox WHERE id LIKE 'manual-execution-alert:%') WHERE id='unrelated-alert'`
	case "alert_self_rowid":
		return `UPDATE canary_alert_outbox SET rowid=10000 WHERE id LIKE 'manual-execution-alert:%'`
	case "audit_insert_id":
		return `INSERT OR REPLACE INTO canary_runtime_audit SELECT * FROM canary_runtime_audit WHERE event_type='MANUAL_EXECUTION_BOUND'`
	case "audit_insert_rowid":
		return `INSERT OR REPLACE INTO canary_runtime_audit(rowid,id,event_type,reason_code,details_json,created_at)
 SELECT rowid,'fresh-audit','LOCAL_METADATA','TEST','{}',created_at FROM canary_runtime_audit WHERE event_type='MANUAL_EXECUTION_BOUND'`
	default:
		panic("unknown audit case")
	}
}

func rejectAuditConflict(t *testing.T, s *Store, query string) {
	t.Helper()
	before := bindingAuditSnapshot(t, s)
	if _, err := s.db.Exec(query); err == nil || !strings.Contains(err.Error(), "manual execution") {
		t.Fatal("expected audit identity guard", err)
	}
	if bindingAuditSnapshot(t, s) != before {
		t.Fatal("rejected statement changed row identity/effects")
	}
}

func TestHTTPM2AuditAlertUniqueConflictGuardsAndRestart(t *testing.T) {
	for _, kind := range []string{"alert_insert_id", "alert_insert_audit", "alert_insert_rowid", "alert_update_id", "alert_update_audit", "alert_update_rowid", "alert_self_rowid", "audit_insert_id", "audit_insert_rowid"} {
		t.Run(kind, func(t *testing.T) {
			d, s, h, r, scope, path := bindingAuditFixture(t)
			for _, recursion := range []int{0, 1} {
				if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA recursive_triggers=%d`, recursion)); err != nil {
					t.Fatal(err)
				}
				var fk, actual int
				if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
					t.Fatal("foreign keys disabled", fk, err)
				}
				if err := s.db.QueryRow(`PRAGMA recursive_triggers`).Scan(&actual); err != nil || actual != recursion {
					t.Fatal("recursion setting not applied", actual, err)
				}
				rejectAuditConflict(t, s, auditConflictSQL(kind))
				assertAuditDuplicate(t, s, h, r, scope, true)
			}
			before := bindingAuditSnapshot(t, s)
			_, s = bindingReopen(t, d, path)
			if bindingAuditSnapshot(t, s) != before {
				t.Fatal("restart changed durable audit identity")
			}
			rejectAuditConflict(t, s, auditConflictSQL(kind))
			assertAuditDuplicate(t, s, h, r, scope, true)
		})
	}
}

func TestHTTPM2AuditAlert023NegativeControlAndRetainedUpgrade(t *testing.T) {
	for _, kind := range []string{"alert_insert_audit", "alert_insert_rowid", "alert_update_id", "alert_update_audit", "alert_update_rowid", "alert_self_rowid"} {
		t.Run(kind, func(t *testing.T) {
			d, s, h, r, scope, path := bindingAuditFixture(t)
			bindingPredecessor023(t, s)
			if _, err := s.db.Exec(`PRAGMA recursive_triggers=OFF`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(auditConflictSQL(kind)); err != nil {
				t.Fatal("023 negative control did not reproduce", err)
			}
			complete := count(t, s.db, "manual_execution_binding_integrity")
			if kind != "alert_self_rowid" && complete != 0 {
				t.Fatal("canonical alert unexpectedly retained")
			}
			before := bindingAuditRows(t, s) + bindingVictimState(t, s)
			_, s = bindingReopen(t, d, path)
			if bindingAuditRows(t, s)+bindingVictimState(t, s) != before {
				t.Fatal("upgrade changed retained metadata")
			}
			if count(t, s.db, "schema_migrations") != 23 {
				t.Fatal("024 journal missing")
			}
			assertAuditDuplicate(t, s, h, r, scope, kind == "alert_self_rowid")
		})
	}
}

func TestHTTPM2AuditAlertDeliveryAndLegacyCompatibility(t *testing.T) {
	_, s, h, r, scope, _ := bindingAuditFixture(t)
	for _, recursion := range []int{0, 1} {
		if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA recursive_triggers=%d`, recursion)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE canary_alert_outbox SET state='DELIVERED',delivered_at='2026-01-01T00:00:01Z'
 WHERE id LIKE 'manual-execution-alert:%';
 UPDATE OR REPLACE canary_alert_outbox SET id=id,audit_id=audit_id,rowid=rowid WHERE id='unrelated-alert';
 INSERT OR REPLACE INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('unrelated-alert','unrelated-audit','WARNING','PENDING','2026-01-01T00:00:02Z')`); err != nil {
			t.Fatal("legitimate delivery/legacy write rejected", err)
		}
		assertAuditDuplicate(t, s, h, r, scope, true)
	}
}

// Reconstruct stored predecessor history with actual 001..023 guards and FK
// enforcement intact. This is an offline fixture, not production repair.
func binding023AuditHistory(t *testing.T, s *Store, field string) {
	t.Helper()
	bindingPredecessor023(t, s)
	if _, err := s.db.Exec(`PRAGMA recursive_triggers=OFF;
 CREATE TEMP TABLE m2_saved_audit AS SELECT * FROM canary_runtime_audit WHERE event_type='MANUAL_EXECUTION_BOUND';
 UPDATE OR REPLACE canary_alert_outbox SET rowid=(SELECT rowid FROM canary_alert_outbox WHERE id LIKE 'manual-execution-alert:%') WHERE id='unrelated-alert';
 INSERT OR REPLACE INTO canary_runtime_audit(rowid,id,event_type,reason_code,details_json,created_at)
 SELECT rowid,'detached-audit','LOCAL_METADATA','TEST','{}',created_at FROM canary_runtime_audit WHERE event_type='MANUAL_EXECUTION_BOUND'`); err != nil {
		t.Fatal("023 stored-history fixture", err)
	}
	attempt, details := "attempt_id", "details_json"
	switch field {
	case "attempt":
		attempt = "'metadata-only-attempt'"
	case "extra_json":
		details = `json_set(details_json,'$.additional_metadata','changed')`
	case "whitespace":
		details = `' '||details_json`
	case "reordered_json":
		details = `json_object('reservation_id',json_extract(details_json,'$.reservation_id'),'contract_hash',json_extract(details_json,'$.contract_hash'),'execution_step_id',json_extract(details_json,'$.execution_step_id'),'request_id',json_extract(details_json,'$.request_id'))`
	case "exact_negative_rowid", "alias_audit", "null_alias":
	default:
		t.Fatal("unknown stored audit fixture", field)
	}
	columns := `id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at`
	selects := `id,event_type,operation_id,` + attempt + `,authorization_id,authorization_epoch,reason_code,` + details + `,created_at`
	if field == "exact_negative_rowid" {
		columns, selects = "rowid,"+columns, "-1,"+selects
	}
	additional := ""
	if field == "alias_audit" || field == "null_alias" {
		aliasID := "'retained-binding-alias'"
		if field == "null_alias" {
			aliasID = "NULL" // SQLite TEXT PRIMARY KEY permits this predecessor row.
		}
		additional = `INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT ` + aliasID + `,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at FROM m2_saved_audit;`
	}
	if _, err := s.db.Exec(`INSERT INTO canary_runtime_audit(` + columns + `) SELECT ` + selects + ` FROM m2_saved_audit;
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 SELECT 'manual-execution-alert:'||operation_id,id,'INFO','PENDING',created_at FROM m2_saved_audit;` + additional + `DROP TABLE m2_saved_audit`); err != nil {
		t.Fatal(err)
	}
	if count(t, s.db, "manual_execution_binding_integrity") != 1 {
		t.Fatal("023 view already rejected reconstructed history")
	}
}

func TestHTTPM2AuditExactHistoryUpgradeRejectsWithoutRepair(t *testing.T) {
	for _, field := range []string{"attempt", "extra_json", "whitespace", "reordered_json", "alias_audit", "null_alias"} {
		t.Run(field, func(t *testing.T) {
			d, s, h, r, scope, path := bindingAuditFixture(t)
			binding023AuditHistory(t, s, field)
			assertAuditDuplicate(t, s, h, r, scope, true)
			before := bindingAuditRows(t, s) + bindingVictimState(t, s)
			_, s = bindingReopen(t, d, path)
			if count(t, s.db, "manual_execution_binding_complete") != 1 || count(t, s.db, "manual_execution_binding_audit_integrity") != 0 || count(t, s.db, "manual_execution_binding_integrity") != 0 {
				t.Fatal("024 accepted extended history or changed old positive view")
			}
			if bindingAuditRows(t, s)+bindingVictimState(t, s) != before {
				t.Fatal("upgrade repaired/dropped audit/nonce/TTL/usage")
			}
			assertAuditDuplicate(t, s, h, r, scope, false)
			rejectAuditConflict(t, s, auditConflictSQL("audit_insert_rowid"))
		})
	}
}

func TestHTTPM2AuditTwoHandleConflictNoConsumption(t *testing.T) {
	d, s, h, r, scope, path := bindingAuditFixture(t)
	second, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(second)
	if err != nil {
		t.Fatal(err)
	}
	before := bindingAuditSnapshot(t, s)
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			store := s
			if i%2 != 0 {
				store = s2
			}
			_, err := store.db.Exec(auditConflictSQL("alert_update_audit"))
			results <- err
		}(i)
	}
	for i := 0; i < 8; i++ {
		if err := <-results; err == nil || !strings.Contains(err.Error(), "manual execution alert update conflict victim protected") {
			t.Fatal("not a verified concurrent guard denial", err)
		}
	}
	if bindingAuditSnapshot(t, s) != before {
		t.Fatal("concurrent conflict changed history/budget")
	}
	assertAuditDuplicate(t, s2, h, r, scope, true)
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	_, s = bindingReopen(t, d, path)
	assertAuditDuplicate(t, s, h, r, scope, true)
}

func TestHTTPM2AuditAlertMultirowAtomicRollback(t *testing.T) {
	_, s, h, r, scope, _ := bindingAuditFixture(t)
	rejectAuditConflict(t, s, `UPDATE OR REPLACE canary_alert_outbox SET state='DELIVERED',
 audit_id=CASE WHEN id='unrelated-alert' THEN (SELECT audit_id FROM canary_alert_outbox WHERE id LIKE 'manual-execution-alert:%') ELSE audit_id END
 WHERE id='unrelated-alert' OR id LIKE 'manual-admission-alert:%'`)
	assertAuditDuplicate(t, s, h, r, scope, true)
}

func TestHTTPM2AuditNegativeRowidConservativeInsertion(t *testing.T) {
	d, s, h, r, scope, path := bindingAuditFixture(t)
	binding023AuditHistory(t, s, "exact_negative_rowid")
	_, s = bindingReopen(t, d, path)
	rejectAuditConflict(t, s, `INSERT INTO canary_runtime_audit(id,event_type,reason_code,details_json,created_at)
 VALUES('implicit-rowid-audit','LOCAL_METADATA','TEST','{}','2026-01-01T00:00:00Z')`)
	if _, err := s.db.Exec(`INSERT INTO canary_runtime_audit(rowid,id,event_type,reason_code,details_json,created_at)
 VALUES(10000,'explicit-rowid-audit','LOCAL_METADATA','TEST','{}','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal("nonconflicting explicit rowid rejected", err)
	}
	assertAuditDuplicate(t, s, h, r, scope, true)
}

func TestHTTPM2AuditIdentityFieldsAndAliasesRejected(t *testing.T) {
	_, s, h, r, scope, _ := bindingAuditFixture(t)
	for _, column := range []string{"id", "audit_id", "severity", "created_at"} {
		value := "'different'"
		if column == "audit_id" {
			value = "'unused-audit'"
		} else if column == "severity" {
			value = "'WARNING'"
		}
		rejectAuditConflict(t, s, `UPDATE canary_alert_outbox SET `+column+`=`+value+` WHERE id LIKE 'manual-execution-alert:%'`)
	}
	for _, expression := range []string{"attempt_id", "'metadata-only-attempt'"} {
		rejectAuditConflict(t, s, `INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'alternate-binding-audit',event_type,operation_id,`+expression+`,authorization_id,authorization_epoch,reason_code,details_json,created_at
 FROM canary_runtime_audit WHERE event_type='MANUAL_EXECUTION_BOUND'`)
	}
	assertAuditDuplicate(t, s, h, r, scope, true)
}

func TestHTTPM2AuditMissingExactViewFailsClosed(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(fmt.Sprint(bound), func(t *testing.T) {
			_, s, h, r, scope, _ := bindingFixture(t, "buy")
			if bound {
				if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(`DROP VIEW manual_execution_binding_audit_integrity`); err != nil {
				t.Fatal(err)
			}
			before := bindingAuditSnapshot(t, s)
			v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || v != (manualExecutionBindingResult{}) {
				t.Fatal("missing exact view accepted", v, err)
			}
			requireOperational503(t, err)
			if bindingAuditSnapshot(t, s) != before {
				t.Fatal("missing view repaired or committed binding effects")
			}
		})
	}
}

func TestHTTPM2AuditMigrationRollbackAndRetry(t *testing.T) {
	d, s, h, r, scope, _ := bindingAuditFixture(t)
	bindingPredecessor023(t, s)
	if _, err := s.db.Exec(`CREATE VIEW manual_execution_binding_audit_integrity AS SELECT 0 AS blocker`); err != nil {
		t.Fatal(err)
	}
	before := bindingAuditSnapshot(t, s)
	if err := d.Migrate(context.Background()); err == nil {
		t.Fatal("conflicting schema did not abort migration")
	}
	if bindingAuditSnapshot(t, s) != before || count(t, s.db, "manual_execution_binding_integrity") != 1 {
		t.Fatal("failed migration partially changed previous schema/data")
	}
	for _, name := range bindingAuditGuards {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&n); err != nil || n != 0 {
			t.Fatal("migration retained partial new guard", name, n, err)
		}
	}
	if _, err := s.db.Exec(`DROP VIEW manual_execution_binding_audit_integrity`); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	assertAuditDuplicate(t, s, h, r, scope, true)
}

func TestHTTPM2AuditOrphanedHistoryAndRetainedAlertProtected(t *testing.T) {
	for _, kind := range []string{"alert_insert_audit", "alert_update_audit", "alert_insert_rowid"} {
		t.Run(kind, func(t *testing.T) {
			d, s, h, r, scope, path := bindingAuditFixture(t)
			bindingPredecessor023(t, s)
			if _, err := s.db.Exec(`PRAGMA recursive_triggers=OFF`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(auditConflictSQL(kind)); err != nil {
				t.Fatal(err)
			}
			_, s = bindingReopen(t, d, path)
			if kind == "alert_insert_rowid" {
				// The original audit is unreferenced: no FK can substitute for the
				// new audit victim guard. Stored history must still remain intact.
				rejectAuditConflict(t, s, auditConflictSQL("audit_insert_rowid"))
			} else {
				rejectAuditConflict(t, s, `DELETE FROM canary_alert_outbox WHERE audit_id LIKE 'manual-execution:%'`)
				rejectAuditConflict(t, s, `UPDATE canary_alert_outbox SET created_at='changed' WHERE audit_id LIKE 'manual-execution:%'`)
				rejectAuditConflict(t, s, `INSERT OR REPLACE INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 SELECT 'another-alert',audit_id,severity,state,created_at FROM canary_alert_outbox WHERE audit_id LIKE 'manual-execution:%'`)
			}
			assertAuditDuplicate(t, s, h, r, scope, false)
		})
	}
}

func TestHTTPM2AuditPrebindingAliasRejectsAtomicClaim(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprint(direct), func(t *testing.T) {
			_, s, h, r, scope, _ := bindingFixture(t, "buy")
			b := bindingCommand(t, s, r)
			if _, err := s.db.Exec(`INSERT INTO canary_runtime_audit(id,event_type,operation_id,reason_code,details_json,created_at)
 VALUES('preexisting-binding-alias','MANUAL_EXECUTION_BOUND',?,'TEST','{}',?)`, b.Operation, b.CheckedAt); err != nil {
				t.Fatal(err)
			}
			before := bindingAuditSnapshot(t, s)
			var err error
			if direct {
				err = insertBinding(s, b)
			} else {
				var v manualExecutionBindingResult
				v, err = bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
				if v != (manualExecutionBindingResult{}) {
					t.Fatal("prebinding alias returned success", v)
				}
			}
			if err == nil || !strings.Contains(err.Error(), "manual execution exact audit effect incomplete") {
				t.Fatal("prebinding alias was adopted", err)
			}
			if bindingAuditSnapshot(t, s) != before || count(t, s.db, "manual_execution_bindings") != 0 {
				t.Fatal("direct/Go binding left partial effects or repaired alias")
			}
		})
	}
}
