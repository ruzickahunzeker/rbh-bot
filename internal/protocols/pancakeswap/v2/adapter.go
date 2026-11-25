// Package v2 implements a pure, side-effect-free ProtocolAdapter for BSC PancakeSwap V2
// (chain_id=56) with Fee-on-Transfer support and mandatory CONTRACT_DEADLINE_UNIX.
// It holds no signer, broadcaster, nonce allocator, or DB write handle.
package v2

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
	ProtocolID      = "pancake-v2"
	AdapterRevision = "pancake-v2-bsc-r1"
)

const pancakeV2RouterABIJSON = `[
  {"name":"getAmountsOut","type":"function","stateMutability":"view","inputs":[{"name":"amountIn","type":"uint256"},{"name":"path","type":"address[]"}],"outputs":[{"name":"amounts","type":"uint256[]"}]},
  {"name":"swapExactETHForTokensSupportingFeeOnTransferTokens","type":"function","inputs":[{"name":"amountOutMin","type":"uint256"},{"name":"path","type":"address[]"},{"name":"to","type":"address"},{"name":"deadline","type":"uint256"}],"outputs":[]},
  {"name":"swapExactTokensForETHSupportingFeeOnTransferTokens","type":"function","inputs":[{"name":"amountIn","type":"uint256"},{"name":"amountOutMin","type":"uint256"},{"name":"path","type":"address[]"},{"name":"to","type":"address"},{"name":"deadline","type":"uint256"}],"outputs":[]}
]`

const pancakeV2FactoryABIJSON = `[
  {"name":"getPair","type":"function","stateMutability":"view","inputs":[{"name":"tokenA","type":"address"},{"name":"tokenB","type":"address"}],"outputs":[{"name":"pair","type":"address"}]}
]`

const erc20ApproveABIJSON = `[
  {"name":"approve","type":"function","inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]}
]`

var (
	routerABI           = mustParseABI(pancakeV2RouterABIJSON)
	factoryABI          = mustParseABI(pancakeV2FactoryABIJSON)
	erc20ABI            = mustParseABI(erc20ApproveABIJSON)
	erc20TransferTopic  = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	wbnbDepositTopic    = crypto.Keccak256Hash([]byte("Deposit(address,uint256)"))
	wbnbWithdrawalTopic = crypto.Keccak256Hash([]byte("Withdrawal(address,uint256)"))
)

func mustParseABI(src string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(src))
	if err != nil {
		panic(err)
	}
	return parsed
}

type Adapter struct {
	chainID    uint64
	protocolID string
	quoteAsset string
	router     common.Address
	factory    common.Address
	wbnb       common.Address
}

func NewAdapter() *Adapter {
	return NewCustomAdapter(bsc.ChainID, ProtocolID, "BNB", bsc.PancakeV2Factory, bsc.PancakeV2Router, bsc.WBNBAddress)
}

// NewCustomAdapter constructs a reusable Uniswap V2-compatible adapter for any EVM chain
// (e.g. ETH Uniswap V2, Base Uniswap V2, HyperEVM HyperSwap V2, BSC PancakeSwap V2).
func NewCustomAdapter(chainID uint64, protocolID, quoteAsset string, factory, router, wrappedNative common.Address) *Adapter {
	return &Adapter{
		chainID:    chainID,
		protocolID: protocolID,
		quoteAsset: quoteAsset,
		router:     router,
		factory:    factory,
		wbnb:       wrappedNative,
	}
}

func (a *Adapter) Describe() ports.AdapterDescriptor {
	pid := a.protocolID
	if pid == "" {
		pid = ProtocolID
	}
	qa := a.quoteAsset
	if qa == "" {
		qa = "BNB"
	}
	return ports.AdapterDescriptor{
		ChainID:              a.chainID,
		ProtocolID:           pid,
		AdapterRevision:      AdapterRevision,
		PoolPhase:            "DEX_V2",
		QuoteAsset:           qa,
		SupportsBuy:          true,
		SupportsSell:         true,
		AllowanceModel:       ports.AllowanceERC20Direct,
		FeeOnTransferSupport: true,
		DeadlineCapability:   ports.DeadlineContractDeadlineUnix,
	}
}

