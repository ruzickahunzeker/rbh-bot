package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
)

func TestManualFirstBroadcastConsumeUpgradeFrom042Atomic(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, TradeOwner, filepath.Join(t.TempDir(), "upgrade-043.db"))
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
		if version > 42 {
			continue
		}
		raw, err := migrationFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(raw)); err != nil {
			t.Fatal(name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations VALUES(?,'2026-10-25T00:00:00Z')`, version); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// A conflict after 043's early DROP statements must roll the entire
	// migration back, including the original consume deny and audit trigger.
	if _, err := d.db.Exec(`CREATE TABLE manual_first_broadcast_consume_scopes(conflict TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(ctx); err == nil {
		t.Fatal("043 ignored conflicting preexisting schema")
	}
	var version, oldDeny, newView int
	if err := d.db.QueryRow(`SELECT (SELECT MAX(version) FROM schema_migrations),
	 (SELECT COUNT(*) FROM sqlite_master WHERE name='manual_b23_no_permit_consume'),
	 (SELECT COUNT(*) FROM sqlite_master WHERE name='manual_first_broadcast_consume_source')`).Scan(&version, &oldDeny, &newView); err != nil || version != 42 || oldDeny != 1 || newView != 0 {
		t.Fatal("failed upgrade left partial schema", version, oldDeny, newView, err)
	}
	if _, err := d.db.Exec(`DROP TABLE manual_first_broadcast_consume_scopes`); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(ctx); err != nil {
		t.Fatal("043 not idempotent", err)
	}
	var scopes, source, oldGuards int
	if err := d.db.QueryRow(`SELECT (SELECT MAX(version) FROM schema_migrations),
	 (SELECT COUNT(*) FROM manual_first_broadcast_consume_scopes),
	 (SELECT COUNT(*) FROM sqlite_master WHERE name='manual_first_broadcast_consume_source'),
	 (SELECT COUNT(*) FROM sqlite_master WHERE name IN ('manual_b1_no_arm','manual_b1_no_permit','manual_b1_no_pass_snapshot','manual_nonce_no_submission_insert','manual_nonce_risk_no_release'))`).Scan(&version, &scopes, &source, &oldGuards); err != nil || version != 43 || scopes != 0 || source != 1 || oldGuards != 5 {
		t.Fatal("unexpected upgraded contract", version, scopes, source, oldGuards, err)
	}
}
