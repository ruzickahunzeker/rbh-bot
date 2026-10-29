package tradeapi

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
)

// Corruption harness only: production migrations keep all of these guards.
// Deliberately damage one isolated fixture to prove readers do not trust an
// unrelated latest row or silently omit broken immutable provenance.
func corruptExecutionHTTPFixture(t *testing.T, db *sql.DB, table, statement string, args ...any) {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`); err != nil {
			t.Error(err)
		}
	}()
	rows, err := conn.QueryContext(context.Background(), `SELECT name FROM sqlite_schema WHERE type='trigger' AND tbl_name=?`, table)
	if err != nil {
		t.Fatal(err)
	}
	var triggers []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		triggers = append(triggers, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(triggers) == 0 {
		t.Fatal("corruption fixture expected production schema guards", table)
	}
	for _, trigger := range triggers {
		if _, err := conn.ExecContext(context.Background(), `DROP TRIGGER "`+strings.ReplaceAll(trigger, `"`, `""`)+`"`); err != nil {
			t.Fatal(err)
		}
	}
	result, err := conn.ExecContext(context.Background(), statement, args...)
	if err != nil {
		t.Fatal("inject isolated reader corruption", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatal("corruption must affect exactly one fixture row", rows, err)
	}
}

func TestHTTPCExecutionIdentityRejectsCorruptHistory(t *testing.T) {
	templates := make(map[bool]manualSubmissionFixture, 2)
	for _, submitted := range []bool{false, true} {
		f := newManualSubmissionFixture(t, "buy")
		if submitted {
			if sub, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID); err != nil || sub.State != "submitted" {
				t.Fatalf("local submitted template: %+v %v", sub, err)
			}
		}
		templates[submitted] = f
	}
	for _, tc := range []struct {
		name, table, statement string
		submitted              bool
	}{
		{
			name: "signed_missing_execution_binding", table: "manual_execution_bindings",
			statement: `DELETE FROM manual_execution_bindings WHERE operation_id=?`,
		},
		{
			name: "signed_missing_nonce_claim", table: "manual_nonce_claims",
			statement: `DELETE FROM manual_nonce_claims WHERE operation_id=?`,
		},
		{
			name: "signed_missing_artifact_command", table: "manual_signed_artifact_commands",
			statement: `DELETE FROM manual_signed_artifact_commands WHERE operation_id=?`,
		},
		{
			name: "signed_latest_shadow_attempt", table: "transaction_attempts",
			statement: `INSERT INTO transaction_attempts(id,step_id,nonce,tx_hash,status,created_at,chain_id,wallet_id)
 SELECT 'shadow-latest-attempt',execution_step_id,nonce,'0x0000000000000000000000000000000000000000000000000000000000000099','signed','9998-01-01T00:00:00Z',4663,wallet_id
 FROM manual_nonce_claims WHERE operation_id=?`,
		},
		{
			name: "signed_artifact_envelope_hash_drift", table: "manual_signed_artifact_commands",
			statement: `UPDATE manual_signed_artifact_commands SET envelope_json=json_set(envelope_json,'$.tx_hash','0x0000000000000000000000000000000000000000000000000000000000000099') WHERE operation_id=?`,
		},
		{
			name: "submitted_missing_first_binding", table: "manual_first_submission_bindings", submitted: true,
			statement: `DELETE FROM manual_first_submission_bindings WHERE operation_id=?`,
		},
		{
			name: "submitted_binding_wrong_submission", table: "manual_first_submission_bindings", submitted: true,
			statement: `UPDATE manual_first_submission_bindings SET submission_id='shadow-nonexistent-submission' WHERE operation_id=?`,
		},
		{
			name: "submitted_binding_tx_hash_drift", table: "manual_first_submission_bindings", submitted: true,
			statement: `UPDATE manual_first_submission_bindings SET tx_hash='0x0000000000000000000000000000000000000000000000000000000000000099' WHERE operation_id=?`,
		},
		{
			name: "submitted_exact_row_tx_hash_drift", table: "transaction_submissions", submitted: true,
			statement: `UPDATE transaction_submissions SET tx_hash='0x0000000000000000000000000000000000000000000000000000000000000099' WHERE id=(SELECT submission_id FROM manual_first_submission_bindings WHERE operation_id=?)`,
		},
		{
			name: "submitted_latest_shadow_submission", table: "transaction_submissions", submitted: true,
			statement: `INSERT INTO transaction_submissions(id,attempt_id,tx_hash,sequence,state,created_at,updated_at)
 SELECT 'shadow-latest-submission',attempt_id,tx_hash,sequence+1,'submitted','9998-01-01T00:00:00Z','9998-01-01T00:00:00Z'
 FROM transaction_submissions WHERE id=(SELECT submission_id FROM manual_first_submission_bindings WHERE operation_id=?)`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := cloneManualSubmissionFixture(t, templates[tc.submitted])
			store := f.e.verifier.store
			h, err := NewWithOptions(store, f.id, testCredential, DefaultPolicy(), nil, Options{ManualIntake: true})
			if err != nil {
				t.Fatal(err)
			}
			var key string
			if err := store.db.QueryRow(`SELECT idempotency_key FROM manual_http_order_sources WHERE id=?`, f.request).Scan(&key); err != nil {
				t.Fatal(err)
			}
			// First prove this is a healthy fixture, not an always-failing route.
			for _, path := range []string{"/v1/trades/" + f.request, "/v1/trades/by-idempotency-key"} {
				got := progressResponse(t, call(h, http.MethodGet, path, key, ""), http.StatusOK, "ADMISSION_RECORDED")
				if got.Execution == nil || got.Execution.TxHash != f.permit.TxHash {
					t.Fatal("healthy identity fixture missing exact signed hash")
				}
			}
			corruptExecutionHTTPFixture(t, store.db, tc.table, tc.statement, f.permit.OperationID)
			before := admittedDatabaseSnapshot(t, store.db)
			calls := f.fake.calls
			for _, path := range []string{"/v1/trades/" + f.request, "/v1/trades/by-idempotency-key"} {
				response := call(h, http.MethodGet, path, key, "")
				if response.Code < 400 || strings.Contains(response.Body.String(), `"execution"`) || strings.Contains(response.Body.String(), `"ADMISSION_RECORDED"`) {
					t.Errorf("corrupt provenance returned healthy history: path=%s code=%d", path, response.Code)
				}
				assertExecutionPublicJSON(t, response.Body.String())
			}
			if admittedDatabaseSnapshot(t, store.db) != before || f.fake.calls != calls {
				t.Fatal("failed closed read mutated state or repeated economic action")
			}
		})
	}
}

