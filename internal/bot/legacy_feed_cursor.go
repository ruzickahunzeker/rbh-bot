package bot

import (
	"context"
	"fmt"
	"time"
)

// AdoptLegacyFeedCursor verifies every legacy observation against the authenticated
// source before assigning its real chain. Migration 004's static RBH default is
// insufficient for older BSC bots. This is bookkeeping only: no signals replay.
func (s *Store) AdoptLegacyFeedCursor(ctx context.Context, chainID uint64, instance string, source OutboxSource) error {
	offset, err := s.DurableEventOffset(ctx)
	if err != nil || offset == 0 {
		return err
	}
	current, err := s.LoadFeedCursor(ctx, chainID, instance)
	if err != nil {
		return err
	}
	if current >= offset {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT feed_offset,observation_id FROM signal_inbox WHERE event_id=observation_id AND feed_offset<=? ORDER BY feed_offset`, offset)
	if err != nil {
		return err
	}
	legacy := map[int64]string{}
	for rows.Next() {
		var n int64
		var id string
		if err = rows.Scan(&n, &id); err != nil {
			rows.Close()
			return err
		}
		legacy[n] = id
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(legacy) == 0 {
		return fmt.Errorf("legacy checkpoint has no verifiable inbox")
	}
	after := int64(0)
	verified := 0
	for after < offset {
		items, err := source.ReadOutboxAfter(ctx, after, 1000)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return fmt.Errorf("legacy outbox history missing")
		}
		before := after
		for _, item := range items {
			if item.Offset > offset {
				break
			}
			if item.Offset <= after || legacy[item.Offset] != item.ObservationID {
				return fmt.Errorf("legacy inbox source identity mismatch at offset %d", item.Offset)
			}
			verified++
			after = item.Offset
		}
		if after == before {
			return fmt.Errorf("legacy source failed to reach checkpoint")
		}
	}
	if verified != len(legacy) {
		return fmt.Errorf("incomplete legacy observation verification")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var check int64
	if err = tx.QueryRowContext(ctx, `SELECT durable_event_offset FROM bot_progress WHERE id=1`).Scan(&check); err != nil {
		return err
	}
	if check != offset {
		return fmt.Errorf("legacy consumer changed during adoption")
	}
	changed, err := tx.ExecContext(ctx, `UPDATE signal_inbox SET chain_id=?,feed_instance_id=? WHERE event_id=observation_id AND feed_offset<=?`, chainID, instance, offset)
	if err != nil {
		return err
	}
	count, err := changed.RowsAffected()
	if err != nil {
		return err
	}
	if count != int64(verified) {
		return fmt.Errorf("legacy inbox changed during adoption")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO bot_feed_cursors(chain_id,feed_instance_id,durable_event_offset,updated_at) VALUES(?,?,?,?) ON CONFLICT(chain_id,feed_instance_id) DO UPDATE SET durable_event_offset=MAX(durable_event_offset,excluded.durable_event_offset),updated_at=excluded.updated_at`, chainID, instance, offset, formatFixedUTC(time.Now()))
	if err != nil {
		return err
	}
	return tx.Commit()
}
