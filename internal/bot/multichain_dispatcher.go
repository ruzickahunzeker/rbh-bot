package bot

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/feed"
)

// MultiChainSignal represents a normalized Swap event arriving from any chain's feed instance.
type MultiChainSignal struct {
	ChainID         uint64
	FeedInstanceID  string
	FeedOffset      int64
	ObservationID   string
	TxHash          string
	TraderAddress   string
	TokenAddress    string
	Side            string // "buy" | "sell"
	Stage           string // "intent" | "included"
	FirstObservedAt time.Time
}

// MultiChainIngestResult reports durable idempotent processing outcome.
type MultiChainIngestResult struct {
	DuplicateOffset  bool
	DuplicateTrigger bool
	Disposition      string // "QUEUED" | "EXPIRED_SKIPPED" | "OBSERVATION_ONLY"
	DecisionID       string
	IdempotencyKey   string
}

// LoadFeedCursor returns the durable offset for (chain_id, feed_instance_id).
func (s *Store) LoadFeedCursor(ctx context.Context, chainID uint64, feedInstanceID string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, ErrBotStoreUnavailable
	}
	var offset int64
	err := s.db.QueryRowContext(ctx,
		`SELECT durable_event_offset FROM bot_feed_cursors WHERE chain_id = ? AND feed_instance_id = ?`,
		chainID, feedInstanceID,
	).Scan(&offset)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return offset, err
}

