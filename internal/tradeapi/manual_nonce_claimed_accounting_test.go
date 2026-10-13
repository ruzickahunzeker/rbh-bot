package tradeapi

import (
	"context"
	"strings"
	"testing"
)

func TestHTTPM3B2ClaimedAccountingCorruptionDenied(t *testing.T) {
	for _, kind := range []string{"risk-released", "risk-committed", "usage-released", "usage-consumed"} {
		t.Run(kind, func(t *testing.T) {
			d, s, h, r, scope, path := boundSigningFixture(t, "buy")
			if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			guard, table, state := "manual_nonce_risk_no_release", "canary_risk_reservations", "released"
			if kind == "risk-committed" {
				state = "committed"
			}
			if strings.HasPrefix(kind, "usage") {
				guard = "manual_nonce_usage_no_release"
				table = "canary_authorization_operation_usage"
				state = "RELEASED"
				if kind == "usage-consumed" {
					state = "CONSUMED"
				}
			}
			query := `UPDATE ` + table + ` SET state=? WHERE operation_id=(SELECT operation_id FROM manual_nonce_claims)`
			before := admittedDatabaseSnapshot(t, s.db)
			if _, err := s.db.Exec(query, state); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("normal SQL accounting release allowed", err)
			}
			// Only the exact phase-state guard is removed for retained-history
			// seeding in this disposable DB; restored before any verification.
			var ddl string
			if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name=?`, guard).Scan(&ddl); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`DROP TRIGGER ` + guard); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(query, state); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(ddl); err != nil {
				t.Fatal(err)
			}
			if count(t, s.db, "manual_nonce_claim_030_complete") != 1 || count(t, s.db, "manual_nonce_claim_complete") != 0 {
				t.Fatal("claimed state not distinguished")
			}
			requireNonceAliasDenied(t, s, h, r, scope, true)
			before = admittedDatabaseSnapshot(t, s.db)
			_, s = bindingReopen(t, d, path)
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("reopen repaired accounting")
			}
			requireNonceAliasDenied(t, s, h, r, scope, true)
			for _, sql := range []string{query, `DELETE FROM ` + table + ` WHERE operation_id=(SELECT operation_id FROM manual_nonce_claims)`} {
				before = admittedDatabaseSnapshot(t, s.db)
				args := []any(nil)
				if strings.HasPrefix(sql, "UPDATE") {
					if strings.HasPrefix(kind, "risk") {
						args = []any{"reserved"}
					} else {
						args = []any{"RESERVED"}
					}
				}
				if _, err := s.db.Exec(sql, args...); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("retained accounting silently repaired", sql, err)
				}
			}
		})
	}
}

func TestHTTPM3B2ClaimedAccountingValidFrozenHistory(t *testing.T) {
	for _, risk := range []string{"reserved", "frozen"} {
		for _, usage := range []string{"RESERVED", "FROZEN"} {
			t.Run(risk+usage, func(t *testing.T) {
				d, s, h, r, scope, path := boundSigningFixture(t, "buy")
				if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`UPDATE canary_risk_reservations SET state=? WHERE operation_id=(SELECT operation_id FROM manual_nonce_claims)`, risk); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`UPDATE canary_authorization_operation_usage SET state=? WHERE operation_id=(SELECT operation_id FROM manual_nonce_claims)`, usage); err != nil {
					t.Fatal(err)
				}
				before := admittedDatabaseSnapshot(t, s.db)
				_, s = bindingReopen(t, d, path)
				o := &localNonceObserver{nonce: "99"}
				v, err := nonceWriter(s, o).claim(context.Background(), h.identity, r.RequestID, scope)
				if err != nil || !v.Duplicate || v.Nonce != "23" || v.ExecutionAuthorized || v.SigningAuthorized || v.SendAuthorized || o.calls.Load() != 0 || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("valid frozen history rejected or changed", v, err)
				}
			})
		}
	}
}
