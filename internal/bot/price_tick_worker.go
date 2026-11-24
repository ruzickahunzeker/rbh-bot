package bot

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// OnConditionalTriggerFn is called whenever a conditional order triggers and executes in shadow mode.
type OnConditionalTriggerFn func(ord ConditionalOrder, spotPriceWei, simOutRaw *big.Int)

// PriceTickWorker periodically polls live quotes for tokens with ACTIVE conditional orders
// (Limit Buy, Take-Profit, Stop-Loss), evaluates triggers atomically via EvaluatePriceTick,
// and executes Shadow fills (or alerts operators) with zero public mainnet broadcast.
type PriceTickWorker struct {
	store       *Store
	shadow      *PaperShadowEngine
	quoter      ShadowQuoter
	onTriggered OnConditionalTriggerFn
	probeWei    *big.Int
}

func NewPriceTickWorker(store *Store, shadow *PaperShadowEngine, quoter ShadowQuoter, onTriggered OnConditionalTriggerFn) (*PriceTickWorker, error) {
	if store == nil || quoter == nil {
		return nil, errors.New("store and quoter are required for price tick worker")
	}
	return &PriceTickWorker{
		store:       store,
		shadow:      shadow,
		quoter:      quoter,
		onTriggered: onTriggered,
		probeWei:    big.NewInt(10_000_000_000_000_000), // 0.01 native unit
	}, nil
}

// TickOnce evaluates all tokens that currently have ACTIVE conditional orders.
func (w *PriceTickWorker) TickOnce(ctx context.Context, now time.Time) (int, error) {
	if w == nil || w.store == nil || w.store.db == nil || w.quoter == nil {
		return 0, errors.New("price tick worker unavailable")
	}
	type target struct {
		chainID  uint64
		token    common.Address
		walletID string
	}
	rows, err := w.store.db.QueryContext(ctx,
		`SELECT chain_id, token, MIN(wallet_id)
		 FROM conditional_orders
		 WHERE state = 'ACTIVE'
		 GROUP BY chain_id, token`,
	)
	if err != nil {
		return 0, err
	}
	var targets []target
	for rows.Next() {
		var t target
		var tokHex string
		if err := rows.Scan(&t.chainID, &tokHex, &t.walletID); err == nil {
			t.token = common.HexToAddress(tokHex)
			targets = append(targets, t)
		}
	}
	rows.Close()

	triggeredTotal := 0
	for _, tgt := range targets {
		_, spotWei, qErr := w.quoter.QuoteShadowFill(ctx, tgt.chainID, tgt.walletID, tgt.token, "buy", w.probeWei)
		if qErr != nil || spotWei == nil || spotWei.Sign() <= 0 {
			continue
		}
		triggered, err := w.store.EvaluatePriceTick(ctx, tgt.chainID, tgt.token, spotWei, now)
		if err != nil {
			return triggeredTotal, err
		}
		for _, ord := range triggered {
			triggeredTotal++
			simOut := big.NewInt(0)
			if w.shadow != nil && w.shadow.Enabled() {
				out, execErr := w.executeShadowConditional(ctx, ord, spotWei, now)
				if execErr == nil && out != nil {
					simOut = out
					_, _ = w.store.db.ExecContext(ctx,
						`UPDATE conditional_orders SET state = 'EXECUTED', updated_at = ? WHERE order_id = ?`,
						formatFixedUTC(now), ord.OrderID,
					)
				}
			}
			if w.onTriggered != nil {
				w.onTriggered(ord, spotWei, simOut)
			}
		}
	}
	return triggeredTotal, nil
}

func (w *PriceTickWorker) executeShadowConditional(ctx context.Context, ord ConditionalOrder, spotWei *big.Int, now time.Time) (*big.Int, error) {
	switch ord.Kind {
	case OrderLimitBuy:
		if ord.AmountRaw == nil || ord.AmountRaw.Sign() <= 0 {
			return nil, errors.New("limit_buy missing amount")
		}
		outRaw, _, err := w.quoter.QuoteShadowFill(ctx, ord.ChainID, ord.WalletID, ord.Token, "buy", ord.AmountRaw)
		if err != nil || outRaw == nil || outRaw.Sign() <= 0 {
			return nil, fmt.Errorf("limit_buy shadow quote failed: %w", err)
		}
		fillID := "cond-fill-" + ord.OrderID
		if err := w.shadow.RecordShadowFill(ctx, fillID, ord.ChainID, ord.WalletID, ord.Token, "buy", ord.AmountRaw, outRaw, spotWei, ord.IdempotencyKey, now); err != nil {
			return nil, err
		}
		return outRaw, nil

	case OrderTakeProfit, OrderStopLoss:
		bal := w.netShadowTokenBalance(ctx, ord.ChainID, ord.WalletID, ord.Token)
		if bal.Sign() <= 0 {
			return big.NewInt(0), nil
		}
		sellAmt := new(big.Int).Mul(bal, new(big.Int).SetUint64(ord.SellBps))
		sellAmt.Div(sellAmt, big.NewInt(10_000))
		if sellAmt.Sign() <= 0 {
			return big.NewInt(0), nil
		}
		outWei, _, err := w.quoter.QuoteShadowFill(ctx, ord.ChainID, ord.WalletID, ord.Token, "sell", sellAmt)
		if err != nil || outWei == nil || outWei.Sign() <= 0 {
			return nil, fmt.Errorf("tp/sl shadow sell quote failed: %w", err)
		}
		fillID := "cond-fill-" + ord.OrderID
		if err := w.shadow.RecordShadowFill(ctx, fillID, ord.ChainID, ord.WalletID, ord.Token, "sell", sellAmt, outWei, spotWei, ord.IdempotencyKey, now); err != nil {
			return nil, err
		}
		return outWei, nil
	default:
		return nil, fmt.Errorf("unsupported order kind %q", ord.Kind)
	}
}

func (w *PriceTickWorker) netShadowTokenBalance(ctx context.Context, chainID uint64, walletID string, token common.Address) *big.Int {
	bal := big.NewInt(0)
	rows, err := w.store.db.QueryContext(ctx,
		`SELECT side, amount_in_raw, simulated_out_raw FROM paper_shadow_fills
		 WHERE chain_id = ? AND wallet_id = ? AND token = ? ORDER BY created_at ASC, rowid ASC`,
		chainID, walletID, strings.ToLower(token.Hex()),
	)
	if err != nil {
		return bal
	}
	defer rows.Close()
	for rows.Next() {
		var side, inStr, outStr string
		if rows.Scan(&side, &inStr, &outStr) != nil {
			continue
		}
		inVal, _ := new(big.Int).SetString(inStr, 10)
		outVal, _ := new(big.Int).SetString(outStr, 10)
		if inVal == nil || outVal == nil {
			continue
		}
		if side == "buy" {
			bal.Add(bal, outVal)
		} else if side == "sell" {
			bal.Sub(bal, inVal)
			if bal.Sign() < 0 {
				bal.SetInt64(0)
			}
		}
	}
	return bal
}

// Run runs the price tick loop until ctx is cancelled.
func (w *PriceTickWorker) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_, _ = w.TickOnce(ctx, time.Now().UTC())
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
