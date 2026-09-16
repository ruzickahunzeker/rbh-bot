package ipc

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	HeaderTimestamp = "X-RBH-Timestamp"
	HeaderNonce     = "X-RBH-Nonce"
	HeaderBodyHash  = "X-RBH-Body-SHA256"
	HeaderSignature = "X-RBH-Signature"
	HeaderRequestID = "X-Request-ID"
)

type Authenticator struct {
	secret []byte
	window time.Duration
	now    func() time.Time
	mu     sync.Mutex
	seen   map[string]time.Time
}

func NewAuthenticator(secret []byte, window time.Duration) (*Authenticator, error) {
	if len(secret) < 32 || window <= 0 {
		return nil, errors.New("IPC secret must be at least 32 bytes and window must be positive")
	}
	return &Authenticator{secret: append([]byte(nil), secret...), window: window, now: time.Now, seen: make(map[string]time.Time)}, nil
}

func (a *Authenticator) Sign(req *http.Request, body []byte) error {
	if a == nil || req == nil {
		return errors.New("invalid authentication request")
	}
	nonce, err := randomHex(16)
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(a.now().UTC().Unix(), 10)
	bodyHash := sha256.Sum256(body)
	req.Header.Set(HeaderTimestamp, timestamp)
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderBodyHash, hex.EncodeToString(bodyHash[:]))
	req.Header.Set(HeaderSignature, a.signature(req.Method, req.URL.RequestURI(), timestamp, nonce, hex.EncodeToString(bodyHash[:])))
	return nil
}

func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := a.Verify(req); err != nil {
			WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", err.Error(), RequestID(req))
			return
		}
		next.ServeHTTP(w, req)
	})
}

func (a *Authenticator) Verify(req *http.Request) error {
	if a == nil || req == nil {
		return errors.New("invalid authentication request")
	}
	timestamp := req.Header.Get(HeaderTimestamp)
	nonce := req.Header.Get(HeaderNonce)
	bodyHash := strings.ToLower(req.Header.Get(HeaderBodyHash))
	signature := strings.ToLower(req.Header.Get(HeaderSignature))
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || nonce == "" || bodyHash == "" || signature == "" {
		return errors.New("missing or invalid authentication headers")
	}
	now := a.now().UTC()
	requestTime := time.Unix(unix, 0).UTC()
	if requestTime.Before(now.Add(-a.window)) || requestTime.After(now.Add(a.window)) {
		return errors.New("request timestamp outside allowed window")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, (1<<20)+1))
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(body) > 1<<20 {
		return errors.New("request body exceeds limit")
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	actualHash := sha256.Sum256(body)
	if !hmac.Equal([]byte(bodyHash), []byte(hex.EncodeToString(actualHash[:]))) {
		return errors.New("request body hash mismatch")
	}
	want := a.signature(req.Method, req.URL.RequestURI(), timestamp, nonce, bodyHash)
	if !hmac.Equal([]byte(signature), []byte(want)) {
		return errors.New("request signature mismatch")
	}
	return a.claimNonce(nonce, now)
}

func (a *Authenticator) claimNonce(nonce string, now time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, expiry := range a.seen {
		if !expiry.After(now) {
			delete(a.seen, key)
		}
	}
	if _, exists := a.seen[nonce]; exists {
		return errors.New("request nonce replayed")
	}
	a.seen[nonce] = now.Add(a.window)
	return nil
}

func (a *Authenticator) signature(method, uri, timestamp, nonce, bodyHash string) string {
	mac := hmac.New(sha256.New, a.secret)
	_, _ = io.WriteString(mac, strings.Join([]string{method, uri, timestamp, nonce, bodyHash}, "\n"))
	return hex.EncodeToString(mac.Sum(nil))
}

func randomHex(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
