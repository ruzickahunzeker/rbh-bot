package feed

import (
	"context"
	"testing"

	"github.com/0xfnzero/rbh-parser-sdk/feed/sequencer"
	"github.com/ethereum/go-ethereum/common"
)

func TestMetricsReflectDurableObservationsAndDegrade(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	ctx := context.Background()
	if _, err := store.CommitSequencer(ctx, []Observation{testObservation(12, 1, "intent/000/buy")}, 12); err != nil {
		t.Fatal(err)
	}
	if err := store.PersistReceiptAudit(ReceiptAudit{TransactionHash: common.HexToHash("0x1234"), BlockNumber: 7, ReceiptStatus: 1, Reason: "receipt_successful"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetractSequencerSequence(ctx, sequencer.Reorg{SequenceNumber: 12, PreviousHash: common.HexToHash("0x1"), ReplacementHash: common.HexToHash("0x2")}); err != nil {
		t.Fatal(err)
	}
	metrics, err := store.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Observations != 2 || metrics.Orphaned != 1 || metrics.OutboxRows != 2 || metrics.ReceiptAudits != 1 || metrics.DurableEventOffset != 2 || !metrics.Degraded {
		t.Fatalf("unexpected metrics: %#v", metrics)
	}
}
