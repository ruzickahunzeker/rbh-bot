package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
)

func TestManualNonceIdentityUpgradeFrom028AtomicAndNoRepair(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, TradeOwner, filepath.Join(t.TempDir(), "upgrade-029.db"))
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
		if version > 28 {
			continue
		}
		raw, err := migrationFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(raw)); err != nil {
			t.Fatal(name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations VALUES(?,'2026-10-14T00:00:00Z')`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('legacy',4663,X'77616C6C65742D3031','legacy-key','fp','swap','failed','2026-01-01T00:00:00Z');
 INSERT INTO execution_steps(id,operation_id,step_index,kind,status) VALUES('legacy-step','legacy',0,'curve_swap','failed');
 INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('legacy-attempt','legacy-step','23','failed','2026-01-01T00:00:00Z');
 CREATE TRIGGER manual_nonce_missing_operation_update BEFORE INSERT ON operations BEGIN SELECT 1; END`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`PRAGMA foreign_keys=OFF;
 INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at,wallet_id) VALUES('orphan','missing-step','24','failed','2026-01-01T00:00:00Z','wallet-01');
 PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	var oldSchema string
	schemaQuery := `SELECT json_group_array(json_object('name',name,'sql',sql)) FROM (SELECT name,sql FROM sqlite_master WHERE name LIKE 'manual_nonce_%' ORDER BY name)`
	if err := d.db.QueryRow(schemaQuery).Scan(&oldSchema); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(ctx); err == nil {
		t.Fatal("late 029 collision accepted")
	}
	var gotSchema string
	if err := d.db.QueryRow(schemaQuery).Scan(&gotSchema); err != nil || gotSchema != oldSchema {
		t.Fatal("partial schema upgrade", err)
	}
	var journal, half int
	if err := d.db.QueryRow(`SELECT (SELECT COUNT(*) FROM schema_migrations WHERE version=29),(SELECT COUNT(*) FROM sqlite_master WHERE name='manual_nonce_missing_step_insert')`).Scan(&journal, &half); err != nil || journal != 0 || half != 0 {
		t.Fatal("partial journal/guard", journal, half, err)
	}
	if _, err := d.db.Exec(`DROP TRIGGER manual_nonce_missing_operation_update`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := d.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var untouched, claims, orphans int
	if err := d.db.QueryRow(`SELECT (SELECT COUNT(*) FROM schema_migrations WHERE version=29),(SELECT COUNT(*) FROM operations WHERE id='legacy' AND typeof(wallet_id)='blob' AND hex(wallet_id)='77616C6C65742D3031'),(SELECT COUNT(*) FROM manual_nonce_claims),(SELECT COUNT(*) FROM transaction_attempts WHERE id='orphan' AND step_id='missing-step' AND nonce='24' AND wallet_id='wallet-01')`).Scan(&journal, &untouched, &claims, &orphans); err != nil || journal != 1 || untouched != 1 || claims != 0 || orphans != 1 {
		t.Fatal("upgrade repaired or allocated", journal, untouched, claims, orphans, err)
	}
}
