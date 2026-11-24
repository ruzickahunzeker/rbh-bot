// Package telegram implements the Step 3B multi-chain Telegram command router,
// strict allowlist enforcement (never leaking private keys or internal secrets),
// exact percentage-to-amount_raw sell conversion, and trade-updates 410 Gone reconciliation.
package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

var (
	ErrUnauthorizedChat  = errors.New("telegram chat_id not in allowlist")
	ErrCursorOutOfRange  = errors.New("trade-updates cursor out of range (410 Gone)")
)

// Backend abstracts local UDS calls to trade-<chain> and feed-<chain> services.
type Backend interface {
	ListWallets(ctx context.Context, chainID uint64) ([]string, error)
	ReadPosition(ctx context.Context, chainID uint64, walletID string, token common.Address) (tokenBalanceRaw *big.Int, nativeBalanceRaw *big.Int, err error)
	SubmitManualTrade(ctx context.Context, chainID uint64, walletID, side string, token common.Address, amountRaw *big.Int) (requestID string, err error)
	AddWatchedWallet(ctx context.Context, chainID uint64, trader common.Address) error
	ConfigureCopyStrategy(ctx context.Context, chainID uint64, trader common.Address, amountRaw *big.Int) error
}

// TradeUpdateFeed abstracts GET /v1/trade-updates?after_seq=... plus 410 Gone reconciliation.
type TradeUpdateFeed interface {
	FetchUpdates(ctx context.Context, chainID uint64, afterSeq int64, limit int) (events []UpdateNotice, nextSeq int64, err error)
	ReconcileOpenJobs(ctx context.Context, chainID uint64) (safeResetSeq int64, err error)
}

type UpdateNotice struct {
	EventSeq      int64
	OperationKind string
	RequestID     string
	State         string
}

type Router struct {
	db      *sql.DB
	backend Backend
	now     func() time.Time
}

func NewRouter(database *storage.Database, backend Backend, now func() time.Time) (*Router, error) {
	if database == nil || backend == nil {
		return nil, errors.New("bot database and backend are required")
	}
	db, err := database.SQLDB(storage.BotOwner)
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Router{db: db, backend: backend, now: now}, nil
}

