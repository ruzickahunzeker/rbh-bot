// Package v2 implements a pure, side-effect-free ProtocolAdapter for BSC PancakeSwap V2
// (chain_id=56) with Fee-on-Transfer support and mandatory CONTRACT_DEADLINE_UNIX.
// It holds no signer, broadcaster, nonce allocator, or DB write handle.
package v2

import (
	"context"
	"errors"
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
  {"name":"swapExactETHForTokensSupportingFeeOnTransferTokens","type":"function","inputs":[{"name":"amountOutMin","type":"uint256"},{"name":"path","type":"address[]"},{"name":"to","type":"address"},{"name":"deadline","type":"uint256"}],"outputs":[]},
  {"name":"swapExactTokensForETHSupportingFeeOnTransferTokens","type":"function","inputs":[{"name":"amountIn","type":"uint256"},{"name":"amountOutMin","type":"uint256"},{"name":"path","type":"address[]"},{"name":"to","type":"address"},{"name":"deadline","type":"uint256"}],"outputs":[]}
]`

const erc20ApproveABIJSON = `[
  {"name":"approve","type":"function","inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]}
]`

var (
	routerABI          = mustParseABI(pancakeV2RouterABIJSON)
	erc20ABI           = mustParseABI(erc20ApproveABIJSON)
	erc20TransferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
)

func mustParseABI(src string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(src))
	if err != nil {
		panic(err)
	}
	return parsed
}

type Adapter struct {
	chainID uint64
	router  common.Address
	wbnb    common.Address
}

func NewAdapter() *Adapter {
	return &Adapter{
		chainID: bsc.ChainID,
		router:  bsc.PancakeV2Router,
		wbnb:    bsc.WBNBAddress,
	}
}

func (a *Adapter) Describe() ports.AdapterDescriptor {
	return ports.AdapterDescriptor{
		ChainID:              a.chainID,
		ProtocolID:           ProtocolID,
		AdapterRevision:      AdapterRevision,
		PoolPhase:            "DEX_V2",
		QuoteAsset:           "BNB",
		SupportsBuy:          true,
		SupportsSell:         true,
		AllowanceModel:       ports.AllowanceERC20Direct,
		FeeOnTransferSupport: true,
		DeadlineCapability:   ports.DeadlineContractDeadlineUnix,
	}
}

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
	return ports.ResolvedRoute{
		ChainID:            a.chainID,
		ProtocolID:         ProtocolID,
		AdapterRevision:    AdapterRevision,
		DeploymentRevision: "pancakeswap-v2-bsc-mainnet",
		PoolPhase:          "DEX_V2",
		Token:              token,
		PoolOrCurve:        a.router,
		RouterOrSpender:    a.router,
		RuntimeCodeHash:    crypto.Keccak256Hash(routerCode),
		QuoteBlockNumber:   blockNumber,
		QuoteBlockHash:     blockHash,
	}, nil
}

func (a *Adapter) Quote(_ context.Context, _ ports.ReadOnlyRPC, req ports.QuoteRequest) (ports.QuoteResult, error) {
	if req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || req.SlippageBps > 10000 {
		return ports.QuoteResult{}, errors.New("invalid quote request")
	}
	minOut := new(big.Int).Mul(req.AmountInRaw, new(big.Int).SetUint64(10000-req.SlippageBps))
	minOut.Div(minOut, big.NewInt(10000))
	return ports.QuoteResult{
		ExpectedOutRaw:   new(big.Int).Set(req.AmountInRaw),
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
	switch req.Side {
	case ports.SideBuy:
		path := []common.Address{a.wbnb, req.Route.Token}
		calldata, err = routerABI.Pack("swapExactETHForTokensSupportingFeeOnTransferTokens", minOut, path, req.Recipient, deadline)
		value = new(big.Int).Set(req.AmountInRaw)
	case ports.SideSell:
		path := []common.Address{req.Route.Token, a.wbnb}
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
		Summary:          string(req.Side) + ":" + ProtocolID,
	}, nil
}

// BuildApproveCalldata builds pure unsigned ERC-20 approve(spender, amount) calldata.
func BuildApproveCalldata(spender common.Address, amountRaw *big.Int) ([]byte, error) {
	if spender == (common.Address{}) || amountRaw == nil || amountRaw.Sign() < 0 {
		return nil, errors.New("invalid approve inputs")
	}
	return erc20ABI.Pack("approve", spender, amountRaw)
}

// DecodeEffects computes the wallet's exact net ERC-20 Transfer delta from receipt logs
// (accurate for BSC Fee-on-Transfer tax tokens).
func (a *Adapter) DecodeEffects(route ports.ResolvedRoute, wallet common.Address, side ports.Side, receipt *types.Receipt) (ports.DecodedEffects, error) {
	if receipt == nil || wallet == (common.Address{}) || route.Token == (common.Address{}) {
		return ports.DecodedEffects{}, errors.New("invalid decode inputs")
	}
	netToken := big.NewInt(0)
	for _, lg := range receipt.Logs {
		if lg == nil || lg.Address != route.Token || len(lg.Topics) != 3 || lg.Topics[0] != erc20TransferTopic || len(lg.Data) != 32 {
			continue
		}
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
	return ports.DecodedEffects{
		WalletAddress:      wallet,
		TokenAddress:       route.Token,
		Side:               side,
		NetNativeDeltaWei:  big.NewInt(0),
		NetTokenDeltaRaw:   netToken,
		EvidenceComplete:   receipt.Status == types.ReceiptStatusSuccessful,
		CanonicalBlockHash: receipt.BlockHash,
	}, nil
}
