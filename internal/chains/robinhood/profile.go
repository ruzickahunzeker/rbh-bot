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

// Robinhood Chain (4663) Uniswap V3, V4, and Flap canonical contracts (verified against rbh-trade-sdk).
var (
	WETHAddress       = common.HexToAddress("0x0Bd7D308f8E1639FAb988df18A8011F41EAcAD73")
	V3Factory         = common.HexToAddress("0x1f7d7550B1b028f7571E69A784071F0205FD2EfA")
	V3QuoterV2        = common.HexToAddress("0x33e885ed0ec9bf04ecfb19341582aadcb4c8a9e7")
	V3SwapRouter02    = common.HexToAddress("0xCaf681a66D020601342297493863E78C959E5cb2")
	V4PoolManager     = common.HexToAddress("0x8366a39CC670B4001A1121B8F6A443A643e40951")
	V4Quoter          = common.HexToAddress("0x8dc178efb8111bb0973dd9d722ebeff267c98f94")
	V4UniversalRouter = common.HexToAddress("0x8876789976dEcBfCbBbe364623C63652db8C0904")
	PonsMemeHook      = common.HexToAddress("0xE5e702641Ea86F4ae6cC3cDaeD2B886f976Be044")
	BagsHook          = common.HexToAddress("0x2380aBf72C17aABAb76480244759AC7E2932EEcC")
	O1Hook            = common.HexToAddress("0x0310cFEbE1D7A69f2414f6595bBe9d17c5342aCc")
	FlapPortal        = common.HexToAddress("0x26605f322f7ff986f381bB9a6e3f5DAb0BeaeB09")
)

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
