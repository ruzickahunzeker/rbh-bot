// Package flap implements a pure, side-effect-free ProtocolAdapter for BSC Flap.sh
// (Portal bonding curve, chain_id=56) with graduation fail-closed detection and
// block-pinned quoteExactInput / swapExactInput. Holds no signer, broadcaster, or DB handle.
package flap

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
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/robinhood"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

const (
	ProtocolID      = "flap"
	AdapterRevision = "flap-portal-bsc-r1"

	// Flap Portal TokenStatus enum values.
	TokenStatusInvalid  uint8 = 0
	TokenStatusTradable uint8 = 1
	TokenStatusInSwap   uint8 = 2
	TokenStatusBonding  uint8 = 3
	TokenStatusDEX      uint8 = 4
)

var (
	// ErrTokenGraduated indicates the Flap token has migrated to DEX (AT-18).
	ErrTokenGraduated = errors.New("flap token has graduated to DEX; route via pancake-v2/v3")

	portalABI = mustParseABI(flapPortalABIJSON)

	erc20TransferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	// TokenBought(uint256 ts, address token, address buyer, uint256 amount, uint256 eth, uint256 fee, uint256 postPrice)
	TokenBoughtTopic = crypto.Keccak256Hash([]byte("TokenBought(uint256,address,address,uint256,uint256,uint256,uint256)"))
	// TokenSold(uint256 ts, address token, address seller, uint256 amount, uint256 eth, uint256 fee, uint256 postPrice)
	TokenSoldTopic = crypto.Keccak256Hash([]byte("TokenSold(uint256,address,address,uint256,uint256,uint256,uint256)"))
)

const flapPortalABIJSON = `[
  {
    "name": "getTokenV2",
    "type": "function",
    "stateMutability": "view",
    "inputs": [{"name": "token", "type": "address"}],
    "outputs": [{
      "name": "state",
      "type": "tuple",
      "components": [
        {"name": "status", "type": "uint8"},
        {"name": "reserve", "type": "uint256"},
        {"name": "circulatingSupply", "type": "uint256"},
        {"name": "price", "type": "uint256"},
        {"name": "tokenVersion", "type": "uint8"},
        {"name": "r", "type": "uint256"},
        {"name": "h", "type": "uint256"},
        {"name": "k", "type": "uint256"},
        {"name": "dexSupplyThresh", "type": "uint256"},
        {"name": "quoteTokenAddress", "type": "address"},
        {"name": "nativeToQuoteSwapEnabled", "type": "bool"},
        {"name": "extensionID", "type": "bytes32"}
      ]
    }]
  },
  {
    "name": "quoteExactInput",
    "type": "function",
    "stateMutability": "view",
    "inputs": [{
      "name": "params",
      "type": "tuple",
      "components": [
        {"name": "inputToken", "type": "address"},
        {"name": "outputToken", "type": "address"},
        {"name": "inputAmount", "type": "uint256"}
      ]
    }],
    "outputs": [{"name": "outputAmount", "type": "uint256"}]
  },
  {
    "name": "swapExactInput",
    "type": "function",
    "stateMutability": "payable",
    "inputs": [{
      "name": "params",
      "type": "tuple",
      "components": [
        {"name": "inputToken", "type": "address"},
        {"name": "outputToken", "type": "address"},
        {"name": "inputAmount", "type": "uint256"},
        {"name": "minOutputAmount", "type": "uint256"},
        {"name": "permitData", "type": "bytes"}
      ]
    }],
    "outputs": [{"name": "outputAmount", "type": "uint256"}]
  }
]`

type quoteExactInputParams struct {
	InputToken  common.Address
	OutputToken common.Address
	InputAmount *big.Int
}

type swapExactInputParams struct {
	InputToken      common.Address
	OutputToken     common.Address
	InputAmount     *big.Int
	MinOutputAmount *big.Int
	PermitData      []byte
}

type tokenStateV2 struct {
	Status                   uint8
	Reserve                  *big.Int
	CirculatingSupply        *big.Int
	Price                    *big.Int
	TokenVersion             uint8
	R                        *big.Int
	H                        *big.Int
	K                        *big.Int
	DexSupplyThresh          *big.Int
	QuoteTokenAddress        common.Address
	NativeToQuoteSwapEnabled bool
	ExtensionID              [32]byte
}

func mustParseABI(src string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(src))
	if err != nil {
		panic(err)
	}
	return parsed
}

type Adapter struct {
	chainID    uint64
	portal     common.Address
	quoteAsset string
	deployRev  string
}

func NewAdapter() *Adapter {
	return &Adapter{
		chainID:    bsc.ChainID,
		portal:     bsc.FlapPortal,
		quoteAsset: "BNB",
		deployRev:  "flap-portal-bsc-mainnet",
	}
}

