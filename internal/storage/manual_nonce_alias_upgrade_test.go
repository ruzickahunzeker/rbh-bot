package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestManualNonceAliasUpgradeFrom027AtomicAndNoRepair(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, TradeOwner, filepath.Join(t.TempDir(), "upgrade-028.db"))
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
	if _, err = tx.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		version, e := migrationVersion(name)
		if e != nil {
			t.Fatal(e)
		}
		if version > 27 {
			continue
		}
		raw, e := migrationFiles.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(string(raw)); e != nil {
			t.Fatal(name, e)
		}
		if _, e = tx.Exec(`INSERT INTO schema_migrations VALUES(?,'2026-10-12T00:00:00Z')`, version); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = tx.Exec(`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('legacy',4663,'legacy-wallet','legacy-key','fp','swap','failed','2026-01-01T00:00:00Z');
 INSERT INTO execution_steps(id,operation_id,step_index,kind,status) VALUES('legacy-step','legacy',0,'curve_swap','failed');
 INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('legacy-blob','legacy-step',X'3233','failed','2026-01-01T00:00:00Z');
 CREATE TRIGGER manual_nonce_alias_operation_delete BEFORE INSERT ON operations BEGIN SELECT 1; END`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = d.db.Exec(`PRAGMA foreign_keys=OFF;
 INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at,wallet_id) VALUES('legacy-orphan','missing-step','23','failed','2026-01-01T00:00:00Z','legacy-wallet');
 PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	var oldView string
	if err = d.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='manual_nonce_claim_complete'`).Scan(&oldView); err != nil {
		t.Fatal(err)
	}
	if err = d.Migrate(ctx); err == nil {
		t.Fatal("late migration collision accepted")
	}
	var actualView string
	var halves int
	if err = d.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='manual_nonce_claim_complete'`).Scan(&actualView); err != nil || actualView != oldView {
		t.Fatal("view replacement not rolled back", err)
	}
	if err = d.db.QueryRow(`SELECT (SELECT COUNT(*) FROM schema_migrations WHERE version=28)+(SELECT COUNT(*) FROM sqlite_master WHERE name IN ('manual_nonce_claim_027_complete','manual_nonce_attempt_aliases','manual_nonce_claim_alias_source'))`).Scan(&halves); err != nil || halves != 0 {
		t.Fatal("partial upgrade", halves, err)
	}
	if _, err = d.db.Exec(`DROP TRIGGER manual_nonce_alias_operation_delete`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = d.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var journal, claims, blob int
	var nonce, step, wallet string
	if err = d.db.QueryRow(`SELECT (SELECT COUNT(*) FROM schema_migrations WHERE version=28),(SELECT COUNT(*) FROM manual_nonce_claims),(SELECT COUNT(*) FROM transaction_attempts WHERE id='legacy-blob' AND typeof(nonce)='blob' AND hex(nonce)='3233')`).Scan(&journal, &claims, &blob); err != nil || journal != 1 || claims != 0 || blob != 1 {
		t.Fatal("upgrade repaired/allocated legacy nonce", journal, claims, blob, err)
	}
	if err = d.db.QueryRow(`SELECT nonce,step_id,wallet_id FROM transaction_attempts WHERE id='legacy-orphan'`).Scan(&nonce, &step, &wallet); err != nil || nonce != "23" || step != "missing-step" || wallet != "legacy-wallet" {
		t.Fatal("orphan silently repaired", nonce, step, wallet, err)
	}
	if err = d.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='manual_nonce_claim_complete'`).Scan(&actualView); err != nil || !strings.Contains(actualView, "manual_nonce_attempt_aliases") {
		t.Fatal("upgraded completeness absent", err)
	}
}
