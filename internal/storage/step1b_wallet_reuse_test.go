package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestStep1BMultiGenReorgFrozenAndBidirectionalAbortTriggers(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, TradeOwner, filepath.Join(t.TempDir(), "step1b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// 1. Duplicate (chain_id, lower(address)) wallet alias must fail closed.
	if _, err := db.db.ExecContext(ctx, `INSERT INTO dry_run_wallets VALUES('w1', 4663, '0xAbCd000000000000000000000000000000000001', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO dry_run_wallets VALUES('w1-alias', 4663, '0xabcd000000000000000000000000000000000001', 1)`); err == nil {
		t.Fatal("expected duplicate normalized wallet address to be rejected")
	}

	// 2. AT-05: Gen-1 & Gen-2 both committed -> Gen-3 reserved -> multi-block Reorg rolls
	// BOTH Gen-1 and Gen-2 back to 'frozen' simultaneously with zero unique index collision.
	for _, op := range []string{"op-1", "op-2", "op-3"} {
		if _, err := db.db.ExecContext(ctx,
			`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES(?,4663,'w1',?,?,'swap','submitted','2026-01-01T00:00:00Z')`,
			op, "key-"+op, "fp-"+op); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO canary_risk_reservations VALUES('res-1','op-1','w1','0x2000',1,'100','10','committed','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',1,'0x1','0x1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO canary_risk_reservations VALUES('res-2','op-2','w1','0x2000',1,'100','10','committed','2026-01-01T00:00:01Z','2026-01-01T00:00:01Z',2,'0x2','0x2')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO canary_risk_reservations VALUES('res-3','op-3','w1','0x2000',1,'100','10','reserved','2026-01-01T00:00:02Z','2026-01-01T00:00:02Z',3,'0x3','0x3')`); err != nil {
		t.Fatal(err)
	}
	// Second concurrent 'reserved' on same wallet MUST still fail.
	if _, err := db.db.ExecContext(ctx, `INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('op-4',4663,'w1','key-4','fp-4','swap','admitted','2026-01-01T00:00:03Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO canary_risk_reservations VALUES('res-4','op-4','w1','0x2000',1,'100','10','reserved','2026-01-01T00:00:03Z','2026-01-01T00:00:03Z',4,'0x4','0x4')`); err == nil {
		t.Fatal("expected second concurrent reserved reservation on same wallet to fail")
	}
	// Multi-block Reorg rolls BOTH op-1 and op-2 back to 'frozen' while op-3 is still 'reserved'.
	if _, err := db.db.ExecContext(ctx, `UPDATE canary_risk_reservations SET state='frozen' WHERE id IN ('res-1','res-2')`); err != nil {
		t.Fatalf("multi-generation Reorg rollback to frozen must not hit unique index collision: %v", err)
	}

	// 3. AT-02: Bidirectional SQLite trigger mutual exclusion between manual_pre_send_aborts and manual_signing_intents.
	if _, err := db.db.ExecContext(ctx, `INSERT INTO manual_pre_send_aborts VALUES('op-4','res-4','expired_pre_sign','2026-01-01T00:05:00Z')`); err != nil {
		t.Fatalf("insert manual_pre_send_aborts failed: %v", err)
	}
	// Immutable triggers block update/delete.
	if _, err := db.db.ExecContext(ctx, `DELETE FROM manual_pre_send_aborts WHERE operation_id='op-4'`); err == nil {
		t.Fatal("expected DELETE on manual_pre_send_aborts to be blocked")
	}
	// Verify trigger SQL for mutual exclusion exists on manual_signing_intents.
	var trigSQL string
	if err := db.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='trigger' AND name='manual_signing_intent_forbids_pre_send_abort'`).Scan(&trigSQL); err != nil || !strings.Contains(trigSQL, "MANUAL_SIGNING_INTENT_CONFLICTS_WITH_PRE_SEND_ABORT") {
		t.Fatalf("missing bidirectional trigger on manual_signing_intents: %q err=%v", trigSQL, err)
	}
}
