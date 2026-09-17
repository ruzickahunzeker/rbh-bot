package feed

import (
	"encoding/json"
	"net/http"
	"strconv"
)

type OutboxHTTPHandler struct {
	store *Store
}

func NewOutboxHTTPHandler(store *Store) http.Handler {
	return &OutboxHTTPHandler{store: store}
}

func (h *OutboxHTTPHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if h == nil || h.store == nil {
		http.Error(w, "feed outbox unavailable", http.StatusServiceUnavailable)
		return
	}
	after, err := strconv.ParseInt(req.URL.Query().Get("after"), 10, 64)
	if err != nil || after < 0 {
		http.Error(w, "invalid after offset", http.StatusBadRequest)
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	items, err := h.store.ReadOutboxAfter(req.Context(), after, limit)
	if err != nil {
		http.Error(w, "read feed outbox", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Items []OutboxItem `json:"items"`
	}{Items: items})
}
