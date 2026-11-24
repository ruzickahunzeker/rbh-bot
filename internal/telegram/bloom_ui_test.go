package telegram

import (
	"context"
	"errors"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type bloomStubBackend struct {
	stubBackend
}

func (b *bloomStubBackend) ProbeTokenQuote(_ context.Context, _ uint64, _ string, _ common.Address, side string, amountRaw *big.Int) (string, string, *big.Int, uint64, error) {
	if side == "buy" {
		// 1e17 wei (0.1 BNB) -> 1,000,000 raw tokens
		out := new(big.Int).Div(amountRaw, big.NewInt(1e11))
		if out.Sign() <= 0 {
			out = big.NewInt(1)
		}
		return "pancake-v2", "DEX", out, 50_000_000, nil
	}
	// Sell: 1,000,000 raw tokens -> 1.2e17 wei (+20% PnL!)
	out := new(big.Int).Mul(amountRaw, big.NewInt(120_000_000_000))
	return "pancake-v2", "DEX", out, 50_000_001, nil
}

func (b *bloomStubBackend) CheckHoneypot(_ context.Context, _ uint64, _ common.Address) (string, error) {
	return "Honeypot Check\nverdict=SAFE safe=true", nil
}

func TestBloomUIFlowStartPasteCAShadowBuySellAndSettings(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.BotOwner, filepath.Join(t.TempDir(), "bot-bloom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 25, 0, 10, 0, 0, time.UTC)
	router, err := NewRouter(db, &bloomStubBackend{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := router.AllowChat(ctx, 7131128175, "op", "operator", 56, "wallet-bsc-readonly"); err != nil {
		t.Fatal(err)
	}

	// Unauthorized callback must fail closed.
	if _, err := router.HandleCallback(ctx, 999999, "menu"); !errors.Is(err, ErrUnauthorizedChat) {
		t.Fatalf("expected ErrUnauthorizedChat, got %v", err)
	}

	// 1. /start renders Bloom Main Dashboard with Inline Keyboard.
	rep, err := router.HandleMessage(ctx, 7131128175, "/start")
	if err != nil || !strings.Contains(rep.Text, "Bloom UI") || len(rep.Buttons) < 4 {
		t.Fatalf("/start failed: %+v err=%v", rep, err)
	}

	// 2. Switch chain button toggles BSC (56) -> RBH (4663) -> BSC (56).
	rep, err = router.HandleCallback(ctx, 7131128175, "sw_chain")
	if err != nil || !strings.Contains(rep.Text, "RBH (4663)") || !rep.EditOriginal {
		t.Fatalf("sw_chain to RBH failed: %+v err=%v", rep, err)
	}
	rep, err = router.HandleCallback(ctx, 7131128175, "sw_chain")
	if err != nil || !strings.Contains(rep.Text, "BSC (56)") {
		t.Fatalf("sw_chain back to BSC failed: %+v err=%v", rep, err)
	}

	// 3. Paste bare CA -> Instant Token Card with Buy presets.
	ca := "0x55d398326f99059fF775485246999027B3197955"
	card, err := router.HandleMessage(ctx, 7131128175, ca)
	if err != nil || !strings.Contains(card.Text, "Token Card") || !strings.Contains(card.Text, "pancake-v2") {
		t.Fatalf("paste CA failed: %+v err=%v", card, err)
	}
	// Extract the 0.1 BNB buy button callback data (sb:<tokKey>:0.1)
	var buy01CB, sellModeCB string
	for _, row := range card.Buttons {
		for _, btn := range row {
			if strings.HasSuffix(btn.CallbackData, ":0.1") {
				buy01CB = btn.CallbackData
			}
			if strings.HasSuffix(btn.CallbackData, ":sell") {
				sellModeCB = btn.CallbackData
			}
		}
	}
	if buy01CB == "" || sellModeCB == "" {
		t.Fatalf("missing buy/sell buttons on token card: %+v", card.Buttons)
	}

	// 4. One-tap Shadow Buy 0.1 BNB -> fills 1,000,000 raw tokens.
	buyRep, err := router.HandleCallback(ctx, 7131128175, buy01CB)
	if err != nil || !strings.Contains(buyRep.Text, "[SHADOW BUY]") || !strings.Contains(buyRep.Text, "Shadow Bal: 1000000") {
		t.Fatalf("shadow buy button failed: %+v err=%v", buyRep, err)
	}

	// 5. Positions card shows +20.0% PnL.
	posRep, err := router.HandleCallback(ctx, 7131128175, "positions")
	if err != nil || !strings.Contains(posRep.Text, "+20.0%") {
		t.Fatalf("positions PnL card failed: %+v err=%v", posRep, err)
	}

	// 6. Switch to Sell Mode & tap Sell 50%.
	sellCard, err := router.HandleCallback(ctx, 7131128175, sellModeCB)
	if err != nil || !strings.Contains(sellCard.Text, "Token Card") {
		t.Fatalf("sell mode switch failed: %+v err=%v", sellCard, err)
	}
	var sell50CB string
	for _, row := range sellCard.Buttons {
		for _, btn := range row {
			if strings.HasSuffix(btn.CallbackData, ":50") {
				sell50CB = btn.CallbackData
			}
		}
	}
	now = now.Add(time.Second)
	sellRep, err := router.HandleCallback(ctx, 7131128175, sell50CB)
	if err != nil || !strings.Contains(sellRep.Text, "[SHADOW SELL 50%]") || !strings.Contains(sellRep.Text, "Shadow Bal: 500000") {
		t.Fatalf("shadow sell 50%% failed: %+v err=%v", sellRep, err)
	}

	// 7. Settings toggles persist cleanly.
	setRep, err := router.HandleCallback(ctx, 7131128175, "set:slip_buy")
	if err != nil || !strings.Contains(setRep.Text, "Buy Slippage: 10.0%") {
		t.Fatalf("set:slip_buy toggle failed: %+v err=%v", setRep, err)
	}
	setRep, err = router.HandleCallback(ctx, 7131128175, "set:confirm")
	if err != nil || !strings.Contains(setRep.Text, "OFF (Degen Instant)") {
		t.Fatalf("set:confirm toggle failed: %+v err=%v", setRep, err)
	}
}
