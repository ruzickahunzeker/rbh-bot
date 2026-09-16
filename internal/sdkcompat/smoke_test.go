// Package sdkcompat proves that the production module resolves both pinned RBH
// SDKs in one dependency graph. It performs no RPC, signing or broadcast.
package sdkcompat

import (
	"math/big"
	"testing"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	pons "github.com/0xfnzero/rbh-trade-sdk/adapters/pons"
	trade "github.com/0xfnzero/rbh-trade-sdk/rbhtrade"
	"github.com/ethereum/go-ethereum/common"
)

func TestPinnedSDKsResolveTogether(t *testing.T) {
	if parser.New() == nil {
		t.Fatal("parser constructor returned nil")
	}
	recipient := common.HexToAddress("0x0000000000000000000000000000000000001001")
	call, err := pons.PackBuy(big.NewInt(1000), big.NewInt(900), recipient)
	if err != nil {
		t.Fatal(err)
	}
	if len(call) != 100 {
		t.Fatalf("unexpected Pons calldata length %d", len(call))
	}
	if _, err := trade.QuoteLongRehypeV4ExactInput(trade.V4PoolState{}, common.Address{}, big.NewInt(1), 0, 0, 0); err == nil {
		t.Fatal("unknown Long state must fail closed")
	}
	_ = trade.BuildV4ExactInputSingle
	_ = trade.BuildStateViewGetSlot0
}
