package feed

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestReceiptProcessorPersistsSuccessEventsAndOutbox(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	value := parser.New()
	processor, err := NewReceiptProcessor(value, store)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "rbh-validation-package", "fixtures", "historical", "candidates", "0x45142a829ca4ce83909c0f87408b27f70da8a1853a101921fffc20ff5fc08b10.json")
	receipt := loadReceipt(t, path)
	observedAt := time.Unix(1_700_000_100, 0).UTC()
	result, err := processor.Process(context.Background(), receipt, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.InsertedObservations != 2 || result.DurableEventOffset != 2 {
		t.Fatalf("unexpected receipt commit: %#v", result)
	}
	outbox, err := store.ReadOutboxAfter(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 2 {
		t.Fatalf("got %d outbox items, want 2", len(outbox))
	}
	snapshot, ok, err := store.LoadRegistrySnapshot(context.Background())
	if err != nil || !ok {
		t.Fatalf("load registry snapshot: ok=%v err=%v", ok, err)
	}
	if len(snapshot.Curves) == 0 {
		t.Fatal("confirmed launch did not persist curve registry state")
	}

	replay, err := processor.Process(context.Background(), receipt, observedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if replay.InsertedObservations != 0 || replay.DurableEventOffset != 2 {
		t.Fatalf("receipt replay was not deduplicated: %#v", replay)
	}
	outbox, err = store.ReadOutboxAfter(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 2 {
		t.Fatalf("replay duplicated outbox: %d", len(outbox))
	}
}

func TestPersistedRegistryHydratesRestartedIntentParser(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	root := filepath.Join("..", "..", "rbh-validation-package", "fixtures", "historical", "candidates")
	value := parser.New()
	processor, err := NewReceiptProcessor(value, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), loadReceipt(t, filepath.Join(root, "0x45142a829ca4ce83909c0f87408b27f70da8a1853a101921fffc20ff5fc08b10.json")), time.Unix(1_700_000_150, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	snapshot, ok, err := store.LoadRegistrySnapshot(context.Background())
	if err != nil || !ok {
		t.Fatalf("load registry snapshot: ok=%v err=%v", ok, err)
	}
	restarted := parser.New()
	if err := restarted.Registry().Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	tx, _ := loadCapturedTransaction(t, filepath.Join(root, "0x04fc83e1a45cbbc7a0dc6e18dd996f91225c7d97158412560dc0d2fbec5c9d2d.json"))
	if !NewPonsCurveIntentNormalizer(restarted).Potential(tx) {
		t.Fatal("restart hydration lost confirmed Pons curve registry")
	}
}

func TestReceiptCommitFailureRestoresParserRegistry(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	value := parser.New()
	processor, err := NewReceiptProcessor(value, store)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.SQLDB(storage.FeedOwner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE feed_outbox"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "rbh-validation-package", "fixtures", "historical", "candidates", "0x45142a829ca4ce83909c0f87408b27f70da8a1853a101921fffc20ff5fc08b10.json")
	if _, err := processor.Process(context.Background(), loadReceipt(t, path), time.Unix(1_700_000_175, 0).UTC()); err == nil {
		t.Fatal("expected receipt commit failure")
	}
	after := value.Registry().Snapshot()
	if len(after.Curves) != 0 || len(after.Pools) != 0 || len(after.PendingPools) != 0 || len(after.Tokens) != 0 || len(after.Venues) != 0 {
		t.Fatalf("failed durable commit polluted parser registry: %#v", after)
	}
	if _, ok, err := store.LoadRegistrySnapshot(context.Background()); err != nil || ok {
		t.Fatalf("rolled-back registry snapshot became durable: ok=%v err=%v", ok, err)
	}
}

func TestReceiptProcessorPersistsHistoricalRevertAsAuditOnly(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	processor, err := NewReceiptProcessor(parser.New(), store)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "rbh-validation-package", "fixtures", "historical", "candidates", "0xa056fd548d5251ba0ccbbf005932d1ee665eca0dc5fdcb6d2e79451b32954e4e.json")
	result, err := processor.Process(context.Background(), loadReceipt(t, path), time.Unix(1_700_000_200, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if result.InsertedObservations != 0 || result.DurableEventOffset != 0 {
		t.Fatalf("reverted receipt produced economic persistence: %#v", result)
	}
	db, err := database.SQLDB(storage.FeedOwner)
	if err != nil {
		t.Fatal(err)
	}
	var audits, events, outbox int
	if err := db.QueryRow("SELECT COUNT(*) FROM feed_receipt_audits").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM feed_events").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM feed_outbox").Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if audits != 1 || events != 0 || outbox != 0 {
		t.Fatalf("audit/events/outbox=%d/%d/%d", audits, events, outbox)
	}
}
