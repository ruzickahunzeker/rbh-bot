// Package hyperevm defines the HyperEVM (Hyperliquid, chain_id=999) ChainProfile,
// WHYPE canonical address, dual-block gas limit awareness, and HyperSwap V2/V3 contracts.
package hyperevm

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
)

const (
	ChainID         uint64 = 999
	ChainSlug              = "hype"
	ProfileRevision        = "hyperevm-mainnet-v1"
	// HyperEVM small blocks (2s) cap individual tx gas at 2,000,000; large blocks (1m) allow up to 30M.
	SmallBlockMaxGas uint64 = 2_000_000
)

var (
	WHYPEAddress       = common.HexToAddress("0x5555555555555555555555555555555555555555")
	Multicall3Address  = common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")
	HyperSwapV2Factory = common.HexToAddress("0x724412C00059bf7d6ee7d4a1d0D5cd4de3ea1C48")
	HyperSwapV2Router  = common.HexToAddress("0xb4a9C4e6Ea8E2191d2FA5B380452a634Fb21240A")
	HyperSwapV3Factory = common.HexToAddress("0xB1c0fa0B789320044A6F623cFe5eBda9562602E3")
	HyperSwapV3Quoter  = common.HexToAddress("0x03A918028f22D9E1473B7959C927AD7425A45C7C")
	HyperSwapV3Router  = common.HexToAddress("0x6D99e7f6747AF2cDbB5164b6DD50e40D4fDe1e77")
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
			MinEffectiveGasPriceWei: big.NewInt(100_000_000), // 0.1 gwei floor
		},
		Finality: evm.FinalityPolicy{
			ExecutionContinuityTag:       "latest",
			ExecutionContinuityMinBlocks: 2,
			ExposureSettlementTag:        "finalized",
			ExposureSettlementMinBlocks:  5,
		},
		Replacement: evm.ReplacementDisabled,
		Submission:  evm.SubmissionPublic,
	}
}
