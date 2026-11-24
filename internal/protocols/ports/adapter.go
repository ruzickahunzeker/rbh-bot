// Package ports defines the narrow, side-effect-free ProtocolAdapter contract
// and pure value types. Implementations must never hold signers, broadcasters,
// nonce allocators, or database write handles.
package ports

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

type AllowanceModel string

const (
	AllowanceNone        AllowanceModel = "NONE"
	AllowanceERC20Direct AllowanceModel = "ERC20_DIRECT"
	AllowancePermit2     AllowanceModel = "PERMIT2"
)

type DeadlineCapability string

const (
	DeadlineApplicationTTLOnly   DeadlineCapability = "APPLICATION_TTL_ONLY"
	DeadlineContractDeadlineUnix DeadlineCapability = "CONTRACT_DEADLINE_UNIX"
)

type AdapterDescriptor struct {
	ChainID              uint64
	ProtocolID           string
	AdapterRevision      string
	PoolPhase            string
	QuoteAsset           string
	SupportsBuy          bool
	SupportsSell         bool
	AllowanceModel       AllowanceModel
	FeeOnTransferSupport bool
	DeadlineCapability   DeadlineCapability
}

type ResolvedRoute struct {
	ChainID            uint64
	ProtocolID         string
	AdapterRevision    string
	DeploymentRevision string
	PoolPhase          string
	Token              common.Address
	Factory            common.Address
	PoolOrCurve        common.Address
	RouterOrSpender    common.Address
	RuntimeCodeHash    common.Hash
	ProxyImplAddress   common.Address // zero for non-proxy contracts
	QuoteBlockNumber   uint64
	QuoteBlockHash     common.Hash
}

type QuoteRequest struct {
	Route          ResolvedRoute
	Side           Side
	AmountInRaw    *big.Int
	SlippageBps    uint64
	Recipient      common.Address
	ContractExpiry int64 // 0 when APPLICATION_TTL_ONLY
}

type QuoteResult struct {
	ExpectedOutRaw   *big.Int
	MinOutRaw        *big.Int
	FeeBps           uint64
	EstimatedGas     uint64
	QuoteBlockNumber uint64
	QuoteBlockHash   common.Hash
}

type UnsignedTx struct {
	To               common.Address
	Value            *big.Int
	Calldata         []byte
	Spender          common.Address
	ContractDeadline int64
	Summary          string
}

type DecodedEffects struct {
	WalletAddress      common.Address
	TokenAddress       common.Address
	Side               Side
	NetNativeDeltaWei  *big.Int // signed net change for wallet (excluding gas)
	NetTokenDeltaRaw   *big.Int // signed net ERC-20 transfer delta for wallet (fee-on-transfer accurate)
	EvidenceComplete   bool
	CanonicalBlockHash common.Hash
}

// ReadOnlyRPC is the narrow read-only query surface allowed inside protocol adapters.
type ReadOnlyRPC interface {
	CallContractAt(ctx context.Context, to common.Address, data []byte, blockHash common.Hash) ([]byte, error)
	CodeAt(ctx context.Context, addr common.Address, blockHash common.Hash) ([]byte, error)
}

// ProtocolAdapter encapsulates protocol-specific route resolution, quoting,
// unsigned calldata construction, and read-only receipt effect decoding.
type ProtocolAdapter interface {
	Describe() AdapterDescriptor
	Resolve(ctx context.Context, rpc ReadOnlyRPC, token common.Address, blockNumber uint64, blockHash common.Hash) (ResolvedRoute, error)
	Quote(ctx context.Context, rpc ReadOnlyRPC, req QuoteRequest) (QuoteResult, error)
	BuildUnsigned(req QuoteRequest, quote QuoteResult) (UnsignedTx, error)
	DecodeEffects(route ResolvedRoute, wallet common.Address, side Side, receipt *types.Receipt) (DecodedEffects, error)
}
