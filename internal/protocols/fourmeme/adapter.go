// Package fourmeme implements a pure, side-effect-free ProtocolAdapter for BSC Four.meme
// (TokenManager2 V2 bonding curve, chain_id=56) with graduation fail-closed detection
// and TokenManagerHelper3 block-pinned quoting. Holds no signer, broadcaster, or DB handle.
package fourmeme

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
	ProtocolID      = "four-meme"
	AdapterRevision = "four-meme-v2-bsc-r1"
)

var (
	// ErrTokenGraduated indicates the bonding curve has migrated to PancakeSwap DEX (AT-18).
	ErrTokenGraduated = errors.New("four-meme token has graduated to DEX; route via pancake-v2/v3")

	helper3ABI       = mustParseABI(fourMemeHelper3ABIJSON)
	tokenManager2ABI = mustParseABI(fourMemeTokenManager2ABIJSON)

	erc20TransferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	// TokenPurchase(address token, address account, uint256 price, uint256 amount, uint256 cost, uint256 fee, uint256 offers, uint256 funds)
	TokenPurchaseTopic = crypto.Keccak256Hash([]byte("TokenPurchase(address,address,uint256,uint256,uint256,uint256,uint256,uint256)"))
	// TokenSale(address token, address account, uint256 price, uint256 amount, uint256 cost, uint256 fee, uint256 offers, uint256 funds)
	TokenSaleTopic = crypto.Keccak256Hash([]byte("TokenSale(address,address,uint256,uint256,uint256,uint256,uint256,uint256)"))
)

const fourMemeHelper3ABIJSON = `[
  {"name":"getTokenInfo","type":"function","stateMutability":"view","inputs":[{"name":"token","type":"address"}],"outputs":[{"name":"version","type":"uint256"},{"name":"tokenManager","type":"address"},{"name":"quote","type":"address"},{"name":"lastPrice","type":"uint256"},{"name":"tradingFeeRate","type":"uint256"},{"name":"minTradingFee","type":"uint256"},{"name":"launchTime","type":"uint256"},{"name":"offers","type":"uint256"},{"name":"maxOffers","type":"uint256"},{"name":"funds","type":"uint256"},{"name":"maxFunds","type":"uint256"},{"name":"liquidityAdded","type":"bool"}]},
  {"name":"tryBuy","type":"function","stateMutability":"view","inputs":[{"name":"token","type":"address"},{"name":"amount","type":"uint256"},{"name":"funds","type":"uint256"}],"outputs":[{"name":"tokenManager","type":"address"},{"name":"quote","type":"address"},{"name":"estimatedAmount","type":"uint256"},{"name":"estimatedCost","type":"uint256"},{"name":"estimatedFee","type":"uint256"},{"name":"amountMsgValue","type":"uint256"},{"name":"amountApproval","type":"uint256"},{"name":"amountFunds","type":"uint256"}]},
  {"name":"trySell","type":"function","stateMutability":"view","inputs":[{"name":"token","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"tokenManager","type":"address"},{"name":"quote","type":"address"},{"name":"funds","type":"uint256"},{"name":"fee","type":"uint256"}]}
]`

const fourMemeTokenManager2ABIJSON = `[
  {"name":"buyTokenAMAP","type":"function","stateMutability":"payable","inputs":[{"name":"token","type":"address"},{"name":"funds","type":"uint256"},{"name":"minAmount","type":"uint256"}],"outputs":[]},
  {"name":"sellToken","type":"function","stateMutability":"nonpayable","inputs":[{"name":"token","type":"address"},{"name":"amount","type":"uint256"},{"name":"minFunds","type":"uint256"}],"outputs":[]}
]`

func mustParseABI(src string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(src))
	if err != nil {
		panic(err)
	}
	return parsed
}

type Adapter struct {
	chainID uint64
	manager common.Address
	helper  common.Address
}

func NewAdapter() *Adapter {
	return &Adapter{
		chainID: bsc.ChainID,
		manager: bsc.FourMemeTokenManager2,
		helper:  bsc.FourMemeHelper3,
	}
}

func (a *Adapter) Describe() ports.AdapterDescriptor {
	return ports.AdapterDescriptor{
		ChainID:              a.chainID,
		ProtocolID:           ProtocolID,
		AdapterRevision:      AdapterRevision,
		PoolPhase:            "BONDING_CURVE",
		QuoteAsset:           "BNB",
		SupportsBuy:          true,
		SupportsSell:         true,
		AllowanceModel:       ports.AllowanceERC20Direct,
		FeeOnTransferSupport: false,
		DeadlineCapability:   ports.DeadlineApplicationTTLOnly,
	}
}

