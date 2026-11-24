package trade

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

var (
	ErrMaxOrderCapExceeded    = errors.New("order amount exceeds configured max_order_native_wei safety cap")
	ErrMaxSlippageCapExceeded = errors.New("order slippage exceeds configured max_slippage_bps safety cap")
	ErrBSCPrivateRelayOnly    = errors.New("BSC (56) live execution strictly requires private_only mode with >= 1 private relay URL")
)

// LiveExecutionPolicy defines hard safety rails enforced before any live signing or broadcast.
type LiveExecutionPolicy struct {
	LiveEnabled       bool
	ChainID           uint64
	MaxOrderNativeWei *big.Int
	MaxSlippageBps    uint64
	RelayMode         RelayMode
	PrivateRelayURLs  []string
}

// ValidateConfig checks static safety invariants at startup or policy reload.
func (p LiveExecutionPolicy) ValidateConfig() error {
	if !p.LiveEnabled {
		return nil // Always safe when live execution is disabled.
	}
	if p.ChainID == 0 {
		return errors.New("live execution requires valid chain_id")
	}
	if p.MaxOrderNativeWei == nil || p.MaxOrderNativeWei.Sign() <= 0 {
		return errors.New("live execution requires positive max_order_native_wei cap")
	}
	if p.MaxSlippageBps == 0 || p.MaxSlippageBps > 5000 {
		return errors.New("live execution requires max_slippage_bps in 1..5000")
	}
	if p.ChainID == 56 {
		if p.RelayMode != RelayModePrivateOnly || len(cleanNonEmpty(p.PrivateRelayURLs)) == 0 {
			return ErrBSCPrivateRelayOnly
		}
	}
	return nil
}

// ValidateOrder checks per-order risk caps before touching any signer or nonce.
func (p LiveExecutionPolicy) ValidateOrder(side ports.Side, amountInRaw *big.Int, slippageBps uint64) error {
	if !p.LiveEnabled {
		return ErrLiveExecutionDisabled
	}
	if err := p.ValidateConfig(); err != nil {
		return err
	}
	if amountInRaw == nil || amountInRaw.Sign() <= 0 {
		return errors.New("positive amount_in_raw required")
	}
	if slippageBps == 0 || slippageBps > p.MaxSlippageBps {
		return fmt.Errorf("%w: requested=%d max=%d", ErrMaxSlippageCapExceeded, slippageBps, p.MaxSlippageBps)
	}
	if side == ports.SideBuy && amountInRaw.Cmp(p.MaxOrderNativeWei) > 0 {
		return fmt.Errorf("%w: requested=%s max=%s", ErrMaxOrderCapExceeded, amountInRaw.String(), p.MaxOrderNativeWei.String())
	}
	return nil
}

func cleanNonEmpty(items []string) []string {
	var out []string
	for _, s := range items {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}
