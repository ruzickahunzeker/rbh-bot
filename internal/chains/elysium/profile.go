// Package elysium defines the Elysium Chain (Hyperliquid Arbitrum Orbit L2, chain_id=99901) ChainProfile.
package elysium

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
)

const (
	ChainID         uint64 = 99901
	ChainSlug              = "ely"
	ProfileRevision        = "elysium-mainnet-v1"
)

var (
	WHYPEAddress         = common.HexToAddress("0x5555555555555555555555555555555555555555")
	NodeInterfaceAddress = common.HexToAddress("0x00000000000000000000000000000000000000C8")
	Multicall3Address    = common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")
)

func Profile() evm.ChainProfile {
	return evm.ChainProfile{
		ChainID:         ChainID,
		ChainSlug:       ChainSlug,
		NetworkKind:     "mainnet",
		ProfileRevision: ProfileRevision,
		NativeSymbol:    "HYPE",
		WrappedNative:   WHYPEAddress,
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
			MinEffectiveGasPriceWei: big.NewInt(10_000_000),
		},
		Finality: evm.FinalityPolicy{
			ExecutionContinuityTag:       "latest",
			ExecutionContinuityMinBlocks: 2,
			ExposureSettlementTag:        "finalized",
			ExposureSettlementMinBlocks:  10,
		},
		Replacement: evm.ReplacementDisabled,
		Submission:  evm.SubmissionPublic,
	}
}
