package tradeapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

// Only the new guard/journal is removed in disposable predecessor fixtures.
// The existing helper's no-arm removal is local fixture authorization only;
// none of the M2 integrity guards or foreign keys is disabled.
func bindingPredecessor024(t *testing.T, s *Store) {
	t.Helper()
	bindingPredecessor025(t, s)
	if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS manual_binding_insert_conflict_victim; DELETE FROM schema_migrations WHERE version=25`); err != nil {
		t.Fatal(err)
	}
}

func bindingSecondWalletAdmitted(t *testing.T, s *Store, h *API, scope manualAdmissionScope) (*API, Record, manualAdmissionScope) {
	t.Helper()
	const address = "0x1000000000000000000000000000000000000099"
	stamp := testNow.Format(time.RFC3339Nano)
	id := Identity{PrincipalID: h.identity.PrincipalID, WalletID: "rowid-wallet-02", WalletAddress: address}
	if _, err := s.db.Exec(`INSERT INTO dry_run_wallets VALUES(?,4663,?,1)`, id.WalletID, address); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO canary_wallet_allowlist VALUES(?,?,4663,1,1,?)`, id.WalletID, address, stamp); err != nil {
		t.Fatal(err)
	}
	api, err := NewWithOptions(s, id, testCredential, h.policy, nil, Options{ManualIntake: true, ValidationBackend: &validationBackend{wallet: address}, ValidationPolicyVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	api.now, api.validator.now = h.now, h.now
	sc := scope
	sc.AuthorizationID, sc.AuthorizationEpoch = "rowid-auth-02", 2
	var policyHash string
	if err = s.db.QueryRow(`SELECT policy_hash FROM manual_risk_policies WHERE id=?`, scope.ManualPolicyID).Scan(&policyHash); err != nil {
		t.Fatal(err)
	}
	gateHash, err := manualGateEvidenceHash(context.Background(), s.db, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO canary_runtime_authorizations(id,epoch,mode,environment,chain_id,policy_version,wallet_id,build_sha,release_id,deployment_id,gate_evidence_set_hash,authorization_ref,max_operations,max_total_input,state,authorized_at,expires_at,created_at,updated_at)
 VALUES(?,?,'CONTROLLED_CANARY',?,4663,1,?,?,?,?,?,'local-fixture-not-real-grant',10,'1000000000000000000','PENDING',?,?,?,?)`, sc.AuthorizationID, sc.AuthorizationEpoch, sc.Environment, id.WalletID, sc.BuildSHA, sc.ReleaseID, sc.DeploymentID, gateHash, stamp, testNow.Add(time.Hour).Format(time.RFC3339Nano), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO canary_authorization_usage VALUES(?,0,'0',?)`, sc.AuthorizationID, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO manual_authorization_units VALUES(?,?,?,?,'MANUAL',4663,'native',18,'TRADE_INPUT',?)`, sc.AuthorizationID, sc.AuthorizationEpoch, sc.ManualPolicyID, policyHash, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE canary_runtime_authorizations SET state='ARMED' WHERE id=?`, sc.AuthorizationID); err != nil {
		t.Fatal(err)
	}
	r := exposureOrder(t, api, "rowid-second", "buy")
	if _, err = atomicWriter(s).admit(context.Background(), api.identity, r.RequestID, sc); err != nil {
		t.Fatal(err)
	}
	return api, r, sc
}

func bindingPhysicalRows(t *testing.T, s *Store) string {
	t.Helper()
	var rows string
	if err := s.db.QueryRow(`SELECT json_group_array(json_object('rowid',rowid,'op',operation_id,'request',request_id,'step',execution_step_id,'expiry',expires_at,'checked',checked_at)) FROM manual_execution_bindings`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func bindingRowidInsert(s *Store, b manualExecutionBinding, rowid int64) error {
	args := append([]any{rowid}, b.arguments()...)
	_, err := s.db.Exec(`INSERT OR REPLACE INTO manual_execution_bindings(rowid,`+manualExecutionBindingColumns+`) VALUES(?`+strings.Repeat(",?", 22)+`)`, args...)
	return err
}

func bindingRowid(t *testing.T, s *Store, r Record) int64 {
	t.Helper()
	var id int64
	if err := s.db.QueryRow(`SELECT rowid FROM manual_execution_bindings WHERE request_id=?`, r.RequestID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func bindingRowidMode(t *testing.T, s *Store, mode string) {
	t.Helper()
	if _, err := s.db.Exec(`PRAGMA recursive_triggers=` + mode); err != nil {
		t.Fatal(err)
	}
	var fk int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatal("foreign keys not enabled", fk, err)
	}
}

func TestHTTPM2BindingRowidConflictRetainsHistory(t *testing.T) {
	for _, mode := range []string{"OFF", "ON"} {
		t.Run(mode, func(t *testing.T) {
			d, s, h, r, scope, path := bindingFixture(t, "buy")
			if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			api, second, sc := bindingSecondWalletAdmitted(t, s, h, scope)
			b := bindingCommand(t, s, second)
			bindingRowidMode(t, s, mode)
			before := bindingPhysicalRows(t, s) + bindingAuditSnapshot(t, s)
			if err := bindingRowidInsert(s, b, bindingRowid(t, s, r)); err == nil || !strings.Contains(err.Error(), "manual binding insert conflict victim protected") {
				t.Fatal("rowid victim not explicitly protected", err)
			}
			if bindingPhysicalRows(t, s)+bindingAuditSnapshot(t, s) != before {
				t.Fatal("rejected insert changed history, TTL or usage")
			}
			assertAuditDuplicate(t, s, h, r, scope, true)
			if _, err := bindingWriter(s).bind(context.Background(), api.identity, second.RequestID, sc); err != nil {
				t.Fatal("nonconflicting first bind rejected", err)
			}
			before = bindingPhysicalRows(t, s) + bindingAuditSnapshot(t, s)
			_, s = bindingReopen(t, d, path)
			assertAuditDuplicate(t, s, h, r, scope, true)
			assertAuditDuplicate(t, s, api, second, sc, true)
			if bindingPhysicalRows(t, s)+bindingAuditSnapshot(t, s) != before {
				t.Fatal("restart/duplicates changed complete history")
			}
		})
	}
}

func TestHTTPM2BindingRowid024NegativeControlAndUpgrade(t *testing.T) {
	for _, mode := range []string{"OFF", "ON"} {
		t.Run(mode, func(t *testing.T) {
			d, s, h, r, scope, path := bindingFixture(t, "buy")
			if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			api, second, sc := bindingSecondWalletAdmitted(t, s, h, scope)
			bindingPredecessor024(t, s)
			bindingRowidMode(t, s, mode)
			before := bindingPhysicalRows(t, s) + bindingAuditSnapshot(t, s)
			err := bindingRowidInsert(s, bindingCommand(t, s, second), bindingRowid(t, s, r))
			if mode == "ON" {
				if err == nil || !strings.Contains(err.Error(), "manual execution binding immutable") || bindingPhysicalRows(t, s)+bindingAuditSnapshot(t, s) != before {
					t.Fatal("predecessor ON control did not reject atomically", err)
				}
				return
			}
			if err != nil || count(t, s.db, "manual_execution_bindings") != 1 || count(t, s.db, "manual_execution_binding_integrity") != 1 {
				t.Fatal("024 negative control did not reproduce", err)
			}
			before = bindingPhysicalRows(t, s) + bindingAuditRows(t, s) + bindingVictimState(t, s)
			_, s = bindingReopen(t, d, path)
			if bindingPhysicalRows(t, s)+bindingAuditRows(t, s)+bindingVictimState(t, s) != before {
				t.Fatal("upgrade repaired retained history")
			}
			before = bindingPhysicalRows(t, s) + bindingAuditSnapshot(t, s)
			v, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || v != (manualExecutionBindingResult{}) {
				t.Fatal("lost binding automatically reconstructed", v, err)
			}
			assertAuditDuplicate(t, s, api, second, sc, true)
			if bindingPhysicalRows(t, s)+bindingAuditSnapshot(t, s) != before {
				t.Fatal("duplicate repaired or recharged retained history")
			}
		})
	}
}

func TestHTTPM2BindingRowidTwoHandleConflictNoConsumption(t *testing.T) {
	d, s, h, r, scope, path := bindingFixture(t, "buy")
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	api, second, sc := bindingSecondWalletAdmitted(t, s, h, scope)
	b, rowid := bindingCommand(t, s, second), bindingRowid(t, s, r)
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
	bindingRowidMode(t, s2, "OFF")
	before := bindingPhysicalRows(t, s) + bindingAuditSnapshot(t, s)
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			store := s
			if i%2 != 0 {
				store = s2
			}
			results <- bindingRowidInsert(store, b, rowid)
		}(i)
	}
	for i := 0; i < 8; i++ {
		if err := <-results; err == nil || !strings.Contains(err.Error(), "manual binding insert conflict victim protected") {
			t.Fatal("not a verified concurrent guard denial", err)
		}
	}
	if bindingPhysicalRows(t, s)+bindingAuditSnapshot(t, s) != before {
		t.Fatal("concurrent conflicts changed durable history")
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	_, s = bindingReopen(t, d, path)
	assertAuditDuplicate(t, s, h, r, scope, true)
	if _, err := bindingWriter(s).bind(context.Background(), api.identity, second.RequestID, sc); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPM2BindingRowidMixedStatementRollback(t *testing.T) {
	_, s, h, r, scope, _ := bindingFixture(t, "buy")
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	_, second, _ := bindingSecondWalletAdmitted(t, s, h, scope)
	b := bindingCommand(t, s, second)
	before := bindingPhysicalRows(t, s) + bindingAuditSnapshot(t, s)
	args := append([]any{int64(100)}, b.arguments()...)
	args = append(args, bindingRowid(t, s, r))
	args = append(args, b.arguments()...)
	values := `(?` + strings.Repeat(",?", 22) + `)`
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO manual_execution_bindings(rowid,`+manualExecutionBindingColumns+`) VALUES`+values+`,`+values, args...); err == nil || !strings.Contains(err.Error(), "manual") {
		t.Fatal("mixed statement did not reject", err)
	}
	if bindingPhysicalRows(t, s)+bindingAuditSnapshot(t, s) != before {
		t.Fatal("mixed statement left first-row binding effects")
	}
}

func TestHTTPM2BindingRowidNegativeSentinelExplicitInsert(t *testing.T) {
	_, s, h, r, scope, _ := bindingFixture(t, "buy")
	if err := bindingRowidInsert(s, bindingCommand(t, s, r), -1); err != nil {
		t.Fatal(err)
	}
	api, second, sc := bindingSecondWalletAdmitted(t, s, h, scope)
	before := bindingPhysicalRows(t, s) + bindingAuditSnapshot(t, s)
	v, err := bindingWriter(s).bind(context.Background(), api.identity, second.RequestID, sc)
	if err == nil || !strings.Contains(err.Error(), "manual binding insert conflict victim protected") || v != (manualExecutionBindingResult{}) {
		t.Fatal("sentinel ambiguity did not fail closed", v, err)
	}
	if bindingPhysicalRows(t, s)+bindingAuditSnapshot(t, s) != before {
		t.Fatal("sentinel denial changed durable effects")
	}
	if err := bindingRowidInsert(s, bindingCommand(t, s, second), 100); err != nil {
		t.Fatal("explicit nonconflicting rowid unavailable", err)
	}
	assertAuditDuplicate(t, s, h, r, scope, true)
	assertAuditDuplicate(t, s, api, second, sc, true)
}

func TestHTTPM2BindingRowidMigrationRollbackAndRetry(t *testing.T) {
	d, s, h, r, scope, _ := bindingFixture(t, "buy")
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	bindingPredecessor024(t, s)
	if _, err := s.db.Exec(`CREATE TRIGGER manual_binding_insert_conflict_victim BEFORE INSERT ON manual_execution_bindings BEGIN SELECT 1; END`); err != nil {
		t.Fatal(err)
	}
	before := bindingPhysicalRows(t, s) + bindingAuditSnapshot(t, s)
	if err := d.Migrate(context.Background()); err == nil {
		t.Fatal("migration conflict accepted")
	}
	if bindingPhysicalRows(t, s)+bindingAuditSnapshot(t, s) != before {
		t.Fatal("failed migration changed data/journal")
	}
	if _, err := s.db.Exec(`DROP TRIGGER manual_binding_insert_conflict_victim`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := d.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	assertAuditDuplicate(t, s, h, r, scope, true)
}