// Resolve queries FourMemeHelper3.getTokenInfo pinned to blockHash, rejecting graduated tokens (liquidityAdded=true).
func (a *Adapter) Resolve(ctx context.Context, rpc ports.ReadOnlyRPC, token common.Address, blockNumber uint64, blockHash common.Hash) (ports.ResolvedRoute, error) {
	if rpc == nil || token == (common.Address{}) || blockHash == (common.Hash{}) {
		return ports.ResolvedRoute{}, errors.New("invalid four-meme resolve inputs")
	}
	mgrCode, err := rpc.CodeAt(ctx, a.manager, blockHash)
	if err != nil || len(mgrCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("four-meme TokenManager2 has no runtime code")
	}
	tokCode, err := rpc.CodeAt(ctx, token, blockHash)
	if err != nil || len(tokCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("target token has no runtime code")
	}
	data, err := helper3ABI.Pack("getTokenInfo", token)
	if err != nil {
		return ports.ResolvedRoute{}, err
	}
	raw, err := rpc.CallContractAt(ctx, a.helper, data, blockHash)
	if err != nil {
		return ports.ResolvedRoute{}, fmt.Errorf("four-meme helper.getTokenInfo failed: %w", err)
	}
	out, err := helper3ABI.Unpack("getTokenInfo", raw)
	if err != nil || len(out) < 12 {
		return ports.ResolvedRoute{}, errors.New("invalid four-meme getTokenInfo response")
	}
	mgrAddr, _ := out[1].(common.Address)
	quoteAsset, _ := out[2].(common.Address)
	liquidityAdded, _ := out[11].(bool)
	if liquidityAdded {
		return ports.ResolvedRoute{}, ErrTokenGraduated
	}
	if mgrAddr == (common.Address{}) {
		return ports.ResolvedRoute{}, errors.New("token is not managed by four-meme")
	}
	if quoteAsset != (common.Address{}) {
		return ports.ResolvedRoute{}, fmt.Errorf("four-meme non-BNB quote asset %s not supported in v1", quoteAsset.Hex())
	}
	return ports.ResolvedRoute{
		ChainID:            a.chainID,
		ProtocolID:         ProtocolID,
		AdapterRevision:    AdapterRevision,
		DeploymentRevision: "four-meme-v2-bsc-mainnet",
		PoolPhase:          "BONDING_CURVE",
		Token:              token,
		PoolOrCurve:        mgrAddr,
		RouterOrSpender:    mgrAddr,
		RuntimeCodeHash:    crypto.Keccak256Hash(mgrCode),
		QuoteBlockNumber:   blockNumber,
		QuoteBlockHash:     blockHash,
	}, nil
}

