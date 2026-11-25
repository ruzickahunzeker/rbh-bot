// Package v4 implements a complete, pure, side-effect-free ProtocolAdapter for Uniswap V4
// across Ethereum (1), BSC (56), Base (8453), and Robinhood Chain (4663), using V4Quoter
// quoteExactInputSingle discovery and UniversalRouter CommandV4Swap (0x10) encoding.
package v4

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	basechain "github.com/ruzickahunzeker/rbh-bot/internal/chains/base"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/ethereum"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/robinhood"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

const (
	ProtocolIDETH  = "uniswap-v4-eth"
	ProtocolIDBSC  = "uniswap-v4-bsc"
	ProtocolIDBase = "uniswap-v4-base"
	ProtocolIDRBH  = "uniswap-v4-rbh"
	AdapterRevision = "uniswap-v4-r1"

	commandV4Swap           byte = 0x10
	actionSwapExactInSingle byte = 0x06
	actionSettleAll         byte = 0x0c
	actionTakeAll           byte = 0x0f
)

const v4QuoterABIJSON = `[{
  "type": "function",
  "name": "quoteExactInputSingle",
  "stateMutability": "nonpayable",
  "inputs": [{
    "name": "params",
    "type": "tuple",
    "components": [
      {
        "name": "poolKey",
        "type": "tuple",
        "components": [
          {"name": "currency0", "type": "address"},
          {"name": "currency1", "type": "address"},
          {"name": "fee", "type": "uint24"},
          {"name": "tickSpacing", "type": "int24"},
          {"name": "hooks", "type": "address"}
        ]
      },
      {"name": "zeroForOne", "type": "bool"},
      {"name": "exactAmount", "type": "uint128"},
      {"name": "hookData", "type": "bytes"}
    ]
  }],
  "outputs": [
    {"name": "amountOut", "type": "uint256"},
    {"name": "gasEstimate", "type": "uint256"}
  ]
}]`

const universalRouterABIJSON = `[{
  "type": "function",
  "name": "execute",
  "stateMutability": "payable",
  "inputs": [
    {"name": "commands", "type": "bytes"},
    {"name": "inputs", "type": "bytes[]"},
    {"name": "deadline", "type": "uint256"}
  ],
  "outputs": []
}]`

var (
	quoterABI          = mustParseABI(v4QuoterABIJSON)
	universalRouterABI = mustParseABI(universalRouterABIJSON)
	v4PlanArgs         = mustArgs("bytes", "bytes[]")
	addrUintArgs       = mustArgs("address", "uint256")
	v4SwapActionArgs   = mustV4SwapActionArgs()
	maxUint128         = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))

	erc20TransferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	// V4 PoolManager Swap(bytes32 indexed id, address indexed sender, int128 amount0, int128 amount1, uint160 sqrtPriceX96, uint128 liquidity, int24 tick, uint24 fee)
	V4PoolSwapTopic = crypto.Keccak256Hash([]byte("Swap(bytes32,address,int128,int128,uint160,uint128,int24,uint24)"))
)

func mustParseABI(raw string) abi.ABI {
	p, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic(err)
	}
	return p
}

func mustArgs(names ...string) abi.Arguments {
	out := make(abi.Arguments, len(names))
	for i, n := range names {
		t, err := abi.NewType(n, "", nil)
		if err != nil {
			panic(err)
		}
		out[i] = abi.Argument{Type: t}
	}
	return out
}

func mustV4SwapActionArgs() abi.Arguments {
	t, err := abi.NewType("tuple", "ExactInputSingleParams", []abi.ArgumentMarshaling{
		{Name: "poolKey", Type: "tuple", Components: []abi.ArgumentMarshaling{
			{Name: "currency0", Type: "address"},
			{Name: "currency1", Type: "address"},
			{Name: "fee", Type: "uint24"},
			{Name: "tickSpacing", Type: "int24"},
			{Name: "hooks", Type: "address"},
		}},
		{Name: "zeroForOne", Type: "bool"},
		{Name: "amountIn", Type: "uint128"},
		{Name: "amountOutMinimum", Type: "uint128"},
		{Name: "hookData", Type: "bytes"},
	})
	if err != nil {
		panic(err)
	}
	return abi.Arguments{{Type: t}}
}

