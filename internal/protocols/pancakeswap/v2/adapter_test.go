package v2

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

type stubRPC struct {
	pairAddr      common.Address
	quoteOut      *big.Int
	lastBlockHash common.Hash
}

func (s *stubRPC) CodeAt(_ context.Context, addr common.Address, _ common.Hash) ([]byte, error) {
	if addr == (common.Address{}) {
		return nil, nil
	}
	return []byte{0x60, 0x80}, nil
}

func (s *stubRPC) CallContractAt(_ context.Context, to common.Address, data []byte, blockHash common.Hash) ([]byte, error) {
	s.lastBlockHash = blockHash
	if to == bsc.PancakeV2Factory && len(data) >= 4 && bytes.Equal(data[:4], factoryABI.Methods["getPair"].ID) {
		return factoryABI.Methods["getPair"].Outputs.Pack(s.pairAddr)
	}
	if to == bsc.PancakeV2Router && len(data) >= 4 && bytes.Equal(data[:4], routerABI.Methods["getAmountsOut"].ID) {
		unpacked, err := routerABI.Methods["getAmountsOut"].Inputs.Unpack(data[4:])
		if err != nil {
			return nil, err
		}
		amtIn := unpacked[0].(*big.Int)
		return routerABI.Methods["getAmountsOut"].Outputs.Pack([]*big.Int{amtIn, s.quoteOut})
	}
	return nil, nil
}

func TestPancakeV2AdapterResolveQuoteBuildAndDecode(t *testing.T) {
	adapter := NewAdapter()
	token := common.HexToAddress("0x000000000000000000000000000000000000BEEF")
	wallet := common.HexToAddress("0x000000000000000000000000000000000000CAFE")
	pair := common.HexToAddress("0x000000000000000000000000000000000000FA12")
	blockHash := common.HexToHash("0x1234")

	// Missing pair in factory must fail closed.
	if _, err := adapter.Resolve(context.Background(), &stubRPC{pairAddr: common.Address{}}, token, 40_000_000, blockHash); err == nil {
		t.Fatal("expected Resolve to fail when factory.getPair returns zero address")
	}

	rpc := &stubRPC{
		pairAddr: pair,
		quoteOut: big.NewInt(250_000), // real on-chain output != 1:1 with AmountInRaw
	}
	route, err := adapter.Resolve(context.Background(), rpc, token, 40_000_000, blockHash)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if route.PoolOrCurve != pair {
		t.Fatalf("expected PoolOrCurve=%s, got %s", pair.Hex(), route.PoolOrCurve.Hex())
	}

	req := ports.QuoteRequest{
		Route:          route,
		Side:           ports.SideBuy,
		AmountInRaw:    big.NewInt(1_000_000_000_000_000), // 0.001 BNB
		SlippageBps:    100,                              // 1%
		Recipient:      wallet,
		ContractExpiry: 1735689600,
	}
	quote, err := adapter.Quote(context.Background(), rpc, req)
	if err != nil {
		t.Fatalf("Quote failed: %v", err)
	}
	if quote.ExpectedOutRaw.Cmp(big.NewInt(250_000)) != 0 || quote.MinOutRaw.Cmp(big.NewInt(247_500)) != 0 {
		t.Fatalf("unexpected real RPC quote: expected=250000 minOut=247500, got expected=%s minOut=%s", quote.ExpectedOutRaw, quote.MinOutRaw)
	}
	if rpc.lastBlockHash != blockHash {
		t.Fatalf("Quote did not pin CallContract to QuoteBlockHash: got %s want %s", rpc.lastBlockHash.Hex(), blockHash.Hex())
	}

	// Mandatory CONTRACT_DEADLINE_UNIX check.
	reqNoDeadline := req
	reqNoDeadline.ContractExpiry = 0
	if _, err := adapter.BuildUnsigned(reqNoDeadline, quote); err == nil {
		t.Fatal("expected error when ContractExpiry <= 0")
	}

	buyTx, err := adapter.BuildUnsigned(req, quote)
	if err != nil {
		t.Fatalf("BuildUnsigned buy failed: %v", err)
	}
	if buyTx.To != bsc.PancakeV2Router || buyTx.Value.Cmp(req.AmountInRaw) != 0 || len(buyTx.Calldata) < 4 {
		t.Fatalf("unexpected buy tx: %+v", buyTx)
	}

	reqSell := req
	reqSell.Side = ports.SideSell
	sellTx, err := adapter.BuildUnsigned(reqSell, quote)
	if err != nil {
		t.Fatalf("BuildUnsigned sell failed: %v", err)
	}
	if sellTx.Value.Sign() != 0 || len(sellTx.Calldata) < 4 {
		t.Fatalf("unexpected sell tx: %+v", sellTx)
	}

	approveData, err := BuildApproveCalldata(bsc.PancakeV2Router, big.NewInt(500_000))
	if err != nil || len(approveData) != 4+64 {
		t.Fatalf("unexpected approve calldata: len=%d err=%v", len(approveData), err)
	}

	// DecodeEffects: tax token transfers (1000 out from wallet, 50 tax to dead) + WBNB Withdrawal of 0.002 BNB to recipient.
	dead := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
	bnbProceeds := big.NewInt(2_000_000_000_000_000)
	receipt := &types.Receipt{
		Status:    types.ReceiptStatusSuccessful,
		BlockHash: blockHash,
		Logs: []*types.Log{
			{
				Address: token,
				Topics:  []common.Hash{erc20TransferTopic, common.BytesToHash(wallet.Bytes()), common.BytesToHash(pair.Bytes())},
				Data:    common.LeftPadBytes(big.NewInt(950).Bytes(), 32),
			},
			{
				Address: token,
				Topics:  []common.Hash{erc20TransferTopic, common.BytesToHash(wallet.Bytes()), common.BytesToHash(dead.Bytes())},
				Data:    common.LeftPadBytes(big.NewInt(50).Bytes(), 32),
			},
			{
				Address: bsc.WBNBAddress,
				Topics:  []common.Hash{wbnbWithdrawalTopic, common.BytesToHash(bsc.PancakeV2Router.Bytes())},
				Data:    common.LeftPadBytes(bnbProceeds.Bytes(), 32),
			},
		},
	}
	effects, err := adapter.DecodeEffects(route, wallet, ports.SideSell, receipt)
	if err != nil {
		t.Fatalf("DecodeEffects failed: %v", err)
	}
	if effects.NetTokenDeltaRaw.Cmp(big.NewInt(-1000)) != 0 {
		t.Fatalf("expected net token delta -1000, got %s", effects.NetTokenDeltaRaw.String())
	}
	if effects.NetNativeDeltaWei.Cmp(bnbProceeds) != 0 {
		t.Fatalf("expected net native delta %s, got %s", bnbProceeds.String(), effects.NetNativeDeltaWei.String())
	}
}
