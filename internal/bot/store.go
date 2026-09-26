package bot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/feed"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

var ErrBotStoreUnavailable = errors.New("bot store unavailable")

type Store struct {
	db *sql.DB
}

type ProcessResult struct {
	Duplicate          bool
	Disposition        string
	OperationIntents   int
	RetractedIntents   int
	DurableEventOffset int64
}

func NewStore(database *storage.Database) (*Store, error) {
	if database == nil {
		return nil, ErrBotStoreUnavailable
	}
	db, err := database.SQLDB(storage.BotOwner)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) UpsertWatchedWallet(ctx context.Context, wallet WatchedWallet) error {
	if s == nil || s.db == nil || ctx == nil {
		return ErrBotStoreUnavailable
	}
	if err := wallet.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO watched_wallets(id, chain_id, address, enabled) VALUES(?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET chain_id=excluded.chain_id, address=excluded.address, enabled=excluded.enabled`,
		wallet.ID, wallet.ChainID, strings.ToLower(wallet.Address.Hex()), boolInt(wallet.Enabled))
	if err != nil {
		return fmt.Errorf("upsert watched wallet: %w", err)
	}
	return nil
}

func (s *Store) UpsertStrategy(ctx context.Context, strategy Strategy) error {
	if s == nil || s.db == nil || ctx == nil {
		return ErrBotStoreUnavailable
	}
	if err := strategy.Validate(); err != nil {
		return err
	}
	config, err := encodeConfig(strategy.Config)
	if err != nil {
		return fmt.Errorf("encode strategy: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin strategy update: %w", err)
	}
	defer tx.Rollback()
	var existingWallet, existingConfig string
	var existingVersion uint64
	var existingEnabled int
	err = tx.QueryRowContext(ctx, `SELECT watched_wallet_id, version, enabled, config_json FROM strategies WHERE id=?`, strategy.ID).
		Scan(&existingWallet, &existingVersion, &existingEnabled, &existingConfig)
	if err == nil {
		if strategy.Version < existingVersion || (strategy.Version == existingVersion &&
			(existingWallet != strategy.WatchedWalletID || existingEnabled != boolInt(strategy.Enabled) || existingConfig != config)) {
			return ErrStrategyConflict
		}
		if strategy.Version == existingVersion {
			return tx.Commit()
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read strategy version: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO strategies(id, watched_wallet_id, version, enabled, config_json) VALUES(?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET watched_wallet_id=excluded.watched_wallet_id, version=excluded.version, enabled=excluded.enabled, config_json=excluded.config_json`,
		strategy.ID, strategy.WatchedWalletID, strategy.Version, boolInt(strategy.Enabled), config)
	if err != nil {
		return fmt.Errorf("upsert strategy: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit strategy update: %w", err)
	}
	return nil
}

func (s *Store) DurableEventOffset(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil || ctx == nil {
		return 0, ErrBotStoreUnavailable
	}
	var offset int64
	if err := s.db.QueryRowContext(ctx, `SELECT durable_event_offset FROM bot_progress WHERE id=1`).Scan(&offset); err != nil {
		return 0, fmt.Errorf("read bot progress: %w", err)
	}
	return offset, nil
}

type eventEnvelope struct {
	Type                  string `json:"type"`
	IntentKind            string `json:"intent_kind"`
	Sender                string `json:"sender"`
	Token                 string `json:"token"`
	CurrencyIn            string `json:"currency_in"`
	CurrencyOut           string `json:"currency_out"`
	Confirmed             bool   `json:"confirmed"`
	OriginalObservationID string `json:"original_observation_id"`
}

