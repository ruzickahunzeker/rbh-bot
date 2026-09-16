package ipc

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthenticationAndReplayProtection(t *testing.T) {
	auth, err := NewAuthenticator(bytes.Repeat([]byte{1}, 32), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"ok":true}`)
	req := httptest.NewRequest(http.MethodPost, "http://unix/internal/test", bytes.NewReader(body))
	if err := auth.Sign(req, body); err != nil {
		t.Fatal(err)
	}
	if err := auth.Verify(req); err != nil {
		t.Fatalf("first verification failed: %v", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	if err := auth.Verify(req); err == nil {
		t.Fatal("expected replay to be rejected")
	}
}

func TestAuthenticationRejectsBodyMutation(t *testing.T) {
	auth, err := NewAuthenticator(bytes.Repeat([]byte{2}, 32), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("original")
	req := httptest.NewRequest(http.MethodPost, "http://unix/internal/test", bytes.NewReader([]byte("changed")))
	if err := auth.Sign(req, body); err != nil {
		t.Fatal(err)
	}
	if err := auth.Verify(req); err == nil {
		t.Fatal("expected changed body to be rejected")
	}
}
