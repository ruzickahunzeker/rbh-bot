package trade

import (
	"bytes"
	"errors"
	"testing"
)

func TestAESGCMKeyringRotationDecryptsHistoricalV1(t *testing.T) {
	keyV1 := bytes.Repeat([]byte{0x11}, 32)
	keyV2 := bytes.Repeat([]byte{0x22}, 32)
	aad := []byte("rbh:4663:op-1:step-1:attempt-1:manual:v1:abc")
	plainV1 := []byte("signed-raw-tx-v1")

	c1, err := NewAESGCMCipher("v1", keyV1)
	if err != nil {
		t.Fatal(err)
	}
	ctV1, nonceV1, err := c1.Encrypt(plainV1, aad)
	if err != nil {
		t.Fatal(err)
	}

	ring, err := NewAESGCMKeyring("v2", map[string][]byte{
		"v1": keyV1,
		"v2": keyV2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ring.KeyVersion() != "v2" {
		t.Fatalf("got active version %q, want v2", ring.KeyVersion())
	}

	// Historical v1 ciphertext must decrypt cleanly under the rotated keyring.
	gotV1, err := ring.Decrypt("v1", ctV1, nonceV1, aad)
	if err != nil {
		t.Fatalf("decrypt v1 under keyring failed: %v", err)
	}
	if !bytes.Equal(gotV1, plainV1) {
		t.Fatalf("plaintext mismatch: got %q want %q", gotV1, plainV1)
	}

	// New encryptions use v2 and reject decryption under v1.
	plainV2 := []byte("signed-raw-tx-v2")
	ctV2, nonceV2, err := ring.Encrypt(plainV2, aad)
	if err != nil {
		t.Fatal(err)
	}
	gotV2, err := ring.Decrypt("v2", ctV2, nonceV2, aad)
	if err != nil || !bytes.Equal(gotV2, plainV2) {
		t.Fatalf("decrypt v2 failed: %q, %v", gotV2, err)
	}
	if _, err := ring.Decrypt("v3", ctV2, nonceV2, aad); !errors.Is(err, ErrUnknownKeyVersion) {
		t.Fatalf("expected ErrUnknownKeyVersion, got %v", err)
	}
}