type poolKeyABI struct {
	Currency0   common.Address
	Currency1   common.Address
	Fee         *big.Int
	TickSpacing *big.Int
	Hooks       common.Address
}

type quoteParamsABI struct {
	PoolKey     poolKeyABI
	ZeroForOne  bool
	ExactAmount *big.Int
	HookData    []byte
}

type swapActionABI struct {
	PoolKey          poolKeyABI
	ZeroForOne       bool
	AmountIn         *big.Int
	AmountOutMinimum *big.Int
	HookData         []byte
}

// PoolTierCandidate defines one (fee, tickSpacing, hooks) V4 pool configuration to probe.
type PoolTierCandidate struct {
	Fee         uint32
	TickSpacing int32
	Hooks       common.Address
}

var defaultPoolTiers = []PoolTierCandidate{
	{Fee: 3000, TickSpacing: 60},
	{Fee: 10000, TickSpacing: 200},
	{Fee: 500, TickSpacing: 10},
	{Fee: 100, TickSpacing: 1},
	{Fee: 0x800000, TickSpacing: 200}, // Dynamic fee (Clanker / hook pools)
	{Fee: 0x800000, TickSpacing: 60},
}

type Adapter struct {
	chainID       uint64
	protocolID    string
	quoteAsset    string
	poolManager   common.Address
	quoter        common.Address
	router        common.Address
	wrappedNative common.Address
	extraHooks    []common.Address
}

func NewETHAdapter() *Adapter {
	return &Adapter{
		chainID:       ethereum.ChainID,
		protocolID:    ProtocolIDETH,
		quoteAsset:    "ETH",
		poolManager:   ethereum.UniswapV4PoolManager,
		quoter:        ethereum.UniswapV4Quoter,
		router:        ethereum.UniswapV4UniversalRouter,
		wrappedNative: ethereum.WETHAddress,
	}
}

func NewBSCAdapter() *Adapter {
	return &Adapter{
		chainID:       bsc.ChainID,
		protocolID:    ProtocolIDBSC,
		quoteAsset:    "BNB",
		poolManager:   bsc.UniswapV4PoolManager,
		quoter:        bsc.UniswapV4Quoter,
		router:        bsc.UniswapV4UniversalRouter,
		wrappedNative: bsc.WBNBAddress,
	}
}

func NewBaseAdapter() *Adapter {
	return &Adapter{
		chainID:       basechain.ChainID,
		protocolID:    ProtocolIDBase,
		quoteAsset:    "ETH",
		poolManager:   basechain.UniswapV4PoolManager,
		quoter:        basechain.UniswapV4Quoter,
		router:        basechain.UniswapV4UniversalRouter,
		wrappedNative: basechain.WETHAddress,
	}
}

func NewRBHAdapter() *Adapter {
	return &Adapter{
		chainID:       robinhood.ChainID,
		protocolID:    ProtocolIDRBH,
		quoteAsset:    "ETH",
		poolManager:   robinhood.V4PoolManager,
		quoter:        robinhood.V4Quoter,
		router:        robinhood.V4UniversalRouter,
		wrappedNative: robinhood.WETHAddress,
		extraHooks:    []common.Address{robinhood.PonsMemeHook, robinhood.BagsHook, robinhood.O1Hook},
	}
}

func (a *Adapter) Describe() ports.AdapterDescriptor {
	return ports.AdapterDescriptor{
		ChainID:              a.chainID,
		ProtocolID:           a.protocolID,
		AdapterRevision:      AdapterRevision,
		PoolPhase:            "DEX_V4",
		QuoteAsset:           a.quoteAsset,
		SupportsBuy:          true,
		SupportsSell:         true,
		AllowanceModel:       ports.AllowanceERC20Direct,
		FeeOnTransferSupport: false,
		DeadlineCapability:   ports.DeadlineContractDeadlineUnix,
	}
}

func makeOrderedPoolKey(quoteCurrency, token common.Address, fee uint32, tickSpacing int32, hook common.Address) poolKeyABI {
	c0, c1 := quoteCurrency, token
	if strings.Compare(strings.ToLower(c0.Hex()), strings.ToLower(c1.Hex())) > 0 {
		c0, c1 = c1, c0
	}
	return poolKeyABI{
		Currency0:   c0,
		Currency1:   c1,
		Fee:         new(big.Int).SetUint64(uint64(fee)),
		TickSpacing: big.NewInt(int64(tickSpacing)),
		Hooks:       hook,
	}
}