// AllowChat registers or updates a whitelisted Telegram chat_id in bot.db.
func (r *Router) AllowChat(ctx context.Context, chatID int64, username, role string, activeChainID uint64, activeWalletID string) error {
	if chatID == 0 || activeChainID == 0 {
		return errors.New("invalid chat allowlist entry")
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO telegram_allowlist(chat_id, username, role, active_chain_id, active_wallet_id, enabled, updated_at)
		 VALUES(?,?,?,?,?,1,?)
		 ON CONFLICT(chat_id) DO UPDATE SET username=excluded.username, role=excluded.role, active_chain_id=excluded.active_chain_id, active_wallet_id=excluded.active_wallet_id, enabled=1, updated_at=excluded.updated_at`,
		chatID, username, role, activeChainID, activeWalletID, r.now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

// HandleCommand authenticates chatID against telegram_allowlist and dispatches multi-chain commands.
// Non-whitelisted chats fail closed with ErrUnauthorizedChat and zero internal data exposure (AT-23).
func (r *Router) HandleCommand(ctx context.Context, chatID int64, text string) (string, error) {
	var role, walletID string
	var chainID uint64
	var enabled int
	err := r.db.QueryRowContext(ctx,
		`SELECT role, active_chain_id, active_wallet_id, enabled FROM telegram_allowlist WHERE chat_id = ?`,
		chatID,
	).Scan(&role, &chainID, &walletID, &enabled)
	if errors.Is(err, sql.ErrNoRows) || enabled != 1 {
		return "", ErrUnauthorizedChat
	}
	if err != nil {
		return "", err
	}

	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 {
		return "Commands: /chain <rbh|bsc>, /wallets, /positions <token>, /buy <token> <wei>, /sell <token> <pct%|amount_raw>, /watch <wallet>, /copy <wallet> <wei>", nil
	}
	cmd := strings.ToLower(parts[0])
	switch cmd {
	case "/chain":
		if len(parts) != 2 {
			return "Usage: /chain <rbh|bsc>", nil
		}
		var newChain uint64
		switch strings.ToLower(parts[1]) {
		case "rbh", "robinhood", "4663":
			newChain = 4663
		case "bsc", "56":
			newChain = 56
		default:
			return "Unsupported chain. Use rbh or bsc.", nil
		}
		if _, err := r.db.ExecContext(ctx,
			`UPDATE telegram_allowlist SET active_chain_id = ?, updated_at = ? WHERE chat_id = ?`,
			newChain, r.now().UTC().Format(time.RFC3339Nano), chatID,
		); err != nil {
			return "", err
		}
		return fmt.Sprintf("Active chain switched to %s (chain_id=%d).", strings.ToLower(parts[1]), newChain), nil

	case "/wallets":
		wallets, err := r.backend.ListWallets(ctx, chainID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Chain %d wallets: %s (active=%s)", chainID, strings.Join(wallets, ", "), walletID), nil

	case "/positions":
		if len(parts) != 2 || !common.IsHexAddress(parts[1]) {
			return "Usage: /positions <token_address>", nil
		}
		tokBal, natBal, err := r.backend.ReadPosition(ctx, chainID, walletID, common.HexToAddress(parts[1]))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Chain %d wallet=%s token=%s token_raw=%s native_wei=%s", chainID, walletID, strings.ToLower(parts[1]), tokBal.String(), natBal.String()), nil

	case "/buy":
		if role != "operator" {
			return "Permission denied: operator role required.", nil
		}
		if len(parts) != 3 || !common.IsHexAddress(parts[1]) {
			return "Usage: /buy <token_address> <native_amount_wei>", nil
		}
		amt, ok := new(big.Int).SetString(parts[2], 10)
		if !ok || amt.Sign() <= 0 {
			return "Invalid positive amount_raw.", nil
		}
		reqID, err := r.backend.SubmitManualTrade(ctx, chainID, walletID, "buy", common.HexToAddress(parts[1]), amt)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("BUY submitted on chain %d: request_id=%s", chainID, reqID), nil

	case "/sell":
		if role != "operator" {
			return "Permission denied: operator role required.", nil
		}
		if len(parts) != 3 || !common.IsHexAddress(parts[1]) {
			return "Usage: /sell <token_address> <pct%|amount_raw>", nil
		}
		tokenAddr := common.HexToAddress(parts[1])
		var sellAmt *big.Int
		if strings.HasSuffix(parts[2], "%") {
			pct, err := strconv.ParseUint(strings.TrimSuffix(parts[2], "%"), 10, 64)
			if err != nil || pct == 0 || pct > 100 {
				return "Sell percentage must be between 1% and 100%.", nil
			}
			tokBal, _, err := r.backend.ReadPosition(ctx, chainID, walletID, tokenAddr)
			if err != nil {
				return "", err
			}
			if tokBal == nil || tokBal.Sign() <= 0 {
				return "Zero token balance to sell.", nil
			}
			sellAmt = new(big.Int).Mul(tokBal, new(big.Int).SetUint64(pct))
			sellAmt.Div(sellAmt, big.NewInt(100))
		} else {
			amt, ok := new(big.Int).SetString(parts[2], 10)
			if !ok || amt.Sign() <= 0 {
				return "Invalid sell amount_raw.", nil
			}
			sellAmt = amt
		}
		reqID, err := r.backend.SubmitManualTrade(ctx, chainID, walletID, "sell", tokenAddr, sellAmt)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("SELL submitted on chain %d: amount_raw=%s request_id=%s", chainID, sellAmt.String(), reqID), nil

	case "/watch":
		if role != "operator" {
			return "Permission denied: operator role required.", nil
		}
		if len(parts) != 2 || !common.IsHexAddress(parts[1]) {
			return "Usage: /watch <wallet_address>", nil
		}
		if err := r.backend.AddWatchedWallet(ctx, chainID, common.HexToAddress(parts[1])); err != nil {
			return "", err
		}
		return fmt.Sprintf("Watched wallet %s synced on chain %d.", strings.ToLower(parts[1]), chainID), nil

	case "/copy":
		if role != "operator" {
			return "Permission denied: operator role required.", nil
		}
		if len(parts) != 3 || !common.IsHexAddress(parts[1]) {
			return "Usage: /copy <wallet_address> <fixed_input_wei>", nil
		}
		amt, ok := new(big.Int).SetString(parts[2], 10)
		if !ok || amt.Sign() <= 0 {
			return "Invalid fixed_input_wei.", nil
		}
		if err := r.backend.ConfigureCopyStrategy(ctx, chainID, common.HexToAddress(parts[1]), amt); err != nil {
			return "", err
		}
		return fmt.Sprintf("Copy strategy enabled on chain %d for %s (%s wei).", chainID, strings.ToLower(parts[1]), amt.String()), nil

	default:
		return "Unknown command.", nil
	}
}

// PollTradeUpdates advances the durable consumer cursor for (chainID, consumerID).
// When trade-service returns ErrCursorOutOfRange (HTTP 410 Gone), it reconciles open jobs
// before resetting the durable cursor to safeResetSeq (AT-26: zero silent event loss).
func (r *Router) PollTradeUpdates(ctx context.Context, chainID uint64, consumerID string, feed TradeUpdateFeed, limit int) ([]UpdateNotice, bool, error) {
	if feed == nil || chainID == 0 || strings.TrimSpace(consumerID) == "" {
		return nil, false, errors.New("invalid poll parameters")
	}
	var cursor int64
	err := r.db.QueryRowContext(ctx,
		`SELECT last_event_seq FROM trade_update_consumer_cursors WHERE chain_id = ? AND consumer_id = ?`,
		chainID, consumerID,
	).Scan(&cursor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	events, nextSeq, err := feed.FetchUpdates(ctx, chainID, cursor, limit)
	if errors.Is(err, ErrCursorOutOfRange) {
		safeSeq, recErr := feed.ReconcileOpenJobs(ctx, chainID)
		if recErr != nil {
			return nil, true, recErr
		}
		if err := r.saveCursor(ctx, chainID, consumerID, safeSeq); err != nil {
			return nil, true, err
		}
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if nextSeq > cursor {
		if err := r.saveCursor(ctx, chainID, consumerID, nextSeq); err != nil {
			return nil, false, err
		}
	}
	return events, false, nil
}

func (r *Router) saveCursor(ctx context.Context, chainID uint64, consumerID string, seq int64) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO trade_update_consumer_cursors(chain_id, consumer_id, last_event_seq, updated_at)
		 VALUES(?,?,?,?)
		 ON CONFLICT(chain_id, consumer_id) DO UPDATE SET last_event_seq = excluded.last_event_seq, updated_at = excluded.updated_at`,
		chainID, consumerID, seq, r.now().UTC().Format(time.RFC3339Nano),
	)
	return err
}
