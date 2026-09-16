package health

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListenRefusesNonSocketPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.sock")
	if err := os.WriteFile(path, []byte("do not remove"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := New("test", path, slog.New(slog.NewTextHandler(io.Discard, nil)), NewReadiness())
	if _, err := server.Listen(); err == nil {
		t.Fatal("expected non-socket path to be rejected")
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "do not remove" {
		t.Fatalf("non-socket path was modified: %q, %v", contents, err)
	}
}

func TestReadinessOverUnixSocketAndShutdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.sock")
	readiness := NewReadiness("database")
	server := New("test", path, slog.New(slog.NewTextHandler(io.Discard, nil)), readiness)
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}, Timeout: time.Second}

	response, err := client.Get("http://unix/ready")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("got status %d, want 503", response.StatusCode)
	}
	_ = response.Body.Close()

	readiness.Set("database", true)
	response, err = client.Get("http://unix/ready")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", response.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ready" {
		t.Fatalf("unexpected response: %#v", body)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
