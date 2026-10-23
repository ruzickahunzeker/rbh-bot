package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
)

func TestManualFirstBroadcastUpgradeFrom039AtomicNoRepair(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, TradeOwner, filepath.Join(t.TempDir(), "upgrade-040.db"))
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
	if _, err = tx.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		v, e := migrationVersion(name)
		if e != nil {
			t.Fatal(e)
		}
		if v > 39 {
			continue
		}
		raw, e := migrationFiles.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(string(raw)); e != nil {
			t.Fatal(name, e)
		}
		if _, e = tx.Exec(`INSERT INTO schema_migrations VALUES(?,'2026-10-17T00:00:00Z')`, v); e != nil {
			t.Fatal(e)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var before string
	if err = d.db.QueryRow(`SELECT json_group_array(json_object('name',name,'sql',sql)) FROM (SELECT name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name)`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version, issuance, permits, snapshots int
	if err = d.db.QueryRow(`SELECT MAX(version),(SELECT COUNT(*) FROM manual_first_broadcast_issuances),(SELECT COUNT(*) FROM canary_send_permits),(SELECT COUNT(*) FROM canary_runtime_gate_snapshots) FROM schema_migrations`).Scan(&version, &issuance, &permits, &snapshots); err != nil || version != 41 || issuance != 0 || permits != 0 || snapshots != 0 {
		t.Fatal(version, issuance, permits, snapshots, err)
	}
	var after string
	if err = d.db.QueryRow(`SELECT json_group_array(json_object('name',name,'sql',sql)) FROM (SELECT name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name)`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("040 did not add schema")
	}
	if err = d.Migrate(ctx); err != nil {
		t.Fatal("041 not idempotent", err)
	}
}
