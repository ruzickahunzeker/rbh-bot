// Package evm provides reusable multi-chain EVM profile types, fee bound math,
// and read-only Multicall3 + JSON-RPC batch snapshot utilities.
package evm

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

type SubmissionMode string

const (
	SubmissionPublic            SubmissionMode = "public"
	SubmissionPrivateOnly       SubmissionMode = "private_only"
	SubmissionPrivateThenPublic SubmissionMode = "private_then_public"
)

type ReplacementMode string

const (
	ReplacementDisabled ReplacementMode = "DISABLED"
	ReplacementEnabled  ReplacementMode = "ENABLED"
)

type AccountCapabilities struct {
	PureEOAOnly          bool // reject delegated EIP-7702 (0xef0100...) and contract wallets for signing
	DecodeEIP7702Blocks  bool // decode 0x04 SetCodeTx in chain/feed reads
}

type TxEnvelopeCapabilities struct {
	SupportsLegacy bool
	SupportsEIP1559 bool
}

type FeePolicy struct {
	ZeroBaseFee              bool // true on BSC (BEP-226)
	MinEffectiveGasPriceWei  *big.Int
	EstimateIncludesL1Fee    bool // true on Arbitrum Nitro / Robinhood
	SupportsNodeInterfaceL1  bool // true when Arbitrum NodeInterface 0x...C8 splits L2/L1
	NodeInterfaceAddress     common.Address
}

// FinalityPolicy separates execution continuity (allowing next Nonce / fast SELL)
// from exposure settlement (releasing unfinalized_native_outlay_wei).
type FinalityPolicy struct {
	ExecutionContinuityTag       string // e.g. "latest" (L2 sequencer canonical) or "safe" (BSC fast finality)
	ExecutionContinuityMinBlocks uint64
	ExposureSettlementTag        string // e.g. "finalized"
	ExposureSettlementMinBlocks  uint64
}

type ChainProfile struct {
	ChainID         uint64
	ChainSlug       string
	V1Aliases       []string
	NetworkKind     string
	ProfileRevision string
	NativeSymbol    string
	WrappedNative   common.Address
	Multicall3      common.Address
	Account         AccountCapabilities
	TxEnvelope      TxEnvelopeCapabilities
	Fee             FeePolicy
	Finality        FinalityPolicy
	Replacement     ReplacementMode
	Submission      SubmissionMode
}

// MatchesSlug returns true if raw matches ChainSlug or any V1 compatibility alias.
func (p ChainProfile) MatchesSlug(raw string) bool {
	if raw == p.ChainSlug {
		return true
	}
	for _, alias := range p.V1Aliases {
		if raw == alias {
			return true
		}
	}
	return false
}

// ComputeEffectiveGasPrice returns the chain-accurate effective gas price for a Type-2 tx.
// On BSC (ZeroBaseFee=true), EffectiveGasPriceForBSC = min(maxFee, maxPriorityFee).
// On standard EIP-1559 chains, effective = min(maxFee, baseFee + maxPriorityFee).
func (p ChainProfile) ComputeEffectiveGasPrice(baseFeeWei, maxFeeWei, maxTipWei *big.Int) (*big.Int, error) {
	if maxFeeWei == nil || maxFeeWei.Sign() <= 0 || maxTipWei == nil || maxTipWei.Sign() < 0 {
		return nil, errors.New("invalid fee parameters")
	}
	if maxTipWei.Cmp(maxFeeWei) > 0 {
		return nil, errors.New("maxPriorityFeePerGas exceeds maxFeePerGas")
	}
	var eff *big.Int
	if p.Fee.ZeroBaseFee {
		eff = new(big.Int).Set(maxTipWei)
		if eff.Cmp(maxFeeWei) > 0 {
			eff.Set(maxFeeWei)
		}
	} else {
		if baseFeeWei == nil || baseFeeWei.Sign() < 0 {
			return nil, errors.New("baseFee is required for non-zero baseFee chain")
		}
		sum := new(big.Int).Add(baseFeeWei, maxTipWei)
		if sum.Cmp(maxFeeWei) < 0 {
			eff = sum
		} else {
			eff = new(big.Int).Set(maxFeeWei)
		}
	}
	if p.Fee.MinEffectiveGasPriceWei != nil && eff.Cmp(p.Fee.MinEffectiveGasPriceWei) < 0 {
		return nil, errors.New("effective gas price below chain minimum floor")
	}
	return eff, nil
}

// ComputeNativeOutlayBound returns the worst-case native wei outlay:
// txValueWei + (l2GasLimit * maxFeePerGasWei) + unbundledL1DataFeeBoundWei.
func ComputeNativeOutlayBound(txValueWei *big.Int, l2GasLimit uint64, maxFeePerGasWei, unbundledL1DataFeeBoundWei *big.Int) (*big.Int, error) {
	if maxFeePerGasWei == nil || maxFeePerGasWei.Sign() <= 0 || l2GasLimit == 0 {
		return nil, errors.New("invalid gas bound inputs")
	}
	out := new(big.Int)
	if txValueWei != nil {
		if txValueWei.Sign() < 0 {
			return nil, errors.New("negative tx value")
		}
		out.Add(out, txValueWei)
	}
	l2Cost := new(big.Int).Mul(new(big.Int).SetUint64(l2GasLimit), maxFeePerGasWei)
	out.Add(out, l2Cost)
	if unbundledL1DataFeeBoundWei != nil {
		if unbundledL1DataFeeBoundWei.Sign() < 0 {
			return nil, errors.New("negative L1 data fee bound")
		}
		out.Add(out, unbundledL1DataFeeBoundWei)
	}
	return out, nil
}
