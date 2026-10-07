// Package tradeapi is the user-facing HTTP contract. This slice is deliberately
// rejection-only: it has no execution, signer, RPC, permit or nonce dependency.
package tradeapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrInvalid  = errors.New("invalid trade request")
	ErrConflict = errors.New("idempotency conflict")
	ErrNotFound = errors.New("request not found")
	ErrWallet   = errors.New("wallet unavailable")
	ErrPolicy   = errors.New("request exceeds API policy")
)

type Request struct {
	Chain           string  `json:"chain"`
	WalletID        string  `json:"wallet_id"`
	Token           string  `json:"token"`
	Side            string  `json:"side"`
	Amount          string  `json:"amount,omitempty"`
	SellPercent     string  `json:"sell_percent,omitempty"`
	SlippagePercent string  `json:"slippage_percent"`
	Fee             *Fee    `json:"fee,omitempty"`
	TTLSeconds      *uint64 `json:"ttl_seconds,omitempty"`
	MinReceive      string  `json:"min_receive,omitempty"`
}

type Fee struct {
	MaxGwei        string `json:"max_gwei,omitempty"`
	TipGwei        string `json:"tip_gwei,omitempty"`
	MaxTotalNative string `json:"max_total_native,omitempty"`
}

// Policy constrains this disabled API only; it is NOT a C07 policy attestation
// or runtime authorization. Execution policy binding is deferred, not invented.
type Policy struct {
	Version           uint64 `json:"version"`
	DefaultTTLSeconds uint64 `json:"default_ttl_seconds"`
	MaxTTLSeconds     uint64 `json:"max_ttl_seconds"`
	MaxSlippageBPS    uint64 `json:"max_slippage_bps"`
	MaxFeeGwei        string `json:"max_fee_gwei"`
}

func DefaultPolicy() Policy {
	return Policy{Version: 1, DefaultTTLSeconds: 30, MaxTTLSeconds: 300, MaxSlippageBPS: 500, MaxFeeGwei: "100"}
}

// decimal accepts ordinary decimal notation only. No floats, rounding,
// exponent notation, signs or precision loss. Returned text is canonical.
func decimal(value string, precision int, allowZero bool) (string, *big.Int, error) {
	if value == "" || len(value) > 256 || precision < 0 || precision > 255 {
		return "", nil, ErrInvalid
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 1 && parts[0][0] == '0' {
		return "", nil, ErrInvalid
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
		if frac == "" || len(frac) > precision {
			return "", nil, ErrInvalid
		}
	}
	for _, part := range parts {
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return "", nil, ErrInvalid
			}
		}
	}
	units, ok := new(big.Int).SetString(parts[0]+frac+strings.Repeat("0", precision-len(frac)), 10)
	if !ok || units.Sign() < 0 || !allowZero && units.Sign() == 0 {
		return "", nil, ErrInvalid
	}
	frac = strings.TrimRight(frac, "0")
	canonical := parts[0]
	if frac != "" {
		canonical += "." + frac
	}
	return canonical, units, nil
}

// HumanUnits must be called with independently verified asset decimals before
// future execution. This slice does not guess token decimals or sell balances.
func HumanUnits(value string, decimals uint8) (string, error) {
	_, units, err := decimal(value, int(decimals), false)
	if err != nil || units.BitLen() > 256 {
		return "", ErrInvalid
	}
	return units.String(), nil
}

func validID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("._:-", ch)) {
			return false
		}
	}
	return true
}

func hash(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }

