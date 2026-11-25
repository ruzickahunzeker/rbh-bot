package v4

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/robinhood"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

type mockV4RPC struct {
	codes  map[common.Address][]byte
	quoter common.Address
	outAmt *big.Int
}

func (m *mockV4RPC) CodeAt(_ context.Context, addr common.Address, _ common.Hash) ([]byte, error) {
	if c, ok := m.codes[addr]; ok {
		return c, nil
	}
	return nil, nil
}

func (m *mockV4RPC) CallContractAt(_ context.Context, to common.Address, data []byte, _ common.Hash) ([]byte, error) {
	if to != m.quoter || len(data) < 4 {
		return nil, errors.New("unexpected target")
	}
	return quoterABI.Methods["quoteExactInputSingle"].Outputs.Pack(m.outAmt, big.NewInt(145000))
}

func TestUniswapV4AdapterResolveQuoteBuildAndDecode(t *testing.T) {
	a := NewBSCAdapter()
	desc := a.Describe()
	if desc.ProtocolID != ProtocolIDBSC || desc.PoolPhase != "DEX_V4" || !desc.SupportsBuy || !desc.SupportsSell {
		t.Fatalf("unexpected descriptor: %+v", desc)
	}

	token := common.HexToAddress("0x55d398326f99059fF775485246999027B3197955")
	blockHash := common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	rpc := &mockV4RPC{
		codes: map[common.Address][]byte{
			bsc.UniswapV4PoolManager:     {0x60, 0x01},
			bsc.UniswapV4UniversalRouter: {0x60, 0x02},
			token:                        {0x60, 0x03},
		},
		quoter: bsc.UniswapV4Quoter,
		outAmt: big.NewInt(5_500_000_000),
	}

	route, err := a.Resolve(context.Background(), rpc, token, 42000000, blockHash)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if route.RouterOrSpender != bsc.UniswapV4UniversalRouter || route.PoolOrCurve != bsc.UniswapV4PoolManager {
		t.Fatalf("unexpected route: %+v", route)
	}

	qReq := ports.QuoteRequest{
		Route:          route,
		Side:           ports.SideBuy,
		AmountInRaw:    big.NewInt(10_000_000_000_000_000),
		SlippageBps:    100,
		Recipient:      common.HexToAddress("0x000000000000000000000000000000000000CAFE"),
		ContractExpiry: 1900000000,
	}
	qRes, err := a.Quote(context.Background(), rpc, qReq)
	if err != nil {
		t.Fatalf("Quote failed: %v", err)
	}
	if qRes.ExpectedOutRaw.Cmp(big.NewInt(5_500_000_000)) != 0 {
		t.Fatalf("expected 5500000000, got %s", qRes.ExpectedOutRaw)
	}

	utx, err := a.BuildUnsigned(qReq, qRes)
	if err != nil {
		t.Fatalf("BuildUnsigned buy failed: %v", err)
	}
	if utx.To != bsc.UniswapV4UniversalRouter {
		t.Fatalf("expected UniversalRouter target, got %s", utx.To.Hex())
	}
	execSel := universalRouterABI.Methods["execute"].ID
	if len(utx.Calldata) < 4 || !bytes.Equal(utx.Calldata[:4], execSel) {
		t.Fatalf("expected execute selector %x, got %x", execSel, utx.Calldata[:4])
	}

	wallet := qReq.Recipient
	amtBytes := common.LeftPadBytes(big.NewInt(5_490_000_000).Bytes(), 32)
	rcpt := &types.Receipt{
		Status:    types.ReceiptStatusSuccessful,
		BlockHash: blockHash,
		Logs: []*types.Log{
			{
				Address: bsc.UniswapV4PoolManager,
				Topics:  []common.Hash{V4PoolSwapTopic, testPoolID(t, route), common.BytesToHash(a.router.Bytes())},
				Data:    testSwapData(new(big.Int).Neg(qReq.AmountInRaw), rpc.outAmt),
			},
			{
				Address: token,
				Topics: []common.Hash{
					erc20TransferTopic,
					common.BytesToHash(bsc.UniswapV4PoolManager.Bytes()),
					common.BytesToHash(wallet.Bytes()),
				},
				Data: amtBytes,
			},
		},
	}
	eff, err := a.DecodeEffects(route, wallet, ports.SideBuy, rcpt)
	if err != nil {
		t.Fatalf("DecodeEffects failed: %v", err)
	}
	if !eff.EvidenceComplete || eff.NetTokenDeltaRaw.Cmp(big.NewInt(5_490_000_000)) != 0 {
		t.Fatalf("unexpected decoded effects: %+v", eff)
	}

	// Verify RBH V4 adapter includes Pons/Bags/O1 launchpad hooks in candidates.
	rbhAdapter := NewRBHAdapter()
	cands := rbhAdapter.candidateKeys(token)
	sawPonsHook := false
	for _, c := range cands {
		if c.hook == robinhood.PonsMemeHook {
			sawPonsHook = true
			break
		}
	}
	if !sawPonsHook {
		t.Fatalf("expected RBH V4 adapter to probe PonsMemeHook")
	}
}
