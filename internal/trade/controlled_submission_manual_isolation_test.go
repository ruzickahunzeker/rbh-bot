package trade

import (
	"database/sql"
	"testing"
)

func TestControlledSubmissionLegacySourceIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, corrupt string
		want          int
	}{
		{"legacy", "", 1},
		{"unknown", `DELETE FROM canary_admission_sources`, 0},
		{"manual-owner", `INSERT INTO manual_http_order_sources VALUES('op','step')`, 0},
		{"manual-owner-blob", `INSERT INTO manual_http_order_sources VALUES(CAST('op' AS BLOB),'other')`, 0},
		{"manual-step-blob", `INSERT INTO manual_http_order_sources VALUES('other',CAST('step' AS BLOB))`, 0},
		{"manual-binding-blob", `INSERT INTO manual_execution_bindings VALUES(CAST('op' AS BLOB))`, 0},
		{"manual-claim-blob", `INSERT INTO manual_nonce_claims VALUES('other',CAST('attempt' AS BLOB))`, 0},
		{"issuance-only", `INSERT INTO manual_first_broadcast_issuances VALUES('op','other')`, 0},
		{"issuance-operation-blob", `INSERT INTO manual_first_broadcast_issuances VALUES(CAST('op' AS BLOB),'other')`, 0},
		{"issuance-attempt-blob", `INSERT INTO manual_first_broadcast_issuances VALUES('other',CAST('attempt' AS BLOB))`, 0},
		{"legacy-alias", `INSERT INTO canary_admission_sources VALUES(CAST('op' AS BLOB))`, 0},
		{"legacy-blob-only", `UPDATE canary_admission_sources SET operation_id=CAST(operation_id AS BLOB)`, 0},
		{"wrong-operation", `UPDATE execution_steps SET operation_id='other'`, 0},
		{"unrelated-manual", `INSERT INTO manual_http_order_sources VALUES('other','other'); INSERT INTO manual_first_broadcast_issuances VALUES('other','other')`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			_, err = db.Exec(`CREATE TABLE canary_admission_sources(operation_id);
CREATE TABLE manual_http_order_sources(operation_id,step_id);
CREATE TABLE manual_execution_bindings(operation_id);
CREATE TABLE manual_nonce_claims(operation_id,attempt_id);
CREATE TABLE manual_first_broadcast_issuances(operation_id,attempt_id);
CREATE TABLE execution_steps(id,operation_id);
CREATE TABLE transaction_attempts(id,step_id);
INSERT INTO canary_admission_sources VALUES('op');
INSERT INTO execution_steps VALUES('step','op');
INSERT INTO transaction_attempts VALUES('attempt','step');`)
			if err != nil {
				t.Fatal(err)
			}
			if tc.corrupt != "" {
				if _, err = db.Exec(tc.corrupt); err != nil {
					t.Fatal(err)
				}
			}
			var got int
			if err = db.QueryRow(`SELECT CASE WHEN ` + controlledLegacySourceSQL("'op'", "'attempt'") + ` THEN 1 ELSE 0 END`).Scan(&got); err != nil || got != tc.want {
				t.Fatalf("legacy eligible=%d want=%d err=%v", got, tc.want, err)
			}
		})
	}
}
