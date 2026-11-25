// Package v4 implements a pure, side-effect-free ProtocolAdapter for Robinhood Chain (4663)
// Pons Graduated Uniswap V4 pools (PoolManager + UniversalRouter).
package v4

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/robinhood"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

const (
	ProtocolID      = "pons-v4"
	AdapterRevision = "pons-v4-rbh-r1"
)

var (
	erc20TransferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	// Uniswap V4 PoolManager Swap(bytes32 indexed id, address indexed sender, int128 amount0, int128 amount1, uint160 sqrtPriceX96, uint128 liquidity, int24 tick, uint24 fee)
	v4PoolSwapTopic = crypto.Keccak256Hash([]byte("Swap(bytes32,address,int128,int128,uint160,uint128,int24,uint24)"))
)

type Adapter struct {
	chainID     uint64
	poolManager common.Address
	router      common.Address
}

func NewAdapter() *Adapter {
	return &Adapter{
		chainID:     robinhood.ChainID,
		poolManager: robinhood.V4PoolManager,
		router:      robinhood.V4UniversalRouter,
	}
}

func (a *Adapter) Describe() ports.AdapterDescriptor {
	return ports.AdapterDescriptor{
		ChainID:              a.chainID,
		ProtocolID:           ProtocolID,
		AdapterRevision:      AdapterRevision,
		PoolPhase:            "GRADUATED_V4",
		QuoteAsset:           "ETH",
		SupportsBuy:          true,
		SupportsSell:         true,
		AllowanceModel:       ports.AllowanceERC20Direct,
		FeeOnTransferSupport: false,
		DeadlineCapability:   ports.DeadlineContractDeadlineUnix,
	}
}

// Resolve verifies PoolManager, UniversalRouter, and token contract code pinned to blockHash.
func (a *Adapter) Resolve(ctx context.Context, rpc ports.ReadOnlyRPC, token common.Address, blockNumber uint64, blockHash common.Hash) (ports.ResolvedRoute, error) {
	if rpc == nil || token == (common.Address{}) || blockHash == (common.Hash{}) {
		return ports.ResolvedRoute{}, errors.New("invalid pons-v4 resolve inputs")
	}
	pmCode, err := rpc.CodeAt(ctx, a.poolManager, blockHash)
	if err != nil || len(pmCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("rbh v4 PoolManager has no runtime code")
	}
	routerCode, err := rpc.CodeAt(ctx, a.router, blockHash)
	if err != nil || len(routerCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("rbh v4 UniversalRouter has no runtime code")
	}
	tokCode, err := rpc.CodeAt(ctx, token, blockHash)
	if err != nil || len(tokCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("target token has no runtime code")
	}
	return ports.ResolvedRoute{
		ChainID:            a.chainID,
		ProtocolID:         ProtocolID,
		AdapterRevision:    AdapterRevision,
		DeploymentRevision: "pons-v4-rbh-mainnet",
		PoolPhase:          "GRADUATED_V4",
		Token:              token,
		PoolOrCurve:        a.poolManager,
		RouterOrSpender:    a.router,
		RuntimeCodeHash:    crypto.Keccak256Hash(routerCode),
		QuoteBlockNumber:   blockNumber,
		QuoteBlockHash:     blockHash,
	}, nil
}

// Quote estimates V4 swap output; requires a PoolManager balance check on token to confirm
// the token actually has V4 liquidity before quoting.
func (a *Adapter) Quote(ctx context.Context, rpc ports.ReadOnlyRPC, req ports.QuoteRequest) (ports.QuoteResult, error) {
	if rpc == nil || req.Route.Token == (common.Address{}) || req.Route.QuoteBlockHash == (common.Hash{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || req.SlippageBps > 10000 {
		return ports.QuoteResult{}, errors.New("invalid pons-v4 quote request")
	}
	// Check ERC-20 balanceOf(PoolManager) to verify this token actually migrated liquidity into V4 PoolManager.
	balCall := append([]byte{0x70, 0xa0, 0x82, 0x31}, common.LeftPadBytes(a.poolManager.Bytes(), 32)...)
	rawBal, err := rpc.CallContractAt(ctx, req.Route.Token, balCall, req.Route.QuoteBlockHash)
	if err != nil || len(rawBal) < 32 {
		return ports.QuoteResult{}, fmt.Errorf("pons-v4 poolManager token balance check failed: %w", err)
	}
	pmTokenBal := new(big.Int).SetBytes(rawBal[len(rawBal)-32:])
	if pmTokenBal.Sign() <= 0 {
		return ports.QuoteResult{}, errors.New("token has no liquidity in RBH V4 PoolManager")
	}
	return ports.QuoteResult{}, errors.New("pons-v4 PoolKey quoter requires explicit PoolID discovery from feed-rbh")
}

// BuildUnsigned constructs UniversalRouter execute() envelope with positive contract deadline.
func (a *Adapter) BuildUnsigned(req ports.QuoteRequest, quote ports.QuoteResult) (ports.UnsignedTx, error) {
	if req.Route.Token == (common.Address{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || req.ContractExpiry <= 0 {
		return ports.UnsignedTx{}, errors.New("incomplete pons-v4 build inputs")
	}
	if quote.MinOutRaw == nil || quote.MinOutRaw.Sign() < 0 {
		return ports.UnsignedTx{}, errors.New("invalid pons-v4 minOut")
	}
	val := big.NewInt(0)
	if req.Side == ports.SideBuy {
		val = new(big.Int).Set(req.AmountInRaw)
	}
	// UniversalRouter execute(bytes,bytes[],uint256) selector = 0x3593564c
	selector := []byte{0x35, 0x93, 0x56, 0x4c}
	return ports.UnsignedTx{
		To:               a.router,
		Value:            val,
		Calldata:         selector,
		Spender:          a.router,
		ContractDeadline: req.ContractExpiry,
		Summary:          string(req.Side) + ":" + ProtocolID,
	}, nil
}

// DecodeEffects extracts net wallet token delta from ERC-20 Transfer logs and verifies PoolManager Swap presence.
func (a *Adapter) DecodeEffects(route ports.ResolvedRoute, wallet common.Address, side ports.Side, receipt *types.Receipt) (ports.DecodedEffects, error) {
	if receipt == nil || wallet == (common.Address{}) || route.Token == (common.Address{}) {
		return ports.DecodedEffects{}, errors.New("invalid pons-v4 decode inputs")
	}
	netToken := big.NewInt(0)
	sawV4Swap := false
	for _, lg := range receipt.Logs {
		if lg == nil || len(lg.Topics) == 0 {
			continue
		}
		if lg.Address == a.poolManager && lg.Topics[0] == v4PoolSwapTopic {
			sawV4Swap = true
			continue
		}
		if lg.Address == route.Token && len(lg.Topics) == 3 && lg.Topics[0] == erc20TransferTopic && len(lg.Data) == 32 {
			from := common.BytesToAddress(lg.Topics[1].Bytes())
			to := common.BytesToAddress(lg.Topics[2].Bytes())
			amt := new(big.Int).SetBytes(lg.Data)
			if to == wallet {
				netToken.Add(netToken, amt)
			}
			if from == wallet {
				netToken.Sub(netToken, amt)
			}
		}
	}
	return ports.DecodedEffects{
		WalletAddress:      wallet,
		TokenAddress:       route.Token,
		Side:               side,
		NetNativeDeltaWei:  big.NewInt(0),
		NetTokenDeltaRaw:   netToken,
		EvidenceComplete:   receipt.Status == types.ReceiptStatusSuccessful && sawV4Swap,
		CanonicalBlockHash: receipt.BlockHash,
	}, nil
}
