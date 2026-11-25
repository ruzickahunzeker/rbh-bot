package bot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/feed"
)

// FeedStreamConfig binds one chain's feed outbox stream to a bot copy-trading strategy lane.
type FeedStreamConfig struct {
	ChainID        uint64
	FeedInstanceID string
	Source         OutboxSource
	PrincipalID    string
	StrategyID     string
	WalletID       string
	FixedAmountRaw *big.Int
	MaxSignalAge   time.Duration
}

// ShadowQuoter fetches a real read-only on-chain quote (via POST /v1/quotes) for Paper/Shadow mode
// without requiring a private key or broadcasting any transaction.
type ShadowQuoter interface {
	QuoteShadowFill(ctx context.Context, chainID uint64, walletID string, token common.Address, side string, amountInRaw *big.Int) (expectedOutRaw *big.Int, spotPriceWei *big.Int, err error)
}

// MultiChainRuntime orchestrates multi-chain feed outbox ingestion, net trigger_id deduplication,
// and either zero-private-key Paper/Shadow live-quote execution or serial wallet trade dispatch.
type MultiChainRuntime struct {
	store        *Store
	streams      []FeedStreamConfig
	shadow       *PaperShadowEngine
	quoter       ShadowQuoter
	submitter    TradeSubmitter
	sniper       *SniperEngine
	onShadowFill func(chainID uint64, walletID string, token common.Address, side string, amountInRaw, outRaw *big.Int)
	batchSize    int
	now          func() time.Time
}

// SetSniperEngine attaches a SniperEngine to react to feed-bsc launch/migration events.
func (r *MultiChainRuntime) SetSniperEngine(eng *SniperEngine) {
	if r != nil {
		r.sniper = eng
	}
}

// SetOnShadowFill registers an optional callback invoked after each durable Paper/Shadow fill
// (e.g., to push real-time notifications to Telegram operators).
func (r *MultiChainRuntime) SetOnShadowFill(fn func(chainID uint64, walletID string, token common.Address, side string, amountInRaw, outRaw *big.Int)) {
	if r != nil {
		r.onShadowFill = fn
	}
}

// Run polls all configured feed streams continuously until ctx is cancelled.
func (r *MultiChainRuntime) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_, _ = r.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func NewMultiChainRuntime(
	store *Store,
	streams []FeedStreamConfig,
	shadow *PaperShadowEngine,
	quoter ShadowQuoter,
	submitter TradeSubmitter,
	batchSize int,
	now func() time.Time,
) (*MultiChainRuntime, error) {
	if store == nil || len(streams) == 0 {
		return nil, ErrConsumerUnavailable
	}
	if batchSize <= 0 || batchSize > 1000 {
		batchSize = 100
	}
	if now == nil {
		now = time.Now
	}
	return &MultiChainRuntime{
		store:     store,
		streams:   streams,
		shadow:    shadow,
		quoter:    quoter,
		submitter: submitter,
		batchSize: batchSize,
		now:       now,
	}, nil
}

