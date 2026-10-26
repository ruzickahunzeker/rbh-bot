package storage

import (
	"context"
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
	if err := d.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 46 {
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
	if err := d.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name='manual_b1_no_arm'`).Scan(&triggerSQL); err != nil || !strings.Contains(triggerSQL, "manual atomic admission not connected") {
		t.Fatal("migration046 changed or removed manual_b1_no_arm", err)
	}
}
