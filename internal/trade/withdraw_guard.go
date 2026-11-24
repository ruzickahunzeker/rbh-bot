package trade

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrWithdrawDisabled     = errors.New("withdraw allowlist is empty; withdrawals are disabled")
	ErrWithdrawNotAllowed   = errors.New("destination address is not in server-side EVM_WITHDRAW_ALLOWLIST")
	ErrWithdrawCapExceeded  = errors.New("withdrawal amount exceeds per-tx safety cap")
)

// WithdrawGuard enforces server-side destination allowlist and per-tx native amount cap
// so Telegram or compromised UI sessions can never drain funds to an arbitrary address.
type WithdrawGuard struct {
	allowed     map[common.Address]struct{}
	maxAmountWei *big.Int
}

func NewWithdrawGuard(allowlistCSV string, maxAmountWei *big.Int) (*WithdrawGuard, error) {
	allowed := make(map[common.Address]struct{})
	for _, raw := range strings.Split(allowlistCSV, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !common.IsHexAddress(raw) {
			return nil, fmt.Errorf("invalid allowlist address %q", raw)
		}
		addr := common.HexToAddress(raw)
		if addr == (common.Address{}) {
			return nil, errors.New("zero address forbidden in withdraw allowlist")
		}
		allowed[addr] = struct{}{}
	}
	var capCopy *big.Int
	if maxAmountWei != nil && maxAmountWei.Sign() > 0 {
		capCopy = new(big.Int).Set(maxAmountWei)
	}
	return &WithdrawGuard{allowed: allowed, maxAmountWei: capCopy}, nil
}

func (g *WithdrawGuard) Validate(dest common.Address, amountWei *big.Int) error {
	if g == nil || len(g.allowed) == 0 {
		return ErrWithdrawDisabled
	}
	if dest == (common.Address{}) || amountWei == nil || amountWei.Sign() <= 0 {
		return errors.New("invalid withdrawal destination or amount")
	}
	if _, ok := g.allowed[dest]; !ok {
		return fmt.Errorf("%w: %s", ErrWithdrawNotAllowed, dest.Hex())
	}
	if g.maxAmountWei != nil && amountWei.Cmp(g.maxAmountWei) > 0 {
		return fmt.Errorf("%w: requested=%s max=%s", ErrWithdrawCapExceeded, amountWei.String(), g.maxAmountWei.String())
	}
	return nil
}