func NewRBHAdapter() *Adapter {
	return &Adapter{
		chainID:    robinhood.ChainID,
		portal:     robinhood.FlapPortal,
		quoteAsset: "ETH",
		deployRev:  "flap-portal-rbh-mainnet",
	}
}

func (a *Adapter) Describe() ports.AdapterDescriptor {
	qa := a.quoteAsset
	if qa == "" {
		qa = "BNB"
	}
	return ports.AdapterDescriptor{
		ChainID:              a.chainID,
		ProtocolID:           ProtocolID,
		AdapterRevision:      AdapterRevision,
		PoolPhase:            "BONDING_CURVE",
		QuoteAsset:           qa,
		SupportsBuy:          true,
		SupportsSell:         true,
		AllowanceModel:       ports.AllowanceERC20Direct,
		FeeOnTransferSupport: false,
		DeadlineCapability:   ports.DeadlineApplicationTTLOnly,
	}
}

// Resolve queries FlapPortal.getTokenV2 pinned to blockHash, rejecting graduated/non-tradable tokens.
func (a *Adapter) Resolve(ctx context.Context, rpc ports.ReadOnlyRPC, token common.Address, blockNumber uint64, blockHash common.Hash) (ports.ResolvedRoute, error) {
	if rpc == nil || token == (common.Address{}) || blockHash == (common.Hash{}) {
		return ports.ResolvedRoute{}, errors.New("invalid flap resolve inputs")
	}
	portalCode, err := rpc.CodeAt(ctx, a.portal, blockHash)
	if err != nil || len(portalCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("flap Portal has no runtime code")
	}
	tokCode, err := rpc.CodeAt(ctx, token, blockHash)
	if err != nil || len(tokCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("target token has no runtime code")
	}
	data, err := portalABI.Pack("getTokenV2", token)
	if err != nil {
		return ports.ResolvedRoute{}, err
	}
	raw, err := rpc.CallContractAt(ctx, a.portal, data, blockHash)
	if err != nil {
		return ports.ResolvedRoute{}, fmt.Errorf("flap portal.getTokenV2 failed: %w", err)
	}
	var out struct {
		State tokenStateV2
	}
	if err := portalABI.UnpackIntoInterface(&out, "getTokenV2", raw); err != nil {
		return ports.ResolvedRoute{}, fmt.Errorf("invalid flap getTokenV2 response: %w", err)
	}
	if out.State.Status == TokenStatusDEX {
		return ports.ResolvedRoute{}, ErrTokenGraduated
	}
	if out.State.Status != TokenStatusTradable {
		return ports.ResolvedRoute{}, fmt.Errorf("flap token status %d is not tradable on bonding curve", out.State.Status)
	}
	if out.State.QuoteTokenAddress != (common.Address{}) {
		return ports.ResolvedRoute{}, fmt.Errorf("unsupported flap non-BNB quote token %s", out.State.QuoteTokenAddress.Hex())
	}
	return ports.ResolvedRoute{
		ChainID:            a.chainID,
		ProtocolID:         ProtocolID,
		AdapterRevision:    AdapterRevision,
		DeploymentRevision: a.deployRev,
		PoolPhase:          "BONDING_CURVE",
		Token:              token,
		PoolOrCurve:        a.portal,
		RouterOrSpender:    a.portal,
		RuntimeCodeHash:    crypto.Keccak256Hash(portalCode),
		QuoteBlockNumber:   blockNumber,
		QuoteBlockHash:     blockHash,
	}, nil
}

