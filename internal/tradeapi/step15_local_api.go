package tradeapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
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
	Chain      string      `json:"chain"`
	WalletID   string      `json:"wallet_id"`
	Protocol   string      `json:"protocol"`
	Token      string      `json:"token"`
	AmountRaw  string      `json:"amount_raw"`
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
	fingerprint        string
}

// LocalV2Provider supplies read-only quote & position queries backed by Multicall3/RPC.
type LocalV2Provider interface {
	QuotePreflight(ctx context.Context, req QuotePreflightRequest, spender common.Address) (QuotePreflightResponse, error)
	ReadPosition(ctx context.Context, walletID string, walletAddr common.Address, token common.Address, spender common.Address, requiredBlock uint64) (PositionSnapshot, error)
}

// LocalV2Extension mounts Step 1.5 local UDS endpoints (/v1/quotes, /v1/approvals, /v1/wallets/{id}/positions)
// without altering any V1 /v1/trades OpenAPI 0.5.1 contract.
type LocalV2Extension struct {
	reg      *registry.Registry
	provider LocalV2Provider
	now      func() time.Time

	mu                sync.Mutex
	approvalsByID     map[string]ApprovalRecord
	approvalsByKey    map[string]string
	walletRequiredBlk map[string]uint64
	seq               uint64
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
		approvalsByID:     make(map[string]ApprovalRecord),
		approvalsByKey:    make(map[string]string),
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

// AttachLocalV2 registers Step 1.5 local UDS routes on an existing API instance.
func (h *API) AttachLocalV2(ext *LocalV2Extension) error {
	if h == nil || ext == nil {
		return ErrInvalid
	}
	h.mux.HandleFunc("POST /v1/quotes", func(w http.ResponseWriter, r *http.Request) { h.handleQuotePreflight(w, r, ext) })
	h.mux.HandleFunc("POST /v1/approvals", func(w http.ResponseWriter, r *http.Request) { h.handleCreateApproval(w, r, ext) })
	h.mux.HandleFunc("GET /v1/approvals/by-idempotency-key", func(w http.ResponseWriter, r *http.Request) { h.handleApprovalByKey(w, r, ext) })
	h.mux.HandleFunc("GET /v1/approvals/{request_id}", func(w http.ResponseWriter, r *http.Request) { h.handleApprovalStatus(w, r, ext) })
	h.mux.HandleFunc("GET /v1/wallets/{wallet_id}/positions", func(w http.ResponseWriter, r *http.Request) { h.handleWalletPositions(w, r, ext) })
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

	// Deterministic fingerprint across canonical fields.
	canon, _ := json.Marshal(req)
	fpSum := sha256.Sum256(canon)
	fp := hex.EncodeToString(fpSum[:])

	ext.mu.Lock()
	defer ext.mu.Unlock()
	if existingID, ok := ext.approvalsByKey[idemKey]; ok {
		existing := ext.approvalsByID[existingID]
		if existing.fingerprint != fp {
			ipc.WriteError(w, http.StatusConflict, "IDEMPOTENCY_KEY_CONFLICT", "idempotency key reused with different payload", ipc.RequestID(r))
			return
		}
		existing.Duplicate = true
		w.Header().Set("Location", "/v1/approvals/"+existing.RequestID)
		writeJSON(w, http.StatusAccepted, existing)
		return
	}
	ext.seq++
	reqID := "appr-" + hex.EncodeToString(fpSum[:8])
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
		CreatedAt:          ext.now().UTC().Format(time.RFC3339Nano),
		fingerprint:        fp,
	}
	ext.approvalsByID[reqID] = rec
	ext.approvalsByKey[idemKey] = reqID
	w.Header().Set("Location", "/v1/approvals/"+reqID)
	writeJSON(w, http.StatusAccepted, rec)
}

func (h *API) handleApprovalStatus(w http.ResponseWriter, r *http.Request, ext *LocalV2Extension) {
	id := r.PathValue("request_id")
	ext.mu.Lock()
	rec, ok := ext.approvalsByID[id]
	ext.mu.Unlock()
	if !ok {
		ipc.WriteError(w, http.StatusNotFound, "NOT_FOUND", "approval request not found", ipc.RequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *API) handleApprovalByKey(w http.ResponseWriter, r *http.Request, ext *LocalV2Extension) {
	k := r.URL.Query().Get("idempotency_key")
	if k == "" {
		writeError(w, r, ErrInvalid)
		return
	}
	ext.mu.Lock()
	id, ok := ext.approvalsByKey[k]
	var rec ApprovalRecord
	if ok {
		rec = ext.approvalsByID[id]
	}
	ext.mu.Unlock()
	if !ok {
		ipc.WriteError(w, http.StatusNotFound, "NOT_FOUND", "approval request not found", ipc.RequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, rec)
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