func encodeKeyMeta(quoteCurrency common.Address, fee uint32, tickSpacing int32, hook common.Address) string {
	return fmt.Sprintf("v4:%s:%d:%d:%s", strings.ToLower(quoteCurrency.Hex()), fee, tickSpacing, strings.ToLower(hook.Hex()))
}

func decodeKeyMeta(meta string, token common.Address) (poolKeyABI, common.Address, bool) {
	parts := strings.Split(meta, ":")
	if len(parts) != 5 || parts[0] != "v4" {
		return poolKeyABI{}, common.Address{}, false
	}
	quoteCur := common.HexToAddress(parts[1])
	fee64, err1 := strconv.ParseUint(parts[2], 10, 32)
	ts64, err2 := strconv.ParseInt(parts[3], 10, 32)
	if err1 != nil || err2 != nil {
		return poolKeyABI{}, common.Address{}, false
	}
	hook := common.HexToAddress(parts[4])
	return makeOrderedPoolKey(quoteCur, token, uint32(fee64), int32(ts64), hook), quoteCur, true
}

func (a *Adapter) candidateKeys(token common.Address) []struct {
	key      poolKeyABI
	quoteCur common.Address
	fee      uint32
	ts       int32
	hook     common.Address
} {
	var out []struct {
		key      poolKeyABI
		quoteCur common.Address
		fee      uint32
		ts       int32
		hook     common.Address
	}
	// Uniswap V4 natively supports address(0) ETH/BNB pools as well as wrapped WETH/WBNB pools.
	quoteCurrencies := []common.Address{{}}
	if a.wrappedNative != (common.Address{}) {
		quoteCurrencies = append(quoteCurrencies, a.wrappedNative)
	}
	hooks := append([]common.Address{{}}, a.extraHooks...)
	for _, qc := range quoteCurrencies {
		if qc == token {
			continue
		}
		for _, hk := range hooks {
			for _, tier := range defaultPoolTiers {
				out = append(out, struct {
					key      poolKeyABI
					quoteCur common.Address
					fee      uint32
					ts       int32
					hook     common.Address
				}{
					key:      makeOrderedPoolKey(qc, token, tier.Fee, tier.TickSpacing, hk),
					quoteCur: qc,
					fee:      tier.Fee,
					ts:       tier.TickSpacing,
					hook:     hk,
				})
			}
		}
	}
	return out
}

// Resolve verifies PoolManager, UniversalRouter, token bytecode, and discovers the highest-liquidity
// V4 PoolKey via V4Quoter.quoteExactInputSingle pinned to blockHash.
func (a *Adapter) Resolve(ctx context.Context, rpc ports.ReadOnlyRPC, token common.Address, blockNumber uint64, blockHash common.Hash) (ports.ResolvedRoute, error) {
	if rpc == nil || token == (common.Address{}) || token == a.wrappedNative || blockHash == (common.Hash{}) {
		return ports.ResolvedRoute{}, errors.New("invalid uniswap-v4 resolve inputs")
	}
	pmCode, err := rpc.CodeAt(ctx, a.poolManager, blockHash)
	if err != nil || len(pmCode) == 0 {
		return ports.ResolvedRoute{}, fmt.Errorf("%s PoolManager has no runtime code", a.protocolID)
	}
	routerCode, err := rpc.CodeAt(ctx, a.router, blockHash)
	if err != nil || len(routerCode) == 0 {
		return ports.ResolvedRoute{}, fmt.Errorf("%s UniversalRouter has no runtime code", a.protocolID)
	}
	tokCode, err := rpc.CodeAt(ctx, token, blockHash)
	if err != nil || len(tokCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("target token has no runtime code")
	}

	probeWei := big.NewInt(1_000_000_000_000_000) // 0.001 native probe
	var bestOut *big.Int
	var bestMeta string
	for _, cand := range a.candidateKeys(token) {
		zeroForOne := cand.quoteCur == cand.key.Currency0
		cd, pErr := quoterABI.Pack("quoteExactInputSingle", quoteParamsABI{
			PoolKey:     cand.key,
			ZeroForOne:  zeroForOne,
			ExactAmount: probeWei,
			HookData:    []byte{},
		})
		if pErr != nil {
			continue
		}
		raw, cErr := rpc.CallContractAt(ctx, a.quoter, cd, blockHash)
		if cErr != nil || len(raw) < 64 {
			continue
		}
		unpacked, uErr := quoterABI.Unpack("quoteExactInputSingle", raw)
		if uErr != nil || len(unpacked) < 1 {
			continue
		}
		amtOut, ok := unpacked[0].(*big.Int)
		if ok && amtOut != nil && amtOut.Sign() > 0 {
			if bestOut == nil || amtOut.Cmp(bestOut) > 0 {
				bestOut = new(big.Int).Set(amtOut)
				bestMeta = encodeKeyMeta(cand.quoteCur, cand.fee, cand.ts, cand.hook)
			}
		}
	}
	if bestOut == nil {
		return ports.ResolvedRoute{}, fmt.Errorf("%s no initialized V4 pool with liquidity found for token", a.protocolID)
	}
	return ports.ResolvedRoute{
		ChainID:            a.chainID,
		ProtocolID:         a.protocolID,
		AdapterRevision:    AdapterRevision,
		DeploymentRevision: bestMeta,
		PoolPhase:          "DEX_V4",
		Token:              token,
		Factory:            a.poolManager,
		PoolOrCurve:        a.poolManager,
		RouterOrSpender:    a.router,
		RuntimeCodeHash:    crypto.Keccak256Hash(routerCode),
		QuoteBlockNumber:   blockNumber,
		QuoteBlockHash:     blockHash,
	}, nil
}

