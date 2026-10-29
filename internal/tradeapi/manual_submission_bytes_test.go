package tradeapi

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
)

func submissionBytesFixture(t *testing.T, side string) (manualartifact.Encrypted, manualartifact.Facts, manualArtifactDecryptor, []byte) {
	t.Helper()
	_, s, api, record, scope, _, writer, _, _ := localSigningFixture(t, side)
	if _, err := writer.sign(context.Background(), api.identity, record.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	var encodedFacts, envelope string
	if err := s.db.QueryRow(`SELECT facts_json,envelope_json FROM manual_signed_artifact_commands WHERE operation_id=?`, *record.OperationID).Scan(&encodedFacts, &envelope); err != nil {
		t.Fatal(err)
	}
	facts, err := manualartifact.Decode(encodedFacts)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, _, err := manualartifact.DecodeEnvelopeMetadata(envelope, facts)
	if err != nil {
		t.Fatal(err)
	}
	aad, err := facts.AAD()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := writer.cipher.Decrypt(encrypted.KeyVersion, encrypted.Ciphertext, encrypted.EncryptionNonce, aad)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clear(raw) })
	return encrypted, facts, writer.cipher, raw
}

type submissionBytesTestDecryptor struct {
	base        manualArtifactDecryptor
	mode        string
	cancel      context.CancelFunc
	calls, keys int
	plaintext   []byte
	expectedAAD []byte
}

func (d *submissionBytesTestDecryptor) KeyVersion() string {
	d.keys++
	if d.mode == "key_panic" || d.mode == "late_key_panic" && d.keys > 1 {
		panic("private key failure")
	}
	if d.mode == "wrong_key" || d.mode == "key_rotation" && d.keys > 1 {
		return "unexpected-key"
	}
	return d.base.KeyVersion()
}

func (d *submissionBytesTestDecryptor) Decrypt(version string, ciphertext, nonce, aad []byte) ([]byte, error) {
	d.calls++
	if d.mode == "decrypt_panic" {
		panic("private decryption failure")
	}
	if !bytes.Equal(aad, d.expectedAAD) {
		return nil, errors.New("wrong AAD")
	}
	raw, err := d.base.Decrypt(version, ciphertext, nonce, aad)
	d.plaintext = raw
	if err != nil {
		return raw, err
	}
	switch d.mode {
	case "ciphertext_mutation":
		ciphertext[0] ^= 1
	case "nonce_mutation":
		nonce[0] ^= 1
	case "aad_mutation":
		aad[0] ^= 1
	case "raw_mutation":
		raw[0] ^= 1
	case "plaintext_alias":
		copy(ciphertext, raw)
		clear(raw)
		raw = ciphertext[:len(raw)]
		d.plaintext = raw
	case "decrypt_error":
		return raw, errors.New("private decryption failure")
	case "cancel":
		d.cancel()
	case "empty":
		clear(raw)
		return nil, nil
	case "oversized":
		clear(raw)
		raw = bytes.Repeat([]byte{9}, manualartifact.MaxRawBytes+1)
		d.plaintext = raw
	}
	return raw, nil
}

func assertSubmissionBytesCleared(t *testing.T, raw []byte) {
	t.Helper()
	if !bytes.Equal(raw, make([]byte, len(raw))) {
		t.Fatal("borrowed plaintext was not cleared")
	}
}

func TestManualSubmissionBytesExactBorrowAndCleanup(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			encrypted, facts, base, original := submissionBytesFixture(t, side)
			aad, _ := facts.AAD()
			decryptor := &submissionBytesTestDecryptor{base: base, expectedAAD: aad}
			var borrowed []byte
			calls := 0
			if err := withManualSubmissionBytes(context.Background(), encrypted, facts, decryptor, func(raw []byte) error {
				calls++
				if !bytes.Equal(raw, original) {
					t.Fatal("callback did not receive original exact bytes")
				}
				assertSubmissionBytesCleared(t, decryptor.plaintext)
				borrowed = raw
				return nil
			}); err != nil || calls != 1 || decryptor.calls != 1 {
				t.Fatalf("callback=%d decrypt=%d error=%v", calls, decryptor.calls, err)
			}
			assertSubmissionBytesCleared(t, borrowed)
			assertSubmissionBytesCleared(t, decryptor.plaintext)
			if _, err := manualartifact.Verify(original, facts); err != nil {
				t.Fatal("caller-owned original changed", err)
			}
		})
	}
}

