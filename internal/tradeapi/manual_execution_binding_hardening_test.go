package tradeapi

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

// Inert legacy metadata, not a nonce allocation or financial action. It is
// deliberately on another wallet so only reference identity can catch aliases.
func bindingLegacyReferences(t *testing.T, s *Store) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO dry_run_wallets VALUES('alias-wallet',4663,?,1);
 INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at)
 VALUES('alias-operation',4663,'alias-wallet','alias-key','fp','swap','prepared','2026-01-01T00:00:00Z'),
 ('alias-insert',4663,'alias-wallet','alias-insert-key','fp2','swap','prepared','2026-01-01T00:00:00Z');
 INSERT INTO execution_steps(id,operation_id,step_index,kind,status)
 VALUES('alias-step','alias-operation',0,'curve_swap','prepared'),('alias-insert-step','alias-insert',0,'curve_swap','prepared');
 INSERT INTO execution_reservations(operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at)
 VALUES('alias-operation','alias-step','alias-wallet','native','1','1','released','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
 INSERT INTO execution_wallet_lanes(wallet_id,address,state,operation_id,step_id,reserved_nonce,updated_at)
 VALUES('alias-wallet',?,'reserved','alias-operation','alias-step','7','2026-01-01T00:00:00Z')`, fmt.Sprintf("0x%040x", 999), fmt.Sprintf("0x%040x", 999))
	if err != nil {
		t.Fatal(err)
	}
}

func bindingReopen(t *testing.T, d *storage.Database, path string) (*storage.Database, *Store) {
	t.Helper()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fresh.Close() })
	if err := fresh.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	return fresh, s
}

func rejectBindingReference(t *testing.T, s *Store, query string) {
	t.Helper()
	before := admittedDatabaseSnapshot(t, s.db)
	if _, err := s.db.Exec(query); err == nil || !strings.Contains(err.Error(), "manual") {
		t.Fatalf("expected MANUAL reference guard rejection: %s: %v", query, err)
	}
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("rejected reference write changed durable data")
	}
}

func TestHTTPM2HardeningFinancialReferenceGuards(t *testing.T) {
	d, s, h, r, scope, path := bindingFixture(t, "buy")
	bindingLegacyReferences(t, s)
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"execution_step_id", "validation_step_id"} {
		for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
			rejectBindingReference(t, s, verb+` INTO execution_reservations(operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at)
 SELECT 'alias-insert',`+column+`,wallet_id,'native','1','1','reserved',checked_at,checked_at FROM manual_execution_bindings`)
		}
		rejectBindingReference(t, s, `UPDATE execution_reservations SET step_id=(SELECT `+column+` FROM manual_execution_bindings),status='reserved' WHERE operation_id='alias-operation'`)
		rejectBindingReference(t, s, `UPDATE OR REPLACE execution_reservations SET step_id=(SELECT `+column+` FROM manual_execution_bindings),status='reserved' WHERE operation_id='alias-operation'`)
	}
	rejectBindingReference(t, s, `UPDATE execution_reservations SET operation_id=(SELECT operation_id FROM manual_execution_bindings) WHERE operation_id='alias-operation'`)
	before := admittedDatabaseSnapshot(t, s.db)
	_, s = bindingReopen(t, d, path)
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("reopen changed original/legacy metadata")
	}
	v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || !v.Duplicate || v.ExecutionAuthorized || v.SendAuthorized || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("valid duplicate drift", v, err)
	}
}

func TestHTTPM2HardeningLaneReferenceGuards(t *testing.T) {
	d, s, h, r, scope, path := bindingFixture(t, "sell_percent")
	bindingLegacyReferences(t, s)
	if _, err := s.db.Exec(`INSERT INTO dry_run_wallets VALUES('fresh-alias-wallet',4663,?,1)`, fmt.Sprintf("0x%040x", 1000)); err != nil {
		t.Fatal(err)
	}
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"execution_step_id", "validation_step_id"} {
		for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
			for _, nonce := range []string{"NULL", "'7'"} {
				rejectBindingReference(t, s, verb+` INTO execution_wallet_lanes(wallet_id,address,state,operation_id,step_id,reserved_nonce,updated_at)
 SELECT 'fresh-alias-wallet','`+fmt.Sprintf("0x%040x", 1000)+`','reserved','alias-insert',`+column+`,`+nonce+`,checked_at FROM manual_execution_bindings`)
			}
		}
		rejectBindingReference(t, s, `UPDATE execution_wallet_lanes SET step_id=(SELECT `+column+` FROM manual_execution_bindings) WHERE wallet_id='alias-wallet'`)
		rejectBindingReference(t, s, `UPDATE OR REPLACE execution_wallet_lanes SET step_id=(SELECT `+column+` FROM manual_execution_bindings) WHERE wallet_id='alias-wallet'`)
	}
	for _, assignment := range []string{
		`operation_id=(SELECT operation_id FROM manual_execution_bindings)`,
		`step_id=(SELECT execution_step_id FROM manual_execution_bindings),operation_id=NULL,reserved_nonce=NULL`,
		`step_id=(SELECT execution_step_id FROM manual_execution_bindings),operation_id=(SELECT operation_id FROM manual_execution_bindings),reserved_nonce=NULL`,
		`wallet_id=(SELECT wallet_id FROM manual_execution_bindings)`,
		`address=(SELECT wallet_address FROM manual_execution_bindings)`,
	} {
		rejectBindingReference(t, s, `UPDATE execution_wallet_lanes SET `+assignment+` WHERE wallet_id='alias-wallet'`)
	}
	// REPLACE must not remove the canonical lane through a conflicting wallet.
	rejectBindingReference(t, s, `INSERT OR REPLACE INTO execution_wallet_lanes(wallet_id,address,state,operation_id,step_id,reserved_nonce,updated_at)
 SELECT wallet_id,wallet_address,'reserved','alias-operation','alias-step','7',checked_at FROM manual_execution_bindings`)
	before := admittedDatabaseSnapshot(t, s.db)
	_, s = bindingReopen(t, d, path)
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("reopen changed lanes")
	}
	v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || !v.Duplicate || v.ExecutionAuthorized || v.SendAuthorized || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("valid duplicate drift", v, err)
	}
}

// Reconstruct only the landed 021 reference boundary in disposable fixtures.
// 001..021 guards remain present; no production migration is rewritten.
func bindingPredecessor021(t *testing.T, s *Store) {
	t.Helper()
	bindingPredecessor022(t, s)
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='trigger' AND name LIKE 'manual_binding_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(names) != 9 {
		t.Fatal("unexpected 022 guard set", names)
	}
	for _, name := range names {
		if _, err := s.db.Exec(`DROP TRIGGER "` + name + `"`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`DROP VIEW manual_execution_binding_integrity; DELETE FROM schema_migrations WHERE version=22`); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPM2Hardening021PollutionUpgradeRejectsWithoutRepair(t *testing.T) {
	for _, kind := range []string{"reservation_insert", "reservation_update", "lane_insert", "lane_update"} {
		t.Run(kind, func(t *testing.T) {
			d, s, h, r, scope, path := bindingFixture(t, "buy")
			bindingLegacyReferences(t, s)
			if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			b := bindingCommand(t, s, r)
			bindingPredecessor021(t, s)
			var query string
			switch kind {
			case "reservation_insert":
				query = `INSERT INTO execution_reservations(operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at)
 SELECT 'alias-insert',execution_step_id,wallet_id,'native','1','1','reserved',checked_at,checked_at FROM manual_execution_bindings`
			case "reservation_update":
				query = `UPDATE execution_reservations SET step_id=(SELECT execution_step_id FROM manual_execution_bindings),status='reserved' WHERE operation_id='alias-operation'`
			case "lane_insert":
				query = `INSERT INTO execution_wallet_lanes(wallet_id,address,state,operation_id,step_id,reserved_nonce,updated_at)
 SELECT 'fresh-alias-wallet','` + fmt.Sprintf("0x%040x", 1000) + `','reserved','alias-insert',execution_step_id,'7',checked_at FROM manual_execution_bindings`
				if _, err := s.db.Exec(`INSERT INTO dry_run_wallets VALUES('fresh-alias-wallet',4663,?,1)`, fmt.Sprintf("0x%040x", 1000)); err != nil {
					t.Fatal(err)
				}
			case "lane_update":
				query = `UPDATE execution_wallet_lanes SET step_id=(SELECT execution_step_id FROM manual_execution_bindings) WHERE wallet_id='alias-wallet'`
			}
			if _, err := s.db.Exec(query); err != nil {
				t.Fatal("predecessor negative control no longer reproduces", err)
			}
			if count(t, s.db, "manual_execution_binding_complete") != 1 {
				t.Fatal("expected former positive-only view to miss pollution")
			}
			_, s = bindingReopen(t, d, path)
			before := admittedDatabaseSnapshot(t, s.db)
			if count(t, s.db, "manual_execution_binding_complete") != 1 || count(t, s.db, "manual_execution_binding_integrity") != 0 {
				t.Fatal("022 did not retain/detect predecessor pollution")
			}
			stored, err := scanManualExecutionBinding(s.db.QueryRow(`SELECT ` + manualExecutionBindingColumns + ` FROM manual_execution_bindings`))
			if err != nil || stored != b {
				t.Fatal("migration changed original binding", err)
			}
			for i := 0; i < 2; i++ {
				v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
				if err == nil || v != (manualExecutionBindingResult{}) || inputFailureCode(err) != "EXECUTION_BINDING_INCOMPLETE" {
					t.Fatal("polluted history accepted", v, err)
				}
			}
			if _, err := admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID); err == nil {
				t.Fatal("polluted bound operation became fresh input")
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("corruption was repaired or usage/expiry changed")
			}
			if strings.HasPrefix(kind, "reservation") {
				rejectBindingReference(t, s, `UPDATE execution_reservations SET step_id='alias-insert-step' WHERE step_id=(SELECT execution_step_id FROM manual_execution_bindings)`)
				rejectBindingReference(t, s, `DELETE FROM execution_reservations WHERE step_id=(SELECT execution_step_id FROM manual_execution_bindings)`)
				rejectBindingReference(t, s, `INSERT OR REPLACE INTO execution_reservations SELECT operation_id,'alias-insert-step',wallet_id,input_asset,input_amount,gas_budget,status,nonce_consumed,gas_consumed,created_at,updated_at FROM execution_reservations WHERE step_id=(SELECT execution_step_id FROM manual_execution_bindings)`)
			} else {
				rejectBindingReference(t, s, `UPDATE execution_wallet_lanes SET step_id='alias-step' WHERE step_id=(SELECT execution_step_id FROM manual_execution_bindings) AND wallet_id!='`+h.identity.WalletID+`'`)
				rejectBindingReference(t, s, `DELETE FROM execution_wallet_lanes WHERE step_id=(SELECT execution_step_id FROM manual_execution_bindings) AND wallet_id!='`+h.identity.WalletID+`'`)
				rejectBindingReference(t, s, `INSERT OR REPLACE INTO execution_wallet_lanes SELECT wallet_id,address,state,operation_id,'alias-step',reserved_nonce,freeze_reason,updated_at FROM execution_wallet_lanes WHERE step_id=(SELECT execution_step_id FROM manual_execution_bindings) AND wallet_id!='`+h.identity.WalletID+`'`)
			}
		})
	}
}

func TestHTTPM2HardeningPrebindingValidationReferenceDeniesAtomicClaim(t *testing.T) {
	for _, kind := range []string{"reservation", "lane"} {
		t.Run(kind, func(t *testing.T) {
			_, s, h, r, scope, _ := bindingFixture(t, "buy")
			bindingLegacyReferences(t, s)
			b := bindingCommand(t, s, r)
			var query string
			if kind == "reservation" {
				query = `UPDATE execution_reservations SET step_id=? WHERE operation_id='alias-operation'`
			} else {
				query = `UPDATE execution_wallet_lanes SET step_id=? WHERE wallet_id='alias-wallet'`
			}
			// Before binding the legacy reference can exist; 022 must not adopt it.
			if _, err := s.db.Exec(query, b.ValidationStep); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			if v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err == nil || v != (manualExecutionBindingResult{}) {
				t.Fatal("preexisting reference adopted", v, err)
			}
			if err := insertBinding(s, b); err == nil {
				t.Fatal("direct SQL bypassed preexisting reference guard")
			}
			if admittedDatabaseSnapshot(t, s.db) != before || count(t, s.db, "manual_execution_bindings") != 0 {
				t.Fatal("claim denial wrote partial effects")
			}
		})
	}
}

func TestHTTPM2HardeningLegacyCompatibilityAndIdleClaim(t *testing.T) {
	_, s, h, r, scope, _ := bindingFixture(t, "sell_amount")
	bindingLegacyReferences(t, s)
	if _, err := s.db.Exec(`UPDATE execution_reservations SET gas_budget='2' WHERE operation_id='alias-operation'; UPDATE execution_wallet_lanes SET reserved_nonce='8' WHERE wallet_id='alias-wallet';
 INSERT INTO execution_wallet_lanes(wallet_id,address,state,updated_at) VALUES(?,?,'idle',?)`, h.identity.WalletID, h.identity.WalletAddress, testNow.Format(time.RFC3339Nano)); err != nil {
		t.Fatal("unrelated legacy work denied", err)
	}
	v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || v.Status != "BOUND_NOT_SIGNED_NOT_PERMISSION" || v.ExecutionAuthorized || v.SendAuthorized {
		t.Fatal("idle-lane claim denied", v, err)
	}
	if count(t, s.db, "manual_execution_binding_integrity") != 1 {
		t.Fatal("unrelated legacy work tainted valid binding")
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO execution_reservations SELECT operation_id,step_id,wallet_id,input_asset,input_amount,'3',status,nonce_consumed,gas_consumed,created_at,updated_at FROM execution_reservations WHERE operation_id='alias-operation';
 UPDATE execution_wallet_lanes SET reserved_nonce='9' WHERE wallet_id='alias-wallet'`); err != nil {
		t.Fatal("unrelated legacy lifecycle blocked after binding", err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	v, err = bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || !v.Duplicate || v.ExecutionAuthorized || v.SendAuthorized || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("duplicate mutated accounting or identity", v, err)
	}
}

func TestHTTPM2HardeningMissingIntegrityViewFailsClosed(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprint(duplicate), func(t *testing.T) {
			_, s, h, r, scope, _ := bindingFixture(t, "buy")
			if duplicate {
				if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(`DROP VIEW manual_execution_binding_integrity`); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || v != (manualExecutionBindingResult{}) {
				t.Fatal("missing schema granted binding/history", v, err)
			}
			requireOperational503(t, err)
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("missing integrity schema committed partial binding or repair")
			}
		})
	}
}
