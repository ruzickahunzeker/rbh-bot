package trade

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
)

// DisabledTradeAPI serves one server-configured wallet within the existing
// authenticated internal IPC trust domain. It has no enable flag, runtime
// authorization grant, signer, SubmissionService, worker or broadcaster.
type DisabledTradeAPI struct {
	store  *Store
	wallet string
	now    func() time.Time
	mux    *http.ServeMux
}

func NewDisabledTradeAPI(store *Store, wallet string) (*DisabledTradeAPI, error) {
	if store == nil || store.db == nil || !validAPIID(wallet) {
		return nil, ErrInvalidRequest
	}
	h := &DisabledTradeAPI{store: store, wallet: wallet, now: time.Now, mux: http.NewServeMux()}
	h.mux.HandleFunc("POST /internal/trade/operations/{operation_id}/submission-requests", h.submit)
	h.mux.HandleFunc("GET /internal/trade/operations/{operation_id}", h.status)
	h.mux.HandleFunc("GET /internal/trade/operations/{operation_id}/events", h.events)
	return h, nil
}

// Production startup must wrap this handler with the existing IPC authenticator.
func (h *DisabledTradeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.mux == nil {
		ipc.WriteError(w, http.StatusServiceUnavailable, "API_UNAVAILABLE", "trade API unavailable", ipc.RequestID(r))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.mux.ServeHTTP(w, r)
}

func decodeSubmissionIntake(r io.Reader, v *SubmissionIntakeRequest) error {
	body, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return ErrInvalidRequest
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return ErrInvalidRequest
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest
	}
	// Reject duplicate JSON members rather than accepting last-member-wins.
	d = json.NewDecoder(bytes.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInvalidRequest
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return ErrInvalidRequest
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return ErrInvalidRequest
		}
		// encoding/json otherwise accepts case-insensitive struct-field aliases,
		// allowing wallet_id + WALLET_ID to behave like a duplicate member.
		switch key {
		case "wallet_id", "idempotency_key", "policy_version", "expires_at":
		default:
			return ErrInvalidRequest
		}
		seen[key] = true
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return ErrInvalidRequest
		}
	}
	return nil
}

func (h *DisabledTradeAPI) submit(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeTradeAPIError(w, r, ErrInvalidRequest)
		return
	}
	var request SubmissionIntakeRequest
	if err := decodeSubmissionIntake(r.Body, &request); err != nil {
		writeTradeAPIError(w, r, err)
		return
	}
	if request.WalletID != h.wallet {
		ipc.WriteError(w, http.StatusForbidden, "WALLET_SCOPE_DENIED", "wallet outside configured API scope", ipc.RequestID(r))
		return
	}
	result, err := h.store.RecordDisabledSubmissionIntake(r.Context(), r.PathValue("operation_id"), request, h.now().UTC())
	if err != nil {
		writeTradeAPIError(w, r, err)
		return
	}
	writeTradeAPIJSON(w, http.StatusServiceUnavailable, result)
}

func (h *DisabledTradeAPI) status(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeTradeAPIError(w, r, ErrInvalidRequest)
		return
	}
	result, err := h.store.OperationAPIStatus(r.Context(), r.PathValue("operation_id"), h.wallet)
	if err != nil {
		writeTradeAPIError(w, r, err)
		return
	}
	writeTradeAPIJSON(w, http.StatusOK, result)
}

func (h *DisabledTradeAPI) events(w http.ResponseWriter, r *http.Request) {
	query, err := parseAPIEventQuery(r.URL.RawQuery)
	if err != nil {
		writeTradeAPIError(w, r, err)
		return
	}
	result, err := h.store.OperationAPIEvents(r.Context(), r.PathValue("operation_id"), h.wallet, query)
	if err != nil {
		writeTradeAPIError(w, r, err)
		return
	}
	writeTradeAPIJSON(w, http.StatusOK, result)
}

func parseAPIEventQuery(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	// Canonical decimal only; no duplicate, encoded or unknown query members.
	const prefix = "after="
	if len(raw) <= len(prefix) || raw[:len(prefix)] != prefix {
		return 0, ErrInvalidRequest
	}
	value := raw[len(prefix):]
	after, err := strconv.ParseUint(value, 10, 63)
	if err != nil || strconv.FormatUint(after, 10) != value {
		return 0, ErrInvalidRequest
	}
	return after, nil
}

func writeTradeAPIJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeTradeAPIError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := http.StatusServiceUnavailable, "API_UNAVAILABLE", "trade API store unavailable"
	switch {
	case errors.Is(err, ErrInvalidRequest):
		status, code, message = http.StatusBadRequest, "INVALID_REQUEST", "invalid trade API request"
	case errors.Is(err, ErrTradeAPINotFound):
		status, code, message = http.StatusNotFound, "OPERATION_NOT_FOUND", "trade operation not found"
	case errors.Is(err, ErrIdempotencyConflict):
		status, code, message = http.StatusConflict, "IDEMPOTENCY_CONFLICT", "request differs from immutable durable identity"
	case errors.Is(err, ErrTTLExpired):
		status, code, message = http.StatusConflict, "TTL_EXPIRED", "application TTL expired"
	case errors.Is(err, ErrTTLUnverifiable):
		status, code, message = http.StatusBadRequest, "TTL_UNVERIFIABLE", "application TTL cannot be verified"
	}
	ipc.WriteError(w, status, code, message, ipc.RequestID(r))
}
