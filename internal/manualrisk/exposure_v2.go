package manualrisk

import (
	"errors"
	"math/big"
)

type OperationKind string

const (
	OpBuy     OperationKind = "buy"
	OpSell    OperationKind = "sell"
	OpApprove OperationKind = "approve"
)

// UnfinalizedOperation represents a single wallet generation for native-denominated
// exposure accounting (unfinalized_native_outlay_wei).
type UnfinalizedOperation struct {
	OperationID       string
	Kind              OperationKind
	NativeValueWei    *big.Int // >0 only for BUY; always 0 for SELL and APPROVE
	MaxNativeGasWei   *big.Int // worst-case native gas outlay (L2 + L1)
	FinalizedSettled  bool     // true once ExposureSettlementLevel is reached
}

// ExposureLimits bounds unfinalized native outlay and concurrent unfinalized generation count.
type ExposureLimits struct {
	MaxWalletUnfinalizedNativeOutlayWei *big.Int
	MaxWalletUnfinalizedOps             int
}

// ComputeUnfinalizedNativeOutlayWei computes the exact native-denominated wei outlay
// across all generations not yet at ExposureSettlementLevel.
// SELL and APPROVE have tx.value == 0 and contribute ONLY MaxNativeGasWei, never ERC-20 token units.
func ComputeUnfinalizedNativeOutlayWei(ops []UnfinalizedOperation) (totalWei *big.Int, unfinalizedCount int, err error) {
	total := big.NewInt(0)
	count := 0
	for _, op := range ops {
		if op.FinalizedSettled {
			continue
		}
		if op.MaxNativeGasWei == nil || op.MaxNativeGasWei.Sign() < 0 {
			return nil, 0, errors.New("invalid max native gas wei")
		}
		total.Add(total, op.MaxNativeGasWei)
		if op.Kind == OpBuy {
			if op.NativeValueWei == nil || op.NativeValueWei.Sign() < 0 {
				return nil, 0, errors.New("invalid buy native value wei")
			}
			total.Add(total, op.NativeValueWei)
		}
		count++
	}
	return total, count, nil
}

// CheckCandidateExposure verifies whether admitting candidate stays within limits.
func CheckCandidateExposure(existing []UnfinalizedOperation, candidate UnfinalizedOperation, limits ExposureLimits) error {
	all := make([]UnfinalizedOperation, 0, len(existing)+1)
	all = append(all, existing...)
	all = append(all, candidate)
	total, count, err := ComputeUnfinalizedNativeOutlayWei(all)
	if err != nil {
		return err
	}
	if limits.MaxWalletUnfinalizedOps > 0 && count > limits.MaxWalletUnfinalizedOps {
		return errors.New("wallet unfinalized operation count limit exceeded")
	}
	if limits.MaxWalletUnfinalizedNativeOutlayWei != nil && total.Cmp(limits.MaxWalletUnfinalizedNativeOutlayWei) > 0 {
		return errors.New("wallet unfinalized native outlay limit exceeded")
	}
	return nil
}