// IngestMultiChainSignal atomically records signal_inbox per (chain_id, feed_instance_id, feed_offset),
// deduplicates copy-trade decisions by net economic trigger_id (across mempool+receipt AND multi-pool
// aggregator splits), enforces maxSignalAge freshness, creates a persistent submission_job with
// immutable trade_idempotency_key, and advances bot_feed_cursors.
func (s *Store) IngestMultiChainSignal(
	ctx context.Context,
	principalID, strategyID, walletID string,
	sig MultiChainSignal,
	maxSignalAge time.Duration,
	now time.Time,
) (MultiChainIngestResult, error) {
	if s == nil || s.db == nil {
		return MultiChainIngestResult{}, ErrBotStoreUnavailable
	}
	if sig.ChainID == 0 || strings.TrimSpace(sig.FeedInstanceID) == "" || sig.FeedOffset <= 0 || sig.ObservationID == "" {
		return MultiChainIngestResult{}, errors.New("invalid multi-chain signal")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MultiChainIngestResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Message-level idempotency per (chain_id, feed_instance_id, feed_offset).
	var existing int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM signal_inbox WHERE chain_id = ? AND feed_instance_id = ? AND feed_offset = ?`,
		sig.ChainID, sig.FeedInstanceID, sig.FeedOffset,
	).Scan(&existing); err != nil {
		return MultiChainIngestResult{}, err
	}
	if existing > 0 {
		return MultiChainIngestResult{DuplicateOffset: true}, nil
	}

	nowStr := now.UTC().Format(time.RFC3339Nano)
	actionKind := "copy_buy"
	if strings.EqualFold(sig.Side, "sell") {
		actionKind = "copy_sell"
	}
	triggerID := feed.ComputeTriggerID(sig.ChainID, sig.TxHash, sig.TraderAddress, sig.TokenAddress, sig.Side)

	// Record inbox row and advance independent (chain_id, feed_instance_id) cursor.
	inboxEventID := fmt.Sprintf("inb-%d-%s-%d", sig.ChainID, sig.FeedInstanceID, sig.FeedOffset)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO signal_inbox(event_id, received_at, payload_json, feed_offset, observation_id, disposition, chain_id, feed_instance_id)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		inboxEventID, nowStr, "{}", sig.FeedOffset, sig.ObservationID, "processed", sig.ChainID, sig.FeedInstanceID,
	); err != nil {
		return MultiChainIngestResult{}, fmt.Errorf("insert signal_inbox: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO bot_feed_cursors(chain_id, feed_instance_id, durable_event_offset, updated_at)
		 VALUES(?, ?, ?, ?)
		 ON CONFLICT(chain_id, feed_instance_id) DO UPDATE SET
		   durable_event_offset = CASE WHEN excluded.durable_event_offset > bot_feed_cursors.durable_event_offset THEN excluded.durable_event_offset ELSE bot_feed_cursors.durable_event_offset END,
		   updated_at = excluded.updated_at`,
		sig.ChainID, sig.FeedInstanceID, sig.FeedOffset, nowStr,
	); err != nil {
		return MultiChainIngestResult{}, fmt.Errorf("advance bot_feed_cursors: %w", err)
	}

	// Default Step 3A: only Stage=="included" triggers copy-trade; intent stage only warms cache.
	if !strings.EqualFold(sig.Stage, "included") {
		if err := tx.Commit(); err != nil {
			return MultiChainIngestResult{}, err
		}
		return MultiChainIngestResult{Disposition: "OBSERVATION_ONLY"}, nil
	}

	// 2. Cross-source / cross-pool-split net economic action dedupe by trigger_id.
	var existingDecisionID, existingKey string
	err = tx.QueryRowContext(ctx,
		`SELECT decision_id, trade_idempotency_key FROM copy_signal_decisions
		 WHERE principal_id = ? AND strategy_id = ? AND chain_id = ? AND trigger_id = ? AND action_kind = ?`,
		principalID, strategyID, sig.ChainID, triggerID, actionKind,
	).Scan(&existingDecisionID, &existingKey)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return MultiChainIngestResult{}, err
		}
		return MultiChainIngestResult{
			DuplicateTrigger: true,
			Disposition:      "DUPLICATE_TRIGGER",
			DecisionID:       existingDecisionID,
			IdempotencyKey:   existingKey,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return MultiChainIngestResult{}, err
	}

	// 3. Freshness check (max_signal_age_ms): never back-buy stale signals after disconnect.
	disposition := "QUEUED"
	if maxSignalAge > 0 && !sig.FirstObservedAt.IsZero() && now.Sub(sig.FirstObservedAt) > maxSignalAge {
		disposition = "EXPIRED_SKIPPED"
	}

	decHash := sha256.Sum256([]byte(principalID + "|" + strategyID + "|" + triggerID + "|" + actionKind))
	decisionID := "dec-" + hex.EncodeToString(decHash[:8])
	keyHash := sha256.Sum256([]byte(decisionID + "|" + walletID))
	idemKey := "copy-" + hex.EncodeToString(keyHash[:16])

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO copy_signal_decisions(decision_id, principal_id, strategy_id, chain_id, trigger_id, action_kind, wallet_id, token, trade_idempotency_key, disposition, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		decisionID, principalID, strategyID, sig.ChainID, triggerID, actionKind, walletID, strings.ToLower(sig.TokenAddress), idemKey, disposition, nowStr,
	); err != nil {
		return MultiChainIngestResult{}, fmt.Errorf("insert copy_signal_decisions: %w", err)
	}
	if disposition == "QUEUED" {
		jobID := "job-" + hex.EncodeToString(keyHash[:8])
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO submission_jobs(job_id, decision_id, chain_id, wallet_id, idempotency_key, state, created_at, updated_at)
			 VALUES(?,?,?,?,?,'PENDING',?,?)`,
			jobID, decisionID, sig.ChainID, walletID, idemKey, nowStr, nowStr,
		); err != nil {
			return MultiChainIngestResult{}, fmt.Errorf("insert submission_jobs: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return MultiChainIngestResult{}, err
	}
	return MultiChainIngestResult{
		Disposition:    disposition,
		DecisionID:     decisionID,
		IdempotencyKey: idemKey,
	}, nil
}

// TradeSubmitter abstracts POST /v1/trades for the serial per-wallet copy-trade dispatcher.
type TradeSubmitter interface {
	SubmitCopyJob(ctx context.Context, chainID uint64, walletID, idempotencyKey string) (requestID string, retryable bool, err error)
}

// DispatchNextForWallet dispatches the oldest PENDING submission_job for (chain_id, wallet_id).
// On retryable contention (409 WALLET_IN_FLIGHT or 503 RPC_STATE_LAGGING), it keeps the job
// PENDING with the EXACT same immutable Idempotency-Key for subsequent retry.
func (s *Store) DispatchNextForWallet(ctx context.Context, chainID uint64, walletID string, submitter TradeSubmitter, now time.Time) (dispatched bool, retryable bool, err error) {
	if s == nil || s.db == nil || submitter == nil {
		return false, false, ErrBotStoreUnavailable
	}
	var jobID, idemKey string
	err = s.db.QueryRowContext(ctx,
		`SELECT job_id, idempotency_key FROM submission_jobs
		 WHERE chain_id = ? AND wallet_id = ? AND state = 'PENDING'
		 ORDER BY created_at ASC LIMIT 1`, chainID, walletID,
	).Scan(&jobID, &idemKey)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	reqID, isRetryable, submitErr := submitter.SubmitCopyJob(ctx, chainID, walletID, idemKey)
	nowStr := now.UTC().Format(time.RFC3339Nano)
	if submitErr != nil {
		newState := "FAILED_TERMINAL"
		if isRetryable {
			newState = "PENDING" // preserve immutable Idempotency-Key for next retry!
		}
		_, _ = s.db.ExecContext(ctx,
			`UPDATE submission_jobs SET state = ?, attempts = attempts + 1, last_error = ?, updated_at = ? WHERE job_id = ?`,
			newState, submitErr.Error(), nowStr, jobID,
		)
		return true, isRetryable, submitErr
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE submission_jobs SET state = 'SUBMITTED', trade_request_id = ?, attempts = attempts + 1, last_error = '', updated_at = ? WHERE job_id = ?`,
		reqID, nowStr, jobID,
	); err != nil {
		return true, false, err
	}
	return true, false, nil
}
