package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

type manualB24ConsumeFixture struct {
	d      *storage.Database
	s      *Store
	trade  *trade.Store
	r      trade.CanaryGateRequest
	permit manualFirstBroadcastPermit
	raw    string
	path   string
}

func manualB24Fixture(t *testing.T) manualB24ConsumeFixture {
	t.Helper()
	return manualB24FixtureForSide(t, "buy")
}

func manualB24FixtureForSide(t *testing.T, side string) manualB24ConsumeFixture {
	t.Helper()
	d, s, path, v, id, request, _, _ := runtimeVerifierFixture(t, side)
	if _, err := v.capture(context.Background(), id, request); err != nil {
		t.Fatal(err)
	}
	var attempt, raw, txHash string
	if err := s.db.QueryRow(`SELECT q.attempt_id,json_extract(m.envelope_json,'$.raw_sha256'),json_extract(m.envelope_json,'$.tx_hash') FROM manual_signed_artifact_commands m JOIN manual_nonce_claims q ON q.operation_id=m.operation_id`).Scan(&attempt, &raw, &txHash); err != nil {
		t.Fatal(err)
	}
	permit, err := (&manualFirstBroadcastIssuer{verifier: v}).issue(context.Background(), id, request, attempt, raw, txHash)
	if err != nil {
		t.Fatal(err)
	}
	store, err := trade.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	r := trade.CanaryGateRequest{OperationID: permit.OperationID, AttemptID: attempt, AuthorizationID: permit.AuthorizationID, AuthorizationEpoch: permit.AuthorizationEpoch, WalletID: id.WalletID, ChainID: trade.ChainID}
	if err := s.db.QueryRow(`SELECT policy_version,environment,build_sha,release_id,deployment_id FROM manual_execution_bindings WHERE operation_id=?`, permit.OperationID).Scan(&r.PolicyVersion, &r.Deployment.Environment, &r.Deployment.BuildSHA, &r.Deployment.ReleaseID, &r.Deployment.DeploymentID); err != nil {
		t.Fatal(err)
	}
	return manualB24ConsumeFixture{d: d, s: s, trade: store, r: r, permit: permit, raw: raw, path: path}
}

