package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestManualRuntimeClockUpgradeFrom038AtomicNoRepair(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, TradeOwner, filepath.Join(t.TempDir(), "upgrade-039.db"))
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
		if version > 38 {
			continue
		}
		raw, err := migrationFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(raw)); err != nil {
			t.Fatal(name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations VALUES(?,'2026-10-16T00:00:00Z')`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO canary_runtime_audit(id,event_type,operation_id,reason_code,details_json,created_at)
 VALUES(X'72657461696E6564',X'4C4547414359',X'6F727068616E','retained','{}','2026-10-16T00:00:00Z');
 CREATE TRIGGER fail_clock_journal BEFORE INSERT ON schema_migrations WHEN NEW.version=39 BEGIN SELECT RAISE(ABORT,'forced 039 journal failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	query := `SELECT json_group_array(json_object('name',name,'sql',sql)) FROM (SELECT name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name)`
	var before, after string
	if err := d.db.QueryRow(query).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "forced 039 journal failure") {
		t.Fatal("late journal failure accepted", err)
	}
	if err := d.db.QueryRow(query).Scan(&after); err != nil || after != before {
		t.Fatal("039 partial objects survived failure", err)
	}
	if _, err := d.db.Exec(`DROP TRIGGER fail_clock_journal`); err != nil {
		t.Fatal(err)
	}
	rows, err := d.db.Query(`SELECT name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	old := map[string]string{}
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		old[name] = definition
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for i := 0; i < 2; i++ {
		if err := d.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for name, definition := range old {
		if name == "manual_b1_no_permit" || name == "manual_b1_no_pass_snapshot" {
			continue
		}
		var actual string
		if err := d.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name=?`, name).Scan(&actual); err != nil || actual != definition {
			t.Fatal("039 changed/deleted old schema object", name, err)
		}
	}
	var versions, retained, evidence, scopes, empty int
	if err := d.db.QueryRow(`SELECT (SELECT COUNT(*) FROM schema_migrations WHERE version=39),
 (SELECT COUNT(*) FROM canary_runtime_audit WHERE typeof(id)='blob' AND hex(id)='72657461696E6564' AND typeof(event_type)='blob' AND typeof(operation_id)='blob'),
 (SELECT COUNT(*) FROM manual_gate_clock_events),(SELECT COUNT(*) FROM manual_gate_clock_scopes),(SELECT COUNT(*) FROM manual_gate_clock_empty)`).Scan(&versions, &retained, &evidence, &scopes, &empty); err != nil || versions != 1 || retained != 1 || evidence != 0 || scopes != 0 || empty != 0 {
		t.Fatal("039 repaired/created evidence or scope", versions, retained, evidence, scopes, empty, err)
	}
}
