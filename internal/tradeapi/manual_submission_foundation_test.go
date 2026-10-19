package tradeapi

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type submissionFoundationCommand struct{ operation, binding, hash, created string }

// The reused offline setup intentionally seeds a synthetic ARMED grant by
// removing no-arm. Restore its exact original SQL BEFORE every B1 test action;
// this is not a claim that an intact production schema can create that grant.
func submissionFoundationFixture(t *testing.T, side string) (*storage.Database, *Store, *API, Record, manualAdmissionScope, string, artifactPhaseCommand, manualartifact.Cipher) {
	t.Helper()
	d, s, h, r, scope, path, artifact, cipher := artifactPhaseFixture(t, side)
	raw, err := os.ReadFile("../storage/migrations/trade/018_manual_risk_units.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(raw), "CREATE TRIGGER manual_b1_no_arm ")
	if start < 0 {
		t.Fatal("missing original no-arm guard")
	}
	definition := string(raw)[start:]
	end := strings.Index(definition, "END;")
	if end < 0 {
		t.Fatal("missing original no-arm body")
	}
	definition = definition[:end+4]
	if _, err := s.db.Exec(definition); err != nil {
		t.Fatal(err)
	}
	var restored string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='manual_b1_no_arm'`).Scan(&restored); err != nil || restored+";" != definition {
		t.Fatal("no-arm was not exactly restored", err)
	}
	return d, s, h, r, scope, path, artifact, cipher
}

func submissionFoundationFor(t *testing.T, s *Store, operation string) submissionFoundationCommand {
	t.Helper()
	v := submissionFoundationCommand{operation: operation}
	if err := s.db.QueryRow(`SELECT binding_json,manual_artifact_bytes_sha256(hex(binding_json)),artifact_completed_at FROM manual_submission_foundation_expected WHERE operation_id=?`, operation).Scan(&v.binding, &v.hash, &v.created); err != nil {
		t.Fatal(err)
	}
	return v
}

func insertSubmissionFoundation(db *sql.DB, v submissionFoundationCommand, verb string) error {
	_, err := db.Exec(verb+` INTO manual_submission_foundations(operation_id,binding_json,binding_hash,created_at) VALUES(?,?,?,?)`, v.operation, v.binding, v.hash, v.created)
	return err
}

func foundationEconomicSnapshot(t *testing.T, s *Store) string {
	t.Helper()
	var all []string
	for _, table := range []string{"operations", "execution_steps", "transaction_attempts", "transaction_submissions", "receipt_observations", "position_effects", "execution_wallet_lanes", "execution_reservations", "canary_runtime_authorizations", "canary_authorization_usage", "canary_authorization_operation_usage", "canary_risk_reservations", "manual_reservation_units", "manual_signed_artifact_commands", "canary_send_permits", "canary_runtime_gate_snapshots"} {
		rows, err := s.db.Query(`SELECT * FROM ` + table + ` ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			v, ptr := make([]any, len(cols)), make([]any, len(cols))
			for i := range v {
				ptr[i] = &v[i]
			}
			if err := rows.Scan(ptr...); err != nil {
				t.Fatal(err)
			}
			all = append(all, table+":"+encode(v))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return encode(all)
}

func assertSubmissionFoundation(t *testing.T, s *Store, v submissionFoundationCommand) {
	t.Helper()
	var binding, digest, created string
	if err := s.db.QueryRow(`SELECT binding_json,binding_hash,created_at FROM manual_submission_foundation_complete WHERE operation_id=?`, v.operation).Scan(&binding, &digest, &created); err != nil || binding != v.binding || digest != v.hash || created != v.created {
		t.Fatal("incomplete structural metadata", err)
	}
	if count(t, s.db, "manual_submission_foundation_scopes") != 0 {
		t.Fatal("retained witness scope")
	}
	for _, table := range []string{"transaction_submissions", "receipt_observations", "position_effects", "canary_send_permits", "canary_runtime_gate_snapshots"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("foundation created economic phase", table)
		}
	}
}

