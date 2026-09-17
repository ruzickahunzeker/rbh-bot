package trade

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type DryRunHTTPHandler struct {
	engine *Engine
}

func NewDryRunHTTPHandler(engine *Engine) http.Handler {
	return &DryRunHTTPHandler{engine: engine}
}

func (h *DryRunHTTPHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if h == nil || h.engine == nil {
		http.Error(w, "trade dry-run unavailable", http.StatusServiceUnavailable)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(req.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	var request DryRunRequest
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid dry-run request", http.StatusBadRequest)
		return
	}
	result, err := h.engine.DryRun(req.Context(), request)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrInvalidRequest) {
			status = http.StatusBadRequest
		} else if errors.Is(err, ErrIdempotencyConflict) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
