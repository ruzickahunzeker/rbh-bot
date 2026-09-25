package sequencer

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestLiveOfficialMainnetFeed(t *testing.T) {
	if os.Getenv("ROBINHOOD_SEQUENCER_LIVE_TEST") == "" {
		t.Skip("set ROBINHOOD_SEQUENCER_LIVE_TEST=1 to run the live check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := New(Config{URL: MainnetURL})
	if err != nil {
		t.Fatal(err)
	}
	received := false
	err = client.Run(ctx, func(_ context.Context, transaction FeedTransaction) error {
		received = true
		if transaction.Transaction == nil || transaction.Sender == [20]byte{} {
			t.Fatal("live feed returned an incomplete transaction")
		}
		cancel()
		return nil
	})
	if !received {
		t.Fatalf("no verified live transaction received: %v", err)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
