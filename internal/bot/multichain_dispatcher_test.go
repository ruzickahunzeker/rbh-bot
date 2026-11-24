package bot

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type stubSubmitter struct {
	failOnce bool
	calls    []string
}

func (s *stubSubmitter) SubmitCopyJob(_ context.Context, _ uint64, _, idempotencyKey string) (string, bool, error) {
	s.calls = append(s.calls, idempotencyKey)
	if s.failOnce {
		s.failOnce = false
		return "", true, errors.New("409 WALLET_IN_FLIGHT")
	}
	return "req-" + idempotencyKey, false, nil
}

func TestStep3AMultiChainIndependentCursorsTriggerDedupeAndSerialRetry(t *testing.T) {
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

	// P0-4: Verify watched_wallets accepts multi-chain BSC (chain_id=56) rows after migration 004.
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO watched_wallets(id, chain_id, address, enabled) VALUES('ww-bsc-1', 56, '0x000000000000000000000000000000000000aaaa', 1)`); err != nil {
		t.Fatalf("expected multi-chain watched_wallets to accept chain_id=56: %v", err)
	}

	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	// P1-4: Unknown signal Side must fail closed (never silently map to copy_buy).
	if _, err := store.IngestMultiChainSignal(ctx, "p1", "strat-bsc", "w-bsc", MultiChainSignal{
		ChainID:         56,
		FeedInstanceID:  "feed-bsc",
		FeedOffset:      1,
		ObservationID:   "obs-bad-side",
		TxHash:          "0xbad",
		TraderAddress:   "0x000000000000000000000000000000000000AAAA",
		TokenAddress:    "0x000000000000000000000000000000000000BBBB",
		Side:            "add_liquidity",
		Stage:           "included",
		FirstObservedAt: now,
	}, 10*time.Second, now); err == nil {
		t.Fatal("expected unknown signal side to fail closed")
	}

	// 1. Both RBH (4663) and BSC (56) start at feed_offset=1 independently without collision!
	rbhRes, err := store.IngestMultiChainSignal(ctx, "p1", "strat-rbh", "w-rbh", MultiChainSignal{
		ChainID:         4663,
		FeedInstanceID:  "feed-rbh",
		FeedOffset:      1,
		ObservationID:   "obs-rbh-1",
		TxHash:          "0xaaa1",
		TraderAddress:   "0x000000000000000000000000000000000000AAAA",
		TokenAddress:    "0x0000000000000000000000000000000000001111",
		Side:            "buy",
		Stage:           "included",
		FirstObservedAt: now.Add(-1 * time.Second),
	}, 10*time.Second, now)
	if err != nil || rbhRes.Disposition != "QUEUED" {
		t.Fatalf("RBH offset=1 failed: %+v err=%v", rbhRes, err)
	}

	bscRes1, err := store.IngestMultiChainSignal(ctx, "p1", "strat-bsc", "w-bsc", MultiChainSignal{
		ChainID:         56,
		FeedInstanceID:  "feed-bsc",
		FeedOffset:      1,
		ObservationID:   "obs-bsc-1-v2leg",
		TxHash:          "0xbbb1",
		TraderAddress:   "0x000000000000000000000000000000000000AAAA",
		TokenAddress:    "0x0000000000000000000000000000000000002222",
		Side:            "buy",
		Stage:           "included",
		FirstObservedAt: now.Add(-1 * time.Second),
	}, 10*time.Second, now)
	if err != nil || bscRes1.Disposition != "QUEUED" {
		t.Fatalf("BSC offset=1 failed: %+v err=%v", bscRes1, err)
	}

	// 2. Same BSC tx split across V2+V3 pools (different offset=2, different observation_id, same net trigger_id)
	// must collapse to the EXACT same decision without creating a second copy-trade!
	bscResSplit, err := store.IngestMultiChainSignal(ctx, "p1", "strat-bsc", "w-bsc", MultiChainSignal{
		ChainID:         56,
		FeedInstanceID:  "feed-bsc",
		FeedOffset:      2,
		ObservationID:   "obs-bsc-2-v3leg",
		TxHash:          "0xbbb1",
		TraderAddress:   "0x000000000000000000000000000000000000aaaa", // case-insensitive
		TokenAddress:    "0x0000000000000000000000000000000000002222",
		Side:            "buy",
		Stage:           "included",
		FirstObservedAt: now.Add(-1 * time.Second),
	}, 10*time.Second, now)
	if err != nil || !bscResSplit.DuplicateTrigger || bscResSplit.DecisionID != bscRes1.DecisionID {
		t.Fatalf("expected cross-pool split dedupe to return original decision, got %+v err=%v", bscResSplit, err)
	}

	// 3. Stale signal after disconnect (> maxSignalAge) must be EXPIRED_SKIPPED, never queued!
	staleRes, err := store.IngestMultiChainSignal(ctx, "p1", "strat-bsc", "w-bsc", MultiChainSignal{
		ChainID:         56,
		FeedInstanceID:  "feed-bsc",
		FeedOffset:      3,
		ObservationID:   "obs-bsc-3-stale",
		TxHash:          "0xbbb3",
		TraderAddress:   "0x000000000000000000000000000000000000AAAA",
		TokenAddress:    "0x0000000000000000000000000000000000003333",
		Side:            "buy",
		Stage:           "included",
		FirstObservedAt: now.Add(-60 * time.Second),
	}, 10*time.Second, now)
	if err != nil || staleRes.Disposition != "EXPIRED_SKIPPED" {
		t.Fatalf("expected EXPIRED_SKIPPED for stale signal, got %+v err=%v", staleRes, err)
	}

	// Queue a second fresh BSC job on the SAME wallet to verify strict serial wallet lane.
	bscRes2, err := store.IngestMultiChainSignal(ctx, "p1", "strat-bsc", "w-bsc", MultiChainSignal{
		ChainID:         56,
		FeedInstanceID:  "feed-bsc",
		FeedOffset:      4,
		ObservationID:   "obs-bsc-4-second",
		TxHash:          "0xbbb4",
		TraderAddress:   "0x000000000000000000000000000000000000AAAA",
		TokenAddress:    "0x0000000000000000000000000000000000004444",
		Side:            "buy",
		Stage:           "included",
		FirstObservedAt: now.Add(-1 * time.Second),
	}, 10*time.Second, now.Add(10*time.Millisecond))
	if err != nil || bscRes2.Disposition != "QUEUED" {
		t.Fatalf("BSC second job failed: %+v err=%v", bscRes2, err)
	}

	// Verify independent durable cursors.
	rbhCursor, _ := store.LoadFeedCursor(ctx, 4663, "feed-rbh")
	bscCursor, _ := store.LoadFeedCursor(ctx, 56, "feed-bsc")
	if rbhCursor != 1 || bscCursor != 4 {
		t.Fatalf("independent cursors mismatch: rbh=%d bsc=%d", rbhCursor, bscCursor)
	}

	// 4. Serial dispatcher: first attempt hits 409 WALLET_IN_FLIGHT -> backoff prevents immediate retry;
	// after backoff elapses, retry uses the EXACT SAME immutable Idempotency-Key.
	sub := &stubSubmitter{failOnce: true}
	dispatched, retryable, err := store.DispatchNextForWallet(ctx, 56, "w-bsc", sub, now)
	if !dispatched || !retryable || err == nil {
		t.Fatalf("expected retryable 409 on first dispatch, got dispatched=%v retryable=%v err=%v", dispatched, retryable, err)
	}
	// Immediate retry before next_retry_at must NOT dispatch!
	dispatched, _, err = store.DispatchNextForWallet(ctx, 56, "w-bsc", sub, now.Add(500*time.Millisecond))
	if dispatched || err != nil {
		t.Fatalf("expected backoff to suppress immediate retry, got dispatched=%v err=%v", dispatched, err)
	}
	// After 2s backoff, retry dispatches Job 1 with the exact same Idempotency-Key.
	dispatched, retryable, err = store.DispatchNextForWallet(ctx, 56, "w-bsc", sub, now.Add(2*time.Second))
	if !dispatched || retryable || err != nil {
		t.Fatalf("expected retry after backoff to succeed, got dispatched=%v retryable=%v err=%v", dispatched, retryable, err)
	}
	if len(sub.calls) != 2 || sub.calls[0] != sub.calls[1] || sub.calls[0] != bscRes1.IdempotencyKey {
		t.Fatalf("Idempotency-Key mutated across retry: %v (want %s)", sub.calls, bscRes1.IdempotencyKey)
	}

	// 5. While Job 1 is SUBMITTED (not yet terminal), Job 2 on the same wallet MUST NOT dispatch.
	dispatched, retryable, err = store.DispatchNextForWallet(ctx, 56, "w-bsc", sub, now.Add(3*time.Second))
	if dispatched || !retryable || err != nil {
		t.Fatalf("expected serial wallet lane to block Job 2 while Job 1 is SUBMITTED: dispatched=%v retryable=%v err=%v", dispatched, retryable, err)
	}
	// Once Job 1 reaches COMPLETED, Job 2 dispatches cleanly!
	if err := store.MarkJobTerminal(ctx, bscRes1.IdempotencyKey, "COMPLETED", "", now.Add(4*time.Second)); err != nil {
		t.Fatalf("MarkJobTerminal failed: %v", err)
	}
	dispatched, retryable, err = store.DispatchNextForWallet(ctx, 56, "w-bsc", sub, now.Add(5*time.Second))
	if !dispatched || retryable || err != nil {
		t.Fatalf("expected Job 2 to dispatch after Job 1 completed: dispatched=%v retryable=%v err=%v", dispatched, retryable, err)
	}
	if len(sub.calls) != 3 || sub.calls[2] != bscRes2.IdempotencyKey {
		t.Fatalf("expected Job 2 key %s, got calls %v", bscRes2.IdempotencyKey, sub.calls)
	}
}
