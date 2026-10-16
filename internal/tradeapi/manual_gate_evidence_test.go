package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type gateEvidenceCommand struct{ id, operation, source, digest, checked string }

func gateEvidenceFor(t *testing.T, s *Store, operation, checked string) gateEvidenceCommand {
	t.Helper()
	v := gateEvidenceCommand{operation: operation, checked: checked}
	if err := s.db.QueryRow(`SELECT source_json,manual_artifact_bytes_sha256(hex(source_json)) FROM manual_gate_evidence_expected WHERE operation_id=?`, operation).Scan(&v.source, &v.digest); err != nil {
		t.Fatal(err)
	}
	gateEvidenceID(t, s, &v)
	return v
}

func gateEvidenceID(t *testing.T, s *Store, v *gateEvidenceCommand) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT 'manual-gate-evidence:'||manual_artifact_bytes_sha256(hex(json_object('operation_id',?,'source_hash',?,'checked_at',?)))`, v.operation, v.digest, v.checked).Scan(&v.id); err != nil {
		t.Fatal(err)
	}
}

func insertGateEvidence(db *sql.DB, v gateEvidenceCommand, verb string) error {
	_, err := db.Exec(verb+` INTO manual_submission_gate_evidence VALUES(?,?,?,?,?)`, v.id, v.operation, v.source, v.digest, v.checked)
	return err
}

func gateEvidenceFixture(t *testing.T, side string) (*storage.Database, *Store, string, gateEvidenceCommand) {
	t.Helper()
	d, s, _, _, scope, path, artifact, _ := submissionFoundationFixture(t, side)
	if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
		t.Fatal(err)
	}
	f := submissionFoundationFor(t, s, artifact.operation)
	if err := insertSubmissionFoundation(s.db, f, "INSERT"); err != nil {
		t.Fatal(err)
	}
	stamp, err := canonicalManualTime(f.created)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO canary_worker_leases VALUES('SUBMISSION',?,'evidence-owner',1,?,?)`, scope.Environment, stamp.Add(time.Minute).Format(time.RFC3339Nano), f.created); err != nil {
		t.Fatal(err)
	}
	return d, s, path, gateEvidenceFor(t, s, f.operation, f.created)
}

func assertGateEvidence(t *testing.T, s *Store, v gateEvidenceCommand) {
	t.Helper()
	var id, operation, source, digest, checked string
	if err := s.db.QueryRow(`SELECT * FROM manual_gate_evidence_complete WHERE id=?`, v.id).Scan(&id, &operation, &source, &digest, &checked); err != nil || id != v.id || operation != v.operation || source != v.source || digest != v.digest || checked != v.checked {
		t.Fatal("historical evidence mismatch", err)
	}
	for _, table := range []string{"manual_gate_evidence_scopes", "manual_gate_evidence_empty", "canary_send_permits", "canary_runtime_gate_snapshots", "transaction_submissions", "receipt_observations", "position_effects"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("unexpected economic action/scope", table)
		}
	}
}

func TestHTTPM4B21ExactThreeSidesHistoryAfterControlsAndReopen(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			d, s, path, v := gateEvidenceFixture(t, side)
			before := foundationEconomicSnapshot(t, s)
			audits, alerts := count(t, s.db, "canary_runtime_audit"), count(t, s.db, "canary_alert_outbox")
			if err := insertGateEvidence(s.db, v, "INSERT"); err != nil {
				t.Fatal(err)
			}
			assertGateEvidence(t, s, v)
			if before != foundationEconomicSnapshot(t, s) || count(t, s.db, "canary_runtime_audit") != audits+1 || count(t, s.db, "canary_alert_outbox") != alerts+1 {
				t.Fatal("changed economic identity or witness count")
			}
			var flags int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM json_each(?) WHERE key IN ('execution_authorized','signing_authorized','send_authorized','replay_authorized','trusted_clock_verified','running_deployment_verified','RPC_runtime_verified','cryptographic_artifact_verified') AND type='false'`, v.source).Scan(&flags); err != nil || flags != 8 {
				t.Fatal("metadata claimed permission/proof", flags, err)
			}
			if _, err := s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'; UPDATE canary_risk_reservations SET state='frozen'; UPDATE canary_authorization_operation_usage SET state='FROZEN'; DELETE FROM canary_worker_leases`); err != nil {
				t.Fatal(err)
			}
			_, s = bindingReopen(t, d, path)
			beforeHistory := admittedDatabaseSnapshot(t, s.db)
			assertGateEvidence(t, s, v)
			if beforeHistory != admittedDatabaseSnapshot(t, s.db) || count(t, s.db, "manual_gate_evidence_expected") != 0 {
				t.Fatal("old observation used as current eligibility")
			}
			if _, err := s.db.Exec(`UPDATE canary_alert_outbox SET state='DELIVERED',delivered_at=created_at WHERE id=?`, "manual-gate-alert:"+v.id); err != nil {
				t.Fatal(err)
			}
			assertGateEvidence(t, s, v)
		})
	}
}

