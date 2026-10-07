package tradeapi

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
)

// API owns one immutable caller/wallet scope. This initial slice is local UDS
// only, not a public multi-tenant server. It cannot be configured to send.
type API struct {
	store      *Store
	identity   Identity
	credential [32]byte
	policy     Policy
	now        func() time.Time
	mux        *http.ServeMux
}

func New(store *Store, identity Identity, token string, policy Policy) (*API, error) {
	if store == nil || store.db == nil || !identity.valid() || len(token) < 32 || len(token) > 256 || policy.validate() != nil {
		return nil, ErrInvalid
	}
	for _, ch := range token {
		if ch < 33 || ch > 126 {
			return nil, ErrInvalid
		}
	}
	h := &API{store: store, identity: identity, credential: sha256.Sum256([]byte(token)), policy: policy, now: time.Now, mux: http.NewServeMux()}
	h.mux.HandleFunc("POST /v1/trades", h.submit)
	h.mux.HandleFunc("GET /v1/trades/by-idempotency-key", h.byKey)
	h.mux.HandleFunc("GET /v1/trades/{request_id}", h.status)
	h.mux.HandleFunc("GET /v1/trades/{request_id}/events", h.events)
	h.mux.HandleFunc("GET /v1/capabilities", h.capabilities)
	h.mux.HandleFunc("GET /v1/wallets", h.wallets)
	return h, nil
}

func (h *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	authorizations := r.Header.Values("Authorization")
	if len(authorizations) != 1 || !strings.HasPrefix(authorizations[0], "Bearer ") {
		ipc.WriteError(w, 401, "UNAUTHORIZED", "valid API credential required", ipc.RequestID(r))
		return
	}
	credential := sha256.Sum256([]byte(strings.TrimPrefix(authorizations[0], "Bearer ")))
	if subtle.ConstantTimeCompare(credential[:], h.credential[:]) != 1 {
		ipc.WriteError(w, 401, "UNAUTHORIZED", "valid API credential required", ipc.RequestID(r))
		return
	}
	h.mux.ServeHTTP(w, r)
}

func decodeRequest(body []byte, request *Request) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := strictObject(d, false); err != nil {
		return err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(request); err != nil {
		return ErrInvalid
	}
	return nil
}

// Duplicate keys, case aliases, unknown fields, null/empty optional fields and
// nested control injection are rejected, not silently normalized away.
func strictObject(d *json.Decoder, fee bool) error {
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return ErrInvalid
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return ErrInvalid
		}
		seen[key] = true
		if !fee && key == "fee" {
			if err = strictObject(d, true); err != nil {
				return err
			}
			continue
		}
		allowed := false
		if fee {
			allowed = key == "max_gwei" || key == "tip_gwei" || key == "max_total_native"
		} else {
			switch key {
			case "chain", "wallet_id", "token", "side", "amount", "sell_percent", "slippage_percent", "ttl_seconds", "min_receive":
				allowed = true
			}
		}
		if !allowed {
			return ErrInvalid
		}
		value, err := d.Token()
		if err != nil || value == nil {
			return ErrInvalid
		}
		if key == "ttl_seconds" {
			n, ok := value.(json.Number)
			if !ok {
				return ErrInvalid
			}
			ttl, err := strconv.ParseUint(string(n), 10, 64)
			if err != nil || ttl == 0 {
				return ErrInvalid
			}
		} else {
			s, ok := value.(string)
			if !ok || s == "" {
				return ErrInvalid
			}
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return ErrInvalid
	}
	return nil
}

func key(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || !validID(values[0]) {
		return "", ErrInvalid
	}
	return values[0], nil
}

