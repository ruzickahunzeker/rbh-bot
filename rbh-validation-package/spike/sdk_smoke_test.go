// UNEXECUTED until the fixed-toolchain SDK runner succeeds.
// No RPC, private keys, signing or transaction broadcast.
package spike

import (
	"github.com/0xfnzero/rbh-parser-sdk/feed/sequencer"
	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	pons "github.com/0xfnzero/rbh-trade-sdk/adapters/pons"
	trade "github.com/0xfnzero/rbh-trade-sdk/rbhtrade"
	"github.com/ethereum/go-ethereum/common"
	"math/big"
	"testing"
)

func TestParserAndFeedConstructors(t *testing.T) {
	if parser.New() == nil {
		t.Fatal("nil parser")
	}
	if _, err := sequencer.New(sequencer.Config{}); err != nil {
		t.Fatal(err)
	}
}
func TestPonsABIEncoding(t *testing.T) {
	recipient := common.HexToAddress("0x0000000000000000000000000000000000001001")
	buy, err := pons.PackBuy(big.NewInt(1000), big.NewInt(900), recipient)
	if err != nil {
		t.Fatal(err)
	}
	sell, err := pons.PackSell(big.NewInt(1000), big.NewInt(900), recipient)
	if err != nil {
		t.Fatal(err)
	}
	if len(buy) != 100 || len(sell) != 100 {
		t.Fatal("unexpected ABI length")
	}
	_ = pons.BuildBuy
	_ = pons.BuildSell
	_ = pons.QuoteBuyFromState
}
func TestUnknownLongStateRejected(t *testing.T) {
	_, err := trade.QuoteLongRehypeV4ExactInput(trade.V4PoolState{}, common.Address{}, big.NewInt(1), 0, 0, 0)
	if err == nil {
		t.Fatal("unknown Long state must fail closed")
	}
}
func TestSharedSymbols(t *testing.T) {
	_ = trade.BuildV4ExactInputSingle
	_ = trade.BuildUniversalRouterApprovals
	_ = trade.DecodeDopplerInitData
	_ = trade.DecodeRehypeInitData
}
