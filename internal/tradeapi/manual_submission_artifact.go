package tradeapi

import (
	"context"
	"database/sql"
	"errors"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
)

// Only historical decryption capability. A signer, encryptor, key loader,
// observer or submission service cannot be reached through this dependency.
type manualArtifactDecryptor interface {
	KeyVersion() string
	Decrypt(string, []byte, []byte, []byte) ([]byte, error)
}

// Adapt to the unchanged M3 history verifier without granting an encryption
// dependency. Open never invokes Encrypt; any accidental future use fails.
type manualArtifactReadCipher struct{ decryptor manualArtifactDecryptor }

func (c manualArtifactReadCipher) KeyVersion() string { return c.decryptor.KeyVersion() }
func (c manualArtifactReadCipher) Decrypt(v string, raw, nonce, aad []byte) ([]byte, error) {
	return c.decryptor.Decrypt(v, raw, nonce, aad)
}
func (manualArtifactReadCipher) Encrypt([]byte, []byte) ([]byte, []byte, error) {
	return nil, nil, manualartifact.ErrCipher
}

// Candidate/history metadata only. No raw/ciphertext/nonce buffer or marker
// owner escapes. Every future action must reload its own current writer gate.
type manualSubmissionArtifact struct {
	Source, Format, Phase, Status                                            string
	Facts                                                                    manualartifact.Facts
	FactsHash, TxHash, RawSHA256, KeyVersion, CompletedAt                    string
	SnapshotOnly, FreshGateRequired                                          bool
	ExecutionAuthorized, SigningAuthorized, SendAuthorized, ReplayAuthorized bool
}

// Private, disconnected M4-A composition: no public constructor or HTTP,
// config/startup caller. No clock: identity inspection is not freshness proof.
type manualSubmissionArtifactReader struct {
	store     *Store
	decryptor manualArtifactDecryptor
}

func (a *manualSubmissionArtifactReader) load(ctx context.Context, id Identity, request string, scope manualAdmissionScope) (manualSubmissionArtifact, error) {
	var zero manualSubmissionArtifact
	if a == nil || a.store == nil || a.store.db == nil || a.decryptor == nil || ctx == nil || !id.valid() || !validID(request) || !scope.valid() {
		return zero, ErrInvalid
	}
	tx, err := a.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	if err := ownManualNonceRequest(ctx, tx, id, request); err != nil {
		return zero, err
	}
	// Source/format is selected by durable ownership, never a caller tag or a
	// decryption-error fallback. Encoded legacy ownership is denial-only.
	var operation, kind, chain, storageClass string
	if err := tx.QueryRowContext(ctx, `SELECT operation_id,source,chain,typeof(operation_id) FROM manual_http_order_sources WHERE id=?`, request).Scan(&operation, &kind, &chain, &storageClass); err != nil {
		return zero, err
	}
	if storageClass != "text" || operation != "manual_op_"+request || kind != "MANUAL" || chain != "robinhood" {
		return zero, ErrConflict
	}
	var owners, legacy int
	if err := tx.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(*) FROM manual_http_order_sources WHERE hex(id)=hex(?) OR hex(operation_id)=hex(?)),
 (SELECT COUNT(*) FROM canary_admission_sources WHERE hex(operation_id)=hex(?))+
 (SELECT COUNT(*) FROM dry_run_results WHERE hex(operation_id)=hex(?))`, request, operation, operation, operation).Scan(&owners, &legacy); err != nil {
		return zero, err
	}
	if owners != 1 || legacy != 0 {
		return zero, ErrConflict
	}
	// Reuse exact original M3 signed-phase, accounting, audit and cryptographic
	// verification on THIS snapshot. Missing/ambiguous/unsupported downstream
	// history rejects; 036 is deliberately not widened or repaired by M4-A.
	history, err := loadManualSignedArtifactHistory(ctx, tx, id, request, scope, manualArtifactReadCipher{a.decryptor})
	if err != nil {
		return zero, err
	}
	var rawFacts string
	if err := tx.QueryRowContext(ctx, `SELECT facts_json FROM manual_signed_artifact_complete WHERE operation_id=?`, operation).Scan(&rawFacts); err != nil {
		return zero, err
	}
	facts, err := manualartifact.Decode(rawFacts)
	if err != nil {
		return zero, err
	}
	_, digest, err := facts.Canonical()
	if err != nil || facts.OperationID != operation || facts.AttemptID != history.AttemptID || facts.RequestID != request || digest != history.FactsHash {
		return zero, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if err := tx.Commit(); err != nil {
		if errors.Is(err, sql.ErrTxDone) && ctx.Err() != nil {
			err = errors.Join(ctx.Err(), err)
		}
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return manualSubmissionArtifact{Source: "MANUAL", Format: "MANUAL_V1_FULL_FACTS_AAD", Phase: "SIGNED_PREBROADCAST",
		Status: "VERIFIED_MANUAL_ARTIFACT_CANDIDATE_NOT_PERMISSION", Facts: facts, FactsHash: digest,
		TxHash: history.TxHash, RawSHA256: history.RawSHA256, KeyVersion: history.KeyVersion, CompletedAt: history.CompletedAt,
		SnapshotOnly: true, FreshGateRequired: true}, nil
}