func (h *API) submit(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, r, ErrInvalid)
		return
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(params) != 0 && (len(params) != 1 || strings.ToLower(params["charset"]) != "utf-8") {
		ipc.WriteError(w, 415, "UNSUPPORTED_MEDIA_TYPE", "application/json required", ipc.RequestID(r))
		return
	}
	idempotencyKey, err := key(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16385))
	if err != nil || len(body) > 16384 {
		ipc.WriteError(w, 413, "REQUEST_TOO_LARGE", "request body limit exceeded", ipc.RequestID(r))
		return
	}
	var request Request
	if err = decodeRequest(body, &request); err != nil {
		writeError(w, r, err)
		return
	}
	request, err = normalize(request)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if request.WalletID != h.identity.WalletID {
		ipc.WriteError(w, 403, "WALLET_SCOPE_DENIED", "wallet outside caller scope", ipc.RequestID(r))
		return
	}
	record, err := h.store.Reject(r.Context(), h.identity, idempotencyKey, request, h.policy, h.now)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/trades/"+record.RequestID)
	writeJSON(w, 503, record)
}

func (h *API) status(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, r, ErrInvalid)
		return
	}
	record, err := h.store.Get(r.Context(), h.identity, r.PathValue("request_id"), false)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, record)
}

func (h *API) byKey(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, r, ErrInvalid)
		return
	}
	k, err := key(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	record, err := h.store.Get(r.Context(), h.identity, k, true)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, record)
}

func (h *API) events(w http.ResponseWriter, r *http.Request) {
	after := uint64(0)
	if raw := r.URL.RawQuery; raw != "" {
		if !strings.HasPrefix(raw, "after=") {
			writeError(w, r, ErrInvalid)
			return
		}
		value := strings.TrimPrefix(raw, "after=")
		var err error
		after, err = strconv.ParseUint(value, 10, 63)
		if err != nil || strconv.FormatUint(after, 10) != value {
			writeError(w, r, ErrInvalid)
			return
		}
	}
	result, err := h.store.Events(r.Context(), h.identity, r.PathValue("request_id"), after)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, result)
}

func (h *API) capabilities(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, r, ErrInvalid)
		return
	}
	writeJSON(w, 200, map[string]any{
		"chains": []string{"robinhood"}, "chain_id": 4663,
		"execution_protocol_allowlist": []string{"pons_v2_curve"},
		"contract_stage":               "HTTP_A_READY_FOR_REVIEW", "request_handling": "REJECTION_ONLY",
		"submission_enabled": false, "send_authorized": false, "live": false, "release_ready": false,
		"protocol_resolution": "NOT_CONNECTED", "token_decimals_validation": "NOT_CONNECTED",
		"fee_execution_binding": "NOT_CONNECTED", "copy_source_validation": "NOT_CONNECTED",
		"api_policy": h.policy, "fee_model": "EIP1559", "fee_unit": "gwei",
		"deadline_capability": "APPLICATION_TTL_ONLY", "contract_deadline": false,
		"events_scope": "API_REJECTION_ONLY", "transport": "LOCAL_UDS",
		"optional_user_limits_default": "UNSET",
		"balance_token_fee_queries":    "NOT_IMPLEMENTED",
	})
}

func (h *API) wallets(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, r, ErrInvalid)
		return
	}
	wallet, err := h.store.Wallet(r.Context(), h.identity)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"wallets": []Wallet{wallet}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := 503, "API_UNAVAILABLE", "trade API unavailable"
	switch {
	case errors.Is(err, ErrInvalid):
		status, code, message = 400, "INVALID_REQUEST", "invalid trade parameters"
	case errors.Is(err, ErrPolicy):
		status, code, message = 422, "API_POLICY_REJECTED", "unsupported chain or request exceeds API limits"
	case errors.Is(err, ErrConflict):
		status, code, message = 409, "IDEMPOTENCY_CONFLICT", "request differs from original request"
	case errors.Is(err, ErrNotFound):
		status, code, message = 404, "REQUEST_NOT_FOUND", "trade request not found"
	case errors.Is(err, ErrWallet):
		status, code, message = 403, "WALLET_UNAVAILABLE", "wallet unavailable in caller scope"
	}
	ipc.WriteError(w, status, code, message, ipc.RequestID(r))
}