func manualB24EconomicSnapshot(t *testing.T, s *Store) string {
	t.Helper()
	var snapshot []string
	for _, table := range []string{"operations", "execution_steps", "transaction_attempts", "transaction_submissions", "receipt_observations", "position_effects", "execution_wallet_lanes", "execution_reservations", "canary_runtime_authorizations", "canary_authorization_usage", "canary_authorization_operation_usage", "canary_risk_reservations", "manual_reservation_units", "manual_signed_artifact_commands", "manual_signing_intents", "manual_nonce_claims", "canary_runtime_gate_snapshots"} {
		rows, err := s.db.Query(`SELECT * FROM ` + table + ` ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values, targets := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			snapshot = append(snapshot, table+":"+encode(values))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return encode(snapshot)
}

func TestHTTPM4B24PrivateFirstPermitConsumesOnceWithoutSubmission(t *testing.T) {
	d, s, _, v, id, request, _, _ := runtimeVerifierFixture(t, "buy")
	if _, err := v.capture(context.Background(), id, request); err != nil {
		t.Fatal(err)
	}
	var attempt, raw, txHash string
	if err := s.db.QueryRow(`SELECT q.attempt_id,json_extract(m.envelope_json,'$.raw_sha256'),json_extract(m.envelope_json,'$.tx_hash') FROM manual_signed_artifact_commands m JOIN manual_nonce_claims q ON q.operation_id=m.operation_id`).Scan(&attempt, &raw, &txHash); err != nil {
		t.Fatal(err)
	}
	permit, err := (&manualFirstBroadcastIssuer{verifier: v}).issue(context.Background(), id, request, attempt, raw, txHash)
	if err != nil {
		t.Fatal(err)
	}
	store, err := trade.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	r := trade.CanaryGateRequest{OperationID: permit.OperationID, AttemptID: attempt, AuthorizationID: permit.AuthorizationID, AuthorizationEpoch: permit.AuthorizationEpoch, WalletID: id.WalletID, ChainID: trade.ChainID}
	if err := s.db.QueryRow(`SELECT policy_version,environment,build_sha,release_id,deployment_id FROM manual_execution_bindings WHERE operation_id=?`, permit.OperationID).Scan(&r.PolicyVersion, &r.Deployment.Environment, &r.Deployment.BuildSHA, &r.Deployment.ReleaseID, &r.Deployment.DeploymentID); err != nil {
		t.Fatal(err)
	}
	now := permit.IssuedAt.Add(time.Nanosecond)
	if _, err := s.db.Exec(`UPDATE canary_send_permits SET state='CONSUMED',consumed_at=? WHERE id=?`, now.Format(time.RFC3339Nano), permit.ID); err == nil {
		t.Fatal("direct SQL consumed without transaction witness")
	}
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `INSERT INTO manual_first_broadcast_consume_scopes(permit_id,operation_id,attempt_id,authorization_id,authorization_epoch,artifact_hash,opened_at) VALUES(?,?,?,?,?,?,?)`, permit.ID, permit.OperationID, attempt, permit.AuthorizationID, permit.AuthorizationEpoch, raw, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `COMMIT`); err == nil {
		t.Fatal("orphan consume scope committed for later adoption")
	}
	if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if reason, err := store.ConsumeManualFirstBroadcastPermit(context.Background(), r, permit.ID, raw, now); err != nil {
		t.Fatalf("private consume rejected: %s: %v", reason, err)
	}
	if reason, err := store.ConsumeManualFirstBroadcastPermit(context.Background(), r, permit.ID, raw, now); err == nil || reason != "PERMIT_IDENTITY_MISMATCH" {
		t.Fatalf("duplicate consume accepted: %s: %v", reason, err)
	}
	var consumed, audits, submissions int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM canary_send_permits WHERE id=? AND state='CONSUMED'),(SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='MANUAL_FIRST_BROADCAST_CONSUMED'),(SELECT COUNT(*) FROM transaction_submissions)`, permit.ID).Scan(&consumed, &audits, &submissions); err != nil || consumed != 1 || audits != 1 || submissions != 0 {
		t.Fatal("unexpected durable consume result", consumed, audits, submissions, err)
	}
}

func TestHTTPM4B24ConsumeAuditFailureRollsBack(t *testing.T) {
	d, s, _, v, id, request, _, _ := runtimeVerifierFixture(t, "buy")
	if _, err := v.capture(context.Background(), id, request); err != nil {
		t.Fatal(err)
	}
	var attempt, raw, txHash string
	if err := s.db.QueryRow(`SELECT q.attempt_id,json_extract(m.envelope_json,'$.raw_sha256'),json_extract(m.envelope_json,'$.tx_hash') FROM manual_signed_artifact_commands m JOIN manual_nonce_claims q ON q.operation_id=m.operation_id`).Scan(&attempt, &raw, &txHash); err != nil {
		t.Fatal(err)
	}
	permit, err := (&manualFirstBroadcastIssuer{verifier: v}).issue(context.Background(), id, request, attempt, raw, txHash)
	if err != nil {
		t.Fatal(err)
	}
	store, err := trade.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	r := trade.CanaryGateRequest{OperationID: permit.OperationID, AttemptID: attempt, AuthorizationID: permit.AuthorizationID, AuthorizationEpoch: permit.AuthorizationEpoch, WalletID: id.WalletID, ChainID: trade.ChainID}
	if err := s.db.QueryRow(`SELECT policy_version,environment,build_sha,release_id,deployment_id FROM manual_execution_bindings WHERE operation_id=?`, permit.OperationID).Scan(&r.PolicyVersion, &r.Deployment.Environment, &r.Deployment.BuildSHA, &r.Deployment.ReleaseID, &r.Deployment.DeploymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_b24_consume_audit BEFORE INSERT ON canary_runtime_audit WHEN NEW.event_type='MANUAL_FIRST_BROADCAST_CONSUMED' BEGIN SELECT RAISE(ABORT,'forced consume audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeManualFirstBroadcastPermit(context.Background(), r, permit.ID, raw, permit.IssuedAt.Add(time.Nanosecond)); err == nil {
		t.Fatal("consume committed without durable audit")
	}
	var state string
	var scopes, audits int
	if err := s.db.QueryRow(`SELECT state FROM canary_send_permits WHERE id=?`, permit.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM manual_first_broadcast_consume_scopes),(SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='MANUAL_FIRST_BROADCAST_CONSUMED')`).Scan(&scopes, &audits); err != nil || state != "ISSUED" || scopes != 0 || audits != 0 {
		t.Fatal("failed consume left partial state", state, scopes, audits, err)
	}
}

func TestHTTPM4B24RestartKeepsConsumeOneShot(t *testing.T) {
	f := manualB24Fixture(t)
	before := manualB24EconomicSnapshot(t, f.s)
	now := f.permit.IssuedAt.Add(time.Nanosecond)
	// An ISSUED permit remains usable once after a physical file close/reopen.
	f.d, f.s = bindingReopen(t, f.d, f.path)
	var err error
	f.trade, err = trade.NewStore(f.d)
	if err != nil {
		t.Fatal(err)
	}
	if reason, err := f.trade.ConsumeManualFirstBroadcastPermit(context.Background(), f.r, f.permit.ID, f.raw, now); err != nil {
		t.Fatalf("initial consume rejected: %s: %v", reason, err)
	}
	if err := f.d.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err := fresh.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	freshStore, err := trade.NewStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if reason, err := freshStore.ConsumeManualFirstBroadcastPermit(context.Background(), f.r, f.permit.ID, f.raw, now); err == nil || reason != "PERMIT_IDENTITY_MISMATCH" {
		t.Fatalf("restart replay consumed permit: %s: %v", reason, err)
	}
	var state, auditID string
	var audits, submissions int
	db, err := fresh.SQLDB(storage.TradeOwner)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT state FROM canary_send_permits WHERE id=?`, f.permit.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*),COALESCE(MIN(id),''), (SELECT COUNT(*) FROM transaction_submissions) FROM canary_runtime_audit WHERE event_type='MANUAL_FIRST_BROADCAST_CONSUMED'`).Scan(&audits, &auditID, &submissions); err != nil {
		t.Fatal(err)
	}
	if state != "CONSUMED" || audits != 1 || auditID == "" || submissions != 0 {
		t.Fatalf("restart changed durable consume state: state=%s audits=%d audit=%s submissions=%d", state, audits, auditID, submissions)
	}
	freshAPIStore, err := NewStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if manualB24EconomicSnapshot(t, freshAPIStore) != before {
		t.Fatal("consume/restart changed original artifact, nonce, budget or economic phase")
	}
}

func TestHTTPM4B24ConcurrentConsumersHaveOneWinner(t *testing.T) {
	f := manualB24Fixture(t)
	before := manualB24EconomicSnapshot(t, f.s)
	secondDB, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer secondDB.Close()
	second, err := trade.NewStore(secondDB)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	type result struct {
		reason string
		err    error
	}
	const consumers = 16
	results := make(chan result, consumers)
	now := f.permit.IssuedAt.Add(time.Nanosecond)
	var wg sync.WaitGroup
	for i := 0; i < consumers; i++ {
		store := []*trade.Store{f.trade, second}[i%2]
		wg.Add(1)
		go func(store *trade.Store) {
			defer wg.Done()
			<-start
			reason, err := store.ConsumeManualFirstBroadcastPermit(context.Background(), f.r, f.permit.ID, f.raw, now)
			results <- result{reason: reason, err: err}
		}(store)
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for result := range results {
		if result.err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent consumers had %d winners", wins)
	}
	db := f.s.db
	var state string
	var consumed, audits, scopes int
	if err := db.QueryRow(`SELECT state FROM canary_send_permits WHERE id=?`, f.permit.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM canary_send_permits WHERE id=? AND state='CONSUMED'),(SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='MANUAL_FIRST_BROADCAST_CONSUMED'),(SELECT COUNT(*) FROM manual_first_broadcast_consume_scopes)`, f.permit.ID).Scan(&consumed, &audits, &scopes); err != nil {
		t.Fatal(err)
	}
	if state != "CONSUMED" || consumed != 1 || audits != 1 || scopes != 0 {
		t.Fatalf("concurrent consume accounting mismatch: state=%s consumed=%d audits=%d scopes=%d", state, consumed, audits, scopes)
	}
	if manualB24EconomicSnapshot(t, f.s) != before {
		t.Fatal("concurrent consume changed original artifact/nonce/accounting")
	}
}

