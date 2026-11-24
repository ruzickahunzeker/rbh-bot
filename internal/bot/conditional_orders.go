package bot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type ConditionalOrderKind string

const (
	OrderLimitBuy   ConditionalOrderKind = "limit_buy"
	OrderTakeProfit ConditionalOrderKind = "take_profit"
	OrderStopLoss   ConditionalOrderKind = "stop_loss"
)

type ConditionalOrder struct {
	OrderID         string
	ChainID         uint64
	WalletID        string
	Token           common.Address
	Kind            ConditionalOrderKind
	TriggerPriceWei *big.Int
	AmountRaw       *big.Int // required for limit_buy
	SellBps         uint64   // 1..10000 for take_profit / stop_loss
	IdempotencyKey  string
	State           string
	ExpiresAt       time.Time
}

// CreateConditionalOrder persists an ACTIVE limit buy or TP/SL order with a deterministic immutable Idempotency-Key.
func (s *Store) CreateConditionalOrder(ctx context.Context, ord ConditionalOrder, now time.Time) (ConditionalOrder, error) {
	if s == nil || s.db == nil {
		return ConditionalOrder{}, ErrBotStoreUnavailable
	}
	if ord.OrderID == "" || ord.ChainID == 0 || strings.TrimSpace(ord.WalletID) == "" || ord.Token == (common.Address{}) ||
		ord.TriggerPriceWei == nil || ord.TriggerPriceWei.Sign() <= 0 || !ord.ExpiresAt.After(now) {
		return ConditionalOrder{}, errors.New("invalid conditional order parameters")
	}
	amtStr := "0"
	switch ord.Kind {
	case OrderLimitBuy:
		if ord.AmountRaw == nil || ord.AmountRaw.Sign() <= 0 || ord.SellBps != 0 {
			return ConditionalOrder{}, errors.New("limit_buy requires positive amount_raw and sell_bps=0")
		}
		amtStr = ord.AmountRaw.String()
	case OrderTakeProfit, OrderStopLoss:
		if ord.SellBps == 0 || ord.SellBps > 10000 {
			return ConditionalOrder{}, errors.New("take_profit/stop_loss requires sell_bps in 1..10000")
		}
	default:
		return ConditionalOrder{}, fmt.Errorf("unsupported conditional order kind %q", ord.Kind)
	}
	if ord.IdempotencyKey == "" {
		ord.IdempotencyKey = "cond-" + canonicalHash128(fmt.Sprintf("%d", ord.ChainID), ord.WalletID, ord.OrderID, string(ord.Kind))
	}
	ord.State = "ACTIVE"
	nowStr := formatFixedUTC(now)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO conditional_orders(order_id, chain_id, wallet_id, token, order_kind, trigger_price_wei, amount_raw, sell_bps, idempotency_key, state, expires_at, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,'ACTIVE',?,?,?)`,
		ord.OrderID, ord.ChainID, ord.WalletID, strings.ToLower(ord.Token.Hex()), string(ord.Kind),
		ord.TriggerPriceWei.String(), amtStr, ord.SellBps, ord.IdempotencyKey, formatFixedUTC(ord.ExpiresAt), nowStr, nowStr,
	); err != nil {
		return ConditionalOrder{}, err
	}
	return ord, nil
}

// EvaluatePriceTick atomically checks all ACTIVE conditional orders for (chainID, token)
// against spotPriceWei: expiring past-TTL orders and transitioning matched orders to TRIGGERED
// exactly once (immune to duplicate triggers across subsequent ticks).
func (s *Store) EvaluatePriceTick(ctx context.Context, chainID uint64, token common.Address, spotPriceWei *big.Int, now time.Time) ([]ConditionalOrder, error) {
	if s == nil || s.db == nil {
		return nil, ErrBotStoreUnavailable
	}
	if chainID == 0 || token == (common.Address{}) || spotPriceWei == nil || spotPriceWei.Sign() <= 0 {
		return nil, errors.New("invalid price tick")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	nowStr := formatFixedUTC(now)
	tokenHex := strings.ToLower(token.Hex())

	// 1. Expire any ACTIVE orders whose expires_at <= now.
	if _, err := tx.ExecContext(ctx,
		`UPDATE conditional_orders SET state = 'EXPIRED', updated_at = ?
		 WHERE chain_id = ? AND token = ? AND state = 'ACTIVE' AND expires_at <= ?`,
		nowStr, chainID, tokenHex, nowStr,
	); err != nil {
		return nil, err
	}

	// 2. Scan remaining ACTIVE orders.
	rows, err := tx.QueryContext(ctx,
		`SELECT order_id, wallet_id, order_kind, trigger_price_wei, amount_raw, sell_bps, idempotency_key, expires_at
		 FROM conditional_orders
		 WHERE chain_id = ? AND token = ? AND state = 'ACTIVE'
		 ORDER BY created_at ASC, rowid ASC`,
		chainID, tokenHex,
	)
	if err != nil {
		return nil, err
	}
	var candidates []ConditionalOrder
	for rows.Next() {
		var o ConditionalOrder
		var kindStr, trigStr, amtStr, expStr string
		if err := rows.Scan(&o.OrderID, &o.WalletID, &kindStr, &trigStr, &amtStr, &o.SellBps, &o.IdempotencyKey, &expStr); err != nil {
			rows.Close()
			return nil, err
		}
		o.ChainID = chainID
		o.Token = token
		o.Kind = ConditionalOrderKind(kindStr)
		o.TriggerPriceWei, _ = new(big.Int).SetString(trigStr, 10)
		o.AmountRaw, _ = new(big.Int).SetString(amtStr, 10)
		o.ExpiresAt, _ = time.Parse(fixedUTCTimestamp, expStr)
		candidates = append(candidates, o)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	var triggered []ConditionalOrder
	for _, o := range candidates {
		matched := false
		switch o.Kind {
		case OrderLimitBuy, OrderStopLoss:
			matched = spotPriceWei.Cmp(o.TriggerPriceWei) <= 0
		case OrderTakeProfit:
			matched = spotPriceWei.Cmp(o.TriggerPriceWei) >= 0
		}
		if !matched {
			continue
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE conditional_orders SET state = 'TRIGGERED', triggered_at = ?, updated_at = ?
			 WHERE order_id = ? AND state = 'ACTIVE'`,
			nowStr, nowStr, o.OrderID,
		)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			o.State = "TRIGGERED"
			triggered = append(triggered, o)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return triggered, nil
}

