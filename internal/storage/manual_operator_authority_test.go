package storage

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

func TestA4OperatorAuthorityOverlayIsDurableButNoArm(t *testing.T) {
	d, err := Open(context.Background(), TradeOwner, filepath.Join(t.TempDir(), "a4.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := d.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 47 {
		t.Fatal("migration046 not landed", version, err)
	}
	for _, table := range []string{"manual_operator_trust_roots", "manual_operator_authorization_requests", "manual_operator_authorization_approvals", "manual_operator_revocations"} {
		var n int
		if err := d.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatal("missing A4 table", table, err)
		}
	}
	// Seed only a pending synthetic row; this is not an operator grant.
	stamp := metadataStamp
	if _, err := d.db.Exec(`INSERT INTO canary_policies VALUES(1,'PONS_V2_CURVE','1',10000,'1','1','1',1,3600,1,'1','1','1',100,'0',?,?)`, strings.Repeat("a", 64), stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO canary_runtime_authorizations(id,epoch,mode,environment,chain_id,policy_version,wallet_id,build_sha,release_id,deployment_id,gate_evidence_set_hash,authorization_ref,max_operations,max_total_input,state,authorized_at,expires_at,created_at,updated_at)
VALUES('a4-auth',1,'CONTROLLED_CANARY','a4-fixture',4663,1,'a4-wallet',?, 'a4-release','a4-deployment',?, 'a4-ref',1,'1','PENDING',?,?,?,?)`, strings.Repeat("1", 40), strings.Repeat("a", 64), stamp, stamp, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO manual_operator_trust_roots VALUES('key-1',?,?,?, ?,NULL)`, strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64), stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO manual_operator_authorization_requests VALUES('req-1','a4-auth',1,?,?,?)`, strings.Repeat("e", 64), `{"authorization_id":"a4-auth","authorization_epoch":1}`, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO manual_operator_authorization_approvals VALUES('approval-1','req-1','key-1',?,?,?, ?,?)`, strings.Repeat("f", 64), strings.Repeat("1", 128), stamp, stamp, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	bad64 := "z" + strings.Repeat("0", 63)
	bad128 := "z" + strings.Repeat("0", 127)
	for _, statement := range []string{
		`INSERT INTO manual_operator_trust_roots VALUES('bad-key',?, ?, ?, ?,NULL)`,
		`INSERT INTO manual_operator_authorization_requests VALUES('bad-req','a4-auth',1,?, ?,?)`,
		`INSERT INTO manual_operator_authorization_approvals VALUES('bad-approval','req-1','key-1',?, ?,?,?,?)`,
		`INSERT INTO manual_operator_revocations VALUES('bad-revoke','a4-auth',1,'TEST',?,?)`,
	} {
		var err error
		switch {
		case strings.Contains(statement, "trust_roots"):
			_, err = d.db.Exec(statement, bad64, strings.Repeat("c", 64), strings.Repeat("d", 64), stamp)
		case strings.Contains(statement, "authorization_requests"):
			_, err = d.db.Exec(statement, bad64, `{"authorization_id":"a4-auth"}`, stamp)
		case strings.Contains(statement, "authorization_approvals"):
			_, err = d.db.Exec(statement, bad64, bad128, stamp, stamp, stamp)
		default:
			_, err = d.db.Exec(statement, bad64, stamp)
		}
		if err == nil {
			t.Fatal("malformed operator evidence accepted", statement)
		}
	}
	for _, statement := range []string{
		`UPDATE manual_operator_trust_roots SET revoked_at='2026-10-28T00:00:00Z'`,
		`DELETE FROM manual_operator_authorization_requests`,
		`UPDATE manual_operator_authorization_approvals SET signature_hex='` + strings.Repeat("2", 128) + `'`,
	} {
		if _, err := d.db.Exec(statement); err == nil {
			t.Fatal("A4 evidence mutation allowed", statement)
		}
	}
	var triggerSQL string
	if err := d.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name='manual_b1_no_arm'`).Scan(&triggerSQL); err != nil || !strings.Contains(triggerSQL, "verified operator approval") {
		t.Fatal("approval-gated no-arm trigger missing", err)
	}
}

func TestA4B2DirectSQLValidApprovalArmsExactlyOnce(t *testing.T) {
	d := metadataDB(t)
	insertMetadataAuth(t, d.db, "a4b2-auth", 1)
	if _, err := d.db.Exec(`INSERT INTO dry_run_wallets(id,chain_id,address,enabled) VALUES('fixture-wallet',4663,'0x1111111111111111111111111111111111111111',1)`); err != nil {
		t.Fatal(err)
	}
	policyHash := insertMetadataPolicy(t, d.db)
	if err := declareMetadataUnit(d.db, "a4b2-auth", 1, policyHash, "native", 18); err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	stamp := metadataStamp
	gateHash := strings.Repeat("b", 64)
	build := strings.Repeat("1", 40)
	envelope := `{"domain":"RBH-MANUAL-A4-AUTHORIZATION-V1","approval_id":"approval-a4b2","request_id":"request-a4b2","operator_key_id":"key-a4b2","issued_at":"` + stamp + `","expires_at":"2026-10-10T01:00:00Z","authorization_id":"a4b2-auth","authorization_epoch":1,"build_sha":"` + build + `","release_id":"fixture-release","deployment_id":"fixture-deployment","environment":"controlled-fixture","chain_id":4663,"wallet_id":"fixture-wallet","wallet_address":"0x1111111111111111111111111111111111111111","policy_version":1,"policy_hash":"` + policyHash + `","gate_evidence_set_hash":"` + gateHash + `","max_operations":10,"max_total_input":"100","source_hash":"` + strings.Repeat("c", 64) + `","nonce":"nonce-a4b2","schema_version":"1"}`
	signature := ed25519.Sign(private, []byte(envelope))
	if _, err := d.db.Exec(`INSERT INTO manual_operator_trust_roots VALUES('key-a4b2',?,?,?, ?,NULL)`, hex.EncodeToString(public), strings.Repeat("d", 64), strings.Repeat("e", 64), stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO manual_operator_authorization_requests(request_id,authorization_id,authorization_epoch,request_hash,canonical_envelope,created_at) VALUES('request-a4b2','a4b2-auth',1,manual_artifact_bytes_sha256(hex(?)),?,?)`, envelope, envelope, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO manual_operator_authorization_approvals VALUES('approval-a4b2','request-a4b2','key-a4b2',manual_artifact_bytes_sha256(hex(?)),?,?,?,?)`, envelope, hex.EncodeToString(signature), stamp, "2026-10-10T01:00:00Z", stamp); err != nil {
		t.Fatal(err)
	}
	for _, gate := range []string{"C04", "C05", "C07", "C08"} {
		evidence := strings.Repeat("f", 64)
		if gate != "C07" {
			if err := d.db.QueryRow(`SELECT evidence_hash FROM canary_gate_attestations WHERE gate=?`, gate).Scan(&evidence); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := d.db.Exec(`INSERT INTO manual_operator_gate_attestations VALUES('a4b2-auth',1,?,?,?,?)`, gate, evidence, "reviewed-head", stamp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.db.Exec(`UPDATE canary_runtime_authorizations SET state='ARMED',updated_at=? WHERE id='a4b2-auth' AND state='PENDING'`, stamp); err != nil {
		t.Fatal("valid approval rejected", err)
	}
	if _, err := d.db.Exec(`UPDATE canary_runtime_authorizations SET state='ARMED',updated_at=? WHERE id='a4b2-auth' AND state='ARMED'`, stamp); err != nil {
		t.Fatal("idempotent armed update rejected", err)
	}
	var state string
	if err := d.db.QueryRow(`SELECT state FROM canary_runtime_authorizations WHERE id='a4b2-auth'`).Scan(&state); err != nil || state != "ARMED" {
		t.Fatal("state", state, err)
	}
	var audits int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE authorization_id='a4b2-auth' AND event_type='AUTHORIZATION_STATE_CHANGED'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("audit count", audits, err)
	}
}
