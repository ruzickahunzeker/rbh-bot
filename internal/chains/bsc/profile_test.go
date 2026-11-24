package bsc

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
)

func TestBSCProfileZeroBaseFeeAndPrivateOnlyGuard(t *testing.T) {
	p := Profile()
	if p.ChainID != 56 || !p.MatchesSlug("bsc") || p.MatchesSlug("rbh") {
		t.Fatalf("unexpected BSC profile: %+v", p)
	}
	if !p.Fee.ZeroBaseFee || !p.Account.DecodeEIP7702Blocks {
		t.Fatalf("expected BSC zero-baseFee and EIP-7702 decode flags")
	}
	// AT-21: maxFee=3 gwei, tip=0.05 gwei -> rejected because EffectiveGasPriceForBSC = tip < 0.1 gwei floor.
	if _, err := p.ComputeEffectiveGasPrice(big.NewInt(0), big.NewInt(3_000_000_000), big.NewInt(50_000_000)); err == nil {
		t.Fatal("expected BSC tip below floor to fail")
	}
	// maxFee=3 gwei, tip=0.2 gwei -> effective=0.2 gwei.
	eff, err := p.ComputeEffectiveGasPrice(big.NewInt(0), big.NewInt(3_000_000_000), big.NewInt(200_000_000))
	if err != nil || eff.Cmp(big.NewInt(200_000_000)) != 0 {
		t.Fatalf("unexpected effective gas price: %v err=%v", eff, err)
	}
	// AT-19: private_only forbids fallback to public mempool.
	if err := ValidateBroadcastRoute(evm.SubmissionPrivateOnly, true); !errors.Is(err, ErrPrivateOnlyNoPublicFallback) {
		t.Fatalf("expected ErrPrivateOnlyNoPublicFallback, got %v", err)
	}
	if err := ValidateBroadcastRoute(evm.SubmissionPrivateOnly, false); err != nil {
		t.Fatalf("expected private route to pass: %v", err)
	}
}
