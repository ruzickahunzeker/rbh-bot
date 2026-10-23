package tradeapi

import (
	"strings"
	"testing"
)

const signingAuditReinsert = ` INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'manual-sign-intent:'||i.operation_id,'MANUAL_SIGNING_INTENT',i.operation_id,q.attempt_id,b.authorization_id,b.authorization_epoch,'SIGNER_MAY_HAVE_RUN_NOT_PERMISSION',
 json_object('owner_id',i.owner_id,'facts_hash',i.facts_hash,'observation_hash',i.observation_hash,'execution_step_id',q.execution_step_id,'nonce',q.nonce),i.checked_at
 FROM manual_signing_intents i JOIN manual_nonce_claims q ON q.operation_id=i.operation_id JOIN manual_execution_bindings b ON b.operation_id=i.operation_id`
const signingAlertReinsert = ` INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 SELECT 'manual-sign-intent-alert:'||operation_id,'manual-sign-intent:'||operation_id,'WARNING','PENDING',checked_at FROM manual_signing_intents`

func TestHTTPM3B3CreationMissingWitnessReopenNoReinsert(t *testing.T) {
	for _, missing := range []string{"audit", "alert", "both"} {
		t.Run(missing, func(t *testing.T) {
			d, s, h, r, scope, path, q := signingWitnessFixture(t)
			if err := insertSigningIntent(s, q, "INSERT"); err != nil {
				t.Fatal(err)
			}
			if missing == "alert" || missing == "both" {
				seedSigningWitnessCorruption(t, s, "canary_alert_outbox", `DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-sign-intent-alert:%'`)
			}
			if missing == "audit" || missing == "both" {
				seedSigningWitnessCorruption(t, s, "canary_runtime_audit", `DELETE FROM canary_runtime_audit WHERE event_type='MANUAL_SIGNING_INTENT'`)
			}
			_, s = bindingReopen(t, d, path)
			assertSigningWitnessIncomplete(t, s, h, r, scope)
			before := admittedDatabaseSnapshot(t, s.db)
			for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
				for _, statement := range []string{signingAuditReinsert, signingAlertReinsert} {
					if _, err := s.db.Exec(verb + statement); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
						t.Fatal("retained missing witness rebuilt", missing, verb, err)
					}
				}
				if _, err := s.db.Exec(verb+` INTO manual_signing_witness_creation_scopes VALUES(?,?,?)`, q.operation, q.owner, q.digest); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("retained marker scope reopened", missing, verb, err)
				}
			}
			assertSigningWitnessIncomplete(t, s, h, r, scope)
		})
	}
}

func TestHTTPM3B3CreationScopeCannotCommitOrBePreseeded(t *testing.T) {
	_, s, _, r, _, _, q := signingWitnessFixture(t)
	s.db.SetMaxOpenConns(1)
	before := admittedDatabaseSnapshot(t, s.db)
	for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
		if _, err := s.db.Exec(verb+` INTO manual_signing_witness_creation_scopes VALUES(?,?,?)`, q.operation, q.owner, q.digest); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("standalone scope committed without its marker", verb, err)
		}
	}
	if _, err := s.db.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO manual_signing_witness_creation_scopes(rowid,operation_id,owner_id,facts_hash) VALUES(1234,?,?,?)`, q.operation, q.owner, q.digest); err != nil {
		t.Fatal("deferred parent source", err)
	}
	for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
		if err := insertSigningIntent(s, q, verb); err == nil {
			t.Fatal("preseeded scope adopted by marker", verb)
		}
		if _, err := s.db.Exec(verb+` INTO manual_signing_witness_creation_scopes(rowid,operation_id,owner_id,facts_hash) VALUES(1234,?,?,?)`, q.operation, strings.Repeat("b", 64), q.digest); err == nil {
			t.Fatal("scope rowid victim replaced", verb)
		}
	}
	for _, statement := range []string{`UPDATE manual_signing_witness_creation_scopes SET owner_id='other'`, `DELETE FROM manual_signing_witness_creation_scopes`} {
		if _, err := s.db.Exec(statement); err == nil {
			t.Fatal("incomplete scope changed or discarded", statement)
		}
	}
	if _, err := s.db.Exec(`COMMIT`); err == nil {
		t.Fatal("deferred scope committed without parent")
	}
	if _, err := s.db.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("failed scope transaction leaked effects")
	}
	if err := insertSigningIntent(s, q, "INSERT"); err != nil || count(t, s.db, "manual_signing_witness_creation_scopes") != 0 || count(t, s.db, "manual_signing_intent_complete") != 1 {
		t.Fatal("legitimate atomic creation failed", err)
	}
	assertNonceClaim(t, s, r, "23")
}

func TestHTTPM3B3CreationScopeCloseFaultAtomicRollback(t *testing.T) {
	for _, boundary := range []string{"INSERT", "DELETE"} {
		for _, action := range []string{"RAISE(IGNORE)", "RAISE(ABORT,'forced scope failure')"} {
			t.Run(boundary+action, func(t *testing.T) {
				_, s, _, _, _, _, q := signingWitnessFixture(t)
				if _, err := s.db.Exec(`CREATE TRIGGER forced_scope_fault BEFORE ` + boundary + ` ON manual_signing_witness_creation_scopes BEGIN SELECT ` + action + `; END`); err != nil {
					t.Fatal(err)
				}
				before := admittedDatabaseSnapshot(t, s.db)
				for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
					if err := insertSigningIntent(s, q, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before || count(t, s.db, "manual_signing_witness_creation_scopes") != 0 {
						t.Fatal("scope fault leaked marker/witness/scope", action, verb, err)
					}
				}
			})
		}
	}
}
