package feed

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestPersistentWatchlistSurvivesRestartAndFiltersNoise(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "feed-bsc.db")

	db1, err := storage.Open(ctx, storage.FeedOwner, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db1.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	ws1, err := NewWatchlistStore(db1)
	if err != nil {
		t.Fatal(err)
	}
	if ws1.Initialized() {
		t.Fatal("fresh feed DB must not be initialized before initial watchlist sync")
	}

	handler := NewWatchlistHTTPHandler(ws1)
	body := `{"version":1,"wallets":["0x1000000000000000000000000000000000000001"],"tokens":["0x2000000000000000000000000000000000000002"]}`
	req := httptest.NewRequest(http.MethodPut, "/internal/v1/watchlist", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /internal/v1/watchlist failed: %d %s", w.Code, w.Body.String())
	}
	if err := db1.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen SQLite simulating independent feed-bsc restart without waiting for bot re-push (AT-26).
	db2, err := storage.Open(ctx, storage.FeedOwner, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	ws2, err := NewWatchlistStore(db2)
	if err != nil {
		t.Fatal(err)
	}
	if !ws2.Initialized() {
		t.Fatal("restarted feed must restore persisted watchlist immediately")
	}
	if !ws2.Matches("0x1000000000000000000000000000000000000001", "0x9999000000000000000000000000000000000009") {
		t.Fatal("expected watched wallet to match after restart")
	}
	if ws2.Matches("0x8888000000000000000000000000000000000008", "0x9999000000000000000000000000000000000009") {
		t.Fatal("expected unrelated swap to be filtered out")
	}

	// Verify net-intent trigger_id ignores source, block_hash, and sub-pool protocol.
	t1 := ComputeTriggerID(56, "0xABC", "0x1000", "0x2000", "buy")
	t2 := ComputeTriggerID(56, "0xabc", "0x1000", "0x2000", "BUY")
	if t1 != t2 || t1 != "56:0xabc:0x1000:0x2000:buy" {
		t.Fatalf("unexpected trigger_id: %q vs %q", t1, t2)
	}
}
