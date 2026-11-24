package tradeapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
	"github.com/ruzickahunzeker/rbh-bot/internal/registry"
)

// QuotePreflightRequest is the read-only POST /v1/quotes payload.
type QuotePreflightRequest struct {
	Chain       string `json:"chain"`
	WalletID    string `json:"wallet_id"`
	Protocol    string `json:"protocol"`
	Token       string `json:"token"`
	Side        string `json:"side"`
	AmountRaw   string `json:"amount_raw"`
	SlippageBps uint64 `json:"slippage_bps"`
}

// QuotePreflightResponse is returned by POST /v1/quotes (zero DB writes, zero reservations).
type QuotePreflightResponse struct {
	ChainID          uint64 `json:"chain_id"`
	Protocol         string `json:"protocol"`
	PoolPhase        string `json:"pool_phase"`
	Spender          string `json:"spender"`
	ExpectedOutRaw   string `json:"expected_out_raw"`
	MinOutRaw        string `json:"min_out_raw"`
	EstimatedGas     uint64 `json:"estimated_gas"`
	CurrentAllowance string `json:"current_allowance_raw"`
	NeedsApprove     bool   `json:"needs_approve"`
	QuoteBlockNumber uint64 `json:"quote_block_number"`
}

// PositionSnapshot is returned by GET /v1/wallets/{wallet_id}/positions.
type PositionSnapshot struct {
	ChainID           uint64 `json:"chain_id"`
	WalletID          string `json:"wallet_id"`
	WalletAddress     string `json:"wallet_address"`
	WalletState       string `json:"wallet_state"` // ready | in_flight | frozen
	Token             string `json:"token"`
	NativeBalanceRaw  string `json:"native_balance_raw"`
	TokenBalanceRaw   string `json:"token_balance_raw"`
	Decimals          uint8  `json:"decimals"`
	AllowanceRaw      string `json:"allowance_raw"`
	Spender           string `json:"spender,omitempty"`
	HasUnfinalizedBuy bool   `json:"has_unfinalized_buy"`
	BlockNumber       uint64 `json:"block_number"`
}

// ApprovalRequest is the strict POST /v1/approvals payload.
// Caller CANNOT supply an arbitrary spender address; spender is derived from Registry.
type ApprovalRequest struct {
	Chain      string `json:"chain"`
	WalletID   string `json:"wallet_id"`
	Protocol   string `json:"protocol"`
	Token      string `json:"token"`
	AmountRaw  string `json:"amount_raw"`
	TTLSeconds uint64 `json:"ttl_seconds"`
	Fee        *Fee   `json:"fee,omitempty"`
}

// ApprovalRecord is the durable/idempotent record returned by POST/GET /v1/approvals.
type ApprovalRecord struct {
	OperationKind      string `json:"operation_kind"` // always "approval"
	RequestID          string `json:"request_id"`
	IdempotencyKey     string `json:"idempotency_key"`
	ChainID            uint64 `json:"chain_id"`
	WalletID           string `json:"wallet_id"`
	Protocol           string `json:"protocol"`
	Token              string `json:"token"`
	Spender            string `json:"spender"`
	AmountRaw          string `json:"amount_raw"`
	NativeGasOutlayWei string `json:"native_gas_outlay_wei"`
	NativeInputWei     string `json:"native_input_wei"` // always "0" for ERC-20 approve
	Status             string `json:"status"`
	Duplicate          bool   `json:"duplicate"`
	CreatedAt          string `json:"created_at"`
}

