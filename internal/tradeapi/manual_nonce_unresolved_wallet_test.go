package tradeapi

import (
	"context"
	"reflect"
	"testing"
)

// Main cases are normal SQL, with all guards/FK intact. Missing/BLOB parent
// cases explicitly seed retained reference corruption then restore FK before
// either the writer or SQL is exercised. No attempt exists in this witness.
func nonceUnresolvedStep(t *testing.T, s *Store, wallet, owner, parent, status string) {
	t.Helper()
	bindingLegacyReferences(t, s)
	s.db.SetMaxOpenConns(1)
	query := `UPDATE execution_steps SET wallet_id=CAST(? AS BLOB),status=? WHERE id='alias-step'`
	if owner == "operation" {
		query = `UPDATE operations SET wallet_id=CAST(? AS BLOB) WHERE id='alias-operation'`
		if _, err := s.db.Exec(query, wallet); err != nil {
			t.Fatal(err)
		}
		query = `UPDATE execution_steps SET status=? WHERE id='alias-step'`
		if _, err := s.db.Exec(query, status); err != nil {
			t.Fatal(err)
		}
	} else if _, err := s.db.Exec(query, wallet, status); err != nil {
		t.Fatal(err)
	}
	if parent != "exact" {
		if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
			t.Fatal(err)
		}
		ref := `CAST('alias-operation' AS BLOB)`
		if parent == "missing" {
			ref = `'missing-parent'`
		}
		if _, err := s.db.Exec(`UPDATE execution_steps SET operation_id=` + ref + ` WHERE id='alias-step'`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
			t.Fatal(err)
		}
	}
	var fk int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 || count(t, s.db, "transaction_attempts") != 0 {
		t.Fatal("fixture must preserve guards/FK and no attempts", fk, err)
	}
}

func TestHTTPM3B2UnresolvedWalletBeforeQuerySQLAndReopen(t *testing.T) {
	for _, tc := range []struct{ owner, parent string }{{"step", "exact"}, {"operation", "exact"}, {"step", "missing"}, {"step", "blob"}, {"operation", "blob"}} {
		t.Run(tc.owner+tc.parent, func(t *testing.T) {
			d, s, h, r, scope, path := boundSigningFixture(t, "buy")
			q := nonceCommand(t, s, h, r, "23")
			nonceUnresolvedStep(t, s, h.identity.WalletID, tc.owner, tc.parent, "nonce_reserved")
			for _, phase := range []string{"original", "reopen"} {
				before := admittedDatabaseSnapshot(t, s.db)
				if phase == "reopen" {
					_, s = bindingReopen(t, d, path)
					if admittedDatabaseSnapshot(t, s.db) != before {
						t.Fatal("reopen changed retained witness")
					}
				}
				o := &localNonceObserver{nonce: "23"}
				v, err := nonceWriter(s, o).claim(context.Background(), h.identity, r.RequestID, scope)
				if err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) || o.calls.Load() != 0 || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("unresolved identity bypassed pre-query gate", v, err, o.calls.Load())
				}
				for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
					if err := insertNonceSQL(context.Background(), s.db, q, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
						t.Fatal("direct SQL bypass/partial effects", phase, verb, err)
					}
				}
			}
		})
	}
}

