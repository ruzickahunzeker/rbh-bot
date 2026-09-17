package feed

import (
	"context"
	"fmt"
)

type Metrics struct {
	Observations       int64
	Orphaned           int64
	OutboxRows         int64
	ReceiptAudits      int64
	DurableEventOffset int64
	Degraded           bool
}

func (s *Store) Metrics(ctx context.Context) (Metrics, error) {
	if s == nil || s.db == nil || ctx == nil {
		return Metrics{}, ErrFeedStoreUnavailable
	}
	var metrics Metrics
	var degraded int
	if err := s.db.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(*) FROM feed_events),
  (SELECT COUNT(*) FROM feed_events WHERE inclusion = 'orphaned'),
  (SELECT COUNT(*) FROM feed_outbox),
  (SELECT COUNT(*) FROM feed_receipt_audits),
  durable_event_offset,
  degraded
FROM feed_progress WHERE id = 1`).Scan(
		&metrics.Observations,
		&metrics.Orphaned,
		&metrics.OutboxRows,
		&metrics.ReceiptAudits,
		&metrics.DurableEventOffset,
		&degraded,
	); err != nil {
		return Metrics{}, fmt.Errorf("read feed metrics: %w", err)
	}
	metrics.Degraded = degraded != 0
	return metrics, nil
}
