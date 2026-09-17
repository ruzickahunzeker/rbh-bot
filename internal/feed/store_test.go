package feed

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func openFeedStore(t *testing.T) (*Store, *storage.Database) {
	t.Helper()
	ctx := context.Background()
	database, err := storage.Open(ctx, storage.FeedOwner, filepath.Join(t.TempDir(), "feed.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		database.Close()
		t.Fatal(err)
	}
	store, err := NewStore(database)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	return store, database
}

func testObservation(sequence uint64, txByte byte, path string) Observation {
	return Observation{
		ChainID:          ChainID,
		Source:           SourceSequencer,
		SourceSequence:   sequence,
		TransactionHash:  common.BytesToHash([]byte{txByte}),
		StableActionPath: path,
		PayloadJSON:      json.RawMessage(`{"type":"test"}`),
		ObservedAt:       time.Unix(1_700_000_000, 0).UTC(),
	}
}

func TestCommitSequencerIsAtomicAndReplaySafe(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	ctx := context.Background()

	first := testObservation(10, 1, "intent/000/buy")
	result, err := store.CommitSequencer(ctx, []Observation{first}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.InsertedObservations != 1 || result.DurableEventOffset != 1 {
		t.Fatalf("unexpected first commit: %#v", result)
	}

	replay, err := store.CommitSequencer(ctx, []Observation{first}, 11)
	if err != nil {
		t.Fatal(err)
	}
	if replay.InsertedObservations != 0 || replay.DurableEventOffset != 1 {
		t.Fatalf("unexpected replay commit: %#v", replay)
	}

	progress, err := store.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if progress.ObservedSequence == nil || *progress.ObservedSequence != 11 || progress.DurableEventOffset != 1 {
		t.Fatalf("unexpected progress: %#v", progress)
	}
	resume, err := store.ResumeSequence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resume == nil || *resume != 10 {
		t.Fatalf("unexpected resume sequence: %v", resume)
	}
	outbox, err := store.ReadOutboxAfter(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 1 || outbox[0].Offset != 1 {
		t.Fatalf("unexpected outbox: %#v", outbox)
	}
}

func TestCommitSequencerCommitsWholeBatchBeforeProgress(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	ctx := context.Background()

	batch := []Observation{
		testObservation(20, 2, "intent/000/buy"),
		testObservation(20, 2, "intent/001/sell"),
	}
	result, err := store.CommitSequencer(ctx, batch, 20)
	if err != nil {
		t.Fatal(err)
	}
	if result.InsertedObservations != 2 || result.DurableEventOffset != 2 {
		t.Fatalf("unexpected batch result: %#v", result)
	}
	progress, err := store.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if progress.ObservedSequence == nil || *progress.ObservedSequence != 20 || progress.DurableEventOffset != 2 {
		t.Fatalf("progress advanced incorrectly: %#v", progress)
	}
	outbox, err := store.ReadOutboxAfter(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 2 || outbox[0].Offset != 1 || outbox[1].Offset != 2 {
		t.Fatalf("unexpected outbox batch: %#v", outbox)
	}
}

func TestOutboxFailureRollsBackObservationAndProgress(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	ctx := context.Background()
	db, err := database.SQLDB(storage.FeedOwner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE feed_outbox"); err != nil {
		t.Fatal(err)
	}

	if _, err := store.CommitSequencer(ctx, []Observation{testObservation(30, 3, "intent/000/buy")}, 30); err == nil {
		t.Fatal("expected missing outbox to fail commit")
	}
	var events int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM feed_events").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("observation escaped rolled-back transaction: %d", events)
	}
	progress, err := store.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if progress.ObservedSequence != nil || progress.DurableEventOffset != 0 {
		t.Fatalf("progress escaped rolled-back transaction: %#v", progress)
	}
}

func TestReceiptAuditIsIdempotentButConflictsFailClosed(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	audit := ReceiptAudit{
		TransactionHash: common.HexToHash("0x1234"),
		BlockNumber:     42,
		ReceiptStatus:   0,
		Reason:          "receipt_reverted",
	}
	if err := store.PersistReceiptAudit(audit); err != nil {
		t.Fatal(err)
	}
	if err := store.PersistReceiptAudit(audit); err != nil {
		t.Fatalf("identical replay must be idempotent: %v", err)
	}
	audit.Reason = "receipt_successful"
	if err := store.PersistReceiptAudit(audit); err == nil {
		t.Fatal("expected conflicting receipt audit to fail closed")
	}
}