// RunOnce polls every configured chain's feed outbox from its independent durable cursor,
// ingests and deduplicates swap signals by net trigger_id, and dispatches queued jobs
// through Paper/Shadow live-quoting (when shadow mode is enabled) or TradeSubmitter.
func (r *MultiChainRuntime) RunOnce(ctx context.Context) (int, error) {
	if r == nil || r.store == nil || ctx == nil {
		return 0, ErrConsumerUnavailable
	}
	totalProcessed := 0
	now := r.now().UTC()

	for _, stream := range r.streams {
		cursor, err := r.store.LoadFeedCursor(ctx, stream.ChainID, stream.FeedInstanceID)
		if err != nil {
			return totalProcessed, err
		}
		items, err := stream.Source.ReadOutboxAfter(ctx, cursor, r.batchSize)
		if err != nil {
			return totalProcessed, fmt.Errorf("read outbox (%d,%s): %w", stream.ChainID, stream.FeedInstanceID, err)
		}
		for _, item := range items {
			var payload feed.NormalizedSwapPayload
			if err := json.Unmarshal(item.PayloadJSON, &payload); err != nil {
				return totalProcessed, fmt.Errorf("decode swap payload offset=%d: %w", item.Offset, err)
			}
			// Route Sniper launch/migration events directly to SniperEngine without polluting copy-trade signals.
			if payload.Side == string(SniperLaunch) || payload.Side == string(SniperMigration) {
				if r.sniper != nil && common.IsHexAddress(payload.Token) {
					_, _ = r.sniper.MatchAndTrigger(ctx, stream.ChainID, common.HexToAddress(payload.Token), SniperTriggerKind(payload.Side), now)
				}
				_, _ = r.store.db.ExecContext(ctx,
					`INSERT INTO bot_feed_cursors(chain_id, feed_instance_id, durable_event_offset, updated_at)
					 VALUES(?,?,?,?)
					 ON CONFLICT(chain_id, feed_instance_id) DO UPDATE SET durable_event_offset = MAX(durable_event_offset, excluded.durable_event_offset), updated_at = excluded.updated_at`,
					stream.ChainID, stream.FeedInstanceID, item.Offset, formatFixedUTC(now),
				)
				totalProcessed++
				continue
			}

			// Enforce per-trader copy_wallet_profiles settings (enabled, copy_buy/copy_sell, pool_filter, max_delay_sec, wallet_id).
			strategyID := stream.StrategyID
			targetWalletID := stream.WalletID
			maxAge := stream.MaxSignalAge
			if skip, profStrategyID, profWalletID, profMaxAge := r.evaluateCopyProfileFilter(ctx, stream.ChainID, payload.Trader, payload.Side, payload.PoolVersion); skip {
				_, _ = r.store.db.ExecContext(ctx,
					`INSERT INTO bot_feed_cursors(chain_id, feed_instance_id, durable_event_offset, updated_at)
					 VALUES(?,?,?,?)
					 ON CONFLICT(chain_id, feed_instance_id) DO UPDATE SET durable_event_offset = MAX(durable_event_offset, excluded.durable_event_offset), updated_at = excluded.updated_at`,
					stream.ChainID, stream.FeedInstanceID, item.Offset, formatFixedUTC(now),
				)
				totalProcessed++
				continue
			} else {
				if profStrategyID != "" {
					strategyID = profStrategyID
				}
				if profWalletID != "" {
					targetWalletID = profWalletID
				}
				if profMaxAge > 0 {
					maxAge = profMaxAge
				}
			}

			stage := payload.Stage
			if stage == "" {
				stage = "included"
			}
			sig := MultiChainSignal{
				ChainID:         stream.ChainID,
				FeedInstanceID:  stream.FeedInstanceID,
				FeedOffset:      item.Offset,
				ObservationID:   item.ObservationID,
				TxHash:          payload.TxHash,
				TraderAddress:   payload.Trader,
				TokenAddress:    payload.Token,
				Side:            payload.Side,
				Stage:           stage,
				FirstObservedAt: now,
			}
			if _, err := r.store.IngestMultiChainSignal(
				ctx,
				stream.PrincipalID,
				strategyID,
				targetWalletID,
				sig,
				maxAge,
				now,
			); err != nil {
				return totalProcessed, err
			}
			totalProcessed++
		}

		// Dispatch next pending job for all wallets with PENDING jobs on this chain.
		if r.shadow != nil && r.shadow.Enabled() && r.quoter != nil {
			if err := r.dispatchShadowLane(ctx, stream, now); err != nil {
				return totalProcessed, err
			}
		} else if r.submitter != nil {
			if _, _, err := r.store.DispatchNextForWallet(ctx, stream.ChainID, stream.WalletID, r.submitter, now); err != nil {
				return totalProcessed, err
			}
		}
	}
	return totalProcessed, nil
}

