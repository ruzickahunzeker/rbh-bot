package tradeapi

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

// Only disposable fixture objects from 023 are removed. Real 001..022 guards
// remain. Reopen/Migrate reinstalls the actual embedded 023 before assertions.
func bindingPredecessor022(t *testing.T, s *Store) {
	t.Helper()
	bindingPredecessor023(t, s)
	for _, name := range []string{"manual_binding_reservation_update_victim", "manual_binding_lane_update_victim", "manual_binding_reservation_insert_victim", "manual_binding_lane_insert_victim"} {
		if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS ` + name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version=23`); err != nil {
		t.Fatal(err)
	}
}

func bindingCleanVictimTarget(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO dry_run_wallets VALUES('clean-wallet',4663,'0x0000000000000000000000000000000000000998',1),('fresh-wallet',4663,'0x0000000000000000000000000000000000000997',1);
 INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at)
 VALUES('clean-operation',4663,'clean-wallet','clean-key','clean-fp','swap','prepared','2026-01-01T00:00:00Z'),('fresh-operation',4663,'fresh-wallet','fresh-key','fresh-fp','swap','prepared','2026-01-01T00:00:00Z');
 INSERT INTO execution_steps(id,operation_id,step_index,kind,status) VALUES('clean-step','clean-operation',0,'curve_swap','prepared'),('fresh-step','fresh-operation',0,'curve_swap','prepared');
 INSERT INTO execution_reservations(operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at)
 VALUES('clean-operation','clean-step','clean-wallet','native','2','3','released','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
 INSERT INTO execution_wallet_lanes(wallet_id,address,state,operation_id,step_id,reserved_nonce,updated_at)
 VALUES('clean-wallet','0x0000000000000000000000000000000000000998','reserved','clean-operation','clean-step','8','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
}

func bindingVictimMutation(kind string) string {
	switch kind {
	case "reservation_operation":
		return `UPDATE OR REPLACE execution_reservations SET operation_id='alias-insert' WHERE operation_id='clean-operation'`
	case "lane_wallet":
		return `UPDATE OR REPLACE execution_wallet_lanes SET wallet_id='alias-wallet' WHERE wallet_id='clean-wallet'`
	case "lane_address":
		return `UPDATE OR REPLACE execution_wallet_lanes SET address=(SELECT address FROM execution_wallet_lanes WHERE wallet_id='alias-wallet') WHERE wallet_id='clean-wallet'`
	case "reservation_rowid":
		return `UPDATE OR REPLACE execution_reservations SET rowid=(SELECT rowid FROM execution_reservations WHERE operation_id='alias-insert') WHERE operation_id='clean-operation'`
	case "lane_rowid":
		return `UPDATE OR REPLACE execution_wallet_lanes SET rowid=(SELECT rowid FROM execution_wallet_lanes WHERE wallet_id='alias-wallet') WHERE wallet_id='clean-wallet'`
	case "reservation_insert_rowid":
		return `INSERT OR REPLACE INTO execution_reservations(rowid,operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at)
 SELECT rowid,'fresh-operation','fresh-step','fresh-wallet','native','1','1','released',created_at,updated_at FROM execution_reservations WHERE operation_id='alias-insert'`
	case "lane_insert_rowid":
		return `INSERT OR REPLACE INTO execution_wallet_lanes(rowid,wallet_id,address,state,operation_id,step_id,reserved_nonce,updated_at)
 SELECT rowid,'fresh-wallet','0x0000000000000000000000000000000000000997','reserved','fresh-operation','fresh-step','7',updated_at FROM execution_wallet_lanes WHERE wallet_id='alias-wallet'`
	default:
		panic("unknown test mutation")
	}
}

func bindingVictimFixture(t *testing.T, kind string) (*storage.Database, *Store, *API, Record, manualAdmissionScope, string) {
	t.Helper()
	d, s, h, r, scope, path := bindingFixture(t, "buy")
	bindingLegacyReferences(t, s)
	bindingCleanVictimTarget(t, s)
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	bindingPredecessor021(t, s)
	var query string
	if strings.HasPrefix(kind, "reservation") {
		query = `INSERT INTO execution_reservations(operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at)
 SELECT 'alias-insert',execution_step_id,'alias-wallet','native','1','1','reserved',checked_at,checked_at FROM manual_execution_bindings`
	} else {
		query = `UPDATE execution_wallet_lanes SET step_id=(SELECT execution_step_id FROM manual_execution_bindings) WHERE wallet_id='alias-wallet'`
	}
	if _, err := s.db.Exec(query); err != nil {
		t.Fatal("021 pollution baseline", err)
	}
	d, s = bindingReopen(t, d, path)
	return d, s, h, r, scope, path
}

func bindingVictimState(t *testing.T, s *Store) string {
	t.Helper()
	var state string
	if err := s.db.QueryRow(`SELECT json_object('binding',(SELECT json_group_array(json_object('op',operation_id,'step',execution_step_id,'expiry',expires_at,'checked',checked_at)) FROM manual_execution_bindings),
 'usage',(SELECT json_group_array(json_object('auth',authorization_id,'count',operation_count,'amount',total_input)) FROM canary_authorization_usage),
 'financial',(SELECT json_group_array(json_object('rowid',rowid,'op',operation_id,'step',step_id,'status',status,'amount',input_amount,'gas',gas_budget)) FROM execution_reservations),
 'lanes',(SELECT json_group_array(json_object('rowid',rowid,'wallet',wallet_id,'address',address,'op',operation_id,'step',step_id,'nonce',reserved_nonce,'state',state)) FROM execution_wallet_lanes))`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertBindingVictimRejected(t *testing.T, s *Store, h *API, r Record, scope manualAdmissionScope) {
	t.Helper()
	if count(t, s.db, "manual_execution_binding_complete") != 1 || count(t, s.db, "manual_execution_binding_integrity") != 0 {
		t.Fatal("pollution disappeared or strict verification accepted")
	}
	var fk, guards int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatal("foreign keys off", fk, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name IN ('manual_binding_reservation_update_victim','manual_binding_lane_update_victim','manual_binding_reservation_insert_victim','manual_binding_lane_insert_victim')`).Scan(&guards); err != nil || guards != 4 {
		t.Fatal("023 guard absent", guards, err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
	if err == nil || inputFailureCode(err) != "EXECUTION_BINDING_INCOMPLETE" || v != (manualExecutionBindingResult{}) {
		t.Fatal("polluted history became permission or success", v, err)
	}
	if _, err := admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID); err == nil {
		t.Fatal("polluted history became M1 candidate")
	}
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("verification repaired or consumed budget")
	}
	for _, table := range []string{"transaction_attempts", "transaction_submissions", "canary_send_permits", "canary_runtime_gate_snapshots"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("economic artifact created", table)
		}
	}
}

func TestHTTPM2ResidualUpdateConflictVictimsRetainedAcrossRestart(t *testing.T) {
	for _, kind := range []string{"reservation_operation", "lane_wallet", "lane_address", "reservation_rowid", "lane_rowid", "reservation_insert_rowid", "lane_insert_rowid"} {
		t.Run(kind, func(t *testing.T) {
			d, s, h, r, scope, path := bindingVictimFixture(t, kind)
			for _, recursive := range []int{0, 1} {
				if _, err := s.db.Exec(fmt.Sprintf("PRAGMA recursive_triggers=%d", recursive)); err != nil {
					t.Fatal(err)
				}
				var actual int
				if err := s.db.QueryRow(`PRAGMA recursive_triggers`).Scan(&actual); err != nil || actual != recursive {
					t.Fatal(actual, err)
				}
				assertBindingVictimRejected(t, s, h, r, scope)
				rejectBindingReference(t, s, bindingVictimMutation(kind))
				assertBindingVictimRejected(t, s, h, r, scope)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			_, s = bindingReopen(t, d, path)
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("restart dropped pollution/nonce/expiry/budget")
			}
			assertBindingVictimRejected(t, s, h, r, scope)
			rejectBindingReference(t, s, bindingVictimMutation(kind))
		})
	}
}

func TestHTTPM2Residual022NegativeControlAnd023Upgrade(t *testing.T) {
	for _, kind := range []string{"reservation_operation", "lane_wallet", "lane_address", "reservation_rowid", "lane_rowid", "reservation_insert_rowid", "lane_insert_rowid"} {
		t.Run(kind, func(t *testing.T) {
			d, s, h, r, scope, path := bindingVictimFixture(t, kind)
			bindingPredecessor022(t, s)
			if _, err := s.db.Exec(`PRAGMA recursive_triggers=OFF`); err != nil {
				t.Fatal(err)
			}
			before := bindingVictimState(t, s)
			// Roll back this predecessor reproduction, then preserve that pollution
			// through an actual close/reopen/023 upgrade. No production repair.
			conn, err := s.db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(context.Background(), bindingVictimMutation(kind)); err != nil {
				t.Fatal("022 defect negative control no longer reproduces", err)
			}
			var valid int
			if err := conn.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM manual_execution_binding_integrity`).Scan(&valid); err != nil || valid != 1 {
				t.Fatal("022 failed to reproduce hidden victim", valid, err)
			}
			if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			if bindingVictimState(t, s) != before {
				t.Fatal("negative control failed to restore retained work")
			}
			_, s = bindingReopen(t, d, path)
			if bindingVictimState(t, s) != before {
				t.Fatal("023 migration repaired/dropped retained work")
			}
			assertBindingVictimRejected(t, s, h, r, scope)
			rejectBindingReference(t, s, bindingVictimMutation(kind))
		})
	}
}