// normalize applies syntax and unit rules, not mutable/default API policy.
// Duplicate retrieval happens before applying a changed policy or making TTL.
func normalize(r Request) (Request, error) {
	if !validID(r.Chain) || !validID(r.WalletID) || len(r.Token) != 42 || !strings.HasPrefix(r.Token, "0x") || !common.IsHexAddress(r.Token) || common.HexToAddress(r.Token) == (common.Address{}) || (r.Side != "buy" && r.Side != "sell") {
		return Request{}, ErrInvalid
	}
	r.Token = strings.ToLower(common.HexToAddress(r.Token).Hex())
	if (r.Amount == "") == (r.SellPercent == "") || r.Side == "buy" && r.SellPercent != "" {
		return Request{}, ErrInvalid
	}
	var err error
	if r.Amount != "" {
		precision := 255 // Token precision remains UNVERIFIED, never execution-ready.
		if r.Side == "buy" {
			precision = 18
		}
		r.Amount, _, err = decimal(r.Amount, precision, false)
		if err != nil {
			return Request{}, err
		}
		if r.Side == "buy" {
			if _, err = HumanUnits(r.Amount, 18); err != nil {
				return Request{}, err
			}
		}
	}
	if r.SellPercent != "" {
		var bps *big.Int
		r.SellPercent, bps, err = decimal(r.SellPercent, 2, false)
		if err != nil || bps.Cmp(big.NewInt(10000)) > 0 {
			return Request{}, ErrInvalid
		}
	}
	var slip *big.Int
	r.SlippagePercent, slip, err = decimal(r.SlippagePercent, 2, true)
	if err != nil || slip.Cmp(big.NewInt(10000)) >= 0 {
		return Request{}, ErrInvalid
	}
	if r.TTLSeconds != nil && (*r.TTLSeconds == 0 || *r.TTLSeconds > 86400) {
		return Request{}, ErrInvalid
	}
	if r.MinReceive != "" {
		precision := 255
		if r.Side == "sell" {
			precision = 18
		}
		r.MinReceive, _, err = decimal(r.MinReceive, precision, false)
		if err != nil {
			return Request{}, err
		}
	}
	if r.Fee != nil {
		f := *r.Fee
		if f.MaxGwei == "" && f.TipGwei == "" && f.MaxTotalNative == "" {
			return Request{}, ErrInvalid
		}
		var max, tip *big.Int
		if f.MaxGwei != "" {
			f.MaxGwei, max, err = decimal(f.MaxGwei, 9, false)
			if err != nil || max.BitLen() > 256 {
				return Request{}, ErrInvalid
			}
		}
		if f.TipGwei != "" {
			f.TipGwei, tip, err = decimal(f.TipGwei, 9, true)
			if err != nil || max == nil || tip.Cmp(max) > 0 {
				return Request{}, ErrInvalid
			}
		}
		if f.MaxTotalNative != "" {
			f.MaxTotalNative, _, err = decimal(f.MaxTotalNative, 18, false)
			if err != nil {
				return Request{}, err
			}
			if _, err = HumanUnits(f.MaxTotalNative, 18); err != nil {
				return Request{}, err
			}
		}
		r.Fee = &f
	}
	return r, nil
}

func (p Policy) validate() error {
	if p.Version == 0 || p.Version > 1<<63-1 || p.DefaultTTLSeconds == 0 || p.DefaultTTLSeconds > p.MaxTTLSeconds || p.MaxTTLSeconds > 86400 || p.MaxSlippageBPS >= 10000 {
		return ErrInvalid
	}
	_, fee, err := decimal(p.MaxFeeGwei, 9, false)
	if err != nil || fee.BitLen() > 256 {
		return ErrInvalid
	}
	return nil
}

func (p Policy) apply(r Request) (uint64, error) {
	if r.Chain != "robinhood" {
		return 0, ErrPolicy
	}
	_, slip, _ := decimal(r.SlippagePercent, 2, true)
	if slip.Cmp(new(big.Int).SetUint64(p.MaxSlippageBPS)) > 0 {
		return 0, ErrPolicy
	}
	ttl := p.DefaultTTLSeconds
	if r.TTLSeconds != nil {
		ttl = *r.TTLSeconds
	}
	if ttl > p.MaxTTLSeconds {
		return 0, ErrPolicy
	}
	if r.Fee != nil && r.Fee.MaxGwei != "" {
		_, fee, _ := decimal(r.Fee.MaxGwei, 9, false)
		_, cap, _ := decimal(p.MaxFeeGwei, 9, false)
		if fee.Cmp(cap) > 0 {
			return 0, ErrPolicy
		}
	}
	return ttl, nil
}

func encode(value any) string { data, _ := json.Marshal(value); return string(data) }
