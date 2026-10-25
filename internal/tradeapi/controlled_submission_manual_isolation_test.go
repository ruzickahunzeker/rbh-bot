package tradeapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

func manualIsolationFixture(t *testing.T) manualB24ConsumeFixture {
	t.Helper()
	f := manualB24Fixture(t)
	// The upstream fixture arms its metadata authorization for private gate
	// tests. Restore the production no-arm guard before exercising the worker.
	if _, err := f.s.db.Exec(`CREATE TRIGGER IF NOT EXISTS manual_b1_no_arm BEFORE UPDATE OF state ON canary_runtime_authorizations
WHEN NEW.state='ARMED' AND EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.id)
BEGIN SELECT RAISE(ABORT,'manual authorization arming not implemented'); END`); err != nil {
		t.Fatal(err)
	}
	return f
}

func seedIsolationLegacyPermit(t *testing.T, f manualB24ConsumeFixture, suffix string, expiry time.Time) string {
	t.Helper()
	op, step, attempt, permit := "legacy-op-"+suffix, "legacy-step-"+suffix, "legacy-attempt-"+suffix, "legacy-permit-"+suffix
	stamp := f.permit.IssuedAt.Format(time.RFC3339Nano)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES(?,4663,?,?,?,'swap','signed',?)`, []any{op, f.r.WalletID, op, op, stamp}},
		{`INSERT INTO execution_steps(id,operation_id,step_index,kind) VALUES(?,?,0,'swap')`, []any{step, op}},
		{`INSERT INTO transaction_attempts(id,step_id,nonce,tx_hash,status,created_at) VALUES(?,?,'100',?,'signed',?)`, []any{attempt, step, f.permit.TxHash, stamp}},
		{`INSERT INTO canary_admission_sources VALUES(?,?,?,'PONS_V2_CURVE','BUY',?,?,'curve',?,'1','1','1','1','1',1,0,?,1,?,?,?,?)`, []any{op, f.r.WalletID, "0x0000000000000000000000000000000000000001", "0x0000000000000000000000000000000000000002", "0x0000000000000000000000000000000000000003", f.permit.TxHash, f.r.PolicyVersion, f.permit.TxHash, expiry.Format(time.RFC3339Nano), f.raw, stamp}},
		{`INSERT INTO canary_runtime_authorizations SELECT ?,epoch+100+?,mode,environment,chain_id,policy_version,wallet_id,build_sha,release_id,deployment_id,gate_evidence_set_hash,authorization_ref,max_operations,max_total_input,'PENDING',authorized_at,expires_at,created_at,updated_at FROM canary_runtime_authorizations WHERE id=?`, []any{"legacy-auth-" + suffix, len(suffix), f.permit.AuthorizationID}},
		{`INSERT INTO canary_send_permits(id,operation_id,attempt_id,authorization_id,authorization_epoch,purpose,artifact_hash,tx_hash,state,issued_at,expires_at) SELECT ?,?,?,id,epoch,'FIRST_BROADCAST',?,?,'ISSUED',?,? FROM canary_runtime_authorizations WHERE id=?`, []any{permit, op, attempt, f.raw, f.permit.TxHash, stamp, expiry.Format(time.RFC3339Nano), "legacy-auth-" + suffix}},
	} {
		if _, err := f.s.db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed legacy metadata: %v", err)
		}
	}
	return permit
}

func TestControlledSubmissionManualPermitDoesNotPoisonLegacyScanOrExpiry(t *testing.T) {
	f := manualIsolationFixture(t)
	expiring := seedIsolationLegacyPermit(t, f, "expired", f.permit.ExpiresAt)
	live := seedIsolationLegacyPermit(t, f, "live", f.permit.ExpiresAt.Add(time.Hour))
	work, err := f.trade.ListControlledSubmissionWork(context.Background(), f.permit.IssuedAt)
	if err != nil || len(work) != 2 {
		t.Fatalf("before expiry: work=%+v err=%v", work, err)
	}
	before := manualB24EconomicSnapshot(t, f.s)
	if err := f.trade.ExpireControlledSendPermits(context.Background(), f.permit.ExpiresAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if after := manualB24EconomicSnapshot(t, f.s); after != before {
		t.Fatal("expiry changed economic state")
	}
	for id, want := range map[string]string{f.permit.ID: "ISSUED", expiring: "EXPIRED", live: "ISSUED"} {
		var state string
		if err := f.s.db.QueryRow(`SELECT state FROM canary_send_permits WHERE id=?`, id).Scan(&state); err != nil || state != want {
			t.Fatalf("permit=%s state=%s want=%s err=%v", id, state, want, err)
		}
	}
	work, err = f.trade.ListControlledSubmissionWork(context.Background(), f.permit.ExpiresAt.Add(time.Second))
	if err != nil || len(work) != 1 || work[0].PermitID != live {
		t.Fatalf("after expiry: work=%+v err=%v", work, err)
	}
	var manualAudit int
	if err := f.s.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='CONTROLLED_SEND_PERMIT_EXPIRED' AND operation_id=?`, f.r.OperationID).Scan(&manualAudit); err != nil || manualAudit != 0 {
		t.Fatalf("manual expiry audits=%d err=%v", manualAudit, err)
	}
}

