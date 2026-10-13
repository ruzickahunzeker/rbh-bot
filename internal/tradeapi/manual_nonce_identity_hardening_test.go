package tradeapi

import (
	"context"
	"strings"
	"testing"
)

func TestHTTPM3B2ResidualBlobWalletIdentity(t *testing.T) {
	for _, owner := range []string{"attempt", "address", "step", "operation"} {
		for _, historical := range []bool{false, true} {
			name := owner + "_first"
			if historical {
				name = owner + "_historical"
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
				if _, err := s.db.Exec(`UPDATE execution_steps SET status='failed' WHERE id='alias-step'`); err != nil {
					t.Fatal(err)
				}
				if owner == "step" {
					if _, err := s.db.Exec(`UPDATE execution_steps SET wallet_id=CAST(? AS BLOB) WHERE id='alias-step'`, h.identity.WalletID); err != nil {
						t.Fatal(err)
					}
				}
				if owner == "operation" {
					if _, err := s.db.Exec(`UPDATE operations SET wallet_id=CAST(? AS BLOB) WHERE id='alias-operation'`, h.identity.WalletID); err != nil {
						t.Fatal(err)
					}
				}
				var wallet, address any
				if owner == "attempt" {
					wallet = []byte(h.identity.WalletID)
				}
				if owner == "address" {
					address = []byte(strings.ToUpper(h.identity.WalletAddress))
				}
				query := `INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at,wallet_id,from_address) VALUES('blob-identity','alias-step','23','failed','2026-01-01T00:00:00Z',?,?)`
				if historical {
					for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
						before := admittedDatabaseSnapshot(t, s.db)
						if _, err := s.db.Exec(strings.Replace(query, "INSERT", verb, 1), wallet, address); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
							t.Fatal("normal SQL identity alias accepted", verb, err)
						}
					}
					// Retained bad history is injected only in this disposable DB;
					// exact production guards/FK are restored before denial tests.
					restore := nonceAliasInjection(t, s)
					if _, err := s.db.Exec(query, wallet, address); err != nil {
						t.Fatal(err)
					}
					restore()
				} else {
					if _, err := s.db.Exec(query, wallet, address); err != nil {
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
							t.Fatal("direct claim accepted", verb, err)
						}
					}
				}
				before := admittedDatabaseSnapshot(t, s.db)
				_, s = bindingReopen(t, d, path)
				if admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("reopen repaired identity")
				}
				requireNonceAliasDenied(t, s, h, r, scope, historical)
			})
		}
	}
}

func TestHTTPM3B2ResidualMissingParentCannotRepair(t *testing.T) {
	for _, kind := range []string{"step", "operation"} {
		for _, recursion := range []string{"OFF", "ON"} {
			t.Run(kind+recursion, func(t *testing.T) {
				d, s, h, r, scope, path := boundSigningFixture(t, "buy")
				if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
					t.Fatal(err)
				}
				bindingLegacyReferences(t, s)
				restore := nonceAliasInjection(t, s)
				step := "missing-step"
				if kind == "operation" {
					step = "orphan-step"
					if _, err := s.db.Exec(`INSERT INTO execution_steps(id,operation_id,step_index,kind,status) VALUES('orphan-step','missing-operation',0,'curve_swap','failed')`); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := s.db.Exec(`INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at,wallet_id) VALUES('retained-alias',?,'24','failed','2026-01-01T00:00:00Z',?)`, step, h.identity.WalletID); err != nil {
					t.Fatal(err)
				}
				restore()
				if _, err := s.db.Exec(`PRAGMA recursive_triggers=` + recursion); err != nil {
					t.Fatal(err)
				}
				requireNonceAliasDenied(t, s, h, r, scope, true)
				insert := `INSERT INTO execution_steps(id,operation_id,step_index,kind,status) VALUES('missing-step','alias-insert',2,'curve_swap','failed')`
				update := `UPDATE execution_steps SET id='missing-step' WHERE id='alias-insert-step'`
				if kind == "operation" {
					insert = `INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('missing-operation',4663,'alias-wallet','new-owner','fp','swap','failed','2026-01-01T00:00:00Z')`
					update = `UPDATE operations SET id='missing-operation' WHERE id='alias-insert'`
				}
				for _, query := range []string{insert, strings.Replace(insert, "INSERT", "INSERT OR IGNORE", 1), strings.Replace(insert, "INSERT", "INSERT OR REPLACE", 1), update, strings.Replace(update, "UPDATE", "UPDATE OR REPLACE", 1)} {
					before := admittedDatabaseSnapshot(t, s.db)
					if _, err := s.db.Exec(query); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
						t.Fatal("parent adoption accepted", query, err)
					}
				}
				before := admittedDatabaseSnapshot(t, s.db)
				_, s = bindingReopen(t, d, path)
				if admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("reopen repaired orphan")
				}
				requireNonceAliasDenied(t, s, h, r, scope, true)
			})
		}
	}
}