func TestHTTPM4B21SourceOverridesTypesAndTimeFailClosed(t *testing.T) {
	_, s, _, v := gateEvidenceFixture(t, "buy")
	before := admittedDatabaseSnapshot(t, s.db)
	for _, path := range []string{"$.foundation.facts.nonce", "$.foundation.facts.expires_at", "$.foundation.contract.principal_id", "$.foundation_hash", "$.control.emergency_stopped", "$.control.updated_at", "$.authorization.epoch", "$.authorization.deployment_id", "$.policy.manual_policy_hash", "$.allowlists.runtime_code_hash", "$.reservation_state", "$.operation_usage_state", "$.aggregate_usage.total_input", "$.submission_lease.role", "$.submission_lease.environment", "$.submission_lease.holder_id", "$.submission_lease.epoch", "$.submission_lease.expires_at", "$.send_authorized", "$.cryptographic_artifact_verified"} {
		bad := v
		if err := s.db.QueryRow(`SELECT json_set(?,?,'override')`, v.source, path).Scan(&bad.source); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow(`SELECT manual_artifact_bytes_sha256(hex(?))`, bad.source).Scan(&bad.digest); err != nil {
			t.Fatal(err)
		}
		gateEvidenceID(t, s, &bad)
		for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE", "INSERT OR FAIL"} {
			if err := insertGateEvidence(s.db, bad, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("override accepted or wrote", path, verb, err)
			}
		}
	}
	for _, kind := range []string{"id", "operation", "source", "hash", "checked", "nul_time", "noncanonical_time", "reversed_time"} {
		var id, operation, source, digest, checked any = v.id, v.operation, v.source, v.digest, v.checked
		switch kind {
		case "id":
			id = []byte(v.id)
		case "operation":
			operation = []byte(v.operation)
		case "source":
			source = []byte(v.source)
		case "hash":
			digest = []byte(v.digest)
		case "checked":
			checked = []byte(v.checked)
		case "nul_time":
			checked = v.checked + "\x00"
		case "noncanonical_time":
			checked = strings.Replace(v.checked, "Z", "+00:00", 1)
		case "reversed_time":
			tm, _ := canonicalManualTime(v.checked)
			checked = tm.Add(-time.Nanosecond).Format(time.RFC3339Nano)
		}
		_, err := s.db.Exec(`INSERT OR IGNORE INTO manual_submission_gate_evidence VALUES(?,?,?,?,?)`, id, operation, source, digest, checked)
		if err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("encoded bytes/type/time accepted", kind, err)
		}
	}
}

