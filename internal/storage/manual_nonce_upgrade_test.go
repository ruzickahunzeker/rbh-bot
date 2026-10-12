package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
)

func TestManualNonceUpgradeFrom026AtomicRollbackAndLegacyPreservation(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, TradeOwner, filepath.Join(t.TempDir(), "upgrade.db"))
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
	if _, err = tx.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		version, e := migrationVersion(name)
		if e != nil {
			t.Fatal(e)
		}
		if version > 26 {
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
	if _, err = tx.Exec(`INSERT INTO dry_run_wallets VALUES('legacy-wallet',4663,'0x0000000000000000000000000000000000000001',1);
 INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('legacy',4663,'legacy-wallet','legacy-key','fp','swap','submitted','2026-01-01T00:00:00Z');
 INSERT INTO execution_steps(id,operation_id,step_index,kind,status,wallet_id) VALUES('legacy-step','legacy',1,'pons_curve_execution','submitted','legacy-wallet');
 INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('legacy-attempt','legacy-step','23','submitted','2026-01-01T00:00:00Z');
 INSERT INTO execution_reservations(operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at) VALUES('legacy','legacy-step','legacy-wallet','native','1','2','frozen','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
 INSERT INTO execution_wallet_lanes VALUES('legacy-wallet','0x0000000000000000000000000000000000000001','frozen','legacy','legacy-step','23','ambiguous','2026-01-01T00:00:00Z');
 CREATE TRIGGER manual_nonce_claim_effect BEFORE INSERT ON operations BEGIN SELECT 1; END`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// A late name collision occurs after guard replacements. The entire additive
	// migration must roll back, retaining all 026 guards and no journal/table half.
	if err = d.Migrate(ctx); err == nil {
		t.Fatal("forced migration failure accepted")
	}
	var tables, oldguards, journal int
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='manual_nonce_claims'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("partial 027 table", tables, err)
	}
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='manual_execution_no_attempt' AND sql LIKE '%manual nonce/signing not connected%'`).Scan(&oldguards); err != nil || oldguards != 1 {
		t.Fatal("026 guard not rolled back", oldguards, err)
	}
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=27`).Scan(&journal); err != nil || journal != 0 {
		t.Fatal("partial migration journal", journal, err)
	}
	if _, err = d.db.Exec(`DROP TRIGGER manual_nonce_claim_effect`); err != nil {
		t.Fatal(err)
	}
	if err = d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var op, attempt, lane, reservation, nonce string
	if err = d.db.QueryRow(`SELECT o.status,t.status,l.state,r.status,l.reserved_nonce FROM operations o JOIN execution_steps s ON s.operation_id=o.id JOIN transaction_attempts t ON t.step_id=s.id JOIN execution_wallet_lanes l ON l.wallet_id=o.wallet_id JOIN execution_reservations r ON r.operation_id=o.id WHERE o.id='legacy'`).Scan(&op, &attempt, &lane, &reservation, &nonce); err != nil || op != "submitted" || attempt != "submitted" || lane != "frozen" || reservation != "frozen" || nonce != "23" {
		t.Fatal("legacy recovery changed", op, attempt, lane, reservation, nonce, err)
	}
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM manual_nonce_claims`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("upgrade allocated nonce", tables, err)
	}
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=27`).Scan(&journal); err != nil || journal != 1 {
		t.Fatal("027 not idempotent", journal, err)
	}
}