// Quote calls V4Quoter.quoteExactInputSingle on the resolved PoolKey pinned to QuoteBlockHash.
func (a *Adapter) Quote(ctx context.Context, rpc ports.ReadOnlyRPC, req ports.QuoteRequest) (ports.QuoteResult, error) {
	if rpc == nil || req.Route.Token == (common.Address{}) || req.Route.QuoteBlockHash == (common.Hash{}) ||
		req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || req.AmountInRaw.Cmp(maxUint128) > 0 || req.SlippageBps > 10000 {
		return ports.QuoteResult{}, errors.New("invalid uniswap-v4 quote request")
	}
	key, quoteCur, ok := decodeKeyMeta(req.Route.DeploymentRevision, req.Route.Token)
	if !ok {
		key = makeOrderedPoolKey(common.Address{}, req.Route.Token, 3000, 60, common.Address{})
		quoteCur = common.Address{}
	}
	var zeroForOne bool
	switch req.Side {
	case ports.SideBuy:
		zeroForOne = quoteCur == key.Currency0
	case ports.SideSell:
		zeroForOne = req.Route.Token == key.Currency0
	default:
		return ports.QuoteResult{}, errors.New("unsupported side")
	}
	cd, err := quoterABI.Pack("quoteExactInputSingle", quoteParamsABI{
		PoolKey:     key,
		ZeroForOne:  zeroForOne,
		ExactAmount: new(big.Int).Set(req.AmountInRaw),
		HookData:    []byte{},
	})
	if err != nil {
		return ports.QuoteResult{}, err
	}
	raw, err := rpc.CallContractAt(ctx, a.quoter, cd, req.Route.QuoteBlockHash)
	if err != nil {
		return ports.QuoteResult{}, fmt.Errorf("%s quoter failed: %w", a.protocolID, err)
	}
	unpacked, err := quoterABI.Unpack("quoteExactInputSingle", raw)
	if err != nil || len(unpacked) < 2 {
		return ports.QuoteResult{}, errors.New("invalid V4Quoter response")
	}
	expectedOut, ok := unpacked[0].(*big.Int)
	if !ok || expectedOut == nil || expectedOut.Sign() <= 0 {
		return ports.QuoteResult{}, errors.New("V4Quoter returned zero output")
	}
	gasEst := uint64(220_000)
	if g, ok := unpacked[1].(*big.Int); ok && g != nil && g.IsUint64() && g.Uint64() > 0 {
		gasEst = g.Uint64() + 60_000
	}
	minOut := new(big.Int).Mul(expectedOut, new(big.Int).SetUint64(10000-req.SlippageBps))
	minOut.Div(minOut, big.NewInt(10000))
	return ports.QuoteResult{
		ExpectedOutRaw:   expectedOut,
		MinOutRaw:        minOut,
		FeeBps:           uint64(key.Fee.Uint64() / 100),
		EstimatedGas:     gasEst,
		QuoteBlockNumber: req.Route.QuoteBlockNumber,
		QuoteBlockHash:   req.Route.QuoteBlockHash,
	}, nil
}