func (r *MultiChainRuntime) evaluateCopyProfileFilter(ctx context.Context, chainID uint64, trader, side, poolVer string) (skip bool, strategyID, walletID string, maxAge time.Duration) {
	var profKey, poolFilter, profWallet string
	var en, copyBuy, copySell, maxDelaySec int
	err := r.store.db.QueryRowContext(ctx,
		`SELECT profile_key, enabled, copy_buy, copy_sell, pool_filter, max_delay_sec, wallet_id
		 FROM copy_wallet_profiles WHERE chain_id = ? AND address = ?`,
		chainID, strings.ToLower(strings.TrimSpace(trader)),
	).Scan(&profKey, &en, &copyBuy, &copySell, &poolFilter, &maxDelaySec, &profWallet)
	if err != nil {
		return false, "", "", 0
	}
	if en == 0 {
		return true, "", "", 0
	}
	if strings.EqualFold(side, "buy") && copyBuy == 0 {
		return true, "", "", 0
	}
	if strings.EqualFold(side, "sell") && copySell == 0 {
		return true, "", "", 0
	}
	pv := strings.ToLower(poolVer)
	switch poolFilter {
	case "launchpad":
		if pv != "four-meme" && pv != "flap" && pv != "pons-v2-curve" && pv != "curve" {
			return true, "", "", 0
		}
	case "dex":
		if pv != "v2" && pv != "v3" && pv != "pancake-v2" && pv != "pancake-v3" && pv != "uniswap-v3-bsc" {
			return true, "", "", 0
		}
	case "pons":
		if !strings.Contains(pv, "pons") {
			return true, "", "", 0
		}
	}
	var dur time.Duration
	if maxDelaySec > 0 {
		dur = time.Duration(maxDelaySec) * time.Second
	}
	return false, "cp:" + profKey, strings.TrimSpace(profWallet), dur
}

func (r *MultiChainRuntime) dispatchShadowLane(ctx context.Context, stream FeedStreamConfig, now time.Time) error {
	// Collect distinct wallets that have PENDING jobs on stream.ChainID (supporting per-profile custom wallets).
	wallets := []string{stream.WalletID}
	if wRows, err := r.store.db.QueryContext(ctx,
		`SELECT DISTINCT wallet_id FROM submission_jobs WHERE chain_id = ? AND state = 'PENDING'`,
		stream.ChainID,
	); err == nil {
		seen := map[string]bool{stream.WalletID: true}
		for wRows.Next() {
			var wid string
			if wRows.Scan(&wid) == nil && wid != "" && !seen[wid] {
				seen[wid] = true
				wallets = append(wallets, wid)
			}
		}
		wRows.Close()
	}

	for _, wid := range wallets {
		adapter := &shadowSubmitterAdapter{
			store:        r.store,
			shadow:       r.shadow,
			quoter:       r.quoter,
			amountRaw:    stream.FixedAmountRaw,
			onShadowFill: r.onShadowFill,
			now:          now,
		}
		for {
			dispatched, _, err := r.store.DispatchNextForWallet(ctx, stream.ChainID, wid, adapter, now)
			if err != nil {
				return err
			}
			if !dispatched {
				break
			}
			if err := r.store.MarkJobTerminal(ctx, adapter.lastKey, "COMPLETED", "", now); err != nil {
				return err
			}
		}
	}
	return nil
}

type shadowSubmitterAdapter struct {
	store        *Store
	shadow       *PaperShadowEngine
	quoter       ShadowQuoter
	amountRaw    *big.Int
	onShadowFill func(chainID uint64, walletID string, token common.Address, side string, amountInRaw, outRaw *big.Int)
	now          time.Time
	lastKey      string
}

