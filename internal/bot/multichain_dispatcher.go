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
	Side            string // strictly "buy" | "sell"
	Stage           string // "intent" | "included"
	FirstObservedAt time.Time
}

// MultiChainIngestResult reports durable idempotent processing outcome.
type MultiChainIngestResult struct {
	DuplicateOffset  bool
	DuplicateTrigger bool
	Disposition      string // "QUEUED" | "EXPIRED_SKIPPED" | "OBSERVATION_ONLY" | "DUPLICATE_TRIGGER"
	DecisionID       string
	IdempotencyKey   string
}

const fixedUTCTimestamp = "2006-01-02T15:04:05.000000000Z"

func formatFixedUTC(t time.Time) string {
	return t.UTC().Format(fixedUTCTimestamp)
}

func canonicalHash128(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = fmt.Fprintf(h, "%d:%s|", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
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
	if sig.ChainID == 0 || strings.TrimSpace(sig.FeedInstanceID) == "" || sig.FeedOffset <= 0 || sig.ObservationID == "" ||
		strings.TrimSpace(sig.TxHash) == "" || strings.TrimSpace(sig.TraderAddress) == "" || strings.TrimSpace(sig.TokenAddress) == "" {
		return MultiChainIngestResult{}, errors.New("invalid multi-chain signal")
	}
	var actionKind string
	switch strings.ToLower(strings.TrimSpace(sig.Side)) {
	case "buy":
		actionKind = "copy_buy"
	case "sell":
		actionKind = "copy_sell"
	default:
		return MultiChainIngestResult{}, fmt.Errorf("invalid signal side %q: must be buy or sell", sig.Side)
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

	nowStr := formatFixedUTC(now)
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

	chainStr := fmt.Sprintf("%d", sig.ChainID)
	decisionID := "dec-" + canonicalHash128(chainStr, principalID, strategyID, triggerID, actionKind)
	idemKey := "copy-" + canonicalHash128(chainStr, decisionID, walletID)

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO copy_signal_decisions(decision_id, principal_id, strategy_id, chain_id, trigger_id, action_kind, wallet_id, token, trade_idempotency_key, disposition, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		decisionID, principalID, strategyID, sig.ChainID, triggerID, actionKind, walletID, strings.ToLower(sig.TokenAddress), idemKey, disposition, nowStr,
	); err != nil {
		return MultiChainIngestResult{}, fmt.Errorf("insert copy_signal_decisions: %w", err)
	}
	if disposition == "QUEUED" {
		jobID := "job-" + canonicalHash128(chainStr, decisionID, walletID, "submission")
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

func retryBackoff(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	d := time.Second << attempts
	if d > 30*time.Second || d <= 0 {
		return 30 * time.Second
	}
	return d
}

// DispatchNextForWallet strictly serializes per-(chain_id, wallet_id) dispatch:
//  1. Refuses to start a new job while another job for (chain_id, wallet_id) is DISPATCHING or SUBMITTED.
//  2. Atomically claims the oldest eligible PENDING job (next_retry_at <= now) as DISPATCHING before RPC/UDS I/O.
//  3. On retryable error (409 WALLET_LOCKED / 503 RPC_STATE_LAGGING), preserves the exact immutable
//     Idempotency-Key with exponential backoff up to max_attempts.
func (s *Store) DispatchNextForWallet(ctx context.Context, chainID uint64, walletID string, submitter TradeSubmitter, now time.Time) (dispatched bool, retryable bool, err error) {
	if s == nil || s.db == nil || submitter == nil {
		return false, false, ErrBotStoreUnavailable
	}
	nowUTC := now.UTC()
	nowStr := formatFixedUTC(nowUTC)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Strict per-wallet serial lane: never dispatch Job N+1 while Job N is still in-flight.
	var inFlight int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM submission_jobs WHERE chain_id = ? AND wallet_id = ? AND state IN ('DISPATCHING', 'SUBMITTED')`,
		chainID, walletID,
	).Scan(&inFlight); err != nil {
		return false, false, err
	}
	if inFlight > 0 {
		return false, true, nil
	}

	// Select ONLY the head-of-line oldest PENDING job so a newer job can never jump
	// ahead of an older job that is waiting on retry backoff.
	var jobID, idemKey, nextRetryAt string
	var attempts, maxAttempts int
	err = tx.QueryRowContext(ctx,
		`SELECT job_id, idempotency_key, attempts, max_attempts, next_retry_at FROM submission_jobs
		 WHERE chain_id = ? AND wallet_id = ? AND state = 'PENDING'
		 ORDER BY created_at ASC, rowid ASC LIMIT 1`, chainID, walletID,
	).Scan(&jobID, &idemKey, &attempts, &maxAttempts, &nextRetryAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if nextRetryAt != "" && nextRetryAt > nowStr {
		return false, true, nil
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE submission_jobs SET state = 'DISPATCHING', updated_at = ? WHERE job_id = ? AND state = 'PENDING'`,
		nowStr, jobID,
	)
	if err != nil {
		return false, false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, true, nil
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}

	// 2. Execute external IPC submission outside SQLite write lock.
	reqID, isRetryable, submitErr := submitter.SubmitCopyJob(ctx, chainID, walletID, idemKey)
	if submitErr != nil {
		nextAttempts := attempts + 1
		newState := "FAILED_TERMINAL"
		nextRetryStr := ""
		if isRetryable && nextAttempts < maxAttempts {
			newState = "PENDING" // preserve immutable Idempotency-Key for next retry!
			nextRetryStr = formatFixedUTC(nowUTC.Add(retryBackoff(attempts)))
		} else {
			isRetryable = false
		}
		_, _ = s.db.ExecContext(ctx,
			`UPDATE submission_jobs SET state = ?, attempts = ?, next_retry_at = ?, last_error = ?, updated_at = ? WHERE job_id = ?`,
			newState, nextAttempts, nextRetryStr, submitErr.Error(), nowStr, jobID,
		)
		return true, isRetryable, submitErr
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE submission_jobs SET state = 'SUBMITTED', trade_request_id = ?, attempts = ?, next_retry_at = '', last_error = '', updated_at = ? WHERE job_id = ?`,
		reqID, attempts+1, nowStr, jobID,
	); err != nil {
		return true, false, err
	}
	return true, false, nil
}

// MarkJobTerminal transitions a SUBMITTED (or PENDING) submission_job to COMPLETED or FAILED_TERMINAL,
// unlocking the per-(chain_id, wallet_id) serial dispatch lane for the next queued job.
func (s *Store) MarkJobTerminal(ctx context.Context, idempotencyKey, terminalState, lastErr string, now time.Time) error {
	if s == nil || s.db == nil {
		return ErrBotStoreUnavailable
	}
	if terminalState != "COMPLETED" && terminalState != "FAILED_TERMINAL" {
		return fmt.Errorf("invalid terminal job state %q", terminalState)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE submission_jobs SET state = ?, last_error = ?, updated_at = ? WHERE idempotency_key = ? AND state IN ('PENDING', 'DISPATCHING', 'SUBMITTED')`,
		terminalState, lastErr, formatFixedUTC(now), idempotencyKey,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
