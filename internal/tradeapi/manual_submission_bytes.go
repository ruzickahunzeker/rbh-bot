package tradeapi

import (
	"bytes"
	"context"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
)

// withManualSubmissionBytes authenticates an original MANUAL envelope and lends
// its exact plaintext to one synchronous local callback. It grants no execution
// permission: ownership, fresh gates, durable intent and outcome accounting are
// the coordinator's responsibility. In particular, an error after callback entry
// does not prove that an external effect was absent.
//
// Both dependencies are trusted local primitives. The decryptor must return an
// owned plaintext buffer; neither dependency may retain a copy, mutate buffers
// concurrently or start asynchronous work. The callback's borrowed buffer is
// cleared on every return, including cancellation and panic. This is a lifetime
// contract, not a sandbox against a callback that deliberately copies plaintext.
func withManualSubmissionBytes(ctx context.Context, encrypted manualartifact.Encrypted, facts manualartifact.Facts, decryptor manualArtifactDecryptor, callback func([]byte) error) (err error) {
	if ctx == nil || decryptor == nil || callback == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	reader := &manualSubmissionBytesDecryptor{base: decryptor, ctx: ctx}
	defer func() {
		clear(reader.raw)
		if recover() != nil {
			err = manualartifact.ErrCipher
		}
	}()
	// Open owns and clears the decryptor's plaintext, and checks the unchanged
	// full-facts AAD, envelope shape, key version and ciphertext/nonce integrity.
	verified, openErr := manualartifact.Open(encrypted, facts, manualArtifactReadCipher{reader})
	if err := ctx.Err(); err != nil {
		return err
	}
	if openErr != nil {
		return openErr
	}
	// Recheck the owned copy actually lent to the callback, never a re-encoded
	// transaction or a later decryption from mutable external state.
	borrowed, err := manualartifact.Verify(reader.raw, facts)
	if err != nil || borrowed != verified {
		return manualartifact.ErrIntegrity
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := callback(reader.raw); err != nil {
		return err
	}
	return ctx.Err()
}

type manualSubmissionBytesDecryptor struct {
	base manualArtifactDecryptor
	ctx  context.Context
	raw  []byte
}

func (r *manualSubmissionBytesDecryptor) KeyVersion() string { return r.base.KeyVersion() }

func (r *manualSubmissionBytesDecryptor) Decrypt(version string, ciphertext, nonce, aad []byte) ([]byte, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	expectedAAD := append([]byte(nil), aad...)
	plaintext, err := r.base.Decrypt(version, ciphertext, nonce, aad)
	// Open clears this returned plaintext even on a decryption error. Only our
	// independent copy survives Open, so its cleanup cannot invalidate borrowing.
	if err == nil && !bytes.Equal(aad, expectedAAD) {
		err = manualartifact.ErrIntegrity
	}
	if err == nil && (len(plaintext) == 0 || len(plaintext) > manualartifact.MaxRawBytes) {
		err = manualartifact.ErrIntegrity
	}
	if err == nil {
		r.raw = append([]byte(nil), plaintext...)
	}
	return plaintext, err
}
