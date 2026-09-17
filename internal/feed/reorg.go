package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/0xfnzero/rbh-parser-sdk/feed/sequencer"
	"github.com/ethereum/go-ethereum/common"
)

type retractionPayload struct {
	SchemaVersion         int    `json:"schema_version"`
	Type                  string `json:"type"`
	Reason                string `json:"reason"`
	Sequence              uint64 `json:"sequence"`
	PreviousHash          string `json:"previous_hash"`
	ReplacementHash       string `json:"replacement_hash"`
	OriginalOffset        int64  `json:"original_offset"`
	OriginalObservationID string `json:"original_observation_id"`
	TransactionHash       string `json:"transaction_hash"`
}

type orphanCandidate struct {
	offset        int64
	observationID string
	txHash        common.Hash
}

// RetractSequencerSequence records a compensating observation for every
// speculative intent orphaned by a sequencer reorg. Original rows are retained
// for audit and marked orphaned; compensation rows are published through the
// same durable outbox. Replaying the same reorg is idempotent.
func (s *Store) RetractSequencerSequence(ctx context.Context, reorg sequencer.Reorg) (int, error) {
	if s == nil || s.db == nil || ctx == nil {
		return 0, ErrFeedStoreUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin reorg compensation: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	rows, err := tx.QueryContext(ctx, `
SELECT offset, observation_id, tx_hash
FROM feed_events
WHERE source = ? AND source_sequence = ? AND inclusion = 'observed'
  AND stable_action_path LIKE 'intent/%'
ORDER BY offset`, string(SourceSequencer), strconv.FormatUint(reorg.SequenceNumber, 10))
	if err != nil {
		return 0, fmt.Errorf("query orphaned sequencer observations: %w", err)
	}
	candidates := make([]orphanCandidate, 0)
	for rows.Next() {
		var candidate orphanCandidate
		var txHash string
		if err := rows.Scan(&candidate.offset, &candidate.observationID, &txHash); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan orphaned sequencer observation: %w", err)
		}
		if len(txHash) != 66 || !strings.HasPrefix(txHash, "0x") {
			_ = rows.Close()
			return 0, fmt.Errorf("invalid stored transaction hash %q", txHash)
		}
		candidate.txHash = common.HexToHash(txHash)
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("iterate orphaned sequencer observations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close orphaned sequencer rows: %w", err)
	}

	stamp := time.Now().UTC()
	inserted := 0
	var highestOffset int64
	for _, candidate := range candidates {
		update, err := tx.ExecContext(ctx, `UPDATE feed_events SET inclusion = 'orphaned' WHERE offset = ? AND inclusion = 'observed'`, candidate.offset)
		if err != nil {
			return 0, fmt.Errorf("mark observation orphaned: %w", err)
		}
		changed, err := update.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("orphan rows affected: %w", err)
		}
		if changed == 0 {
			continue
		}

		payload := retractionPayload{
			SchemaVersion:         1,
			Type:                  "observation_retracted",
			Reason:                "sequencer_reorg",
			Sequence:              reorg.SequenceNumber,
			PreviousHash:          reorg.PreviousHash.Hex(),
			ReplacementHash:       reorg.ReplacementHash.Hex(),
			OriginalOffset:        candidate.offset,
			OriginalObservationID: candidate.observationID,
			TransactionHash:       candidate.txHash.Hex(),
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, fmt.Errorf("marshal reorg compensation: %w", err)
		}
		observation := Observation{
			ChainID:          ChainID,
			Source:           SourceSequencer,
			SourceSequence:   reorg.SequenceNumber,
			TransactionHash:  candidate.txHash,
			StableActionPath: fmt.Sprintf("reorg/%020d/%020d", reorg.SequenceNumber, candidate.offset),
			PayloadJSON:      encoded,
			ObservedAt:       stamp,
		}
		if err := observation.Validate(); err != nil {
			return 0, err
		}
		result, err := tx.ExecContext(ctx, `
INSERT INTO feed_events(observation_id, chain_id, source, source_sequence, tx_hash, stable_action_path, payload_json, observed_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(observation_id) DO NOTHING`, observation.ID(), observation.ChainID, string(observation.Source), strconv.FormatUint(observation.SourceSequence, 10), observation.TransactionHash.Hex(), observation.StableActionPath, string(observation.PayloadJSON), observation.ObservedAt.Format(time.RFC3339Nano))
		if err != nil {
			return 0, fmt.Errorf("insert reorg compensation: %w", err)
		}
		created, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("reorg compensation rows affected: %w", err)
		}
		if created == 0 {
			continue
		}
		offset, err := result.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("reorg compensation offset: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO feed_outbox(offset, payload_json) VALUES(?, ?)`, offset, string(encoded)); err != nil {
			return 0, fmt.Errorf("insert reorg outbox: %w", err)
		}
		inserted++
		if offset > highestOffset {
			highestOffset = offset
		}
	}

	reason := fmt.Sprintf("sequencer_reorg sequence=%d previous=%s replacement=%s", reorg.SequenceNumber, reorg.PreviousHash.Hex(), reorg.ReplacementHash.Hex())
	if _, err := tx.ExecContext(ctx, `
UPDATE feed_progress
SET observed_sequence = CASE
      WHEN observed_sequence IS NULL OR observed_sequence > ? THEN ?
      ELSE observed_sequence
    END,
    durable_event_offset = CASE
      WHEN durable_event_offset < ? THEN ?
      ELSE durable_event_offset
    END,
    degraded = 1,
    degraded_reason = ?,
    updated_at = ?
WHERE id = 1`, reorg.SequenceNumber, reorg.SequenceNumber, highestOffset, highestOffset, reason, stamp.Format(time.RFC3339Nano)); err != nil {
		return 0, fmt.Errorf("update reorg progress: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit reorg compensation: %w", err)
	}
	committed = true
	return inserted, nil
}