func TestHTTPM4B21ExpiryAndFractionalLeaseBoundaries(t *testing.T) {
	for _, mode := range []string{"lease_whole", "lease_fractional", "operation", "authorization"} {
		t.Run(mode, func(t *testing.T) {
			_, s, _, v := gateEvidenceFixture(t, "buy")
			start, _ := canonicalManualTime(v.checked)
			expiry := start.Add(10 * time.Second)
			if mode == "lease_fractional" {
				expiry = expiry.Add(200 * time.Nanosecond)
			}
			switch mode {
			case "lease_whole", "lease_fractional":
				if _, err := s.db.Exec(`UPDATE canary_worker_leases SET expires_at=?`, expiry.Format(time.RFC3339Nano)); err != nil {
					t.Fatal(err)
				}
			case "operation":
				var raw string
				if err := s.db.QueryRow(`SELECT expires_at FROM manual_execution_bindings`).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				expiry, _ = canonicalManualTime(raw)
			case "authorization":
				seedSigningWitnessCorruption(t, s, "canary_runtime_authorizations", `UPDATE canary_runtime_authorizations SET expires_at=?`, expiry.Format(time.RFC3339Nano))
			}
			for _, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
				candidate := gateEvidenceFor(t, s, v.operation, expiry.Add(offset).Format(time.RFC3339Nano))
				before := admittedDatabaseSnapshot(t, s.db)
				err := insertGateEvidence(s.db, candidate, "INSERT")
				if offset < 0 {
					if err != nil {
						t.Fatal("pre-expiry evidence denied", mode, err)
					}
					assertGateEvidence(t, s, candidate)
				} else if err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("expired boundary accepted", mode, offset, err)
				}
			}
		})
	}
}

func TestHTTPM4B21DurableDriftPriorityAndIndependentLease(t *testing.T) {
	for _, mode := range []string{"stop_and_bad_identity", "disabled", "missing_control", "revoke", "lease_missing", "lease_role", "lease_blob_alias", "lease_holder_blob", "lease_time", "lease_time_blob", "policy", "wallet", "contract", "token", "gate", "risk_frozen", "usage_frozen", "usage_corrupt"} {
		t.Run(mode, func(t *testing.T) {
			_, s, _, v := gateEvidenceFixture(t, "buy")
			var err error
			switch mode {
			case "stop_and_bad_identity":
				_, err = s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`)
				v.operation, v.source = "outside", "{"
			case "disabled":
				_, err = s.db.Exec(`UPDATE canary_control_state SET mode='DISABLED'`)
			case "missing_control":
				_, err = s.db.Exec(`DELETE FROM canary_control_state`)
			case "revoke":
				_, err = s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`)
			case "lease_missing":
				_, err = s.db.Exec(`DELETE FROM canary_worker_leases`)
			case "lease_role":
				_, err = s.db.Exec(`UPDATE canary_worker_leases SET role='RECOVERY'`)
			case "lease_blob_alias":
				_, err = s.db.Exec(`INSERT INTO canary_worker_leases SELECT role,CAST(environment AS BLOB),'shadow',2,expires_at,updated_at FROM canary_worker_leases`)
			case "lease_holder_blob":
				seedSigningWitnessCorruption(t, s, "canary_worker_leases", `UPDATE canary_worker_leases SET holder_id=CAST(holder_id AS BLOB)`)
			case "lease_time", "lease_time_blob":
				var value any = "invalid"
				if mode == "lease_time_blob" {
					value = []byte(v.checked)
				}
				_, err = s.db.Exec(`UPDATE canary_worker_leases SET expires_at=?`, value)
			case "policy":
				_, err = s.db.Exec(`UPDATE canary_control_state SET policy_version=2`)
			case "wallet":
				seedSigningWitnessCorruption(t, s, "canary_wallet_allowlist", `UPDATE canary_wallet_allowlist SET enabled=0`)
			case "contract":
				seedSigningWitnessCorruption(t, s, "canary_contract_allowlist", `UPDATE canary_contract_allowlist SET runtime_code_hash='0x'||printf('%064d',0)`)
			case "token":
				seedSigningWitnessCorruption(t, s, "canary_token_allowlist", `UPDATE canary_token_allowlist SET enabled=0`)
			case "gate":
				seedSigningWitnessCorruption(t, s, "canary_gate_attestations", `DELETE FROM canary_gate_attestations WHERE gate='C05'`)
			case "risk_frozen":
				_, err = s.db.Exec(`UPDATE canary_risk_reservations SET state='frozen'`)
			case "usage_frozen":
				_, err = s.db.Exec(`UPDATE canary_authorization_operation_usage SET state='FROZEN'`)
			case "usage_corrupt":
				seedSigningWitnessCorruption(t, s, "canary_authorization_usage", `UPDATE canary_authorization_usage SET operation_count=0`)
			}
			if err != nil {
				t.Fatal("invalid fixture", mode, err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			if err := insertGateEvidence(s.db, v, "INSERT"); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("durable drift accepted/mutated", mode, err)
			} else if mode == "stop_and_bad_identity" && !strings.Contains(err.Error(), "control unavailable disabled or stopped") {
				t.Fatal("stop lost first priority", err)
			}
		})
	}
	_, s, _, v := gateEvidenceFixture(t, "buy")
	if _, err := s.db.Exec(`INSERT INTO canary_worker_leases SELECT 'RECOVERY',environment,'independent',99,expires_at,updated_at FROM canary_worker_leases`); err != nil {
		t.Fatal(err)
	}
	if err := insertGateEvidence(s.db, v, "INSERT"); err != nil {
		t.Fatal("RECOVERY lease interfered with SUBMISSION", err)
	}
}

