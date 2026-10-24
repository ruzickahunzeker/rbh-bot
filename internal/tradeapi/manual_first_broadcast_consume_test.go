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