func (s *Store) ProcessFeedEvent(ctx context.Context, item feed.OutboxItem, receivedAt time.Time) (ProcessResult, error) {
	if s == nil || s.db == nil || ctx == nil {
		return ProcessResult{}, ErrBotStoreUnavailable
	}
	if item.Offset <= 0 || item.ObservationID == "" || item.TransactionHash == (common.Hash{}) || !json.Valid(item.PayloadJSON) || receivedAt.IsZero() {
		return ProcessResult{}, ErrInvalidFeedEvent
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("begin bot event: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT durable_event_offset FROM bot_progress WHERE id=1`).Scan(&current); err != nil {
		return ProcessResult{}, fmt.Errorf("read bot event offset: %w", err)
	}
	if item.Offset <= current {
		var observationID string
		err := tx.QueryRowContext(ctx, `SELECT observation_id FROM signal_inbox WHERE feed_offset=?`, item.Offset).Scan(&observationID)
		if err != nil || observationID != item.ObservationID {
			return ProcessResult{}, fmt.Errorf("feed replay conflict at offset %d", item.Offset)
		}
		if err := tx.Commit(); err != nil {
			return ProcessResult{}, err
		}
		committed = true
		return ProcessResult{Duplicate: true, Disposition: "duplicate", DurableEventOffset: current}, nil
	}
	if item.Offset != current+1 {
		return ProcessResult{}, fmt.Errorf("%w: have %d received %d", ErrFeedGap, current, item.Offset)
	}

	var event eventEnvelope
	if err := json.Unmarshal(item.PayloadJSON, &event); err != nil {
		return ProcessResult{}, ErrInvalidFeedEvent
	}
	result := ProcessResult{Disposition: "unsupported", DurableEventOffset: item.Offset}
	if event.Type == "observation_retracted" {
		if event.OriginalObservationID == "" {
			return ProcessResult{}, ErrInvalidFeedEvent
		}
		changed, err := tx.ExecContext(ctx, `
UPDATE operation_intents SET status='retracted', retracted_at=?
WHERE source_observation_id=? AND status='created'`, receivedAt.UTC().Format(time.RFC3339Nano), event.OriginalObservationID)
		if err != nil {
			return ProcessResult{}, fmt.Errorf("retract operation intents: %w", err)
		}
		rows, err := changed.RowsAffected()
		if err != nil {
			return ProcessResult{}, err
		}
		result.RetractedIntents = int(rows)
		result.Disposition = "retraction_applied"
	} else if event.Type == "transaction_intent" {
		created, disposition, err := processTransactionIntent(ctx, tx, item, event, receivedAt)
		if err != nil {
			return ProcessResult{}, err
		}
		result.OperationIntents = created
		result.Disposition = disposition
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO signal_inbox(event_id, received_at, payload_json, feed_offset, observation_id, disposition)
VALUES(?, ?, ?, ?, ?, ?)`, item.ObservationID, receivedAt.UTC().Format(time.RFC3339Nano), string(item.PayloadJSON), item.Offset, item.ObservationID, result.Disposition); err != nil {
		return ProcessResult{}, fmt.Errorf("insert signal inbox: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bot_progress SET durable_event_offset=?, updated_at=? WHERE id=1`, item.Offset, receivedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return ProcessResult{}, fmt.Errorf("update bot progress: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ProcessResult{}, fmt.Errorf("commit bot event: %w", err)
	}
	committed = true
	return result, nil
}

func processTransactionIntent(ctx context.Context, tx *sql.Tx, item feed.OutboxItem, event eventEnvelope, receivedAt time.Time) (int, string, error) {
	if !common.IsHexAddress(event.Sender) {
		return 0, "ineligible", nil
	}
	rows, err := tx.QueryContext(ctx, `
SELECT s.id, s.watched_wallet_id, s.version, s.config_json
FROM strategies s JOIN watched_wallets w ON w.id=s.watched_wallet_id
WHERE s.enabled=1 AND w.enabled=1 AND w.chain_id=4663 AND lower(w.address)=lower(?)
ORDER BY s.id`, event.Sender)
	if err != nil {
		return 0, "", fmt.Errorf("query matching strategies: %w", err)
	}
	defer rows.Close()
	strategies := make([]Strategy, 0)
	for rows.Next() {
		var strategy Strategy
		var config string
		if err := rows.Scan(&strategy.ID, &strategy.WatchedWalletID, &strategy.Version, &config); err != nil {
			return 0, "", err
		}
		strategy.Enabled = true
		if err := json.Unmarshal([]byte(config), &strategy.Config); err != nil || strategy.Validate() != nil {
			return 0, "", fmt.Errorf("invalid persisted strategy %s", strategy.ID)
		}
		strategies = append(strategies, strategy)
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}
	created := 0
	for _, strategy := range strategies {
		intent, eligible := applyPolicy(strategy, item, event, receivedAt)
		if !eligible {
			continue
		}
		encoded, err := json.Marshal(intent)
		if err != nil {
			return 0, "", err
		}
		result, err := tx.ExecContext(ctx, `
INSERT INTO operation_intents(id, idempotency_key, strategy_id, source_event_id, source_observation_id, source_tx_hash, kind, token, amount_mode, amount_value, policy_version, deadline_capability, expires_at, status, payload_json, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'created', ?, ?)
ON CONFLICT(id) DO NOTHING`, intent.ID, intent.IdempotencyKey, intent.StrategyID, intent.SourceEventID, intent.SourceObservationID,
			intent.SourceTxHash, intent.Kind, intent.Token, intent.AmountMode, intent.AmountValue, intent.PolicyVersion, intent.DeadlineCapability, intent.ExpiresAt, string(encoded), receivedAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return 0, "", fmt.Errorf("insert operation intent: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, "", err
		}
		created += int(count)
	}
	if created > 0 {
		return created, "operation_intent_created", nil
	}
	if len(strategies) == 0 {
		return 0, "no_matching_strategy", nil
	}
	return 0, "policy_blocked", nil
}

func applyPolicy(strategy Strategy, item feed.OutboxItem, event eventEnvelope, now time.Time) (OperationIntent, bool) {
	if strategy.Config.RequireConfirmed && !event.Confirmed {
		return OperationIntent{}, false
	}
	var kind, token, mode, amount string
	switch event.IntentKind {
	case "buy":
		if !strategy.Config.CopyBuys {
			return OperationIntent{}, false
		}
		kind, token, mode, amount = "copy_buy", event.CurrencyOut, "fixed_input", strategy.Config.FixedBuyAmount
	case "sell":
		if !strategy.Config.CopySells {
			return OperationIntent{}, false
		}
		kind, token, mode, amount = "copy_sell", event.CurrencyIn, "balance_bps", fmt.Sprintf("%d", strategy.Config.SellBPS)
	default:
		return OperationIntent{}, false
	}
	if !common.IsHexAddress(token) || common.HexToAddress(token) == (common.Address{}) {
		return OperationIntent{}, false
	}
	id := deterministicIntentID(strategy, item.ObservationID, kind)
	expiresAt, ok := canonicalExpiry(now, strategy.Config.ApplicationTTLSeconds)
	if !ok {
		return OperationIntent{}, false
	}
	return OperationIntent{
		ID: id, IdempotencyKey: id, StrategyID: strategy.ID, WatchedWalletID: strategy.WatchedWalletID, SourceEventID: item.ObservationID,
		SourceObservationID: item.ObservationID, SourceTxHash: item.TransactionHash.Hex(), Kind: kind,
		Token: common.HexToAddress(token).Hex(), AmountMode: mode, AmountValue: amount,
		PolicyVersion: strategy.Version, Policy: strategy.Config, DeadlineCapability: DeadlineCapabilityApplicationTTL, ExpiresAt: expiresAt, Status: "created",
	}, true
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
