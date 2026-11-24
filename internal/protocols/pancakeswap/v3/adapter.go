// Package v3 implements a pure, side-effect-free ProtocolAdapter for BSC PancakeSwap V3
// (chain_id=56) with Factory getPool resolution, QuoterV2 block-pinned quoting,
// SmartRouter exactInputSingle calldata construction, and net ERC-20/WBNB receipt decoding.
package v3

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

const (
	ProtocolID      = "pancake-v3"
	AdapterRevision = "pancake-v3-bsc-r1"
)

// DefaultFeeTiers lists canonical PancakeSwap V3 fee tiers in preference order (0.25%, 0.05%, 1%, 0.01%).
var DefaultFeeTiers = []uint32{2500, 500, 10000, 100}

const pancakeV3FactoryABIJSON = `[
  {"name":"getPool","type":"function","stateMutability":"view","inputs":[{"name":"tokenA","type":"address"},{"name":"tokenB","type":"address"},{"name":"fee","type":"uint24"}],"outputs":[{"name":"pool","type":"address"}]}
]`

const pancakeV3QuoterV2ABIJSON = `[
  {"name":"quoteExactInputSingle","type":"function","stateMutability":"nonpayable","inputs":[{"name":"params","type":"tuple","components":[{"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},{"name":"amountIn","type":"uint256"},{"name":"fee","type":"uint24"},{"name":"sqrtPriceLimitX96","type":"uint160"}]}],"outputs":[{"name":"amountOut","type":"uint256"},{"name":"sqrtPriceX96After","type":"uint160"},{"name":"initializedTicksCrossed","type":"uint32"},{"name":"gasEstimate","type":"uint256"}]}
]`

const pancakeV3RouterABIJSON = `[
  {"name":"exactInputSingle","type":"function","stateMutability":"payable","inputs":[{"name":"params","type":"tuple","components":[{"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},{"name":"fee","type":"uint24"},{"name":"recipient","type":"address"},{"name":"amountIn","type":"uint256"},{"name":"amountOutMinimum","type":"uint256"},{"name":"sqrtPriceLimitX96","type":"uint160"}]}],"outputs":[{"name":"amountOut","type":"uint256"}]},
  {"name":"unwrapWETH9","type":"function","stateMutability":"payable","inputs":[{"name":"amountMinimum","type":"uint256"},{"name":"recipient","type":"address"}],"outputs":[]},
  {"name":"multicall","type":"function","stateMutability":"payable","inputs":[{"name":"deadline","type":"uint256"},{"name":"data","type":"bytes[]"}],"outputs":[{"name":"results","type":"bytes[]"}]}
]`

var (
	factoryABI          = mustParseABI(pancakeV3FactoryABIJSON)
	quoterABI           = mustParseABI(pancakeV3QuoterV2ABIJSON)
	routerABI           = mustParseABI(pancakeV3RouterABIJSON)
	erc20TransferTopic  = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	wbnbDepositTopic    = crypto.Keccak256Hash([]byte("Deposit(address,uint256)"))
	wbnbWithdrawalTopic = crypto.Keccak256Hash([]byte("Withdrawal(address,uint256)"))
	// smartRouterAddressThis is Constants.ADDRESS_THIS (address(2)) used by PancakeSwap V3 SmartRouter
	// when routing token->WBNB output into the router prior to unwrapWETH9.
	smartRouterAddressThis = common.HexToAddress("0x0000000000000000000000000000000000000002")
)

func mustParseABI(src string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(src))
	if err != nil {
		panic(err)
	}
	return parsed
}

type quoteExactInputSingleParams struct {
	TokenIn           common.Address
	TokenOut          common.Address
	AmountIn          *big.Int
	Fee               *big.Int
	SqrtPriceLimitX96 *big.Int
}

type exactInputSingleParams struct {
	TokenIn           common.Address
	TokenOut          common.Address
	Fee               *big.Int
	Recipient         common.Address
	AmountIn          *big.Int
	AmountOutMinimum  *big.Int
	SqrtPriceLimitX96 *big.Int
}

type Adapter struct {
	chainID uint64
	factory common.Address
	quoter  common.Address
	router  common.Address
	wbnb    common.Address
	feeTier uint32
}

