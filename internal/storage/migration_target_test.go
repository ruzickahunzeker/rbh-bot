package storage

import (
	"context"
	"io/fs"
	"sort"
	"testing"
)

// migrateTradeThroughTest isolates a historical migration's contract from
// later, explicitly authorized schema changes. Fault/rollback tests still use
// the production Migrate method; this helper only scopes successful assertions.
func migrateTradeThroughTest(t *testing.T, ctx context.Context, d *Database, target int64) {
	t.Helper()
	if d.owner != TradeOwner {
		t.Fatal("historical trade migration helper requires trade owner")
	}
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
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			t.Fatal(err)
		}
		if version > target {
			continue
		}
		found = found || version == target
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=?`, version).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			continue
		}
		raw, err := migrationFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, string(raw)); err != nil {
			t.Fatal(name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations VALUES(?,'2026-10-26T00:00:00Z')`, version); err != nil {
			t.Fatal(name, err)
		}
	}
	var actual int64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&actual); err != nil || !found || actual != target {
		t.Fatal("historical migration boundary mismatch", target, actual, found, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func migrateTradeLatestIdempotentTest(t *testing.T, ctx context.Context, d *Database) {
	t.Helper()
	for i := 0; i < 2; i++ {
		if err := d.Migrate(ctx); err != nil {
			t.Fatal("historical database cannot upgrade idempotently to latest", err)
		}
	}
}
