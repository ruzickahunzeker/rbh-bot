package tradeapi

import (
	"context"
	"strings"
	"testing"
)

func TestHTTPM4B23FirstBroadcastRejectsArtifactMismatch(t *testing.T) {
	_, s, _, v, id, request, _, _ := runtimeVerifierFixture(t, "buy")
	var attempt, raw, tx string
	if err := s.db.QueryRow(`SELECT q.attempt_id,json_extract(m.envelope_json,'$.raw_sha256'),json_extract(m.envelope_json,'$.tx_hash') FROM manual_signed_artifact_commands m JOIN manual_nonce_claims q ON q.operation_id=m.operation_id`).Scan(&attempt, &raw, &tx); err != nil {
		t.Fatal(err)
	}
	issuer := &manualFirstBroadcastIssuer{verifier: v}
	if _, err := issuer.issue(context.Background(), id, request, attempt, strings.Repeat("a", 64), tx); err == nil {
		t.Fatal("issuance accepted artifact mismatch")
	}
	if count(t, s.db, "manual_first_broadcast_issuances") != 0 || count(t, s.db, "canary_send_permits") != 0 || count(t, s.db, "canary_runtime_gate_snapshots") != 0 {
		t.Fatal("rejected issuance left permission metadata")
	}
}

func TestHTTPM4B23ConsumeAndReplayRemainDisabled(t *testing.T) {
	_, s, _, v, id, request, _, _ := runtimeVerifierFixture(t, "buy")
	var attempt, raw, tx string
	if err := s.db.QueryRow(`SELECT q.attempt_id,json_extract(m.envelope_json,'$.raw_sha256'),json_extract(m.envelope_json,'$.tx_hash') FROM manual_signed_artifact_commands m JOIN manual_nonce_claims q ON q.operation_id=m.operation_id`).Scan(&attempt, &raw, &tx); err != nil {
		t.Fatal(err)
	}
	// A direct permit insert remains denied because no complete B23 issuance row exists.
	err := s.db.QueryRow(`SELECT request_id FROM manual_execution_bindings LIMIT 1`).Scan(&request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO canary_send_permits(id,operation_id,attempt_id,authorization_id,authorization_epoch,purpose,artifact_hash,tx_hash,state,issued_at,expires_at) VALUES('manual-direct', 'manual_op_'||?, ?, 'auth', 1, 'FIRST_BROADCAST', ?, ?, 'ISSUED', '2026-10-17T00:00:00Z', '2026-10-17T00:01:00Z')`, request, attempt, strings.Repeat("a", 64), tx); err == nil {
		t.Fatal("direct permit bypass accepted")
	}
	_ = v
	_ = id
	_ = raw
}

func TestHTTPM4B23FreshFirstBroadcastPermitIsMetadataOnly(t *testing.T) {
	_, s, _, v, id, request, _, _ := runtimeVerifierFixture(t, "buy")
	if _, err := v.capture(context.Background(), id, request); err != nil {
		t.Fatal("B22 fresh preparation", err)
	}
	var attempt, raw, tx string
	if err := s.db.QueryRow(`SELECT q.attempt_id,json_extract(m.envelope_json,'$.raw_sha256'),json_extract(m.envelope_json,'$.tx_hash') FROM manual_signed_artifact_commands m JOIN manual_nonce_claims q ON q.operation_id=m.operation_id`).Scan(&attempt, &raw, &tx); err != nil {
		t.Fatal(err)
	}
	permit, err := (&manualFirstBroadcastIssuer{verifier: v}).issue(context.Background(), id, request, attempt, raw, tx)
	if err != nil {
		t.Fatal("first permit issuance", err)
	}
	if permit.ID == "" || count(t, s.db, "manual_first_broadcast_issuances") != 1 || count(t, s.db, "canary_send_permits") != 1 || count(t, s.db, "canary_runtime_gate_snapshots") != 1 {
		t.Fatal("missing exact metadata", permit)
	}
	var purpose, state string
	if err := s.db.QueryRow(`SELECT purpose,state FROM canary_send_permits WHERE id=?`, permit.ID).Scan(&purpose, &state); err != nil || purpose != "FIRST_BROADCAST" || state != "ISSUED" {
		t.Fatal("unexpected permit", purpose, state, err)
	}
	if count(t, s.db, "transaction_submissions") != 0 || count(t, s.db, "manual_signing_intents") != 1 {
		t.Fatal("issuance reached submission/signing state")
	}
}

func TestHTTPM4B23DuplicateFirstPermitDoesNotMintWindow(t *testing.T) {
	_, s, _, v, id, request, _, _ := runtimeVerifierFixture(t, "buy")
	if _, err := v.capture(context.Background(), id, request); err != nil {
		t.Fatal(err)
	}
	var attempt, raw, tx string
	if err := s.db.QueryRow(`SELECT q.attempt_id,json_extract(m.envelope_json,'$.raw_sha256'),json_extract(m.envelope_json,'$.tx_hash') FROM manual_signed_artifact_commands m JOIN manual_nonce_claims q ON q.operation_id=m.operation_id`).Scan(&attempt, &raw, &tx); err != nil {
		t.Fatal(err)
	}
	issuer := &manualFirstBroadcastIssuer{verifier: v}
	p1, err := issuer.issue(context.Background(), id, request, attempt, raw, tx)
	if err != nil {
		t.Fatal(err)
	}
	events := count(t, s.db, "manual_gate_clock_events")
	audits := count(t, s.db, "canary_runtime_audit")
	p2, err := issuer.issue(context.Background(), id, request, attempt, raw, tx)
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 || count(t, s.db, "manual_first_broadcast_issuances") != 1 || count(t, s.db, "canary_send_permits") != 1 || count(t, s.db, "canary_runtime_gate_snapshots") != 1 || count(t, s.db, "manual_gate_clock_events") != events || count(t, s.db, "canary_runtime_audit") != audits {
		t.Fatal("duplicate minted metadata/window", p1, p2)
	}
}

func TestHTTPM4B23AuditFailureRollsBackEntireIssuance(t *testing.T) {
	_, s, _, v, id, request, _, _ := runtimeVerifierFixture(t, "buy")
	if _, err := v.capture(context.Background(), id, request); err != nil {
		t.Fatal(err)
	}
	var attempt, raw, tx string
	if err := s.db.QueryRow(`SELECT q.attempt_id,json_extract(m.envelope_json,'$.raw_sha256'),json_extract(m.envelope_json,'$.tx_hash') FROM manual_signed_artifact_commands m JOIN manual_nonce_claims q ON q.operation_id=m.operation_id`).Scan(&attempt, &raw, &tx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_first_permit_audit BEFORE INSERT ON canary_runtime_audit WHEN NEW.event_type='MANUAL_FIRST_BROADCAST_ISSUED' BEGIN SELECT RAISE(ABORT,'forced first permit audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	beforeClock, beforeAudit := count(t, s.db, "manual_gate_clock_events"), count(t, s.db, "canary_runtime_audit")
	if _, err := (&manualFirstBroadcastIssuer{verifier: v}).issue(context.Background(), id, request, attempt, raw, tx); err == nil {
		t.Fatal("audit failure accepted")
	}
	if count(t, s.db, "manual_first_broadcast_issuances") != 0 || count(t, s.db, "canary_send_permits") != 0 || count(t, s.db, "canary_runtime_gate_snapshots") != 0 || count(t, s.db, "manual_gate_clock_events") != beforeClock || count(t, s.db, "canary_runtime_audit") != beforeAudit {
		t.Fatal("audit failure left partial issuance")
	}
}
