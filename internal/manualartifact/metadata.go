package manualartifact

import (
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
)

// EnvelopeMetadata validates storage shape and identity only. Without a cipher
// it cannot establish that ciphertext contains a valid signed transaction.
type EnvelopeMetadata struct {
	Version         uint64 `json:"version"`
	FactsHash       string `json:"facts_hash"`
	TxHash          string `json:"tx_hash"`
	RawSHA256       string `json:"raw_sha256"`
	KeyVersion      string `json:"key_version"`
	Ciphertext      string `json:"ciphertext"`
	EncryptionNonce string `json:"encryption_nonce"`
	CompletedAt     string `json:"completed_at"`
}

func DecodeEnvelopeMetadata(raw string, f Facts) (Encrypted, string, error) {
	var m EnvelopeMetadata
	if len(raw) > 2*MaxRawBytes+2048 || json.Unmarshal([]byte(raw), &m) != nil {
		return Encrypted{}, "", ErrIdentity
	}
	canonical, err := json.Marshal(m)
	_, digest, fe := f.Canonical()
	completed, ce := manualnonce.CanonicalTime(m.CompletedAt)
	claimed, _ := manualnonce.CanonicalTime(f.ClaimedAt)
	expires, _ := manualnonce.CanonicalTime(f.ExpiresAt)
	if err != nil || fe != nil || ce != nil || string(canonical) != raw || m.Version != Version || m.FactsHash != digest ||
		completed.Before(claimed) || !completed.Before(expires) || !fixedHex(m.TxHash, 64, true, true) ||
		!fixedHex(m.RawSHA256, 64, false, true) || !validID(m.KeyVersion) || len(m.EncryptionNonce) != 24 ||
		len(m.Ciphertext) <= 32 || len(m.Ciphertext) > 2*(MaxRawBytes+16) || strings.ToLower(m.Ciphertext) != m.Ciphertext || strings.ToLower(m.EncryptionNonce) != m.EncryptionNonce {
		return Encrypted{}, "", ErrIdentity
	}
	ciphertext, e1 := hex.DecodeString(m.Ciphertext)
	nonce, e2 := hex.DecodeString(m.EncryptionNonce)
	if e1 != nil || e2 != nil {
		return Encrypted{}, "", ErrIdentity
	}
	return Encrypted{Version: m.Version, FactsHash: m.FactsHash, TxHash: m.TxHash, RawSHA256: m.RawSHA256, KeyVersion: m.KeyVersion, Ciphertext: ciphertext, EncryptionNonce: nonce}, m.CompletedAt, nil
}
