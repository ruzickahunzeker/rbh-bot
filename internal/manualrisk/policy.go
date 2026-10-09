package manualrisk

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

type InputLimit struct {
	Unit            Unit   `json:"unit"`
	MaxOperation    string `json:"max_operation"`
	MaxWalletActive string `json:"max_wallet_active"`
	MaxGlobalActive string `json:"max_global_active"`
}

// Policy is an additional typed MANUAL policy, not a rebind/reinterpretation
// of the original C2 C07 policy. Limits are operator-owned, never HTTP inputs.
type Policy struct {
	ID                        string       `json:"id"`
	SourcePolicyVersion       uint64       `json:"source_policy_version"`
	SourcePolicyHash          string       `json:"source_policy_hash"`
	ChainID                   uint64       `json:"chain_id"`
	Protocol                  string       `json:"protocol"`
	InputLimits               []InputLimit `json:"input_limits"`
	MaxNativeGasPerOperation  string       `json:"max_native_gas_per_operation"`
	MaxNativeWalletOutlay     string       `json:"max_native_wallet_outlay"`
	MaxNativeGlobalOutlay     string       `json:"max_native_global_outlay"`
	MinNativeBalance          string       `json:"min_native_balance"`
	MaxSellBPS                uint64       `json:"max_sell_bps"`
	MaxWalletOperationsWindow uint64       `json:"max_wallet_operations_window"`
	MaxGlobalOperationsWindow uint64       `json:"max_global_operations_window"`
	WindowSeconds             uint64       `json:"window_seconds"`
}

func validID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_-.", c)) {
			return false
		}
	}
	return true
}

func digest(s string) bool {
	if len(s) != 64 || s != strings.ToLower(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func (p Policy) Validate() error {
	if !validID(p.ID) || p.SourcePolicyVersion == 0 || p.SourcePolicyVersion > 1<<63-1 || !digest(p.SourcePolicyHash) || p.ChainID != 4663 || p.Protocol != "PONS_V2_CURVE" || len(p.InputLimits) == 0 || len(p.InputLimits) > 128 || p.MaxSellBPS == 0 || p.MaxSellBPS > 10000 || p.WindowSeconds == 0 || p.WindowSeconds > 31536000 || p.MaxWalletOperationsWindow == 0 || p.MaxGlobalOperationsWindow < p.MaxWalletOperationsWindow || p.MaxGlobalOperationsWindow > 1<<63-1 {
		return ErrInvalid
	}
	previous := ""
	for _, l := range p.InputLimits {
		if l.Unit.Validate() != nil || l.Unit.Kind != TradeInput || l.Unit.Asset <= previous {
			return ErrInvalid
		}
		previous = l.Unit.Asset // one decimals declaration per asset, sorted
		o, e := uint256(l.MaxOperation, false)
		w, we := uint256(l.MaxWalletActive, false)
		g, ge := uint256(l.MaxGlobalActive, false)
		if e != nil || we != nil || ge != nil || o.Cmp(w) > 0 || w.Cmp(g) > 0 {
			return ErrInvalid
		}
	}
	g, e := uint256(p.MaxNativeGasPerOperation, false)
	w, we := uint256(p.MaxNativeWalletOutlay, false)
	t, te := uint256(p.MaxNativeGlobalOutlay, false)
	_, re := uint256(p.MinNativeBalance, true)
	if e != nil || we != nil || te != nil || re != nil || g.Cmp(w) > 0 || w.Cmp(t) > 0 {
		return ErrInvalid
	}
	return nil
}

func (p Policy) Canonical() (string, string, error) {
	if err := p.Validate(); err != nil {
		return "", "", err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", "", err
	}
	h := sha256.Sum256(b)
	return string(b), hex.EncodeToString(h[:]), nil
}

func ParsePolicy(raw, hash string, sourceVersion uint64, sourceHash string) (Policy, error) {
	var p Policy
	if len(raw) > 65536 {
		return p, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewBufferString(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil {
		return Policy{}, ErrInvalid
	}
	canonical, got, err := p.Canonical()
	if err != nil || canonical != raw || got != hash || p.SourcePolicyVersion != sourceVersion || p.SourcePolicyHash != sourceHash {
		return Policy{}, ErrInvalid
	}
	return p, nil
}

func (p Policy) Limit(unit Unit) (InputLimit, error) {
	if p.Validate() != nil || unit.Validate() != nil || unit.Kind != TradeInput {
		return InputLimit{}, ErrInvalid
	}
	for _, l := range p.InputLimits {
		if l.Unit == unit {
			return l, nil
		}
	}
	return InputLimit{}, ErrInvalid
}