func (a *shadowSubmitterAdapter) SubmitCopyJob(ctx context.Context, chainID uint64, walletID, idempotencyKey string) (string, bool, error) {
	a.lastKey = idempotencyKey
	var tokenHex, actionKind, strategyID string
	err := a.store.db.QueryRowContext(ctx,
		`SELECT token, action_kind, strategy_id FROM copy_signal_decisions WHERE trade_idempotency_key = ?`,
		idempotencyKey,
	).Scan(&tokenHex, &actionKind, &strategyID)
	if err != nil {
		return "", false, err
	}
	tokenAddr := common.HexToAddress(tokenHex)
	side := "buy"
	if actionKind == "copy_sell" {
		side = "sell"
	}

	var buyNativeStr string
	sellRatioPct := 100
	if strings.HasPrefix(strategyID, "cp:") {
		profKey := strings.TrimPrefix(strategyID, "cp:")
		_ = a.store.db.QueryRowContext(ctx,
			`SELECT buy_amount_native, sell_ratio_pct FROM copy_wallet_profiles WHERE profile_key = ?`,
			profKey,
		).Scan(&buyNativeStr, &sellRatioPct)
	}

	var amtIn *big.Int
	if side == "buy" {
		if buyNativeStr != "" {
			if f, ok := new(big.Float).SetPrec(128).SetString(strings.TrimSpace(buyNativeStr)); ok && f.Sign() > 0 {
				scale := new(big.Float).SetPrec(128).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
				f.Mul(f, scale)
				amtIn, _ = f.Int(nil)
			}
		}
		if amtIn == nil || amtIn.Sign() <= 0 {
			amtIn = a.amountRaw
		}
		if amtIn == nil || amtIn.Sign() <= 0 {
			amtIn = big.NewInt(1_000_000_000_000_000)
		}
	} else {
		// Compute actual shadow token balance and scale by sell_ratio_pct% (P0-2 fix!).
		bal := shadowBalanceForWalletToken(ctx, a.store.db, chainID, walletID, tokenAddr)
		if bal.Sign() <= 0 {
			return "shadow-noop-" + idempotencyKey, false, nil
		}
		if sellRatioPct <= 0 || sellRatioPct > 100 {
			sellRatioPct = 100
		}
		amtIn = new(big.Int).Mul(bal, big.NewInt(int64(sellRatioPct)))
		amtIn.Div(amtIn, big.NewInt(100))
		if amtIn.Sign() <= 0 {
			return "shadow-noop-" + idempotencyKey, false, nil
		}
	}

	outRaw, spotWei, err := a.quoter.QuoteShadowFill(ctx, chainID, walletID, tokenAddr, side, amtIn)
	if err != nil {
		return "", true, err
	}
	if outRaw == nil || outRaw.Sign() <= 0 {
		return "", false, errors.New("shadow quote returned zero output")
	}
	if spotWei == nil || spotWei.Sign() <= 0 {
		spotWei = big.NewInt(1)
	}
	fillID := "shadow-" + idempotencyKey
	if err := a.shadow.RecordShadowFill(ctx, fillID, chainID, walletID, tokenAddr, side, amtIn, outRaw, spotWei, idempotencyKey, a.now); err != nil {
		return "", false, err
	}
	if a.onShadowFill != nil {
		a.onShadowFill(chainID, walletID, tokenAddr, side, amtIn, outRaw)
	}
	return fillID, false, nil
}

func shadowBalanceForWalletToken(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, chainID uint64, walletID string, token common.Address) *big.Int {
	bal := big.NewInt(0)
	rows, err := db.QueryContext(ctx,
		`SELECT side, amount_in_raw, simulated_out_raw FROM paper_shadow_fills
		 WHERE chain_id = ? AND wallet_id = ? AND token = ? ORDER BY created_at ASC, rowid ASC`,
		chainID, walletID, strings.ToLower(token.Hex()),
	)
	if err != nil {
		return bal
	}
	defer rows.Close()
	for rows.Next() {
		var s, inStr, outStr string
		if rows.Scan(&s, &inStr, &outStr) != nil {
			continue
		}
		inVal, _ := new(big.Int).SetString(inStr, 10)
		outVal, _ := new(big.Int).SetString(outStr, 10)
		if inVal == nil || outVal == nil {
			continue
		}
		if s == "buy" {
			bal.Add(bal, outVal)
		} else if s == "sell" {
			bal.Sub(bal, inVal)
			if bal.Sign() < 0 {
				bal.SetInt64(0)
			}
		}
	}
	return bal
}
