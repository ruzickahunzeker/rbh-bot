// Package aerodrome implements a pure, side-effect-free ProtocolAdapter for Base Mainnet
// Aerodrome Finance V2 (chain_id=8453, volatile + stable pools, SupportingFeeOnTransferTokens).
package aerodrome

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
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

const (
	ProtocolID      = "aerodrome-v2"
	AdapterRevision = "aerodrome-v2-base-r1"
)

const aeroFactoryABIJSON = `[
  {"name":"getPool","type":"function","stateMutability":"view","inputs":[{"name":"tokenA","type":"address"},{"name":"tokenB","type":"address"},{"name":"stable","type":"bool"}],"outputs":[{"name":"pool","type":"address"}]}
]`

const aeroRouterABIJSON = `[
  {
    "name": "getAmountsOut",
    "type": "function",
    "stateMutability": "view",
    "inputs": [
      {"name": "amountIn", "type": "uint256"},
      {"name": "routes", "type": "tuple[]", "components": [
        {"name": "from", "type": "address"},
        {"name": "to", "type": "address"},
        {"name": "stable", "type": "bool"},
        {"name": "factory", "type": "address"}
      ]}
    ],
    "outputs": [{"name": "amounts", "type": "uint256[]"}]
  },
  {
    "name": "swapExactETHForTokensSupportingFeeOnTransferTokens",
    "type": "function",
    "stateMutability": "payable",
    "inputs": [
      {"name": "amountOutMin", "type": "uint256"},
      {"name": "routes", "type": "tuple[]", "components": [
        {"name": "from", "type": "address"},
        {"name": "to", "type": "address"},
        {"name": "stable", "type": "bool"},
        {"name": "factory", "type": "address"}
      ]},
      {"name": "to", "type": "address"},
      {"name": "deadline", "type": "uint256"}
    ],
    "outputs": []
  },
  {
    "name": "swapExactTokensForETHSupportingFeeOnTransferTokens",
    "type": "function",
    "stateMutability": "nonpayable",
    "inputs": [
      {"name": "amountIn", "type": "uint256"},
      {"name": "amountOutMin", "type": "uint256"},
      {"name": "routes", "type": "tuple[]", "components": [
        {"name": "from", "type": "address"},
        {"name": "to", "type": "address"},
        {"name": "stable", "type": "bool"},
        {"name": "factory", "type": "address"}
      ]},
      {"name": "to", "type": "address"},
      {"name": "deadline", "type": "uint256"}
    ],
    "outputs": []
  }
]`

type aeroRoute struct {
	From    common.Address
	To      common.Address
	Stable  bool
	Factory common.Address
}

var (
	factoryABI          = mustParseABI(aeroFactoryABIJSON)
	routerABI           = mustParseABI(aeroRouterABIJSON)
	erc20TransferTopic  = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	wethDepositTopic    = crypto.Keccak256Hash([]byte("Deposit(address,uint256)"))
	wethWithdrawalTopic = crypto.Keccak256Hash([]byte("Withdrawal(address,uint256)"))
)

func mustParseABI(src string) abi.ABI {
	p, err := abi.JSON(strings.NewReader(src))
	if err != nil {
		panic(err)
	}
	return p
}

type Adapter struct {
	chainID uint64
	factory common.Address
	router  common.Address
	weth    common.Address
}

func NewBaseAdapter() *Adapter {
	return &Adapter{
		chainID: basechain.ChainID,
		factory: basechain.AerodromePoolFactory,
		router:  basechain.AerodromeRouter,
		weth:    basechain.WETHAddress,
	}
}

func (a *Adapter) Describe() ports.AdapterDescriptor {
	return ports.AdapterDescriptor{
		ChainID:              a.chainID,
		ProtocolID:           ProtocolID,
		AdapterRevision:      AdapterRevision,
		PoolPhase:            "DEX_V2",
		QuoteAsset:           "ETH",
		SupportsBuy:          true,
		SupportsSell:         true,
		AllowanceModel:       ports.AllowanceERC20Direct,
		FeeOnTransferSupport: true,
		DeadlineCapability:   ports.DeadlineContractDeadlineUnix,
	}
}

func (a *Adapter) Resolve(ctx context.Context, rpc ports.ReadOnlyRPC, token common.Address, blockNumber uint64, blockHash common.Hash) (ports.ResolvedRoute, error) {
	if rpc == nil || token == (common.Address{}) || token == a.weth || blockHash == (common.Hash{}) {
		return ports.ResolvedRoute{}, errors.New("invalid aerodrome resolve inputs")
	}
	routerCode, err := rpc.CodeAt(ctx, a.router, blockHash)
	if err != nil || len(routerCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("aerodrome router has no runtime code")
	}
	tokCode, err := rpc.CodeAt(ctx, token, blockHash)
	if err != nil || len(tokCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("target token has no runtime code")
	}
	for _, stable := range []bool{false, true} {
		cd, err := factoryABI.Pack("getPool", a.weth, token, stable)
		if err != nil {
			continue
		}
		raw, err := rpc.CallContractAt(ctx, a.factory, cd, blockHash)
		if err != nil {
			continue
		}
		out, err := factoryABI.Unpack("getPool", raw)
		if err != nil || len(out) != 1 {
			continue
		}
		poolAddr, ok := out[0].(common.Address)
		if ok && poolAddr != (common.Address{}) {
			if pCode, cErr := rpc.CodeAt(ctx, poolAddr, blockHash); cErr == nil && len(pCode) > 0 {
				rev := "aerodrome-volatile"
				if stable {
					rev = "aerodrome-stable"
				}
				return ports.ResolvedRoute{
					ChainID:            a.chainID,
					ProtocolID:         ProtocolID,
					AdapterRevision:    AdapterRevision,
					DeploymentRevision: rev,
					PoolPhase:          "DEX_V2",
					Token:              token,
					Factory:            a.factory,
					PoolOrCurve:        poolAddr,
					RouterOrSpender:    a.router,
					RuntimeCodeHash:    crypto.Keccak256Hash(routerCode),
					QuoteBlockNumber:   blockNumber,
					QuoteBlockHash:     blockHash,
				}, nil
			}
		}
	}
	return ports.ResolvedRoute{}, errors.New("aerodrome pool does not exist for WETH/token")
}