// TradeUpdateEvent is a single state transition row projected from trade_audit_events(event_seq).
type TradeUpdateEvent struct {
	EventSeq      int64           `json:"event_seq"`
	OperationKind string          `json:"operation_kind"` // "trade" | "approval"
	RequestID     string          `json:"request_id"`
	OperationID   string          `json:"operation_id"`
	State         string          `json:"state"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     string          `json:"created_at"`
}

// LocalV2Provider supplies read-only quote & position queries backed by Multicall3/RPC.
type LocalV2Provider interface {
	QuotePreflight(ctx context.Context, req QuotePreflightRequest, spender common.Address) (QuotePreflightResponse, error)
	ReadPosition(ctx context.Context, walletID string, walletAddr common.Address, token common.Address, spender common.Address, requiredBlock uint64) (PositionSnapshot, error)
}

// LocalV2Extension mounts Step 1.5 / 2A / 3A local UDS endpoints:
// /v1/quotes, durable /v1/approvals (+ symmetrical GET), /v1/wallets/{id}/positions,
// and /v1/trade-updates?after_seq=... (with 410 Gone cursor guard).
type LocalV2Extension struct {
	reg      *registry.Registry
	provider LocalV2Provider
	now      func() time.Time

	mu                sync.Mutex
	walletRequiredBlk map[string]uint64
}

func NewLocalV2Extension(reg *registry.Registry, provider LocalV2Provider, now func() time.Time) (*LocalV2Extension, error) {
	if reg == nil || provider == nil {
		return nil, ErrInvalid
	}
	if now == nil {
		now = time.Now
	}
	return &LocalV2Extension{
		reg:               reg,
		provider:          provider,
		now:               now,
		walletRequiredBlk: make(map[string]uint64),
	}, nil
}

// SetWalletCanonicalBlock updates the dynamic MAX(inclusion_block_number) for walletID.
func (e *LocalV2Extension) SetWalletCanonicalBlock(walletID string, blockNumber uint64) {
	e.mu.Lock()
	e.walletRequiredBlk[walletID] = blockNumber
	e.mu.Unlock()
}

func (e *LocalV2Extension) requiredBlock(walletID string) uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.walletRequiredBlk[walletID]
}

// AttachLocalV2 registers Step 1.5 / 2A / 3A local UDS routes on an existing API instance.
func (h *API) AttachLocalV2(ext *LocalV2Extension) error {
	if h == nil || ext == nil {
		return ErrInvalid
	}
	h.mux.HandleFunc("POST /v1/quotes", func(w http.ResponseWriter, r *http.Request) { h.handleQuotePreflight(w, r, ext) })
	h.mux.HandleFunc("POST /v1/approvals", func(w http.ResponseWriter, r *http.Request) { h.handleCreateApproval(w, r, ext) })
	h.mux.HandleFunc("GET /v1/approvals/by-idempotency-key", func(w http.ResponseWriter, r *http.Request) { h.handleApprovalByKey(w, r) })
	h.mux.HandleFunc("GET /v1/approvals/{request_id}", func(w http.ResponseWriter, r *http.Request) { h.handleApprovalStatus(w, r) })
	h.mux.HandleFunc("GET /v1/wallets/{wallet_id}/positions", func(w http.ResponseWriter, r *http.Request) { h.handleWalletPositions(w, r, ext) })
	h.mux.HandleFunc("GET /v1/trade-updates", func(w http.ResponseWriter, r *http.Request) { h.handleTradeUpdates(w, r) })
	return nil
}

func (h *API) handleQuotePreflight(w http.ResponseWriter, r *http.Request, ext *LocalV2Extension) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16385))
	if err != nil || len(body) > 16384 {
		ipc.WriteError(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "request body limit exceeded", ipc.RequestID(r))
		return
	}
	var req QuotePreflightRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, r, ErrInvalid)
		return
	}
	if !ext.reg.Chain().MatchesSlug(req.Chain) || req.WalletID != h.identity.WalletID || !common.IsHexAddress(req.Token) {
		writeError(w, r, ErrInvalid)
		return
	}
	amt, ok := new(big.Int).SetString(req.AmountRaw, 10)
	if !ok || amt.Sign() <= 0 || req.SlippageBps > 10000 {
		writeError(w, r, ErrInvalid)
		return
	}
	sp, err := ext.reg.Spender(req.Protocol)
	if err != nil {
		ipc.WriteError(w, http.StatusBadRequest, "UNSUPPORTED_PROTOCOL", err.Error(), ipc.RequestID(r))
		return
	}
	resp, err := ext.provider.QuotePreflight(r.Context(), req, sp.Address)
	if err != nil {
		if errors.Is(err, evm.ErrRPCStateLagging) {
			writeRetryableError(w, r, http.StatusServiceUnavailable, "RPC_STATE_LAGGING", err.Error())
			return
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *API) handleWalletPositions(w http.ResponseWriter, r *http.Request, ext *LocalV2Extension) {
	walletID := r.PathValue("wallet_id")
	if walletID != h.identity.WalletID {
		ipc.WriteError(w, http.StatusForbidden, "WALLET_SCOPE_DENIED", "wallet outside caller scope", ipc.RequestID(r))
		return
	}
	tokenHex := r.URL.Query().Get("token")
	if !common.IsHexAddress(tokenHex) {
		writeError(w, r, ErrInvalid)
		return
	}
	var spender common.Address
	if proto := r.URL.Query().Get("protocol"); proto != "" {
		sp, err := ext.reg.Spender(proto)
		if err != nil {
			ipc.WriteError(w, http.StatusBadRequest, "UNSUPPORTED_PROTOCOL", err.Error(), ipc.RequestID(r))
			return
		}
		spender = sp.Address
	}
	snap, err := ext.provider.ReadPosition(
		r.Context(),
		walletID,
		common.HexToAddress(h.identity.WalletAddress),
		common.HexToAddress(tokenHex),
		spender,
		ext.requiredBlock(walletID),
	)
	if err != nil {
		if errors.Is(err, evm.ErrRPCStateLagging) {
			writeRetryableError(w, r, http.StatusServiceUnavailable, "RPC_STATE_LAGGING", err.Error())
			return
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (h *API) handleCreateApproval(w http.ResponseWriter, r *http.Request, ext *LocalV2Extension) {
	idemKey, err := key(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16385))
	if err != nil || len(body) > 16384 {
		ipc.WriteError(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "request body limit exceeded", ipc.RequestID(r))
		return
	}
	var req ApprovalRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields() // strictly rejects caller-injected "spender" field!
	if err := dec.Decode(&req); err != nil {
		writeError(w, r, ErrInvalid)
		return
	}
	if !ext.reg.Chain().MatchesSlug(req.Chain) || !common.IsHexAddress(req.Token) || req.TTLSeconds == 0 || req.TTLSeconds > 86400 {
		writeError(w, r, ErrInvalid)
		return
	}
	if req.WalletID != h.identity.WalletID {
		ipc.WriteError(w, http.StatusForbidden, "WALLET_SCOPE_DENIED", "wallet outside caller scope", ipc.RequestID(r))
		return
	}
	amt, ok := new(big.Int).SetString(req.AmountRaw, 10)
	if !ok || amt.Sign() < 0 {
		writeError(w, r, ErrInvalid)
		return
	}
	sp, err := ext.reg.Spender(req.Protocol)
	if err != nil {
		ipc.WriteError(w, http.StatusBadRequest, "UNSUPPORTED_PROTOCOL", err.Error(), ipc.RequestID(r))
		return
	}

	canon, _ := json.Marshal(req)
	fpSum := sha256.Sum256(canon)
	fp := hex.EncodeToString(fpSum[:])

	tx, err := h.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	// Idempotent lookup in SQLite manual_approval_requests.
	var existing ApprovalRecord
	var existingFP string
	err = tx.QueryRowContext(r.Context(),
		`SELECT request_id, idempotency_key, chain_id, wallet_id, protocol, token, spender, amount_raw, native_gas_outlay_wei, native_input_wei, status, created_at, fingerprint
		 FROM manual_approval_requests WHERE idempotency_key = ?`, idemKey,
	).Scan(&existing.RequestID, &existing.IdempotencyKey, &existing.ChainID, &existing.WalletID, &existing.Protocol, &existing.Token,
		&existing.Spender, &existing.AmountRaw, &existing.NativeGasOutlayWei, &existing.NativeInputWei, &existing.Status, &existing.CreatedAt, &existingFP)
	if err == nil {
		if existingFP != fp {
			ipc.WriteError(w, http.StatusConflict, "IDEMPOTENCY_KEY_CONFLICT", "idempotency key reused with different payload", ipc.RequestID(r))
			return
		}
		existing.OperationKind = "approval"
		existing.Duplicate = true
		w.Header().Set("Location", "/v1/approvals/"+existing.RequestID)
		writeJSON(w, http.StatusAccepted, existing)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, err)
		return
	}

	reqID := "appr-" + hex.EncodeToString(fpSum[:8])
	nowStr := ext.now().UTC().Format(time.RFC3339Nano)
	rec := ApprovalRecord{
		OperationKind:      "approval",
		RequestID:          reqID,
		IdempotencyKey:     idemKey,
		ChainID:            ext.reg.Chain().ChainID,
		WalletID:           req.WalletID,
		Protocol:           req.Protocol,
		Token:              strings.ToLower(common.HexToAddress(req.Token).Hex()),
		Spender:            strings.ToLower(sp.Address.Hex()),
		AmountRaw:          amt.String(),
		NativeGasOutlayWei: "100000000000000", // native gas bound only; never adds token AmountRaw
		NativeInputWei:     "0",
		Status:             "ACCEPTED",
		Duplicate:          false,
		CreatedAt:          nowStr,
	}
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO manual_approval_requests(request_id, idempotency_key, chain_id, wallet_id, protocol, token, spender, amount_raw, native_gas_outlay_wei, native_input_wei, fingerprint, status, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rec.RequestID, rec.IdempotencyKey, rec.ChainID, rec.WalletID, rec.Protocol, rec.Token, rec.Spender, rec.AmountRaw,
		rec.NativeGasOutlayWei, rec.NativeInputWei, fp, rec.Status, rec.CreatedAt,
	); err != nil {
		writeError(w, r, err)
		return
	}
	evPayload, _ := json.Marshal(rec)
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO trade_audit_events(operation_kind, request_id, operation_id, state, payload_json, created_at)
		 VALUES('approval', ?, ?, ?, ?, ?)`,
		rec.RequestID, rec.RequestID, rec.Status, string(evPayload), nowStr,
	); err != nil {
		writeError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/approvals/"+reqID)
	writeJSON(w, http.StatusAccepted, rec)
}

