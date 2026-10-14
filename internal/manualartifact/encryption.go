package manualartifact

import (
	"bytes"
	"reflect"
)

// Structural subset of the existing trade.ArtifactCipher: no signer, key
// loading, RPC, database or send method. Implementations are trusted local
// primitives. Context/TTL/durable fencing belong to later orchestration.
type Cipher interface {
	KeyVersion() string
	Encrypt([]byte, []byte) ([]byte, []byte, error)
	Decrypt(string, []byte, []byte, []byte) ([]byte, error)
}

// Candidate only. The later writer must independently verify gates and commit
// exact attempt/step/lane effects. No raw transaction or permission is exposed.
type Encrypted struct {
	Version                                  uint64
	FactsHash, TxHash, RawSHA256, KeyVersion string
	Ciphertext, EncryptionNonce              []byte
}

func clearBytes(v []byte) {
	for i := range v {
		v[i] = 0
	}
}
func usable(c Cipher) bool {
	if c == nil {
		return false
	}
	v := reflect.ValueOf(c)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return !v.IsNil()
	}
	return true
}

// Seal copies input before handing it to the local cipher, so caller-owned raw
// bytes remain untouched. Returned buffers are owned, not cipher aliases.
func Seal(raw []byte, f Facts, c Cipher) (result Encrypted, err error) {
	defer func() {
		if recover() != nil {
			result = Encrypted{}
			err = ErrCipher
		}
	}()
	verified, err := Verify(raw, f)
	if err != nil {
		return Encrypted{}, err
	}
	if !usable(c) {
		return Encrypted{}, ErrCipher
	}
	version := c.KeyVersion()
	if !validID(version) {
		return Encrypted{}, ErrCipher
	}
	aad, err := f.AAD()
	if err != nil {
		return Encrypted{}, err
	}
	plaintext := append([]byte(nil), raw...)
	defer clearBytes(plaintext)
	ciphertext, nonce, err := c.Encrypt(plaintext, aad)
	if err != nil || len(ciphertext) != len(raw)+16 || len(ciphertext) > MaxRawBytes+16 || len(nonce) != 12 || c.KeyVersion() != version {
		return Encrypted{}, ErrCipher
	}
	result = Encrypted{Version: Version, FactsHash: verified.FactsHash, TxHash: verified.TxHash, RawSHA256: verified.RawSHA256, KeyVersion: version,
		Ciphertext: append([]byte(nil), ciphertext...), EncryptionNonce: append([]byte(nil), nonce...)}
	if _, err = Open(result, f, c); err != nil {
		return Encrypted{}, ErrCipher
	}
	return result, nil
}

// Open verifies ciphertext against ORIGINAL facts, even when used for history.
// No clock check here: verifying bytes is not permission to act on old bytes.
func Open(v Encrypted, f Facts, c Cipher) (result Verification, err error) {
	defer func() {
		if recover() != nil {
			result = Verification{}
			err = ErrCipher
		}
	}()
	_, digest, err := f.Canonical()
	if err != nil {
		return Verification{}, err
	}
	if !usable(c) || v.Version != Version || !validID(v.KeyVersion) || v.KeyVersion != c.KeyVersion() || v.FactsHash != digest ||
		!fixedHex(v.TxHash, 64, true, true) || !fixedHex(v.RawSHA256, 64, false, true) ||
		len(v.Ciphertext) <= 16 || len(v.Ciphertext) > MaxRawBytes+16 || len(v.EncryptionNonce) != 12 {
		return Verification{}, ErrCipher
	}
	aad, _ := f.AAD()
	// Own copies prevent a faulty local implementation from mutating durable or
	// caller-owned ciphertext and nonce buffers during verification.
	ciphertext := append([]byte(nil), v.Ciphertext...)
	nonce := append([]byte(nil), v.EncryptionNonce...)
	plaintext, err := c.Decrypt(v.KeyVersion, ciphertext, nonce, aad)
	defer clearBytes(plaintext)
	if err != nil {
		return Verification{}, ErrCipher
	}
	verified, err := Verify(plaintext, f)
	if err != nil || verified.FactsHash != v.FactsHash || verified.TxHash != v.TxHash || verified.RawSHA256 != v.RawSHA256 ||
		c.KeyVersion() != v.KeyVersion || !bytes.Equal(ciphertext, v.Ciphertext) || !bytes.Equal(nonce, v.EncryptionNonce) {
		return Verification{}, ErrIntegrity
	}
	return verified, nil
}
