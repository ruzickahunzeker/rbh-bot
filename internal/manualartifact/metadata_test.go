package manualartifact_test

import (
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
)

func TestManualArtifactEnvelopeMetadataCanonicalShapeNotCrypto(t *testing.T) {
	f, key, cipher := artifactFixture(t)
	v, err := manualartifact.Seal(signedFacts(t, f, key), f, cipher)
	if err != nil {
		t.Fatal(err)
	}
	m := manualartifact.EnvelopeMetadata{Version: 1, FactsHash: v.FactsHash, TxHash: v.TxHash, RawSHA256: v.RawSHA256, KeyVersion: v.KeyVersion,
		Ciphertext: hex.EncodeToString(v.Ciphertext), EncryptionNonce: hex.EncodeToString(v.EncryptionNonce), CompletedAt: f.ClaimedAt}
	marshal := func(m manualartifact.EnvelopeMetadata) string { b, _ := json.Marshal(m); return string(b) }
	decoded, stamp, err := manualartifact.DecodeEnvelopeMetadata(marshal(m), f)
	if err != nil || !reflect.DeepEqual(decoded, v) || stamp != f.ClaimedAt {
		t.Fatal("exact envelope", err)
	}
	for _, kind := range []string{"NUL", "whitespace", "unknown", "key_NUL", "hash", "nonce", "cipher_hex", "cipher_length", "expiry", "before_claim", "time_offset", "version"} {
		t.Run(kind, func(t *testing.T) {
			bad := m
			switch kind {
			case "key_NUL":
				bad.KeyVersion += "\x00"
			case "hash":
				bad.FactsHash = strings.Repeat("0", 64)
			case "nonce":
				bad.EncryptionNonce += "00"
			case "cipher_hex":
				bad.Ciphertext = "GG" + bad.Ciphertext[2:]
			case "cipher_length":
				bad.Ciphertext = "00"
			case "expiry":
				bad.CompletedAt = f.ExpiresAt
			case "before_claim":
				bad.CompletedAt = "2026-10-13T23:59:59Z"
			case "time_offset":
				bad.CompletedAt = "2026-10-14T00:00:00+00:00"
			case "version":
				bad.Version = 2
			}
			raw := marshal(bad)
			switch kind {
			case "NUL":
				raw += "\x00"
			case "whitespace":
				raw += " "
			case "unknown":
				raw = strings.TrimSuffix(raw, "}") + `,"extra":false}`
			}
			got, stamp, err := manualartifact.DecodeEnvelopeMetadata(raw, f)
			if err == nil || !reflect.DeepEqual(got, manualartifact.Encrypted{}) || stamp != "" {
				t.Fatal("malformed metadata exposed candidate", kind, err)
			}
		})
	}
	// Shape validation intentionally cannot establish crypto validity.
	m.Ciphertext = strings.Repeat("00", len(v.Ciphertext))
	shaped, _, err := manualartifact.DecodeEnvelopeMetadata(marshal(m), f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manualartifact.Open(shaped, f, cipher); err == nil {
		t.Fatal("shape substituted for authenticated decryption")
	}
}