// Resolve verifies router, token, and real PancakeSwap V2 Factory pair contract existence
// pinned to blockHash, returning the canonical V2 pair address as PoolOrCurve.
func (a *Adapter) Resolve(ctx context.Context, rpc ports.ReadOnlyRPC, token common.Address, blockNumber uint64, blockHash common.Hash) (ports.ResolvedRoute, error) {
	if rpc == nil || token == (common.Address{}) || token == a.wbnb || blockHash == (common.Hash{}) {
		return ports.ResolvedRoute{}, errors.New("invalid BSC pancake-v2 resolve inputs")
	}
	routerCode, err := rpc.CodeAt(ctx, a.router, blockHash)
	if err != nil || len(routerCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("pancake-v2 router has no runtime code")
	}
	tokenCode, err := rpc.CodeAt(ctx, token, blockHash)
	if err != nil || len(tokenCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("target token has no runtime code")
	}
	lookupPair := func(quoteTok common.Address) (common.Address, error) {
		cd, err := factoryABI.Pack("getPair", quoteTok, token)
		if err != nil {
			return common.Address{}, err
		}
		rawPair, err := rpc.CallContractAt(ctx, a.factory, cd, blockHash)
		if err != nil {
			return common.Address{}, err
		}
		unpacked, err := factoryABI.Unpack("getPair", rawPair)
		if err != nil || len(unpacked) != 1 {
			return common.Address{}, errors.New("invalid pancake-v2 factory.getPair response")
		}
		addr, _ := unpacked[0].(common.Address)
		return addr, nil
	}

	pid := a.Describe().ProtocolID
	deployRev := pid + "-mainnet"
	if a.chainID == bsc.ChainID {
		deployRev = "pancakeswap-v2-bsc-mainnet"
	}
	pairAddr, err := lookupPair(a.wbnb)
	if err != nil {
		return ports.ResolvedRoute{}, fmt.Errorf("%s factory.getPair failed: %w", pid, err)
	}
	// Only probe BSC's USDT/USD1 2-hop pairs when running on BSC (chain_id=56).
	if a.chainID == bsc.ChainID {
		if pairAddr == (common.Address{}) && token != bsc.USDTAddress {
			if p, pErr := lookupPair(bsc.USDTAddress); pErr == nil && p != (common.Address{}) {
				pairAddr = p
				deployRev = "pancakeswap-v2-bsc-2hop-usdt"
			}
		}
		if pairAddr == (common.Address{}) && token != bsc.USD1Address {
			if p, pErr := lookupPair(bsc.USD1Address); pErr == nil && p != (common.Address{}) {
				pairAddr = p
				deployRev = "pancakeswap-v2-bsc-2hop-usd1"
			}
		}
	}
	if pairAddr == (common.Address{}) {
		return ports.ResolvedRoute{}, fmt.Errorf("%s pair does not exist", pid)
	}
	pairCode, err := rpc.CodeAt(ctx, pairAddr, blockHash)
	if err != nil || len(pairCode) == 0 {
		return ports.ResolvedRoute{}, fmt.Errorf("%s pair has no runtime code", pid)
	}
	return ports.ResolvedRoute{
		ChainID:            a.chainID,
		ProtocolID:         pid,
		AdapterRevision:    AdapterRevision,
		DeploymentRevision: deployRev,
		PoolPhase:          "DEX_V2",
		Token:              token,
		PoolOrCurve:        pairAddr,
		RouterOrSpender:    a.router,
		RuntimeCodeHash:    crypto.Keccak256Hash(routerCode),
		QuoteBlockNumber:   blockNumber,
		QuoteBlockHash:     blockHash,
	}, nil
}

func (a *Adapter) swapPath(route ports.ResolvedRoute, side ports.Side) ([]common.Address, error) {
	var hop common.Address
	switch route.DeploymentRevision {
	case "pancakeswap-v2-bsc-2hop-usdt":
		hop = bsc.USDTAddress
	case "pancakeswap-v2-bsc-2hop-usd1":
		hop = bsc.USD1Address
	}
	switch side {
	case ports.SideBuy:
		if hop != (common.Address{}) {
			return []common.Address{a.wbnb, hop, route.Token}, nil
		}
		return []common.Address{a.wbnb, route.Token}, nil
	case ports.SideSell:
		if hop != (common.Address{}) {
			return []common.Address{route.Token, hop, a.wbnb}, nil
		}
		return []common.Address{route.Token, a.wbnb}, nil
	default:
		return nil, errors.New("unsupported side")
	}
}

