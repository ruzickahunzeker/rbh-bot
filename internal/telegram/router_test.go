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

type stubBackend struct {
	lastChain  uint64
	lastSide   string
	lastAmount *big.Int
	watched    []common.Address
	tokenBal   *big.Int
}

func (b *stubBackend) ListWallets(_ context.Context, _ uint64) ([]string, error) {
	return []string{"w-1"}, nil
}
func (b *stubBackend) ReadPosition(_ context.Context, _ uint64, _ string, _ common.Address) (*big.Int, *big.Int, error) {
	return new(big.Int).Set(b.tokenBal), big.NewInt(5_000_000_000_000_000), nil
}
func (b *stubBackend) SubmitManualTrade(_ context.Context, chainID uint64, _, side string, _ common.Address, amountRaw *big.Int) (string, error) {
	b.lastChain = chainID
	b.lastSide = side
	b.lastAmount = new(big.Int).Set(amountRaw)
	return "req-tg-1", nil
}
func (b *stubBackend) AddWatchedWallet(_ context.Context, _ uint64, trader common.Address) error {
	b.watched = append(b.watched, trader)
	return nil
}
func (b *stubBackend) ConfigureCopyStrategy(_ context.Context, _ uint64, _ common.Address, _ *big.Int) error {
	return nil
}

type stubUpdateFeed struct {
	goneOnce   bool
	reconciled bool
	resetSeq   int64
}

func (f *stubUpdateFeed) FetchUpdates(_ context.Context, _ uint64, afterSeq int64, _ int) ([]UpdateNotice, int64, error) {
	if f.goneOnce {
		f.goneOnce = false
		return nil, afterSeq, ErrCursorOutOfRange
	}
	return []UpdateNotice{{EventSeq: afterSeq + 1, OperationKind: "trade", RequestID: "req-1", State: "CONFIRMED"}}, afterSeq + 1, nil
}
func (f *stubUpdateFeed) ReconcileOpenJobs(_ context.Context, _ uint64) (int64, error) {
	f.reconciled = true
	return f.resetSeq, nil
}

