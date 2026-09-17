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
	processor, err := NewReceiptProcessor(parser.New(), store)
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
