package manualrisk

import (
	"math/big"
	"testing"
)

func TestComputeUnfinalizedNativeOutlayAndFinalityRelease(t *testing.T) {
	// AT-04: BUY (0.05 ETH + 0.001 gas), SELL (0 native value + 0.001 gas, even if selling 10^24 tokens),
	// APPROVE (0 native value + 0.0005 gas).
	buyVal := big.NewInt(50_000_000_000_000_000)  // 0.05 ETH
	gas1 := big.NewInt(1_000_000_000_000_000)     // 0.001 ETH
	gasApprove := big.NewInt(500_000_000_000_000) // 0.0005 ETH

	ops := []UnfinalizedOperation{
		{OperationID: "gen-1", Kind: OpBuy, NativeValueWei: buyVal, MaxNativeGasWei: gas1},
		{OperationID: "gen-2", Kind: OpApprove, NativeValueWei: big.NewInt(0), MaxNativeGasWei: gasApprove},
		{OperationID: "gen-3", Kind: OpSell, NativeValueWei: big.NewInt(0), MaxNativeGasWei: gas1},
	}
	total, count, err := ComputeUnfinalizedNativeOutlayWei(ops)
	if err != nil {
		t.Fatal(err)
	}
	want := big.NewInt(52_500_000_000_000_000) // 0.0525 ETH
	if count != 3 || total.Cmp(want) != 0 {
		t.Fatalf("got total=%s count=%d, want total=%s count=3", total, count, want)
	}

	// Once gen-1 reaches ExposureSettlementLevel (FinalizedSettled=true), its 0.051 ETH is released.
	ops[0].FinalizedSettled = true
	limits := ExposureLimits{
		MaxWalletUnfinalizedNativeOutlayWei: big.NewInt(60_000_000_000_000_000), // 0.06 ETH
		MaxWalletUnfinalizedOps:             5,
	}
	nextBuy := UnfinalizedOperation{OperationID: "gen-4", Kind: OpBuy, NativeValueWei: buyVal, MaxNativeGasWei: gas1}
	if err := CheckCandidateExposure(ops, nextBuy, limits); err != nil {
		t.Fatalf("expected next buy after finality release to fit within 0.06 ETH cap: %v", err)
	}
}