func TestStep3BTelegramAllowlistMultiChainExactSellAnd410Reconcile(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.BotOwner, filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	backend := &stubBackend{tokenBal: big.NewInt(950_000)}
	router, err := NewRouter(db, backend, func() time.Time { return time.Date(2026, 11, 24, 16, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}

	// AT-23: Non-whitelisted chat_id fails closed with ErrUnauthorizedChat.
	if _, err := router.HandleCommand(ctx, 999999, "/wallets"); !errors.Is(err, ErrUnauthorizedChat) {
		t.Fatalf("expected ErrUnauthorizedChat, got %v", err)
	}

	if err := router.AllowChat(ctx, 1001, "alice", "operator", 4663, "w-1"); err != nil {
		t.Fatal(err)
	}
	// Switch explicitly to BSC (56).
	resp, err := router.HandleCommand(ctx, 1001, "/chain bsc")
	if err != nil || !strings.Contains(resp, "chain_id=56") {
		t.Fatalf("chain switch failed: resp=%q err=%v", resp, err)
	}
	// 100% sell translates exact token balance (950_000) to amount_raw without dust.
	resp, err = router.HandleCommand(ctx, 1001, "/sell 0x000000000000000000000000000000000000BEEF 100%")
	if err != nil || backend.lastChain != 56 || backend.lastSide != "sell" || backend.lastAmount.Cmp(big.NewInt(950_000)) != 0 {
		t.Fatalf("100%% sell failed: resp=%q chain=%d side=%s amt=%v err=%v", resp, backend.lastChain, backend.lastSide, backend.lastAmount, err)
	}

	// AT-26: 410 Gone triggers ReconcileOpenJobs before resetting cursor.
	feed := &stubUpdateFeed{goneOnce: true, resetSeq: 42}
	_, reconciled, err := router.PollTradeUpdates(ctx, 56, "tg-notifier", feed, 100)
	if err != nil || !reconciled || !feed.reconciled {
		t.Fatalf("expected 410 Gone reconciliation, got reconciled=%v feed.reconciled=%v err=%v", reconciled, feed.reconciled, err)
	}
	// Next poll resumes cleanly from seq=42 -> receives seq=43.
	events, reconciled, err := router.PollTradeUpdates(ctx, 56, "tg-notifier", feed, 100)
	if err != nil || reconciled || len(events) != 1 || events[0].EventSeq != 43 {
		t.Fatalf("expected resumed poll from 42->43, got events=%+v reconciled=%v err=%v", events, reconciled, err)
	}
}

func TestRouterConditionalOrdersWatchlistAndStatus(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.BotOwner, filepath.Join(t.TempDir(), "bot-m8.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	backend := NewUDSBackendWithWatchlist(map[uint64]ChainUDSConfig{
		56:   {ChainSlug: "bsc", TradeSocket: "/tmp/nonexistent-bsc.sock"},
		4663: {ChainSlug: "rbh", TradeSocket: "/tmp/nonexistent-rbh.sock"},
	}, db, nil)
	now := time.Date(2026, 11, 24, 23, 59, 0, 0, time.UTC)
	router, err := NewRouter(db, backend, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := router.AllowChat(ctx, 7131128175, "op", "operator", 56, "wallet-bsc-readonly"); err != nil {
		t.Fatal(err)
	}

	// 1. /watch -> /watchlist -> /unwatch -> /watchlist
	trader := "0x000000000000000000000000000000000000cafe"
	if resp, err := router.HandleCommand(ctx, 7131128175, "/watch "+trader); err != nil || !strings.Contains(resp, "synced") {
		t.Fatalf("/watch failed: resp=%q err=%v", resp, err)
	}
	if resp, err := router.HandleCommand(ctx, 7131128175, "/watchlist"); err != nil || !strings.Contains(resp, trader) {
		t.Fatalf("/watchlist missing trader: resp=%q err=%v", resp, err)
	}
	if resp, err := router.HandleCommand(ctx, 7131128175, "/unwatch "+trader); err != nil || !strings.Contains(resp, "Removed") {
		t.Fatalf("/unwatch failed: resp=%q err=%v", resp, err)
	}
	if resp, err := router.HandleCommand(ctx, 7131128175, "/watchlist"); err != nil || !strings.Contains(resp, "empty") {
		t.Fatalf("/watchlist should be empty: resp=%q err=%v", resp, err)
	}

	// 2. /tp -> /sl -> /orders -> /cancelorder
	token := "0x55d398326f99059ff775485246999027b3197955"
	tpResp, err := router.HandleCommand(ctx, 7131128175, "/tp "+token+" 2000000000 50")
	if err != nil || !strings.Contains(tpResp, "take_profit") || !strings.Contains(tpResp, "sell=50%") {
		t.Fatalf("/tp failed: resp=%q err=%v", tpResp, err)
	}
	now = now.Add(time.Second)
	slResp, err := router.HandleCommand(ctx, 7131128175, "/sl "+token+" 500000000 100")
	if err != nil || !strings.Contains(slResp, "stop_loss") || !strings.Contains(slResp, "sell=100%") {
		t.Fatalf("/sl failed: resp=%q err=%v", slResp, err)
	}
	ordersResp, err := router.HandleCommand(ctx, 7131128175, "/orders")
	if err != nil || !strings.Contains(ordersResp, "take_profit") || !strings.Contains(ordersResp, "stop_loss") {
		t.Fatalf("/orders missing entries: resp=%q err=%v", ordersResp, err)
	}
	// Extract order_id from tpResp ("Created take_profit order <id> on chain 56...")
	fields := strings.Fields(tpResp)
	orderID := fields[3]
	if cancelResp, err := router.HandleCommand(ctx, 7131128175, "/cancelorder "+orderID); err != nil || !strings.Contains(cancelResp, "cancelled") {
		t.Fatalf("/cancelorder failed: resp=%q err=%v", cancelResp, err)
	}

	// 3. /status reports both BSC and RBH
	statusResp, err := router.HandleCommand(ctx, 7131128175, "/status")
	if err != nil || !strings.Contains(statusResp, "BSC (56)") || !strings.Contains(statusResp, "RBH (4663)") {
		t.Fatalf("/status failed: resp=%q err=%v", statusResp, err)
	}
}