func TestHTTPM3B2ResidualCompositeKeyScopeAndVictims(t *testing.T) {
	for _, recursion := range []string{"OFF", "ON"} {
		t.Run(recursion, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			bindingLegacyReferences(t, s)
			restore := nonceAliasInjection(t, s)
			if _, err := s.db.Exec(`INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at,wallet_id) VALUES('retained-alias','alias-step',X'3233','failed','2026-01-01T00:00:00Z',?)`, h.identity.WalletID); err != nil {
				t.Fatal(err)
			}
			restore()
			if _, err := s.db.Exec(`PRAGMA recursive_triggers=` + recursion); err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{
				`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('other-wallet',4663,'other-wallet','alias-key','fp','swap','failed','2026-01-01T00:00:00Z')`,
				`UPDATE operations SET idempotency_key='different-key' WHERE id='other-wallet'`,
				`UPDATE operations SET idempotency_key='alias-key' WHERE id='other-wallet'`,
			} {
				if _, err := s.db.Exec(query); err != nil {
					t.Fatal("unrelated composite key blocked", query, err)
				}
			}
			for _, query := range []string{
				`INSERT OR REPLACE INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('replacement',4663,'alias-wallet','alias-key','fp','swap','failed','2026-01-01T00:00:00Z')`,
				`UPDATE OR REPLACE operations SET wallet_id='alias-wallet' WHERE id='other-wallet'`,
				`INSERT OR REPLACE INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('alias-operation',4663,'other-wallet','other-key','fp','swap','failed','2026-01-01T00:00:00Z')`,
				`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('other-chain',1,'alias-wallet','alias-key','fp','swap','failed','2026-01-01T00:00:00Z')`,
			} {
				before := admittedDatabaseSnapshot(t, s.db)
				if _, err := s.db.Exec(query); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("actual conflict victim lost", query, err)
				}
			}
			requireNonceAliasDenied(t, s, h, r, scope, true)
		})
	}
}

func TestHTTPM3B2ResidualIdentityMutationAndCompatibility(t *testing.T) {
	for _, owner := range []string{"attempt", "address", "step", "operation"} {
		t.Run(owner, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			bindingLegacyReferences(t, s)
			// Unrelated BLOB identity is not globally forbidden and canonical
			// nonconflicting legacy history remains mutable outside MANUAL facts.
			if _, err := s.db.Exec(`INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at,wallet_id) VALUES('unrelated','alias-step','24','failed','2026-01-01T00:00:00Z',X'6F74686572')`); err != nil {
				t.Fatal(err)
			}
			observer := &localNonceObserver{nonce: "99"}
			if v, err := nonceWriter(s, observer).claim(context.Background(), h.identity, r.RequestID, scope); err != nil || !v.Duplicate || v.ExecutionAuthorized || v.SigningAuthorized || v.SendAuthorized || observer.calls.Load() != 0 {
				t.Fatal("unrelated history blocked", v, err)
			}
			query := `UPDATE transaction_attempts SET wallet_id=CAST(? AS BLOB) WHERE id='unrelated'`
			value := h.identity.WalletID
			switch owner {
			case "address":
				query = `UPDATE transaction_attempts SET from_address=CAST(? AS BLOB) WHERE id='unrelated'`
				value = strings.ToUpper(h.identity.WalletAddress)
			case "step":
				query = `UPDATE execution_steps SET wallet_id=CAST(? AS BLOB) WHERE id='alias-step'`
			case "operation":
				query = `UPDATE operations SET wallet_id=CAST(? AS BLOB) WHERE id='alias-operation'`
			}
			for _, verb := range []string{"UPDATE", "UPDATE OR REPLACE"} {
				before := admittedDatabaseSnapshot(t, s.db)
				if _, err := s.db.Exec(strings.Replace(query, "UPDATE", verb, 1), value); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("identity adoption accepted", owner, verb, err)
				}
			}
			// A canonical TEXT owner with different nonce is still safe history.
			if _, err := s.db.Exec(`UPDATE transaction_attempts SET wallet_id=? WHERE id='unrelated'`, h.identity.WalletID); err != nil {
				t.Fatal("canonical nonconflicting history blocked", err)
			}
			if v, err := nonceWriter(s, observer).claim(context.Background(), h.identity, r.RequestID, scope); err != nil || !v.Duplicate || observer.calls.Load() != 0 {
				t.Fatal("canonical history invalid", v, err)
			}
		})
	}
}

func TestHTTPM3B2ResidualEmbeddedNULNonceFailsClosed(t *testing.T) {
	for _, historical := range []bool{false, true} {
		name := "first"
		if historical {
			name = "historical"
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
			query := `INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at,wallet_id) VALUES('nul-history','alias-step',?,'failed','2026-01-01T00:00:00Z',?)`
			if historical {
				before := admittedDatabaseSnapshot(t, s.db)
				if _, err := s.db.Exec(query, "23\x00junk", h.identity.WalletID); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("NUL nonce inserted", err)
				}
				restore := nonceAliasInjection(t, s)
				if _, err := s.db.Exec(query, "23\x00junk", h.identity.WalletID); err != nil {
					t.Fatal(err)
				}
				restore()
			} else {
				if _, err := s.db.Exec(query, "23\x00junk", h.identity.WalletID); err != nil {
					t.Fatal(err)
				}
				if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
					t.Fatal(err)
				}
			}
			requireNonceAliasDenied(t, s, h, r, scope, historical)
			_, s = bindingReopen(t, d, path)
			requireNonceAliasDenied(t, s, h, r, scope, historical)
		})
	}
}
