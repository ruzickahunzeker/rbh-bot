package bot

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/feed"
	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
)

func TestHTTPOutboxSourceUsesAuthenticatedUnixSocket(t *testing.T) {
	secret := []byte("01234567890123456789012345678901")
	auth, err := ipc.NewAuthenticator(secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "feed.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	item := feed.OutboxItem{Offset: 1, ObservationID: "obs-1", Source: feed.SourceSequencer, SourceSequence: 1, TransactionHash: common.HexToHash("0x1"), StableActionPath: "intent/000/buy", PayloadJSON: json.RawMessage(`{"type":"transaction_intent"}`)}
	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("after") != "0" || req.URL.Query().Get("limit") != "10" {
			t.Fatalf("unexpected query %s", req.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []feed.OutboxItem{item}})
	}))
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	defer server.Shutdown(context.Background())

	source, err := NewHTTPOutboxSource(socket, auth)
	if err != nil {
		t.Fatal(err)
	}
	items, err := source.ReadOutboxAfter(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ObservationID != item.ObservationID || items[0].TransactionHash != item.TransactionHash {
		t.Fatalf("unexpected items: %#v", items)
	}
}
