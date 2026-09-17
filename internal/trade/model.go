package trade

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/bot"
)

const (
	ChainID            uint64 = 4663
	DefaultSlippageBPS uint64 = 500
)

var (
	ErrInvalidRequest      = errors.New("invalid dry-run request")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrUnknownRoute        = errors.New("unknown Pons Curve route")
	ErrStaleState          = errors.New("stale or changed chain state")
	ErrSimulationReverted  = errors.New("simulation reverted")
	ErrTradeStore          = errors.New("trade store unavailable")
)

type DryRunRequest struct {
	WalletID string              `json:"wallet_id"`
	Intent   bot.OperationIntent `json:"intent"`
}

func (r DryRunRequest) Validate() error {
	if strings.TrimSpace(r.WalletID) == "" || r.Intent.ID == "" || r.Intent.IdempotencyKey == "" || r.Intent.ID != r.Intent.IdempotencyKey || r.Intent.Status != "created" {
		return ErrInvalidRequest
	}
	if strings.TrimSpace(r.Intent.StrategyID) == "" || strings.TrimSpace(r.Intent.WatchedWalletID) == "" || strings.TrimSpace(r.Intent.SourceEventID) == "" || strings.TrimSpace(r.Intent.SourceObservationID) == "" || r.Intent.PolicyVersion == 0 {
		return ErrInvalidRequest
	}
	if len(r.Intent.SourceTxHash) != 66 || len(common.FromHex(r.Intent.SourceTxHash)) != common.HashLength || common.HexToHash(r.Intent.SourceTxHash) == (common.Hash{}) {
		return ErrInvalidRequest
	}
	if r.Intent.Kind != "copy_buy" && r.Intent.Kind != "copy_sell" {
		return ErrInvalidRequest
	}
	if !common.IsHexAddress(r.Intent.Token) || common.HexToAddress(r.Intent.Token) == (common.Address{}) {
		return ErrInvalidRequest
	}
	amount, ok := new(big.Int).SetString(r.Intent.AmountValue, 10)
	if !ok || amount.Sign() <= 0 || amount.BitLen() > 256 || amount.String() != r.Intent.AmountValue {
		return ErrInvalidRequest
	}
	if (r.Intent.Kind == "copy_buy" && r.Intent.AmountMode != "fixed_input") || (r.Intent.Kind == "copy_sell" && r.Intent.AmountMode != "balance_bps") {
		return ErrInvalidRequest
	}
	if r.Intent.Kind == "copy_sell" && amount.Cmp(big.NewInt(10_000)) > 0 {
		return ErrInvalidRequest
	}
	return nil
}

func (r DryRunRequest) Fingerprint() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

type BlockRef struct {
	Number uint64      `json:"number"`
	Hash   common.Hash `json:"hash"`
	Time   time.Time   `json:"time"`
}

type CurveRoute struct {
	Protocol    string         `json:"protocol"`
	Token       common.Address `json:"token"`
	Curve       common.Address `json:"curve"`
	QuoteToken  common.Address `json:"quote_token"`
	NativeQuote bool           `json:"native_quote"`
}

type ExecutionParameters struct {
	Direction   string `json:"direction"`
	AmountIn    string `json:"amount_in"`
	MinimumOut  string `json:"minimum_out"`
	QuotedOut   string `json:"quoted_out"`
	SlippageBPS uint64 `json:"slippage_bps"`
	Recipient   string `json:"recipient"`
}

type UnsignedCall struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Value string `json:"value"`
	Data  string `json:"data"`
}

type DryRunResult struct {
	OperationID   string              `json:"operation_id"`
	StepID        string              `json:"step_id"`
	Status        string              `json:"status"`
	Route         CurveRoute          `json:"route"`
	Parameters    ExecutionParameters `json:"parameters"`
	UnsignedCall  UnsignedCall        `json:"unsigned_call"`
	Block         BlockRef            `json:"block"`
	ReturnData    string              `json:"return_data,omitempty"`
	FailureCode   string              `json:"failure_code,omitempty"`
	FailureDetail string              `json:"failure_detail,omitempty"`
	Duplicate     bool                `json:"duplicate"`
}

func operationID(request DryRunRequest) string {
	return request.Intent.ID
}

func stepID(operation string) string {
	digest := sha256.Sum256([]byte(operation + "\x00dry-run\x000"))
	return hex.EncodeToString(digest[:])
}