func TestHTTPM4B21StaleSourceStopRevokeAndTakeover(t *testing.T) {
	for _, mode := range []string{"stop", "revoke", "takeover"} {
		t.Run(mode, func(t *testing.T) {
			_, s, path, v := gateEvidenceFixture(t, "buy")
			db2, err := storage.Open(context.Background(), storage.TradeOwner, path)
			if err != nil {
				t.Fatal(err)
			}
			defer db2.Close()
			s2, err := NewStore(db2)
			if err != nil {
				t.Fatal(err)
			}
			query := `UPDATE canary_control_state SET emergency_stopped=1`
			if mode == "revoke" {
				query = `UPDATE canary_runtime_authorizations SET state='REVOKED'`
			} else if mode == "takeover" {
				query = `UPDATE canary_worker_leases SET holder_id='takeover',lease_epoch=lease_epoch+1`
			}
			if _, err := s2.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			if err := insertGateEvidence(s.db, v, "INSERT"); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("stale captured DB source accepted", mode, err)
			}
		})
	}
}

func TestHTTPM4B21WitnessFaultsAndUncommittableInterruptedScope(t *testing.T) {
	_, s, _, v := gateEvidenceFixture(t, "buy")
	for _, table := range []string{"canary_runtime_audit", "canary_alert_outbox"} {
		for _, action := range []string{"ABORT,'forced'", "IGNORE", "FAIL,'forced'"} {
			field, match := "event_type", "MANUAL_SUBMISSION_GATE_EVIDENCE"
			if table == "canary_alert_outbox" {
				field, match = "audit_id", "manual-gate-audit:"+v.id
			}
			if _, err := s.db.Exec(`CREATE TRIGGER gate_fault BEFORE INSERT ON ` + table + ` WHEN NEW.` + field + `='` + match + `' BEGIN SELECT RAISE(` + action + `); END`); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
				if err := insertGateEvidence(s.db, v, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("fault left committed effects", table, action, verb, err)
				}
			}
			if strings.HasPrefix(action, "FAIL") {
				conn, err := s.db.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
					t.Fatal(err)
				}
				_, insertErr := conn.ExecContext(context.Background(), `INSERT INTO manual_submission_gate_evidence VALUES(?,?,?,?,?)`, v.id, v.operation, v.source, v.digest, v.checked)
				if insertErr == nil {
					t.Fatal("expected injected statement FAIL")
				}
				var scopes, parents int
				if err := conn.QueryRowContext(context.Background(), `SELECT (SELECT COUNT(*) FROM manual_gate_evidence_scopes),(SELECT COUNT(*) FROM manual_submission_gate_evidence)`).Scan(&scopes, &parents); err != nil || scopes != 1 || parents != 0 {
					t.Fatal("witness fault must precede parent insertion", scopes, parents, err)
				}
				_, err = conn.ExecContext(context.Background(), `COMMIT`)
				var coded interface{ Code() int }
				if err == nil || !errors.As(err, &coded) || coded.Code() != 787 {
					t.Fatal("incomplete scope COMMIT must fail FK", err)
				}
				if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
					t.Fatal(err)
				}
				if err := conn.Close(); err != nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("rollback did not restore all witnesses", err)
				}
			}
			if _, err := s.db.Exec(`DROP TRIGGER gate_fault`); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := insertGateEvidence(s.db, v, "INSERT"); err != nil {
		t.Fatal(err)
	}
	assertGateEvidence(t, s, v)
}

