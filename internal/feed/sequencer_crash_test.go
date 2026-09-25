package feed

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestC04SequencerCheckpointRealProcessCrashWindows(t *testing.T) {
	for _, stage := range []string{"message_received_before_commit", "after_atomic_observation_commit", "after_checkpoint_commit"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "feed.db")
			cmd := exec.Command(os.Args[0], "-test.run=TestC04SequencerCheckpointCrashWorker")
			cmd.Env = append(os.Environ(), "RBH_C04_CHECKPOINT_WORKER=1", "RBH_C04_CHECKPOINT_STAGE="+stage, "RBH_C04_CHECKPOINT_DB="+path)
			if err := cmd.Run(); err == nil {
				t.Fatal("worker did not crash")
			}
			database, err := storage.Open(context.Background(), storage.FeedOwner, path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			store, err := NewStore(database)
			if err != nil {
				t.Fatal(err)
			}
			observation := testObservation(100, 9, "intent/000/buy")
			if _, err = store.CommitSequencer(context.Background(), []Observation{observation}, 100); err != nil {
				t.Fatal(err)
			}
			progress, err := store.Progress(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			outbox, err := store.ReadOutboxAfter(context.Background(), 0, 10)
			if err != nil {
				t.Fatal(err)
			}
			if progress.ObservedSequence == nil || *progress.ObservedSequence != 100 || progress.DurableEventOffset != 1 || len(outbox) != 1 {
				t.Fatalf("restart state progress=%+v outbox=%d", progress, len(outbox))
			}
		})
	}
}

func TestC04SequencerCheckpointCrashWorker(t *testing.T) {
	if os.Getenv("RBH_C04_CHECKPOINT_WORKER") != "1" {
		return
	}
	ctx := context.Background()
	database, err := storage.Open(ctx, storage.FeedOwner, os.Getenv("RBH_C04_CHECKPOINT_DB"))
	if err != nil || database.Migrate(ctx) != nil {
		os.Exit(81)
	}
	store, err := NewStore(database)
	if err != nil {
		os.Exit(82)
	}
	if os.Getenv("RBH_C04_CHECKPOINT_STAGE") == "message_received_before_commit" {
		os.Exit(91)
	}
	observation := testObservation(100, 9, "intent/000/buy")
	if _, err = store.CommitSequencer(ctx, []Observation{observation}, 100); err != nil {
		os.Exit(83)
	}
	// Observation, outbox and checkpoint share one SQLite transaction, so the
	// two post-commit labels intentionally recover from the same durable state.
	os.Exit(92)
}