// Quote queries PancakeSwap V2 Router.getAmountsOut pinned to QuoteBlockHash and applies slippage.
func (a *Adapter) Quote(ctx context.Context, rpc ports.ReadOnlyRPC, req ports.QuoteRequest) (ports.QuoteResult, error) {
	if rpc == nil || req.Route.Token == (common.Address{}) || req.Route.QuoteBlockHash == (common.Hash{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || req.SlippageBps > 10000 {
		return ports.QuoteResult{}, errors.New("invalid quote request")
	}
	path, err := a.swapPath(req.Route, req.Side)
	if err != nil {
		return ports.QuoteResult{}, err
	}
	calldata, err := routerABI.Pack("getAmountsOut", req.AmountInRaw, path)
	if err != nil {
		return ports.QuoteResult{}, err
	}
	rawOut, err := rpc.CallContractAt(ctx, a.router, calldata, req.Route.QuoteBlockHash)
	if err != nil {
		return ports.QuoteResult{}, fmt.Errorf("pancake-v2 router.getAmountsOut failed: %w", err)
	}
	unpacked, err := routerABI.Unpack("getAmountsOut", rawOut)
	if err != nil || len(unpacked) != 1 {
		return ports.QuoteResult{}, errors.New("invalid pancake-v2 getAmountsOut response")
	}
	amounts, ok := unpacked[0].([]*big.Int)
	if !ok || len(amounts) < 2 || amounts[len(amounts)-1] == nil || amounts[len(amounts)-1].Sign() <= 0 {
		return ports.QuoteResult{}, errors.New("pancake-v2 getAmountsOut returned zero output")
	}
	expectedOut := new(big.Int).Set(amounts[len(amounts)-1])
	minOut := new(big.Int).Mul(expectedOut, new(big.Int).SetUint64(10000-req.SlippageBps))
	minOut.Div(minOut, big.NewInt(10000))
	return ports.QuoteResult{
		ExpectedOutRaw:   expectedOut,
		MinOutRaw:        minOut,
		FeeBps:           25, // 0.25% PancakeSwap V2 swap fee
		EstimatedGas:     220_000,
		QuoteBlockNumber: req.Route.QuoteBlockNumber,
		QuoteBlockHash:   req.Route.QuoteBlockHash,
	}, nil
}

// BuildUnsigned constructs exact PancakeSwap V2 SupportingFeeOnTransferTokens calldata
// with mandatory positive CONTRACT_DEADLINE_UNIX. Never signs, sends, or implicitly approves.
func (a *Adapter) BuildUnsigned(req ports.QuoteRequest, quote ports.QuoteResult) (ports.UnsignedTx, error) {
	if req.Route.Token == (common.Address{}) || req.Recipient == (common.Address{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 {
		return ports.UnsignedTx{}, errors.New("incomplete pancake-v2 build inputs")
	}
	if req.ContractExpiry <= 0 {
		return ports.UnsignedTx{}, errors.New("pancake-v2 requires positive contract_deadline_unix")
	}
	minOut := quote.MinOutRaw
	if minOut == nil || minOut.Sign() < 0 {
		return ports.UnsignedTx{}, errors.New("invalid minOut")
	}
	deadline := big.NewInt(req.ContractExpiry)
	var (
		calldata []byte
		value    *big.Int
		err      error
	)
	path, err := a.swapPath(req.Route, req.Side)
	if err != nil {
		return ports.UnsignedTx{}, err
	}
	switch req.Side {
	case ports.SideBuy:
		calldata, err = routerABI.Pack("swapExactETHForTokensSupportingFeeOnTransferTokens", minOut, path, req.Recipient, deadline)
		value = new(big.Int).Set(req.AmountInRaw)
	case ports.SideSell:
		calldata, err = routerABI.Pack("swapExactTokensForETHSupportingFeeOnTransferTokens", req.AmountInRaw, minOut, path, req.Recipient, deadline)
		value = big.NewInt(0)
	default:
		return ports.UnsignedTx{}, errors.New("unsupported side")
	}
	if err != nil {
		return ports.UnsignedTx{}, err
	}
	return ports.UnsignedTx{
		To:               a.router,
		Value:            value,
		Calldata:         calldata,
		Spender:          a.router,
		ContractDeadline: req.ContractExpiry,
		Summary:          string(req.Side) + ":" + a.Describe().ProtocolID,
	}, nil
}

// BuildApproveCalldata builds pure unsigned ERC-20 approve(spender, amount) calldata.
func BuildApproveCalldata(spender common.Address, amountRaw *big.Int) ([]byte, error) {
	if spender == (common.Address{}) || amountRaw == nil || amountRaw.Sign() < 0 {
		return nil, errors.New("invalid approve inputs")
	}
	return erc20ABI.Pack("approve", spender, amountRaw)
}

// DecodeEffects computes the wallet's exact net ERC-20 Transfer delta AND native BNB delta
// (via WBNB Deposit/Withdrawal logs emitted by the router/wallet) from receipt logs.
func (a *Adapter) DecodeEffects(route ports.ResolvedRoute, wallet common.Address, side ports.Side, receipt *types.Receipt) (ports.DecodedEffects, error) {
	if receipt == nil || wallet == (common.Address{}) || route.Token == (common.Address{}) {
		return ports.DecodedEffects{}, errors.New("invalid decode inputs")
	}
	netToken := big.NewInt(0)
	netNative := big.NewInt(0)
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
		if lg.Address == a.wbnb && len(lg.Topics) == 2 {
			actor := common.BytesToAddress(lg.Topics[1].Bytes())
			amt := new(big.Int).SetBytes(lg.Data)
			if lg.Topics[0] == wbnbWithdrawalTopic && (actor == a.router || actor == wallet) {
				// Router unwrapped WBNB to native BNB and sent it to the recipient wallet.
				netNative.Add(netNative, amt)
			} else if lg.Topics[0] == wbnbDepositTopic && (actor == a.router || actor == wallet) {
				// Router wrapped incoming native BNB from the buyer.
				netNative.Sub(netNative, amt)
			}
		}
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
