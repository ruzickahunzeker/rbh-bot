package v3

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	basechain "github.com/ruzickahunzeker/rbh-bot/internal/chains/base"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

type stubBaseV3RPC struct {
	poolAddr common.Address
	quoteOut *big.Int
}

func (s *stubBaseV3RPC) CodeAt(_ context.Context, addr common.Address, _ common.Hash) ([]byte, error) {
	if addr == (common.Address{}) {
		return nil, nil
	}
	return []byte{0x60, 0x80}, nil
}

func (s *stubBaseV3RPC) CallContractAt(_ context.Context, to common.Address, data []byte, _ common.Hash) ([]byte, error) {
	if to == basechain.UniswapV3Factory && len(data) >= 4 && bytes.Equal(data[:4], factoryABI.Methods["getPool"].ID) {
		return factoryABI.Methods["getPool"].Outputs.Pack(s.poolAddr)
	}
	if to == basechain.UniswapV3QuoterV2 && len(data) >= 4 && bytes.Equal(data[:4], quoterABI.Methods["quoteExactInputSingle"].ID) {
		return quoterABI.Methods["quoteExactInputSingle"].Outputs.Pack(s.quoteOut, big.NewInt(1), uint32(1), big.NewInt(160_000))
	}
	return nil, nil
}

func TestBaseUniswapV3AdapterResolveQuoteBuildAndDecode(t *testing.T) {
	adapter := NewBaseAdapter()
	token := common.HexToAddress("0x000000000000000000000000000000000000BEEF")
	wallet := common.HexToAddress("0x000000000000000000000000000000000000CAFE")
	pool := common.HexToAddress("0x0000000000000000000000000000000000008453")
	blockHash := common.HexToHash("0x8453")

	rpc := &stubBaseV3RPC{poolAddr: pool, quoteOut: big.NewInt(800_000)}
	route, err := adapter.Resolve(context.Background(), rpc, token, 22_000_000, blockHash)
	if err != nil || route.PoolOrCurve != pool || route.ChainID != 8453 {
		t.Fatalf("Resolve failed: route=%+v err=%v", route, err)
	}
	req := ports.QuoteRequest{
		Route:          route,
		Side:           ports.SideSell,
		AmountInRaw:    big.NewInt(1_000_000),
		SlippageBps:    100,
		Recipient:      wallet,
		ContractExpiry: 1735689600,
	}
	quote, err := adapter.Quote(context.Background(), rpc, req)
	if err != nil || quote.MinOutRaw.Cmp(big.NewInt(792_000)) != 0 {
		t.Fatalf("Quote failed: quote=%+v err=%v", quote, err)
	}
	sellTx, err := adapter.BuildUnsigned(req, quote)
	if err != nil || sellTx.To != basechain.UniswapV3SwapRouter02 || !bytes.Equal(sellTx.Calldata[:4], []byte{0x5a, 0xe4, 0x01, 0xdc}) || !bytes.Contains(sellTx.Calldata, []byte{0x49, 0x40, 0x4b, 0x7c}) {
		t.Fatalf("expected Base SwapRouter02 multicall+unwrapWETH9, err=%v", err)
	}

	receipt := &types.Receipt{
		Status:    types.ReceiptStatusSuccessful,
		BlockHash: blockHash,
		Logs: []*types.Log{
			{
				Address: token,
				Topics:  []common.Hash{erc20TransferTopic, common.BytesToHash(wallet.Bytes()), common.BytesToHash(pool.Bytes())},
				Data:    common.LeftPadBytes(big.NewInt(1_000_000).Bytes(), 32),
			},
			{
				Address: basechain.WETHAddress,
				Topics:  []common.Hash{wethWithdrawalTopic, common.BytesToHash(basechain.UniswapV3SwapRouter02.Bytes())},
				Data:    common.LeftPadBytes(big.NewInt(795_000).Bytes(), 32),
			},
		},
	}
	eff, err := adapter.DecodeEffects(route, wallet, ports.SideSell, receipt)
	if err != nil || eff.NetTokenDeltaRaw.Cmp(big.NewInt(-1_000_000)) != 0 || eff.NetNativeDeltaWei.Cmp(big.NewInt(795_000)) != 0 {
		t.Fatalf("unexpected Base DecodeEffects: %+v err=%v", eff, err)
	}
}
