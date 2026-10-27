package trade

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

const operatorAuthorizationDomain = "RBH-MANUAL-A4-AUTHORIZATION-V1"

// OperatorApprovalEnvelope is the canonical, signed authority payload. It is
// verification-only in A4-B1; it cannot arm an authorization or reach sending.
type OperatorApprovalEnvelope struct {
	ApprovalID          string `json:"approval_id"`
	RequestID           string `json:"request_id"`
	OperatorKeyID       string `json:"operator_key_id"`
	IssuedAt            string `json:"issued_at"`
	ExpiresAt           string `json:"expires_at"`
	AuthorizationID     string `json:"authorization_id"`
	AuthorizationEpoch  uint64 `json:"authorization_epoch"`
	BuildSHA            string `json:"build_sha"`
	ReleaseID           string `json:"release_id"`
	DeploymentID        string `json:"deployment_id"`
	Environment         string `json:"environment"`
	ChainID             uint64 `json:"chain_id"`
	WalletID            string `json:"wallet_id"`
	WalletAddress       string `json:"wallet_address"`
	PolicyVersion       uint64 `json:"policy_version"`
	PolicyHash          string `json:"policy_hash"`
	GateEvidenceSetHash string `json:"gate_evidence_set_hash"`
	MaxOperations       uint64 `json:"max_operations"`
	MaxTotalInput       string `json:"max_total_input"`
	SourceHash          string `json:"source_hash"`
	Nonce               string `json:"nonce"`
	SchemaVersion       string `json:"schema_version"`
}

func (e OperatorApprovalEnvelope) canonicalPayload() ([]byte, error) {
	if e.ApprovalID == "" || e.RequestID == "" || e.OperatorKeyID == "" || e.AuthorizationID == "" ||
		e.AuthorizationEpoch == 0 || e.ChainID != 4663 || e.Environment == "" || e.WalletID == "" ||
		e.PolicyVersion == 0 || e.MaxOperations == 0 || e.Nonce == "" || e.SchemaVersion != "1" ||
		!common.IsHexAddress(e.WalletAddress) || common.HexToAddress(e.WalletAddress) == (common.Address{}) {
		return nil, ErrCanaryRuntimeRejected
	}
	issued, err := time.Parse(time.RFC3339Nano, e.IssuedAt)
	if err != nil || issued.UTC().Format(time.RFC3339Nano) != e.IssuedAt {
		return nil, ErrCanaryRuntimeRejected
	}
	expires, err := time.Parse(time.RFC3339Nano, e.ExpiresAt)
	if err != nil || expires.UTC().Format(time.RFC3339Nano) != e.ExpiresAt || !issued.Before(expires) {
		return nil, ErrCanaryRuntimeRejected
	}
	for i, v := range []string{e.BuildSHA, e.PolicyHash, e.GateEvidenceSetHash, e.SourceHash} {
		if (i == 0 && len(v) != 40 && len(v) != 64) || (i > 0 && len(v) != 64) {
			return nil, ErrCanaryRuntimeRejected
		}
		if _, err := hex.DecodeString(v); err != nil {
			return nil, ErrCanaryRuntimeRejected
		}
	}
	if maximum, ok := canonicalPositive(e.MaxTotalInput); !ok || maximum.String() != e.MaxTotalInput {
		return nil, ErrCanaryRuntimeRejected
	}
	return json.Marshal(struct {
		Domain string `json:"domain"`
		OperatorApprovalEnvelope
	}{operatorAuthorizationDomain, e})
}

func (e OperatorApprovalEnvelope) Hash() (string, error) {
	payload, err := e.canonicalPayload()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// VerifyOperatorApproval verifies only the canonical signature and envelope
// validity. It performs no database write, state transition, signer call or
// permit operation.
func VerifyOperatorApproval(e OperatorApprovalEnvelope, publicKeyHex, signatureHex string) error {
	return verifyOperatorApprovalAt(e, publicKeyHex, signatureHex, time.Time{})
}

// VerifyOperatorApprovalAt additionally enforces the bounded approval window.
func VerifyOperatorApprovalAt(e OperatorApprovalEnvelope, publicKeyHex, signatureHex string, now time.Time) error {
	return verifyOperatorApprovalAt(e, publicKeyHex, signatureHex, now)
}

func verifyOperatorApprovalAt(e OperatorApprovalEnvelope, publicKeyHex, signatureHex string, now time.Time) error {
	payload, err := e.canonicalPayload()
	if err != nil {
		return err
	}
	publicKey, err := hex.DecodeString(publicKeyHex)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return ErrCanaryRuntimeRejected
	}
	signature, err := hex.DecodeString(signatureHex)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return ErrCanaryRuntimeRejected
	}
	if !now.IsZero() {
		issued, _ := time.Parse(time.RFC3339Nano, e.IssuedAt)
		expires, _ := time.Parse(time.RFC3339Nano, e.ExpiresAt)
		if now.UTC().Before(issued) || !now.UTC().Before(expires) {
			return ErrCanaryRuntimeRejected
		}
	}
	return nil
}