func TestHTTPM2ResidualCanonicalLaneConflictVictim(t *testing.T) {
	_, s, h, r, scope, _ := bindingFixture(t, "buy")
	bindingLegacyReferences(t, s)
	bindingCleanVictimTarget(t, s)
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	for _, assignment := range []string{
		`wallet_id=(SELECT wallet_id FROM manual_execution_bindings)`,
		`address=(SELECT wallet_address FROM manual_execution_bindings)`,
		`rowid=(SELECT l.rowid FROM execution_wallet_lanes l JOIN manual_execution_bindings b ON b.execution_step_id=l.step_id)`,
	} {
		rejectBindingReference(t, s, `UPDATE OR REPLACE execution_wallet_lanes SET `+assignment+` WHERE wallet_id='clean-wallet'`)
	}
	rejectBindingReference(t, s, `INSERT OR REPLACE INTO execution_wallet_lanes(rowid,wallet_id,address,state,operation_id,step_id,reserved_nonce,updated_at)
 SELECT l.rowid,'clean-wallet','0x0000000000000000000000000000000000000998','reserved','clean-operation','clean-step','8',l.updated_at FROM execution_wallet_lanes l JOIN manual_execution_bindings b ON b.execution_step_id=l.step_id`)
	if count(t, s.db, "manual_execution_binding_integrity") != 1 {
		t.Fatal("canonical lane lost")
	}
}

