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
