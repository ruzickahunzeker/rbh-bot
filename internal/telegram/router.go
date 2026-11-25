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

// QuoteBackend is optionally implemented by Backend to serve /quote via local UDS POST /v1/quotes.
type QuoteBackend interface {
	QuoteToken(ctx context.Context, chainID uint64, walletID, protocol string, token common.Address, side string, amountRaw *big.Int) (string, error)
}

// HoneypotBackend is optionally implemented by Backend to serve /honeypot via eth_simulateV1.
type HoneypotBackend interface {
	CheckHoneypot(ctx context.Context, chainID uint64, token common.Address) (string, error)
}

// WatchlistManagerBackend is optionally implemented by Backend to serve /watchlist and /unwatch.
type WatchlistManagerBackend interface {
	ListWatchedWallets(ctx context.Context, chainID uint64) ([]string, error)
	RemoveWatchedWallet(ctx context.Context, chainID uint64, trader common.Address) error
}

// StatusBackend is optionally implemented by Backend to serve /status.
type StatusBackend interface {
	CheckStatus(ctx context.Context) (string, error)
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
	readDB  *sql.DB
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
	rdb, err := database.ReadDB(storage.BotOwner)
	if err != nil || rdb == nil {
		rdb = db
	}
	if now == nil {
		now = time.Now
	}
	return &Router{db: db, readDB: rdb, backend: backend, now: now}, nil
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
	err := r.readDB.QueryRowContext(ctx,
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
		return helpText(), nil
	}
	cmd := strings.ToLower(parts[0])
	switch cmd {
	case "/start", "/help":
		return helpText(), nil
	case "/quote":
		qb, ok := r.backend.(QuoteBackend)
		if !ok {
			return "Quote backend is not enabled.", nil
		}
		var proto, side string
		var tokStr, amtStr string
		switch len(parts) {
		case 3:
			// Shorthand: /quote <token> <amount_raw> -> default buy on chain's primary DEX
			tokStr, amtStr, side = parts[1], parts[2], "buy"
			proto = defaultProtocolForChain(chainID)
		case 5:
			// Full form: /quote <protocol> <token> <buy|sell> <amount_raw>
			proto, tokStr, side, amtStr = strings.ToLower(parts[1]), parts[2], strings.ToLower(parts[3]), parts[4]
		default:
			return "Usage: /quote <token> <amount_wei>  OR  /quote <pancake-v2|pancake-v3|uniswap-v3> <token> <buy|sell> <amount_raw>", nil
		}
		if !common.IsHexAddress(tokStr) || (side != "buy" && side != "sell") {
			return "Invalid token address or side (buy|sell).", nil
		}
		amt, ok := new(big.Int).SetString(amtStr, 10)
		if !ok || amt.Sign() <= 0 {
			return "Invalid positive amount_raw.", nil
		}
		return qb.QuoteToken(ctx, chainID, walletID, proto, common.HexToAddress(tokStr), side, amt)

	case "/honeypot":
		hb, ok := r.backend.(HoneypotBackend)
		if !ok {
			return "Honeypot simulator is not enabled.", nil
		}
		if len(parts) != 2 || !common.IsHexAddress(parts[1]) {
			return "Usage: /honeypot <token_address>", nil
		}
		return hb.CheckHoneypot(ctx, chainID, common.HexToAddress(parts[1]))

	case "/chain":
		if len(parts) != 2 {
			return "Usage: /chain <rbh|bsc>", nil
		}
		var newChain uint64
		switch strings.ToLower(parts[1]) {
		case "eth", "ethereum", "evm", "1":
			newChain = 1
		case "bsc", "56":
			newChain = 56
		case "hype", "hyperevm", "999":
			newChain = 999
		case "rbh", "robinhood", "4663":
			newChain = 4663
		case "arc", "5042":
			newChain = 5042
		case "base", "8453":
			newChain = 8453
		case "ely", "elysium", "99901":
			newChain = 99901
		default:
			return "Unsupported chain. Use bsc, rbh, base, eth, hype, arc, or ely.", nil
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
			if sellAmt.Sign() <= 0 {
				return "Calculated sell amount rounds to zero.", nil
			}
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

	case "/unwatch":
		if role != "operator" {
			return "Permission denied: operator role required.", nil
		}
		wb, ok := r.backend.(WatchlistManagerBackend)
		if !ok {
			return "Watchlist manager is not enabled.", nil
		}
		if len(parts) != 2 || !common.IsHexAddress(parts[1]) {
			return "Usage: /unwatch <wallet_address>", nil
		}
		if err := wb.RemoveWatchedWallet(ctx, chainID, common.HexToAddress(parts[1])); err != nil {
			return "", err
		}
		return fmt.Sprintf("Removed watched wallet %s on chain %d.", strings.ToLower(parts[1]), chainID), nil

	case "/watchlist":
		wb, ok := r.backend.(WatchlistManagerBackend)
		if !ok {
			return "Watchlist manager is not enabled.", nil
		}
		list, err := wb.ListWatchedWallets(ctx, chainID)
		if err != nil {
			return "", err
		}
		if len(list) == 0 {
			return fmt.Sprintf("Chain %d watchlist is empty.", chainID), nil
		}
		return fmt.Sprintf("Chain %d watchlist (%d):\n%s", chainID, len(list), strings.Join(list, "\n")), nil

	case "/tp", "/sl":
		if role != "operator" {
			return "Permission denied: operator role required.", nil
		}
		if len(parts) < 3 || len(parts) > 4 || !common.IsHexAddress(parts[1]) {
			return fmt.Sprintf("Usage: %s <token_address> <trigger_price_wei> [sell_pct_1_to_100]", cmd), nil
		}
		trigPrice, ok := new(big.Int).SetString(parts[2], 10)
		if !ok || trigPrice.Sign() <= 0 {
			return "Invalid positive trigger_price_wei.", nil
		}
		sellPct := uint64(100)
		if len(parts) == 4 {
			pctStr := strings.TrimSuffix(parts[3], "%")
			p, err := strconv.ParseUint(pctStr, 10, 64)
			if err != nil || p == 0 || p > 100 {
				return "Sell percentage must be between 1 and 100.", nil
			}
			sellPct = p
		}
		kind := "take_profit"
		if cmd == "/sl" {
			kind = "stop_loss"
		}
		nowUTC := r.now().UTC()
		orderID := fmt.Sprintf("%s-%d-%d", strings.TrimPrefix(cmd, "/"), chainID, nowUTC.UnixNano())
		idemKey := "tg-" + orderID
		expiresAt := nowUTC.Add(24 * time.Hour)
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO conditional_orders(order_id, chain_id, wallet_id, token, order_kind, trigger_price_wei, amount_raw, sell_bps, idempotency_key, state, expires_at, created_at, updated_at)
			 VALUES(?,?,?,?,?,?,?,?,?,'ACTIVE',?,?,?)`,
			orderID, chainID, walletID, strings.ToLower(common.HexToAddress(parts[1]).Hex()),
			kind, trigPrice.String(), "0", sellPct*100, idemKey,
			expiresAt.Format(time.RFC3339Nano), nowUTC.Format(time.RFC3339Nano), nowUTC.Format(time.RFC3339Nano),
		); err != nil {
			return "", err
		}
		return fmt.Sprintf("Created %s order %s on chain %d\ntoken=%s trigger_wei=%s sell=%d%% ttl=24h",
			kind, orderID, chainID, strings.ToLower(parts[1]), trigPrice.String(), sellPct), nil

	case "/orders":
		rows, err := r.readDB.QueryContext(ctx,
			`SELECT order_id, order_kind, token, trigger_price_wei, sell_bps, state
			 FROM conditional_orders
			 WHERE chain_id = ? AND state IN ('ACTIVE', 'TRIGGERED')
			 ORDER BY created_at DESC LIMIT 20`,
			chainID,
		)
		if err != nil {
			return "", err
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var oid, kind, tok, trig, st string
			var sellBps uint64
			if err := rows.Scan(&oid, &kind, &tok, &trig, &sellBps, &st); err == nil {
				lines = append(lines, fmt.Sprintf("- %s [%s] %s tok=%s trig=%s sell=%d%%", oid, st, kind, tok, trig, sellBps/100))
			}
		}
		if len(lines) == 0 {
			return fmt.Sprintf("No active conditional orders on chain %d.", chainID), nil
		}
		return fmt.Sprintf("Chain %d conditional orders:\n%s", chainID, strings.Join(lines, "\n")), nil

	case "/cancelorder":
		if role != "operator" {
			return "Permission denied: operator role required.", nil
		}
		if len(parts) != 2 {
			return "Usage: /cancelorder <order_id>", nil
		}
		res, err := r.db.ExecContext(ctx,
			`UPDATE conditional_orders SET state = 'CANCELLED', updated_at = ? WHERE order_id = ? AND chain_id = ? AND state = 'ACTIVE'`,
			r.now().UTC().Format(time.RFC3339Nano), parts[1], chainID,
		)
		if err != nil {
			return "", err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Sprintf("Order %s not found or not ACTIVE on chain %d.", parts[1], chainID), nil
		}
		return fmt.Sprintf("Order %s cancelled on chain %d.", parts[1], chainID), nil

	case "/status":
		sb, ok := r.backend.(StatusBackend)
		if !ok {
			return fmt.Sprintf("Active chain=%d wallet=%s (status backend not configured)", chainID, walletID), nil
		}
		return sb.CheckStatus(ctx)

	case "/snipe":
		if role != "operator" {
			return "Permission denied: operator role required.", nil
		}
		if len(parts) < 3 || len(parts) > 4 || !common.IsHexAddress(parts[1]) {
			return "Usage: /snipe <token_address> <amount_wei> [launch|migration]", nil
		}
		amt, ok := new(big.Int).SetString(parts[2], 10)
		if !ok || amt.Sign() <= 0 {
			return "Invalid positive amount_wei.", nil
		}
		kind := "migration"
		if len(parts) == 4 {
			k := strings.ToLower(parts[3])
			if k != "launch" && k != "migration" {
				return "Trigger kind must be 'launch' (PairCreated) or 'migration' (Launchpad graduation).", nil
			}
			kind = k
		}
		nowUTC := r.now().UTC()
		taskID := fmt.Sprintf("sn-%d-%d", chainID, nowUTC.UnixNano())
		tokHex := strings.ToLower(common.HexToAddress(parts[1]).Hex())
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO sniper_tasks(task_id, chain_id, wallet_id, token, trigger_kind, amount_in_wei, max_slippage_bps, anti_mev, honeypot_check, state, created_at, updated_at)
			 VALUES(?,?,?,?,?,?,1500,1,1,'ARMED',?,?)`,
			taskID, chainID, walletID, tokHex, kind, amt.String(),
			nowUTC.Format(time.RFC3339Nano), nowUTC.Format(time.RFC3339Nano),
		); err != nil {
			return "", err
		}
		if syncer, ok := r.backend.(interface{ SyncWatchlist(context.Context, uint64) error }); ok {
			_ = syncer.SyncWatchlist(ctx, chainID)
		}
		return fmt.Sprintf("🎯 Armed %s sniper %s on chain %d\ntoken=%s amount_wei=%s anti_mev=ON honeypot=ON",
			kind, taskID, chainID, tokHex, amt.String()), nil

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

func helpText() string {
	return strings.Join([]string{
		"evm-bot Multi-Chain Commands (BSC 56 + RBH 4663):",
		"/status - check BSC + RBH service health & counters",
		"/chain <bsc|rbh> - switch active chain",
		"/wallets - list registered wallets",
		"/positions <token> - atomic Multicall3 balance + allowance",
		"/quote <token> <amount_wei> - live DEX/Launchpad quote",
		"/honeypot <token> - eth_simulateV1 Buy->Approve->Sell check",
		"/watch <wallet> - add & sync wallet to feed Watchlist",
		"/unwatch <wallet> - remove wallet from Watchlist",
		"/watchlist - list watched wallets on active chain",
		"/tp <token> <price_wei> [pct] - create Take-Profit order",
		"/sl <token> <price_wei> [pct] - create Stop-Loss order",
		"/orders - list active conditional orders",
		"/cancelorder <order_id> - cancel an active order",
		"/copy <wallet> <wei> - configure copy strategy",
	}, "\n")
}

func defaultProtocolForChain(chainID uint64) string {
	switch chainID {
	case 56:
		return "pancake-v2"
	case 8453:
		return "uniswap-v3"
	default:
		return "pons-v2-curve"
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