func TestHTTPM4B1ExactThreeSidesReopenNoEconomicMutation(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			d, s, h, r, scope, path, artifact, cipher := submissionFoundationFixture(t, side)
			if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
				t.Fatal(err)
			}
			v := submissionFoundationFor(t, s, artifact.operation)
			before := foundationEconomicSnapshot(t, s)
			audits, alerts := count(t, s.db, "canary_runtime_audit"), count(t, s.db, "canary_alert_outbox")
			if err := insertSubmissionFoundation(s.db, v, "INSERT"); err != nil {
				t.Fatal(err)
			}
			assertSubmissionFoundation(t, s, v)
			if before != foundationEconomicSnapshot(t, s) || count(t, s.db, "canary_runtime_audit") != audits+1 || count(t, s.db, "canary_alert_outbox") != alerts+1 {
				t.Fatal("economic mutation or duplicate witnesses")
			}
			got, err := handoffReader(s, cipher).load(context.Background(), h.identity, r.RequestID, scope)
			if err != nil {
				t.Fatal("M4A compatibility", err)
			}
			assertHandoffMetadata(t, got, r, scope)
			_, s = bindingReopen(t, d, path)
			assertSubmissionFoundation(t, s, v)
			if before != foundationEconomicSnapshot(t, s) {
				t.Fatal("reopen changed original accounting/artifact")
			}
			var projection map[string]any
			if err := json.Unmarshal([]byte(v.binding), &projection); err != nil || projection["status"] != "FOUNDATION_METADATA_NOT_PERMISSION" {
				t.Fatal("invalid metadata", err)
			}
			for _, field := range []string{"execution_authorized", "signing_authorized", "send_authorized", "replay_authorized"} {
				if projection[field] != false {
					t.Fatal("metadata became permission", field)
				}
			}
		})
	}
}

