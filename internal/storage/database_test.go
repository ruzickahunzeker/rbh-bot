package storage

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestOpenMigrateAndValidateEveryOwner(t *testing.T) {
	wantMigrations := map[Owner]int{
		FeedOwner:  4,
		BotOwner:   3,
		TradeOwner: 38, // 015..039 additive; 014 remains on its independent branch.
	}
	for _, owner := range []Owner{FeedOwner, BotOwner, TradeOwner} {
		t.Run(string(owner), func(t *testing.T) {
			database, err := Open(context.Background(), owner, filepath.Join(t.TempDir(), "service.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if err := database.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := database.Migrate(context.Background()); err != nil {
				t.Fatalf("migrations must be idempotent: %v", err)
			}
			var count int
			if err := database.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != wantMigrations[owner] {
				t.Fatalf("got %d migrations, want %d", count, wantMigrations[owner])
			}
		})
	}
}

func TestMigrationLockHonorsContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.db")
	database, err := Open(context.Background(), FeedOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	locker, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	connection, err := locker.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	defer connection.ExecContext(context.Background(), "ROLLBACK")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := database.Migrate(ctx); err == nil {
		t.Fatal("expected locked migration to fail closed")
	}
}

func TestRejectsUnknownOwner(t *testing.T) {
	if _, err := Open(context.Background(), Owner("other-service"), filepath.Join(t.TempDir(), "other.db")); err == nil {
		t.Fatal("expected unknown database owner to be rejected")
	}
}

func TestManualExecutionBindingUpgradeFrom020PreservesDurableRows(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, TradeOwner, filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// Build the real predecessor schema from embedded landed SQL, without 021.
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
	if _, err = tx.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		version, e := migrationVersion(name)
		if e != nil {
			t.Fatal(e)
		}
		if version > 20 {
			continue
		}
		raw, e := migrationFiles.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(string(raw)); e != nil {
			t.Fatal(name, e)
		}
		if _, e = tx.Exec(`INSERT INTO schema_migrations VALUES(?,?)`, version, "2026-01-01T00:00:00Z"); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = tx.Exec(`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('legacy',4663,'wallet','key','fingerprint','swap','submitted','2026-01-01T00:00:00Z');
 INSERT INTO execution_steps(id,operation_id,step_index,kind,status) VALUES('legacy-step','legacy',0,'curve_swap','submitted');
 INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('legacy-attempt','legacy-step','23','submitted','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var op, step, nonce string
	if err = d.db.QueryRow(`SELECT o.status,s.status,t.nonce FROM operations o JOIN execution_steps s ON s.operation_id=o.id JOIN transaction_attempts t ON t.step_id=s.id`).Scan(&op, &step, &nonce); err != nil || op != "submitted" || step != "submitted" || nonce != "23" {
		t.Fatal("migration changed legacy work", op, step, nonce, err)
	}
	var bindings, migrated int
	if err = d.db.QueryRow(`SELECT (SELECT COUNT(*) FROM manual_execution_bindings),(SELECT COUNT(*) FROM schema_migrations WHERE version=21)`).Scan(&bindings, &migrated); err != nil || bindings != 0 || migrated != 1 {
		t.Fatal(bindings, migrated, err)
	}
	if err = d.Migrate(ctx); err != nil {
		t.Fatal("upgrade not idempotent", err)
	}
	for _, name := range []string{"manual_b1_no_arm", "manual_b1_no_permit", "manual_b1_no_pass_snapshot", "manual_command_operation_no_reset", "manual_command_step_no_reset"} {
		var n int
		if err = d.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&n); err != nil || n != 1 {
			t.Fatal("landed guard missing", name, err)
		}
	}
}

func TestManualBindingReferenceUpgradeFrom021PreservesLegacy(t *testing.T) {
	testManualBindingReferenceUpgradePreservesLegacy(t, 21)
}

func TestManualBindingReferenceUpgradeFrom022PreservesLegacy(t *testing.T) {
	testManualBindingReferenceUpgradePreservesLegacy(t, 22)
}

func TestManualBindingAuditUpgradeFrom023PreservesLegacy(t *testing.T) {
	testManualBindingReferenceUpgradePreservesLegacy(t, 23)
}

func TestManualBindingRowidUpgradeFrom024PreservesLegacy(t *testing.T) {
	testManualBindingReferenceUpgradePreservesLegacy(t, 24)
}

func TestManualBindingRetainedLaneUpgradeFrom025PreservesLegacy(t *testing.T) {
	testManualBindingReferenceUpgradePreservesLegacy(t, 25)
}

func testManualBindingReferenceUpgradePreservesLegacy(t *testing.T, predecessor int64) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade-021.db")
	d, err := Open(ctx, TradeOwner, path)
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
		if version > predecessor {
			continue
		}
		raw, e := migrationFiles.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(string(raw)); e != nil {
			t.Fatal(name, e)
		}
		if _, e = tx.Exec(`INSERT INTO schema_migrations VALUES(?,?)`, version, "2026-01-01T00:00:00Z"); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = tx.Exec(`INSERT INTO dry_run_wallets VALUES('legacy-wallet',4663,'0x0000000000000000000000000000000000000001',1);
 INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('legacy',4663,'legacy-wallet','legacy-key','fp','swap','submitted','2026-01-01T00:00:00Z');
 INSERT INTO execution_steps(id,operation_id,step_index,kind,status,wallet_id) VALUES('legacy-step','legacy',0,'curve_swap','submitted','legacy-wallet');
 INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('legacy-attempt','legacy-step','23','submitted','2026-01-01T00:00:00Z');
 INSERT INTO execution_reservations(operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at) VALUES('legacy','legacy-step','legacy-wallet','native','1','2','frozen','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
 INSERT INTO execution_wallet_lanes VALUES('legacy-wallet','0x0000000000000000000000000000000000000001','frozen','legacy','legacy-step','23','ambiguous','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(ctx, TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i := 0; i < 2; i++ {
		if err = d.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var matched int
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM operations o JOIN execution_steps s ON s.operation_id=o.id JOIN transaction_attempts t ON t.step_id=s.id
 JOIN execution_reservations r ON r.operation_id=o.id AND r.step_id=s.id JOIN execution_wallet_lanes l ON l.operation_id=o.id AND l.step_id=s.id
 WHERE o.status='submitted' AND s.status='submitted' AND t.nonce='23' AND r.status='frozen' AND r.input_amount='1' AND r.gas_budget='2'
 AND l.state='frozen' AND l.reserved_nonce='23' AND l.freeze_reason='ambiguous'`).Scan(&matched); err != nil || matched != 1 {
		t.Fatal("022 changed existing legacy economic metadata", matched, err)
	}
	for _, name := range []string{"manual_b1_no_arm", "manual_b1_no_permit", "manual_b1_no_pass_snapshot", "manual_execution_binding_no_update", "manual_binding_reference_no_adoption", "manual_binding_reservation_reference_insert", "manual_binding_lane_reference_insert", "manual_binding_reservation_update_victim", "manual_binding_lane_update_victim", "manual_binding_reservation_insert_victim", "manual_binding_lane_insert_victim", "manual_execution_alert_insert_victim", "manual_execution_alert_update_victim", "manual_execution_alert_identity_full", "manual_execution_alert_retained_no_delete", "manual_execution_audit_insert_victim", "manual_execution_audit_identity_insert"} {
		if err = d.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&matched); err != nil || matched != 1 {
			t.Fatal("closed/additive guard missing", name, err)
		}
	}
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=22`).Scan(&matched); err != nil || matched != 1 {
		t.Fatal("022 journal/idempotency failed", matched, err)
	}
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=23`).Scan(&matched); err != nil || matched != 1 {
		t.Fatal("023 journal/idempotency failed", matched, err)
	}
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=24`).Scan(&matched); err != nil || matched != 1 {
		t.Fatal("024 journal/idempotency failed", matched, err)
	}
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='manual_execution_alert_exact_effect'`).Scan(&matched); err != nil || matched != 1 {
		t.Fatal("024 exact effect guard missing", matched, err)
	}
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM manual_execution_binding_integrity`).Scan(&matched); err != nil || matched != 0 {
		t.Fatal("migration minted a binding", matched, err)
	}
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=25`).Scan(&matched); err != nil || matched != 1 {
		t.Fatal("025 journal/idempotency failed", matched, err)
	}
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='manual_binding_insert_conflict_victim'`).Scan(&matched); err != nil || matched != 1 {
		t.Fatal("025 binding conflict guard missing", matched, err)
	}
	if err = d.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=26`).Scan(&matched); err != nil || matched != 1 {
		t.Fatal("026 journal/idempotency failed", matched, err)
	}
	for _, name := range []string{"manual_binding_lane_retained_delete", "manual_binding_lane_retained_update"} {
		if err = d.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&matched); err != nil || matched != 1 {
			t.Fatal("026 retained-lane guard missing", name, matched, err)
		}
	}
}