// Quote calls FlapPortal.quoteExactInput pinned to req.Route.QuoteBlockHash.
func (a *Adapter) Quote(ctx context.Context, rpc ports.ReadOnlyRPC, req ports.QuoteRequest) (ports.QuoteResult, error) {
	if rpc == nil || req.Route.Token == (common.Address{}) || req.Route.QuoteBlockHash == (common.Hash{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || req.SlippageBps > 10000 {
		return ports.QuoteResult{}, errors.New("invalid flap quote request")
	}
	var params quoteExactInputParams
	switch req.Side {
	case ports.SideBuy:
		params = quoteExactInputParams{
			InputToken:  common.Address{},
			OutputToken: req.Route.Token,
			InputAmount: new(big.Int).Set(req.AmountInRaw),
		}
	case ports.SideSell:
		params = quoteExactInputParams{
			InputToken:  req.Route.Token,
			OutputToken: common.Address{},
			InputAmount: new(big.Int).Set(req.AmountInRaw),
		}
	default:
		return ports.QuoteResult{}, fmt.Errorf("unsupported side %q", req.Side)
	}
	calldata, err := portalABI.Pack("quoteExactInput", params)
	if err != nil {
		return ports.QuoteResult{}, err
	}
	raw, err := rpc.CallContractAt(ctx, a.portal, calldata, req.Route.QuoteBlockHash)
	if err != nil {
		return ports.QuoteResult{}, fmt.Errorf("flap quoteExactInput failed: %w", err)
	}
	out, err := portalABI.Unpack("quoteExactInput", raw)
	if err != nil || len(out) < 1 {
		return ports.QuoteResult{}, errors.New("invalid flap quoteExactInput response")
	}
	expectedOut, ok := out[0].(*big.Int)
	if !ok || expectedOut == nil || expectedOut.Sign() <= 0 {
		return ports.QuoteResult{}, errors.New("flap quote returned zero output")
	}
	minOut := new(big.Int).Mul(expectedOut, new(big.Int).SetUint64(10000-req.SlippageBps))
	minOut.Div(minOut, big.NewInt(10000))
	return ports.QuoteResult{
		ExpectedOutRaw:   expectedOut,
		MinOutRaw:        minOut,
		FeeBps:           100,
		EstimatedGas:     240_000,
		QuoteBlockNumber: req.Route.QuoteBlockNumber,
		QuoteBlockHash:   req.Route.QuoteBlockHash,
	}, nil
}

// BuildUnsigned encodes FlapPortal.swapExactInput calldata (pure, offline).
func (a *Adapter) BuildUnsigned(req ports.QuoteRequest, quote ports.QuoteResult) (ports.UnsignedTx, error) {
	if req.Route.Token == (common.Address{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || quote.MinOutRaw == nil || quote.MinOutRaw.Sign() < 0 {
		return ports.UnsignedTx{}, errors.New("incomplete flap build inputs")
	}
	target := req.Route.RouterOrSpender
	if target == (common.Address{}) {
		target = a.portal
	}
	var (
		params swapExactInputParams
		value  *big.Int
	)
	switch req.Side {
	case ports.SideBuy:
		params = swapExactInputParams{
			InputToken:      common.Address{},
			OutputToken:     req.Route.Token,
			InputAmount:     new(big.Int).Set(req.AmountInRaw),
			MinOutputAmount: new(big.Int).Set(quote.MinOutRaw),
			PermitData:      []byte{},
		}
		value = new(big.Int).Set(req.AmountInRaw)
	case ports.SideSell:
		params = swapExactInputParams{
			InputToken:      req.Route.Token,
			OutputToken:     common.Address{},
			InputAmount:     new(big.Int).Set(req.AmountInRaw),
			MinOutputAmount: new(big.Int).Set(quote.MinOutRaw),
			PermitData:      []byte{},
		}
		value = big.NewInt(0)
	default:
		return ports.UnsignedTx{}, fmt.Errorf("unsupported side %q", req.Side)
	}
	calldata, err := portalABI.Pack("swapExactInput", params)
	if err != nil {
		return ports.UnsignedTx{}, err
	}
	return ports.UnsignedTx{
		To:               target,
		Value:            value,
		Calldata:         calldata,
		Spender:          target,
		ContractDeadline: 0, // APPLICATION_TTL_ONLY
		Summary:          string(req.Side) + ":" + ProtocolID,
	}, nil
}

// DecodeEffects computes net token delta from ERC-20 Transfer logs and native BNB delta from
// FlapPortal TokenBought/TokenSold events.
func (a *Adapter) DecodeEffects(route ports.ResolvedRoute, wallet common.Address, side ports.Side, receipt *types.Receipt) (ports.DecodedEffects, error) {
	if receipt == nil || wallet == (common.Address{}) || route.Token == (common.Address{}) {
		return ports.DecodedEffects{}, errors.New("invalid decode inputs")
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
			continue
		}
		// TokenBought / TokenSold: 7 non-indexed 32-byte words:
		// [0] ts, [1] token, [2] buyer/seller, [3] amount, [4] eth, [5] fee, [6] postPrice
		if lg.Address == a.portal && len(lg.Data) >= 7*32 {
			evToken := common.BytesToAddress(lg.Data[1*32 : 2*32])
			evAccount := common.BytesToAddress(lg.Data[2*32 : 3*32])
			if evToken != route.Token || evAccount != wallet {
				continue
			}
			evEth := new(big.Int).SetBytes(lg.Data[4*32 : 5*32])
			switch lg.Topics[0] {
			case TokenBoughtTopic:
				netNative.Sub(netNative, evEth)
			case TokenSoldTopic:
				netNative.Add(netNative, evEth)
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
