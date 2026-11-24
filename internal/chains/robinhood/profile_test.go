package robinhood

import (
	"math/big"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
)

func TestRobinhoodProfileAndSplitNitroFeeBound(t *testing.T) {
	p := Profile()
	if p.ChainID != 4663 || !p.MatchesSlug("rbh") || !p.MatchesSlug("robinhood") || p.MatchesSlug("bsc") {
		t.Fatalf("unexpected robinhood profile: %+v", p)
	}
	// Scenario from AT-21: low L2 baseFee (0.01 gwei) + high user max_gwei (3 gwei).
	// Naive (l2Gas+l1Gas)*3gwei would amplify L1 data fee by 300x; SplitNitroFeeBound avoids amplification.
	l2Base := big.NewInt(10_000_000)       // 0.01 gwei
	userMax := big.NewInt(3_000_000_000)   // 3 gwei
	txVal := big.NewInt(10_000_000_000_000_000) // 0.01 ETH
	gasLimit, outlay, err := SplitNitroFeeBound(txVal, 200_000, 150_000, l2Base, userMax, 1500, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if gasLimit <= 350_000 {
		t.Fatalf("expected buffered total gas limit > 350000, got %d", gasLimit)
	}
	// Expected L2 max cost = 230_000 * 3 gwei = 0.00069 ETH; L1 buffered = 180_000 * 0.01 gwei = 0.0000018 ETH.
	// Total gas outlay ~ 0.0006918 ETH (NOT 0.00123 ETH from 300x L1 amplification).
	maxExpected := new(big.Int).Add(txVal, big.NewInt(800_000_000_000_000)) // 0.0108 ETH
	if outlay.Cmp(maxExpected) >= 0 {
		t.Fatalf("L1 data fee was over-amplified: outlay=%s >= maxExpected=%s", outlay, maxExpected)
	}

	// Verify BSC zero-baseFee Type-2 effective gas price floor check (AT-21).
	bsc := evm.ChainProfile{
		ChainID:   56,
		ChainSlug: "bsc",
		Fee: evm.FeePolicy{
			ZeroBaseFee:             true,
			MinEffectiveGasPriceWei: big.NewInt(100_000_000), // 0.1 gwei floor
		},
	}
	// max_gwei=3, tip_gwei=0.05 -> underpriced on BSC because baseFee=0!
	if _, err := bsc.ComputeEffectiveGasPrice(big.NewInt(0), big.NewInt(3_000_000_000), big.NewInt(50_000_000)); err == nil {
		t.Fatal("expected BSC low tip_gwei below floor to fail even with high max_gwei")
	}
	eff, err := bsc.ComputeEffectiveGasPrice(big.NewInt(0), big.NewInt(3_000_000_000), big.NewInt(200_000_000))
	if err != nil || eff.Cmp(big.NewInt(200_000_000)) != 0 {
		t.Fatalf("expected BSC effective price = tip_gwei (0.2 gwei), got %v err=%v", eff, err)
	}
}