func TestHTTPM4B1ExactProjectionRejectsCallerOverrides(t *testing.T) {
	_, s, _, _, _, _, artifact, _ := submissionFoundationFixture(t, "buy")
	if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
		t.Fatal(err)
	}
	v := submissionFoundationFor(t, s, artifact.operation)
	before := admittedDatabaseSnapshot(t, s.db)
	paths := []string{"$.source", "$.format", "$.status", "$.facts.operation_id", "$.facts.request_id", "$.facts.execution_step_id", "$.facts.attempt_id", "$.facts.wallet_id", "$.facts.reservation_id", "$.facts.chain_id", "$.facts.nonce", "$.facts.data", "$.facts.gas_tip_cap", "$.facts.policy_version", "$.facts.quote_block_hash", "$.facts.expires_at", "$.facts.authorization_epoch", "$.facts.environment", "$.facts.build_sha", "$.facts.release_id", "$.facts.deployment_id", "$.facts_hash", "$.envelope_hash", "$.tx_hash", "$.raw_sha256", "$.key_version", "$.contract.principal_id", "$.contract.input.units", "$.reservation.id", "$.reservation.unit_asset", "$.reservation.unit_decimals", "$.usage.amount", "$.send_authorized"}
	for _, path := range paths {
		for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
			bad := v
			if err := s.db.QueryRow(`SELECT json_set(?,?,'override')`, v.binding, path).Scan(&bad.binding); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT manual_artifact_bytes_sha256(hex(?))`, bad.binding).Scan(&bad.hash); err != nil {
				t.Fatal(err)
			}
			if err := insertSubmissionFoundation(s.db, bad, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("caller override accepted/ignored or wrote", path, verb, err)
			}
		}
	}
	for _, field := range []string{"operation", "json", "hash", "created", "created_before_artifact", "created_nul", "json_blob", "operation_blob", "hash_blob", "time_blob"} {
		var op, binding, digest, created any = v.operation, v.binding, v.hash, v.created
		switch field {
		case "operation":
			op = "outside"
		case "json":
			binding = v.binding + " "
		case "hash":
			digest = strings.Repeat("0", 64)
		case "created":
			created = "invalid"
		case "created_before_artifact":
			created = "2020-01-01T00:00:00Z"
		case "created_nul":
			created = v.created + "\x00"
		case "json_blob":
			binding = []byte(v.binding)
		case "operation_blob":
			op = []byte(v.operation)
		case "hash_blob":
			digest = []byte(v.hash)
		case "time_blob":
			created = []byte(v.created)
		}
		for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
			_, err := s.db.Exec(verb+` INTO manual_submission_foundations VALUES(?,?,?,?)`, op, binding, digest, created)
			if err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("invalid bytes/identity/time accepted", field, verb, err)
			}
		}
	}
}

func TestHTTPM4B1UnsignedMarkerAndLegacySourcesRejected(t *testing.T) {
	for _, kind := range []string{"marker_only", "dry_run_text", "dry_run_blob", "source_blob_operation", "submission"} {
		t.Run(kind, func(t *testing.T) {
			_, s, _, _, _, _, artifact, _ := submissionFoundationFixture(t, "buy")
			if kind != "marker_only" {
				if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "dry_run_text", "dry_run_blob":
				var operation any = artifact.operation
				if kind == "dry_run_blob" {
					operation = []byte(artifact.operation)
				}
				seedSigningWitnessCorruption(t, s, "dry_run_results", `INSERT INTO dry_run_results(operation_id,step_id,status,created_at) VALUES(?,?,'fail_closed',?)`, operation, "manual_step_"+strings.TrimPrefix(artifact.operation, "manual_op_"), artifact.checked)
			case "source_blob_operation":
				// Alias collision needs only durable operation bytes; retained
				// corruption is injected with original guards restored for read.
				seedSigningWitnessCorruption(t, s, "manual_http_order_sources", `UPDATE manual_http_order_sources SET operation_id=CAST(operation_id AS BLOB)`)
			case "submission":
				seedSigningWitnessCorruption(t, s, "transaction_submissions", `INSERT INTO transaction_submissions(id,attempt_id,tx_hash,sequence,state,created_at,updated_at) SELECT 'unsupported',id,tx_hash,0,'submitting',created_at,updated_at FROM transaction_attempts`)
			}
			if count(t, s.db, "manual_submission_foundation_expected") != 0 {
				t.Fatal("unsupported source/phase offered projection", kind)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
				if err := insertSubmissionFoundation(s.db, submissionFoundationCommand{artifact.operation, "{}", strings.Repeat("0", 64), artifact.checked}, verb); err == nil || before != admittedDatabaseSnapshot(t, s.db) {
					t.Fatal("unsupported source adopted", kind, verb, err)
				}
			}
		})
	}
}

func TestHTTPM4B1WitnessFailuresRollbackAllVerbs(t *testing.T) {
	_, s, _, _, _, _, artifact, _ := submissionFoundationFixture(t, "buy")
	if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
		t.Fatal(err)
	}
	v := submissionFoundationFor(t, s, artifact.operation)
	for _, target := range []string{"audit_abort", "audit_ignore", "alert_abort", "alert_ignore"} {
		table, column, value := "canary_runtime_audit", "event_type", "MANUAL_SUBMISSION_FOUNDATION"
		if strings.HasPrefix(target, "alert") {
			table, column, value = "canary_alert_outbox", "audit_id", "manual-submission-foundation:"+v.operation
		}
		action := "ABORT,'forced witness failure'"
		if strings.HasSuffix(target, "ignore") {
			action = "IGNORE"
		}
		if _, err := s.db.Exec(`CREATE TRIGGER m4b1_fault BEFORE INSERT ON ` + table + ` WHEN NEW.` + column + `='` + value + `' BEGIN SELECT RAISE(` + action + `); END`); err != nil {
			t.Fatal(err)
		}
		before := admittedDatabaseSnapshot(t, s.db)
		for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
			if err := insertSubmissionFoundation(s.db, v, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("partial foundation/witness/scope survived", target, verb, err)
			}
		}
		if _, err := s.db.Exec(`DROP TRIGGER m4b1_fault`); err != nil {
			t.Fatal(err)
		}
	}
	if err := insertSubmissionFoundation(s.db, v, "INSERT"); err != nil {
		t.Fatal(err)
	}
	assertSubmissionFoundation(t, s, v)
}

func TestHTTPM4B1DuplicateIdentityVictimsAndDelivery(t *testing.T) {
	_, s, _, _, _, _, artifact, _ := submissionFoundationFixture(t, "buy")
	if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
		t.Fatal(err)
	}
	v := submissionFoundationFor(t, s, artifact.operation)
	if err := insertSubmissionFoundation(s.db, v, "INSERT"); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
		if err := insertSubmissionFoundation(s.db, v, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("duplicate reminted history", verb, err)
		}
		for _, query := range []string{
			` INTO manual_submission_foundations(rowid,operation_id,binding_json,binding_hash,created_at) SELECT rowid,'outside',binding_json,binding_hash,created_at FROM manual_submission_foundations`,
			` INTO canary_runtime_audit(rowid,id,event_type,reason_code,details_json,created_at) SELECT rowid,'outside','outside','outside','{}',created_at FROM canary_runtime_audit WHERE event_type='MANUAL_SUBMISSION_FOUNDATION'`,
			` INTO canary_alert_outbox(rowid,id,audit_id,severity,state,created_at) SELECT rowid,'outside','outside','INFO','PENDING',created_at FROM canary_alert_outbox WHERE id LIKE 'manual-submission-foundation-alert:%'`,
		} {
			if _, err := s.db.Exec(verb + query); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("conflict victim/IGNORE bypass", verb, query, err)
			}
		}
	}
	for _, query := range []string{
		`UPDATE OR REPLACE manual_submission_foundations SET operation_id=operation_id`,
		`DELETE FROM manual_submission_foundations`,
		`UPDATE canary_runtime_audit SET reason_code='changed' WHERE event_type='MANUAL_SUBMISSION_FOUNDATION'`,
		`DELETE FROM canary_runtime_audit WHERE event_type='MANUAL_SUBMISSION_FOUNDATION'`,
		`UPDATE OR REPLACE canary_alert_outbox SET rowid=rowid+100000 WHERE id LIKE 'manual-submission-foundation-alert:%'`,
		`UPDATE canary_alert_outbox SET severity='CRITICAL' WHERE id LIKE 'manual-submission-foundation-alert:%'`,
		`DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-submission-foundation-alert:%'`,
		`INSERT INTO manual_submission_foundation_scopes SELECT operation_id,binding_hash FROM manual_submission_foundations`,
	} {
		if _, err := s.db.Exec(query); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("immutable identity or scope reopened", query, err)
		}
	}
	if _, err := s.db.Exec(`UPDATE canary_alert_outbox SET state='DELIVERED',delivered_at=created_at WHERE id LIKE 'manual-submission-foundation-alert:%'`); err != nil {
		t.Fatal("legitimate outbox delivery blocked", err)
	}
	assertSubmissionFoundation(t, s, v)
}

