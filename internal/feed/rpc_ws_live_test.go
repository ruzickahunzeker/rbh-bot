package feed

import (
	"context"
	"os"
	"testing"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
)

// Explicit read-only integration: temporary feed DB, no bot or trade submission.
func TestRPCWSLiveReadOnly(t *testing.T) {
	endpoint, ws := os.Getenv("FEED_LIVE_RPC_URL"), os.Getenv("FEED_LIVE_WS_URL")
	if endpoint == "" || ws == "" {
		t.Skip("explicit feed live endpoints required")
	}
	store, db := openFeedStore(t)
	defer db.Close()
	base, err := NewEVMReceiptRunner(4663, parser.DefaultAddressBook().WETH, endpoint, store, nil, 5*time.Second)
	if err != nil {
		t.Fatal("invalid base configuration")
	}
	runner, err := NewRPCWSRunner(base, parser.New(), RPCWSOptions{WSURL: ws, StartMode: "latest", MaxCatchup: 200, MaxLag: 8, RequestInterval: 50 * time.Millisecond})
	if err != nil {
		t.Fatal("invalid WS configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	var first, last uint64
	var ready bool
	for ctx.Err() == nil {
		p, found, e := store.RPCCheckpoint(ctx, 4663)
		if e != nil {
			t.Fatal(e)
		}
		if found {
			if first == 0 {
				first = p.Number
			}
			last = p.Number
		}
		if runner.Ready(ctx) {
			ready = true
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := <-done; err != nil {
		t.Fatal("runner exited unexpectedly")
	}
	t.Logf("live WS ready_seen=%t first_block=%d last_block=%d processed_delta=%d", ready, first, last, last-first)
	if !ready || last <= first+10 {
		t.Fatal("live feed did not remain caught up or advance")
	}
	p, err := store.Progress(context.Background())
	if err != nil || p.Degraded {
		t.Fatalf("live degraded: %v %s", err, p.DegradedReason)
	}
}
