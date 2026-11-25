// Package v3 implements a pure, side-effect-free ProtocolAdapter for Base Mainnet
// Uniswap V3 (chain_id=8453) using SwapRouter02 multicall(deadline, [exactInputSingle, unwrapWETH9]).
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
	basechain "github.com/ruzickahunzeker/rbh-bot/internal/chains/base"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

const (
	ProtocolID      = "uniswap-v3"
	ProtocolIDBSC   = "uniswap-v3-bsc"
	AdapterRevision = "uniswap-v3-base-r1"
)

// DefaultFeeTiers lists canonical Uniswap V3 fee tiers on Base (0.3%, 0.05%, 1%, 0.01%).
var DefaultFeeTiers = []uint32{3000, 500, 10000, 100}

const uniV3FactoryABIJSON = `[
  {"name":"getPool","type":"function","stateMutability":"view","inputs":[{"name":"tokenA","type":"address"},{"name":"tokenB","type":"address"},{"name":"fee","type":"uint24"}],"outputs":[{"name":"pool","type":"address"}]}
]`

const uniV3QuoterV2ABIJSON = `[
  {"name":"quoteExactInputSingle","type":"function","stateMutability":"nonpayable","inputs":[{"name":"params","type":"tuple","components":[{"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},{"name":"amountIn","type":"uint256"},{"name":"fee","type":"uint24"},{"name":"sqrtPriceLimitX96","type":"uint160"}]}],"outputs":[{"name":"amountOut","type":"uint256"},{"name":"sqrtPriceX96After","type":"uint160"},{"name":"initializedTicksCrossed","type":"uint32"},{"name":"gasEstimate","type":"uint256"}]}
]`

const uniV3SwapRouter02ABIJSON = `[
  {"name":"exactInputSingle","type":"function","stateMutability":"payable","inputs":[{"name":"params","type":"tuple","components":[{"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},{"name":"fee","type":"uint24"},{"name":"recipient","type":"address"},{"name":"amountIn","type":"uint256"},{"name":"amountOutMinimum","type":"uint256"},{"name":"sqrtPriceLimitX96","type":"uint160"}]}],"outputs":[{"name":"amountOut","type":"uint256"}]},
  {"name":"unwrapWETH9","type":"function","stateMutability":"payable","inputs":[{"name":"amountMinimum","type":"uint256"},{"name":"recipient","type":"address"}],"outputs":[]},
  {"name":"multicall","type":"function","stateMutability":"payable","inputs":[{"name":"deadline","type":"uint256"},{"name":"data","type":"bytes[]"}],"outputs":[{"name":"results","type":"bytes[]"}]}
]`

var (
	factoryABI          = mustParseABI(uniV3FactoryABIJSON)
	quoterABI           = mustParseABI(uniV3QuoterV2ABIJSON)
	routerABI           = mustParseABI(uniV3SwapRouter02ABIJSON)
	erc20TransferTopic  = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	wethDepositTopic    = crypto.Keccak256Hash([]byte("Deposit(address,uint256)"))
	wethWithdrawalTopic = crypto.Keccak256Hash([]byte("Withdrawal(address,uint256)"))
	routerAddressThis   = common.HexToAddress("0x0000000000000000000000000000000000000002")
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
	chainID    uint64
	protocolID string
	quoteAsset string
	factory    common.Address
	quoter     common.Address
	router     common.Address
	weth       common.Address
	feeTier    uint32
}

func NewBaseAdapter() *Adapter {
	return &Adapter{
		chainID:    basechain.ChainID,
		protocolID: ProtocolID,
		quoteAsset: "ETH",
		factory:    basechain.UniswapV3Factory,
		quoter:     basechain.UniswapV3QuoterV2,
		router:     basechain.UniswapV3SwapRouter02,
		weth:       basechain.WETHAddress,
		feeTier:    3000,
	}
}

// NewBSCAdapter constructs a Uniswap V3 adapter for BNB Smart Chain (chain_id=56).
func NewBSCAdapter() *Adapter {
	return &Adapter{
		chainID:    bsc.ChainID,
		protocolID: ProtocolIDBSC,
		quoteAsset: "BNB",
		factory:    bsc.UniswapV3Factory,
		quoter:     bsc.UniswapV3QuoterV2,
		router:     bsc.UniswapV3SwapRouter02,
		weth:       bsc.WBNBAddress,
		feeTier:    3000,
	}
}

func (a *Adapter) Describe() ports.AdapterDescriptor {
	pid := a.protocolID
	if pid == "" {
		pid = ProtocolID
	}
	qa := a.quoteAsset
	if qa == "" {
		qa = "ETH"
	}
	return ports.AdapterDescriptor{
		ChainID:              a.chainID,
		ProtocolID:           pid,
		AdapterRevision:      AdapterRevision,
		PoolPhase:            "DEX_V3",
		QuoteAsset:           qa,
		SupportsBuy:          true,
		SupportsSell:         true,
		AllowanceModel:       ports.AllowanceERC20Direct,
		FeeOnTransferSupport: false,
		DeadlineCapability:   ports.DeadlineContractDeadlineUnix,
	}
}

