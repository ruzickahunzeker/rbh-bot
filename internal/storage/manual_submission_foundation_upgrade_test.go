package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestManualSubmissionFoundationUpgradeFrom036AtomicNoRepair(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, TradeOwner, filepath.Join(t.TempDir(), "upgrade-037.db"))
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
		if version > 36 {
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
 CREATE TRIGGER fail_foundation_journal BEFORE INSERT ON schema_migrations WHEN NEW.version=37 BEGIN SELECT RAISE(ABORT,'forced journal failure'); END`); err != nil {
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
	if err := d.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "forced journal failure") {
		t.Fatal("late journal failure accepted", err)
	}
	if err := d.db.QueryRow(query).Scan(&after); err != nil || after != before {
		t.Fatal("partial 037 schema survived rollback", err)
	}
	if _, err := d.db.Exec(`DROP TRIGGER fail_foundation_journal`); err != nil {
		t.Fatal(err)
	}
	var oldGuards string
	guardsQuery := `SELECT json_group_array(json_object('name',name,'sql',sql)) FROM (SELECT name,sql FROM sqlite_master WHERE type IN ('trigger','view') AND name NOT LIKE 'manual_submission_foundation_%' AND name NOT LIKE 'manual_gate_%' AND name NOT LIKE 'manual_first_broadcast_%' AND name NOT LIKE 'manual_b23_%' AND name NOT LIKE 'manual_b24_%' AND name NOT IN ('manual_b1_no_permit','manual_b1_no_pass_snapshot') ORDER BY name)`
	if err := d.db.QueryRow(guardsQuery).Scan(&oldGuards); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := d.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.db.QueryRow(guardsQuery).Scan(&after); err != nil || after != oldGuards {
		t.Fatal("037 changed an old guard/view", err)
	}
	var versions, retained, foundations, scopes int
	if err := d.db.QueryRow(`SELECT (SELECT COUNT(*) FROM schema_migrations WHERE version=37),
 (SELECT COUNT(*) FROM canary_runtime_audit WHERE typeof(id)='blob' AND hex(id)='72657461696E6564' AND typeof(event_type)='blob' AND typeof(operation_id)='blob'),
 (SELECT COUNT(*) FROM manual_submission_foundations),(SELECT COUNT(*) FROM manual_submission_foundation_scopes)`).Scan(&versions, &retained, &foundations, &scopes); err != nil || versions != 1 || retained != 1 || foundations != 0 || scopes != 0 {
		t.Fatal("037 repaired retained data or created metadata", versions, retained, foundations, scopes, err)
	}
}