func TestHTTPM2ResidualLegacySelfUpdateAndReplaceCompatibility(t *testing.T) {
	_, s, h, r, scope, _ := bindingFixture(t, "sell_amount")
	bindingLegacyReferences(t, s)
	bindingCleanVictimTarget(t, s)
	if _, err := s.db.Exec(`INSERT INTO execution_wallet_lanes(wallet_id,address,state,updated_at) VALUES(?,?,'idle','2026-01-01T00:00:00Z')`, h.identity.WalletID, h.identity.WalletAddress); err != nil {
		t.Fatal(err)
	}
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal("idle UPSERT falsely considered a victim", err)
	}
	for _, recursive := range []int{0, 1} {
		if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA recursive_triggers=%d`, recursive)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE OR REPLACE execution_reservations SET operation_id=operation_id,step_id=step_id,rowid=rowid,gas_budget='3' WHERE operation_id='clean-operation';
 UPDATE OR REPLACE execution_wallet_lanes SET wallet_id=wallet_id,address=address,rowid=rowid,reserved_nonce='9' WHERE wallet_id='clean-wallet'`); err != nil {
			t.Fatal("unrelated self update blocked", err)
		}
	}
	if _, err := s.db.Exec(`UPDATE OR REPLACE execution_reservations SET operation_id='alias-operation' WHERE operation_id='clean-operation';
 UPDATE OR REPLACE execution_wallet_lanes SET address=(SELECT address FROM execution_wallet_lanes WHERE wallet_id='alias-wallet') WHERE wallet_id='clean-wallet'`); err != nil {
		t.Fatal("unrelated legacy conflict victim blocked", err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || !v.Duplicate || v.ExecutionAuthorized || v.SendAuthorized || admittedDatabaseSnapshot(t, s.db) != before || count(t, s.db, "manual_execution_binding_integrity") != 1 {
		t.Fatal("legacy replace changed MANUAL identity/budget", v, err)
	}
}

func TestHTTPM2ResidualSecondHandleConflictRejectsWithoutConsumption(t *testing.T) {
	d, s, h, r, scope, path := bindingVictimFixture(t, "reservation_operation")
	second, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(second)
	if err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			store := s
			if i%2 != 0 {
				store = s2
			}
			_, err := store.db.Exec(bindingVictimMutation("reservation_operation"))
			results <- err
		}(i)
	}
	for i := 0; i < 8; i++ {
		if err := <-results; err == nil || !strings.Contains(err.Error(), "manual reservation update conflict victim protected") {
			t.Fatal("concurrent victim write not verified rejection", err)
		}
	}
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("concurrent victim writes consumed or cleaned work")
	}
	assertBindingVictimRejected(t, s2, h, r, scope)
	second.Close()
	_, s = bindingReopen(t, d, path)
	assertBindingVictimRejected(t, s, h, r, scope)
}

