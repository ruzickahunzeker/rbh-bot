package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
)

const metadataStamp = "2026-10-10T00:00:00Z"
const metadataToken = "0x2000000000000000000000000000000000000002"

func metadataDB(t *testing.T) *Database {
	t.Helper()
	d, err := Open(context.Background(), TradeOwner, filepath.Join(t.TempDir(), "trade.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err = d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	seedMetadataSource(t, d.db)
	return d
}

func seedMetadataSource(t *testing.T, db *sql.DB) {
	t.Helper()
	// Controlled metadata fixture only, not a C07 production policy grant.
	_, err := db.Exec(`INSERT INTO canary_policies VALUES(1,'PONS_V2_CURVE','100',10000,'200','300','400',100,3600,1,'1000000','100','10',500,'0',?,?)`, strings.Repeat("a", 64), metadataStamp)
	if err != nil {
		t.Fatal(err)
	}
}

func metadataPolicy() manualrisk.Policy {
	return manualrisk.Policy{ID: "manual-risk-1", SourcePolicyVersion: 1, SourcePolicyHash: strings.Repeat("a", 64), ChainID: 4663, Protocol: "PONS_V2_CURVE",
		InputLimits: []manualrisk.InputLimit{
			{Unit: manualrisk.Unit{ChainID: 4663, Asset: metadataToken, Decimals: 6, Kind: manualrisk.TradeInput}, MaxOperation: "100", MaxWalletActive: "200", MaxGlobalActive: "300"},
			{Unit: manualrisk.Unit{ChainID: 4663, Asset: "native", Decimals: 18, Kind: manualrisk.TradeInput}, MaxOperation: "100", MaxWalletActive: "200", MaxGlobalActive: "300"}},
		MaxNativeGasPerOperation: "10", MaxNativeWalletOutlay: "200", MaxNativeGlobalOutlay: "300", MinNativeBalance: "0", MaxSellBPS: 5000,
		MaxWalletOperationsWindow: 10, MaxGlobalOperationsWindow: 100, WindowSeconds: 3600}
}

func insertMetadataPolicy(t *testing.T, db *sql.DB) string {
	t.Helper()
	p := metadataPolicy()
	raw, hash, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO manual_risk_policies VALUES(?,?,?,?,?,?)`, p.ID, p.SourcePolicyVersion, p.SourcePolicyHash, raw, hash, metadataStamp); err != nil {
		t.Fatal(err)
	}
	return hash
}

func insertMetadataAuth(t *testing.T, db *sql.DB, id string, epoch int) {
	t.Helper()
	// Dummy identity, test-only PENDING source. No signer/keys/real grant API.
	_, err := db.Exec(`INSERT INTO canary_runtime_authorizations(id,epoch,mode,environment,chain_id,policy_version,wallet_id,build_sha,release_id,deployment_id,gate_evidence_set_hash,authorization_ref,max_operations,max_total_input,state,authorized_at,expires_at,created_at,updated_at)
 VALUES(?,?,'CONTROLLED_CANARY','controlled-fixture',4663,1,'fixture-wallet',?,'fixture-release','fixture-deployment',?,'fixture-only',10,'100','PENDING',?,'2026-10-10T01:00:00Z',?,?)`, id, epoch, strings.Repeat("1", 40), strings.Repeat("b", 64), metadataStamp, metadataStamp, metadataStamp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO canary_authorization_usage VALUES(?,0,'0',?)`, id, metadataStamp); err != nil {
		t.Fatal(err)
	}
}

func declareMetadataUnit(db *sql.DB, id string, epoch int, hash, asset string, decimals int) error {
	_, err := db.Exec(`INSERT INTO manual_authorization_units VALUES(?,?,'manual-risk-1',?,'MANUAL',4663,?,?,'TRADE_INPUT',?)`, id, epoch, hash, asset, decimals, metadataStamp)
	return err
}

func metadataCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestManualB23ScopeDoesNotConstrainNonManualFirstBroadcast(t *testing.T) {
	d := metadataDB(t)
	insertMetadataAuth(t, d.db, "generic-auth", 1)
	if _, err := d.db.Exec(`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('generic-op',4663,'generic-wallet','generic-key','generic-fingerprint','swap','prepared',?);
INSERT INTO execution_steps(id,operation_id,step_index,kind,status) VALUES('generic-step','generic-op',0,'curve_swap','prepared');
INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('generic-attempt','generic-step','1','prepared',?)`, metadataStamp, metadataStamp); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO canary_send_permits(id,operation_id,attempt_id,authorization_id,authorization_epoch,purpose,artifact_hash,tx_hash,state,issued_at,expires_at) VALUES('generic-first-permit','generic-op','generic-attempt','generic-auth',1,'FIRST_BROADCAST',?,?, 'ISSUED',?,?)`, strings.Repeat("c", 64), "0x"+strings.Repeat("d", 64), metadataStamp, "2026-10-10T01:00:00Z"); err != nil {
		t.Fatalf("non-MANUAL FIRST_BROADCAST was constrained by B2.3: %v", err)
	}
}

func TestManualB1PolicyAndAuthorizationUnitImmutableAudit(t *testing.T) {
	d := metadataDB(t)
	hash := insertMetadataPolicy(t, d.db)
	insertMetadataAuth(t, d.db, "auth-native", 1)
	if err := declareMetadataUnit(d.db, "auth-native", 1, hash, "native", 18); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"manual_risk_policies", "manual_authorization_units"} {
		for _, statement := range []string{`DELETE FROM ` + table, `INSERT OR REPLACE INTO ` + table + ` SELECT * FROM ` + table} {
			if _, err := d.db.Exec(statement); err == nil {
				t.Fatal("mutation allowed", statement)
			}
		}
	}
	for _, q := range []string{
		`UPDATE manual_risk_policies SET policy_hash='` + strings.Repeat("0", 64) + `'`,
		`UPDATE manual_authorization_units SET input_asset='` + metadataToken + `',input_decimals=6`,
		`UPDATE canary_runtime_authorizations SET state='ARMED' WHERE id='auth-native'`,
		`INSERT OR REPLACE INTO canary_runtime_authorizations SELECT * FROM canary_runtime_authorizations WHERE id='auth-native'`,
	} {
		if _, err := d.db.Exec(q); err == nil {
			t.Fatal("metadata bypass accepted", q)
		}
	}
	if metadataCount(t, d.db, "canary_runtime_audit") != 2 || metadataCount(t, d.db, "canary_alert_outbox") != 2 {
		t.Fatal("audit/outbox missing, duplicated or rejected arm audited as success")
	}
	var state string
	if err := d.db.QueryRow(`SELECT state FROM canary_runtime_authorizations WHERE id='auth-native'`).Scan(&state); err != nil || state != "PENDING" {
		t.Fatal("guarded authorization armed", err)
	}
}

func TestManualB1PolicySQLShapeUnitsRangeAndSourceFailClosed(t *testing.T) {
	mutations := []func(map[string]any){
		func(p map[string]any) { p["source_policy_hash"] = strings.Repeat("b", 64) },
		func(p map[string]any) { p["protocol"] = "FLAP" },
		func(p map[string]any) { p["chain_id"] = 8453 },
		func(p map[string]any) { p["max_sell_bps"] = 10001 },
		func(p map[string]any) { p["window_seconds"] = 0 },
		func(p map[string]any) { delete(p, "min_native_balance") },
		func(p map[string]any) { p["unknown"] = "field" },
		func(p map[string]any) { p["max_native_wallet_outlay"] = "9" },
		func(p map[string]any) { p["max_native_global_outlay"] = "01" },
		func(p map[string]any) { p["max_native_global_outlay"] = strings.Repeat("9", 79) },
		func(p map[string]any) {
			p["max_native_global_outlay"] = "115792089237316195423570985008687907853269984665640564039457584007913129639936"
		},
		func(p map[string]any) { p["max_native_global_outlay"] = "1e9" },
		func(p map[string]any) { p["max_native_global_outlay"] = 300 },
		func(p map[string]any) { p["input_limits"] = []any{} },
		func(p map[string]any) { a := p["input_limits"].([]any); p["input_limits"] = []any{a[0], a[0]} },
		func(p map[string]any) { a := p["input_limits"].([]any); p["input_limits"] = []any{a[1], a[0]} },
		func(p map[string]any) { p["input_limits"].([]any)[0].(map[string]any)["max_operation"] = "201" },
		func(p map[string]any) { p["input_limits"].([]any)[0].(map[string]any)["max_operation"] = "0" },
		func(p map[string]any) { p["input_limits"].([]any)[0].(map[string]any)["max_operation"] = "-1" },
		func(p map[string]any) { delete(p["input_limits"].([]any)[0].(map[string]any), "max_operation") },
		func(p map[string]any) {
			p["input_limits"].([]any)[0].(map[string]any)["unit"].(map[string]any)["chain_id"] = 8453
		},
		func(p map[string]any) {
			p["input_limits"].([]any)[0].(map[string]any)["unit"].(map[string]any)["asset"] = "0x" + strings.Repeat("0", 40)
		},
		func(p map[string]any) {
			p["input_limits"].([]any)[0].(map[string]any)["unit"].(map[string]any)["asset"] = "0x" + strings.Repeat("A", 40)
		},
		func(p map[string]any) {
			p["input_limits"].([]any)[0].(map[string]any)["unit"].(map[string]any)["decimals"] = 256
		},
		func(p map[string]any) {
			p["input_limits"].([]any)[1].(map[string]any)["unit"].(map[string]any)["decimals"] = 6
		},
		func(p map[string]any) {
			p["input_limits"].([]any)[0].(map[string]any)["unit"].(map[string]any)["kind"] = "NATIVE_GAS"
		},
	}
	d := metadataDB(t)
	raw, hash, err := metadataPolicy().Canonical()
	if err != nil {
		t.Fatal(err)
	}
	for i, mutate := range mutations {
		var p map[string]any
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatal(err)
		}
		mutate(p)
		bad, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = d.db.Exec(`INSERT INTO manual_risk_policies VALUES('manual-risk-1',1,?,?,?,?)`, strings.Repeat("a", 64), string(bad), hash, metadataStamp); err == nil {
			t.Fatalf("invalid SQL policy accepted: case %d", i)
		}
	}
	if metadataCount(t, d.db, "manual_risk_policies") != 0 || metadataCount(t, d.db, "canary_runtime_audit") != 0 || metadataCount(t, d.db, "canary_alert_outbox") != 0 {
		t.Fatal("rejected SQL policy left partial metadata/audit")
	}
}

func TestManualB1AuthorizationUnitRequiresUnusedPendingAndExactPolicyUnit(t *testing.T) {
	for _, fault := range []string{"armed", "revoked", "wrong_epoch", "wrong_hash", "unknown_asset", "wrong_decimals", "native_decimals", "wrong_chain", "wrong_kind", "wrong_mode", "late_stamp", "nonzero_count", "nonzero_total", "operation_history"} {
		t.Run(fault, func(t *testing.T) {
			d := metadataDB(t)
			hash := insertMetadataPolicy(t, d.db)
			insertMetadataAuth(t, d.db, "auth", 1)
			epoch, asset, decimals, chain, kind, source, stamp := 1, "native", 18, 4663, "TRADE_INPUT", "MANUAL", metadataStamp
			switch fault {
			case "armed", "revoked":
				state := "ARMED"
				if fault == "revoked" {
					state = "REVOKED"
				}
				if _, err := d.db.Exec(`UPDATE canary_runtime_authorizations SET state=? WHERE id='auth'`, state); err != nil {
					t.Fatal(err)
				}
			case "wrong_epoch":
				epoch = 2
			case "wrong_hash":
				hash = strings.Repeat("b", 64)
			case "unknown_asset":
				asset, decimals = "0x3000000000000000000000000000000000000003", 6
			case "wrong_decimals":
				asset, decimals = metadataToken, 18
			case "native_decimals":
				decimals = 6
			case "wrong_chain":
				chain = 8453
			case "wrong_kind":
				kind = "NATIVE_GAS"
			case "wrong_mode":
				source = "COPY"
			case "late_stamp":
				stamp = "2026-10-10T00:01:00Z"
			case "nonzero_count":
				if _, err := d.db.Exec(`UPDATE canary_authorization_usage SET operation_count=1 WHERE authorization_id='auth'`); err != nil {
					t.Fatal(err)
				}
			case "nonzero_total":
				if _, err := d.db.Exec(`UPDATE canary_authorization_usage SET total_input='1' WHERE authorization_id='auth'`); err != nil {
					t.Fatal(err)
				}
			case "operation_history":
				if _, err := d.db.Exec(`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('op',4663,'fixture-wallet','op','fixture','swap','fixture',?)`, metadataStamp); err != nil {
					t.Fatal(err)
				}
				if _, err := d.db.Exec(`INSERT INTO canary_authorization_operation_usage VALUES('auth','op','1','RELEASED',?,?)`, metadataStamp, metadataStamp); err != nil {
					t.Fatal(err)
				}
			}
			before := metadataCount(t, d.db, "canary_runtime_audit")
			_, err := d.db.Exec(`INSERT INTO manual_authorization_units VALUES('auth',?,'manual-risk-1',?,?,?, ?,?,?,?)`, epoch, hash, source, chain, asset, decimals, kind, stamp)
			if err == nil || metadataCount(t, d.db, "manual_authorization_units") != 0 || metadataCount(t, d.db, "canary_runtime_audit") != before {
				t.Fatal("invalid/retroactive declaration accepted", err)
			}
		})
	}
}

func TestManualB1MetadataAuditOutboxFaultRollsBackInsert(t *testing.T) {
	for _, table := range []string{"canary_runtime_audit", "canary_alert_outbox"} {
		for _, phase := range []string{"policy", "unit"} {
			t.Run(table+"_"+phase, func(t *testing.T) {
				d := metadataDB(t)
				var hash string
				if phase == "unit" {
					hash = insertMetadataPolicy(t, d.db)
					insertMetadataAuth(t, d.db, "auth", 1)
				}
				before := metadataCount(t, d.db, "canary_runtime_audit")
				if _, err := d.db.Exec(`CREATE TRIGGER forced_metadata_fault BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'forced'); END`); err != nil {
					t.Fatal(err)
				}
				var err error
				if phase == "policy" {
					p := metadataPolicy()
					raw, digest, e := p.Canonical()
					if e != nil {
						t.Fatal(e)
					}
					_, err = d.db.Exec(`INSERT INTO manual_risk_policies VALUES(?,?,?,?,?,?)`, p.ID, p.SourcePolicyVersion, p.SourcePolicyHash, raw, digest, metadataStamp)
				} else {
					err = declareMetadataUnit(d.db, "auth", 1, hash, "native", 18)
				}
				if err == nil || metadataCount(t, d.db, "canary_runtime_audit") != before || metadataCount(t, d.db, "canary_alert_outbox") != before || metadataCount(t, d.db, "manual_authorization_units") != 0 {
					t.Fatal("metadata/audit/outbox partially committed")
				}
				if phase == "policy" && metadataCount(t, d.db, "manual_risk_policies") != 0 {
					t.Fatal("policy survived failed audit")
				}
			})
		}
	}
}

func TestManualB1DeclaredAuthorizationCannotConsumeBudgetOrGate(t *testing.T) {
	d := metadataDB(t)
	hash := insertMetadataPolicy(t, d.db)
	insertMetadataAuth(t, d.db, "auth", 1)
	if err := declareMetadataUnit(d.db, "auth", 1, hash, metadataToken, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('op',4663,'fixture-wallet','op','fixture','swap','fixture',?)`, metadataStamp); err != nil {
		t.Fatal(err)
	}
	insertMetadataAuth(t, d.db, "legacy-other", 2)
	if _, err := d.db.Exec(`DELETE FROM canary_authorization_usage WHERE authorization_id='legacy-other'`); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO canary_authorization_operation_usage VALUES('auth','op','1','RESERVED','` + metadataStamp + `','` + metadataStamp + `')`,
		`UPDATE canary_authorization_usage SET total_input='1',operation_count=1 WHERE authorization_id='auth'`,
		`DELETE FROM canary_authorization_usage WHERE authorization_id='auth'`,
		`INSERT OR REPLACE INTO canary_authorization_usage VALUES('auth',1,'1','` + metadataStamp + `')`,
		`UPDATE canary_authorization_usage SET authorization_id='legacy-other' WHERE authorization_id='auth'`,
		`INSERT INTO canary_runtime_gate_snapshots(id,operation_id,stage,authorization_id,authorization_epoch,build_sha,release_id,deployment_id,policy_version,policy_hash,gate_evidence_set_hash,emergency_stop_revision,decision,reason_code,checked_at) VALUES('snapshot','op','EXECUTION_ADMISSION','auth',1,'` + strings.Repeat("1", 40) + `','fixture-release','fixture-deployment',1,'` + strings.Repeat("a", 64) + `','` + strings.Repeat("b", 64) + `',0,'PASS','fixture','` + metadataStamp + `')`,
		`INSERT INTO canary_send_permits(id,operation_id,attempt_id,authorization_id,authorization_epoch,purpose,artifact_hash,tx_hash,state,issued_at,expires_at) VALUES('permit','op','fixture-no-artifact','auth',1,'FIRST_BROADCAST','` + strings.Repeat("c", 64) + `','0x` + strings.Repeat("d", 64) + `','ISSUED','` + metadataStamp + `','2026-10-10T01:00:00Z')`,
	} {
		if _, err := d.db.Exec(q); err == nil {
			t.Fatal("B1 isolation bypass accepted", q)
		}
	}
	if metadataCount(t, d.db, "canary_authorization_operation_usage") != 0 || metadataCount(t, d.db, "canary_runtime_gate_snapshots") != 0 || metadataCount(t, d.db, "canary_risk_reservations") != 0 || metadataCount(t, d.db, "canary_send_permits") != 0 || metadataCount(t, d.db, "transaction_attempts") != 0 {
		t.Fatal("B1 metadata granted economic action")
	}
	var n int
	var amount string
	if err := d.db.QueryRow(`SELECT operation_count,total_input FROM canary_authorization_usage WHERE authorization_id='auth'`).Scan(&n, &amount); err != nil || n != 0 || amount != "0" {
		t.Fatal("usage consumed", err)
	}
}

func TestManualB1TwoHandleConcurrentDeclarationsExactlyOnce(t *testing.T) {
	d := metadataDB(t)
	hash := insertMetadataPolicy(t, d.db)
	insertMetadataAuth(t, d.db, "auth", 1)
	var path string
	var sequence int
	var name string
	if err := d.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	other, err := Open(context.Background(), TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var accepted, rejected atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			handle := d.db
			if i%2 != 0 {
				handle = other.db
			}
			if err := declareMetadataUnit(handle, "auth", 1, hash, "native", 18); err == nil {
				accepted.Add(1)
			} else {
				rejected.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if accepted.Load() != 1 || rejected.Load() != 23 || metadataCount(t, d.db, "manual_authorization_units") != 1 || metadataCount(t, d.db, "canary_runtime_audit") != 2 || metadataCount(t, d.db, "canary_alert_outbox") != 2 {
		t.Fatal("concurrent declarations were not exactly once", accepted.Load(), rejected.Load())
	}
}

func TestManualB1CryptographicPolicyVerificationIsRequiredBeforeUse(t *testing.T) {
	d := metadataDB(t)
	p := metadataPolicy()
	raw, _, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	// SQLite has no configured SHA256 function. Shape/FK checks do not assert
	// a cryptographic proof. B2/B3 MUST call the pure canonical/hash loader.
	bad := strings.Repeat("0", 64)
	if _, err = d.db.Exec(`INSERT INTO manual_risk_policies VALUES(?,?,?,?,?,?)`, p.ID, p.SourcePolicyVersion, p.SourcePolicyHash, raw, bad, metadataStamp); err != nil {
		t.Fatal(err)
	}
	if _, err = manualrisk.ParsePolicy(raw, bad, p.SourcePolicyVersion, p.SourcePolicyHash); err == nil {
		t.Fatal("false digest accepted by Go verification")
	}
	insertMetadataAuth(t, d.db, "auth", 1)
	if err = declareMetadataUnit(d.db, "auth", 1, bad, "native", 18); err != nil {
		t.Fatal(err)
	}
	if _, err = d.db.Exec(`UPDATE canary_runtime_authorizations SET state='ARMED' WHERE id='auth'`); err == nil {
		t.Fatal("metadata hash shape became economic permission")
	}
}

func TestManualB1UpgradeFrom017PreservesLegacyAndRestartMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trade.db")
	d, err := Open(context.Background(), TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	entries, err := fs.Glob(migrationFiles, "migrations/trade/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	if _, err = d.db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range entries {
		v, err := migrationVersion(name)
		if err != nil {
			t.Fatal(err)
		}
		if v > 17 {
			continue
		}
		raw, err := migrationFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = d.db.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
		if _, err = d.db.Exec(`INSERT INTO schema_migrations VALUES(?,?)`, v, metadataStamp); err != nil {
			t.Fatal(err)
		}
	}
	seedMetadataSource(t, d.db)
	insertMetadataAuth(t, d.db, "legacy", 1)
	if _, err = d.db.Exec(`UPDATE canary_runtime_authorizations SET state='ARMED' WHERE id='legacy'`); err != nil {
		t.Fatal(err)
	}
	if err = d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = d.db.QueryRow(`SELECT state FROM canary_runtime_authorizations WHERE id='legacy'`).Scan(&state); err != nil || state != "ARMED" || metadataCount(t, d.db, "manual_risk_policies") != 0 || metadataCount(t, d.db, "manual_authorization_units") != 0 {
		t.Fatal("upgrade retroactively declared/changed legacy auth")
	}
	hash := insertMetadataPolicy(t, d.db)
	insertMetadataAuth(t, d.db, "new", 2)
	if err = declareMetadataUnit(d.db, "new", 2, hash, "native", 18); err != nil {
		t.Fatal(err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := Open(context.Background(), TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err = fresh.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var raw, gotHash string
	if err = fresh.db.QueryRow(`SELECT policy_json,policy_hash FROM manual_risk_policies`).Scan(&raw, &gotHash); err != nil {
		t.Fatal(err)
	}
	if _, err = manualrisk.ParsePolicy(raw, gotHash, 1, strings.Repeat("a", 64)); err != nil || gotHash != hash {
		t.Fatal("restart policy identity drift", err)
	}
	if metadataCount(t, fresh.db, "manual_authorization_units") != 1 || metadataCount(t, fresh.db, "canary_runtime_audit") != 3 || metadataCount(t, fresh.db, "canary_alert_outbox") != 2 {
		t.Fatal("restart duplicate metadata/audit")
	}
	if _, err = fresh.db.Exec(`UPDATE canary_runtime_authorizations SET state='ARMED' WHERE id='new'`); err == nil {
		t.Fatal("restart removed isolation guard")
	}
}
