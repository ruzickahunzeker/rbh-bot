package feed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOutboxHTTPIncludesDurableIdentity(t *testing.T) {
	store, database := openFeedStore(t)
	defer database.Close()
	observation := testObservation(7, 9, "intent/000/buy")
	if _, err := store.CommitSequencer(context.Background(), []Observation{observation}, 7); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/internal/feed/outbox?after=0&limit=10", nil)
	recorder := httptest.NewRecorder()
	NewOutboxHTTPHandler(store).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Items []OutboxItem `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].ObservationID != observation.ID() || response.Items[0].TransactionHash != observation.TransactionHash || response.Items[0].StableActionPath != observation.StableActionPath {
		t.Fatalf("identity missing from outbox: %#v", response.Items)
	}
}
