package bot

import (
	"context"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestStep4ConditionalOrdersAndDefaultOffPaperShadowLedger(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.BotOwner, filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 24, 16, 45, 0, 0, time.UTC)
	token := common.HexToAddress("0x0000000000000000000000000000000000008453")

	// 1. Create Limit Buy (<= 100), Take-Profit (>= 200), and Stop-Loss (<= 80) on Base (chain_id=8453).
	if _, err := store.CreateConditionalOrder(ctx, ConditionalOrder{
		OrderID:         "ord-limit-1",
		ChainID:         8453,
		WalletID:        "w-base-1",
		Token:           token,
		Kind:            OrderLimitBuy,
		TriggerPriceWei: big.NewInt(100),
		AmountRaw:       big.NewInt(1_000_000_000_000_000),
		ExpiresAt:       now.Add(1 * time.Hour),
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateConditionalOrder(ctx, ConditionalOrder{
		OrderID:         "ord-tp-1",
		ChainID:         8453,
		WalletID:        "w-base-1",
		Token:           token,
		Kind:            OrderTakeProfit,
		TriggerPriceWei: big.NewInt(200),
		SellBps:         5000, // 50%
		ExpiresAt:       now.Add(1 * time.Hour),
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateConditionalOrder(ctx, ConditionalOrder{
		OrderID:         "ord-sl-1",
		ChainID:         8453,
		WalletID:        "w-base-1",
		Token:           token,
		Kind:            OrderStopLoss,
		TriggerPriceWei: big.NewInt(80),
		SellBps:         10000, // 100%
		ExpiresAt:       now.Add(1 * time.Hour),
	}, now); err != nil {
		t.Fatal(err)
	}

	// Spot=150 triggers nothing.
	trig, err := store.EvaluatePriceTick(ctx, 8453, token, big.NewInt(150), now.Add(time.Minute))
	if err != nil || len(trig) != 0 {
		t.Fatalf("expected 0 triggers at spot=150, got %+v err=%v", trig, err)
	}
	// Spot=95 triggers ONLY ord-limit-1 (once!).
	trig, err = store.EvaluatePriceTick(ctx, 8453, token, big.NewInt(95), now.Add(2*time.Minute))
	if err != nil || len(trig) != 1 || trig[0].OrderID != "ord-limit-1" {
		t.Fatalf("expected ord-limit-1 triggered at spot=95, got %+v err=%v", trig, err)
	}
	// Repeated tick at spot=95 MUST NOT double-trigger ord-limit-1.
	trig, err = store.EvaluatePriceTick(ctx, 8453, token, big.NewInt(95), now.Add(3*time.Minute))
	if err != nil || len(trig) != 0 {
		t.Fatalf("expected zero duplicate triggers, got %+v err=%v", trig, err)
	}
	// Spot=210 triggers ord-tp-1.
	trig, err = store.EvaluatePriceTick(ctx, 8453, token, big.NewInt(210), now.Add(4*time.Minute))
	if err != nil || len(trig) != 1 || trig[0].OrderID != "ord-tp-1" {
		t.Fatalf("expected ord-tp-1 triggered at spot=210, got %+v err=%v", trig, err)
	}
	// Crash-recovery ListTriggeredOrders returns ord-limit-1 and ord-tp-1 until marked EXECUTED.
	pendingTrig, err := store.ListTriggeredOrders(ctx, 8453)
	if err != nil || len(pendingTrig) != 2 {
		t.Fatalf("expected 2 TRIGGERED orders recoverable after restart, got %+v err=%v", pendingTrig, err)
	}
	if err := store.MarkConditionalOrderExecuted(ctx, "ord-tp-1", now.Add(5*time.Minute)); err != nil {
		t.Fatalf("MarkConditionalOrderExecuted failed: %v", err)
	}
	// Cancel OCO sibling ord-sl-1 explicitly; also verify expired orders do not trigger.
	if err := store.CancelConditionalOrder(ctx, "ord-sl-1", now.Add(5*time.Minute)); err != nil {
		t.Fatalf("CancelConditionalOrder failed: %v", err)
	}
	// Past expiry (now + 2h) expires ord-sl-1 without triggering even at spot=50.
	trig, err = store.EvaluatePriceTick(ctx, 8453, token, big.NewInt(50), now.Add(2*time.Hour))
	if err != nil || len(trig) != 0 {
		t.Fatalf("expected expired ord-sl-1 not to trigger, got %+v err=%v", trig, err)
	}

	// 2. Paper/Shadow mode is default OFF; when enabled, tracks simulated PnL with zero broadcast.
	disabledShadow, err := NewPaperShadowEngine(store, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := disabledShadow.RecordShadowFill(ctx, "f-off", 8453, "w-base-1", token, "buy", big.NewInt(1000), big.NewInt(500), big.NewInt(2), "sim", now); err == nil {
		t.Fatal("expected disabled paper shadow engine to reject fill")
	}

	shadow, err := NewPaperShadowEngine(store, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := shadow.RecordShadowFill(ctx, "f-1", 8453, "w-base-1", token, "buy", big.NewInt(1000), big.NewInt(500), big.NewInt(2), "limit", now); err != nil {
		t.Fatal(err)
	}
	if err := shadow.RecordShadowFill(ctx, "f-2", 8453, "w-base-1", token, "sell", big.NewInt(500), big.NewInt(1400), big.NewInt(3), "tp", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	netTok, netNat, err := shadow.ShadowPosition(ctx, 8453, "w-base-1", token)
	if err != nil || netTok.Sign() != 0 || netNat.Cmp(big.NewInt(400)) != 0 {
		t.Fatalf("expected netToken=0 netNative=+400, got tok=%s nat=%s err=%v", netTok, netNat, err)
	}
}
