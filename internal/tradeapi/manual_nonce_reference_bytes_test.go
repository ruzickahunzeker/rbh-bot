package tradeapi

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestHTTPM3B2BytesOwnClaimEvidenceNoNUL(t *testing.T) {
	for _, field := range []string{"Nonce", "ObservationHash", "CheckedAt", "ObservationRaw"} {
		t.Run(field, func(t *testing.T) {
			d, s, h, r, scope, path := boundSigningFixture(t, "buy")
			q := nonceCommand(t, s, h, r, "23")
			v := reflect.ValueOf(&q).Elem().FieldByName(field)
			v.SetString(v.String() + "\x00junk")
			for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
				before := admittedDatabaseSnapshot(t, s.db)
				if err := insertNonceSQL(context.Background(), s.db, q, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("NUL own claim accepted", field, verb, err)
				}
			}
			// Reproduce already-retained 029 history in a disposable fixture.
			// Restore exact current DDL before exercising history/reopen denial.
			var guardDDL, viewDDL string
			if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='manual_nonce_claim_bytes_source'`).Scan(&guardDDL); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='manual_nonce_claim_complete'`).Scan(&viewDDL); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`DROP TRIGGER manual_nonce_claim_bytes_source; DROP VIEW manual_nonce_claim_complete; CREATE VIEW manual_nonce_claim_complete AS SELECT * FROM manual_nonce_claim_028_complete`); err != nil {
				t.Fatal(err)
			}
			if err := insertNonceSQL(context.Background(), s.db, q, "INSERT"); err != nil {
				t.Fatal("retained 029 setup", err)
			}
			if _, err := s.db.Exec(`DROP VIEW manual_nonce_claim_complete;` + viewDDL + `;` + guardDDL); err != nil {
				t.Fatal(err)
			}
			if count(t, s.db, "manual_nonce_claim_028_complete") != 1 || count(t, s.db, "manual_nonce_claim_complete") != 0 {
				t.Fatal("whole-byte history not distinguished")
			}
			requireNonceAliasDenied(t, s, h, r, scope, true)
			before := admittedDatabaseSnapshot(t, s.db)
			_, s = bindingReopen(t, d, path)
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("retained claim repaired")
			}
			requireNonceAliasDenied(t, s, h, r, scope, true)
		})
	}
}

func TestHTTPM3B2BytesBlobParentHistoryAndVictims(t *testing.T) {
	for _, owner := range []string{"attempt-step", "step-operation"} {
		for _, historical := range []bool{false, true} {
			name := owner + "first"
			if historical {
				name = owner + "history"
			}
			t.Run(name, func(t *testing.T) {
				d, s, h, r, scope, path := bindingFixture(t, "buy")
				if historical {
					if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
						t.Fatal(err)
					}
					if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
						t.Fatal(err)
					}
				}
				bindingLegacyReferences(t, s)
				s.db.SetMaxOpenConns(1)
				if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
					t.Fatal(err)
				}
				stepSQL := `'alias-step'`
				if owner == "attempt-step" {
					stepSQL = `CAST('alias-step' AS BLOB)`
					if _, err := s.db.Exec(`UPDATE execution_steps SET wallet_id=?,status='failed' WHERE id='alias-step'`, h.identity.WalletID); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := s.db.Exec(`UPDATE operations SET wallet_id=? WHERE id='alias-operation'; UPDATE execution_steps SET operation_id=CAST('alias-operation' AS BLOB),status='failed' WHERE id='alias-step'`, h.identity.WalletID); err != nil {
						t.Fatal(err)
					}
				}
				query := `INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('blob-reference',` + stepSQL + `,'23','failed','2026-01-01T00:00:00Z')`
				if historical {
					before := admittedDatabaseSnapshot(t, s.db)
					if _, err := s.db.Exec(query); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
						t.Fatal("blob reference inserted", err)
					}
					restore := nonceAliasInjection(t, s)
					if _, err := s.db.Exec(query); err != nil {
						t.Fatal(err)
					}
					restore()
				} else {
					if _, err := s.db.Exec(query); err != nil {
						t.Fatal(err)
					}
					if _, err := s.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
						t.Fatal(err)
					}
					if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
						t.Fatal(err)
					}
				}
				requireNonceAliasDenied(t, s, h, r, scope, historical)
				if !historical {
					q := nonceCommand(t, s, h, r, "23")
					for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
						before := admittedDatabaseSnapshot(t, s.db)
						if err := insertNonceSQL(context.Background(), s.db, q, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
							t.Fatal("reference SQL claim", err)
						}
					}
				} else {
					for _, recursion := range []string{"OFF", "ON"} {
						if _, err := s.db.Exec(`PRAGMA recursive_triggers=` + recursion); err != nil {
							t.Fatal(err)
						}
						for _, sql := range []string{
							`DELETE FROM transaction_attempts WHERE id='blob-reference'`,
							`UPDATE transaction_attempts SET step_id='alias-insert-step',nonce='24' WHERE id='blob-reference'`,
							`DELETE FROM execution_steps WHERE id='alias-step'`,
							`UPDATE execution_steps SET wallet_id=NULL,operation_id='alias-insert' WHERE id='alias-step'`,
							`DELETE FROM operations WHERE id='alias-operation'`,
							`UPDATE operations SET wallet_id='other' WHERE id='alias-operation'`,
							`INSERT OR REPLACE INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('alias-operation',4663,'other','replacement','fp','swap','failed','2026-01-01T00:00:00Z')`,
						} {
							// In attempt-step only the step wallet owns relevance; its
							// separate operation is unrelated and need not be frozen.
							if owner == "attempt-step" && strings.Contains(sql, "operations") {
								continue
							}
							before := admittedDatabaseSnapshot(t, s.db)
							if _, err := s.db.Exec(sql); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
								t.Fatal("reference victim repaired", sql, err)
							}
						}
					}
				}
				before := admittedDatabaseSnapshot(t, s.db)
				_, s = bindingReopen(t, d, path)
				if admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("reference reopen repair")
				}
				requireNonceAliasDenied(t, s, h, r, scope, historical)
			})
		}
	}
}

