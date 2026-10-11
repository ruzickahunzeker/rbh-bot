package tradeapi

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func bindingPredecessor025(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS manual_binding_lane_retained_delete;
 DROP TRIGGER IF EXISTS manual_binding_lane_retained_update;
 DELETE FROM schema_migrations WHERE version=26`); err != nil {
		t.Fatal(err)
	}
}

// Real 021 permits a case-variant legacy lane BEFORE the first binding.
// Inserting it after binding is not a reachable negative control: 021 rejects it.
// No landed integrity guard or FK is removed within the predecessor boundary.
func bindingRetainedLaneFixture(t *testing.T) (*storage.Database, *Store, *API, Record, manualAdmissionScope, string) {
	t.Helper()
	d, s, h, r, scope, path := bindingFixture(t, "buy")
	bindingPredecessor021(t, s)
	bindingLegacyReferences(t, s)
	alias := strings.ToUpper(h.identity.WalletAddress)
	if alias == h.identity.WalletAddress {
		t.Fatal("fixture must have a distinct BINARY case variant")
	}
	if _, err := s.db.Exec(`UPDATE execution_wallet_lanes SET address=? WHERE wallet_id='alias-wallet'`, alias); err != nil {
		t.Fatal(err)
	}
	if err := insertBinding(s, bindingCommand(t, s, r)); err != nil {
		t.Fatal("actual 021 pre-binding alias rejected", err)
	}
	if count(t, s.db, "manual_execution_binding_complete") != 1 {
		t.Fatal("021 positive view must accept original binding")
	}
	before := bindingPhysicalRows(t, s) + bindingVictimState(t, s) + bindingAuditRows(t, s)
	d, s = bindingReopen(t, d, path)
	if bindingPhysicalRows(t, s)+bindingVictimState(t, s)+bindingAuditRows(t, s) != before {
		t.Fatal("upgrade cleaned or changed retained history/expiry/usage")
	}
	if count(t, s.db, "manual_execution_binding_complete") != 1 || count(t, s.db, "manual_execution_binding_integrity") != 0 {
		t.Fatal("upgraded strict view must retain and detect address-only pollution")
	}
	assertRetainedLaneDuplicate(t, s, h, r, scope, false)
	return d, s, h, r, scope, path
}

var retainedLaneMutations = []string{
	`DELETE FROM execution_wallet_lanes WHERE wallet_id='alias-wallet'`,
	`UPDATE execution_wallet_lanes SET address='0x0000000000000000000000000000000000000888' WHERE wallet_id='alias-wallet'`,
	`UPDATE OR REPLACE execution_wallet_lanes SET address='0x0000000000000000000000000000000000000888' WHERE wallet_id='alias-wallet'`,
}

func assertRetainedLaneDuplicate(t *testing.T, s *Store, h *API, r Record, scope manualAdmissionScope, valid bool) {
	t.Helper()
	before := bindingAuditSnapshot(t, s)
	v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
	if valid {
		if err != nil || !v.Duplicate || v.ExecutionAuthorized || v.SendAuthorized || v.ExpiresAt != r.ExpiresAt {
			t.Fatal("historical duplicate became permission/drift", v, err)
		}
	} else if err == nil || inputFailureCode(err) != "EXECUTION_BINDING_INCOMPLETE" || v != (manualExecutionBindingResult{}) {
		t.Fatal("retained pollution accepted/repaired", v, err)
	}
	if bindingAuditSnapshot(t, s) != before {
		t.Fatal("duplicate changed retained rows, expiry or budget")
	}
	for _, table := range []string{"transaction_attempts", "transaction_submissions", "canary_send_permits", "canary_runtime_gate_snapshots"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("economic row", table)
		}
	}
	var financial int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM execution_reservations WHERE operation_id=(SELECT operation_id FROM manual_execution_bindings WHERE request_id=?)`, r.RequestID).Scan(&financial); err != nil || financial != 0 {
		t.Fatal("target acquired financial reservation", financial, err)
	}
	if _, err := admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID); err == nil {
		t.Fatal("bound history became M1 candidate")
	}
}

