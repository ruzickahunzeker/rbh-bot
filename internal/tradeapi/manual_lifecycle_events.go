package tradeapi

import (
	"context"
	"database/sql"
	"errors"

	"github.com/ethereum/go-ethereum/common"
)

const manualLifecyclePageSize = 100

// A cursor belongs to one durable request journal. It is never synthesized
// from receipt timestamps, unrelated table rowids or current permission state.
func (s *Store) manualLifecycleEvents(ctx context.Context, identity Identity, request string, after uint64) (Events, error) {
	result := Events{Scope: "MANUAL_ORDER_LIFECYCLE", Events: []Event{}, NextCursor: after}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var address, operation string
	var first uint64
	err = tx.QueryRowContext(ctx, `SELECT wallet_address,operation_id,sequence FROM manual_http_order_sources WHERE id=? AND principal_id=? AND wallet_id=?`, request, identity.PrincipalID, identity.WalletID).Scan(&address, &operation, &first)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if !common.IsHexAddress(address) || common.HexToAddress(address) != common.HexToAddress(identity.WalletAddress) {
		return result, ErrNotFound
	}
	// Missing/mismatched entries are corruption, not an empty healthy page. This
	// also checks migration backfill without advancing past an unseen event.
	if err := manualHistoryAbsent(ctx, tx, `SELECT
 NOT EXISTS(SELECT 1 FROM manual_lifecycle_events WHERE request_id=? AND cursor=? AND audit_id='manual-http-audit:'||?)
 OR EXISTS(SELECT 1 FROM canary_runtime_audit a LEFT JOIN manual_lifecycle_events e ON e.audit_id=a.id AND e.request_id=?
 WHERE (a.operation_id=? OR a.id='manual-http-audit:'||?) AND e.audit_id IS NULL)
 OR EXISTS(SELECT 1 FROM manual_lifecycle_events e LEFT JOIN canary_runtime_audit a ON a.id=e.audit_id
 WHERE e.request_id=? AND (a.id IS NULL OR (a.operation_id IS NOT ? AND a.id IS NOT 'manual-http-audit:'||?)
 OR e.event_type IS NOT a.event_type OR e.reason_code IS NOT a.reason_code OR e.created_at IS NOT a.created_at))
 OR (SELECT COUNT(*) FROM manual_lifecycle_events WHERE request_id=?)!=(SELECT MAX(cursor)-MIN(cursor)+1 FROM manual_lifecycle_events WHERE request_id=?)
 OR (SELECT MIN(cursor) FROM manual_lifecycle_events WHERE request_id=?)!=?`,
		request, first, request, request, operation, request, request, operation, request, request, request, request, first); err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT cursor,event_type,reason_code,created_at FROM manual_lifecycle_events WHERE request_id=? AND cursor>? ORDER BY cursor LIMIT ?`, request, after, manualLifecyclePageSize)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Cursor, &e.Type, &e.Reason, &e.CreatedAt); err != nil {
			rows.Close()
			return result, err
		}
		result.Events = append(result.Events, e)
		result.NextCursor = e.Cursor
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, closeErr
	}
	return result, tx.Commit()
}
