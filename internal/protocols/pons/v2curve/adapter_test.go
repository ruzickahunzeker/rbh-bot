package v2curve

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

func TestDecodeEffectsFeeOnTransferNetWalletDelta(t *testing.T) {
	adapter := NewAdapter()
	wallet := common.HexToAddress("0x1000000000000000000000000000000000000001")
	pool := common.HexToAddress("0x2000000000000000000000000000000000000002")
	taxVault := common.HexToAddress("0x3000000000000000000000000000000000000003")
	token := common.HexToAddress("0x4000000000000000000000000000000000000004")

	// Simulate 5% fee-on-transfer buy: pool sends 950 tokens to wallet and 50 tokens to taxVault.
	toData := func(v int64) []byte {
		b := make([]byte, 32)
		big.NewInt(v).FillBytes(b)
		return b
	}
	receipt := &types.Receipt{
		Status:    types.ReceiptStatusSuccessful,
		BlockHash: common.HexToHash("0xabc"),
		Logs: []*types.Log{
			{
				Address: token,
				Topics:  []common.Hash{erc20TransferTopic, common.BytesToHash(pool.Bytes()), common.BytesToHash(wallet.Bytes())},
				Data:    toData(950),
			},
			{
				Address: token,
				Topics:  []common.Hash{erc20TransferTopic, common.BytesToHash(pool.Bytes()), common.BytesToHash(taxVault.Bytes())},
				Data:    toData(50),
			},
		},
	}
	eff, err := adapter.DecodeEffects(ports.ResolvedRoute{Token: token}, wallet, ports.SideBuy, receipt)
	if err != nil {
		t.Fatal(err)
	}
	if !eff.EvidenceComplete || eff.NetTokenDeltaRaw.Cmp(big.NewInt(950)) != 0 {
		t.Fatalf("expected net wallet delta 950 (not 1000), got %v complete=%v", eff.NetTokenDeltaRaw, eff.EvidenceComplete)
	}
}