func TestManualSubmissionBytesEnvelopeAndDecryptorDenials(t *testing.T) {
	encrypted, facts, base, _ := submissionBytesFixture(t, "buy")
	for _, mode := range []string{"format", "facts_hash", "tx_hash", "raw_hash", "bad_aad", "bad_ciphertext", "short_ciphertext", "oversized_ciphertext", "short_nonce", "bad_nonce", "invalid_key", "wrong_key", "key_rotation", "key_panic", "late_key_panic", "decrypt_panic", "ciphertext_mutation", "nonce_mutation", "aad_mutation", "raw_mutation", "plaintext_alias", "decrypt_error", "cancel", "empty", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			input, bound := encrypted, facts
			input.Ciphertext = append([]byte(nil), encrypted.Ciphertext...)
			input.EncryptionNonce = append([]byte(nil), encrypted.EncryptionNonce...)
			switch mode {
			case "format":
				input.Version++
			case "facts_hash":
				input.FactsHash = strings.Repeat("a", 64)
			case "tx_hash":
				input.TxHash = "0x" + strings.Repeat("a", 64)
			case "raw_hash":
				input.RawSHA256 = strings.Repeat("a", 64)
			case "bad_aad":
				bound.RequestID += "-other"
				_, input.FactsHash, _ = bound.Canonical()
			case "bad_ciphertext":
				input.Ciphertext[0] ^= 1
			case "short_ciphertext":
				input.Ciphertext = input.Ciphertext[:16]
			case "oversized_ciphertext":
				input.Ciphertext = make([]byte, manualartifact.MaxRawBytes+17)
			case "short_nonce":
				input.EncryptionNonce = input.EncryptionNonce[:11]
			case "bad_nonce":
				input.EncryptionNonce[0] ^= 1
			case "invalid_key":
				input.KeyVersion = ""
			}
			beforeCiphertext, beforeNonce := append([]byte(nil), input.Ciphertext...), append([]byte(nil), input.EncryptionNonce...)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			aad, _ := bound.AAD()
			decryptor := &submissionBytesTestDecryptor{base: base, mode: mode, cancel: cancel, expectedAAD: aad}
			calls := 0
			err := withManualSubmissionBytes(ctx, input, bound, decryptor, func([]byte) error { calls++; return nil })
			if err == nil || calls != 0 {
				t.Fatalf("invalid bytes reached callback=%d error=%v", calls, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("primitive error leaked")
			}
			assertSubmissionBytesCleared(t, decryptor.plaintext)
			if !bytes.Equal(input.Ciphertext, beforeCiphertext) || !bytes.Equal(input.EncryptionNonce, beforeNonce) {
				t.Fatal("decryptor mutated caller-owned envelope")
			}
		})
	}
}

func TestManualSubmissionBytesCallbackFailureAndCancellation(t *testing.T) {
	encrypted, facts, base, original := submissionBytesFixture(t, "buy")
	for _, mode := range []string{"error", "panic", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wanted := errors.New("controlled callback failure")
			var borrowed []byte
			calls := 0
			err := withManualSubmissionBytes(ctx, encrypted, facts, base, func(raw []byte) error {
				calls++
				borrowed = raw
				if !bytes.Equal(raw, original) {
					t.Fatal("callback bytes changed")
				}
				switch mode {
				case "error":
					return wanted
				case "panic":
					panic("private callback panic")
				default:
					cancel()
					return nil
				}
			})
			if err == nil || calls != 1 || len(borrowed) == 0 {
				t.Fatalf("callback=%d error=%v", calls, err)
			}
			if mode == "error" && !errors.Is(err, wanted) || mode == "cancel" && !errors.Is(err, context.Canceled) || mode == "panic" && !errors.Is(err, manualartifact.ErrCipher) {
				t.Fatal("wrong callback error", err)
			}
			assertSubmissionBytesCleared(t, borrowed)
		})
	}
}

func TestManualSubmissionBytesInvalidDependenciesAndPrecancel(t *testing.T) {
	encrypted, facts, base, _ := submissionBytesFixture(t, "buy")
	for _, mode := range []string{"nil_context", "nil_decryptor", "typed_nil_decryptor", "nil_callback", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			aad, _ := facts.AAD()
			probe := &submissionBytesTestDecryptor{base: base, expectedAAD: aad}
			var decryptor manualArtifactDecryptor = probe
			calls := 0
			callback := func([]byte) error { calls++; return nil }
			switch mode {
			case "nil_context":
				ctx = nil
			case "nil_decryptor":
				decryptor = nil
			case "typed_nil_decryptor":
				decryptor = (*submissionBytesTestDecryptor)(nil)
			case "nil_callback":
				callback = nil
			case "cancel":
				cancel()
			}
			if err := withManualSubmissionBytes(ctx, encrypted, facts, decryptor, callback); err == nil || calls != 0 || probe.calls != 0 {
				t.Fatalf("invalid call reached callback=%d decrypt=%d error=%v", calls, probe.calls, err)
			}
		})
	}
}
