package bot

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/feed"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type memorySource struct {
	items []feed.OutboxItem
}

func TestConsumerReadsCommittedFeedOutboxIntoBotDatabase(t *testing.T) {
	ctx := context.Background()
	feedDB, err := storage.Open(ctx, storage.FeedOwner, filepath.Join(t.TempDir(), "feed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer feedDB.Close()
	if err := feedDB.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	feedStore, err := feed.NewStore(feedDB)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"schema_version": 1, "type": "transaction_intent", "intent_kind": "buy",
		"sender": watchAddress.Hex(), "currency_out": tokenAddress.Hex(), "confirmed": false,
	})
	observation := feed.Observation{
		ChainID: feed.ChainID, Source: feed.SourceSequencer, SourceSequence: 10,
		TransactionHash: common.HexToHash("0x10"), StableActionPath: "intent/000/buy",
		PayloadJSON: payload, ObservedAt: time.Unix(100, 0),
	}
	if _, err := feedStore.CommitSequencer(ctx, []feed.Observation{observation}, 10); err != nil {
		t.Fatal(err)
	}

	botStore, closeBot := testStore(t)
	defer closeBot()
	seedStrategy(t, botStore, StrategyConfig{CopyBuys: true, FixedBuyAmount: "100", MaxBuyAmount: "200", ApplicationTTLSeconds: 60})
	consumer, err := NewConsumer(botStore, feedStore, 100)
	if err != nil {
		t.Fatal(err)
	}
	consumer.now = func() time.Time { return time.Unix(101, 0) }
	if count, err := consumer.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	assertCounts(t, botStore, 1, 1, 1)
}

func (s memorySource) ReadOutboxAfter(_ context.Context, after int64, limit int) ([]feed.OutboxItem, error) {
	result := make([]feed.OutboxItem, 0, limit)
	for _, item := range s.items {
		if item.Offset > after && len(result) < limit {
			result = append(result, item)
		}
	}
	return result, nil
}

func TestConsumerRestartResumesFromDurableOffset(t *testing.T) {
	store, closeDB := testStore(t)
	defer closeDB()
	seedStrategy(t, store, StrategyConfig{CopyBuys: true, CopySells: true, FixedBuyAmount: "100", MaxBuyAmount: "200", SellBPS: 2500, ApplicationTTLSeconds: 60})
	source := memorySource{items: []feed.OutboxItem{
		intentItem(t, 1, "obs-1", "buy", false),
		intentItem(t, 2, "obs-2", "sell", false),
	}}
	first, err := NewConsumer(store, source, 1)
	if err != nil {
		t.Fatal(err)
	}
	first.now = func() time.Time { return time.Unix(100, 0) }
	if count, err := first.RunOnce(context.Background()); err != nil || count != 1 {
		t.Fatalf("first count=%d err=%v", count, err)
	}

	// A new consumer represents process restart. It reads bot_progress and starts
	// strictly after offset 1, so the committed event cannot duplicate.
	restarted, err := NewConsumer(store, source, 100)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return time.Unix(101, 0) }
	if count, err := restarted.RunOnce(context.Background()); err != nil || count != 1 {
		t.Fatalf("restart count=%d err=%v", count, err)
	}
	assertCounts(t, store, 2, 2, 2)
	if count, err := restarted.RunOnce(context.Background()); err != nil || count != 0 {
		t.Fatalf("drained count=%d err=%v", count, err)
	}
}