func NewAdapter() *Adapter {
	return &Adapter{
		chainID: bsc.ChainID,
		factory: bsc.PancakeV3Factory,
		quoter:  bsc.PancakeV3QuoterV2,
		router:  bsc.PancakeV3SmartRouter,
		wbnb:    bsc.WBNBAddress,
		feeTier: 2500,
	}
}

func (a *Adapter) Describe() ports.AdapterDescriptor {
	return ports.AdapterDescriptor{
		ChainID:              a.chainID,
		ProtocolID:           ProtocolID,
		AdapterRevision:      AdapterRevision,
		PoolPhase:            "DEX_V3",
		QuoteAsset:           "BNB",
		SupportsBuy:          true,
		SupportsSell:         true,
		AllowanceModel:       ports.AllowanceERC20Direct,
		FeeOnTransferSupport: false, // V3 pools do not support fee-on-transfer tokens
		DeadlineCapability:   ports.DeadlineContractDeadlineUnix,
	}
}

// Resolve queries PancakeSwap V3 Factory.getPool across canonical fee tiers pinned to blockHash.
func (a *Adapter) Resolve(ctx context.Context, rpc ports.ReadOnlyRPC, token common.Address, blockNumber uint64, blockHash common.Hash) (ports.ResolvedRoute, error) {
	if rpc == nil || token == (common.Address{}) || token == a.wbnb || blockHash == (common.Hash{}) {
		return ports.ResolvedRoute{}, errors.New("invalid BSC pancake-v3 resolve inputs")
	}
	routerCode, err := rpc.CodeAt(ctx, a.router, blockHash)
	if err != nil || len(routerCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("pancake-v3 router has no runtime code")
	}
	tokenCode, err := rpc.CodeAt(ctx, token, blockHash)
	if err != nil || len(tokenCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("target token has no runtime code")
	}

	var poolAddr common.Address
	var chosenTier uint32
	for _, tier := range DefaultFeeTiers {
		data, err := factoryABI.Pack("getPool", a.wbnb, token, big.NewInt(int64(tier)))
		if err != nil {
			return ports.ResolvedRoute{}, err
		}
		raw, err := rpc.CallContractAt(ctx, a.factory, data, blockHash)
		if err != nil {
			continue
		}
		out, err := factoryABI.Unpack("getPool", raw)
		if err != nil || len(out) != 1 {
			continue
		}
		cand, ok := out[0].(common.Address)
		if ok && cand != (common.Address{}) {
			code, err := rpc.CodeAt(ctx, cand, blockHash)
			if err == nil && len(code) > 0 {
				poolAddr = cand
				chosenTier = tier
				break
			}
		}
	}
	if poolAddr == (common.Address{}) {
		return ports.ResolvedRoute{}, errors.New("pancake-v3 pool not found for WBNB/token")
	}
	return ports.ResolvedRoute{
		ChainID:            a.chainID,
		ProtocolID:         ProtocolID,
		AdapterRevision:    AdapterRevision,
		DeploymentRevision: fmt.Sprintf("pancakeswap-v3-bsc-fee-%d", chosenTier),
		PoolPhase:          "DEX_V3",
		Token:              token,
		Factory:            a.factory,
		PoolOrCurve:        poolAddr,
		RouterOrSpender:    a.router,
		RuntimeCodeHash:    crypto.Keccak256Hash(routerCode),
		QuoteBlockNumber:   blockNumber,
		QuoteBlockHash:     blockHash,
	}, nil
}

func feeTierFromRoute(route ports.ResolvedRoute, fallback uint32) uint32 {
	for _, tier := range DefaultFeeTiers {
		if strings.HasSuffix(route.DeploymentRevision, fmt.Sprintf("-fee-%d", tier)) {
			return tier
		}
	}
	return fallback
}

