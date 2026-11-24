package flap

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

type fakeRPC struct {
	tokenStatus uint8
	quoteOut    *big.Int
}

func (f *fakeRPC) CodeAt(_ context.Context, _ common.Address, _ common.Hash) ([]byte, error) {
	return []byte{0x60, 0x00}, nil
}

func (f *fakeRPC) CallContractAt(_ context.Context, to common.Address, data []byte, _ common.Hash) ([]byte, error) {
	if to != bsc.FlapPortal || len(data) < 4 {
		return nil, errors.New("unexpected call target")
	}
	switch {
	case bytes.Equal(data[:4], portalABI.Methods["getTokenV2"].ID):
		state := tokenStateV2{
			Status:            f.tokenStatus,
			Reserve:           big.NewInt(1e18),
			CirculatingSupply: big.NewInt(1e18),
			Price:             big.NewInt(1e12),
			TokenVersion:      2,
			R:                 big.NewInt(1),
			H:                 big.NewInt(1),
			K:                 big.NewInt(1),
			DexSupplyThresh:   big.NewInt(8e18),
			QuoteTokenAddress: common.Address{},
		}
		return portalABI.Methods["getTokenV2"].Outputs.Pack(state)
	case bytes.Equal(data[:4], portalABI.Methods["quoteExactInput"].ID):
		return portalABI.Methods["quoteExactInput"].Outputs.Pack(f.quoteOut)
	default:
		return nil, errors.New("unknown selector")
	}
}

func TestFlapAdapterResolveQuoteBuildDecode(t *testing.T) {
	adp := NewAdapter()
	token := common.HexToAddress("0x2222222222222222222222222222222222222222")
	wallet := common.HexToAddress("0x3333333333333333333333333333333333333333")
	blockHash := common.HexToHash("0xabcdef")

	// Graduated token must fail closed with ErrTokenGraduated.
	gradRPC := &fakeRPC{tokenStatus: TokenStatusDEX, quoteOut: big.NewInt(1000)}
	if _, err := adp.Resolve(context.Background(), gradRPC, token, 100, blockHash); !errors.Is(err, ErrTokenGraduated) {
		t.Fatalf("expected ErrTokenGraduated, got %v", err)
	}

	// Active bonding curve token resolves, quotes, builds buy & sell, and decodes effects.
	rpc := &fakeRPC{tokenStatus: TokenStatusTradable, quoteOut: big.NewInt(5_000_000)}
	route, err := adp.Resolve(context.Background(), rpc, token, 100, blockHash)
	if err != nil {
		t.Fatalf("Resolve active token: %v", err)
	}
	if route.PoolPhase != "BONDING_CURVE" || route.RouterOrSpender != bsc.FlapPortal {
		t.Fatalf("unexpected route: %+v", route)
	}

	buyReq := ports.QuoteRequest{
		Route:       route,
		Side:        ports.SideBuy,
		AmountInRaw: big.NewInt(1e16),
		SlippageBps: 200,
	}
	buyQuote, err := adp.Quote(context.Background(), rpc, buyReq)
	if err != nil {
		t.Fatalf("Quote buy: %v", err)
	}
	if buyQuote.MinOutRaw.Cmp(big.NewInt(4_900_000)) != 0 {
		t.Fatalf("unexpected buy minOut: %s", buyQuote.MinOutRaw)
	}
	buyTx, err := adp.BuildUnsigned(buyReq, buyQuote)
	if err != nil {
		t.Fatalf("BuildUnsigned buy: %v", err)
	}
	if buyTx.Value.Cmp(big.NewInt(1e16)) != 0 || buyTx.To != bsc.FlapPortal {
		t.Fatalf("unexpected buy tx: to=%s value=%s", buyTx.To.Hex(), buyTx.Value)
	}

	sellReq := ports.QuoteRequest{
		Route:       route,
		Side:        ports.SideSell,
		AmountInRaw: big.NewInt(4_900_000),
		SlippageBps: 100,
	}
	sellQuote, err := adp.Quote(context.Background(), rpc, sellReq)
	if err != nil {
		t.Fatalf("Quote sell: %v", err)
	}
	sellTx, err := adp.BuildUnsigned(sellReq, sellQuote)
	if err != nil {
		t.Fatalf("BuildUnsigned sell: %v", err)
	}
	if sellTx.Value.Sign() != 0 {
		t.Fatalf("expected 0 msg.value on sell, got %s", sellTx.Value)
	}

	// DecodeEffects on TokenBought + ERC-20 Transfer
	boughtData := make([]byte, 7*32)
	copy(boughtData[1*32+12:2*32], token.Bytes())
	copy(boughtData[2*32+12:3*32], wallet.Bytes())
	big.NewInt(1e16).FillBytes(boughtData[4*32 : 5*32])

	transferData := make([]byte, 32)
	big.NewInt(5_000_000).FillBytes(transferData)

	rcpt := &types.Receipt{
		Status:    types.ReceiptStatusSuccessful,
		BlockHash: blockHash,
		GasUsed:   140_000,
		Logs: []*types.Log{
			{
				Address: token,
				Topics: []common.Hash{
					erc20TransferTopic,
					common.BytesToHash(bsc.FlapPortal.Bytes()),
					common.BytesToHash(wallet.Bytes()),
				},
				Data: transferData,
			},
			{
				Address: bsc.FlapPortal,
				Topics:  []common.Hash{TokenBoughtTopic},
				Data:    boughtData,
			},
		},
	}
	eff, err := adp.DecodeEffects(route, wallet, ports.SideBuy, rcpt)
	if err != nil {
		t.Fatalf("DecodeEffects: %v", err)
	}
	if !eff.EvidenceComplete || eff.NetTokenDeltaRaw.Cmp(big.NewInt(5_000_000)) != 0 || eff.NetNativeDeltaWei.Cmp(big.NewInt(-1e16)) != 0 {
		t.Fatalf("unexpected effects: %+v", eff)
	}
}
