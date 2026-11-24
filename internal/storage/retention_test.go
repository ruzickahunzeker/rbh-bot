package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRetentionNeverDeletesUnconsumedEventsAndAdvancesFloor(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, TradeOwner, filepath.Join(t.TempDir(), "trade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	sqlDB, err := db.SQLDB(TradeOwner)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 24, 16, 0, 0, 0, time.UTC)
	for i := 1; i <= 5; i++ {
		if _, err := sqlDB.ExecContext(ctx,
			`INSERT INTO trade_audit_events(operation_kind, request_id, operation_id, state, payload_json, created_at)
			 VALUES('approval', 'req', 'op', 'ACCEPTED', '{}', ?)`,
			now.Format(time.RFC3339Nano),
		); err != nil {
			t.Fatal(err)
		}
	}

	// Slowest consumer is at seq=3; cutoff asks to prune up to seq=5.
	// Retention MUST stop at seq=2 (preserving seq 3, 4, 5) and advance min_retained_seq to 3.
	res, err := db.PruneTradeAuditEvents(ctx, 3, 5, now)
	if err != nil {
		t.Fatalf("PruneTradeAuditEvents failed: %v", err)
	}
	if res.PrunedRows != 2 || res.NewMinRetained != 3 {
		t.Fatalf("expected pruned=2 floor=3, got %+v", res)
	}
	var remaining int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM trade_audit_events`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 3 {
		t.Fatalf("expected 3 unconsumed events preserved, got %d", remaining)
	}

	// U-P1-1: Passing minConsumerSeq=100, cutoffSeq=50 when MAX(event_seq)=5 must cap floor at 6 (max_seq+1).
	res2, err := db.PruneTradeAuditEvents(ctx, 100, 50, now)
	if err != nil {
		t.Fatal(err)
	}
	if res2.NewMinRetained != 6 {
		t.Fatalf("expected min_retained_seq capped at max_seq+1 (6), got %d", res2.NewMinRetained)
	}
}
