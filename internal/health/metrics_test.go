package health

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsIncludesServiceSpecificWriter(t *testing.T) {
	ready := NewReadiness("database")
	ready.Set("database", true)
	server := New("feed-service", "/tmp/unused.sock", slog.New(slog.NewTextHandler(io.Discard, nil)), ready)
	server.SetMetricsWriter(func(_ context.Context, writer io.Writer) error {
		_, err := io.WriteString(writer, "rbh_feed_durable_event_offset 42\n")
		return err
	})
	recorder := httptest.NewRecorder()
	server.metrics(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, `rbh_service_ready{service="feed-service"} 1`) || !strings.Contains(body, "rbh_feed_durable_event_offset 42") {
		t.Fatalf("unexpected metrics body: %s", body)
	}
}