func (a *Adapter) Resolve(ctx context.Context, rpc ports.ReadOnlyRPC, token common.Address, blockNumber uint64, blockHash common.Hash) (ports.ResolvedRoute, error) {
	if rpc == nil || token == (common.Address{}) || token == a.weth || blockHash == (common.Hash{}) {
		return ports.ResolvedRoute{}, errors.New("invalid Base uniswap-v3 resolve inputs")
	}
	routerCode, err := rpc.CodeAt(ctx, a.router, blockHash)
	if err != nil || len(routerCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("uniswap-v3 router has no runtime code")
	}
	tokenCode, err := rpc.CodeAt(ctx, token, blockHash)
	if err != nil || len(tokenCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("target token has no runtime code")
	}
	var poolAddr common.Address
	var chosenTier uint32
	for _, tier := range DefaultFeeTiers {
		data, err := factoryABI.Pack("getPool", a.weth, token, big.NewInt(int64(tier)))
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
		return ports.ResolvedRoute{}, errors.New("uniswap-v3 pool not found for WETH/token")
	}
	return ports.ResolvedRoute{
		ChainID:            a.chainID,
		ProtocolID:         a.Describe().ProtocolID,
		AdapterRevision:    AdapterRevision,
		DeploymentRevision: fmt.Sprintf("%s-fee-%d", a.Describe().ProtocolID, chosenTier),
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

func (a *Adapter) Quote(ctx context.Context, rpc ports.ReadOnlyRPC, req ports.QuoteRequest) (ports.QuoteResult, error) {
	if rpc == nil || req.Route.Token == (common.Address{}) || req.Route.QuoteBlockHash == (common.Hash{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || req.SlippageBps > 10000 {
		return ports.QuoteResult{}, errors.New("invalid uniswap-v3 quote request")
	}
	tokenIn, tokenOut := a.weth, req.Route.Token
	if req.Side == ports.SideSell {
		tokenIn, tokenOut = req.Route.Token, a.weth
	} else if req.Side != ports.SideBuy {
		return ports.QuoteResult{}, errors.New("unsupported side")
	}
	tier := feeTierFromRoute(req.Route, a.feeTier)
	calldata, err := quoterABI.Pack("quoteExactInputSingle", quoteExactInputSingleParams{
		TokenIn:           tokenIn,
		TokenOut:          tokenOut,
		AmountIn:          new(big.Int).Set(req.AmountInRaw),
		Fee:               big.NewInt(int64(tier)),
		SqrtPriceLimitX96: big.NewInt(0),
	})
	if err != nil {
		return ports.QuoteResult{}, err
	}
	raw, err := rpc.CallContractAt(ctx, a.quoter, calldata, req.Route.QuoteBlockHash)
	if err != nil {
		return ports.QuoteResult{}, fmt.Errorf("uniswap-v3 quoter failed: %w", err)
	}
	unpacked, err := quoterABI.Unpack("quoteExactInputSingle", raw)
	if err != nil || len(unpacked) < 4 {
		return ports.QuoteResult{}, errors.New("invalid uniswap-v3 quoter response")
	}
	amountOut, ok := unpacked[0].(*big.Int)
	if !ok || amountOut == nil || amountOut.Sign() <= 0 {
		return ports.QuoteResult{}, errors.New("uniswap-v3 quoter returned zero output")
	}
	gasEst := uint64(250_000)
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

func (a *Adapter) BuildUnsigned(req ports.QuoteRequest, quote ports.QuoteResult) (ports.UnsignedTx, error) {
	if req.Route.Token == (common.Address{}) || req.Recipient == (common.Address{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 {
		return ports.UnsignedTx{}, errors.New("incomplete uniswap-v3 build inputs")
	}
	if req.ContractExpiry <= 0 {
		return ports.UnsignedTx{}, errors.New("uniswap-v3 requires positive contract_deadline_unix")
	}
	if quote.MinOutRaw == nil || quote.MinOutRaw.Sign() < 0 {
		return ports.UnsignedTx{}, errors.New("invalid minOut")
	}
	tier := feeTierFromRoute(req.Route, a.feeTier)
	var subcalls [][]byte
	var value *big.Int
	switch req.Side {
	case ports.SideBuy:
		swapCall, err := routerABI.Pack("exactInputSingle", exactInputSingleParams{
			TokenIn:           a.weth,
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
			TokenOut:          a.weth,
			Fee:               big.NewInt(int64(tier)),
			Recipient:         routerAddressThis,
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

func (a *Adapter) DecodeEffects(route ports.ResolvedRoute, wallet common.Address, side ports.Side, receipt *types.Receipt) (ports.DecodedEffects, error) {
	if receipt == nil || wallet == (common.Address{}) || route.Token == (common.Address{}) {
		return ports.DecodedEffects{}, errors.New("invalid decode inputs")
	}
	netToken := big.NewInt(0)
	netNative := big.NewInt(0)
	netWETH := big.NewInt(0)
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
		if lg.Address == a.weth && len(lg.Topics) == 3 && lg.Topics[0] == erc20TransferTopic {
			from := common.BytesToAddress(lg.Topics[1].Bytes())
			to := common.BytesToAddress(lg.Topics[2].Bytes())
			amt := new(big.Int).SetBytes(lg.Data)
			if to == wallet {
				netWETH.Add(netWETH, amt)
			}
			if from == wallet {
				netWETH.Sub(netWETH, amt)
			}
			continue
		}
		if lg.Address == a.weth && len(lg.Topics) == 2 {
			actor := common.BytesToAddress(lg.Topics[1].Bytes())
			amt := new(big.Int).SetBytes(lg.Data)
			if lg.Topics[0] == wethWithdrawalTopic && (actor == a.router || actor == wallet) {
				netNative.Add(netNative, amt)
			} else if lg.Topics[0] == wethDepositTopic && (actor == a.router || actor == wallet) {
				netNative.Sub(netNative, amt)
			}
		}
	}
	if netNative.Sign() == 0 && netWETH.Sign() != 0 {
		netNative = netWETH
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
