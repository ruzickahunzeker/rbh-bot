package trade

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"
)

func testOperatorEnvelope() OperatorApprovalEnvelope {
	stamp := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	return OperatorApprovalEnvelope{
		ApprovalID: "approval-1", RequestID: "request-1", OperatorKeyID: "key-1",
		IssuedAt: stamp.Format(time.RFC3339Nano), ExpiresAt: stamp.Add(time.Minute).Format(time.RFC3339Nano),
		AuthorizationID: "auth-1", AuthorizationEpoch: 1, BuildSHA: "0123456789abcdef0123456789abcdef01234567",
		ReleaseID: "release-1", DeploymentID: "deployment-1", Environment: "controlled",
		ChainID: 4663, WalletID: "wallet-1", WalletAddress: "0x1111111111111111111111111111111111111111",
		PolicyVersion: 1, PolicyHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		GateEvidenceSetHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		MaxOperations:       1, MaxTotalInput: "1", SourceHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Nonce: "nonce-1", SchemaVersion: "1",
	}
}

func TestOperatorApprovalCanonicalSignatureVerification(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	e := testOperatorEnvelope()
	payload, err := e.canonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	signature, err := private.Sign(nil, payload, crypto.Hash(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyOperatorApproval(e, hex.EncodeToString(public), hex.EncodeToString(signature)); err != nil {
		t.Fatal(err)
	}
	issued, _ := time.Parse(time.RFC3339Nano, e.IssuedAt)
	expires, _ := time.Parse(time.RFC3339Nano, e.ExpiresAt)
	if err := VerifyOperatorApprovalAt(e, hex.EncodeToString(public), hex.EncodeToString(signature), issued); err != nil {
		t.Fatal("issued boundary rejected", err)
	}
	if err := VerifyOperatorApprovalAt(e, hex.EncodeToString(public), hex.EncodeToString(signature), expires); err == nil {
		t.Fatal("expiry boundary accepted")
	}
	if _, err := e.Hash(); err != nil {
		t.Fatal(err)
	}
	if err := VerifyOperatorApproval(e, hex.EncodeToString(public), hex.EncodeToString(make([]byte, ed25519.SignatureSize))); err == nil {
		t.Fatal("invalid signature accepted")
	}
	e.Nonce = "changed"
	if err := VerifyOperatorApproval(e, hex.EncodeToString(public), hex.EncodeToString(signature)); err == nil {
		t.Fatal("mutated envelope accepted")
	}
}
