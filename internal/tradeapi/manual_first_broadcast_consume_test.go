package tradeapi

import (
	"context"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

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
