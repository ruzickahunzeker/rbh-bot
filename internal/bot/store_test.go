package bot

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/feed"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

var (
	watchAddress = common.HexToAddress("0x1000000000000000000000000000000000000001")
	tokenAddress = common.HexToAddress("0x2000000000000000000000000000000000000002")
)

func TestProcessFeedEventPersistsIntentProgressAndDedupe(t *testing.T) {
	store, closeDB := testStore(t)
	defer closeDB()
	seedStrategy(t, store, StrategyConfig{CopyBuys: true, FixedBuyAmount: "100", MaxBuyAmount: "200"})
	item := intentItem(t, 1, "obs-1", "buy", false)

	result, err := store.ProcessFeedEvent(context.Background(), item, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if result.OperationIntents != 1 || result.Disposition != "operation_intent_created" || result.DurableEventOffset != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	assertCounts(t, store, 1, 1, 1)
	var storedPayload string
	if err := store.db.QueryRow(`SELECT payload_json FROM operation_intents`).Scan(&storedPayload); err != nil {
		t.Fatal(err)
	}
	var stored OperationIntent
	if err := json.Unmarshal([]byte(storedPayload), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.PolicyVersion != 1 || stored.Policy.FixedBuyAmount != "100" || stored.WatchedWalletID != "wallet-1" || stored.ID != stored.IdempotencyKey {
		t.Fatalf("incomplete deterministic intent: %#v", stored)
	}

	replay, err := store.ProcessFeedEvent(context.Background(), item, time.Unix(101, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Duplicate || replay.DurableEventOffset != 1 {
		t.Fatalf("unexpected replay: %#v", replay)
	}
	assertCounts(t, store, 1, 1, 1)
}

func TestPolicyBlocksUnconfirmedWhenRequired(t *testing.T) {
	store, closeDB := testStore(t)
	defer closeDB()
	seedStrategy(t, store, StrategyConfig{CopyBuys: true, RequireConfirmed: true, FixedBuyAmount: "100", MaxBuyAmount: "200"})
	result, err := store.ProcessFeedEvent(context.Background(), intentItem(t, 1, "obs-1", "buy", false), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != "policy_blocked" || result.OperationIntents != 0 {
		t.Fatalf("unexpected policy decision: %#v", result)
	}
	assertCounts(t, store, 1, 0, 1)
}

func TestRetractionIsDurableAndIdempotent(t *testing.T) {
	store, closeDB := testStore(t)
	defer closeDB()
	seedStrategy(t, store, StrategyConfig{CopyBuys: true, FixedBuyAmount: "100", MaxBuyAmount: "200"})
	if _, err := store.ProcessFeedEvent(context.Background(), intentItem(t, 1, "obs-original", "buy", false), time.Now()); err != nil {
		t.Fatal(err)
	}
	retraction := retractionItem(t, 2, "obs-retraction", "obs-original")
	result, err := store.ProcessFeedEvent(context.Background(), retraction, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.RetractedIntents != 1 || result.Disposition != "retraction_applied" {
		t.Fatalf("unexpected retraction: %#v", result)
	}
	var status string
	if err := store.db.QueryRow(`SELECT status FROM operation_intents`).Scan(&status); err != nil || status != "retracted" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	if replay, err := store.ProcessFeedEvent(context.Background(), retraction, time.Now()); err != nil || !replay.Duplicate {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
}

func TestGapAndCommitFailureFailClosed(t *testing.T) {
	store, closeDB := testStore(t)
	defer closeDB()
	seedStrategy(t, store, StrategyConfig{CopyBuys: true, FixedBuyAmount: "100", MaxBuyAmount: "200"})
	if _, err := store.ProcessFeedEvent(context.Background(), intentItem(t, 2, "obs-gap", "buy", false), time.Now()); !errors.Is(err, ErrFeedGap) {
		t.Fatalf("got %v, want gap", err)
	}
	assertCounts(t, store, 0, 0, 0)
	if _, err := store.db.Exec(`DROP TABLE operation_intents`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProcessFeedEvent(context.Background(), intentItem(t, 1, "obs-1", "buy", false), time.Now()); err == nil {
		t.Fatal("expected operation intent write failure")
	}
	var inbox, offset int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM signal_inbox`).Scan(&inbox); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT durable_event_offset FROM bot_progress WHERE id=1`).Scan(&offset); err != nil {
		t.Fatal(err)
	}
	if inbox != 0 || offset != 0 {
		t.Fatalf("partial commit inbox=%d offset=%d", inbox, offset)
	}
}

func TestStrategyValidationRejectsUnsafeAmounts(t *testing.T) {
	for _, config := range []StrategyConfig{
		{CopyBuys: true, FixedBuyAmount: "0", MaxBuyAmount: "1"},
		{CopyBuys: true, FixedBuyAmount: "2", MaxBuyAmount: "1"},
		{CopySells: true, SellBPS: 10_001},
		{},
	} {
		if err := config.Validate(); !errors.Is(err, ErrInvalidStrategy) {
			t.Fatalf("config=%#v err=%v", config, err)
		}
	}
}

func TestStrategyVersionIsMonotonicAndSameVersionImmutable(t *testing.T) {
	store, closeDB := testStore(t)
	defer closeDB()
	seedStrategy(t, store, StrategyConfig{CopyBuys: true, FixedBuyAmount: "100", MaxBuyAmount: "200"})
	changed := Strategy{ID: "strategy-1", WatchedWalletID: "wallet-1", Version: 1, Enabled: true, Config: StrategyConfig{CopyBuys: true, FixedBuyAmount: "101", MaxBuyAmount: "200"}}
	if err := store.UpsertStrategy(context.Background(), changed); !errors.Is(err, ErrStrategyConflict) {
		t.Fatalf("same-version mutation err=%v", err)
	}
	changed.Version = 2
	if err := store.UpsertStrategy(context.Background(), changed); err != nil {
		t.Fatal(err)
	}
	changed.Version = 1
	if err := store.UpsertStrategy(context.Background(), changed); !errors.Is(err, ErrStrategyConflict) {
		t.Fatalf("version rollback err=%v", err)
	}
}

func testStore(t *testing.T) (*Store, func()) {
	t.Helper()
	database, err := storage.Open(context.Background(), storage.BotOwner, filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(context.Background()); err != nil {
		database.Close()
		t.Fatal(err)
	}
	store, err := NewStore(database)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	return store, func() { _ = database.Close() }
}

func seedStrategy(t *testing.T, store *Store, config StrategyConfig) {
	t.Helper()
	ctx := context.Background()
	if err := store.UpsertWatchedWallet(ctx, WatchedWallet{ID: "wallet-1", ChainID: 4663, Address: watchAddress, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertStrategy(ctx, Strategy{ID: "strategy-1", WatchedWalletID: "wallet-1", Version: 1, Enabled: true, Config: config}); err != nil {
		t.Fatal(err)
	}
}

func intentItem(t *testing.T, offset int64, observationID, kind string, confirmed bool) feed.OutboxItem {
	t.Helper()
	currencyIn, currencyOut := tokenAddress.Hex(), common.HexToAddress("0x3000000000000000000000000000000000000003").Hex()
	if kind == "sell" {
		currencyOut = common.HexToAddress("0x4000000000000000000000000000000000000004").Hex()
	}
	payload, err := json.Marshal(map[string]any{
		"schema_version": 1, "type": "transaction_intent", "intent_kind": kind,
		"sender": watchAddress.Hex(), "currency_in": currencyIn, "currency_out": currencyOut, "confirmed": confirmed,
	})
	if err != nil {
		t.Fatal(err)
	}
	return feed.OutboxItem{Offset: offset, ObservationID: observationID, Source: feed.SourceSequencer, SourceSequence: uint64(offset), TransactionHash: common.BigToHash(common.Big1), StableActionPath: "intent/000/" + kind, PayloadJSON: payload}
}

func retractionItem(t *testing.T, offset int64, observationID, original string) feed.OutboxItem {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"schema_version": 1, "type": "observation_retracted", "original_observation_id": original})
	if err != nil {
		t.Fatal(err)
	}
	return feed.OutboxItem{Offset: offset, ObservationID: observationID, Source: feed.SourceSequencer, SourceSequence: uint64(offset), TransactionHash: common.BigToHash(common.Big2), StableActionPath: "reorg/1/1", PayloadJSON: payload}
}

func assertCounts(t *testing.T, store *Store, inbox, intents int, offset int64) {
	t.Helper()
	var gotInbox, gotIntents int
	var gotOffset int64
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM signal_inbox`).Scan(&gotInbox); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM operation_intents`).Scan(&gotIntents); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT durable_event_offset FROM bot_progress WHERE id=1`).Scan(&gotOffset); err != nil {
		t.Fatal(err)
	}
	if gotInbox != inbox || gotIntents != intents || gotOffset != offset {
		t.Fatalf("inbox/intents/offset=%d/%d/%d want %d/%d/%d", gotInbox, gotIntents, gotOffset, inbox, intents, offset)
	}
}
