package storage

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const a4BootstrapSecret = "a4-b2-controlled-bootstrap-test-secret"

func a4BootstrapMAC(parts ...string) string {
	mac := hmac.New(sha256.New, []byte(a4BootstrapSecret))
	_, _ = mac.Write([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(mac.Sum(nil))
}

func a4GateSetHash(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(digest[:])
}

func TestA4OperatorAuthorityOverlayIsDurableButNoArm(t *testing.T) {
	if err := os.Setenv("RBH_MANUAL_OPERATOR_BOOTSTRAP_SECRET", a4BootstrapSecret); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("RBH_MANUAL_OPERATOR_BOOTSTRAP_SECRET") })
	d, err := Open(context.Background(), TradeOwner, filepath.Join(t.TempDir(), "a4.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := d.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 49 {
		t.Fatal("A4 hardening migration not landed", version, err)
	}
	for _, table := range []string{"manual_operator_trust_roots", "manual_operator_trust_root_bootstrap", "manual_operator_authorization_requests", "manual_operator_authorization_approvals", "manual_operator_revocations", "manual_operator_authorization_sources", "manual_operator_c07_attestations"} {
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
	bootstrapMAC := a4BootstrapMAC("key-1", strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64), "a4-deployment")
	if _, err := d.db.Exec(`INSERT INTO manual_operator_trust_root_bootstrap(operator_key_id,public_key_hex,root_fingerprint,bootstrap_evidence_hash,deployment_id,created_at,attestation_mac) VALUES('key-1',?,?,?, 'a4-deployment',?,?)`, strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64), stamp, bootstrapMAC); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO manual_operator_trust_roots VALUES('key-1',?,?,?, ?,NULL)`, strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64), stamp); err != nil {
		t.Fatal(err)
	}
	validEnvelope := `{"domain":"RBH-MANUAL-A4-AUTHORIZATION-V1","approval_id":"approval-1","request_id":"req-1","operator_key_id":"key-1","issued_at":"` + stamp + `","expires_at":"2026-10-10T01:00:00Z","authorization_id":"a4-auth","authorization_epoch":1,"build_sha":"` + strings.Repeat("1", 40) + `","release_id":"a4-release","deployment_id":"a4-deployment","environment":"a4-fixture","chain_id":4663,"wallet_id":"a4-wallet","wallet_address":"0x1111111111111111111111111111111111111111","policy_version":1,"policy_hash":"` + strings.Repeat("a", 64) + `","gate_evidence_set_hash":"` + strings.Repeat("b", 64) + `","max_operations":1,"max_total_input":"1","source_hash":"` + strings.Repeat("c", 64) + `","nonce":"nonce-1","schema_version":"1"}`
	if _, err := d.db.Exec(`INSERT INTO manual_operator_authorization_requests VALUES('req-1','a4-auth',1,?,?,?)`, strings.Repeat("e", 64), validEnvelope, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO manual_operator_authorization_approvals VALUES('approval-1','req-1','key-1',?,?,?, ?,?)`, strings.Repeat("e", 64), strings.Repeat("1", 128), stamp, "2026-10-10T01:00:00Z", stamp); err != nil {
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
	if err := os.Setenv("RBH_MANUAL_OPERATOR_BOOTSTRAP_SECRET", a4BootstrapSecret); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("RBH_MANUAL_OPERATOR_BOOTSTRAP_SECRET") })
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
	c07Evidence := strings.Repeat("f", 64)
	reviewedHead := strings.Repeat("a", 40)
	gateHash := a4GateSetHash("c1af9454a6a4433b488158c25224d13dfbf31178c71dae7468c80917743381fd", reviewedHead, stamp, "1a793f13d9304a31ed405f9fe25f1713cf79b6f8d28bb3da8ad16a01dc24c40b", reviewedHead, stamp, c07Evidence, reviewedHead, stamp, "8178e7b976dbea67be1de83cec529171e56d154cd2114f1bf290bebbd1fc77d8", reviewedHead, stamp)
	sourceHash := strings.Repeat("c", 64)
	build := strings.Repeat("1", 40)
	if _, err := d.db.Exec(`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('a4b2-op',4663,'fixture-wallet','a4b2-key','a4b2-fingerprint','swap','prepared',?)`, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO canary_admission_sources(operation_id,wallet_id,wallet_address,protocol,direction,token_address,contract_address,contract_role,runtime_code_hash,amount,gas_limit,gas_fee_cap,gas_tip_cap,native_balance,slippage_bps,sell_bps,policy_version,quote_block_number,quote_block_hash,expires_at,source_hash,created_at) VALUES('a4b2-op','fixture-wallet','0x1111111111111111111111111111111111111111','PONS_V2_CURVE','BUY','0x2222222222222222222222222222222222222222','0x3333333333333333333333333333333333333333','curve','0x`+strings.Repeat("4",64)+`','100','1','1','1','1000',100,100,1,1,'0x`+strings.Repeat("5",64)+`','2026-10-10T01:00:00Z',?,?)`, sourceHash, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO manual_operator_authorization_sources VALUES('a4b2-auth',1,'a4b2-op',?,?)`, sourceHash, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO manual_gate_clock_events(id,chain_id,environment,generation,high_water_at,fault_latched,fault_reason,fault_at,updated_at) VALUES('manual-clock:'||manual_artifact_bytes_sha256(hex(json_object('chain_id',4663,'environment','controlled-fixture','generation',1))),4663,'controlled-fixture',1,?,0,'',NULL,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	envelope := `{"domain":"RBH-MANUAL-A4-AUTHORIZATION-V1","approval_id":"approval-a4b2","request_id":"request-a4b2","operator_key_id":"key-a4b2","issued_at":"` + stamp + `","expires_at":"2026-10-10T01:00:00Z","authorization_id":"a4b2-auth","authorization_epoch":1,"build_sha":"` + build + `","release_id":"fixture-release","deployment_id":"fixture-deployment","environment":"controlled-fixture","chain_id":4663,"wallet_id":"fixture-wallet","wallet_address":"0x1111111111111111111111111111111111111111","policy_version":1,"policy_hash":"` + policyHash + `","gate_evidence_set_hash":"` + gateHash + `","max_operations":10,"max_total_input":"100","source_hash":"` + sourceHash + `","nonce":"nonce-a4b2","schema_version":"1"}`
	signature := ed25519.Sign(private, []byte(envelope))
	bootstrapMAC := a4BootstrapMAC("key-a4b2", hex.EncodeToString(public), strings.Repeat("d", 64), strings.Repeat("e", 64), "fixture-deployment")
	if _, err := d.db.Exec(`INSERT INTO manual_operator_trust_root_bootstrap(operator_key_id,public_key_hex,root_fingerprint,bootstrap_evidence_hash,deployment_id,created_at,attestation_mac) VALUES('key-a4b2',?,?,?, 'fixture-deployment',?,?)`, hex.EncodeToString(public), strings.Repeat("d", 64), strings.Repeat("e", 64), stamp, bootstrapMAC); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO manual_operator_trust_roots VALUES('key-a4b2',?,?,?, ?,NULL)`, hex.EncodeToString(public), strings.Repeat("d", 64), strings.Repeat("e", 64), stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO manual_operator_authorization_requests(request_id,authorization_id,authorization_epoch,request_hash,canonical_envelope,created_at) VALUES('request-a4b2','a4b2-auth',1,manual_artifact_bytes_sha256(hex(?)),?,?)`, envelope, envelope, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO manual_operator_authorization_approvals VALUES('approval-a4b2','request-a4b2','key-a4b2',manual_artifact_bytes_sha256(hex(?)),?,?,?,?)`, envelope, hex.EncodeToString(signature), stamp, "2026-10-10T01:00:00Z", stamp); err != nil {
		t.Fatal(err)
	}
	c07MAC := a4BootstrapMAC("a4b2-auth", "1", c07Evidence, reviewedHead, stamp)
	if _, err := d.db.Exec(`INSERT INTO manual_operator_c07_attestations(authorization_id,authorization_epoch,evidence_hash,reviewed_head,attested_at,attestation_mac) VALUES('a4b2-auth',1,?,?,?,?)`, c07Evidence, reviewedHead, stamp, c07MAC); err != nil {
		t.Fatal(err)
	}
	for _, gate := range []string{"C04", "C05", "C07", "C08"} {
		evidence := c07Evidence
		if gate != "C07" {
			if err := d.db.QueryRow(`SELECT evidence_hash FROM canary_gate_attestations WHERE gate=?`, gate).Scan(&evidence); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := d.db.Exec(`INSERT INTO manual_operator_gate_attestations VALUES('a4b2-auth',1,?,?,?,?)`, gate, evidence, reviewedHead, stamp); err != nil {
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

func TestA4B2HardeningRejectsUnwitnessedRootAndNonCanonicalEnvelope(t *testing.T) {
	d := metadataDB(t)
	insertMetadataAuth(t, d.db, "a4b2-hardening", 1)
	stamp := metadataStamp
	if _, err := d.db.Exec(`INSERT INTO manual_operator_trust_roots VALUES('unwitnessed',?,?,?, ?,NULL)`, strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64), stamp); err == nil {
		t.Fatal("direct SQL trust-root insertion bypassed bootstrap witness")
	}
	// The request table accepts only the exact canonical envelope key set and
	// types; a valid JSON object with an extra key is still rejected.
	badEnvelope := `{"domain":"RBH-MANUAL-A4-AUTHORIZATION-V1","extra":"caller"}`
	if _, err := d.db.Exec(`INSERT INTO manual_operator_authorization_requests VALUES('noncanonical','a4b2-hardening',1,?,?,?)`, strings.Repeat("4", 64), badEnvelope, stamp); err == nil {
		t.Fatal("non-canonical operator envelope accepted")
	}
}