func TestHTTPM2ResidualMultirowUpdateAbortsAllEffects(t *testing.T) {
	_, s, h, r, scope, _ := bindingVictimFixture(t, "reservation_operation")
	rejectBindingReference(t, s, `UPDATE OR REPLACE execution_reservations SET gas_budget='4',
 operation_id=CASE WHEN operation_id='clean-operation' THEN 'alias-insert' ELSE operation_id END
 WHERE operation_id IN ('alias-operation','clean-operation')`)
	assertBindingVictimRejected(t, s, h, r, scope)
}

func TestHTTPM2ResidualIdleLaneWithExplicitNegativeRowidClaim(t *testing.T) {
	_, s, h, r, scope, _ := bindingFixture(t, "buy")
	bindingCleanVictimTarget(t, s)
	if _, err := s.db.Exec(`INSERT INTO execution_wallet_lanes(rowid,wallet_id,address,state,updated_at) VALUES(-1,?,?,'idle','2026-01-01T00:00:00Z')`, h.identity.WalletID, h.identity.WalletAddress); err != nil {
		t.Fatal(err)
	}
	v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || v.ExecutionAuthorized || v.SendAuthorized || count(t, s.db, "manual_execution_binding_integrity") != 1 {
		t.Fatal("exact idle claim lost to implicit rowid sentinel", v, err)
	}
	// SQLite reports an unspecified NEW.rowid as -1 in BEFORE INSERT. If an
	// explicitly negative protected row exists, conservatively reject that
	// unprovable insertion; an explicit nonconflicting positive rowid is valid.
	rejectBindingReference(t, s, `INSERT INTO execution_wallet_lanes(wallet_id,address,state,updated_at)
 VALUES('fresh-wallet','0x0000000000000000000000000000000000000997','idle','2026-01-01T00:00:00Z')`)
	if _, err := s.db.Exec(`INSERT INTO execution_wallet_lanes(rowid,wallet_id,address,state,updated_at)
 VALUES(100,'fresh-wallet','0x0000000000000000000000000000000000000997','idle','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal("nonconflicting explicit legacy rowid denied", err)
	}
}
