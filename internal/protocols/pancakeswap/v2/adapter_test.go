package v2

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

func TestPancakeV2BuildUnsignedAndDecodeTaxToken(t *testing.T) {
	a := NewAdapter()
	desc := a.Describe()
	if desc.ChainID != 56 || desc.DeadlineCapability != ports.DeadlineContractDeadlineUnix || !desc.FeeOnTransferSupport {
		t.Fatalf("unexpected descriptor: %+v", desc)
	}

	token := common.HexToAddress("0x2000000000000000000000000000000000000002")
	wallet := common.HexToAddress("0x1000000000000000000000000000000000000001")
	route := ports.ResolvedRoute{ChainID: 56, ProtocolID: ProtocolID, Token: token, PoolOrCurve: bsc.PancakeV2Router, RouterOrSpender: bsc.PancakeV2Router}
	quote := ports.QuoteResult{ExpectedOutRaw: big.NewInt(1000), MinOutRaw: big.NewInt(950)}

	// Missing deadline must fail closed.
	if _, err := a.BuildUnsigned(ports.QuoteRequest{Route: route, Side: ports.SideBuy, AmountInRaw: big.NewInt(1e16), Recipient: wallet, ContractExpiry: 0}, quote); err == nil {
		t.Fatal("expected missing contract_deadline_unix to fail closed")
	}

	// Valid BUY produces swapExactETHForTokensSupportingFeeOnTransferTokens selector 0xb6f9de95.
	buyTx, err := a.BuildUnsigned(ports.QuoteRequest{Route: route, Side: ports.SideBuy, AmountInRaw: big.NewInt(1e16), Recipient: wallet, ContractExpiry: 1795440000}, quote)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(buyTx.Calldata[:4]) != "b6f9de95" || buyTx.Value.Cmp(big.NewInt(1e16)) != 0 || buyTx.To != bsc.PancakeV2Router {
		t.Fatalf("unexpected buy tx: selector=%s value=%s to=%s", hex.EncodeToString(buyTx.Calldata[:4]), buyTx.Value, buyTx.To)
	}

	// Valid SELL produces swapExactTokensForETHSupportingFeeOnTransferTokens selector 0x791ac947 and value=0.
	sellTx, err := a.BuildUnsigned(ports.QuoteRequest{Route: route, Side: ports.SideSell, AmountInRaw: big.NewInt(950), Recipient: wallet, ContractExpiry: 1795440000}, quote)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(sellTx.Calldata[:4]) != "791ac947" || sellTx.Value.Sign() != 0 {
		t.Fatalf("unexpected sell tx: selector=%s value=%s", hex.EncodeToString(sellTx.Calldata[:4]), sellTx.Value)
	}

	// Approve calldata uses ERC-20 approve selector 0x095ea7b3.
	apprData, err := BuildApproveCalldata(bsc.PancakeV2Router, big.NewInt(950))
	if err != nil || !bytes.Equal(apprData[:4], common.FromHex("0x095ea7b3")) {
		t.Fatalf("unexpected approve calldata: %x err=%v", apprData, err)
	}

	// DecodeEffects returns net wallet delta on tax token.
	toData := func(v int64) []byte {
		b := make([]byte, 32)
		big.NewInt(v).FillBytes(b)
		return b
	}
	receipt := &types.Receipt{
		Status:    types.ReceiptStatusSuccessful,
		BlockHash: common.HexToHash("0xdef"),
		Logs: []*types.Log{
			{Address: token, Topics: []common.Hash{erc20TransferTopic, common.BytesToHash(bsc.PancakeV2Router.Bytes()), common.BytesToHash(wallet.Bytes())}, Data: toData(920)},
		},
	}
	eff, err := a.DecodeEffects(route, wallet, ports.SideBuy, receipt)
	if err != nil || eff.NetTokenDeltaRaw.Cmp(big.NewInt(920)) != 0 {
		t.Fatalf("unexpected decoded effects: %+v err=%v", eff, err)
	}
}
