package feed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xfnzero/rbh-parser-sdk/feed/sequencer"
	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

type capturedTransactionInput struct {
	Transaction json.RawMessage `json:"transaction"`
}

type rpcTransactionMeta struct {
	From common.Address `json:"from"`
}

func loadCapturedTransaction(t *testing.T, path string) (*gethtypes.Transaction, common.Address) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var capture capturedTransactionInput
	if err := json.Unmarshal(raw, &capture); err != nil {
		t.Fatal(err)
	}
	var tx gethtypes.Transaction
	if err := json.Unmarshal(capture.Transaction, &tx); err != nil {
		t.Fatal(err)
	}
	var meta rpcTransactionMeta
	if err := json.Unmarshal(capture.Transaction, &meta); err != nil {
		t.Fatal(err)
	}
	return &tx, meta.From
}

func TestSequencerHandlerCommitsOnlyAfterNormalization(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	root := filepath.Join("..", "..", "rbh-validation-package", "fixtures", "historical", "candidates")
	value := parser.New()
	if events, err := value.ParseReceipt(loadReceipt(t, filepath.Join(root, "0x45142a829ca4ce83909c0f87408b27f70da8a1853a101921fffc20ff5fc08b10.json"))); err != nil || len(events) == 0 {
		t.Fatalf("bootstrap confirmed Pons launch: events=%d err=%v", len(events), err)
	}
	runner, err := NewSequencerRunner(value, store)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "0x04fc83e1a45cbbc7a0dc6e18dd996f91225c7d97158412560dc0d2fbec5c9d2d.json")
	tx, sender := loadCapturedTransaction(t, path)
	receivedAt := time.Unix(1_700_000_300, 0).UTC()
	if err := runner.Handle(context.Background(), sequencer.FeedTransaction{
		ReceivedAt:     receivedAt,
		SequenceNumber: 100,
		Sender:         sender,
		Transaction:    tx,
	}); err != nil {
		t.Fatal(err)
	}
	progress, err := store.Progress(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if progress.ObservedSequence == nil || *progress.ObservedSequence != 100 || progress.DurableEventOffset != 1 {
		t.Fatalf("unexpected progress: %#v", progress)
	}
	outbox, err := store.ReadOutboxAfter(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 1 {
		t.Fatalf("got %d outbox items, want 1", len(outbox))
	}

	// Delivery replay is expected after reconnect. Observation identity suppresses
	// duplicate durable events while the handled sequence may still advance.
	if err := runner.Handle(context.Background(), sequencer.FeedTransaction{
		ReceivedAt:     receivedAt.Add(time.Second),
		SequenceNumber: 101,
		Sender:         sender,
		Transaction:    tx,
	}); err != nil {
		t.Fatal(err)
	}
	progress, err = store.Progress(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if progress.ObservedSequence == nil || *progress.ObservedSequence != 101 || progress.DurableEventOffset != 1 {
		t.Fatalf("unexpected replay progress: %#v", progress)
	}
	outbox, err = store.ReadOutboxAfter(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 1 {
		t.Fatalf("replay duplicated outbox: %d", len(outbox))
	}
}

func TestSequencerConfigUsesDurableResumeAndDegradesOnDiscontinuity(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	ctx := context.Background()
	if _, err := store.CommitSequencer(ctx, nil, 50); err != nil {
		t.Fatal(err)
	}
	runner, err := NewSequencerRunner(parser.New(), store)
	if err != nil {
		t.Fatal(err)
	}
	config, err := runner.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if config.OnSequence != nil {
		t.Fatal("SDK OnSequence must not be used as durable checkpoint")
	}
	if config.InitialSequence == nil || *config.InitialSequence != 49 {
		t.Fatalf("initial sequence=%v, want 49", config.InitialSequence)
	}
	if config.AllowGaps || config.AllowReorgs {
		t.Fatal("gap/reorg must fail closed")
	}
	if err := config.OnGap(ctx, sequencer.SequenceGap{Expected: 51, Received: 53}); err != nil {
		t.Fatal(err)
	}
	progress, err := store.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !progress.Degraded || progress.DegradedReason == "" {
		t.Fatalf("gap did not degrade feed: %#v", progress)
	}
}

func TestSequencerStatusTracksConnectionStateWithoutClearingSafetyDegrade(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	runner, err := NewSequencerRunner(parser.New(), store)
	if err != nil {
		t.Fatal(err)
	}
	config, err := runner.Config(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	config.OnStatus(context.Background(), sequencer.ConnectionStatus{State: sequencer.ConnectionDisconnected, Cause: context.DeadlineExceeded})
	status := runner.Status()
	if status.State != sequencer.ConnectionDisconnected || status.Cause == "" {
		t.Fatalf("unexpected status: %#v", status)
	}
}