func TestControlledSubmissionRecoveryDoesNotTouchManualHistory(t *testing.T) {
	for _, stage := range []string{"before-intent", "after-intent", "historical-before", "historical-after"} {
		t.Run(stage, func(t *testing.T) {
			f := manualIsolationFixture(t)
			now := f.permit.IssuedAt.Add(time.Nanosecond)
			if reason, err := f.trade.ConsumeManualFirstBroadcastPermit(context.Background(), f.r, f.permit.ID, f.raw, now); err != nil {
				t.Fatalf("consume: %s: %v", reason, err)
			}
			var guard string
			if err := f.s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name='manual_nonce_no_submission_insert'`).Scan(&guard); err != nil {
				t.Fatal(err)
			}
			// Simulate restored historical rows only; restore the insertion guard
			// before invoking recovery, retaining every update/accounting guard.
			if _, err := f.s.db.Exec(`DROP TRIGGER manual_nonce_no_submission_insert`); err != nil {
				t.Fatal(err)
			}
			state, failure := "submitting", ""
			var intent any
			if stage == "after-intent" || stage == "historical-after" {
				intent = now.Format(time.RFC3339Nano)
			}
			if stage == "historical-before" {
				state, failure = "known_unsent", "restart_before_send_intent"
			}
			if stage == "historical-after" {
				state, failure = "broadcast_unknown", "restart_after_send_intent"
			}
			_, err := f.s.db.Exec(`INSERT INTO transaction_submissions(id,attempt_id,tx_hash,sequence,state,failure_class,send_intent_at,created_at,updated_at) VALUES(?,?,?,0,?,?,?,?,?)`, "restored-manual", f.r.AttemptID, f.permit.TxHash, state, failure, intent, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
			if _, restoreErr := f.s.db.Exec(guard); restoreErr != nil {
				t.Fatal(restoreErr)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Use the MANUAL authorization environment so its consumed permit
			// would qualify for the former generic recovery query.
			lease := trade.SubmissionLeaseFence{Environment: f.r.Deployment.Environment, HolderID: "legacy-worker"}
			if _, err := f.s.db.Exec(`UPDATE canary_worker_leases SET holder_id=?,lease_epoch=lease_epoch+1,expires_at=? WHERE role='SUBMISSION' AND environment=?`, lease.HolderID, time.Now().Add(time.Hour).Format(time.RFC3339Nano), lease.Environment); err != nil {
				t.Fatal(err)
			}
			if err := f.s.db.QueryRow(`SELECT lease_epoch FROM canary_worker_leases WHERE role='SUBMISSION' AND environment=?`, lease.Environment).Scan(&lease.Epoch); err != nil {
				t.Fatal(err)
			}
			before := manualB24EconomicSnapshot(t, f.s)
			if err := f.trade.RecoverControlledInflight(context.Background(), lease, now.Add(time.Second)); err != nil {
				t.Fatal(fmt.Errorf("recover %s: %w", stage, err))
			}
			if manualB24EconomicSnapshot(t, f.s) != before {
				t.Fatal("legacy recovery mutated MANUAL history or accounting")
			}
		})
	}
}
