package trade

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
)

func TestManualB1LegacyStoreCannotArmOrConsumeDeclaredAuthorization(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	r := canaryRequest("b1-compat", now)
	persistSource(t, s, r)
	a, err := s.CreateRuntimeAuthorization(context.Background(), runtimeAuthorization("b1-auth", now), now)
	if err != nil {
		t.Fatal(err)
	}
	var sourceHash string
	if err = s.db.QueryRow(`SELECT policy_hash FROM canary_policies WHERE version=1`).Scan(&sourceHash); err != nil {
		t.Fatal(err)
	}
	p := manualrisk.Policy{ID: "manual-compat", SourcePolicyVersion: 1, SourcePolicyHash: sourceHash, ChainID: 4663, Protocol: CanaryProtocol,
		InputLimits:              []manualrisk.InputLimit{{Unit: manualrisk.Unit{ChainID: 4663, Asset: "native", Decimals: 18, Kind: manualrisk.TradeInput}, MaxOperation: "100", MaxWalletActive: "200", MaxGlobalActive: "300"}},
		MaxNativeGasPerOperation: "10", MaxNativeWalletOutlay: "200", MaxNativeGlobalOutlay: "300", MinNativeBalance: "0", MaxSellBPS: 5000,
		MaxWalletOperationsWindow: 10, MaxGlobalOperationsWindow: 100, WindowSeconds: 3600}
	raw, hash, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	stamp := now.Format(time.RFC3339Nano)
	if _, err = s.db.Exec(`INSERT INTO manual_risk_policies VALUES(?,?,?,?,?,?)`, p.ID, 1, sourceHash, raw, hash, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO manual_authorization_units VALUES(?,?,?,?,'MANUAL',4663,'native',18,'TRADE_INPUT',?)`, a.ID, a.Epoch, p.ID, hash, stamp); err != nil {
		t.Fatal(err)
	}
	if err = s.TransitionRuntimeAuthorization(context.Background(), a.ID, "PENDING", "ARMED", now); err == nil {
		t.Fatal("legacy API armed partially implemented MANUAL authorization")
	}
	if _, err = s.ReserveRuntimeAuthorizationBudget(context.Background(), a.ID, r.OperationID, "1", now); !errors.Is(err, ErrCanaryRuntimeRejected) {
		t.Fatal("pending MANUAL budget consumed", err)
	}
	// Controlled corruption fixture: remove only the arming guard and force an
	// ARM transition, then prove the legacy budget API's INSERT still fails and
	// its entire transaction cannot leave usage/audit behind. Not real authority.
	for _, trigger := range []string{"manual_b1_no_arm", "manual_operator_upgrade_arm_guard"} {
		if _, err = s.db.Exec(`DROP TRIGGER ` + trigger); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.Exec(`UPDATE canary_runtime_authorizations SET state='ARMED' WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveRuntimeAuthorizationBudget(context.Background(), a.ID, r.OperationID, "1", now); err == nil {
		t.Fatal("legacy API bypassed MANUAL atomic-budget isolation")
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit`).Scan(&after); err != nil || before != after {
		t.Fatal("rejected budget wrote audit", err)
	}
	var count int
	var total string
	if err = s.db.QueryRow(`SELECT operation_count,total_input FROM canary_authorization_usage WHERE authorization_id=?`, a.ID).Scan(&count, &total); err != nil || count != 0 || total != "0" {
		t.Fatal("failed budget partially consumed usage", err)
	}
	for _, table := range []string{"canary_authorization_operation_usage", "canary_send_permits", "transaction_attempts", "transaction_submissions", "canary_risk_reservations"} {
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("B1 compatibility proof created economic work", table, count, err)
		}
	}
}
