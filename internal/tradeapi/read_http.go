package tradeapi

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"net/url"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
)

// ReadQueries cannot express a send, signing, nonce or execution operation.
type ReadQueries interface {
	Token(context.Context, string) (chainquery.Token, error)
	Balances(context.Context, string, string) (chainquery.Balances, error)
	Fees(context.Context) (chainquery.Fees, error)
}

func readQuery(raw string, allowToken bool) (string, error) {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return "", ErrInvalid
	}
	if len(q["chain"]) != 1 || q.Get("chain") != "robinhood" {
		return "", ErrInvalid
	}
	for key, values := range q {
		if len(values) != 1 || (key != "chain" && (!allowToken || key != "token")) {
			return "", ErrInvalid
		}
	}
	if values, exists := q["token"]; exists {
		if _, err := chainquery.Address(values[0]); err != nil {
			return "", ErrInvalid
		}
	}
	return q.Get("token"), nil
}

func (h *API) token(w http.ResponseWriter, r *http.Request) {
	if _, err := readQuery(r.URL.RawQuery, false); err != nil {
		writeError(w, r, err)
		return
	}
	if _, err := chainquery.Address(r.PathValue("address")); err != nil {
		writeError(w, r, ErrInvalid)
		return
	}
	if h.queries == nil {
		writeReadError(w, r, chainquery.ErrUnavailable)
		return
	}
	info, err := h.queries.Token(r.Context(), r.PathValue("address"))
	if err != nil {
		writeReadError(w, r, err)
		return
	}
	writeJSON(w, 200, info)
}

func (h *API) balances(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("wallet_id") != h.identity.WalletID {
		ipc.WriteError(w, 403, "WALLET_SCOPE_DENIED", "wallet outside caller scope", ipc.RequestID(r))
		return
	}
	token, err := readQuery(r.URL.RawQuery, true)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if h.queries == nil {
		writeReadError(w, r, chainquery.ErrUnavailable)
		return
	}
	// Validate DB source before and after the RPC window. The last read remains
	// advisory, not a reservation or permission to spend concurrently.
	if _, _, err = h.store.readWalletState(r.Context(), h.identity); err != nil {
		writeError(w, r, err)
		return
	}
	balance, err := h.queries.Balances(r.Context(), h.identity.WalletAddress, token)
	if err != nil {
		writeReadError(w, r, err)
		return
	}
	wallet, blocked, err := h.store.readWalletState(r.Context(), h.identity)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var unreserved *string
	var sellable *string
	if balance.Token != nil {
		value := balance.TokenAmount
		limited := balance.AllowanceLimitedTokenAmount
		if blocked {
			value = "0"
			limited = "0"
		}
		unreserved = &value
		sellable = &limited
	}
	writeJSON(w, 200, struct {
		Wallet                 Wallet              `json:"wallet"`
		Balances               chainquery.Balances `json:"balances"`
		WalletLaneBlocked      bool                `json:"wallet_lane_blocked"`
		UnreservedTokenBalance *string             `json:"unreserved_token_balance"`
		SellableTokenAmount    *string             `json:"sellable_token_amount"`
		AvailableNative        *string             `json:"available_native"`
		AvailabilityScope      string              `json:"availability_scope"`
		SendAuthorized         bool                `json:"send_authorized"`
	}{wallet, balance, blocked, unreserved, sellable, nil, "REFERENCE_ONLY_NOT_EXECUTION_RESERVATION", false})
}

func (h *API) fees(w http.ResponseWriter, r *http.Request) {
	if _, err := readQuery(r.URL.RawQuery, false); err != nil {
		writeError(w, r, err)
		return
	}
	if h.queries == nil {
		writeReadError(w, r, chainquery.ErrUnavailable)
		return
	}
	fee, err := h.queries.Fees(r.Context())
	if err != nil {
		writeReadError(w, r, err)
		return
	}
	_, max, err := decimal(fee.SuggestedMaxGwei, 9, false)
	_, cap, _ := decimal(h.policy.MaxFeeGwei, 9, false)
	_, tip, tipErr := decimal(fee.SuggestedTipGwei, 9, true)
	if err != nil || tipErr != nil || max.Cmp(cap) > 0 || tip.Cmp(max) > 0 || max.Cmp(new(big.Int)) <= 0 {
		writeError(w, r, ErrPolicy)
		return
	}
	writeJSON(w, 200, fee)
}

func writeReadError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := 503, "QUERY_UNAVAILABLE", "read-only chain query unavailable"
	if errors.Is(err, chainquery.ErrObservation) {
		status, code, message = 502, "UNVERIFIABLE_OBSERVATION", "chain observation could not be verified"
	}
	if errors.Is(err, chainquery.ErrUnsupported) {
		status, code, message = 422, "UNSUPPORTED_TOKEN_ROUTE", "token is not an allowed native-quote Pons v2 Curve route"
	}
	ipc.WriteError(w, status, code, message, ipc.RequestID(r))
}
