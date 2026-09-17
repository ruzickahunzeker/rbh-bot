package feed

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/0xfnzero/rbh-parser-sdk/feed/sequencer"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestRetractSequencerSequenceKeepsHistoryAndEmitsOneCompensation(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	ctx := context.Background()
	original := testObservation(100, 9, "intent/000/buy")
	if result, err := store.CommitSequencer(ctx, []Observation{original}, 100); err != nil || result.DurableEventOffset != 1 {
		t.Fatalf("seed observation: result=%#v err=%v", result, err)
	}
	reorg := sequencer.Reorg{
		SequenceNumber:  100,
		PreviousHash:    common.HexToHash("0x1111"),
		ReplacementHash: common.HexToHash("0x2222"),
	}
	inserted, err := store.RetractSequencerSequence(ctx, reorg)
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 1 {
		t.Fatalf("inserted compensations=%d, want 1", inserted)
	}

	db, err := database.SQLDB(storage.FeedOwner)
	if err != nil {
		t.Fatal(err)
	}
	var inclusion string
	if err := db.QueryRowContext(ctx, `SELECT inclusion FROM feed_events WHERE observation_id = ?`, original.ID()).Scan(&inclusion); err != nil {
		t.Fatal(err)
	}
	if inclusion != "orphaned" {
		t.Fatalf("original inclusion=%q, want orphaned", inclusion)
	}
	outbox, err := store.ReadOutboxAfter(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 2 {
		t.Fatalf("outbox rows=%d, want original plus compensation", len(outbox))
	}
	var payload retractionPayload
	if err := json.Unmarshal(outbox[1].PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Type != "observation_retracted" || payload.Reason != "sequencer_reorg" || payload.Sequence != 100 || payload.OriginalOffset != 1 || payload.OriginalObservationID != original.ID() {
		t.Fatalf("unexpected retraction payload: %#v", payload)
	}

	progress, err := store.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !progress.Degraded || progress.DurableEventOffset != 2 || progress.ObservedSequence == nil || *progress.ObservedSequence != 100 {
		t.Fatalf("unexpected reorg progress: %#v", progress)
	}
	resume, err := store.ResumeSequence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resume == nil || *resume != 99 {
		t.Fatalf("resume=%v, want 99", resume)
	}

	inserted, err = store.RetractSequencerSequence(ctx, reorg)
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 0 {
		t.Fatalf("replayed reorg inserted %d compensations", inserted)
	}
	outbox, err = store.ReadOutboxAfter(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 2 {
		t.Fatalf("replayed reorg duplicated outbox: %d", len(outbox))
	}
}

func TestSequencerReorgCallbackUsesDurableCompensation(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	ctx := context.Background()
	original := testObservation(44, 7, "intent/000/sell")
	if _, err := store.CommitSequencer(ctx, []Observation{original}, 44); err != nil {
		t.Fatal(err)
	}
	runner, err := NewSequencerRunner(nil, store)
	if err != nil {
		t.Fatal(err)
	}
	config, err := runner.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.OnReorg(ctx, sequencer.Reorg{SequenceNumber: 44, PreviousHash: common.HexToHash("0xaa"), ReplacementHash: common.HexToHash("0xbb")}); err != nil {
		t.Fatal(err)
	}
	outbox, err := store.ReadOutboxAfter(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 2 {
		t.Fatalf("callback did not publish compensation: %d", len(outbox))
	}
	progress, err := store.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !progress.Degraded {
		t.Fatal("reorg callback did not fail readiness closed")
	}
}