func (a *Adapter) buildRoutes(route ports.ResolvedRoute, side ports.Side) ([]aeroRoute, error) {
	stable := route.DeploymentRevision == "aerodrome-stable"
	switch side {
	case ports.SideBuy:
		return []aeroRoute{{From: a.weth, To: route.Token, Stable: stable, Factory: a.factory}}, nil
	case ports.SideSell:
		return []aeroRoute{{From: route.Token, To: a.weth, Stable: stable, Factory: a.factory}}, nil
	default:
		return nil, errors.New("unsupported side")
	}
}

func (a *Adapter) Quote(ctx context.Context, rpc ports.ReadOnlyRPC, req ports.QuoteRequest) (ports.QuoteResult, error) {
	if rpc == nil || req.Route.Token == (common.Address{}) || req.Route.QuoteBlockHash == (common.Hash{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || req.SlippageBps > 10000 {
		return ports.QuoteResult{}, errors.New("invalid aerodrome quote request")
	}
	routes, err := a.buildRoutes(req.Route, req.Side)
	if err != nil {
		return ports.QuoteResult{}, err
	}
	cd, err := routerABI.Pack("getAmountsOut", req.AmountInRaw, routes)
	if err != nil {
		return ports.QuoteResult{}, err
	}
	raw, err := rpc.CallContractAt(ctx, a.router, cd, req.Route.QuoteBlockHash)
	if err != nil {
		return ports.QuoteResult{}, fmt.Errorf("aerodrome getAmountsOut failed: %w", err)
	}
	out, err := routerABI.Unpack("getAmountsOut", raw)
	if err != nil || len(out) != 1 {
		return ports.QuoteResult{}, errors.New("invalid aerodrome getAmountsOut response")
	}
	amounts, ok := out[0].([]*big.Int)
	if !ok || len(amounts) < 2 || amounts[len(amounts)-1] == nil || amounts[len(amounts)-1].Sign() <= 0 {
		return ports.QuoteResult{}, errors.New("aerodrome returned zero output")
	}
	expectedOut := new(big.Int).Set(amounts[len(amounts)-1])
	minOut := new(big.Int).Mul(expectedOut, new(big.Int).SetUint64(10000-req.SlippageBps))
	minOut.Div(minOut, big.NewInt(10000))
	return ports.QuoteResult{
		ExpectedOutRaw:   expectedOut,
		MinOutRaw:        minOut,
		FeeBps:           30,
		EstimatedGas:     240_000,
		QuoteBlockNumber: req.Route.QuoteBlockNumber,
		QuoteBlockHash:   req.Route.QuoteBlockHash,
	}, nil
}

func (a *Adapter) BuildUnsigned(req ports.QuoteRequest, quote ports.QuoteResult) (ports.UnsignedTx, error) {
	if req.Route.Token == (common.Address{}) || req.Recipient == (common.Address{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || req.ContractExpiry <= 0 {
		return ports.UnsignedTx{}, errors.New("incomplete aerodrome build inputs")
	}
	if quote.MinOutRaw == nil || quote.MinOutRaw.Sign() < 0 {
		return ports.UnsignedTx{}, errors.New("invalid minOut")
	}
	routes, err := a.buildRoutes(req.Route, req.Side)
	if err != nil {
		return ports.UnsignedTx{}, err
	}
	deadline := big.NewInt(req.ContractExpiry)
	var calldata []byte
	value := big.NewInt(0)
	switch req.Side {
	case ports.SideBuy:
		calldata, err = routerABI.Pack("swapExactETHForTokensSupportingFeeOnTransferTokens", quote.MinOutRaw, routes, req.Recipient, deadline)
		value = new(big.Int).Set(req.AmountInRaw)
	case ports.SideSell:
		calldata, err = routerABI.Pack("swapExactTokensForETHSupportingFeeOnTransferTokens", req.AmountInRaw, quote.MinOutRaw, routes, req.Recipient, deadline)
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
		Summary:          string(req.Side) + ":" + ProtocolID,
	}, nil
}

func (a *Adapter) DecodeEffects(route ports.ResolvedRoute, wallet common.Address, side ports.Side, receipt *types.Receipt) (ports.DecodedEffects, error) {
	if receipt == nil || wallet == (common.Address{}) || route.Token == (common.Address{}) {
		return ports.DecodedEffects{}, errors.New("invalid aerodrome decode inputs")
	}
	netToken := big.NewInt(0)
	netNative := big.NewInt(0)
	for _, lg := range receipt.Logs {
		if lg == nil || len(lg.Topics) == 0 {
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
		} else if lg.Address == a.weth && len(lg.Topics) == 2 && len(lg.Data) == 32 {
			actor := common.BytesToAddress(lg.Topics[1].Bytes())
			if actor != a.router && actor != wallet {
				continue
			}
			amt := new(big.Int).SetBytes(lg.Data)
			if lg.Topics[0] == wethWithdrawalTopic {
				netNative.Add(netNative, amt)
			} else if lg.Topics[0] == wethDepositTopic {
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