func TestHTTPM4B1MissingWitnessCannotRepairOrAdopt(t *testing.T) {
	for _, kind := range []string{"audit", "alert", "blob_audit", "blob_foundation"} {
		t.Run(kind, func(t *testing.T) {
			d, s, _, _, _, path, artifact, _ := submissionFoundationFixture(t, "buy")
			if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
				t.Fatal(err)
			}
			v := submissionFoundationFor(t, s, artifact.operation)
			if err := insertSubmissionFoundation(s.db, v, "INSERT"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO canary_runtime_audit(id,event_type,reason_code,details_json,created_at) VALUES('outside-audit','OUTSIDE','outside','{}','2026-10-16T00:00:00Z');
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) VALUES('outside-alert','outside-audit','INFO','PENDING','2026-10-16T00:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "audit":
				seedSigningWitnessCorruption(t, s, "canary_runtime_audit", `DELETE FROM canary_runtime_audit WHERE event_type='MANUAL_SUBMISSION_FOUNDATION'`)
			case "alert":
				seedSigningWitnessCorruption(t, s, "canary_alert_outbox", `DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-submission-foundation-alert:%'`)
			case "blob_audit":
				seedSigningWitnessCorruption(t, s, "canary_runtime_audit", `UPDATE canary_runtime_audit SET id=CAST(id AS BLOB),event_type=CAST(event_type AS BLOB),operation_id=CAST(operation_id AS BLOB) WHERE event_type='MANUAL_SUBMISSION_FOUNDATION'`)
			case "blob_foundation":
				seedSigningWitnessCorruption(t, s, "manual_submission_foundations", `UPDATE manual_submission_foundations SET operation_id=CAST(operation_id AS BLOB)`)
			}
			_, s = bindingReopen(t, d, path)
			if count(t, s.db, "manual_submission_foundation_complete") != 0 {
				t.Fatal("corruption accepted")
			}
			before := admittedDatabaseSnapshot(t, s.db)
			for _, query := range []string{
				`INSERT INTO manual_submission_foundation_scopes VALUES(?,?)`,
				`INSERT OR REPLACE INTO manual_submission_foundations(operation_id,binding_hash,binding_json,created_at) VALUES(?,?,'{}','2026-10-16T00:00:00Z')`,
			} {
				if _, err := s.db.Exec(query, v.operation, v.hash); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("retained history adopted/repaired", query, err)
				}
			}
			_, err := s.db.Exec(`INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'manual-submission-foundation:'||q.operation_id,'MANUAL_SUBMISSION_FOUNDATION',q.operation_id,q.attempt_id,b.authorization_id,b.authorization_epoch,
 'METADATA_ONLY_NOT_GATE_OR_SEND_PERMISSION',json_object('binding_hash',?,'execution_step_id',q.execution_step_id,'facts_hash',json_extract(?,'$.facts_hash')),?
 FROM manual_nonce_claims q JOIN manual_execution_bindings b ON b.operation_id=q.operation_id WHERE q.operation_id=?`, v.hash, v.binding, v.created, v.operation)
			if err == nil || !strings.Contains(err.Error(), "initial statement only") || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("audit recreation accepted", err)
			}
			_, err = s.db.Exec(`INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) VALUES(?,?,'INFO','PENDING',?)`, "manual-submission-foundation-alert:"+v.operation, "manual-submission-foundation:"+v.operation, v.created)
			if err == nil || !strings.Contains(err.Error(), "initial statement only") || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("alert recreation accepted", err)
			}
			_, err = s.db.Exec(`UPDATE OR REPLACE canary_alert_outbox SET id=?,audit_id=? WHERE id='outside-alert'`, "manual-submission-foundation-alert:"+v.operation, "manual-submission-foundation:"+v.operation)
			if err == nil || !strings.Contains(err.Error(), "foundation alert identity immutable") || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("unrelated alert adopted missing witness", err)
			}
		})
	}
}

