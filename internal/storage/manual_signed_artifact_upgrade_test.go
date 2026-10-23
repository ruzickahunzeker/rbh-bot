package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
)

func TestManualSignedArtifactUpgradeFrom035AtomicNoRepair(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, TradeOwner, filepath.Join(t.TempDir(), "upgrade-036.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.db.SetMaxOpenConns(1)
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
		if version > 35 {
			continue
		}
		raw, err := migrationFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(raw)); err != nil {
			t.Fatal(name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations VALUES(?,'2026-10-15T00:00:00Z')`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO canary_runtime_audit(id,event_type,operation_id,reason_code,details_json,created_at)
 VALUES(X'6C6567616379',X'4C4547414359',X'6F727068616E','retained','{}','2026-10-15T00:00:00Z');
 CREATE TRIGGER fail_artifact_journal BEFORE INSERT ON schema_migrations WHEN NEW.version=36 BEGIN SELECT RAISE(ABORT,'forced journal failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	schemaQuery := `SELECT json_group_array(json_object('name',name,'sql',sql)) FROM (SELECT name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name)`
	var before, after string
	if err := d.db.QueryRow(schemaQuery).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(ctx); err == nil {
		t.Fatal("late 036 journal failure accepted")
	}
	if err := d.db.QueryRow(schemaQuery).Scan(&after); err != nil || after != before {
		t.Fatal("partial 036 scope/view/effect survived rollback", err)
	}
	if _, err := d.db.Exec(`DROP TRIGGER fail_artifact_journal`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := d.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var versions, retained, scopes int
	if err := d.db.QueryRow(`SELECT (SELECT COUNT(*) FROM schema_migrations WHERE version=36),
 (SELECT COUNT(*) FROM canary_runtime_audit WHERE typeof(id)='blob' AND hex(id)='6C6567616379' AND typeof(event_type)='blob' AND typeof(operation_id)='blob'),
 (SELECT COUNT(*) FROM manual_artifact_effect_scopes)`).Scan(&versions, &retained, &scopes); err != nil || versions != 1 || retained != 1 || scopes != 0 {
		t.Fatal("036 journal/data/scope drift", versions, retained, scopes, err)
	}
}
