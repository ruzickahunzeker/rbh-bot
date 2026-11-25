package v4

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/robinhood"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

type stubV4RPC struct{ bal *big.Int }

func (s *stubV4RPC) CodeAt(_ context.Context, _ common.Address, _ common.Hash) ([]byte, error) {
	return []byte{0x60, 0x00}, nil
}

func (s *stubV4RPC) CallContractAt(_ context.Context, _ common.Address, _ []byte, _ common.Hash) ([]byte, error) {
	out := make([]byte, 32)
	s.bal.FillBytes(out)
	return out, nil
}

func TestPonsV4AdapterResolveBuildDecode(t *testing.T) {
	adp := NewAdapter()
	token := common.HexToAddress("0x1111111111111111111111111111111111111111")
	wallet := common.HexToAddress("0x2222222222222222222222222222222222222222")
	blkHash := common.HexToHash("0xabcd")

	route, err := adp.Resolve(context.Background(), &stubV4RPC{bal: big.NewInt(1e18)}, token, 100, blkHash)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if route.PoolOrCurve != robinhood.V4PoolManager || route.RouterOrSpender != robinhood.V4UniversalRouter {
		t.Fatalf("unexpected V4 route: %+v", route)
	}
	tx, err := adp.BuildUnsigned(ports.QuoteRequest{
		Route: route, Side: ports.SideBuy, AmountInRaw: big.NewInt(1e16), ContractExpiry: 1999999999,
	}, ports.QuoteResult{MinOutRaw: big.NewInt(100)})
	if err != nil || tx.To != robinhood.V4UniversalRouter || tx.Value.Cmp(big.NewInt(1e16)) != 0 {
		t.Fatalf("BuildUnsigned failed: %+v err=%v", tx, err)
	}

	amtData := make([]byte, 32)
	big.NewInt(777_000).FillBytes(amtData)
	rcpt := &types.Receipt{
		Status:    types.ReceiptStatusSuccessful,
		BlockHash: blkHash,
		Logs: []*types.Log{
			{Address: robinhood.V4PoolManager, Topics: []common.Hash{v4PoolSwapTopic}},
			{Address: token, Topics: []common.Hash{erc20TransferTopic, common.BytesToHash(robinhood.V4PoolManager.Bytes()), common.BytesToHash(wallet.Bytes())}, Data: amtData},
		},
	}
	eff, err := adp.DecodeEffects(route, wallet, ports.SideBuy, rcpt)
	if err != nil || !eff.EvidenceComplete || eff.NetTokenDeltaRaw.Cmp(big.NewInt(777_000)) != 0 {
		t.Fatalf("DecodeEffects failed: %+v err=%v", eff, err)
	}
}