// Quote queries PancakeSwap V3 QuoterV2.quoteExactInputSingle pinned to QuoteBlockHash.
func (a *Adapter) Quote(ctx context.Context, rpc ports.ReadOnlyRPC, req ports.QuoteRequest) (ports.QuoteResult, error) {
	if rpc == nil || req.Route.Token == (common.Address{}) || req.Route.QuoteBlockHash == (common.Hash{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || req.SlippageBps > 10000 {
		return ports.QuoteResult{}, errors.New("invalid pancake-v3 quote request")
	}
	tokenIn, tokenOut := a.wbnb, req.Route.Token
	if req.Side == ports.SideSell {
		tokenIn, tokenOut = req.Route.Token, a.wbnb
	} else if req.Side != ports.SideBuy {
		return ports.QuoteResult{}, errors.New("unsupported side")
	}
	tier := feeTierFromRoute(req.Route, a.feeTier)
	params := quoteExactInputSingleParams{
		TokenIn:           tokenIn,
		TokenOut:          tokenOut,
		AmountIn:          new(big.Int).Set(req.AmountInRaw),
		Fee:               big.NewInt(int64(tier)),
		SqrtPriceLimitX96: big.NewInt(0),
	}
	calldata, err := quoterABI.Pack("quoteExactInputSingle", params)
	if err != nil {
		return ports.QuoteResult{}, err
	}
	raw, err := rpc.CallContractAt(ctx, a.quoter, calldata, req.Route.QuoteBlockHash)
	if err != nil {
		return ports.QuoteResult{}, fmt.Errorf("pancake-v3 quoter failed: %w", err)
	}
	unpacked, err := quoterABI.Unpack("quoteExactInputSingle", raw)
	if err != nil || len(unpacked) < 4 {
		return ports.QuoteResult{}, errors.New("invalid pancake-v3 quoter response")
	}
	amountOut, ok := unpacked[0].(*big.Int)
	if !ok || amountOut == nil || amountOut.Sign() <= 0 {
		return ports.QuoteResult{}, errors.New("pancake-v3 quoter returned zero output")
	}
	gasEst := uint64(260_000)
	if g, ok := unpacked[3].(*big.Int); ok && g != nil && g.IsUint64() && g.Uint64() > 0 {
		gasEst = g.Uint64() + 50_000
	}
	minOut := new(big.Int).Mul(amountOut, new(big.Int).SetUint64(10000-req.SlippageBps))
	minOut.Div(minOut, big.NewInt(10000))
	return ports.QuoteResult{
		ExpectedOutRaw:   new(big.Int).Set(amountOut),
		MinOutRaw:        minOut,
		FeeBps:           uint64(tier) / 100,
		EstimatedGas:     gasEst,
		QuoteBlockNumber: req.Route.QuoteBlockNumber,
		QuoteBlockHash:   req.Route.QuoteBlockHash,
	}, nil
}

// BuildUnsigned constructs BSC PancakeSwap V3 SmartRouter multicall(uint256 deadline, bytes[] data)
// wrapping IV3SwapRouter.exactInputSingle (selector 0x04e45aaf) and unwrapWETH9 on sells (U-P0-3).
func (a *Adapter) BuildUnsigned(req ports.QuoteRequest, quote ports.QuoteResult) (ports.UnsignedTx, error) {
	if req.Route.Token == (common.Address{}) || req.Recipient == (common.Address{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 {
		return ports.UnsignedTx{}, errors.New("incomplete pancake-v3 build inputs")
	}
	if req.ContractExpiry <= 0 {
		return ports.UnsignedTx{}, errors.New("pancake-v3 requires positive contract_deadline_unix")
	}
	if quote.MinOutRaw == nil || quote.MinOutRaw.Sign() < 0 {
		return ports.UnsignedTx{}, errors.New("invalid minOut")
	}
	tier := feeTierFromRoute(req.Route, a.feeTier)
	var (
		subcalls [][]byte
		value    *big.Int
	)
	switch req.Side {
	case ports.SideBuy:
		swapCall, err := routerABI.Pack("exactInputSingle", exactInputSingleParams{
			TokenIn:           a.wbnb,
			TokenOut:          req.Route.Token,
			Fee:               big.NewInt(int64(tier)),
			Recipient:         req.Recipient,
			AmountIn:          new(big.Int).Set(req.AmountInRaw),
			AmountOutMinimum:  new(big.Int).Set(quote.MinOutRaw),
			SqrtPriceLimitX96: big.NewInt(0),
		})
		if err != nil {
			return ports.UnsignedTx{}, err
		}
		subcalls = [][]byte{swapCall}
		value = new(big.Int).Set(req.AmountInRaw)
	case ports.SideSell:
		swapCall, err := routerABI.Pack("exactInputSingle", exactInputSingleParams{
			TokenIn:           req.Route.Token,
			TokenOut:          a.wbnb,
			Fee:               big.NewInt(int64(tier)),
			Recipient:         smartRouterAddressThis,
			AmountIn:          new(big.Int).Set(req.AmountInRaw),
			AmountOutMinimum:  new(big.Int).Set(quote.MinOutRaw),
			SqrtPriceLimitX96: big.NewInt(0),
		})
		if err != nil {
			return ports.UnsignedTx{}, err
		}
		unwrapCall, err := routerABI.Pack("unwrapWETH9", new(big.Int).Set(quote.MinOutRaw), req.Recipient)
		if err != nil {
			return ports.UnsignedTx{}, err
		}
		subcalls = [][]byte{swapCall, unwrapCall}
		value = big.NewInt(0)
	default:
		return ports.UnsignedTx{}, errors.New("unsupported side")
	}
	calldata, err := routerABI.Pack("multicall", big.NewInt(req.ContractExpiry), subcalls)
	if err != nil {
		return ports.UnsignedTx{}, err
	}
	return ports.UnsignedTx{
		To:               a.router,
		Value:            value,
		Calldata:         calldata,
		Spender:          a.router,
		ContractDeadline: req.ContractExpiry,
		Summary:          string(req.Side) + ":" + ProtocolID,
	}, nil
}

// DecodeEffects computes the wallet's exact net ERC-20 Transfer delta and native BNB delta via WBNB logs.
func (a *Adapter) DecodeEffects(route ports.ResolvedRoute, wallet common.Address, side ports.Side, receipt *types.Receipt) (ports.DecodedEffects, error) {
	if receipt == nil || wallet == (common.Address{}) || route.Token == (common.Address{}) {
		return ports.DecodedEffects{}, errors.New("invalid decode inputs")
	}
	netToken := big.NewInt(0)
	netNative := big.NewInt(0)
	netWBNB := big.NewInt(0)
	for _, lg := range receipt.Logs {
		if lg == nil || len(lg.Data) != 32 || len(lg.Topics) < 2 {
			continue
		}
		if lg.Address == route.Token && len(lg.Topics) == 3 && lg.Topics[0] == erc20TransferTopic {
			from := common.BytesToAddress(lg.Topics[1].Bytes())
			to := common.BytesToAddress(lg.Topics[2].Bytes())
			amt := new(big.Int).SetBytes(lg.Data)
			if to == wallet {
				netToken.Add(netToken, amt)
			}
			if from == wallet {
				netToken.Sub(netToken, amt)
			}
			continue
		}
		if lg.Address == a.wbnb && len(lg.Topics) == 3 && lg.Topics[0] == erc20TransferTopic {
			from := common.BytesToAddress(lg.Topics[1].Bytes())
			to := common.BytesToAddress(lg.Topics[2].Bytes())
			amt := new(big.Int).SetBytes(lg.Data)
			if to == wallet {
				netWBNB.Add(netWBNB, amt)
			}
			if from == wallet {
				netWBNB.Sub(netWBNB, amt)
			}
			continue
		}
		if lg.Address == a.wbnb && len(lg.Topics) == 2 {
			actor := common.BytesToAddress(lg.Topics[1].Bytes())
			amt := new(big.Int).SetBytes(lg.Data)
			if lg.Topics[0] == wbnbWithdrawalTopic && (actor == a.router || actor == wallet) {
				netNative.Add(netNative, amt)
			} else if lg.Topics[0] == wbnbDepositTopic && (actor == a.router || actor == wallet) {
				netNative.Sub(netNative, amt)
			}
		}
	}
	if netNative.Sign() == 0 && netWBNB.Sign() != 0 {
		netNative = netWBNB
	}
	return ports.DecodedEffects{
		WalletAddress:      wallet,
		TokenAddress:       route.Token,
		Side:               side,
		NetNativeDeltaWei:  netNative,
		NetTokenDeltaRaw:   netToken,
		EvidenceComplete:   receipt.Status == types.ReceiptStatusSuccessful,
		CanonicalBlockHash: receipt.BlockHash,
	}, nil
}