func (h *API) handleApprovalStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("request_id")
	rec, err := h.loadApproval(r.Context(), `WHERE request_id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		ipc.WriteError(w, http.StatusNotFound, "NOT_FOUND", "approval request not found", ipc.RequestID(r))
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *API) handleApprovalByKey(w http.ResponseWriter, r *http.Request) {
	k := r.URL.Query().Get("idempotency_key")
	if k == "" {
		writeError(w, r, ErrInvalid)
		return
	}
	rec, err := h.loadApproval(r.Context(), `WHERE idempotency_key = ?`, k)
	if errors.Is(err, sql.ErrNoRows) {
		ipc.WriteError(w, http.StatusNotFound, "NOT_FOUND", "approval request not found", ipc.RequestID(r))
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *API) loadApproval(ctx context.Context, where string, arg any) (ApprovalRecord, error) {
	var rec ApprovalRecord
	err := h.store.db.QueryRowContext(ctx,
		`SELECT request_id, idempotency_key, chain_id, wallet_id, protocol, token, spender, amount_raw, native_gas_outlay_wei, native_input_wei, status, created_at
		 FROM manual_approval_requests `+where, arg,
	).Scan(&rec.RequestID, &rec.IdempotencyKey, &rec.ChainID, &rec.WalletID, &rec.Protocol, &rec.Token,
		&rec.Spender, &rec.AmountRaw, &rec.NativeGasOutlayWei, &rec.NativeInputWei, &rec.Status, &rec.CreatedAt)
	if err != nil {
		return ApprovalRecord{}, err
	}
	rec.OperationKind = "approval"
	return rec, nil
}

// handleTradeUpdates serves GET /v1/trade-updates?after_seq=<N>&limit=<M> from
// append-only trade_audit_events(event_seq), returning 410 Gone if cursor is out of range.
func (h *API) handleTradeUpdates(w http.ResponseWriter, r *http.Request) {
	afterSeq := int64(0)
	if raw := r.URL.Query().Get("after_seq"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			writeError(w, r, ErrInvalid)
			return
		}
		afterSeq = v
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 || v > 500 {
			writeError(w, r, ErrInvalid)
			return
		}
		limit = v
	}

	var minRetained, maxSeq int64
	if err := h.store.db.QueryRowContext(r.Context(), `SELECT min_retained_seq FROM trade_audit_retention_floor WHERE id = 1`).Scan(&minRetained); err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.store.db.QueryRowContext(r.Context(), `SELECT COALESCE(MAX(event_seq), 0) FROM trade_audit_events`).Scan(&maxSeq); err != nil {
		writeError(w, r, err)
		return
	}
	if (afterSeq > 0 && afterSeq < minRetained-1) || (afterSeq == 0 && minRetained > 1) || afterSeq > maxSeq {
		ipc.WriteError(w, http.StatusGone, "CURSOR_OUT_OF_RANGE", "trade-updates cursor expired or out of range; reconcile open jobs before resetting", ipc.RequestID(r))
		return
	}

	rows, err := h.store.db.QueryContext(r.Context(),
		`SELECT event_seq, operation_kind, request_id, operation_id, state, payload_json, created_at
		 FROM trade_audit_events WHERE event_seq > ? ORDER BY event_seq ASC LIMIT ?`, afterSeq, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer rows.Close()
	events := make([]TradeUpdateEvent, 0)
	lastSeq := afterSeq
	for rows.Next() {
		var ev TradeUpdateEvent
		var payloadStr string
		if err := rows.Scan(&ev.EventSeq, &ev.OperationKind, &ev.RequestID, &ev.OperationID, &ev.State, &payloadStr, &ev.CreatedAt); err != nil {
			writeError(w, r, err)
			return
		}
		ev.Payload = json.RawMessage(payloadStr)
		lastSeq = ev.EventSeq
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events":           events,
		"next_after_seq":   lastSeq,
		"min_retained_seq": minRetained,
		"max_seq":          maxSeq,
	})
}

func writeRetryableError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":       code,
			"message":    msg,
			"retryable":  true,
			"request_id": ipc.RequestID(r),
		},
	})
}
