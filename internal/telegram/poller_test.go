package telegram

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestTelegramPollerHandlesAuthorizedAndSilentlyDropsUnauthorized(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.BotOwner, filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	backend := &stubBackend{tokenBal: big.NewInt(100)}
	router, err := NewRouter(db, backend, func() time.Time { return time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	if err := router.AllowChat(ctx, 777, "owner", "operator", 56, "w-bsc-1"); err != nil {
		t.Fatal(err)
	}

	var sent []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "getUpdates") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": []any{
					map[string]any{"update_id": 1, "message": map[string]any{"chat": map[string]any{"id": 999, "username": "stranger"}, "text": "/wallets"}},
					map[string]any{"update_id": 2, "message": map[string]any{"chat": map[string]any{"id": 777, "username": "owner"}, "text": "/wallets"}},
				},
			})
			return
		}
		if strings.Contains(r.URL.Path, "sendMessage") {
			var msg map[string]any
			_ = json.NewDecoder(r.Body).Decode(&msg)
			sent = append(sent, msg)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}
	}))
	defer srv.Close()

	poller, err := NewPoller("dummy-token", router, nil)
	if err != nil {
		t.Fatal(err)
	}
	poller.baseURL = srv.URL

	handled, err := poller.PollOnce(ctx, 0)
	if err != nil || handled != 1 || len(sent) != 1 {
		t.Fatalf("expected exactly 1 authorized reply (stranger dropped), got handled=%d sent=%+v err=%v", handled, sent, err)
	}
	if int64(sent[0]["chat_id"].(float64)) != 777 {
		t.Fatalf("unexpected reply chat_id: %+v", sent[0])
	}
}
