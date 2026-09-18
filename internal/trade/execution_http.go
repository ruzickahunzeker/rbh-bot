package trade

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type PrepareExecutionRequest struct {
	OperationID string `json:"operation_id"`
	WalletID    string `json:"wallet_id"`
}

type PrepareExecutionHandler struct{ kernel *ExecutionKernel }

func NewPrepareExecutionHandler(kernel *ExecutionKernel) http.Handler {
	return &PrepareExecutionHandler{kernel: kernel}
}

func (h *PrepareExecutionHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if h == nil || h.kernel == nil {
		http.Error(w, "execution kernel unavailable", http.StatusServiceUnavailable)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(req.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	var request PrepareExecutionRequest
	if err := decoder.Decode(&request); err != nil || request.OperationID == "" || request.WalletID == "" {
		http.Error(w, "invalid execution request", http.StatusBadRequest)
		return
	}
	artifact, err := h.kernel.Prepare(req.Context(), request.OperationID, request.WalletID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrInvalidRequest) {
			status = http.StatusBadRequest
		} else if IsExecutionFailClosed(err) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(artifact)
}