func TestHTTPCExecutionIdentityForeignOwnershipIsNotFound(t *testing.T) {
	f := newManualSubmissionFixture(t, "buy")
	if sub, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID); err != nil || sub.State != "submitted" {
		t.Fatal("local submitted fixture", sub, err)
	}
	store := f.e.verifier.store
	var key string
	if err := store.db.QueryRow(`SELECT idempotency_key FROM manual_http_order_sources WHERE id=?`, f.request).Scan(&key); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, store.db)
	for _, tc := range []struct {
		name string
		id   Identity
	}{
		{"principal", Identity{PrincipalID: "foreign-principal", WalletID: f.id.WalletID, WalletAddress: f.id.WalletAddress}},
		{"wallet_id", Identity{PrincipalID: f.id.PrincipalID, WalletID: "foreign-wallet", WalletAddress: f.id.WalletAddress}},
		{"wallet_address", Identity{PrincipalID: f.id.PrincipalID, WalletID: f.id.WalletID, WalletAddress: "0x0000000000000000000000000000000000000099"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := NewWithOptions(store, tc.id, testCredential, DefaultPolicy(), nil, Options{ManualIntake: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/v1/trades/" + f.request, "/v1/trades/by-idempotency-key"} {
				response := call(h, http.MethodGet, path, key, "")
				if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), `"execution"`) || strings.Contains(response.Body.String(), f.permit.TxHash) {
					t.Errorf("foreign identity disclosed history: path=%s code=%d", path, response.Code)
				}
				assertExecutionPublicJSON(t, response.Body.String())
			}
		})
	}
	if admittedDatabaseSnapshot(t, store.db) != before || f.fake.calls != 1 {
		t.Fatal("foreign ownership lookup changed durable state or repeated invocation")
	}
}
