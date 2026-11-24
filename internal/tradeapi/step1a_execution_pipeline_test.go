package tradeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestStep1AFormalPipelineSinglePostAndReopenSQLite(t *testing.T) {
	f := newLocalPostFixture(t, "buy")
	pipeline, err := newManualExecutionPipeline(ManualExecutionPipelineConfig{
		Store:      f.store,
		Scope:      f.h.scope,
		Binding:    f.h.binding,
		Nonce:      f.h.nonce,
		Signing:    f.h.signing,
		Verifier:   f.h.verifier,
		Executor:   f.h.executor,
		ExecBudget: 2 * time.Minute,
		Now:        func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.api.attachExecutionPipeline(pipeline); err != nil {
		t.Fatal(err)
	}

	rawBody, err := json.Marshal(f.request)
	if err != nil {
		t.Fatal(err)
	}
	// 1. Single POST /v1/trades directly to API (no test harness wrapper).
	req := httptest.NewRequest(http.MethodPost, "/v1/trades", bytes.NewReader(rawBody))
	req.Header.Set("Authorization", "Bearer "+testCredential)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "step1a-first-order-001")
	w := httptest.NewRecorder()
	f.h.api.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("first POST got %d: %s", w.Code, w.Body.String())
	}
	var first Record
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.OperationID == nil || first.Execution == nil || first.Execution.SubmissionState != "submitted" {
		t.Fatalf("expected durable submitted execution snapshot, got %+v", first.Execution)
	}
	if f.signer.calls.Load() != 1 || f.transport.calls.Load() != 1 {
		t.Fatalf("expected exactly 1 signer & 1 transport call, got signer=%d transport=%d", f.signer.calls.Load(), f.transport.calls.Load())
	}

	// 2. Duplicate POST with same Idempotency-Key must return cached history with zero extra sign/send.
	dupReq := httptest.NewRequest(http.MethodPost, "/v1/trades", bytes.NewReader(rawBody))
	dupReq.Header.Set("Authorization", "Bearer "+testCredential)
	dupReq.Header.Set("Content-Type", "application/json")
	dupReq.Header.Set("Idempotency-Key", "step1a-first-order-001")
	dw := httptest.NewRecorder()
	f.h.api.ServeHTTP(dw, dupReq)
	if dw.Code != http.StatusAccepted {
		t.Fatalf("duplicate POST got %d: %s", dw.Code, dw.Body.String())
	}
	var dup Record
	if err := json.Unmarshal(dw.Body.Bytes(), &dup); err != nil {
		t.Fatal(err)
	}
	if !dup.Duplicate || dup.RequestID != first.RequestID {
		t.Fatalf("expected duplicate record for %s, got %+v", first.RequestID, dup)
	}
	if f.signer.calls.Load() != 1 || f.transport.calls.Load() != 1 {
		t.Fatalf("duplicate POST triggered extra calls: signer=%d transport=%d", f.signer.calls.Load(), f.transport.calls.Load())
	}

	// 3. Truly close and reopen SQLite from disk, then verify GET /v1/trades/{request_id}.
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	reopenedDB, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedDB.Close()
	reopenedStore, err := NewStore(reopenedDB)
	if err != nil {
		t.Fatal(err)
	}
	readOnlyAPI, err := NewWithOptions(reopenedStore, f.h.api.identity, testCredential, DefaultPolicy(), nil, Options{ManualIntake: true})
	if err != nil {
		t.Fatal(err)
	}
	getReq := httptest.NewRequest(http.MethodGet, "/v1/trades/"+first.RequestID, nil)
	getReq.Header.Set("Authorization", "Bearer "+testCredential)
	gw := httptest.NewRecorder()
	readOnlyAPI.ServeHTTP(gw, getReq)
	if gw.Code != http.StatusOK {
		t.Fatalf("reopened GET got %d: %s", gw.Code, gw.Body.String())
	}
	var reopened Record
	if err := json.Unmarshal(gw.Body.Bytes(), &reopened); err != nil {
		t.Fatal(err)
	}
	if reopened.Execution == nil || reopened.Execution.SubmissionState != "submitted" {
		t.Fatalf("reopened SQLite missing submitted execution: %+v", reopened.Execution)
	}
}

func TestStep1AClientDisconnectDoesNotAbortAndDuplicateJoinsInFlight(t *testing.T) {
	f := newLocalPostFixture(t, "buy")
	pipeline, err := newManualExecutionPipeline(ManualExecutionPipelineConfig{
		Store:      f.store,
		Scope:      f.h.scope,
		Binding:    f.h.binding,
		Nonce:      f.h.nonce,
		Signing:    f.h.signing,
		Verifier:   f.h.verifier,
		Executor:   f.h.executor,
		ExecBudget: 2 * time.Minute,
		Now:        func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.api.attachExecutionPipeline(pipeline); err != nil {
		t.Fatal(err)
	}

	reqCtx, cancelClient := context.WithCancel(context.Background())
	inTransport := make(chan struct{})
	releaseTransport := make(chan struct{})
	pipeline.hookAfterAdm = func() {
		// Simulate client HTTP socket disconnect immediately after C3-C durable admission commits.
		cancelClient()
	}
	f.transport.during = func(_ context.Context) {
		close(inTransport)
		<-releaseTransport
	}

	rawBody, err := json.Marshal(f.request)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/trades", bytes.NewReader(rawBody)).WithContext(reqCtx)
		req.Header.Set("Authorization", "Bearer "+testCredential)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "step1a-disconnect-key")
		w := httptest.NewRecorder()
		f.h.api.ServeHTTP(w, req)
		firstDone <- w
	}()

	// Wait until detached continuation reaches transport despite client disconnect.
	select {
	case <-inTransport:
	case <-time.After(5 * time.Second):
		t.Fatal("detached continuation did not reach transport after client disconnect")
	}

	// Send duplicate POST with same Idempotency-Key while first continuation is still in-flight.
	dupDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		dupReq := httptest.NewRequest(http.MethodPost, "/v1/trades", bytes.NewReader(rawBody))
		dupReq.Header.Set("Authorization", "Bearer "+testCredential)
		dupReq.Header.Set("Content-Type", "application/json")
		dupReq.Header.Set("Idempotency-Key", "step1a-disconnect-key")
		dw := httptest.NewRecorder()
		f.h.api.ServeHTTP(dw, dupReq)
		dupDone <- dw
	}()

	// Release transport so detached continuation finishes.
	close(releaseTransport)
	w1 := <-firstDone
	w2 := <-dupDone
	if w1.Code != http.StatusAccepted || w2.Code != http.StatusAccepted {
		t.Fatalf("expected 202 on both responses, got first=%d dup=%d (%s)", w1.Code, w2.Code, w2.Body.String())
	}
	var rec2 Record
	if err := json.Unmarshal(w2.Body.Bytes(), &rec2); err != nil {
		t.Fatal(err)
	}
	if !rec2.Duplicate || rec2.Execution == nil || rec2.Execution.SubmissionState != "submitted" {
		t.Fatalf("duplicate retry did not observe completed submitted execution: %+v", rec2)
	}
	if f.signer.calls.Load() != 1 || f.transport.calls.Load() != 1 {
		t.Fatalf("expected exactly 1 signer & 1 transport call across disconnect+retry, got signer=%d transport=%d", f.signer.calls.Load(), f.transport.calls.Load())
	}
}