func TestHTTPM3B2UnresolvedWalletAppearsAfterObservation(t *testing.T) {
	for _, stage := range []string{"after_observation", "before_insert"} {
		t.Run(stage, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			bindingLegacyReferences(t, s)
			o := &localNonceObserver{nonce: "23"}
			w := nonceWriter(s, o)
			before := admittedDatabaseSnapshot(t, s.db)
			w.checkpoint = func(at string) error {
				if at == stage && at == "after_observation" {
					_, err := s.db.Exec(`UPDATE execution_steps SET wallet_id=CAST(? AS BLOB),status='nonce_reserved' WHERE id='alias-step'`, h.identity.WalletID)
					before = admittedDatabaseSnapshot(t, s.db)
					return err
				}
				return nil
			}
			if stage == "before_insert" {
				// Simulate same-transaction late witness through a BEFORE
				// trigger; final completeness is not relied on for denial.
				if _, err := s.db.Exec(`CREATE TRIGGER seed_unresolved_before_claim BEFORE INSERT ON manual_nonce_claims BEGIN
				 UPDATE execution_steps SET wallet_id=CAST(NEW.wallet_id AS BLOB),status='nonce_reserved' WHERE id='alias-step'; END`); err != nil {
					t.Fatal(err)
				}
			}
			v, err := w.claim(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) || o.calls.Load() != 1 || count(t, s.db, "manual_nonce_claims") != 0 || count(t, s.db, "transaction_attempts") != 0 {
				t.Fatal("late unresolved witness accepted", v, err, o.calls.Load())
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("provisional claim or late-trigger metadata did not roll back")
			}
			if count(t, s.db, "canary_risk_reservations") != 1 || count(t, s.db, "canary_authorization_operation_usage") != 1 || count(t, s.db, "canary_send_permits") != 0 {
				t.Fatal("financial/send owner changed")
			}
		})
	}
}

func TestHTTPM3B2UnresolvedWalletScopeAndHistoricalOnly(t *testing.T) {
	for _, tc := range []struct{ wallet, status string }{{"alias-wallet", "nonce_reserved"}, {"own", "failed"}, {"own", "prepared"}} {
		t.Run(tc.wallet+tc.status, func(t *testing.T) {
			d, s, h, r, scope, path := boundSigningFixture(t, "sell_percent")
			wallet := tc.wallet
			if wallet == "own" {
				wallet = h.identity.WalletID
			}
			nonceUnresolvedStep(t, s, wallet, "step", "exact", tc.status)
			o := &localNonceObserver{nonce: "23"}
			v, err := nonceWriter(s, o).claim(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || v.Nonce != "23" || v.Duplicate || o.calls.Load() != 1 {
				t.Fatal("unrelated/terminal metadata blocked", v, err)
			}
			if _, err := s.db.Exec(`UPDATE canary_risk_reservations SET state='frozen'; UPDATE canary_authorization_operation_usage SET state='FROZEN'`); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			_, s = bindingReopen(t, d, path)
			o = &localNonceObserver{nonce: "99"}
			v, err = nonceWriter(s, o).claim(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || !v.Duplicate || v.Nonce != "23" || o.calls.Load() != 0 || admittedDatabaseSnapshot(t, s.db) != before || v.ExecutionAuthorized || v.SigningAuthorized || v.SendAuthorized {
				t.Fatal("historical duplicate changed", v, err)
			}
		})
	}
}

func TestHTTPM3B2UnresolvedWalletEveryActiveState(t *testing.T) {
	for _, status := range []any{"manual_execution_bound", "nonce_reserved", "signing", "signed", "submitted", "broadcast_unknown", "reconciling", "manual_resolution", []byte("nonce_reserved"), "nonce_reserved\x00"} {
		t.Run(stringifyNonceStatus(status), func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			q := nonceCommand(t, s, h, r, "23")
			bindingLegacyReferences(t, s)
			if _, err := s.db.Exec(`UPDATE execution_steps SET wallet_id=CAST(? AS BLOB),status=? WHERE id='alias-step'`, h.identity.WalletID, status); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			o := &localNonceObserver{nonce: "23"}
			v, err := nonceWriter(s, o).claim(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) || o.calls.Load() != 0 {
				t.Fatal("active/unverifiable status missed", v, err)
			}
			if err := insertNonceSQL(context.Background(), s.db, q, "INSERT"); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("direct active/unverifiable status missed", err)
			}
		})
	}
}

func stringifyNonceStatus(v any) string {
	if b, ok := v.([]byte); ok {
		return "blob-" + string(b)
	}
	return v.(string)
}
