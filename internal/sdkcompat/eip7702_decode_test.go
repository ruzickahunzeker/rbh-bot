package sdkcompat

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/holiman/uint256"
)

// TestGoEthereumSupportsEIP7702SetCodeTx verifies Step 0A.4:
// go-ethereum cleanly marshals and unmarshals Type-4 (0x04) EIP-7702 transactions
// and blocks containing them (required for BSC Pascal/BEP-441 & Base).
func TestGoEthereumSupportsEIP7702SetCodeTx(t *testing.T) {
	auth := types.SetCodeAuthorization{
		ChainID: *uint256.NewInt(56),
		Address: common.HexToAddress("0x0000000000000000000000000000000000007702"),
		Nonce:   1,
		V:       0,
		R:       *uint256.NewInt(1),
		S:       *uint256.NewInt(2),
	}
	tx := types.NewTx(&types.SetCodeTx{
		ChainID:    uint256.NewInt(56),
		Nonce:      7,
		GasTipCap:  uint256.NewInt(100_000_000),
		GasFeeCap:  uint256.NewInt(1_000_000_000),
		Gas:        150_000,
		To:         common.HexToAddress("0x1000000000000000000000000000000000000001"),
		Value:      uint256.NewInt(0),
		Data:       []byte{0xde, 0xad, 0xbe, 0xef},
		AuthList:   []types.SetCodeAuthorization{auth},
		V:          uint256.NewInt(0),
		R:          uint256.NewInt(3),
		S:          uint256.NewInt(4),
	})
	if tx.Type() != types.SetCodeTxType || tx.Type() != 0x04 {
		t.Fatalf("unexpected tx type: 0x%02x", tx.Type())
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal EIP-7702 tx: %v", err)
	}
	var decoded types.Transaction
	if err := decoded.UnmarshalBinary(raw); err != nil {
		t.Fatalf("unmarshal EIP-7702 tx: %v", err)
	}
	if decoded.Type() != types.SetCodeTxType || len(decoded.SetCodeAuthorizations()) != 1 {
		t.Fatalf("decoded EIP-7702 tx mismatch: type=%d auths=%d", decoded.Type(), len(decoded.SetCodeAuthorizations()))
	}
	block := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(50_000_000)}).WithBody(types.Body{Transactions: []*types.Transaction{&decoded}})
	if len(block.Transactions()) != 1 || block.Transactions()[0].Type() != types.SetCodeTxType {
		t.Fatal("block body did not preserve EIP-7702 transaction")
	}
}
