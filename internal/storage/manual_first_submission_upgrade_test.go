package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestManualFirstSubmissionUpgradeFrom043AtomicNoAdoption(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, TradeOwner, filepath.Join(t.TempDir(), "upgrade-044.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	names, err := fs.Glob(migrationFiles, "migrations/trade/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			t.Fatal(err)
		}
		if version > 43 {
			continue
		}
		raw, err := migrationFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(raw)); err != nil {
			t.Fatal(name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations VALUES(?,'2026-10-26T00:00:00Z')`, version); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Model an imported historical standalone consumed permit. The import
	// intentionally lacks live authorizing sources: migration must retain it,
	// not repair/adopt it into executable work. Full legal B24 fixtures belong
	// to tradeapi acceptance tests; this test covers upgrade non-adoption.
	rows, err := d.db.Query(`SELECT name,sql FROM sqlite_master WHERE type='trigger' AND tbl_name='canary_send_permits' AND sql LIKE '%BEFORE INSERT%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	type trigger struct{ name, sql string }
	var triggers []trigger
	for rows.Next() {
		var item trigger
		if err := rows.Scan(&item.name, &item.sql); err != nil {
			t.Fatal(err)
		}
		triggers = append(triggers, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, item := range triggers {
		if _, err := d.db.Exec(`DROP TRIGGER "` + strings.ReplaceAll(item.name, `"`, `""`) + `"`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.db.Exec(`PRAGMA foreign_keys=OFF;
INSERT INTO canary_send_permits(id,operation_id,attempt_id,authorization_id,authorization_epoch,purpose,artifact_hash,tx_hash,state,issued_at,expires_at,consumed_at)
VALUES('manual-first-permit:retained-attempt','retained-operation','retained-attempt','retained-authorization',1,'FIRST_BROADCAST',
'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
'CONSUMED','2026-10-25T00:00:00Z','2026-10-25T00:01:00Z','2026-10-25T00:00:01Z');
PRAGMA foreign_keys=ON;
INSERT INTO canary_runtime_audit(id,event_type,operation_id,reason_code,details_json,created_at)
VALUES(X'72657461696E6564',X'4C4547414359',X'6F727068616E','retained','{}','2026-10-25T00:00:00Z');
CREATE TRIGGER fail_first_submission_journal BEFORE INSERT ON schema_migrations WHEN NEW.version=44
BEGIN SELECT RAISE(ABORT,'forced 044 journal failure'); END`); err != nil {
		t.Fatal(err)
	}
	for _, item := range triggers {
		if _, err := d.db.Exec(item.sql); err != nil {
			t.Fatal(err)
		}
	}
	read := func(query string) string {
		t.Helper()
		var value string
		if err := d.db.QueryRow(query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	schemaQuery := `SELECT json_group_array(json_object('name',name,'sql',sql)) FROM (SELECT name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name)`
	historyQuery := `SELECT json_object(
'journal',(SELECT json_group_array(json_object('version',version,'applied_at',applied_at)) FROM (SELECT version,applied_at FROM schema_migrations WHERE version<=43 ORDER BY version)),
'permit',(SELECT json_object('id',id,'operation',operation_id,'attempt',attempt_id,'authorization',authorization_id,'epoch',authorization_epoch,'purpose',purpose,'artifact',artifact_hash,'tx',tx_hash,'state',state,'issued',issued_at,'expires',expires_at,'consumed',consumed_at) FROM canary_send_permits WHERE id='manual-first-permit:retained-attempt'),
'audit',(SELECT json_object('id',hex(id),'id_type',typeof(id),'event',hex(event_type),'event_type',typeof(event_type),'operation',hex(operation_id),'operation_type',typeof(operation_id),'reason',reason_code,'details',details_json,'created',created_at) FROM canary_runtime_audit WHERE hex(id)='72657461696E6564'))`
	before, history := read(schemaQuery), read(historyQuery)
	if err := d.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "forced 044 journal failure") {
		t.Fatal("044 late journal failure was ignored", err)
	}
	if read(schemaQuery) != before || read(historyQuery) != history {
		t.Fatal("failed 044 upgrade left partial schema or rewrote historical rows")
	}
	var journal, partial int
	if err := d.db.QueryRow(`SELECT (SELECT COUNT(*) FROM schema_migrations WHERE version=44),(SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'manual_first_submission_%')`).Scan(&journal, &partial); err != nil || journal != 0 || partial != 0 {
		t.Fatal("failed 044 upgrade retained a journal/schema fragment", journal, partial, err)
	}
	if _, err := d.db.Exec(`DROP TRIGGER fail_first_submission_journal`); err != nil {
		t.Fatal(err)
	}
	// The new phase projection must not redefine the prior 036..043 views.
	viewsQuery := `SELECT json_group_array(json_object('name',name,'sql',sql)) FROM (SELECT name,sql FROM sqlite_master WHERE type='view' AND name NOT LIKE 'manual_first_submission_%' ORDER BY name)`
	oldViews := read(viewsQuery)
	for i := 0; i < 2; i++ {
		if err := d.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if read(historyQuery) != history || read(viewsQuery) != oldViews {
		t.Fatal("044 rewrote prior journal/data or widened signed-only views")
	}
	var bindings, submissions, scopes, retained int
	if err := d.db.QueryRow(`SELECT (SELECT COUNT(*) FROM schema_migrations WHERE version=44),
(SELECT COUNT(*) FROM manual_first_submission_bindings),(SELECT COUNT(*) FROM transaction_submissions),
(SELECT COUNT(*) FROM manual_first_submission_scopes)+(SELECT COUNT(*) FROM manual_first_submission_transition_scopes),
(SELECT COUNT(*) FROM canary_send_permits WHERE state='CONSUMED' AND id='manual-first-permit:retained-attempt')`).Scan(&journal, &bindings, &submissions, &scopes, &retained); err != nil || journal != 1 || bindings != 0 || submissions != 0 || scopes != 0 || retained != 1 {
		t.Fatal("044 adopted or reset historical consumed work", journal, bindings, submissions, scopes, retained, err)
	}
	if _, err := d.db.Exec(`INSERT INTO manual_first_submission_scopes(permit_id) VALUES('manual-first-permit:retained-attempt')`); err == nil {
		t.Fatal("historical consumed permit reopened a claim scope")
	}
	if _, err := d.db.Exec(`UPDATE canary_send_permits SET state='ISSUED',consumed_at=NULL WHERE id='manual-first-permit:retained-attempt'`); err == nil {
		t.Fatal("historical consumed permit reset to issued")
	}
	if read(historyQuery) != history {
		t.Fatal("rejected historical adoption changed durable history")
	}
}
