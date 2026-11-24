package manualartifact_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

func TestFactsV2AndV1AADCoexistenceAndDeadlineDerivation(t *testing.T) {
	expires := "2026-11-24T12:00:00Z"
	deadline, err := manualartifact.DeriveContractDeadline(manualartifact.DeadlineCapabilityContractDeadlineUnix, expires)
	if err != nil || deadline <= 0 {
		t.Fatalf("unexpected deadline: %d err=%v", deadline, err)
	}
	f2 := manualartifact.FactsV2{
		Version:              2,
		ChainID:              56,
		ProtocolID:           "pancake-v2",
		AdapterRevision:      "pancake-v2-r1",
		OperationID:          "op-bsc-1",
		ExecutionStepID:      "step-1",
		AttemptID:            "attempt-1",
		WalletAddress:        "0x1000000000000000000000000000000000000001",
		Nonce:                9,
		DeadlineCapability:   manualartifact.DeadlineCapabilityContractDeadlineUnix,
		ExpiresAt:            expires,
		ContractDeadlineUnix: deadline,
	}
	aadV2, err := f2.AADv2()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(aadV2), "evm:56:op-bsc-1:step-1:attempt-1:manual:v2:") {
		t.Fatalf("unexpected AAD v2 prefix: %s", string(aadV2))
	}

	// Encrypt and decrypt v2 artifact using AESGCMKeyring; cross-chain AAD tampering must fail.
	ring, err := trade.NewAESGCMKeyring("v2", map[string][]byte{
		"v1": bytes.Repeat([]byte{0x11}, 32),
		"v2": bytes.Repeat([]byte{0x22}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	rawTx := []byte("bsc-raw-signed-bytes")
	ct, nonce, err := ring.Encrypt(rawTx, aadV2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ring.Decrypt("v2", ct, nonce, aadV2)
	if err != nil || !bytes.Equal(got, rawTx) {
		t.Fatalf("v2 decrypt failed: %q err=%v", got, err)
	}
	fWrongChain := f2
	fWrongChain.ChainID = 4663
	wrongAAD, _ := fWrongChain.AADv2()
	if _, err := ring.Decrypt("v2", ct, nonce, wrongAAD); err == nil {
		t.Fatal("expected cross-chain AAD mismatch to fail decryption")
	}

	// Verify v1 AAD prefix ("rbh:4663:...:manual:v1:") still decrypts under the same keyring.
	v1AAD := []byte("rbh:4663:op-rbh-1:step-1:attempt-1:manual:v1:" + strings.Repeat("a", 64))
	c1, _ := trade.NewAESGCMCipher("v1", bytes.Repeat([]byte{0x11}, 32))
	ctV1, nV1, err := c1.Encrypt([]byte("rbh-v1-tx"), v1AAD)
	if err != nil {
		t.Fatal(err)
	}
	gotV1, err := ring.Decrypt("v1", ctV1, nV1, v1AAD)
	if err != nil || string(gotV1) != "rbh-v1-tx" {
		t.Fatalf("v1 decrypt under rotated keyring failed: %q err=%v", gotV1, err)
	}
}