// PaperShadowEngine records zero-broadcast simulated fills when explicitly enabled (default OFF).
type PaperShadowEngine struct {
	store   *Store
	enabled bool
}

func NewPaperShadowEngine(store *Store, enabled bool) (*PaperShadowEngine, error) {
	if store == nil || store.db == nil {
		return nil, ErrBotStoreUnavailable
	}
	return &PaperShadowEngine{store: store, enabled: enabled}, nil
}

func (p *PaperShadowEngine) Enabled() bool { return p != nil && p.enabled }

func (p *PaperShadowEngine) RecordShadowFill(
	ctx context.Context,
	fillID string,
	chainID uint64,
	walletID string,
	token common.Address,
	side string,
	amountInRaw, simulatedOutRaw, spotPriceWei *big.Int,
	sourceRef string,
	now time.Time,
) error {
	if !p.Enabled() {
		return errors.New("paper shadow mode is disabled")
	}
	side = strings.ToLower(strings.TrimSpace(side))
	if fillID == "" || chainID == 0 || walletID == "" || token == (common.Address{}) || (side != "buy" && side != "sell") ||
		amountInRaw == nil || amountInRaw.Sign() <= 0 || simulatedOutRaw == nil || simulatedOutRaw.Sign() <= 0 ||
		spotPriceWei == nil || spotPriceWei.Sign() <= 0 {
		return errors.New("invalid shadow fill")
	}
	_, err := p.store.db.ExecContext(ctx,
		`INSERT INTO paper_shadow_fills(fill_id, chain_id, wallet_id, token, side, amount_in_raw, simulated_out_raw, spot_price_wei, source_ref, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		fillID, chainID, walletID, strings.ToLower(token.Hex()), side,
		amountInRaw.String(), simulatedOutRaw.String(), spotPriceWei.String(), sourceRef, formatFixedUTC(now),
	)
	return err
}

// ShadowPosition returns simulated net token balance and net native spend/proceeds for (chainID, walletID, token).
func (p *PaperShadowEngine) ShadowPosition(ctx context.Context, chainID uint64, walletID string, token common.Address) (netToken *big.Int, netNative *big.Int, err error) {
	if p == nil || p.store == nil || p.store.db == nil {
		return nil, nil, ErrBotStoreUnavailable
	}
	rows, err := p.store.db.QueryContext(ctx,
		`SELECT side, amount_in_raw, simulated_out_raw FROM paper_shadow_fills
		 WHERE chain_id = ? AND wallet_id = ? AND token = ?`,
		chainID, walletID, strings.ToLower(token.Hex()),
	)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	netToken = big.NewInt(0)
	netNative = big.NewInt(0)
	for rows.Next() {
		var side, inStr, outStr string
		if err := rows.Scan(&side, &inStr, &outStr); err != nil {
			return nil, nil, err
		}
		inAmt, _ := new(big.Int).SetString(inStr, 10)
		outAmt, _ := new(big.Int).SetString(outStr, 10)
		if side == "buy" {
			netNative.Sub(netNative, inAmt)
			netToken.Add(netToken, outAmt)
		} else {
			netToken.Sub(netToken, inAmt)
			netNative.Add(netNative, outAmt)
		}
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, err
	}
	return netToken, netNative, nil
}
