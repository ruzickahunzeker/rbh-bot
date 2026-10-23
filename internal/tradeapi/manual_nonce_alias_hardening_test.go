package tradeapi

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func requireNonceAliasDenied(t *testing.T, s *Store, h *API, r Record, scope manualAdmissionScope, historical bool) {
	t.Helper()
	before := admittedDatabaseSnapshot(t, s.db)
	o := &localNonceObserver{nonce: "23"}
	v, err := nonceWriter(s, o).claim(context.Background(), h.identity, r.RequestID, scope)
	if err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("alias accepted or repaired", v, err)
	}
	if historical && o.calls.Load() != 0 {
		t.Fatal("corrupt history caused another nonce query")
	}
	for _, table := range []string{"transaction_submissions", "receipt_observations", "position_effects", "canary_send_permits", "canary_runtime_gate_snapshots"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("prohibited effect", table)
		}
	}
}

// Capture and restore exactly the production guards around explicit corruption
// injection in disposable fixtures. Normal-SQL tests below remove no guard/FK.
func nonceAliasInjection(t *testing.T, s *Store) func() {
	t.Helper()
	s.db.SetMaxOpenConns(1)
	var guards []string
	for _, name := range []string{"manual_execution_no_attempt", "manual_nonce_alias_insert"} {
		var ddl string
		if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&ddl); err != nil {
			t.Fatal(err)
		}
		guards = append(guards, ddl)
		if _, err := s.db.Exec(`DROP TRIGGER ` + name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	return func() {
		for _, ddl := range guards {
			if _, err := s.db.Exec(ddl); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHTTPM3B2HardeningLegacyNonceStorageNormalSQL(t *testing.T) {
	for _, nonce := range []string{`X'3233'`, `X'3234'`, `X''`, `''`, `'023'`, `'+23'`, `'-1'`, `'23.5'`, `'18446744073709551615'`, `'18446744073709551616'`, `'23'`} {
		t.Run(nonce, func(t *testing.T) {
			d, s, h, r, scope, path := bindingFixture(t, "buy")
			bindingLegacyReferences(t, s)
			// Inert failed legacy history with operation-only wallet identity.
			if _, err := s.db.Exec(`UPDATE operations SET wallet_id=? WHERE id='alias-operation';
 UPDATE execution_steps SET status='failed' WHERE id='alias-step';
 INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('legacy-nonce','alias-step',`+nonce+`,'failed','2026-01-01T00:00:00Z')`, h.identity.WalletID); err != nil {
				t.Fatal(err)
			}
			var fk, triggers int
			if err := s.db.QueryRow(`SELECT (SELECT foreign_keys FROM pragma_foreign_keys),(SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name IN ('manual_execution_no_attempt','manual_nonce_alias_insert'))`).Scan(&fk, &triggers); err != nil || fk != 1 || triggers != 2 {
				t.Fatal("production guards absent", fk, triggers, err)
			}
			if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			requireNonceAliasDenied(t, s, h, r, scope, false)
			q := nonceCommand(t, s, h, r, "23")
			for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
				before := admittedDatabaseSnapshot(t, s.db)
				if err := insertNonceSQL(context.Background(), s.db, q, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("direct SQL claim bypass", verb, err)
				}
			}
			before := admittedDatabaseSnapshot(t, s.db)
			_, s = bindingReopen(t, d, path)
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("reopen changed malformed history")
			}
			requireNonceAliasDenied(t, s, h, r, scope, false)
		})
	}
}

func TestHTTPM3B2HardeningLegacyNonceScopeAndCanonicalCompatibility(t *testing.T) {
	for _, tc := range []struct {
		nonce      string
		sameWallet bool
	}{{`'22'`, true}, {`'18446744073709551614'`, true}, {`X'3233'`, false}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			_, s, h, r, scope, _ := bindingFixture(t, "buy")
			bindingLegacyReferences(t, s)
			if _, err := s.db.Exec(`UPDATE execution_steps SET status='failed' WHERE id='alias-step';
 INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('legacy-nonce','alias-step',` + tc.nonce + `,'failed','2026-01-01T00:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			if tc.sameWallet {
				if _, err := s.db.Exec(`UPDATE operations SET wallet_id=? WHERE id='alias-operation'`, h.identity.WalletID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			v, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || v.Nonce != "23" || v.Duplicate || v.SigningAuthorized || v.SendAuthorized || v.ExecutionAuthorized {
				t.Fatal("unrelated/nonconflicting history denied", v, err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			v, err = nonceWriter(s, &localNonceObserver{}).claim(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || !v.Duplicate || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("valid duplicate broken", v, err)
			}
		})
	}
}

func TestHTTPM3B2HardeningOrphanFirstClaimFailsClosed(t *testing.T) {
	for _, owner := range []string{"wallet", "address", "missing_operation"} {
		t.Run(owner, func(t *testing.T) {
			d, s, h, r, scope, path := bindingFixture(t, "buy")
			s.db.SetMaxOpenConns(1)
			if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
				t.Fatal(err)
			}
			// Retained predecessor corruption only; no production guard removed.
			step := "missing-step"
			var wallet, address any
			switch owner {
			case "wallet":
				wallet = h.identity.WalletID
			case "address":
				address = strings.ToUpper(h.identity.WalletAddress)
			case "missing_operation":
				step = "orphan-step"
				if _, err := s.db.Exec(`INSERT INTO execution_steps(id,operation_id,step_index,kind,status,wallet_id) VALUES(?,'missing-operation',0,'curve_swap','failed',?)`, step, h.identity.WalletID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(`INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at,wallet_id,from_address) VALUES('legacy-orphan',?,'23','failed','2026-01-01T00:00:00Z',?,?)`, step, wallet, address); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
				t.Fatal(err)
			}
			if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			requireNonceAliasDenied(t, s, h, r, scope, false)
			q := nonceCommand(t, s, h, r, "23")
			before := admittedDatabaseSnapshot(t, s.db)
			if err := insertNonceSQL(context.Background(), s.db, q, "INSERT OR IGNORE"); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("direct SQL orphan accepted", err)
			}
			_, s = bindingReopen(t, d, path)
			requireNonceAliasDenied(t, s, h, r, scope, false)
		})
	}
}

func TestHTTPM3B2HardeningRetainedAliasHistoryAndVictims(t *testing.T) {
	for _, kind := range []string{"orphan_wallet", "orphan_address", "blob_wallet", "blob_operation", "blob_step", "missing_operation"} {
		t.Run(kind, func(t *testing.T) {
			d, s, h, r, scope, path := boundSigningFixture(t, "buy")
			if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			bindingLegacyReferences(t, s)
			if _, err := s.db.Exec(`UPDATE execution_steps SET status='failed' WHERE id='alias-step'`); err != nil {
				t.Fatal(err)
			}
			step, nonce := "alias-step", `X'3233'`
			var wallet, address any
			switch kind {
			case "orphan_wallet", "orphan_address":
				step, nonce = "missing-step", `'23'`
				if kind == "orphan_wallet" {
					wallet = h.identity.WalletID
				} else {
					address = strings.ToUpper(h.identity.WalletAddress)
				}
			case "blob_wallet":
				wallet = h.identity.WalletID
			case "blob_operation":
				if _, err := s.db.Exec(`UPDATE operations SET wallet_id=? WHERE id='alias-operation'`, h.identity.WalletID); err != nil {
					t.Fatal(err)
				}
			case "blob_step", "missing_operation":
				if _, err := s.db.Exec(`UPDATE execution_steps SET wallet_id=? WHERE id='alias-step'`, h.identity.WalletID); err != nil {
					t.Fatal(err)
				}
			}
			restore := nonceAliasInjection(t, s)
			if kind == "missing_operation" {
				if _, err := s.db.Exec(`INSERT INTO execution_steps(id,operation_id,step_index,kind,status,wallet_id) VALUES('orphan-step','missing-operation',0,'curve_swap','failed',?)`, h.identity.WalletID); err != nil {
					t.Fatal(err)
				}
				step, nonce = "orphan-step", `'23'`
			}
			if _, err := s.db.Exec(`INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at,wallet_id,from_address) VALUES('retained-alias',?,`+nonce+`,'failed','2026-01-01T00:00:00Z',?,?)`, step, wallet, address); err != nil {
				t.Fatal(err)
			}
			restore()
			requireNonceAliasDenied(t, s, h, r, scope, true)
			before := admittedDatabaseSnapshot(t, s.db)
			_, s = bindingReopen(t, d, path)
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("migration/reopen repaired retained alias")
			}
			requireNonceAliasDenied(t, s, h, r, scope, true)
			for _, recursion := range []string{"OFF", "ON"} {
				if _, err := s.db.Exec(`PRAGMA recursive_triggers=` + recursion); err != nil {
					t.Fatal(err)
				}
				queries := []string{
					`DELETE FROM transaction_attempts WHERE id='retained-alias'`,
					`UPDATE transaction_attempts SET nonce='24',wallet_id=NULL,from_address=NULL,step_id='alias-insert-step' WHERE id='retained-alias'`,
					`INSERT OR REPLACE INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('retained-alias','alias-insert-step','24','failed','2026-01-01T00:00:00Z')`,
					`INSERT OR REPLACE INTO transaction_attempts(rowid,id,step_id,nonce,status,created_at) SELECT rowid,'replacement','alias-insert-step','24','failed','2026-01-01T00:00:00Z' FROM transaction_attempts WHERE id='retained-alias'`,
					`UPDATE OR REPLACE transaction_attempts SET rowid=(SELECT rowid FROM transaction_attempts WHERE id='retained-alias') WHERE id=(SELECT attempt_id FROM manual_nonce_claims)`,
				}
				if strings.HasPrefix(kind, "blob_") {
					queries = append(queries,
						`UPDATE execution_steps SET wallet_id='alias-wallet',operation_id='alias-insert' WHERE id='alias-step'`,
						`UPDATE operations SET wallet_id='alias-wallet' WHERE id='alias-operation'`,
						`INSERT OR REPLACE INTO execution_steps(id,operation_id,step_index,kind,status) VALUES('alias-step','alias-insert',2,'curve_swap','failed')`,
						`INSERT OR REPLACE INTO execution_steps(rowid,id,operation_id,step_index,kind,status) SELECT rowid,'hidden-step','alias-insert',2,'curve_swap','failed' FROM execution_steps WHERE id='alias-step'`,
						`UPDATE OR REPLACE execution_steps SET rowid=(SELECT rowid FROM execution_steps WHERE id='alias-step') WHERE id='alias-insert-step'`,
						`INSERT OR REPLACE INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('alias-operation',4663,'alias-wallet','replacement-key','fp','swap','failed','2026-01-01T00:00:00Z')`,
						`INSERT OR REPLACE INTO operations(rowid,id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) SELECT rowid,'hidden-owner',4663,'alias-wallet','replacement-key','fp','swap','failed','2026-01-01T00:00:00Z' FROM operations WHERE id='alias-operation'`,
						`UPDATE OR REPLACE operations SET rowid=(SELECT rowid FROM operations WHERE id='alias-operation') WHERE id='alias-insert'`,
						`DELETE FROM execution_steps WHERE id='alias-step'`,
						`DELETE FROM operations WHERE id='alias-operation'`)
				}
				for _, query := range queries {
					before := admittedDatabaseSnapshot(t, s.db)
					if _, err := s.db.Exec(query); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
						t.Fatal("pollution hidden by mutation", query, err)
					}
				}
			}
		})
	}
}