func TestHTTPM4B1PreseedNamespaceAndOrphanScopeRejected(t *testing.T) {
	_, s, _, _, _, _, artifact, _ := submissionFoundationFixture(t, "buy")
	if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
		t.Fatal(err)
	}
	v := submissionFoundationFor(t, s, artifact.operation)
	before := admittedDatabaseSnapshot(t, s.db)
	for _, blob := range []bool{false, true} {
		var id, event, operation any = "manual-submission-foundation:" + v.operation, "MANUAL_SUBMISSION_FOUNDATION", v.operation
		if blob {
			id, event, operation = []byte(id.(string)), []byte(event.(string)), []byte(operation.(string))
		}
		for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
			_, err := s.db.Exec(verb+` INTO canary_runtime_audit(id,event_type,operation_id,reason_code,details_json,created_at) VALUES(?,?,?,'preseed','{}',?)`, id, event, operation, v.created)
			if err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("audit namespace preseed accepted", verb, blob, err)
			}
		}
	}
	if _, err := s.db.Exec(`INSERT INTO manual_submission_foundation_scopes VALUES(?,?)`, v.operation, v.hash); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("orphan scope committed", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO manual_submission_foundation_scopes VALUES(?,?)`, v.operation, v.hash); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO manual_submission_foundations VALUES(?,?,?,?)`, v.operation, v.binding, v.hash, v.created); err == nil {
		t.Fatal("preopened scope adopted")
	}
	if err := tx.Rollback(); err != nil || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("scope rollback", err)
	}
}

func TestHTTPM4B1ConcurrentDistinctHandlesExactlyOneFoundation(t *testing.T) {
	_, s, _, _, _, path, artifact, _ := submissionFoundationFixture(t, "buy")
	if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
		t.Fatal(err)
	}
	v := submissionFoundationFor(t, s, artifact.operation)
	db2, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	s2, err := NewStore(db2)
	if err != nil {
		t.Fatal(err)
	}
	before := foundationEconomicSnapshot(t, s)
	start := make(chan struct{})
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			db := s.db
			if i%2 != 0 {
				db = s2.db
			}
			results <- insertSubmissionFoundation(db, v, "INSERT")
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if strings.Contains(err.Error(), "manual submission foundation") || strings.Contains(strings.ToLower(err.Error()), "busy") || strings.Contains(strings.ToLower(err.Error()), "locked") {
			rejected++
		} else {
			t.Fatal("unexplained result", err)
		}
	}
	if accepted != 1 || rejected != 7 || before != foundationEconomicSnapshot(t, s) {
		t.Fatal("duplicate owner or economic mutation", accepted, rejected)
	}
	assertSubmissionFoundation(t, s, v)
}

