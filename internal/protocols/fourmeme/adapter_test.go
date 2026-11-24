package fourmeme

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

type stubFourRPC struct {
	graduated bool
	buyOut    *big.Int
	sellFunds *big.Int
	sellFee   *big.Int
}

func (s *stubFourRPC) CodeAt(_ context.Context, addr common.Address, _ common.Hash) ([]byte, error) {
	if addr == (common.Address{}) {
		return nil, nil
	}
	return []byte{0x60, 0x80}, nil
}

func (s *stubFourRPC) CallContractAt(_ context.Context, to common.Address, data []byte, _ common.Hash) ([]byte, error) {
	if to != bsc.FourMemeHelper3 || len(data) < 4 {
		return nil, nil
	}
	switch {
	case bytes.Equal(data[:4], helper3ABI.Methods["getTokenInfo"].ID):
		return helper3ABI.Methods["getTokenInfo"].Outputs.Pack(
			big.NewInt(2),
			bsc.FourMemeTokenManager2,
			common.Address{}, // native BNB quote
			big.NewInt(1000),
			big.NewInt(100),
			big.NewInt(0),
			big.NewInt(1700000000),
			big.NewInt(1000000),
			big.NewInt(8000000),
			big.NewInt(5000000),
			big.NewInt(24000000),
			s.graduated,
		)
	case bytes.Equal(data[:4], helper3ABI.Methods["tryBuy"].ID):
		return helper3ABI.Methods["tryBuy"].Outputs.Pack(
			bsc.FourMemeTokenManager2,
			common.Address{},
			s.buyOut,
			big.NewInt(990_000_000_000_000),
			big.NewInt(10_000_000_000_000),
			big.NewInt(1_000_000_000_000_000),
			big.NewInt(0),
			big.NewInt(1_000_000_000_000_000),
		)
	case bytes.Equal(data[:4], helper3ABI.Methods["trySell"].ID):
		return helper3ABI.Methods["trySell"].Outputs.Pack(
			bsc.FourMemeTokenManager2,
			common.Address{},
			s.sellFunds,
			s.sellFee,
		)
	}
	return nil, nil
}

func TestFourMemeResolveGraduationQuoteBuildAndDecode(t *testing.T) {
	adapter := NewAdapter()
	token := common.HexToAddress("0x0000000000000000000000000000000000004444")
	wallet := common.HexToAddress("0x000000000000000000000000000000000000CAFE")
	blockHash := common.HexToHash("0x4444")

	// AT-18: Graduated token must fail closed with ErrTokenGraduated.
	if _, err := adapter.Resolve(context.Background(), &stubFourRPC{graduated: true}, token, 45_000_000, blockHash); !errors.Is(err, ErrTokenGraduated) {
		t.Fatalf("expected ErrTokenGraduated for graduated curve, got %v", err)
	}

	rpc := &stubFourRPC{
		graduated: false,
		buyOut:    big.NewInt(5_000_000),
		sellFunds: big.NewInt(2_000_000_000_000_000),
		sellFee:   big.NewInt(20_000_000_000_000),
	}
	route, err := adapter.Resolve(context.Background(), rpc, token, 45_000_000, blockHash)
	if err != nil || route.RouterOrSpender != bsc.FourMemeTokenManager2 {
		t.Fatalf("Resolve failed: route=%+v err=%v", route, err)
	}

	buyReq := ports.QuoteRequest{
		Route:       route,
		Side:        ports.SideBuy,
		AmountInRaw: big.NewInt(1_000_000_000_000_000),
		SlippageBps: 200,
		Recipient:   wallet,
	}
	buyQuote, err := adapter.Quote(context.Background(), rpc, buyReq)
	if err != nil || buyQuote.ExpectedOutRaw.Cmp(big.NewInt(5_000_000)) != 0 || buyQuote.MinOutRaw.Cmp(big.NewInt(4_900_000)) != 0 {
		t.Fatalf("buy Quote failed: %+v err=%v", buyQuote, err)
	}
	buyTx, err := adapter.BuildUnsigned(buyReq, buyQuote)
	if err != nil || buyTx.To != bsc.FourMemeTokenManager2 || buyTx.Value.Cmp(buyReq.AmountInRaw) != 0 {
		t.Fatalf("BuildUnsigned buy failed: %+v err=%v", buyTx, err)
	}

	sellReq := buyReq
	sellReq.Side = ports.SideSell
	sellReq.AmountInRaw = big.NewInt(5_000_000)
	sellQuote, err := adapter.Quote(context.Background(), rpc, sellReq)
	if err != nil || sellQuote.ExpectedOutRaw.Cmp(big.NewInt(1_980_000_000_000_000)) != 0 {
		t.Fatalf("sell Quote failed: %+v err=%v", sellQuote, err)
	}
	sellTx, err := adapter.BuildUnsigned(sellReq, sellQuote)
	if err != nil || sellTx.Value.Sign() != 0 {
		t.Fatalf("BuildUnsigned sell failed: %+v err=%v", sellTx, err)
	}

	// DecodeEffects: TokenSale event + ERC-20 Transfer out.
	var saleData []byte
	saleData = append(saleData, common.LeftPadBytes(token.Bytes(), 32)...)
	saleData = append(saleData, common.LeftPadBytes(wallet.Bytes(), 32)...)
	saleData = append(saleData, common.LeftPadBytes(big.NewInt(400).Bytes(), 32)...)
	saleData = append(saleData, common.LeftPadBytes(big.NewInt(5_000_000).Bytes(), 32)...)
	saleData = append(saleData, common.LeftPadBytes(rpc.sellFunds.Bytes(), 32)...)
	saleData = append(saleData, common.LeftPadBytes(rpc.sellFee.Bytes(), 32)...)
	saleData = append(saleData, common.LeftPadBytes(big.NewInt(0).Bytes(), 32)...)
	saleData = append(saleData, common.LeftPadBytes(big.NewInt(0).Bytes(), 32)...)

	receipt := &types.Receipt{
		Status:    types.ReceiptStatusSuccessful,
		BlockHash: blockHash,
		Logs: []*types.Log{
			{
				Address: token,
				Topics:  []common.Hash{erc20TransferTopic, common.BytesToHash(wallet.Bytes()), common.BytesToHash(bsc.FourMemeTokenManager2.Bytes())},
				Data:    common.LeftPadBytes(big.NewInt(5_000_000).Bytes(), 32),
			},
			{
				Address: bsc.FourMemeTokenManager2,
				Topics:  []common.Hash{TokenSaleTopic},
				Data:    saleData,
			},
		},
	}
	eff, err := adapter.DecodeEffects(route, wallet, ports.SideSell, receipt)
	if err != nil || eff.NetTokenDeltaRaw.Cmp(big.NewInt(-5_000_000)) != 0 || eff.NetNativeDeltaWei.Cmp(big.NewInt(1_980_000_000_000_000)) != 0 {
		t.Fatalf("unexpected DecodeEffects: %+v err=%v", eff, err)
	}
}
