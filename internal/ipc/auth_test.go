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

func TestVerifyHandlesNilBody(t *testing.T) {
	auth, err := NewAuthenticator(bytes.Repeat([]byte{3}, 32), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://unix/internal/ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Body = nil
	if err := auth.Sign(req, nil); err != nil {
		t.Fatal(err)
	}
	if err := auth.Verify(req); err != nil {
		t.Fatalf("expected nil-body GET to verify cleanly: %v", err)
	}
}

func TestReplayCacheBoundedAndUnauthenticatedDoSResistant(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	auth, err := NewAuthenticatorWithLimit(bytes.Repeat([]byte{4}, 32), 30*time.Second, 2)
	if err != nil {
		t.Fatal(err)
	}
	auth.now = func() time.Time { return now }

	// Flood with unauthenticated/bad-signature requests: replay cache must stay empty.
	for i := 0; i < 10; i++ {
		badReq := httptest.NewRequest(http.MethodPost, "http://unix/internal/test", bytes.NewReader([]byte(`{}`)))
		badReq.Header.Set(HeaderTimestamp, "1795435200")
		badReq.Header.Set(HeaderNonce, "bad-nonce-"+string(rune('a'+i)))
		badReq.Header.Set(HeaderBodyHash, "0000")
		badReq.Header.Set(HeaderSignature, "deadbeef")
		if err := auth.Verify(badReq); err == nil {
			t.Fatal("expected bad signature to be rejected")
		}
	}
	if size, capVal := auth.ReplayCacheStats(); size != 0 || capVal != 2 {
		t.Fatalf("unauthenticated flood polluted replay cache: size=%d cap=%d", size, capVal)
	}

	// Fill bounded capacity with 2 valid signed requests.
	var firstNonceReq *http.Request
	for i := 0; i < 2; i++ {
		body := []byte(`{"n":1}`)
		req := httptest.NewRequest(http.MethodPost, "http://unix/internal/test", bytes.NewReader(body))
		if err := auth.Sign(req, body); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			clone := req.Clone(req.Context())
			clone.Body = io.NopCloser(bytes.NewReader(body))
			firstNonceReq = clone
		}
		if err := auth.Verify(req); err != nil {
			t.Fatalf("valid request %d failed: %v", i, err)
		}
	}

	// Third distinct valid request within window must fail-closed without evicting earlier nonces.
	thirdBody := []byte(`{"n":3}`)
	thirdReq := httptest.NewRequest(http.MethodPost, "http://unix/internal/test", bytes.NewReader(thirdBody))
	if err := auth.Sign(thirdReq, thirdBody); err != nil {
		t.Fatal(err)
	}
	if err := auth.Verify(thirdReq); err == nil {
		t.Fatal("expected bounded replay cache overflow to fail closed")
	}

	// First nonce must still be rejected as a replay (never evicted while unexpired).
	if err := auth.Verify(firstNonceReq); err == nil {
		t.Fatal("expected unexpired first nonce to remain protected against replay")
	}

	// After window expires, old entries are pruned and new requests succeed again.
	now = now.Add(31 * time.Second)
	freshBody := []byte(`{"n":4}`)
	freshReq := httptest.NewRequest(http.MethodPost, "http://unix/internal/test", bytes.NewReader(freshBody))
	if err := auth.Sign(freshReq, freshBody); err != nil {
		t.Fatal(err)
	}
	if err := auth.Verify(freshReq); err != nil {
		t.Fatalf("expected request after window expiry to succeed: %v", err)
	}
}
