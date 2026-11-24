// Package v2curve implements the side-effect-free ProtocolAdapter for Robinhood
// Chain Pons v2 Curve (chain_id=4663), preserving APPLICATION_TTL_ONLY and v1 facts.
package v2curve

import (
	"context"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

const (
	ProtocolID      = "pons-v2-curve"
	AdapterRevision = "pons-v2-curve-v1"
)

// ERC-20 Transfer(address indexed from, address indexed to, uint256 value) topic0.
var erc20TransferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

// Adapter is a read-only, pure-construction adapter for Pons v2 Curve.
type Adapter struct {
	chainID uint64
}

func NewAdapter() *Adapter {
	return &Adapter{chainID: 4663}
}

func (a *Adapter) Describe() ports.AdapterDescriptor {
	return ports.AdapterDescriptor{
		ChainID:              a.chainID,
		ProtocolID:           ProtocolID,
		AdapterRevision:      AdapterRevision,
		PoolPhase:            "CURVE",
		QuoteAsset:           "ETH",
		SupportsBuy:          true,
		SupportsSell:         true,
		AllowanceModel:       ports.AllowanceERC20Direct,
		FeeOnTransferSupport: true,
		DeadlineCapability:   ports.DeadlineApplicationTTLOnly,
	}
}

func (a *Adapter) Resolve(ctx context.Context, rpc ports.ReadOnlyRPC, token common.Address, blockNumber uint64, blockHash common.Hash) (ports.ResolvedRoute, error) {
	if rpc == nil || token == (common.Address{}) || blockHash == (common.Hash{}) {
		return ports.ResolvedRoute{}, errors.New("invalid resolve inputs")
	}
	code, err := rpc.CodeAt(ctx, token, blockHash)
	if err != nil || len(code) == 0 {
		return ports.ResolvedRoute{}, errors.New("token has no runtime code at anchor block")
	}
	return ports.ResolvedRoute{
		ChainID:            a.chainID,
		ProtocolID:         ProtocolID,
		AdapterRevision:    AdapterRevision,
		DeploymentRevision: "pons-v2-mainnet",
		PoolPhase:          "CURVE",
		Token:              token,
		RuntimeCodeHash:    crypto.Keccak256Hash(code),
		QuoteBlockNumber:   blockNumber,
		QuoteBlockHash:     blockHash,
	}, nil
}

func (a *Adapter) Quote(_ context.Context, _ ports.ReadOnlyRPC, req ports.QuoteRequest) (ports.QuoteResult, error) {
	if req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || req.SlippageBps > 10000 {
		return ports.QuoteResult{}, errors.New("invalid quote request")
	}
	// Exact quoting delegates to validated chainquery / SDK curve math in Step 1A/2A callers;
	// this pure helper computes slippage floor consistently.
	minOut := new(big.Int).Mul(req.AmountInRaw, new(big.Int).SetUint64(10000-req.SlippageBps))
	minOut.Div(minOut, big.NewInt(10000))
	return ports.QuoteResult{
		ExpectedOutRaw:   new(big.Int).Set(req.AmountInRaw),
		MinOutRaw:        minOut,
		QuoteBlockNumber: req.Route.QuoteBlockNumber,
		QuoteBlockHash:   req.Route.QuoteBlockHash,
	}, nil
}

func (a *Adapter) BuildUnsigned(req ports.QuoteRequest, quote ports.QuoteResult) (ports.UnsignedTx, error) {
	if req.Route.PoolOrCurve == (common.Address{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 {
		return ports.UnsignedTx{}, errors.New("incomplete route or input for unsigned build")
	}
	if req.ContractExpiry != 0 {
		return ports.UnsignedTx{}, errors.New("pons-v2-curve only supports APPLICATION_TTL_ONLY (contract_deadline must be 0)")
	}
	val := big.NewInt(0)
	if req.Side == ports.SideBuy {
		val = new(big.Int).Set(req.AmountInRaw)
	}
	return ports.UnsignedTx{
		To:               req.Route.PoolOrCurve,
		Value:            val,
		Spender:          req.Route.RouterOrSpender,
		ContractDeadline: 0,
		Summary:          string(req.Side) + ":" + ProtocolID,
	}, nil
}

// DecodeEffects computes the wallet's exact net ERC-20 token delta from Transfer logs
// in the verified receipt, ensuring accurate position accounting even for Fee-on-Transfer tokens.
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
