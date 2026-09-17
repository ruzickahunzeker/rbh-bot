package feed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

var ErrFeedStoreUnavailable = errors.New("feed store unavailable")

type Store struct {
	db *sql.DB
}

func NewStore(database *storage.Database) (*Store, error) {
	db, err := database.SQLDB(storage.FeedOwner)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) PersistReceiptAudit(audit ReceiptAudit) error {
	if s == nil || s.db == nil || audit.TransactionHash == ([32]byte{}) {
		return ErrFeedStoreUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO feed_receipt_audits(tx_hash, block_number, receipt_status, reason, persisted_at)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(tx_hash) DO NOTHING`,
		audit.TransactionHash.Hex(), audit.BlockNumber, audit.ReceiptStatus, audit.Reason, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("persist receipt audit: %w", err)
	}
	var blockNumber uint64
	var status uint64
	var reason string
	if err := s.db.QueryRowContext(ctx, `SELECT block_number, receipt_status, reason FROM feed_receipt_audits WHERE tx_hash = ?`, audit.TransactionHash.Hex()).Scan(&blockNumber, &status, &reason); err != nil {
		return fmt.Errorf("verify receipt audit: %w", err)
	}
	if blockNumber != audit.BlockNumber || status != audit.ReceiptStatus || reason != audit.Reason {
		return fmt.Errorf("receipt audit conflict for %s", audit.TransactionHash.Hex())
	}
	return nil
}

// CommitSequencer persists observations, outbox rows and the highest transaction
// handler sequence in one SQLite transaction. On restart ResumeSequence rewinds
// one full sequence before reconnecting so a crash inside a multi-tx sequence
// cannot skip the unprocessed suffix.
func (s *Store) CommitSequencer(ctx context.Context, observations []Observation, observedSequence uint64) (CommitResult, error) {
	return s.commit(ctx, observations, &observedSequence, nil)
}

// CommitObservations persists observations and outbox rows without changing
// sequencer progress. Use CommitReceipt when parser registry state changed.
func (s *Store) CommitObservations(ctx context.Context, observations []Observation) (CommitResult, error) {
	return s.commit(ctx, observations, nil, nil)
}

// CommitReceipt binds confirmed receipt observations to the parser registry
// snapshot produced by the same receipt. The snapshot and outbox become durable
// in the same SQLite transaction, so restart hydration cannot observe one
// without the other.
func (s *Store) CommitReceipt(ctx context.Context, observations []Observation, snapshot parser.RegistrySnapshot) (CommitResult, error) {
	return s.commit(ctx, observations, nil, &snapshot)
}

func (s *Store) commit(ctx context.Context, observations []Observation, observedSequence *uint64, registrySnapshot *parser.RegistrySnapshot) (CommitResult, error) {
	if s == nil || s.db == nil || ctx == nil {
		return CommitResult{}, ErrFeedStoreUnavailable
	}
	for _, observation := range observations {
		if err := observation.Validate(); err != nil {
			return CommitResult{}, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommitResult{}, fmt.Errorf("begin feed commit: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	result := CommitResult{}
	for _, observation := range observations {
		insert, err := tx.ExecContext(ctx, `
INSERT INTO feed_events(observation_id, chain_id, source, source_sequence, tx_hash, stable_action_path, payload_json, observed_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(observation_id) DO NOTHING`,
			observation.ID(), observation.ChainID, string(observation.Source), strconv.FormatUint(observation.SourceSequence, 10),
			observation.TransactionHash.Hex(), observation.StableActionPath, string(observation.PayloadJSON), observation.ObservedAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return CommitResult{}, fmt.Errorf("insert observation: %w", err)
		}
		rows, err := insert.RowsAffected()
		if err != nil {
			return CommitResult{}, fmt.Errorf("observation rows affected: %w", err)
		}
		if rows == 0 {
			continue
		}
		offset, err := insert.LastInsertId()
		if err != nil {
			return CommitResult{}, fmt.Errorf("observation offset: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO feed_outbox(offset, payload_json) VALUES(?, ?)`, offset, string(observation.PayloadJSON)); err != nil {
			return CommitResult{}, fmt.Errorf("insert outbox: %w", err)
		}
		result.InsertedObservations++
		if offset > result.DurableEventOffset {
			result.DurableEventOffset = offset
		}
	}

	if result.DurableEventOffset == 0 {
		if err := tx.QueryRowContext(ctx, `SELECT durable_event_offset FROM feed_progress WHERE id = 1`).Scan(&result.DurableEventOffset); err != nil {
			return CommitResult{}, fmt.Errorf("read durable offset: %w", err)
		}
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if observedSequence == nil {
		if _, err := tx.ExecContext(ctx, `
UPDATE feed_progress
SET durable_event_offset = CASE WHEN durable_event_offset < ? THEN ? ELSE durable_event_offset END,
    updated_at = ?
WHERE id = 1`, result.DurableEventOffset, result.DurableEventOffset, stamp); err != nil {
			return CommitResult{}, fmt.Errorf("update durable feed offset: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
UPDATE feed_progress
SET observed_sequence = CASE WHEN observed_sequence IS NULL OR observed_sequence < ? THEN ? ELSE observed_sequence END,
    durable_event_offset = CASE WHEN durable_event_offset < ? THEN ? ELSE durable_event_offset END,
    updated_at = ?
WHERE id = 1`, *observedSequence, *observedSequence, result.DurableEventOffset, result.DurableEventOffset, stamp); err != nil {
			return CommitResult{}, fmt.Errorf("update feed progress: %w", err)
		}
	}
	if registrySnapshot != nil {
		payload, err := json.Marshal(registrySnapshot)
		if err != nil {
			return CommitResult{}, fmt.Errorf("marshal registry snapshot: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO feed_registry_snapshot(id, payload_json, durable_event_offset, updated_at)
VALUES(1, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  payload_json = excluded.payload_json,
  durable_event_offset = excluded.durable_event_offset,
  updated_at = excluded.updated_at`, string(payload), result.DurableEventOffset, stamp); err != nil {
			return CommitResult{}, fmt.Errorf("persist registry snapshot: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return CommitResult{}, fmt.Errorf("commit feed transaction: %w", err)
	}
	committed = true
	return result, nil
}

func (s *Store) LoadRegistrySnapshot(ctx context.Context) (parser.RegistrySnapshot, bool, error) {
	if s == nil || s.db == nil || ctx == nil {
		return parser.RegistrySnapshot{}, false, ErrFeedStoreUnavailable
	}
	var payload string
	var snapshotOffset int64
	err := s.db.QueryRowContext(ctx, `SELECT payload_json, durable_event_offset FROM feed_registry_snapshot WHERE id = 1`).Scan(&payload, &snapshotOffset)
	if errors.Is(err, sql.ErrNoRows) {
		return parser.RegistrySnapshot{}, false, nil
	}
	if err != nil {
		return parser.RegistrySnapshot{}, false, fmt.Errorf("read registry snapshot: %w", err)
	}
	var progressOffset int64
	if err := s.db.QueryRowContext(ctx, `SELECT durable_event_offset FROM feed_progress WHERE id = 1`).Scan(&progressOffset); err != nil {
		return parser.RegistrySnapshot{}, false, fmt.Errorf("read registry snapshot progress: %w", err)
	}
	if snapshotOffset < 0 || snapshotOffset > progressOffset {
		return parser.RegistrySnapshot{}, false, fmt.Errorf("registry snapshot offset %d exceeds durable offset %d", snapshotOffset, progressOffset)
	}
	var snapshot parser.RegistrySnapshot
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		return parser.RegistrySnapshot{}, false, fmt.Errorf("decode registry snapshot: %w", err)
	}
	return snapshot, true, nil
}

func (s *Store) Progress(ctx context.Context) (Progress, error) {
	if s == nil || s.db == nil || ctx == nil {
		return Progress{}, ErrFeedStoreUnavailable
	}
	var sequence sql.NullInt64
	var degraded int
	var reason sql.NullString
	var updated string
	var progress Progress
	if err := s.db.QueryRowContext(ctx, `
SELECT observed_sequence, durable_event_offset, degraded, degraded_reason, updated_at
FROM feed_progress WHERE id = 1`).Scan(&sequence, &progress.DurableEventOffset, &degraded, &reason, &updated); err != nil {
		return Progress{}, fmt.Errorf("read feed progress: %w", err)
	}
	if sequence.Valid {
		value := uint64(sequence.Int64)
		progress.ObservedSequence = &value
	}
	progress.Degraded = degraded != 0
	if reason.Valid {
		progress.DegradedReason = reason.String
	}
	parsed, err := time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return Progress{}, fmt.Errorf("parse feed progress timestamp: %w", err)
	}
	progress.UpdatedAt = parsed
	return progress, nil
}

func (s *Store) ResumeSequence(ctx context.Context) (*uint64, error) {
	progress, err := s.Progress(ctx)
	if err != nil {
		return nil, err
	}
	if progress.ObservedSequence == nil {
		return nil, nil
	}
	resume := uint64(0)
	if *progress.ObservedSequence > 0 {
		resume = *progress.ObservedSequence - 1
	}
	return &resume, nil
}

func (s *Store) ReadOutboxAfter(ctx context.Context, after int64, limit int) ([]OutboxItem, error) {
	if s == nil || s.db == nil || ctx == nil {
		return nil, ErrFeedStoreUnavailable
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT offset, payload_json FROM feed_outbox WHERE offset > ? ORDER BY offset LIMIT ?`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("query feed outbox: %w", err)
	}
	defer rows.Close()
	items := make([]OutboxItem, 0, limit)
	for rows.Next() {
		var item OutboxItem
		var payload string
		if err := rows.Scan(&item.Offset, &payload); err != nil {
			return nil, fmt.Errorf("scan feed outbox: %w", err)
		}
		item.PayloadJSON = []byte(payload)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feed outbox: %w", err)
	}
	return items, nil
}

func (s *Store) MarkDegraded(ctx context.Context, reason string) error {
	if s == nil || s.db == nil || ctx == nil {
		return ErrFeedStoreUnavailable
	}
	_, err := s.db.ExecContext(ctx, `UPDATE feed_progress SET degraded = 1, degraded_reason = ?, updated_at = ? WHERE id = 1`, reason, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("mark feed degraded: %w", err)
	}
	return nil
}

func (s *Store) ClearDegraded(ctx context.Context) error {
	if s == nil || s.db == nil || ctx == nil {
		return ErrFeedStoreUnavailable
	}
	_, err := s.db.ExecContext(ctx, `UPDATE feed_progress SET degraded = 0, degraded_reason = NULL, updated_at = ? WHERE id = 1`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("clear feed degraded: %w", err)
	}
	return nil
}