func TestHTTPM4B21DuplicateNamespaceVictimsAndConcurrentHandles(t *testing.T) {
	_, s, path, v := gateEvidenceFixture(t, "buy")
	d, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s2, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			db := s.db
			if i%2 != 0 {
				db = s2.db
			}
			results <- insertGateEvidence(db, v, "INSERT")
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !strings.Contains(err.Error(), "manual gate") && !strings.Contains(strings.ToLower(err.Error()), "busy") && !strings.Contains(strings.ToLower(err.Error()), "locked") {
			t.Fatal("unexplained contender result", err)
		}
	}
	if accepted != 1 {
		t.Fatal("duplicate observation accepted", accepted)
	}
	assertGateEvidence(t, s, v)
	before := admittedDatabaseSnapshot(t, s.db)
	for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE", "INSERT OR FAIL"} {
		if err := insertGateEvidence(s.db, v, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("duplicate reissued", verb, err)
		}
	}
	for _, query := range []string{
		`UPDATE OR REPLACE manual_submission_gate_evidence SET id=id`,
		`DELETE FROM manual_submission_gate_evidence`,
		`INSERT INTO manual_gate_evidence_empty VALUES(1)`,
		`INSERT OR REPLACE INTO manual_submission_gate_evidence(rowid,id,operation_id,source_json,source_hash,checked_at) SELECT rowid,'outside',operation_id,source_json,source_hash,checked_at FROM manual_submission_gate_evidence`,
		`INSERT OR REPLACE INTO canary_runtime_audit(rowid,id,event_type,reason_code,details_json,created_at) SELECT rowid,'outside','outside','outside','{}',created_at FROM canary_runtime_audit WHERE event_type='MANUAL_SUBMISSION_GATE_EVIDENCE'`,
		`DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-gate-alert:%'`,
		`UPDATE canary_alert_outbox SET severity='CRITICAL' WHERE id LIKE 'manual-gate-alert:%'`,
		`INSERT INTO manual_gate_evidence_scopes(id,operation_id,source_hash,checked_at) SELECT id,operation_id,source_hash,checked_at FROM manual_submission_gate_evidence`,
	} {
		if _, err := s.db.Exec(query); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("identity/victim/scope bypass", query, err)
		}
	}
}

func TestHTTPM4B21RetainedWitnessMissingOrBlobNoRepair(t *testing.T) {
	for _, kind := range []string{"audit_missing", "audit_details_blob", "alert_missing"} {
		t.Run(kind, func(t *testing.T) {
			d, s, path, v := gateEvidenceFixture(t, "buy")
			if err := insertGateEvidence(s.db, v, "INSERT"); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "audit_missing":
				seedSigningWitnessCorruption(t, s, "canary_runtime_audit", `DELETE FROM canary_runtime_audit WHERE event_type='MANUAL_SUBMISSION_GATE_EVIDENCE'`)
			case "audit_details_blob":
				seedSigningWitnessCorruption(t, s, "canary_runtime_audit", `UPDATE canary_runtime_audit SET details_json=CAST(details_json AS BLOB) WHERE event_type='MANUAL_SUBMISSION_GATE_EVIDENCE'`)
			case "alert_missing":
				seedSigningWitnessCorruption(t, s, "canary_alert_outbox", `DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-gate-alert:%'`)
			}
			_, s = bindingReopen(t, d, path)
			if count(t, s.db, "manual_gate_evidence_complete") != 0 {
				t.Fatal("corrupt evidence complete")
			}
			before := admittedDatabaseSnapshot(t, s.db)
			if err := insertGateEvidence(s.db, v, "INSERT OR REPLACE"); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("retained history recreated", err)
			}
			_, err := s.db.Exec(`INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'manual-gate-audit:'||e.id,'MANUAL_SUBMISSION_GATE_EVIDENCE',e.operation_id,q.attempt_id,b.authorization_id,b.authorization_epoch,
 'DB_OBSERVATION_NOT_GATE_OR_SEND_PERMISSION',json_object('evidence_id',e.id,'source_hash',e.source_hash),e.checked_at
 FROM manual_submission_gate_evidence e JOIN manual_nonce_claims q ON q.operation_id=e.operation_id JOIN manual_execution_bindings b ON b.operation_id=e.operation_id`)
			if err == nil || !strings.Contains(err.Error(), "initial statement only") || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("exact witness repair permitted", err)
			}
		})
	}
}

