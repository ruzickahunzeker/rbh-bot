package feed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xfnzero/rbh-parser-sdk/feed/sequencer"
	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

// TestC04ProductionSequencerRunnerLive is an explicit, read-only integration
// proof for the production runner and pinned repository-local SDK snapshot.
// It never constructs, signs, or broadcasts a transaction.
func TestC04ProductionSequencerRunnerLive(t *testing.T) {
	if os.Getenv("RBH_C04_SEQUENCER_LIVE_TEST") != "1" {
		t.Skip("set RBH_C04_SEQUENCER_LIVE_TEST=1 to run the public feed proof")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	database, err := storage.Open(ctx, storage.FeedOwner, filepath.Join(t.TempDir(), "feed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(database)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewSequencerRunner(parser.New(), store)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err = <-done:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("runner stopped before live: %v", err)
			}
			t.Fatal("runner stopped before verified live state")
		case <-ticker.C:
			if runner.Status().State == sequencer.ConnectionLive {
				cancel()
				return
			}
		case <-ctx.Done():
			t.Fatalf("runner did not reach verified live state: status=%+v", runner.Status())
		}
	}
}

func TestC04ProductionRunnerFailsClosedOnExpiredRetentionCursor(t *testing.T) {
	if os.Getenv("RBH_C04_SEQUENCER_LIVE_TEST") != "1" {
		t.Skip("set RBH_C04_SEQUENCER_LIVE_TEST=1 to run the public feed proof")
	}
	const observedBoundary = uint64(72_104_311)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	database, err := storage.Open(ctx, storage.FeedOwner, filepath.Join(t.TempDir(), "feed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(database)
	if err != nil {
		t.Fatal(err)
	}
	// ResumeSequence deliberately replays one sequence, so committing S+1 asks
	// the target service for the previously observed boundary S.
	if _, err = store.CommitSequencer(ctx, nil, observedBoundary+1); err != nil {
		t.Fatal(err)
	}
	runner, err := NewSequencerRunner(parser.New(), store)
	if err != nil {
		t.Fatal(err)
	}
	err = runner.Run(ctx)
	if err == nil || !errors.Is(err, sequencer.ErrSequenceGap) {
		t.Fatalf("expected target retention gap, got %v", err)
	}
	progress, progressErr := store.Progress(context.Background())
	if progressErr != nil {
		t.Fatal(progressErr)
	}
	if !progress.Degraded || progress.DegradedReason == "" || runner.Ready(context.Background()) {
		t.Fatalf("retention gap did not remain fail-closed: progress=%+v ready=%v", progress, runner.Ready(context.Background()))
	}
}
