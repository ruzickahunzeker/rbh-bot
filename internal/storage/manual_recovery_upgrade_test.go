package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestManualRecovery045UpgradeBackfillAndAtomicRollback(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade-045.db")
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
	if _, err := tx.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			t.Fatal(err)
		}
		if version > 44 {
			continue
		}
		raw, err := migrationFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(raw)); err != nil {
			t.Fatal(name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations VALUES(?,'2026-10-26T00:00:00Z')`, version); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// A schema-level intake fixture, not executable/admitted work. Upgrading
	// must preserve it and its already-exposed intake cursor, not adopt it into
	// a submission or invent recovery commands. HTTP identity tests live elsewhere.
	if _, err := d.db.Exec(`
 INSERT INTO dry_run_wallets(id,chain_id,address,enabled) VALUES('upgrade-wallet',4663,'0x1111111111111111111111111111111111111111',1);
 INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at,request_json,updated_at,deadline_capability,expires_at)
 VALUES('manual_op_upgrade',4663,'upgrade-wallet','manual:req_upgrade',printf('%064d',1),'swap','manual_validation_pending','2026-10-26T00:00:00Z','{"chain":"robinhood","wallet_id":"upgrade-wallet"}','2026-10-26T00:00:00Z','APPLICATION_TTL_ONLY','2026-10-26T00:01:00Z');
 INSERT INTO execution_steps(id,operation_id,step_index,kind,status,wallet_id,created_at,updated_at,expires_at)
 VALUES('manual_step_upgrade','manual_op_upgrade',0,'manual_trade_validation','validation_pending','upgrade-wallet','2026-10-26T00:00:00Z','2026-10-26T00:00:00Z','2026-10-26T00:01:00Z');
 INSERT INTO manual_http_order_sources(id,principal_id,idempotency_key,operation_id,step_id,chain,wallet_id,wallet_address,source,request_fingerprint,request_json,api_policy_version,api_policy_hash,api_policy_json,expires_at,created_at,deadline_capability,contract_deadline,send_authorized,submission_queued)
 VALUES('req_upgrade','upgrade-caller','upgrade-key','manual_op_upgrade','manual_step_upgrade','robinhood','upgrade-wallet','0x1111111111111111111111111111111111111111','MANUAL',printf('%064d',1),'{"chain":"robinhood","wallet_id":"upgrade-wallet"}',1,printf('%064d',2),'{}','2026-10-26T00:01:00Z','2026-10-26T00:00:00Z','APPLICATION_TTL_ONLY',0,0,0);
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,reason_code,details_json,created_at)
 VALUES('upgrade-z','MANUAL_UPGRADE_TEST','manual_op_upgrade','HISTORICAL_OBSERVATION','{}','2026-10-26T00:00:01Z'),
 ('upgrade-a','MANUAL_UPGRADE_TEST','manual_op_upgrade','HISTORICAL_OBSERVATION','{}','2026-10-26T00:00:01Z');
 CREATE TRIGGER fail_recovery_upgrade BEFORE INSERT ON schema_migrations WHEN NEW.version=45
 BEGIN SELECT RAISE(ABORT,'forced 045 journal failure'); END;
 `); err != nil {
		t.Fatal(err)
	}
	read := func(query string) string {
		t.Helper()
		var value string
		if err := d.db.QueryRow(query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	schemaQuery := `SELECT json_group_array(json_object('name',name,'sql',sql)) FROM (SELECT name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name)`
	historyQuery := `SELECT json_object('journal',(SELECT json_group_array(json_object('version',version,'at',applied_at)) FROM (SELECT version,applied_at FROM schema_migrations WHERE version<=44 ORDER BY version)),
 'source',(SELECT json_object('id',id,'cursor',sequence,'operation',operation_id,'json',request_json,'expires',expires_at) FROM manual_http_order_sources WHERE id='req_upgrade'),
 'audit',(SELECT json_group_array(json_object('id',id,'event',event_type,'at',created_at)) FROM (SELECT id,event_type,created_at FROM canary_runtime_audit ORDER BY id)))`
	schema, history := read(schemaQuery), read(historyQuery)
	if err := d.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "forced 045 journal failure") {
		t.Fatal("late migration fault ignored", err)
	}
	if read(schemaQuery) != schema || read(historyQuery) != history {
		t.Fatal("failed migration retained partial schema/backfill or changed history")
	}
	if _, err := d.db.Exec(`DROP TRIGGER fail_recovery_upgrade`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := d.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if read(historyQuery) != history {
		t.Fatal("upgrade changed historical rows")
	}
	journalQuery := `SELECT json_group_array(json_object('cursor',cursor,'audit',audit_id)) FROM (SELECT cursor,audit_id FROM manual_lifecycle_events WHERE request_id='req_upgrade' ORDER BY cursor)`
	journal := read(journalQuery)
	if journal != `[{"cursor":1,"audit":"manual-http-audit:req_upgrade"},{"cursor":2,"audit":"upgrade-a"},{"cursor":3,"audit":"upgrade-z"}]` {
		t.Fatal("backfill did not preserve intake cursor or deterministic append order", journal)
	}
	if read(`SELECT json_object('commands',(SELECT COUNT(*) FROM manual_recovery_commands),'submissions',(SELECT COUNT(*) FROM transaction_submissions),'scopes',(SELECT COUNT(*) FROM manual_recovery_scopes))`) != `{"commands":0,"submissions":0,"scopes":0}` {
		t.Fatal("upgrade invented economic/recovery work")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(ctx, TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if read(journalQuery) != journal || read(historyQuery) != history {
		t.Fatal("restart changed assigned history cursors")
	}
}
