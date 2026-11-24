package storage

import (
	"context"
	"errors"
	"time"
)

// RetentionResult summarizes a cursor-bounded SQLite retention pass.
type RetentionResult struct {
	PrunedRows     int64
	NewMinRetained int64
}

// PruneTradeAuditEvents deletes trade_audit_events strictly below min(minConsumerSeq, cutoffSeq)
// and atomically advances trade_audit_retention_floor.min_retained_seq in the same write tx.
//
// Invariants (AT-17 / AT-26):
//   - Never deletes any event_seq >= minConsumerSeq (unconsumed events are preserved).
//   - Never touches unfinalized risk reservations (canary_risk_reservations.state IN ('reserved','frozen')).
//   - Advances min_retained_seq so any stale consumer behind the new floor receives HTTP 410 Gone.
func (d *Database) PruneTradeAuditEvents(ctx context.Context, minConsumerSeq, cutoffSeq int64, now time.Time) (RetentionResult, error) {
	if d == nil || d.db == nil {
		return RetentionResult{}, errors.New("database is closed")
	}
	sqlDB, err := d.SQLDB(TradeOwner)
	if err != nil {
		return RetentionResult{}, err
	}
	if minConsumerSeq <= 0 || cutoffSeq <= 0 {
		return RetentionResult{}, nil
	}
	// Safe prune ceiling is strictly less than minConsumerSeq so unconsumed rows are never removed.
	safePruneMaxSeq := cutoffSeq
	if minConsumerSeq-1 < safePruneMaxSeq {
		safePruneMaxSeq = minConsumerSeq - 1
	}
	if safePruneMaxSeq <= 0 {
		return RetentionResult{}, nil
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return RetentionResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `DELETE FROM trade_audit_events WHERE event_seq <= ?`, safePruneMaxSeq)
	if err != nil {
		return RetentionResult{}, err
	}
	pruned, _ := res.RowsAffected()

	newFloor := safePruneMaxSeq + 1
	if _, err := tx.ExecContext(ctx,
		`UPDATE trade_audit_retention_floor
		 SET min_retained_seq = CASE WHEN ? > min_retained_seq THEN ? ELSE min_retained_seq END,
		     updated_at = ?
		 WHERE id = 1`,
		newFloor, newFloor, now.UTC().Format(time.RFC3339Nano),
	); err != nil {
		return RetentionResult{}, err
	}
	var persistedFloor int64
	if err := tx.QueryRowContext(ctx, `SELECT min_retained_seq FROM trade_audit_retention_floor WHERE id = 1`).Scan(&persistedFloor); err != nil {
		return RetentionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RetentionResult{}, err
	}
	return RetentionResult{PrunedRows: pruned, NewMinRetained: persistedFloor}, nil
}
