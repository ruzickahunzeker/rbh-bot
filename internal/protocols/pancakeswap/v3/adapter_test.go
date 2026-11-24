package v3

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

type stubV3RPC struct {
	poolAddr common.Address
	poolTier uint32
	quoteOut *big.Int
}

func (s *stubV3RPC) CodeAt(_ context.Context, addr common.Address, _ common.Hash) ([]byte, error) {
	if addr == (common.Address{}) {
		return nil, nil
	}
	return []byte{0x60, 0x80}, nil
}

func (s *stubV3RPC) CallContractAt(_ context.Context, to common.Address, data []byte, _ common.Hash) ([]byte, error) {
	if to == bsc.PancakeV3Factory && len(data) >= 4 && bytes.Equal(data[:4], factoryABI.Methods["getPool"].ID) {
		unpacked, err := factoryABI.Methods["getPool"].Inputs.Unpack(data[4:])
		if err != nil {
			return nil, err
		}
		tier := unpacked[2].(*big.Int).Uint64()
		if uint32(tier) == s.poolTier {
			return factoryABI.Methods["getPool"].Outputs.Pack(s.poolAddr)
		}
		return factoryABI.Methods["getPool"].Outputs.Pack(common.Address{})
	}
	if to == bsc.PancakeV3QuoterV2 && len(data) >= 4 && bytes.Equal(data[:4], quoterABI.Methods["quoteExactInputSingle"].ID) {
		return quoterABI.Methods["quoteExactInputSingle"].Outputs.Pack(
			s.quoteOut,
			big.NewInt(1),
			uint32(1),
			big.NewInt(180_000),
		)
	}
	return nil, nil
}

func TestPancakeV3AdapterResolveQuoteBuildAndDecode(t *testing.T) {
	adapter := NewAdapter()
	token := common.HexToAddress("0x000000000000000000000000000000000000BEEF")
	wallet := common.HexToAddress("0x000000000000000000000000000000000000CAFE")
	pool := common.HexToAddress("0x000000000000000000000000000000000000F003")
	blockHash := common.HexToHash("0xabcd")

	rpc := &stubV3RPC{
		poolAddr: pool,
		poolTier: 500, // 0.05% pool
		quoteOut: big.NewInt(500_000),
	}
	route, err := adapter.Resolve(context.Background(), rpc, token, 42_000_000, blockHash)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if route.PoolOrCurve != pool || route.RouterOrSpender != bsc.PancakeV3SmartRouter {
		t.Fatalf("unexpected resolved V3 route: %+v", route)
	}

	req := ports.QuoteRequest{
		Route:          route,
		Side:           ports.SideBuy,
		AmountInRaw:    big.NewInt(1_000_000_000_000_000),
		SlippageBps:    200, // 2%
		Recipient:      wallet,
		ContractExpiry: 1735689600,
	}
	quote, err := adapter.Quote(context.Background(), rpc, req)
	if err != nil {
		t.Fatalf("Quote failed: %v", err)
	}
	if quote.ExpectedOutRaw.Cmp(big.NewInt(500_000)) != 0 || quote.MinOutRaw.Cmp(big.NewInt(490_000)) != 0 || quote.FeeBps != 5 {
		t.Fatalf("unexpected V3 quote: %+v", quote)
	}

	tx, err := adapter.BuildUnsigned(req, quote)
	if err != nil || tx.To != bsc.PancakeV3SmartRouter || tx.Value.Cmp(req.AmountInRaw) != 0 {
		t.Fatalf("BuildUnsigned failed: tx=%+v err=%v", tx, err)
	}
	reqNoDeadline := req
	reqNoDeadline.ContractExpiry = 0
	if _, err := adapter.BuildUnsigned(reqNoDeadline, quote); err == nil {
		t.Fatal("expected V3 BuildUnsigned to reject zero deadline")
	}

	receipt := &types.Receipt{
		Status:    types.ReceiptStatusSuccessful,
		BlockHash: blockHash,
		Logs: []*types.Log{
			{
				Address: token,
				Topics:  []common.Hash{erc20TransferTopic, common.BytesToHash(pool.Bytes()), common.BytesToHash(wallet.Bytes())},
				Data:    common.LeftPadBytes(big.NewInt(495_000).Bytes(), 32),
			},
			{
				Address: bsc.WBNBAddress,
				Topics:  []common.Hash{wbnbDepositTopic, common.BytesToHash(bsc.PancakeV3SmartRouter.Bytes())},
				Data:    common.LeftPadBytes(req.AmountInRaw.Bytes(), 32),
			},
		},
	}
	eff, err := adapter.DecodeEffects(route, wallet, ports.SideBuy, receipt)
	if err != nil {
		t.Fatalf("DecodeEffects failed: %v", err)
	}
	if eff.NetTokenDeltaRaw.Cmp(big.NewInt(495_000)) != 0 || eff.NetNativeDeltaWei.Cmp(new(big.Int).Neg(req.AmountInRaw)) != 0 {
		t.Fatalf("unexpected V3 DecodeEffects: token=%s native=%s", eff.NetTokenDeltaRaw, eff.NetNativeDeltaWei)
	}
}
