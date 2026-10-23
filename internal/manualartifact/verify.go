package manualartifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Verification exposes metadata only, never plaintext raw bytes or permission.
type Verification struct{ FactsHash, TxHash, RawSHA256 string }

func Verify(raw []byte, f Facts) (Verification, error) {
	var zero Verification
	expected, err := f.Transaction()
	if err != nil {
		return zero, err
	}
	if len(raw) == 0 || len(raw) > MaxRawBytes {
		return zero, ErrIntegrity
	}
	var tx types.Transaction
	if tx.UnmarshalBinary(raw) != nil || tx.Type() != types.DynamicFeeTxType || len(tx.AccessList()) != 0 {
		return zero, ErrIntegrity
	}
	canonical, err := tx.MarshalBinary()
	defer clearBytes(canonical)
	if err != nil || !bytes.Equal(raw, canonical) {
		return zero, ErrIntegrity
	}
	if tx.ChainId().Cmp(big.NewInt(4663)) != 0 || tx.Nonce() != expected.Nonce() || tx.To() == nil || *tx.To() != *expected.To() ||
		tx.Value().Cmp(expected.Value()) != 0 || !bytes.Equal(tx.Data(), expected.Data()) || tx.Gas() != expected.Gas() ||
		tx.GasFeeCap().Cmp(expected.GasFeeCap()) != 0 || tx.GasTipCap().Cmp(expected.GasTipCap()) != 0 {
		return zero, ErrIntegrity
	}
	sender, err := types.Sender(types.LatestSignerForChainID(big.NewInt(4663)), &tx)
	if err != nil || sender != common.HexToAddress(f.From) {
		return zero, ErrIntegrity
	}
	_, digest, _ := f.Canonical()
	sum := sha256.Sum256(raw)
	return Verification{FactsHash: digest, TxHash: tx.Hash().Hex(), RawSHA256: hex.EncodeToString(sum[:])}, nil
}
