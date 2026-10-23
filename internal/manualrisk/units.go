// Package manualrisk defines pure unit-aware MANUAL arithmetic. It has no
// database, network, authorization, signing or execution capabilities.
package manualrisk

import (
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
)

var ErrInvalid = errors.New("invalid manual risk quantity or policy")

const (
	TradeInput   = "TRADE_INPUT"
	NativeGas    = "NATIVE_GAS"
	NativeValue  = "NATIVE_VALUE"
	NativeOutlay = "NATIVE_OUTLAY"
)

type Unit struct {
	ChainID  uint64 `json:"chain_id"`
	Asset    string `json:"asset"`
	Decimals uint8  `json:"decimals"`
	Kind     string `json:"kind"`
}

type Quantity struct {
	Unit  Unit   `json:"unit"`
	Units string `json:"units"`
}

func (u Unit) Validate() error {
	if u.ChainID != 4663 {
		return ErrInvalid
	}
	if u.Asset == "native" {
		if u.Decimals != 18 {
			return ErrInvalid
		}
	} else {
		if len(u.Asset) != 42 || !strings.HasPrefix(u.Asset, "0x") || strings.ToLower(u.Asset) != u.Asset || u.Asset == "0x"+strings.Repeat("0", 40) {
			return ErrInvalid
		}
		if _, err := hex.DecodeString(u.Asset[2:]); err != nil {
			return ErrInvalid
		}
	}
	switch u.Kind {
	case TradeInput:
	case NativeGas, NativeValue, NativeOutlay:
		if u.Asset != "native" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func uint256(s string, zero bool) (*big.Int, error) {
	if len(s) == 0 || len(s) > 78 {
		return nil, ErrInvalid
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 || (!zero && n.Sign() == 0) || n.BitLen() > 256 || n.String() != s {
		return nil, ErrInvalid
	}
	return n, nil
}

func (q Quantity) Validate() error {
	if err := q.Unit.Validate(); err != nil {
		return err
	}
	_, err := uint256(q.Units, true)
	return err
}

// Add rejects different assets, decimals, chains AND accounting kinds. It does
// not equate native trade-input and native gas just because both are in wei.
func Add(a, b Quantity) (Quantity, error) {
	if a.Validate() != nil || b.Validate() != nil || a.Unit != b.Unit {
		return Quantity{}, ErrInvalid
	}
	l, _ := uint256(a.Units, true)
	r, _ := uint256(b.Units, true)
	n := new(big.Int).Add(l, r)
	if n.BitLen() > 256 {
		return Quantity{}, ErrInvalid
	}
	return Quantity{a.Unit, n.String()}, nil
}

func CheckCap(used, added, cap Quantity) error {
	if cap.Validate() != nil || cap.Unit != used.Unit || cap.Units == "0" {
		return ErrInvalid
	}
	next, err := Add(used, added)
	if err != nil {
		return err
	}
	n, _ := uint256(next.Units, true)
	maximum, _ := uint256(cap.Units, false)
	if n.Cmp(maximum) > 0 {
		return ErrInvalid
	}
	return nil
}

func GasBudget(limit, fee, tip string) (Quantity, error) {
	l, e := uint256(limit, false)
	f, fe := uint256(fee, false)
	t, te := uint256(tip, true)
	if e != nil || fe != nil || te != nil || t.Cmp(f) > 0 || !l.IsUint64() {
		return Quantity{}, ErrInvalid
	}
	n := new(big.Int).Mul(l, f)
	if n.BitLen() > 256 {
		return Quantity{}, ErrInvalid
	}
	return Quantity{Unit{4663, "native", 18, NativeGas}, n.String()}, nil
}

// Outlay explicitly combines native call value and gas, not a SELL token input.
func Outlay(value, gas Quantity) (Quantity, error) {
	if value.Validate() != nil || gas.Validate() != nil || value.Unit != (Unit{4663, "native", 18, NativeValue}) || gas.Unit != (Unit{4663, "native", 18, NativeGas}) {
		return Quantity{}, ErrInvalid
	}
	u := Unit{4663, "native", 18, NativeOutlay}
	return Add(Quantity{u, value.Units}, Quantity{u, gas.Units})
}

func CheckRetainedBalance(balance, outlay, retained string) error {
	b, e := uint256(balance, true)
	o, oe := uint256(outlay, true)
	r, re := uint256(retained, true)
	if e != nil || oe != nil || re != nil {
		return ErrInvalid
	}
	required := new(big.Int).Add(o, r)
	if required.BitLen() > 256 || b.Cmp(required) < 0 {
		return ErrInvalid
	}
	return nil
}

func CheckFixedSell(amount, balance string, maxBPS uint64) error {
	a, e := uint256(amount, false)
	b, be := uint256(balance, false)
	if e != nil || be != nil || maxBPS == 0 || maxBPS > 10000 {
		return ErrInvalid
	}
	// Intermediate cross-products are intentionally arbitrary precision, while
	// both input quantities remain uint256. No division/rounding is performed.
	used := new(big.Int).Mul(a, big.NewInt(10000))
	maximum := new(big.Int).Mul(b, new(big.Int).SetUint64(maxBPS))
	if used.Cmp(maximum) > 0 {
		return ErrInvalid
	}
	return nil
}

// Budget checks a proposed one-operation input allowance against a declared
// unit; it neither consumes a durable budget nor grants execution permission.
type Budget struct {
	Unit          Unit
	MaxOperations uint64
	MaxTotalInput string
}

func (b Budget) Check(usedOperations uint64, usedTotal, input Quantity) error {
	if b.Unit.Validate() != nil || b.Unit.Kind != TradeInput || b.MaxOperations == 0 || b.MaxOperations > 1<<63-1 || usedOperations >= b.MaxOperations || input.Units == "0" {
		return ErrInvalid
	}
	return CheckCap(usedTotal, input, Quantity{b.Unit, b.MaxTotalInput})
}