func TestHTTPM4B21NoGuardOpeningAndNoRuntimeCaller(t *testing.T) {
	raw, err := os.ReadFile("../storage/migrations/trade/038_manual_submission_gate_evidence.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, denied := range []string{"DROP ", "ALTER ", "INSERT INTO CANARY_SEND_PERMITS", "INSERT INTO CANARY_RUNTIME_GATE_SNAPSHOTS", "INSERT INTO TRANSACTION_SUBMISSIONS", "UPDATE TRANSACTION_", "UPDATE EXECUTION_", "UPDATE CANARY_RUNTIME_AUTHORIZATIONS"} {
		if strings.Contains(strings.ToUpper(string(raw)), denied) {
			t.Fatal("B2.1 opened economic/schema path", denied)
		}
	}
	for _, directory := range []string{"cmd", "internal"} {
		if err := filepath.WalkDir(filepath.Join("..", "..", directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, name := range []string{"manual_submission_gate_evidence", "manual_gate_evidence_scopes", "manual_gate_evidence_complete"} {
				if strings.Contains(string(raw), name) {
					t.Errorf("gate evidence wired into product %s", path)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, s, _, v := gateEvidenceFixture(t, "buy")
	if err := insertGateEvidence(s.db, v, "INSERT"); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	for _, name := range []string{"manual_b1_no_arm", "manual_b1_no_permit", "manual_b1_no_pass_snapshot", "manual_nonce_no_submission_insert", "manual_nonce_no_receipt_insert", "manual_nonce_no_effect_insert", "manual_nonce_risk_no_release", "manual_nonce_usage_no_release"} {
		var guards int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&guards); err != nil || guards != 1 {
			t.Fatal("missing old guard", name, err)
		}
	}
	if _, err := s.db.Exec(`UPDATE canary_runtime_authorizations SET state='ARMED'`); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("evidence opened no-arm", err)
	}
	for _, query := range []string{
		`INSERT INTO canary_send_permits(id,operation_id,attempt_id,authorization_id,authorization_epoch,purpose,artifact_hash,tx_hash,state,issued_at,expires_at)
 SELECT 'denied',b.operation_id,q.attempt_id,b.authorization_id,b.authorization_epoch,'FIRST_BROADCAST',json_extract(f.binding_json,'$.raw_sha256'),json_extract(f.binding_json,'$.tx_hash'),'ISSUED',e.checked_at,b.expires_at
 FROM manual_submission_gate_evidence e JOIN manual_submission_foundations f ON f.operation_id=e.operation_id JOIN manual_execution_bindings b ON b.operation_id=e.operation_id JOIN manual_nonce_claims q ON q.operation_id=e.operation_id`,
		`INSERT INTO canary_runtime_gate_snapshots(id,operation_id,attempt_id,stage,authorization_id,authorization_epoch,build_sha,release_id,deployment_id,policy_version,policy_hash,gate_evidence_set_hash,emergency_stop_revision,decision,reason_code,checked_at)
 SELECT 'denied',b.operation_id,q.attempt_id,'FIRST_BROADCAST',b.authorization_id,b.authorization_epoch,b.build_sha,b.release_id,b.deployment_id,b.policy_version,json_extract(f.binding_json,'$.facts.policy_hash'),b.gate_evidence_set_hash,0,'PASS','denied',e.checked_at
 FROM manual_submission_gate_evidence e JOIN manual_submission_foundations f ON f.operation_id=e.operation_id JOIN manual_execution_bindings b ON b.operation_id=e.operation_id JOIN manual_nonce_claims q ON q.operation_id=e.operation_id`,
	} {
		if _, err := s.db.Exec(query); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("evidence opened permit/PASS", err)
		}
	}
}

func TestHTTPM4B21OrphanScopePreseedAndFreshMetadataNoWindow(t *testing.T) {
	_, s, _, v := gateEvidenceFixture(t, "buy")
	before := admittedDatabaseSnapshot(t, s.db)
	if _, err := s.db.Exec(`INSERT INTO manual_gate_evidence_scopes(id,operation_id,source_hash,checked_at) VALUES(?,?,?,?)`, v.id, v.operation, v.digest, v.checked); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("orphan scope committed", err)
	}
	for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
		_, err := s.db.Exec(verb+` INTO canary_runtime_audit(id,event_type,operation_id,reason_code,details_json,created_at) VALUES(?,'MANUAL_SUBMISSION_GATE_EVIDENCE',?,'preseed','{}',?)`, "manual-gate-audit:"+v.id, v.operation, v.checked)
		if err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("audit preseed accepted", verb, err)
		}
	}
	if err := insertGateEvidence(s.db, v, "INSERT"); err != nil {
		t.Fatal(err)
	}
	start, _ := canonicalManualTime(v.checked)
	fresh := gateEvidenceFor(t, s, v.operation, start.Add(time.Nanosecond).Format(time.RFC3339Nano))
	economic := foundationEconomicSnapshot(t, s)
	if fresh.id == v.id {
		t.Fatal("separate exact observation unavailable")
	}
	if err := insertGateEvidence(s.db, fresh, "INSERT"); err != nil {
		t.Fatal(err)
	}
	if economic != foundationEconomicSnapshot(t, s) || count(t, s.db, "manual_submission_gate_evidence") != 2 {
		t.Fatal("fresh metadata created economic/expiry window")
	}
	assertGateEvidence(t, s, fresh)
}

