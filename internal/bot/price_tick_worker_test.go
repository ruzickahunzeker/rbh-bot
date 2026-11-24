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

type stubTickQuoter struct {
	spotWei *big.Int
	sellOut *big.Int
}

func (q *stubTickQuoter) QuoteShadowFill(_ context.Context, _ uint64, _ string, _ common.Address, side string, _ *big.Int) (*big.Int, *big.Int, error) {
	if side == "sell" {
		return new(big.Int).Set(q.sellOut), new(big.Int).Set(q.spotWei), nil
	}
	return big.NewInt(1_000_000), new(big.Int).Set(q.spotWei), nil
}

func TestPriceTickWorkerTriggersAndExecutesShadowTP(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.BotOwner, filepath.Join(t.TempDir(), "bot-tick.db"))
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
	shadow, err := NewPaperShadowEngine(store, true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 25, 0, 20, 0, 0, time.UTC)
	token := common.HexToAddress("0x55d398326f99059fF775485246999027B3197955")

	// Seed an existing shadow buy of 1,000,000 raw tokens.
	if err := shadow.RecordShadowFill(ctx, "seed-buy-1", 56, "wallet-bsc-readonly", token, "buy", big.NewInt(1e17), big.NewInt(1_000_000), big.NewInt(100), "seed", now); err != nil {
		t.Fatal(err)
	}
	// Create a Take-Profit order at spot >= 200 selling 50% (5000 bps).
	if _, err := store.CreateConditionalOrder(ctx, ConditionalOrder{
		OrderID:         "tp-1",
		ChainID:         56,
		WalletID:        "wallet-bsc-readonly",
		Token:           token,
		Kind:            OrderTakeProfit,
		TriggerPriceWei: big.NewInt(200),
		SellBps:         5000,
		ExpiresAt:       now.Add(time.Hour),
	}, now); err != nil {
		t.Fatal(err)
	}

	quoter := &stubTickQuoter{spotWei: big.NewInt(150), sellOut: big.NewInt(1e17)}
	var notified []ConditionalOrder
	worker, err := NewPriceTickWorker(store, shadow, quoter, func(ord ConditionalOrder, _, _ *big.Int) {
		notified = append(notified, ord)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Spot 150 < 200 -> should NOT trigger.
	n, err := worker.TickOnce(ctx, now.Add(time.Second))
	if err != nil || n != 0 || len(notified) != 0 {
		t.Fatalf("expected 0 triggers at spot 150, got n=%d notified=%d err=%v", n, len(notified), err)
	}

	// Spot jumps to 220 >= 200 -> triggers TP once, records 50% shadow sell, marks EXECUTED.
	quoter.spotWei = big.NewInt(220)
	n, err = worker.TickOnce(ctx, now.Add(2*time.Second))
	if err != nil || n != 1 || len(notified) != 1 {
		t.Fatalf("expected 1 trigger at spot 220, got n=%d notified=%d err=%v", n, len(notified), err)
	}
	// Second tick at spot 250 must NOT re-trigger.
	quoter.spotWei = big.NewInt(250)
	n, err = worker.TickOnce(ctx, now.Add(3*time.Second))
	if err != nil || n != 0 {
		t.Fatalf("expected 0 duplicate triggers, got n=%d err=%v", n, err)
	}
	var state string
	_ = store.db.QueryRowContext(ctx, `SELECT state FROM conditional_orders WHERE order_id = 'tp-1'`).Scan(&state)
	if state != "EXECUTED" {
		t.Fatalf("expected order state EXECUTED, got %q", state)
	}
}
