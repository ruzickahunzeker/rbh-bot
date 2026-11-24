// Package bsc defines the BNB Smart Chain (chain_id=56) ChainProfile,
// BEP-226 zero-baseFee effective gas floor validation, BEP-126 fast finality
// policy, and private_only broadcast guard.
package bsc

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
)

const (
	ChainID         uint64 = 56
	ChainSlug              = "bsc"
	ProfileRevision        = "bsc-mainnet-v1"
)

var (
	WBNBAddress       = common.HexToAddress("0xbb4CdB9CBd36B01bD1cBaEBF2De08d9173bc095c")
	Multicall3Address = common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")
	PancakeV2Router     = common.HexToAddress("0x10ED43C718714eb63d5aA57B78B54704E256024E")
	PancakeV2Factory    = common.HexToAddress("0xcA143Ce32Fe78f1f7019d7d551a6402fC5350c73")
	PancakeV3Factory    = common.HexToAddress("0x0BFbCF9fa4f9C56B0F40a671Ad40E0805A091865")
	PancakeV3QuoterV2   = common.HexToAddress("0xB048Bbc1Ee6b733FFfCFb9e9CeF7375518e25997")
	PancakeV3SmartRouter = common.HexToAddress("0x13f4EA83D0bd40E75C8222255bc855a974568Dd4")

	// DefaultMinGasPriceWei is 0.1 gwei (validators currently accept 0.05-0.1 gwei floor).
	DefaultMinGasPriceWei = big.NewInt(100_000_000)

	ErrPrivateOnlyNoPublicFallback = errors.New("bsc private_only submission policy forbids public mempool fallback")
)

func Profile() evm.ChainProfile {
	return evm.ChainProfile{
		ChainID:         ChainID,
		ChainSlug:       ChainSlug,
		NetworkKind:     "mainnet",
		ProfileRevision: ProfileRevision,
		NativeSymbol:    "BNB",
		WrappedNative:   WBNBAddress,
		Multicall3:      Multicall3Address,
		Account: evm.AccountCapabilities{
			PureEOAOnly:         true,
			DecodeEIP7702Blocks: true, // BSC Pascal hardfork (BEP-441)
		},
		TxEnvelope: evm.TxEnvelopeCapabilities{
			SupportsLegacy:  true,
			SupportsEIP1559: true, // BEP-226 zero-baseFee Type-2
		},
		Fee: evm.FeePolicy{
			ZeroBaseFee:             true,
			MinEffectiveGasPriceWei: new(big.Int).Set(DefaultMinGasPriceWei),
			EstimateIncludesL1Fee:   false,
		},
		Finality: evm.FinalityPolicy{
			ExecutionContinuityTag:       "safe",
			ExecutionContinuityMinBlocks: 2, // BEP-126 Fast Finality
			ExposureSettlementTag:        "finalized",
			ExposureSettlementMinBlocks:  15,
		},
		Replacement: evm.ReplacementDisabled,
		Submission:  evm.SubmissionPrivateOnly,
	}
}

// ValidateBroadcastRoute enforces AT-19: private_only never silently falls back to public mempool.
func ValidateBroadcastRoute(mode evm.SubmissionMode, targetIsPublic bool) error {
	if mode == evm.SubmissionPrivateOnly && targetIsPublic {
		return ErrPrivateOnlyNoPublicFallback
	}
	return nil
}