func TestHTTPM3B2BytesProjectedParentRename(t *testing.T) {
	for _, owner := range []string{"step", "operation"} {
		for _, recursion := range []string{"OFF", "ON"} {
			t.Run(owner+recursion, func(t *testing.T) {
				_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
				if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
					t.Fatal(err)
				}
				bindingLegacyReferences(t, s)
				restore := nonceAliasInjection(t, s)
				step := "missing-step"
				if owner == "operation" {
					step = "orphan-step"
					if _, err := s.db.Exec(`INSERT INTO execution_steps(id,operation_id,step_index,kind,status) VALUES('orphan-step','missing-operation',0,'curve_swap','failed')`); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := s.db.Exec(`INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('unrelated-orphan',?,'23','failed','2026-01-01T00:00:00Z')`, step); err != nil {
					t.Fatal(err)
				}
				restore()
				if _, err := s.db.Exec(`PRAGMA recursive_triggers=` + recursion); err != nil {
					t.Fatal(err)
				}
				o := &localNonceObserver{}
				if v, err := nonceWriter(s, o).claim(context.Background(), h.identity, r.RequestID, scope); err != nil || !v.Duplicate || o.calls.Load() != 0 {
					t.Fatal("unrelated history globally blocked", v, err)
				}
				query := `UPDATE execution_steps SET id='missing-step',wallet_id=? WHERE id='alias-insert-step'`
				if owner == "operation" {
					query = `UPDATE operations SET id='missing-operation',wallet_id=? WHERE id='alias-insert'`
				}
				for _, verb := range []string{"UPDATE", "UPDATE OR REPLACE"} {
					before := admittedDatabaseSnapshot(t, s.db)
					if _, err := s.db.Exec(strings.Replace(query, "UPDATE", verb, 1), h.identity.WalletID); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
						t.Fatal("projected parent adoption", verb, err)
					}
				}
				if v, err := nonceWriter(s, o).claim(context.Background(), h.identity, r.RequestID, scope); err != nil || !v.Duplicate || v.ExecutionAuthorized || v.SigningAuthorized || v.SendAuthorized || o.calls.Load() != 0 {
					t.Fatal("claim changed after rejection", v, err)
				}
			})
		}
	}
}

func TestHTTPM3B2BytesUnrelatedBlobReferencesRemainHistory(t *testing.T) {
	for _, owner := range []string{"attempt-step", "step-operation"} {
		t.Run(owner, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			bindingLegacyReferences(t, s)
			s.db.SetMaxOpenConns(1)
			if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
				t.Fatal(err)
			}
			stepSQL := `CAST('alias-step' AS BLOB)`
			if owner == "step-operation" {
				stepSQL = `'alias-step'`
				if _, err := s.db.Exec(`UPDATE execution_steps SET operation_id=CAST('alias-operation' AS BLOB) WHERE id='alias-step'`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(`INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('other-reference',` + stepSQL + `,'23','failed','2026-01-01T00:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
				t.Fatal(err)
			}
			o := &localNonceObserver{nonce: "99"}
			if v, err := nonceWriter(s, o).claim(context.Background(), h.identity, r.RequestID, scope); err != nil || !v.Duplicate || v.ExecutionAuthorized || v.SigningAuthorized || v.SendAuthorized || o.calls.Load() != 0 {
				t.Fatal("unrelated references globally blocked", v, err)
			}
		})
	}
}
