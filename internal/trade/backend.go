package trade

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	pons "github.com/0xfnzero/rbh-trade-sdk/adapters/pons"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

type CurveBackend interface {
	Snapshot(context.Context) (BlockRef, error)
	ResolveCurve(context.Context, common.Address, BlockRef) (CurveRoute, error)
	QuoteBuy(context.Context, CurveRoute, *big.Int, common.Address, BlockRef, uint64) (*big.Int, *big.Int, error)
	QuoteSell(context.Context, CurveRoute, *big.Int, BlockRef, uint64) (*big.Int, *big.Int, error)
	TokenBalance(context.Context, common.Address, common.Address, BlockRef) (*big.Int, error)
	Simulate(context.Context, common.Address, common.Address, *big.Int, []byte, BlockRef) ([]byte, error)
	VerifySnapshot(context.Context, BlockRef) error
}

type RPCBackend struct {
	eth    *ethclient.Client
	client *pons.Client
}

func DialRPCBackend(ctx context.Context, rpcURL string) (*RPCBackend, error) {
	if ctx == nil || rpcURL == "" {
		return nil, ErrInvalidRequest
	}
	client, eth, err := pons.Dial(ctx, rpcURL)
	if err != nil {
		return nil, err
	}
	return &RPCBackend{eth: eth, client: client}, nil
}

func (b *RPCBackend) Close() {
	if b != nil && b.eth != nil {
		b.eth.Close()
	}
}

func (b *RPCBackend) Snapshot(ctx context.Context) (BlockRef, error) {
	return b.SnapshotAt(ctx, nil)
}

func (b *RPCBackend) SnapshotAt(ctx context.Context, number *big.Int) (BlockRef, error) {
	if b == nil || b.eth == nil {
		return BlockRef{}, ErrInvalidRequest
	}
	header, err := b.eth.HeaderByNumber(ctx, number)
	if err != nil {
		return BlockRef{}, err
	}
	return BlockRef{Number: header.Number.Uint64(), Hash: header.Hash(), Time: time.Unix(int64(header.Time), 0).UTC()}, nil
}

func (b *RPCBackend) ResolveCurve(ctx context.Context, token common.Address, block BlockRef) (CurveRoute, error) {
	launched, err := b.client.GetLaunchedToken(ctx, token, &bind.CallOpts{BlockNumber: new(big.Int).SetUint64(block.Number)})
	if err != nil {
		return CurveRoute{}, err
	}
	if !launched.Exists || launched.Token != token || launched.Curve == (common.Address{}) || launched.Phase != uint8(pons.PhaseNotGraduated) {
		return CurveRoute{}, ErrUnknownRoute
	}
	return CurveRoute{Protocol: "pons-v2-curve", Token: token, Curve: launched.Curve, QuoteToken: launched.PairToken, NativeQuote: pons.IsNativeQuote(launched.PairToken)}, nil
}

func (b *RPCBackend) QuoteBuy(ctx context.Context, route CurveRoute, amount *big.Int, recipient common.Address, block BlockRef, slippage uint64) (*big.Int, *big.Int, error) {
	quote, err := b.client.QuoteBuy(ctx, route.Curve, amount, recipient, &bind.CallOpts{BlockNumber: new(big.Int).SetUint64(block.Number)})
	if err != nil {
		return nil, nil, err
	}
	minimum, err := pons.MinTokensOutForBuy(amount, quote, slippage)
	if err != nil {
		return nil, nil, err
	}
	return new(big.Int).Set(quote.TokensOut), minimum, nil
}

func (b *RPCBackend) QuoteSell(ctx context.Context, route CurveRoute, amount *big.Int, block BlockRef, slippage uint64) (*big.Int, *big.Int, error) {
	if slippage > 10_000 {
		return nil, nil, ErrInvalidRequest
	}
	quote, err := b.client.QuoteSell(ctx, route.Curve, amount, &bind.CallOpts{BlockNumber: new(big.Int).SetUint64(block.Number)})
	if err != nil {
		return nil, nil, err
	}
	minimum := new(big.Int).Mul(quote.QuoteOut, new(big.Int).SetUint64(10_000-slippage))
	minimum.Div(minimum, big.NewInt(10_000))
	if minimum.Sign() <= 0 {
		return nil, nil, ErrInvalidRequest
	}
	return new(big.Int).Set(quote.QuoteOut), minimum, nil
}

func (b *RPCBackend) TokenBalance(ctx context.Context, token, owner common.Address, block BlockRef) (*big.Int, error) {
	selector := crypto.Keccak256([]byte("balanceOf(address)"))[:4]
	data := make([]byte, 4+32)
	copy(data, selector)
	copy(data[4+12:], owner.Bytes())
	result, err := b.eth.CallContract(ctx, ethereum.CallMsg{To: &token, Data: data}, new(big.Int).SetUint64(block.Number))
	if err != nil {
		return nil, err
	}
	if len(result) != 32 {
		return nil, errors.New("invalid ERC-20 balance response")
	}
	return new(big.Int).SetBytes(result), nil
}

func (b *RPCBackend) Simulate(ctx context.Context, from, to common.Address, value *big.Int, data []byte, block BlockRef) ([]byte, error) {
	result, err := b.eth.CallContract(ctx, ethereum.CallMsg{From: from, To: &to, Value: new(big.Int).Set(value), Data: append([]byte(nil), data...)}, new(big.Int).SetUint64(block.Number))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSimulationReverted, err)
	}
	return result, nil
}

func (b *RPCBackend) VerifySnapshot(ctx context.Context, block BlockRef) error {
	header, err := b.eth.HeaderByNumber(ctx, new(big.Int).SetUint64(block.Number))
	if err != nil {
		return err
	}
	if header.Hash() != block.Hash {
		return ErrStaleState
	}
	return nil
}