// Quote queries FourMemeHelper3.tryBuy / trySell pinned to QuoteBlockHash and applies slippage.
func (a *Adapter) Quote(ctx context.Context, rpc ports.ReadOnlyRPC, req ports.QuoteRequest) (ports.QuoteResult, error) {
	if rpc == nil || req.Route.Token == (common.Address{}) || req.Route.QuoteBlockHash == (common.Hash{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || req.SlippageBps > 10000 {
		return ports.QuoteResult{}, errors.New("invalid four-meme quote request")
	}
	var expectedOut *big.Int
	switch req.Side {
	case ports.SideBuy:
		data, err := helper3ABI.Pack("tryBuy", req.Route.Token, big.NewInt(0), new(big.Int).Set(req.AmountInRaw))
		if err != nil {
			return ports.QuoteResult{}, err
		}
		raw, err := rpc.CallContractAt(ctx, a.helper, data, req.Route.QuoteBlockHash)
		if err != nil {
			return ports.QuoteResult{}, fmt.Errorf("four-meme helper.tryBuy failed: %w", err)
		}
		out, err := helper3ABI.Unpack("tryBuy", raw)
		if err != nil || len(out) < 8 {
			return ports.QuoteResult{}, errors.New("invalid four-meme tryBuy response")
		}
		est, ok := out[2].(*big.Int)
		if !ok || est == nil || est.Sign() <= 0 {
			return ports.QuoteResult{}, errors.New("four-meme tryBuy returned zero amount")
		}
		expectedOut = new(big.Int).Set(est)
	case ports.SideSell:
		data, err := helper3ABI.Pack("trySell", req.Route.Token, new(big.Int).Set(req.AmountInRaw))
		if err != nil {
			return ports.QuoteResult{}, err
		}
		raw, err := rpc.CallContractAt(ctx, a.helper, data, req.Route.QuoteBlockHash)
		if err != nil {
			return ports.QuoteResult{}, fmt.Errorf("four-meme helper.trySell failed: %w", err)
		}
		out, err := helper3ABI.Unpack("trySell", raw)
		if err != nil || len(out) < 4 {
			return ports.QuoteResult{}, errors.New("invalid four-meme trySell response")
		}
		funds, ok := out[2].(*big.Int)
		fee, _ := out[3].(*big.Int)
		if !ok || funds == nil || funds.Sign() <= 0 {
			return ports.QuoteResult{}, errors.New("four-meme trySell returned zero funds")
		}
		netFunds := new(big.Int).Set(funds)
		if fee != nil && fee.Sign() > 0 && netFunds.Cmp(fee) > 0 {
			netFunds.Sub(netFunds, fee)
		}
		expectedOut = netFunds
	default:
		return ports.QuoteResult{}, errors.New("unsupported side")
	}

	minOut := new(big.Int).Mul(expectedOut, new(big.Int).SetUint64(10000-req.SlippageBps))
	minOut.Div(minOut, big.NewInt(10000))
	return ports.QuoteResult{
		ExpectedOutRaw:   expectedOut,
		MinOutRaw:        minOut,
		FeeBps:           100, // 1% Four.meme standard bonding-curve fee
		EstimatedGas:     260_000,
		QuoteBlockNumber: req.Route.QuoteBlockNumber,
		QuoteBlockHash:   req.Route.QuoteBlockHash,
	}, nil
}

// BuildUnsigned constructs TokenManager2.buyTokenAMAP / sellToken calldata (pure, offline).
func (a *Adapter) BuildUnsigned(req ports.QuoteRequest, quote ports.QuoteResult) (ports.UnsignedTx, error) {
	if req.Route.Token == (common.Address{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || quote.MinOutRaw == nil || quote.MinOutRaw.Sign() < 0 {
		return ports.UnsignedTx{}, errors.New("incomplete four-meme build inputs")
	}
	target := req.Route.RouterOrSpender
	if target == (common.Address{}) {
		target = a.manager
	}
	var (
		calldata []byte
		value    *big.Int
		err      error
	)
	switch req.Side {
	case ports.SideBuy:
		calldata, err = tokenManager2ABI.Pack("buyTokenAMAP", req.Route.Token, new(big.Int).Set(req.AmountInRaw), new(big.Int).Set(quote.MinOutRaw))
		value = new(big.Int).Set(req.AmountInRaw)
	case ports.SideSell:
		calldata, err = tokenManager2ABI.Pack("sellToken", req.Route.Token, new(big.Int).Set(req.AmountInRaw), new(big.Int).Set(quote.MinOutRaw))
		value = big.NewInt(0)
	default:
		return ports.UnsignedTx{}, errors.New("unsupported side")
	}
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

// DecodeEffects extracts net wallet ERC-20 Transfer delta and native BNB delta from TokenPurchase/TokenSale logs.
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
		// TokenPurchase / TokenSale non-indexed 8x32-byte data: [token, account, price, amount, cost, fee, offers, funds]
		if len(lg.Data) >= 8*32 && (lg.Topics[0] == TokenPurchaseTopic || lg.Topics[0] == TokenSaleTopic) {
			evToken := common.BytesToAddress(lg.Data[0:32])
			evAccount := common.BytesToAddress(lg.Data[32:64])
			if evToken != route.Token || evAccount != wallet {
				continue
			}
			cost := new(big.Int).SetBytes(lg.Data[4*32 : 5*32])
			fee := new(big.Int).SetBytes(lg.Data[5*32 : 6*32])
			if lg.Topics[0] == TokenPurchaseTopic {
				netNative.Sub(netNative, new(big.Int).Add(cost, fee))
			} else {
				netProceeds := new(big.Int).Sub(cost, fee)
				if netProceeds.Sign() > 0 {
					netNative.Add(netNative, netProceeds)
				}
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
