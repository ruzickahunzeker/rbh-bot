// Package arc defines the Circle Arc Mainnet (chain_id=5042) ChainProfile.
// Arc uses native USDC as gas (18 native wei decimals, 6 ERC-20 decimals) with
// Malachite BFT sub-second deterministic finality.
package arc

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
)

const (
	ChainID            uint64 = 5042
	ChainSlug                 = "arc"
	ProfileRevision           = "arc-mainnet-v1"
	NativeGasDecimals  uint8  = 18
	ERC20USDCDecimals  uint8  = 6
)

var (
	// WrappedUSDCAddress is the canonical wrapped native USDC contract on Arc.
	WrappedUSDCAddress = common.HexToAddress("0x3600000000000000000000000000000000000000")
	Multicall3Address  = common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")
)

// NativeWeiToERC20USDC converts Arc's 18-decimal native USDC gas units to 6-decimal ERC-20 USDC units.
func NativeWeiToERC20USDC(nativeWei *big.Int) *big.Int {
	if nativeWei == nil || nativeWei.Sign() <= 0 {
		return big.NewInt(0)
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(12), nil) // 10^(18-6)
	return new(big.Int).Div(nativeWei, scale)
}

func Profile() evm.ChainProfile {
	return evm.ChainProfile{
		ChainID:         ChainID,
		ChainSlug:       ChainSlug,
		NetworkKind:     "mainnet",
		ProfileRevision: ProfileRevision,
		NativeSymbol:    "USDC",
		WrappedNative:   WrappedUSDCAddress,
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
			MinEffectiveGasPriceWei: big.NewInt(1_000_000),
		},
		Finality: evm.FinalityPolicy{
			ExecutionContinuityTag:       "latest", // Malachite BFT deterministic 1-block finality
			ExecutionContinuityMinBlocks: 1,
			ExposureSettlementTag:        "finalized",
			ExposureSettlementMinBlocks:  1,
		},
		Replacement: evm.ReplacementDisabled,
		Submission:  evm.SubmissionPublic,
	}
}