func TestHTTPM4B24MutableControlsAndIdentityRejectWithoutConsume(t *testing.T) {
	// Identity-only denials must leave the entire DB unchanged, so they can
	// share one issued fixture without repeating expensive historical setup.
	// Control mutations still get isolated fixtures and retain all assertions.
	shared := manualB24Fixture(t)
	cases := []struct {
		name   string
		mutate func(*testing.T, *manualB24ConsumeFixture, time.Time)
		want   string
	}{
		{name: "artifact", mutate: func(_ *testing.T, _ *manualB24ConsumeFixture, _ time.Time) {}, want: "PERMIT_IDENTITY_MISMATCH"},
		{name: "operation", mutate: func(_ *testing.T, f *manualB24ConsumeFixture, _ time.Time) { f.r.OperationID = "wrong-operation" }, want: "PERMIT_IDENTITY_MISMATCH"},
		{name: "attempt", mutate: func(_ *testing.T, f *manualB24ConsumeFixture, _ time.Time) { f.r.AttemptID = "wrong-attempt" }, want: "PERMIT_IDENTITY_MISMATCH"},
		{name: "wallet", mutate: func(_ *testing.T, f *manualB24ConsumeFixture, _ time.Time) { f.r.WalletID = "wrong-wallet" }, want: "AUTHORIZATION_BINDING_MISMATCH"},
		{name: "epoch", mutate: func(_ *testing.T, f *manualB24ConsumeFixture, _ time.Time) { f.r.AuthorizationEpoch++ }, want: "PERMIT_IDENTITY_MISMATCH"},
		{name: "policy", mutate: func(_ *testing.T, f *manualB24ConsumeFixture, _ time.Time) { f.r.PolicyVersion++ }, want: "POLICY_OR_CHAIN_MISMATCH"},
		{name: "chain", mutate: func(_ *testing.T, f *manualB24ConsumeFixture, _ time.Time) { f.r.ChainID++ }, want: "POLICY_OR_CHAIN_MISMATCH"},
		{name: "deployment", mutate: func(_ *testing.T, f *manualB24ConsumeFixture, _ time.Time) {
			f.r.Deployment.BuildSHA = strings.Repeat("f", len(f.r.Deployment.BuildSHA))
		}, want: "AUTHORIZATION_BINDING_MISMATCH"},
		{name: "environment", mutate: func(_ *testing.T, f *manualB24ConsumeFixture, _ time.Time) { f.r.Deployment.Environment = "other" }, want: "AUTHORIZATION_BINDING_MISMATCH"},
		{name: "release", mutate: func(_ *testing.T, f *manualB24ConsumeFixture, _ time.Time) { f.r.Deployment.ReleaseID = "other" }, want: "AUTHORIZATION_BINDING_MISMATCH"},
		{name: "deployment-id", mutate: func(_ *testing.T, f *manualB24ConsumeFixture, _ time.Time) { f.r.Deployment.DeploymentID = "other" }, want: "AUTHORIZATION_BINDING_MISMATCH"},
		{name: "stop", mutate: func(t *testing.T, f *manualB24ConsumeFixture, _ time.Time) {
			if _, err := f.s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`); err != nil {
				t.Fatal(err)
			}
		}, want: "EMERGENCY_STOPPED"},
		{name: "revoke", mutate: func(t *testing.T, f *manualB24ConsumeFixture, now time.Time) {
			if err := f.trade.TransitionRuntimeAuthorization(context.Background(), f.r.AuthorizationID, "ARMED", "REVOKED", now); err != nil {
				t.Fatal(err)
			}
		}, want: "AUTHORIZATION_BINDING_MISMATCH"},
		{name: "reservation-frozen", mutate: func(t *testing.T, f *manualB24ConsumeFixture, _ time.Time) {
			if _, err := f.s.db.Exec(`UPDATE canary_risk_reservations SET state='frozen' WHERE operation_id=?`, f.r.OperationID); err != nil {
				t.Fatal(err)
			}
		}, want: "MANUAL_ADMISSION_BINDING_INVALID"},
		{name: "expired", mutate: func(_ *testing.T, _ *manualB24ConsumeFixture, _ time.Time) {}, want: "PERMIT_EXPIRED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := shared
			switch tc.name {
			case "stop", "revoke", "reservation-frozen":
				f = manualB24Fixture(t)
			}
			now := f.permit.IssuedAt.Add(time.Nanosecond)
			artifact := f.raw
			if tc.name == "artifact" {
				artifact = strings.Repeat("0", 64)
			}
			if tc.name == "expired" {
				now = f.permit.ExpiresAt.Add(time.Nanosecond)
			}
			tc.mutate(t, &f, now)
			before := admittedDatabaseSnapshot(t, f.s.db)
			reason, err := f.trade.ConsumeManualFirstBroadcastPermit(context.Background(), f.r, f.permit.ID, artifact, now)
			if err == nil || reason != tc.want {
				t.Fatalf("unexpected rejection: reason=%s err=%v want=%s", reason, err, tc.want)
			}
			var state string
			if err := f.s.db.QueryRow(`SELECT state FROM canary_send_permits WHERE id=?`, f.permit.ID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != "ISSUED" || count(t, f.s.db, "manual_first_broadcast_consume_scopes") != 0 || countEvent(t, f.s.db, "MANUAL_FIRST_BROADCAST_CONSUMED") != 0 {
				t.Fatalf("rejected consume mutated state: state=%s scopes=%d audits=%d", state, count(t, f.s.db, "manual_first_broadcast_consume_scopes"), countEvent(t, f.s.db, "MANUAL_FIRST_BROADCAST_CONSUMED"))
			}
			if admittedDatabaseSnapshot(t, f.s.db) != before {
				t.Fatal("rejected consume changed durable rows")
			}
		})
	}
}

func countEvent(t *testing.T, db *sql.DB, event string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type=?`, event).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestHTTPM4B24DisabledWalletCannotConsumePreviouslyIssuedPermit(t *testing.T) {
	f := manualB24Fixture(t)
	if _, err := f.s.db.Exec(`UPDATE dry_run_wallets SET enabled=0 WHERE id=?`, f.r.WalletID); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, f.s.db)
	reason, err := f.trade.ConsumeManualFirstBroadcastPermit(context.Background(), f.r, f.permit.ID, f.raw, f.permit.IssuedAt.Add(time.Nanosecond))
	if err == nil || reason != "MANUAL_SOURCE_INVALID" {
		t.Fatalf("disabled wallet consumed issued permit: %s: %v", reason, err)
	}
	if admittedDatabaseSnapshot(t, f.s.db) != before {
		t.Fatal("wallet-disabled rejection mutated durable rows")
	}
}

func TestHTTPM4B24PermitTimeBoundaries(t *testing.T) {
	for _, boundary := range []string{"before-issue", "at-issue", "before-expiry", "at-expiry", "after-expiry"} {
		t.Run(boundary, func(t *testing.T) {
			f := manualB24Fixture(t)
			now := f.permit.ExpiresAt
			switch boundary {
			case "before-issue":
				now = f.permit.IssuedAt.Add(-time.Nanosecond)
			case "at-issue":
				now = f.permit.IssuedAt
			case "before-expiry":
				now = now.Add(-time.Nanosecond)
			case "after-expiry":
				now = now.Add(time.Nanosecond)
			}
			before := admittedDatabaseSnapshot(t, f.s.db)
			reason, err := f.trade.ConsumeManualFirstBroadcastPermit(context.Background(), f.r, f.permit.ID, f.raw, now)
			if boundary == "before-expiry" {
				if err != nil || countEvent(t, f.s.db, "MANUAL_FIRST_BROADCAST_CONSUMED") != 1 {
					t.Fatalf("valid expiry -1ns rejected: %s: %v", reason, err)
				}
			} else if err == nil || admittedDatabaseSnapshot(t, f.s.db) != before {
				t.Fatalf("invalid boundary changed permit: %s: %v", reason, err)
			}
		})
	}
}

func TestHTTPM4B24WitnessFailureModesRollback(t *testing.T) {
	for _, table := range []string{"canary_runtime_audit", "canary_alert_outbox"} {
		for _, action := range []string{"ABORT", "FAIL", "IGNORE"} {
			t.Run(table+"/"+action, func(t *testing.T) {
				f := manualB24Fixture(t)
				when := `NEW.event_type='MANUAL_FIRST_BROADCAST_CONSUMED'`
				if table == "canary_alert_outbox" {
					when = `NEW.audit_id='manual-first-permit-consumed-audit:` + f.permit.ID + `'`
				}
				raise := `RAISE(` + action + `,'forced witness failure')`
				if action == "IGNORE" {
					raise = `RAISE(IGNORE)`
				}
				if _, err := f.s.db.Exec(`CREATE TRIGGER fail_b24_witness BEFORE INSERT ON ` + table + ` WHEN ` + when + ` BEGIN SELECT ` + raise + `; END`); err != nil {
					t.Fatal(err)
				}
				before := admittedDatabaseSnapshot(t, f.s.db)
				if _, err := f.trade.ConsumeManualFirstBroadcastPermit(context.Background(), f.r, f.permit.ID, f.raw, f.permit.IssuedAt.Add(time.Nanosecond)); err == nil {
					t.Fatal("failed witness allowed consume")
				}
				if admittedDatabaseSnapshot(t, f.s.db) != before {
					t.Fatal("witness failure left partial consume/audit/alert")
				}
			})
		}
	}
}

func TestHTTPM4B24ConsumeFaultWindowsRollback(t *testing.T) {
	for _, stage := range []string{"manual_consume_rechecked", "manual_consume_scoped", "manual_consume_before_commit"} {
		t.Run(stage, func(t *testing.T) {
			f := manualB24Fixture(t)
			f.trade.SetCanaryHookForTest(func(got string) error {
				if got == stage {
					return errors.New("forced consume fault window")
				}
				return nil
			})
			before := manualB24EconomicSnapshot(t, f.s)
			if _, err := f.trade.ConsumeManualFirstBroadcastPermit(context.Background(), f.r, f.permit.ID, f.raw, f.permit.IssuedAt.Add(time.Nanosecond)); err == nil {
				t.Fatal("fault window allowed consume")
			}
			var state string
			if err := f.s.db.QueryRow(`SELECT state FROM canary_send_permits WHERE id=?`, f.permit.ID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != "ISSUED" || count(t, f.s.db, "manual_first_broadcast_consume_scopes") != 0 || countEvent(t, f.s.db, "MANUAL_FIRST_BROADCAST_CONSUMED") != 0 || manualB24EconomicSnapshot(t, f.s) != before {
				t.Fatalf("fault window left partial state: state=%s scopes=%d audits=%d", state, count(t, f.s.db, "manual_first_broadcast_consume_scopes"), countEvent(t, f.s.db, "MANUAL_FIRST_BROADCAST_CONSUMED"))
			}
		})
	}
}

func TestHTTPM4B24CurrentDurableSourceAndLeaseRequired(t *testing.T) {
	for _, kind := range []string{"lease-missing", "lease-expired", "lease-owner", "lease-epoch", "released-reservation", "consumed-usage", "artifact-corruption", "gate-corruption", "policy-corruption", "allowlist-corruption"} {
		t.Run(kind, func(t *testing.T) {
			f := manualB24Fixture(t)
			now := f.permit.IssuedAt.Add(time.Nanosecond)
			var err error
			switch kind {
			case "lease-missing":
				_, err = f.s.db.Exec(`DELETE FROM canary_worker_leases WHERE role='SUBMISSION'`)
			case "lease-expired":
				_, err = f.s.db.Exec(`UPDATE canary_worker_leases SET expires_at=? WHERE role='SUBMISSION'`, now.Format(time.RFC3339Nano))
			case "lease-owner":
				_, err = f.s.db.Exec(`UPDATE canary_worker_leases SET holder_id='takeover',lease_epoch=lease_epoch+1 WHERE role='SUBMISSION'`)
			case "lease-epoch":
				_, err = f.s.db.Exec(`UPDATE canary_worker_leases SET lease_epoch=lease_epoch+1 WHERE role='SUBMISSION'`)
			case "released-reservation":
				// Retained corruption only: restore all original guards before
				// attempting consume. Intact MANUAL guards prohibit this release.
				seedSigningWitnessCorruption(t, f.s, "canary_risk_reservations", `UPDATE canary_risk_reservations SET state='released' WHERE operation_id=?`, f.r.OperationID)
			case "consumed-usage":
				seedSigningWitnessCorruption(t, f.s, "canary_authorization_operation_usage", `UPDATE canary_authorization_operation_usage SET state='CONSUMED' WHERE operation_id=?`, f.r.OperationID)
			case "artifact-corruption":
				seedSigningWitnessCorruption(t, f.s, "manual_signed_artifact_commands", `UPDATE manual_signed_artifact_commands SET envelope_json=json_set(envelope_json,'$.raw_sha256',?) WHERE operation_id=?`, strings.Repeat("0", 64), f.r.OperationID)
			case "gate-corruption":
				seedSigningWitnessCorruption(t, f.s, "canary_gate_attestations", `UPDATE canary_gate_attestations SET evidence_hash=? WHERE gate='C05'`, strings.Repeat("0", 64))
			case "policy-corruption":
				seedSigningWitnessCorruption(t, f.s, "manual_risk_policies", `UPDATE manual_risk_policies SET policy_hash=?`, strings.Repeat("0", 64))
			case "allowlist-corruption":
				seedSigningWitnessCorruption(t, f.s, "canary_contract_allowlist", `UPDATE canary_contract_allowlist SET enabled=0`)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, f.s.db)
			if _, err := f.trade.ConsumeManualFirstBroadcastPermit(context.Background(), f.r, f.permit.ID, f.raw, now); err == nil {
				t.Fatal("invalid current source consumed historical permit")
			}
			if admittedDatabaseSnapshot(t, f.s.db) != before {
				t.Fatal("invalid source was repaired or consumed")
			}
		})
	}
}
