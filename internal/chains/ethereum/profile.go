// Package ethereum defines the Ethereum Mainnet (chain_id=1) ChainProfile,
// Flashbots/MEV-Blocker private_only protection policy, and canonical Uniswap V2/V3 contracts.
package ethereum

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
)

const (
	ChainID         uint64 = 1
	ChainSlug              = "eth"
	ProfileRevision        = "ethereum-mainnet-v1"
)

var (
	WETHAddress           = common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")
	Multicall3Address     = common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")
	UniswapV2Factory      = common.HexToAddress("0x5C69bEe701ef814a2B6a3EDD4B1652CB9cc5aA6f")
	UniswapV2Router02     = common.HexToAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D")
	UniswapV3Factory      = common.HexToAddress("0x1F98431c8aD98523631AE4a59f267346ea31F984")
	UniswapV3QuoterV2     = common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e")
	UniswapV3SwapRouter02 = common.HexToAddress("0x68b3465833fb72A70ecDF485E0e4C7bD8665Fc45")
)

func Profile() evm.ChainProfile {
	return evm.ChainProfile{
		ChainID:         ChainID,
		ChainSlug:       ChainSlug,
		NetworkKind:     "mainnet",
		ProfileRevision: ProfileRevision,
		NativeSymbol:    "ETH",
		WrappedNative:   WETHAddress,
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
			MinEffectiveGasPriceWei: big.NewInt(1_000_000_000), // 1 gwei floor
		},
		Finality: evm.FinalityPolicy{
			ExecutionContinuityTag:       "safe",
			ExecutionContinuityMinBlocks: 2,
			ExposureSettlementTag:        "finalized",
			ExposureSettlementMinBlocks:  32,
		},
		Replacement: evm.ReplacementDisabled,
		Submission:  evm.SubmissionPrivateOnly, // Flashbots Protect / MEV-Blocker
	}
}
