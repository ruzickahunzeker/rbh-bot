package trade

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
)

// recoveryArtifactAAD selects the immutable source format before decrypting.
// A MANUAL artifact must never fall back to legacy AAD after a missing or
// malformed MANUAL witness. This grants no signing or sending capability.
func (s *Store) recoveryArtifactAAD(ctx context.Context, stored encryptedArtifact) ([]byte, error) {
	var request string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM manual_http_order_sources WHERE operation_id=? AND source='MANUAL' AND chain='robinhood'`, stored.Operation).Scan(&request)
	if errors.Is(err, sql.ErrNoRows) {
		if len(stored.Operation) >= len("manual_op_") && stored.Operation[:len("manual_op_")] == "manual_op_" {
			return nil, ErrArtifactIntegrity
		}
		return artifactAAD(stored.Operation, stored.StepID, stored.AttemptID), nil
	}
	if err != nil {
		return nil, err
	}
	var raw, envelope, factsHash string
	// The signed-phase completeness view intentionally disappears after a
	// submission changes the step/lane state. Recovery must use its retained
	// immutable command, then re-authenticate the exact envelope and bytes.
	err = s.db.QueryRowContext(ctx, `SELECT facts_json,envelope_json,facts_hash FROM manual_signed_artifact_commands WHERE operation_id=?`, stored.Operation).Scan(&raw, &envelope, &factsHash)
	if err != nil {
		return nil, ErrArtifactIntegrity
	}
	f, err := manualartifact.Decode(raw)
	if err != nil {
		return nil, ErrArtifactIntegrity
	}
	v, _, err := manualartifact.DecodeEnvelopeMetadata(envelope, f)
	var rawHash string
	if err == nil {
		err = s.db.QueryRowContext(ctx, `SELECT raw_tx_hash FROM transaction_attempts WHERE id=? AND step_id=?`, stored.AttemptID, stored.StepID).Scan(&rawHash)
	}
	if err != nil || v.FactsHash != factsHash || v.TxHash != stored.TxHash || v.RawSHA256 != rawHash || v.KeyVersion != stored.KeyVersion || !bytes.Equal(v.Ciphertext, stored.Ciphertext) || !bytes.Equal(v.EncryptionNonce, stored.EncryptionNonce) {
		return nil, ErrArtifactIntegrity
	}
	nonce, err := strconv.ParseUint(f.Nonce, 10, 64)
	if err != nil || f.OperationID != stored.Operation || f.RequestID != request || f.ExecutionStepID != stored.StepID || f.AttemptID != stored.AttemptID || f.WalletID != stored.WalletID || f.ChainID != ChainID || nonce != stored.Nonce ||
		f.PolicyVersion != stored.PolicyVersion || f.QuoteBlockNumber != stored.QuoteBlockNumber || f.QuoteBlockHash != stored.QuoteBlockHash || f.ExpiresAt != stored.ExpiresAt ||
		common.HexToAddress(f.From) != common.HexToAddress(stored.From) || common.HexToAddress(f.To) != common.HexToAddress(stored.To) || f.Value != stored.Value || f.Data != stored.Data || f.GasLimit != stored.GasLimit || f.GasTipCap != stored.GasTipCap || f.GasFeeCap != stored.GasFeeCap {
		return nil, ErrArtifactIntegrity
	}
	aad, err := f.AAD()
	if err != nil {
		return nil, ErrArtifactIntegrity
	}
	return aad, nil
}
