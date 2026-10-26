package tradeapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestLocalPostExecutionAdmissionWinnerOwnsContinuation(t *testing.T) {
	f := newLocalPostFixture(t, "buy")
	d, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	second := f.anotherHandle(t, d, s)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	// Force the first intake's validation to pause outside a writer transaction.
	// The duplicate intake can then win the FIRST admission commit. Neither
	// duplicate intake nor admission history alone is continuation permission.
	f.backend.during = func() {
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("POST", "/v1/trades", strings.NewReader(encode(f.request))).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer "+testCredential)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "different-winners")
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		firstDone <- w
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first intake did not reach validation barrier")
	}
	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { secondDone <- call(second, "POST", "/v1/trades", "different-winners", encode(f.request)) }()
	var admitted Record
	select {
	case w := <-secondDone:
		admitted = localPostRecord(t, w, 202)
		if !admitted.Duplicate || admitted.Execution == nil || admitted.Execution.SubmissionState != "submitted" {
			t.Fatal("unique first admission was stranded by the composite duplicate flag", w.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("first-admission winner did not finish while intake winner was paused")
	}
	f.assertOne(t, admitted, 1)
	before := admittedDatabaseSnapshot(t, f.store.db)
	releaseOnce.Do(func() { close(release) })
	select {
	case w := <-firstDone:
		loser := localPostRecord(t, w, 202)
		if !loser.Duplicate || loser.Execution.SubmissionState != "submitted" || loser.ExpiresAt != admitted.ExpiresAt || loser.RequestID != admitted.RequestID || admittedDatabaseSnapshot(t, f.store.db) != before {
			t.Fatal("historical admission loser repeated or changed execution", w.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("original intake did not return historical admission")
	}
	duplicate := localPostRecord(t, call(f.h, "POST", "/v1/trades", "different-winners", encode(f.request)), 202)
	if !duplicate.Duplicate || admittedDatabaseSnapshot(t, f.store.db) != before {
		t.Fatal("later duplicate recreated continuation")
	}
	f.assertOne(t, admitted, 1)
}
