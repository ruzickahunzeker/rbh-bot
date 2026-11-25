// Package base defines the Base Mainnet (OP Stack L2, chain_id=8453) ChainProfile,
// OP Stack Ecotone/Fjord L1 Data Fee oracle quoting (0x4200...000F), and fee bound helpers.
package base

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

const (
	ChainID         uint64 = 8453
	ChainSlug              = "base"
	ProfileRevision        = "base-mainnet-v1"
)

var (
	WETHAddress           = common.HexToAddress("0x4200000000000000000000000000000000000006")
	OPGasPriceOracle      = common.HexToAddress("0x420000000000000000000000000000000000000F")
	Multicall3Address     = common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")
	UniswapV2Factory      = common.HexToAddress("0x8909Dc15e40173Ff4699343b6eB8132c65e18eC6")
	UniswapV2Router02     = common.HexToAddress("0x4752ba5DBc23f44D87826276BF6Fd6b1C372aD24")
	UniswapV3Factory      = common.HexToAddress("0x33128a8fC17869897dcE68Ed026d694621f6FDfD")
	UniswapV3QuoterV2     = common.HexToAddress("0x3d4e44Eb1374240CE5F1B871ab261CD16335B76a")
	UniswapV3SwapRouter02 = common.HexToAddress("0x2626664c2603336E57B271c5C0b26F421741e481")

	// DefaultMinGasPriceWei is 0.001 gwei (Base L2 sequencer minimum priority floor).
	DefaultMinGasPriceWei = big.NewInt(1_000_000)

	gasOracleABI = mustParseOracleABI()
)

const opGasPriceOracleABIJSON = `[
  {"name":"getL1Fee","type":"function","stateMutability":"view","inputs":[{"name":"_data","type":"bytes"}],"outputs":[{"name":"","type":"uint256"}]}
]`

func mustParseOracleABI() abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(opGasPriceOracleABIJSON))
	if err != nil {
		panic(err)
	}
	return parsed
}

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
			DecodeEIP7702Blocks: true, // Base Pectra / EIP-7702 support
		},
		TxEnvelope: evm.TxEnvelopeCapabilities{
			SupportsLegacy:  true,
			SupportsEIP1559: true,
		},
		Fee: evm.FeePolicy{
			ZeroBaseFee:             false,
			MinEffectiveGasPriceWei: new(big.Int).Set(DefaultMinGasPriceWei),
			EstimateIncludesL1Fee:   false, // OP Stack requires adding GasPriceOracle.getL1Fee separately!
		},
		Finality: evm.FinalityPolicy{
			ExecutionContinuityTag:       "latest", // Base sequencer canonical L2 inclusion
			ExecutionContinuityMinBlocks: 1,
			ExposureSettlementTag:        "finalized", // L1 finalized batch settlement
			ExposureSettlementMinBlocks:  0,
		},
		Replacement: evm.ReplacementDisabled,
		Submission:  evm.SubmissionPublic,
	}
}

// EstimateL1DataFee queries OP Stack GasPriceOracle.getL1Fee(rawUnsignedTxBytes) pinned to blockHash
// and applies a safety buffer (in bps, e.g. 2000 = +20%) to bound worst-case L1 blob fee spikes.
func EstimateL1DataFee(ctx context.Context, rpc ports.ReadOnlyRPC, rawTxData []byte, blockHash common.Hash, bufferBps uint64) (*big.Int, error) {
	if rpc == nil || len(rawTxData) == 0 || blockHash == (common.Hash{}) || bufferBps > 10000 {
		return nil, errors.New("invalid OP Stack L1 data fee inputs")
	}
	calldata, err := gasOracleABI.Pack("getL1Fee", rawTxData)
	if err != nil {
		return nil, err
	}
	raw, err := rpc.CallContractAt(ctx, OPGasPriceOracle, calldata, blockHash)
	if err != nil {
		return nil, fmt.Errorf("OP Stack GasPriceOracle.getL1Fee failed: %w", err)
	}
	unpacked, err := gasOracleABI.Unpack("getL1Fee", raw)
	if err != nil || len(unpacked) != 1 {
		return nil, errors.New("invalid GasPriceOracle.getL1Fee response")
	}
	l1Fee, ok := unpacked[0].(*big.Int)
	if !ok || l1Fee == nil || l1Fee.Sign() < 0 {
		return nil, errors.New("negative or nil OP Stack L1 fee")
	}
	buffered := new(big.Int).Mul(l1Fee, new(big.Int).SetUint64(10000+bufferBps))
	buffered.Div(buffered, big.NewInt(10000))
	return buffered, nil
}
