package tradeapi

import (
	"context"
	"strings"
	"testing"
)

func TestHTTPM4B23DirectSQLIssuanceRequiresScopeAndCannotBeAdopted(t *testing.T) {
	_, s, _, v, id, request, _, _ := runtimeVerifierFixture(t, "buy")
	if _, err := v.capture(context.Background(), id, request); err != nil {
		t.Fatal(err)
	}
	var attempt, raw, tx, operation, auth, source, sourceHash, processHash, observation, issued, expires string
	var epoch, generation int
	if err := s.db.QueryRow(`SELECT q.attempt_id,json_extract(m.envelope_json,'$.raw_sha256'),json_extract(m.envelope_json,'$.tx_hash'),b.operation_id,b.authorization_id,b.authorization_epoch,e.source_json,e.source_hash,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',e.checked_at,'2026-10-07T12:00:30Z',(SELECT generation FROM manual_gate_clock_complete WHERE environment='b2-local' LIMIT 1) FROM manual_submission_gate_evidence e JOIN manual_execution_bindings b ON b.operation_id=e.operation_id JOIN manual_signed_artifact_commands m ON m.operation_id=e.operation_id JOIN manual_nonce_claims q ON q.operation_id=e.operation_id LIMIT 1`).Scan(&attempt, &raw, &tx, &operation, &auth, &epoch, &source, &sourceHash, &processHash, &observation, &issued, &expires, &generation); err != nil {
		t.Fatal(err)
	}
	args := []any{"manual-first-permit:" + attempt, operation, attempt, auth, epoch, source, sourceHash, processHash, observation, generation, raw, tx, issued, expires, "ISSUED"}
	beforeAudit, beforeAlerts := count(t, s.db, "canary_runtime_audit"), count(t, s.db, "canary_alert_outbox")
	if _, err := s.db.Exec(`INSERT INTO manual_first_broadcast_issuances(id,operation_id,attempt_id,authorization_id,authorization_epoch,source_json,source_hash,process_hash,observation_hash,clock_generation,artifact_hash,tx_hash,issued_at,expires_at,state) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...); err == nil {
		t.Fatal("direct issuance without scope accepted")
	}
	if count(t, s.db, "manual_first_broadcast_issuances") != 0 || count(t, s.db, "canary_runtime_audit") != beforeAudit || count(t, s.db, "canary_alert_outbox") != beforeAlerts {
		t.Fatal("direct issuance changed durable state")
	}
	var scopes int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM manual_first_broadcast_scopes`).Scan(&scopes); err != nil {
		t.Fatal(err)
	}
	if scopes != 0 {
		t.Fatalf("orphan scope remained: %d", scopes)
	}
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(context.Background(), `BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(context.Background(), `INSERT INTO manual_first_broadcast_scopes(issuance_id,attempt_id,source_hash,permit_id,snapshot_id) VALUES(?,?,?,?,?)`, "manual-first-permit:"+attempt, attempt, sourceHash, "manual-first-permit:"+attempt, "manual-first-gate:"+attempt); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(context.Background(), `INSERT INTO manual_first_broadcast_issuances(id,operation_id,attempt_id,authorization_id,authorization_epoch,source_json,source_hash,process_hash,observation_hash,clock_generation,artifact_hash,tx_hash,issued_at,expires_at,state) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...); err != nil {
		t.Fatal("scoped issuance rejected before commit", err)
	}
	_, commitErr := conn.ExecContext(context.Background(), `COMMIT`)
	if commitErr == nil {
		t.Fatal("orphan issuance committed without witnesses")
	}
	if commitErr.Error() == "" {
		t.Fatal("empty orphan commit error")
	}
	if _, rbErr := conn.ExecContext(context.Background(), `ROLLBACK`); rbErr != nil {
		t.Log("orphan rollback", rbErr)
	}
	_ = conn.Close()
	var residual string
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM manual_first_broadcast_issuances)||'/'||(SELECT COUNT(*) FROM manual_first_broadcast_scopes)`).Scan(&residual); err != nil {
		t.Fatal(err)
	}
	if residual != "0/0" {
		t.Fatalf("orphan transaction retained rows: %s", residual)
	}
	if _, err := s.db.Exec(`INSERT INTO canary_send_permits(id,operation_id,attempt_id,authorization_id,authorization_epoch,purpose,artifact_hash,tx_hash,state,issued_at,expires_at) VALUES(?,?,?,?,?,'FIRST_BROADCAST',?,?, 'ISSUED',?,?)`, "manual-first-permit:"+attempt, operation, attempt, auth, epoch, raw, tx, issued, expires); err == nil {
		var triggers string
		_ = s.db.QueryRow(`SELECT COALESCE(group_concat(name||':'||sql), '') FROM sqlite_master WHERE type='trigger' AND name LIKE 'manual_first_broadcast%permit%'`).Scan(&triggers)
		t.Fatal("later permit adoption accepted", triggers)
	}
}

func TestHTTPM4B23ScopeClosesOnlyWithExactWitnesses(t *testing.T) {
	_, s, _, v, id, request, _, _ := runtimeVerifierFixture(t, "buy")
	if _, err := v.capture(context.Background(), id, request); err != nil {
		t.Fatal(err)
	}
	var attempt, raw, tx string
	if err := s.db.QueryRow(`SELECT q.attempt_id,json_extract(m.envelope_json,'$.raw_sha256'),json_extract(m.envelope_json,'$.tx_hash') FROM manual_signed_artifact_commands m JOIN manual_nonce_claims q ON q.operation_id=m.operation_id`).Scan(&attempt, &raw, &tx); err != nil {
		t.Fatal(err)
	}
	p, err := (&manualFirstBroadcastIssuer{verifier: v}).issue(context.Background(), id, request, attempt, raw, tx)
	if err != nil {
		t.Fatal(err)
	}
	if count(t, s.db, "manual_first_broadcast_scopes") != 0 || p.ID != "manual-first-permit:"+attempt {
		t.Fatal("scope not closed by exact permit and snapshot", p)
	}
	if _, err := s.db.Exec(`UPDATE canary_send_permits SET state='CONSUMED' WHERE id=?`, p.ID); err == nil {
		t.Fatal("B2.3 permit consumption enabled")
	}
}

func TestHTTPM4B23WrongScopeWitnessIdentityRejected(t *testing.T) {
	_, s, _, _, _, _, _, _ := runtimeVerifierFixture(t, "buy")
	if _, err := s.db.Exec(`INSERT INTO manual_first_broadcast_scopes(issuance_id,attempt_id,source_hash,permit_id,snapshot_id) VALUES('x','attempt','` + strings.Repeat("a", 64) + `','wrong','wrong')`); err == nil {
		t.Fatal("wrong scope witness IDs accepted")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM manual_first_broadcast_scopes`).Scan(&n); err != nil || n != 0 {
		t.Fatal("wrong scope left durable row", n, err)
	}
}