func TestHTTPM4B1HistoricalMetadataNeverCurrentGate(t *testing.T) {
	_, s, _, _, _, _, artifact, _ := submissionFoundationFixture(t, "buy")
	if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'; UPDATE canary_risk_reservations SET state='frozen'; UPDATE canary_authorization_operation_usage SET state='FROZEN'`); err != nil {
		t.Fatal(err)
	}
	v := submissionFoundationFor(t, s, artifact.operation)
	v.created = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	before := foundationEconomicSnapshot(t, s)
	if err := insertSubmissionFoundation(s.db, v, "INSERT"); err != nil {
		t.Fatal("history was incorrectly used as current action gate", err)
	}
	assertSubmissionFoundation(t, s, v)
	if before != foundationEconomicSnapshot(t, s) {
		t.Fatal("history thawed/recharged/extended identity")
	}
	for _, query := range []string{
		`UPDATE canary_runtime_authorizations SET state='ARMED'`,
		`INSERT INTO transaction_submissions(id,attempt_id,tx_hash,sequence,state,created_at,updated_at) SELECT 'forbidden',id,tx_hash,0,'submitting',created_at,updated_at FROM transaction_attempts`,
		`UPDATE transaction_attempts SET status='submitted'`,
		`UPDATE execution_wallet_lanes SET state='idle',operation_id=NULL,step_id=NULL,reserved_nonce=NULL`,
		`UPDATE canary_risk_reservations SET state='released'`,
		`UPDATE canary_authorization_operation_usage SET state='RELEASED'`,
	} {
		before := admittedDatabaseSnapshot(t, s.db)
		if _, err := s.db.Exec(query); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("foundation opened economic guard", query, err)
		}
	}
}

func TestHTTPM4B1MigrationHasNoOldGuardExceptions(t *testing.T) {
	raw, err := os.ReadFile("../storage/migrations/trade/037_manual_submission_foundation.sql")
	if err != nil {
		t.Fatal(err)
	}
	upper := strings.ToUpper(string(raw))
	for _, denied := range []string{"DROP ", "ALTER ", "UPDATE CANARY_", "UPDATE TRANSACTION_", "UPDATE EXECUTION_", "INSERT INTO TRANSACTION_SUBMISSIONS", "INSERT INTO CANARY_SEND_PERMITS", "INSERT INTO CANARY_RUNTIME_GATE_SNAPSHOTS"} {
		if strings.Contains(upper, denied) {
			t.Fatal("B1 widened old schema/lifecycle", denied)
		}
	}
}

func TestHTTPM4B1PermitPassAndArmRemainClosed(t *testing.T) {
	_, s, _, _, _, _, artifact, _ := submissionFoundationFixture(t, "buy")
	if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
		t.Fatal(err)
	}
	v := submissionFoundationFor(t, s, artifact.operation)
	if err := insertSubmissionFoundation(s.db, v, "INSERT"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ query, reason string }{
		{`UPDATE canary_runtime_authorizations SET state='ARMED'`, "manual atomic admission not connected"},
		{`INSERT INTO canary_send_permits(id,operation_id,attempt_id,authorization_id,authorization_epoch,purpose,artifact_hash,tx_hash,state,issued_at,expires_at)
 SELECT 'forbidden',b.operation_id,q.attempt_id,b.authorization_id,b.authorization_epoch,'FIRST_BROADCAST',json_extract(f.binding_json,'$.raw_sha256'),json_extract(f.binding_json,'$.tx_hash'),'ISSUED',f.created_at,b.expires_at
 FROM manual_submission_foundations f JOIN manual_execution_bindings b ON b.operation_id=f.operation_id JOIN manual_nonce_claims q ON q.operation_id=f.operation_id`, "manual permit not connected"},
		{`INSERT INTO canary_runtime_gate_snapshots(id,operation_id,attempt_id,stage,authorization_id,authorization_epoch,build_sha,release_id,deployment_id,policy_version,policy_hash,gate_evidence_set_hash,emergency_stop_revision,decision,reason_code,checked_at)
 SELECT 'forbidden',b.operation_id,q.attempt_id,'FIRST_BROADCAST',b.authorization_id,b.authorization_epoch,b.build_sha,b.release_id,b.deployment_id,b.policy_version,json_extract(f.binding_json,'$.facts.policy_hash'),b.gate_evidence_set_hash,0,'PASS','forbidden',f.created_at
 FROM manual_submission_foundations f JOIN manual_execution_bindings b ON b.operation_id=f.operation_id JOIN manual_nonce_claims q ON q.operation_id=f.operation_id`, "manual economic gate not connected"},
	} {
		before := admittedDatabaseSnapshot(t, s.db)
		_, err := s.db.Exec(tc.query)
		messageOK := err != nil && (strings.Contains(err.Error(), tc.reason) ||
			strings.Contains(err.Error(), "manual permit requires complete first issuance") ||
			strings.Contains(err.Error(), "manual PASS snapshot requires complete first issuance"))
		if !messageOK || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("foundation opened old guard", tc.reason, err)
		}
	}
}