func TestHTTPM2RetainedLane025NegativeControl(t *testing.T) {
	for _, mode := range []string{"OFF", "ON"} {
		t.Run(mode, func(t *testing.T) {
			_, s, h, r, scope, _ := bindingRetainedLaneFixture(t)
			bindingPredecessor025(t, s)
			bindingRowidMode(t, s, mode)
			before := bindingAuditSnapshot(t, s)
			for _, query := range retainedLaneMutations {
				tx, err := s.db.BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(query); err != nil {
					tx.Rollback()
					t.Fatal("025 negative control did not reproduce", err)
				}
				var complete int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM manual_execution_binding_integrity`).Scan(&complete); err != nil || complete != 1 {
					tx.Rollback()
					t.Fatal("025 mutation failed to hide pollution", complete, err)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			if bindingAuditSnapshot(t, s) != before {
				t.Fatal("negative-control rollback lost retained history")
			}
			assertRetainedLaneDuplicate(t, s, h, r, scope, false)
		})
	}
}

func TestHTTPM2RetainedLane025CommittedRepairNegativeControl(t *testing.T) {
	for _, mode := range []string{"OFF", "ON"} {
		for i, query := range retainedLaneMutations[:2] {
			t.Run(fmt.Sprintf("%s/%d", mode, i), func(t *testing.T) {
				d, s, h, r, scope, path := bindingRetainedLaneFixture(t)
				bindingPredecessor025(t, s)
				bindingRowidMode(t, s, mode)
				if _, err := s.db.Exec(query); err != nil {
					t.Fatal("025 committed negative control did not reproduce", err)
				}
				// Demonstrate the actual writer accepting the silently repaired
				// history, not merely a positive view query inside a rolled-back tx.
				assertRetainedLaneDuplicate(t, s, h, r, scope, true)
				before := bindingPhysicalRows(t, s) + bindingVictimState(t, s) + bindingAuditRows(t, s)
				_, s = bindingReopen(t, d, path)
				assertRetainedLaneDuplicate(t, s, h, r, scope, true)
				if bindingPhysicalRows(t, s)+bindingVictimState(t, s)+bindingAuditRows(t, s) != before {
					t.Fatal("upgrade rewrote negative-control history")
				}
			})
		}
	}
}

func TestHTTPM2RetainedLaneMutationDenialAndReopen(t *testing.T) {
	for _, mode := range []string{"OFF", "ON"} {
		t.Run(mode, func(t *testing.T) {
			d, s, h, r, scope, path := bindingRetainedLaneFixture(t)
			bindingRowidMode(t, s, mode)
			before := bindingAuditSnapshot(t, s)
			for _, query := range retainedLaneMutations {
				rejectBindingReference(t, s, query)
			}
			// Even a no-op on polluted retained history cannot masquerade as repair.
			rejectBindingReference(t, s, `UPDATE execution_wallet_lanes SET address=address WHERE wallet_id='alias-wallet'`)
			_, s = bindingReopen(t, d, path)
			bindingRowidMode(t, s, mode)
			for _, query := range retainedLaneMutations {
				rejectBindingReference(t, s, query)
			}
			assertRetainedLaneDuplicate(t, s, h, r, scope, false)
			if bindingAuditSnapshot(t, s) != before {
				t.Fatal("reopen/denial changed expiry, lane, accounting, audit or history")
			}
		})
	}
}

func TestHTTPM2RetainedLaneTwoHandleDenial(t *testing.T) {
	d, s, h, r, scope, path := bindingRetainedLaneFixture(t)
	other, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	s2, err := NewStore(other)
	if err != nil {
		t.Fatal(err)
	}
	bindingRowidMode(t, s, "OFF")
	bindingRowidMode(t, s2, "ON")
	before := bindingAuditSnapshot(t, s)
	results := make(chan error, 12)
	for i := 0; i < 12; i++ {
		go func(i int) {
			store := s
			if i%2 != 0 {
				store = s2
			}
			_, err := store.db.Exec(retainedLaneMutations[i%len(retainedLaneMutations)])
			results <- err
		}(i)
	}
	for i := 0; i < 12; i++ {
		if err := <-results; err == nil || !strings.Contains(err.Error(), "manual retained lane") {
			t.Fatal("expected verified concurrent guard denial", err)
		}
	}
	if bindingAuditSnapshot(t, s) != before {
		t.Fatal("concurrent retarget/delete changed durable history")
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	_, s = bindingReopen(t, d, path)
	assertRetainedLaneDuplicate(t, s, h, r, scope, false)
}

func TestHTTPM2RetainedLaneMixedStatementRollback(t *testing.T) {
	_, s, _, _, _, _ := bindingRetainedLaneFixture(t)
	if _, err := s.db.Exec(`INSERT INTO dry_run_wallets VALUES('unrelated-wallet',4663,'0x0000000000000000000000000000000000000777',1);
 INSERT INTO execution_wallet_lanes(wallet_id,address,state,updated_at) VALUES('unrelated-wallet','0x0000000000000000000000000000000000000777','idle','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"OFF", "ON"} {
		bindingRowidMode(t, s, mode)
		// SQLite visits rowids in order; move the unrelated row before the alias.
		if _, err := s.db.Exec(`UPDATE execution_wallet_lanes SET rowid=-100 WHERE wallet_id='unrelated-wallet'`); err != nil {
			t.Fatal(err)
		}
		rejectBindingReference(t, s, `UPDATE execution_wallet_lanes SET updated_at='2026-02-01T00:00:00Z' WHERE wallet_id IN ('unrelated-wallet','alias-wallet')`)
		rejectBindingReference(t, s, `DELETE FROM execution_wallet_lanes WHERE wallet_id IN ('unrelated-wallet','alias-wallet')`)
	}
}

func TestHTTPM2RetainedLaneIdleClaimAndLegacyCompatibility(t *testing.T) {
	for _, mode := range []string{"OFF", "ON"} {
		t.Run(mode, func(t *testing.T) {
			_, s, h, r, scope, _ := bindingFixture(t, "buy")
			bindingRowidMode(t, s, mode)
			bindingLegacyReferences(t, s)
			b := bindingCommand(t, s, r)
			if _, err := s.db.Exec(`INSERT INTO execution_wallet_lanes(wallet_id,address,state,updated_at) VALUES(?,?,'idle',?)`, b.Wallet, b.Address, b.CheckedAt); err != nil {
				t.Fatal(err)
			}
			if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal("exact idle UPSERT denied", err)
			}
			if _, err := s.db.Exec(`UPDATE execution_wallet_lanes SET address='0x0000000000000000000000000000000000000888' WHERE wallet_id='alias-wallet'; DELETE FROM execution_wallet_lanes WHERE wallet_id='alias-wallet'`); err != nil {
				t.Fatal("unrelated legacy lifecycle denied", err)
			}
			assertRetainedLaneDuplicate(t, s, h, r, scope, true)
		})
	}
}

func TestHTTPM2RetainedLaneMigrationRollbackAndRetry(t *testing.T) {
	d, s, h, r, scope, _ := bindingRetainedLaneFixture(t)
	bindingPredecessor025(t, s)
	// Fail at the second new trigger, after the first has been executed.
	if _, err := s.db.Exec(`CREATE TRIGGER manual_binding_lane_retained_update BEFORE UPDATE ON execution_wallet_lanes BEGIN SELECT 1; END`); err != nil {
		t.Fatal(err)
	}
	before := bindingAuditSnapshot(t, s)
	if err := d.Migrate(context.Background()); err == nil {
		t.Fatal("migration conflict accepted")
	}
	if bindingAuditSnapshot(t, s) != before {
		t.Fatal("failed migration changed rows/journal")
	}
	var created int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='manual_binding_lane_retained_delete'`).Scan(&created); err != nil || created != 0 {
		t.Fatal("partial migration trigger committed", created, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER manual_binding_lane_retained_update`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := d.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range retainedLaneMutations {
		rejectBindingReference(t, s, query)
	}
	assertRetainedLaneDuplicate(t, s, h, r, scope, false)
	var journal int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=26`).Scan(&journal); err != nil || journal != 1 {
		t.Fatal(fmt.Sprint("026 journal count ", journal, " ", err))
	}
}
