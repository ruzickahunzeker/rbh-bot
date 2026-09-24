package trade

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

var (
	ErrWalletLaneBusy = errors.New("execution wallet lane is unresolved")
	ErrNonceConflict  = errors.New("pending nonce is stale or conflicting")
	ErrReservation    = errors.New("execution reservation unavailable")
	ErrFeePolicy      = errors.New("gas or fee policy rejected")
)

type FeeParameters struct {
	GasLimit  uint64
	GasTipCap *big.Int
	GasFeeCap *big.Int
}

func (b *RPCBackend) SendRawTransaction(ctx context.Context, raw []byte) (string, error) {
	if b == nil || b.eth == nil || len(raw) == 0 {
		return "", ErrInvalidRequest
	}
	var hash common.Hash
	if err := b.eth.Client().CallContext(ctx, &hash, "eth_sendRawTransaction", hexutil.Encode(raw)); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already known") {
			var tx types.Transaction
			if tx.UnmarshalBinary(raw) != nil {
				return "", ErrArtifactIntegrity
			}
			return tx.Hash().Hex(), nil
		}
		return "", classifyBroadcastError(err)
	}
	return hash.Hex(), nil
}

func classifyBroadcastError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	for _, deterministic := range []string{"insufficient funds", "intrinsic gas", "invalid sender", "invalid transaction", "nonce too low", "chain id"} {
		if strings.Contains(message, deterministic) {
			return fmt.Errorf("%w: %v", ErrBroadcastRejected, err)
		}
	}
	return fmt.Errorf("%w: %v", ErrBroadcastAmbiguous, err)
}

type ExecutionBackend interface {
	ExecutionChainID(context.Context) (*big.Int, error)
	Snapshot(context.Context) (BlockRef, error)
	ResolveCurve(context.Context, common.Address, BlockRef) (CurveRoute, error)
	VerifySnapshot(context.Context, BlockRef) error
	PendingNonce(context.Context, common.Address) (uint64, error)
	NativeBalance(context.Context, common.Address, BlockRef) (*big.Int, error)
	TokenBalance(context.Context, common.Address, common.Address, BlockRef) (*big.Int, error)
	FeeParameters(context.Context, common.Address, UnsignedCall, BlockRef) (FeeParameters, error)
}

func (b *RPCBackend) ExecutionChainID(ctx context.Context) (*big.Int, error) {
	if b == nil || b.eth == nil {
		return nil, ErrInvalidRequest
	}
	return b.eth.ChainID(ctx)
}

func (b *RPCBackend) PendingNonce(ctx context.Context, wallet common.Address) (uint64, error) {
	if b == nil || b.eth == nil || wallet == (common.Address{}) {
		return 0, ErrInvalidRequest
	}
	return b.eth.PendingNonceAt(ctx, wallet)
}

func (b *RPCBackend) NativeBalance(ctx context.Context, wallet common.Address, block BlockRef) (*big.Int, error) {
	if b == nil || b.eth == nil || wallet == (common.Address{}) || block.Number == 0 {
		return nil, ErrInvalidRequest
	}
	return b.eth.BalanceAt(ctx, wallet, new(big.Int).SetUint64(block.Number))
}

func (b *RPCBackend) FeeParameters(ctx context.Context, from common.Address, call UnsignedCall, block BlockRef) (FeeParameters, error) {
	if b == nil || b.eth == nil || from == (common.Address{}) || !common.IsHexAddress(call.To) || block.Number == 0 {
		return FeeParameters{}, ErrInvalidRequest
	}
	value, ok := new(big.Int).SetString(call.Value, 10)
	if !ok || value.Sign() < 0 {
		return FeeParameters{}, ErrInvalidRequest
	}
	data := common.FromHex(call.Data)
	to := common.HexToAddress(call.To)
	gas, err := b.eth.EstimateGas(ctx, ethereum.CallMsg{From: from, To: &to, Value: value, Data: data})
	if err != nil {
		return FeeParameters{}, fmt.Errorf("%w: estimate gas: %v", ErrFeePolicy, err)
	}
	tip, err := b.eth.SuggestGasTipCap(ctx)
	if err != nil || tip == nil || tip.Sign() < 0 {
		return FeeParameters{}, fmt.Errorf("%w: tip: %v", ErrFeePolicy, err)
	}
	header, err := b.eth.HeaderByNumber(ctx, new(big.Int).SetUint64(block.Number))
	if err != nil || header.BaseFee == nil || header.BaseFee.Sign() < 0 {
		return FeeParameters{}, fmt.Errorf("%w: base fee: %v", ErrFeePolicy, err)
	}
	feeCap := new(big.Int).Mul(header.BaseFee, big.NewInt(2))
	feeCap.Add(feeCap, tip)
	if gas == 0 || feeCap.Sign() <= 0 {
		return FeeParameters{}, ErrFeePolicy
	}
	return FeeParameters{GasLimit: gas, GasTipCap: new(big.Int).Set(tip), GasFeeCap: feeCap}, nil
}