func TestHTTPM4B1ActualCommitFailureAndRollback(t *testing.T) {
	d, s, _, _, _, path, artifact, _ := submissionFoundationFixture(t, "buy")
	if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
		t.Fatal(err)
	}
	v := submissionFoundationFor(t, s, artifact.operation)
	if _, err := s.db.Exec(`CREATE TABLE foundation_fault_parent(id INTEGER PRIMARY KEY);
 CREATE TABLE foundation_fault_child(id INTEGER REFERENCES foundation_fault_parent(id) DEFERRABLE INITIALLY DEFERRED)`); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), `ROLLBACK`)
	if _, err := conn.ExecContext(context.Background(), `INSERT INTO foundation_fault_child VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `INSERT INTO manual_submission_foundations VALUES(?,?,?,?)`, v.operation, v.binding, v.hash, v.created); err != nil {
		t.Fatal(err)
	}
	var complete int
	if err := conn.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM manual_submission_foundation_complete`).Scan(&complete); err != nil || complete != 1 {
		t.Fatal("fixture did not reach complete precommit state", complete, err)
	}
	_, err = conn.ExecContext(context.Background(), `COMMIT`)
	var sqliteError interface{ Code() int }
	if err == nil || !errors.As(err, &sqliteError) || sqliteError.Code() != 787 {
		t.Fatal("expected real deferred FK commit failure", err)
	}
	if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("failed commit leaked foundation/audit/alert/scope", err)
	}
	_, s = bindingReopen(t, d, path)
	if admittedDatabaseSnapshot(t, s.db) != before || count(t, s.db, "manual_submission_foundations") != 0 {
		t.Fatal("failed COMMIT became durable history")
	}
	if err := insertSubmissionFoundation(s.db, v, "INSERT"); err != nil {
		t.Fatal("original metadata could not be recorded after rollback", err)
	}
	assertSubmissionFoundation(t, s, v)
}

func TestHTTPM4B1SQLCompletenessIsNotCryptographicPermission(t *testing.T) {
	_, s, h, r, scope, _, artifact, cipher := submissionFoundationFixture(t, "buy")
	var envelope manualartifact.EnvelopeMetadata
	if err := json.Unmarshal([]byte(artifact.envelope), &envelope); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := hex.DecodeString(envelope.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext[0] ^= 1
	envelope.Ciphertext = hex.EncodeToString(ciphertext)
	artifact.envelope = encode(envelope)
	if err := insertArtifactCommand(s, artifact, "INSERT"); err != nil {
		t.Fatal("fixture must be structurally complete", err)
	}
	v := submissionFoundationFor(t, s, artifact.operation)
	if err := insertSubmissionFoundation(s.db, v, "INSERT"); err != nil {
		t.Fatal(err)
	}
	assertSubmissionFoundation(t, s, v)
	before := admittedDatabaseSnapshot(t, s.db)
	d := &handoffDecryptor{base: cipher}
	got, err := handoffReader(s, d).load(context.Background(), h.identity, r.RequestID, scope)
	if err == nil || got != (manualSubmissionArtifact{}) || d.calls != 1 || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("schema metadata substituted for authenticated artifact", got, err)
	}
}
