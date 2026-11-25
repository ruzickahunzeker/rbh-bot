package aerodrome

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	basechain "github.com/ruzickahunzeker/rbh-bot/internal/chains/base"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

type mockAeroRPC struct {
	codes map[common.Address][]byte
	pool  common.Address
	out   *big.Int
}

func (m *mockAeroRPC) CodeAt(_ context.Context, addr common.Address, _ common.Hash) ([]byte, error) {
	if c, ok := m.codes[addr]; ok {
		return c, nil
	}
	return nil, nil
}

func (m *mockAeroRPC) CallContractAt(_ context.Context, to common.Address, data []byte, _ common.Hash) ([]byte, error) {
	if len(data) < 4 {
		return nil, errors.New("short calldata")
	}
	switch to {
	case basechain.AerodromePoolFactory:
		return factoryABI.Methods["getPool"].Outputs.Pack(m.pool)
	case basechain.AerodromeRouter:
		return routerABI.Methods["getAmountsOut"].Outputs.Pack([]*big.Int{big.NewInt(1e15), m.out})
	default:
		return nil, errors.New("unexpected contract")
	}
}

func TestAerodromeAdapterResolveQuoteBuildAndDecode(t *testing.T) {
	a := NewBaseAdapter()
	desc := a.Describe()
	if desc.ProtocolID != ProtocolID || desc.ChainID != basechain.ChainID || !desc.FeeOnTransferSupport {
		t.Fatalf("unexpected descriptor: %+v", desc)
	}

	token := common.HexToAddress("0x940181a94A35A4569E4529A3CDfB74e38FD98631") // AERO on Base
	pool := common.HexToAddress("0x7f670f78B17dEC44d5Ef68a48740b6f8849cc2e6")
	blockHash := common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")

	rpc := &mockAeroRPC{
		codes: map[common.Address][]byte{
			basechain.AerodromeRouter: {0x60, 0x01},
			token:                     {0x60, 0x02},
			pool:                      {0x60, 0x03},
		},
		pool: pool,
		out:  big.NewInt(42_000_000_000_000_000),
	}

	route, err := a.Resolve(context.Background(), rpc, token, 25000000, blockHash)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if route.PoolOrCurve != pool || route.RouterOrSpender != basechain.AerodromeRouter || route.DeploymentRevision != "aerodrome-volatile" {
		t.Fatalf("unexpected route: %+v", route)
	}

	wallet := common.HexToAddress("0x000000000000000000000000000000000000BEEF")
	qReq := ports.QuoteRequest{
		Route:          route,
		Side:           ports.SideBuy,
		AmountInRaw:    big.NewInt(1_000_000_000_000_000),
		SlippageBps:    100,
		Recipient:      wallet,
		ContractExpiry: 1900000000,
	}
	qRes, err := a.Quote(context.Background(), rpc, qReq)
	if err != nil {
		t.Fatalf("Quote failed: %v", err)
	}
	if qRes.ExpectedOutRaw.Cmp(rpc.out) != 0 {
		t.Fatalf("expected %s, got %s", rpc.out, qRes.ExpectedOutRaw)
	}

	utx, err := a.BuildUnsigned(qReq, qRes)
	if err != nil {
		t.Fatalf("BuildUnsigned failed: %v", err)
	}
	buySel := routerABI.Methods["swapExactETHForTokensSupportingFeeOnTransferTokens"].ID
	if !bytes.Equal(utx.Calldata[:4], buySel) || utx.Value.Cmp(qReq.AmountInRaw) != 0 {
		t.Fatalf("unexpected unsigned buy tx")
	}

	rcpt := &types.Receipt{
		Status:    types.ReceiptStatusSuccessful,
		BlockHash: blockHash,
		Logs: []*types.Log{
			{
				Address: token,
				Topics: []common.Hash{
					erc20TransferTopic,
					common.BytesToHash(pool.Bytes()),
					common.BytesToHash(wallet.Bytes()),
				},
				Data: common.LeftPadBytes(rpc.out.Bytes(), 32),
			},
		},
	}
	eff, err := a.DecodeEffects(route, wallet, ports.SideBuy, rcpt)
	if err != nil {
		t.Fatalf("DecodeEffects failed: %v", err)
	}
	if !eff.EvidenceComplete || eff.NetTokenDeltaRaw.Cmp(rpc.out) != 0 {
		t.Fatalf("unexpected effects: %+v", eff)
	}
}