// BuildUnsigned encodes UniversalRouter.execute([0x10], [v4Plan], deadline) for V4 ExactInputSingle.
func (a *Adapter) BuildUnsigned(req ports.QuoteRequest, quote ports.QuoteResult) (ports.UnsignedTx, error) {
	if req.Route.Token == (common.Address{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 ||
		req.AmountInRaw.Cmp(maxUint128) > 0 || req.ContractExpiry <= 0 {
		return ports.UnsignedTx{}, errors.New("incomplete uniswap-v4 build inputs")
	}
	if quote.MinOutRaw == nil || quote.MinOutRaw.Sign() < 0 || quote.MinOutRaw.Cmp(maxUint128) > 0 {
		return ports.UnsignedTx{}, errors.New("invalid uniswap-v4 minOut")
	}
	key, quoteCur, ok := decodeKeyMeta(req.Route.DeploymentRevision, req.Route.Token)
	if !ok {
		key = makeOrderedPoolKey(common.Address{}, req.Route.Token, 3000, 60, common.Address{})
		quoteCur = common.Address{}
	}
	var (
		currencyIn  common.Address
		currencyOut common.Address
		zeroForOne  bool
		value       = big.NewInt(0)
	)
	switch req.Side {
	case ports.SideBuy:
		currencyIn = quoteCur
		currencyOut = req.Route.Token
		zeroForOne = currencyIn == key.Currency0
		if currencyIn == (common.Address{}) {
			value = new(big.Int).Set(req.AmountInRaw)
		}
	case ports.SideSell:
		currencyIn = req.Route.Token
		currencyOut = quoteCur
		zeroForOne = currencyIn == key.Currency0
	default:
		return ports.UnsignedTx{}, errors.New("unsupported side")
	}

	swapParam, err := v4SwapActionArgs.Pack(swapActionABI{
		PoolKey:          key,
		ZeroForOne:       zeroForOne,
		AmountIn:         new(big.Int).Set(req.AmountInRaw),
		AmountOutMinimum: new(big.Int).Set(quote.MinOutRaw),
		HookData:         []byte{},
	})
	if err != nil {
		return ports.UnsignedTx{}, err
	}
	settleParam, err := addrUintArgs.Pack(currencyIn, new(big.Int).Set(req.AmountInRaw))
	if err != nil {
		return ports.UnsignedTx{}, err
	}
	takeParam, err := addrUintArgs.Pack(currencyOut, new(big.Int).Set(quote.MinOutRaw))
	if err != nil {
		return ports.UnsignedTx{}, err
	}
	v4Plan, err := v4PlanArgs.Pack(
		[]byte{actionSwapExactInSingle, actionSettleAll, actionTakeAll},
		[][]byte{swapParam, settleParam, takeParam},
	)
	if err != nil {
		return ports.UnsignedTx{}, err
	}
	calldata, err := universalRouterABI.Pack("execute", []byte{commandV4Swap}, [][]byte{v4Plan}, big.NewInt(req.ContractExpiry))
	if err != nil {
		return ports.UnsignedTx{}, err
	}
	_ = hex.EncodeToString // keep hex available for debugging if needed
	return ports.UnsignedTx{
		To:               a.router,
		Value:            value,
		Calldata:         calldata,
		Spender:          a.router,
		ContractDeadline: req.ContractExpiry,
		Summary:          string(req.Side) + ":" + a.protocolID,
	}, nil
}

// DecodeEffects computes net wallet ERC-20 Transfer delta and verifies PoolManager V4 Swap log presence.
func (a *Adapter) DecodeEffects(route ports.ResolvedRoute, wallet common.Address, side ports.Side, receipt *types.Receipt) (ports.DecodedEffects, error) {
	if receipt == nil || wallet == (common.Address{}) || route.Token == (common.Address{}) {
		return ports.DecodedEffects{}, errors.New("invalid uniswap-v4 decode inputs")
	}
	netToken := big.NewInt(0)
	sawV4Swap := false
	for _, lg := range receipt.Logs {
		if lg == nil || len(lg.Topics) == 0 {
			continue
		}
		if lg.Address == a.poolManager && lg.Topics[0] == V4PoolSwapTopic {
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
