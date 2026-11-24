package bot

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type stubRetrySubmitter struct {
	keys       []string
	failFirst  bool
	callCount  int
}

func (s *stubRetrySubmitter) SubmitCopyJob(_ context.Context, _ uint64, _, idempotencyKey string) (string, bool, error) {
	s.callCount++
	s.keys = append(s.keys, idempotencyKey)
	if s.failFirst && s.callCount == 1 {
		return "", true, errors.New("409 WALLET_IN_FLIGHT")
	}
	return "req-ok-1", false, nil
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
	now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)

	// 1. AT-15: feed-rbh (4663) AND feed-bsc (56) both send offset=1 independently without conflict.
	rRBH, err := store.IngestMultiChainSignal(ctx, "p1", "strat-1", "w-rbh", MultiChainSignal{
		ChainID: 4663, FeedInstanceID: "feed-rbh", FeedOffset: 1,
		ObservationID: "obs-rbh-1", TxHash: "0x1111", TraderAddress: "0xaaaa", TokenAddress: "0xbbbb",
		Side: "buy", Stage: "included", FirstObservedAt: now.Add(-1 * time.Second),
	}, 10*time.Second, now)
	if err != nil || rRBH.Disposition != "QUEUED" {
		t.Fatalf("RBH offset=1 failed: %+v err=%v", rRBH, err)
	}
	rBSC1, err := store.IngestMultiChainSignal(ctx, "p1", "strat-1", "w-bsc", MultiChainSignal{
		ChainID: 56, FeedInstanceID: "feed-bsc", FeedOffset: 1,
		ObservationID: "obs-bsc-v2-pool", TxHash: "0x2222", TraderAddress: "0xaaaa", TokenAddress: "0xcccc",
		Side: "buy", Stage: "included", FirstObservedAt: now.Add(-1 * time.Second),
	}, 10*time.Second, now)
	if err != nil || rBSC1.Disposition != "QUEUED" {
		t.Fatalf("BSC offset=1 failed: %+v err=%v", rBSC1, err)
	}

	// Verify independent cursors.
	cRBH, _ := store.LoadFeedCursor(ctx, 4663, "feed-rbh")
	cBSC, _ := store.LoadFeedCursor(ctx, 56, "feed-bsc")
	if cRBH != 1 || cBSC != 1 {
		t.Fatalf("expected independent cursors (1,1), got (%d,%d)", cRBH, cBSC)
	}

	// 2. AT-15: Same BSC tx split across Pancake V2 + V3 pools (different observation_id & feed_offset=2)
	// maps to the SAME trigger_id -> DuplicateTrigger=true, zero second copy-trade decision or key!
	rBSC2, err := store.IngestMultiChainSignal(ctx, "p1", "strat-1", "w-bsc", MultiChainSignal{
		ChainID: 56, FeedInstanceID: "feed-bsc", FeedOffset: 2,
		ObservationID: "obs-bsc-v3-pool", TxHash: "0x2222", TraderAddress: "0xaaaa", TokenAddress: "0xcccc",
		Side: "buy", Stage: "included", FirstObservedAt: now.Add(-1 * time.Second),
	}, 10*time.Second, now)
	if err != nil || !rBSC2.DuplicateTrigger || rBSC2.IdempotencyKey != rBSC1.IdempotencyKey {
		t.Fatalf("expected aggregator split to dedupe by trigger_id: %+v err=%v", rBSC2, err)
	}

	// 3. AT-16: Stale signal exceeding max_signal_age_ms advances cursor and marks EXPIRED_SKIPPED.
	rStale, err := store.IngestMultiChainSignal(ctx, "p1", "strat-1", "w-bsc", MultiChainSignal{
		ChainID: 56, FeedInstanceID: "feed-bsc", FeedOffset: 3,
		ObservationID: "obs-bsc-stale", TxHash: "0x3333", TraderAddress: "0xaaaa", TokenAddress: "0xdddd",
		Side: "buy", Stage: "included", FirstObservedAt: now.Add(-60 * time.Second),
	}, 10*time.Second, now)
	if err != nil || rStale.Disposition != "EXPIRED_SKIPPED" {
		t.Fatalf("expected stale signal EXPIRED_SKIPPED, got %+v err=%v", rStale, err)
	}

	// 4. AT-16: Serial dispatcher retries 409 WALLET_IN_FLIGHT preserving the EXACT same Idempotency-Key.
	sub := &stubRetrySubmitter{failFirst: true}
	dispatched, retryable, err := store.DispatchNextForWallet(ctx, 56, "w-bsc", sub, now)
	if !dispatched || !retryable || err == nil {
		t.Fatalf("expected retryable WALLET_IN_FLIGHT on first attempt: dispatched=%v retryable=%v err=%v", dispatched, retryable, err)
	}
	dispatched, retryable, err = store.DispatchNextForWallet(ctx, 56, "w-bsc", sub, now.Add(time.Second))
	if !dispatched || retryable || err != nil {
		t.Fatalf("expected second attempt to succeed: dispatched=%v retryable=%v err=%v", dispatched, retryable, err)
	}
	if len(sub.keys) != 2 || sub.keys[0] != sub.keys[1] || sub.keys[0] != rBSC1.IdempotencyKey {
		t.Fatalf("expected immutable Idempotency-Key across retry, got %v", sub.keys)
	}
}
