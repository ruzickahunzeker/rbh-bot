package storage

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func openTradeThrough044(t *testing.T, path string) *Database {
	t.Helper()
	ctx := context.Background()
	d, err := Open(ctx, TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
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
	return d
}

func TestManualRecovery045UpgradeBackfillAndAtomicRollback(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade-045.db")
	d := openTradeThrough044(t, path)
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
	d, err := Open(ctx, TradeOwner, path)
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

func TestManualRecovery045RejectsConflictingImportedWalletHistory(t *testing.T) {
	for _, conflict := range []string{"foreign_active_risk", "normalized_address_active_risk", "two_retained_claims"} {
		t.Run(conflict, func(t *testing.T) {
			d := openTradeThrough044(t, filepath.Join(t.TempDir(), "conflicting-import.db"))
			// Model a nonstandard imported/pre-hardening store. These rows are
			// deliberately installed without source guards; this is NOT evidence
			// that ordinary 044 SQL can create authorized nonce claims. The
			// acceptance here is refusal to upgrade/repair such retained data.
			ctx := context.Background()
			conn, err := d.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := conn.QueryContext(ctx, `SELECT name,sql FROM sqlite_master WHERE type='trigger' AND tbl_name IN ('manual_nonce_claims','canary_risk_reservations') ORDER BY name`)
			if err != nil {
				t.Fatal(err)
			}
			type trigger struct{ name, sql string }
			var triggers []trigger
			for rows.Next() {
				var item trigger
				if err := rows.Scan(&item.name, &item.sql); err != nil {
					t.Fatal(err)
				}
				triggers = append(triggers, item)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			for _, item := range triggers {
				if _, err := conn.ExecContext(ctx, `DROP TRIGGER "`+strings.ReplaceAll(item.name, `"`, `""`)+`"`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF;
 INSERT INTO dry_run_wallets(id,chain_id,address,enabled) VALUES('retained-wallet',4663,'0xabcdef1111111111111111111111111111111111',1),('alias-wallet',4663,'0xABCDEF1111111111111111111111111111111111',1);
 INSERT INTO manual_nonce_claims(operation_id,execution_step_id,attempt_id,wallet_id,wallet_address,nonce,reservation_id,contract_hash,observation_json,observation_hash,checked_at)
 VALUES('manual_op_retained','retained-step','retained-attempt','retained-wallet','0xabcdef1111111111111111111111111111111111','1','retained-risk','fixture-contract','{}','fixture-observation','2026-10-26T00:00:00Z');
 INSERT INTO canary_risk_reservations(id,operation_id,wallet_id,token_address,policy_version,input_amount,gas_budget,state,created_at,updated_at)
 VALUES('retained-risk','manual_op_retained','retained-wallet','fixture-token',1,'10','1','committed','2026-10-26T00:00:00Z','2026-10-26T00:00:00Z');`); err != nil {
				t.Fatal(err)
			}
			if conflict == "two_retained_claims" {
				_, err = conn.ExecContext(ctx, `INSERT INTO manual_nonce_claims(operation_id,execution_step_id,attempt_id,wallet_id,wallet_address,nonce,reservation_id,contract_hash,observation_json,observation_hash,checked_at)
 VALUES('manual_op_retained_second','retained-step-second','retained-attempt-second','retained-wallet','0xabcdef1111111111111111111111111111111111','2','retained-risk-second','fixture-contract','{}','fixture-observation','2026-10-26T00:00:00Z')`)
			} else {
				wallet := "retained-wallet"
				if conflict == "normalized_address_active_risk" {
					wallet = "alias-wallet"
				}
				_, err = conn.ExecContext(ctx, `INSERT INTO canary_risk_reservations(id,operation_id,wallet_id,token_address,policy_version,input_amount,gas_budget,state,created_at,updated_at) VALUES('new-risk','manual_op_new',?,'fixture-token',1,'11','1','reserved','2026-10-26T00:00:01Z','2026-10-26T00:00:01Z')`, wallet)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range triggers {
				if _, err := conn.ExecContext(ctx, item.sql); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			read := func(query string) string {
				t.Helper()
				var raw string
				if err := d.db.QueryRow(query).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				return raw
			}
			schemaQuery := `SELECT json_group_array(json_object('name',name,'sql',sql)) FROM (SELECT name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name)`
			historyQuery := `SELECT json_object('claims',(SELECT json_group_array(json_object('operation',operation_id,'wallet',wallet_id,'address',wallet_address,'nonce',nonce)) FROM (SELECT * FROM manual_nonce_claims ORDER BY operation_id)),
 'risk',(SELECT json_group_array(json_object('id',id,'wallet',wallet_id,'amount',input_amount,'state',state)) FROM (SELECT * FROM canary_risk_reservations ORDER BY id)),
 'journal',(SELECT json_group_array(json_object('version',version,'at',applied_at)) FROM (SELECT * FROM schema_migrations ORDER BY version)))`
			schema, history := read(schemaQuery), read(historyQuery)
			if err := d.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "manual_recovery_wallet_reuse_requires_review") {
				t.Fatal("conflicting imported wallet history was not explicitly rejected", err)
			}
			if read(schemaQuery) != schema || read(historyQuery) != history {
				t.Fatal("refused upgrade repaired, released or partially migrated retained work")
			}
		})
	}
}