// Promote the independent BLOCKED-head probe: even after a once-only FAIL clears,
// later SQL may not adopt a failed scope and turn it into committed evidence.
// Completing provisional witnesses is not itself permission or successful repair.
func TestHTTPM4B21InterruptedWitnessCannotAdoptOrCommit(t *testing.T) {
	for _, scenario := range []struct{ table, verb string }{
		{"canary_runtime_audit", "INSERT"}, {"canary_runtime_audit", "INSERT OR IGNORE"},
		{"canary_runtime_audit", "INSERT OR REPLACE"}, {"canary_runtime_audit", "INSERT OR FAIL"},
		{"canary_alert_outbox", "INSERT"}, {"canary_alert_outbox", "INSERT OR IGNORE"},
		{"canary_alert_outbox", "INSERT OR REPLACE"}, {"canary_alert_outbox", "INSERT OR FAIL"},
	} {
		table := scenario.table
		for _, drift := range []bool{false, true} {
			name := table + "/" + scenario.verb + "/original"
			if drift {
				name = table + "/" + scenario.verb + "/stop_revoke_freeze_lease_deleted"
			}
			t.Run(name, func(t *testing.T) {
				_, s, _, v := gateEvidenceFixture(t, "buy")
				field, match := "event_type", "MANUAL_SUBMISSION_GATE_EVIDENCE"
				if table == "canary_alert_outbox" {
					field, match = "audit_id", "manual-gate-audit:"+v.id
				}
				if _, err := s.db.Exec(`CREATE TRIGGER gate_once_fault BEFORE INSERT ON ` + table + ` WHEN NEW.` + field + `='` + match + `' AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit WHERE id='gate-once-cleared') BEGIN SELECT RAISE(FAIL,'once-only witness fault'); END`); err != nil {
					t.Fatal(err)
				}
				before := admittedDatabaseSnapshot(t, s.db)
				economic := foundationEconomicSnapshot(t, s)
				conn, err := s.db.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				exec := func(query string, args ...any) {
					t.Helper()
					if _, err := conn.ExecContext(context.Background(), query, args...); err != nil {
						t.Fatal(query, err)
					}
				}
				exec(`BEGIN IMMEDIATE`)
				defer conn.ExecContext(context.Background(), `ROLLBACK`)
				if _, err := conn.ExecContext(context.Background(), scenario.verb+` INTO manual_submission_gate_evidence VALUES(?,?,?,?,?)`, v.id, v.operation, v.source, v.digest, v.checked); err == nil || !strings.Contains(err.Error(), "once-only witness fault") {
					t.Fatal("fault must interrupt witness staging", err)
				}
				var parents, scopes int
				if err := conn.QueryRowContext(context.Background(), `SELECT (SELECT COUNT(*) FROM manual_submission_gate_evidence),(SELECT COUNT(*) FROM manual_gate_evidence_scopes)`).Scan(&parents, &scopes); err != nil || parents != 0 || scopes != 1 {
					t.Fatal("failed witness created parent", parents, scopes, err)
				}
				if drift {
					exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'; UPDATE canary_risk_reservations SET state='frozen'; UPDATE canary_authorization_operation_usage SET state='FROZEN'; DELETE FROM canary_worker_leases`)
				}
				// No DDL after the error: clear the fault using unrelated ordinary SQL.
				exec(`INSERT INTO canary_runtime_audit(id,event_type,reason_code,details_json,created_at) VALUES('gate-once-cleared','TEST_ONCE_FAULT_CLEARED','test fault cleared','{}',?)`, v.checked)
				if table == "canary_runtime_audit" {
					exec(`INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'manual-gate-audit:'||s.id,'MANUAL_SUBMISSION_GATE_EVIDENCE',s.operation_id,q.attempt_id,b.authorization_id,b.authorization_epoch,
 'DB_OBSERVATION_NOT_GATE_OR_SEND_PERMISSION',json_object('evidence_id',s.id,'source_hash',s.source_hash),s.checked_at
 FROM manual_gate_evidence_scopes s JOIN manual_nonce_claims q ON q.operation_id=s.operation_id JOIN manual_execution_bindings b ON b.operation_id=s.operation_id`)
				}
				exec(`INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) SELECT 'manual-gate-alert:'||id,'manual-gate-audit:'||id,'INFO','PENDING',checked_at FROM manual_gate_evidence_scopes`)
				for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE", "INSERT OR FAIL"} {
					if _, err := conn.ExecContext(context.Background(), verb+` INTO manual_submission_gate_evidence VALUES(?,?,?,?,?)`, v.id, v.operation, v.source, v.digest, v.checked); err == nil {
						t.Fatal("later statement adopted failed scope", verb)
					}
				}
				if _, err := conn.ExecContext(context.Background(), `DELETE FROM manual_gate_evidence_scopes`); err == nil {
					t.Fatal("witness repair closed scope without parent")
				}
				var complete int
				if err := conn.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM manual_gate_evidence_complete`).Scan(&complete); err != nil || complete != 0 {
					t.Fatal("repaired incomplete evidence accepted", complete, err)
				}
				_, err = conn.ExecContext(context.Background(), `COMMIT`)
				var coded interface{ Code() int }
				if err == nil || !errors.As(err, &coded) || coded.Code() != 787 {
					t.Fatal("later witness repair made COMMIT possible", err)
				}
				exec(`ROLLBACK`)
				if err := conn.Close(); err != nil {
					t.Fatal(err)
				}
				if before != admittedDatabaseSnapshot(t, s.db) || economic != foundationEconomicSnapshot(t, s) {
					t.Fatal("rollback retained witness/control/economic mutations")
				}
				if _, err := s.db.Exec(`DROP TRIGGER gate_once_fault`); err != nil {
					t.Fatal(err)
				}
				if err := insertGateEvidence(s.db, v, "INSERT"); err != nil {
					t.Fatal("fresh statement after rollback rejected", err)
				}
				assertGateEvidence(t, s, v)
			})
		}
	}
}
