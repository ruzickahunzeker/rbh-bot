// Package robinhood defines the Robinhood Chain (Arbitrum Orbit L2, chain_id=4663) ChainProfile.
package robinhood

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
)

const (
	ChainID         uint64 = 4663
	ChainSlug              = "rbh"
	V1Alias                = "robinhood"
	ProfileRevision        = "rbh-mainnet-v1"
)

// NodeInterfaceAddress is the standard Arbitrum Nitro precompile for splitting L2 and L1 gas estimates.
var NodeInterfaceAddress = common.HexToAddress("0x00000000000000000000000000000000000000C8")

// Multicall3Address is the canonical Multicall3 deployment address across EVM chains.
var Multicall3Address = common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")

func Profile() evm.ChainProfile {
	return evm.ChainProfile{
		ChainID:         ChainID,
		ChainSlug:       ChainSlug,
		V1Aliases:       []string{V1Alias},
		NetworkKind:     "mainnet",
		ProfileRevision: ProfileRevision,
		NativeSymbol:    "ETH",
		Multicall3:      Multicall3Address,
		Account: evm.AccountCapabilities{
			PureEOAOnly:         true,
			DecodeEIP7702Blocks: true,
		},
		TxEnvelope: evm.TxEnvelopeCapabilities{
			SupportsLegacy:  true,
			SupportsEIP1559: true,
		},
		Fee: evm.FeePolicy{
			ZeroBaseFee:             false,
			EstimateIncludesL1Fee:   true,
			SupportsNodeInterfaceL1: true,
			NodeInterfaceAddress:    NodeInterfaceAddress,
		},
		Finality: evm.FinalityPolicy{
			ExecutionContinuityTag:       "latest",
			ExecutionContinuityMinBlocks: 1,
			ExposureSettlementTag:        "finalized",
			ExposureSettlementMinBlocks:  1,
		},
		Replacement: evm.ReplacementDisabled,
		Submission:  evm.SubmissionPublic,
	}
}

// SplitNitroFeeBound computes separate L2 execution gas limit and L1 data fee bound
// from Arbitrum NodeInterface gasEstimateComponents, preventing high user maxFeePerGas
// from artificially amplifying the L1 data fee component.
func SplitNitroFeeBound(txValueWei *big.Int, l2GasEstimate, l1GasEstimate uint64, currentL2BaseFeeWei, userMaxFeePerGasWei *big.Int, l2MarginBps, l1MarginBps uint64) (totalGasLimit uint64, nativeOutlayBound *big.Int, err error) {
	if l2GasEstimate == 0 || currentL2BaseFeeWei == nil || currentL2BaseFeeWei.Sign() <= 0 || userMaxFeePerGasWei == nil || userMaxFeePerGasWei.Sign() <= 0 {
		return 0, nil, errors.New("invalid Nitro fee estimation components")
	}
	l2Buffered := applyBpsMargin(l2GasEstimate, l2MarginBps)
	// L1 data fee in wei at estimation time = l1GasEstimate * currentL2BaseFeeWei.
	l1FeeWei := new(big.Int).Mul(new(big.Int).SetUint64(l1GasEstimate), currentL2BaseFeeWei)
	l1FeeBuffered := applyBigBpsMargin(l1FeeWei, l1MarginBps)

	// Convert buffered L1 fee to gas units at current L2 baseFee for the transaction's gasLimit.
	l1GasUnits := new(big.Int).Div(new(big.Int).Add(l1FeeBuffered, new(big.Int).Sub(currentL2BaseFeeWei, big.NewInt(1))), currentL2BaseFeeWei)
	if !l1GasUnits.IsUint64() {
		return 0, nil, errors.New("L1 gas units overflow")
	}
	totalGasLimit = l2Buffered + l1GasUnits.Uint64()
	nativeOutlayBound, err = evm.ComputeNativeOutlayBound(txValueWei, l2Buffered, userMaxFeePerGasWei, l1FeeBuffered)
	if err != nil {
		return 0, nil, err
	}
	return totalGasLimit, nativeOutlayBound, nil
}

func applyBpsMargin(v, bps uint64) uint64 {
	if bps == 0 {
		return v
	}
	return v + (v*bps+9999)/10000
}

func applyBigBpsMargin(v *big.Int, bps uint64) *big.Int {
	if v == nil || bps == 0 {
		return new(big.Int).Set(v)
	}
	extra := new(big.Int).Mul(v, new(big.Int).SetUint64(bps))
	extra.Add(extra, big.NewInt(9999))
	extra.Div(extra, big.NewInt(10000))
	return new(big.Int).Add(v, extra)
}
