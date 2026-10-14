package manualartifact_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

func artifactFixture(t *testing.T) (manualartifact.Facts, *ecdsa.PrivateKey, manualartifact.Cipher) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	f := manualartifact.Facts{Version: manualartifact.Version, OperationID: "manual-op", RequestID: "req-1", ExecutionStepID: "manual-exec:manual-op", AttemptID: "manual-nonce:manual-op", WalletID: "wallet-01", ReservationID: "manual-risk:manual-op",
		ContractHash: strings.Repeat("1", 64), ChainID: 4663, From: crypto.PubkeyToAddress(key.PublicKey).Hex(), To: "0x2000000000000000000000000000000000000002",
		Value: "10000000000000000", Data: "0x12345678", Nonce: "0", GasLimit: 100000, GasFeeCap: "1000000000", GasTipCap: "0", PolicyVersion: 1, PolicyHash: strings.Repeat("2", 64),
		QuoteBlockNumber: 100, QuoteBlockHash: "0x" + strings.Repeat("3", 64), RuntimeCodeHash: "0x" + strings.Repeat("4", 64), ExpiresAt: "2026-10-14T00:00:30Z", ClaimedAt: "2026-10-14T00:00:00Z", ClaimObservationHash: strings.Repeat("5", 64), DeadlineCapability: "APPLICATION_TTL_ONLY",
		AuthorizationID: "auth-1", AuthorizationEpoch: 1, ManualPolicyID: "policy-1", ManualPolicyHash: strings.Repeat("6", 64), GateEvidenceSetHash: strings.Repeat("7", 64), Environment: "local", BuildSHA: strings.Repeat("8", 40), ReleaseID: "local-release", DeploymentID: "local-deployment"}
	c, err := trade.NewAESGCMCipher("fixture-only", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return f, key, c
}

func rawSigned(t *testing.T, tx *types.Transaction, key *ecdsa.PrivateKey, chain *big.Int) []byte {
	t.Helper()
	signer, err := trade.NewLocalSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.SignTransaction(context.Background(), tx, chain)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signedFacts(t *testing.T, f manualartifact.Facts, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	tx, err := f.Transaction()
	if err != nil {
		t.Fatal(err)
	}
	return rawSigned(t, tx, key, big.NewInt(4663))
}

func TestManualArtifactExactRoundTripNonceTipAndOwnedBuffers(t *testing.T) {
	for _, nonce := range []string{"0", "9223372036854775808", "18446744073709551614"} {
		for _, tip := range []string{"0", "100000000"} {
			t.Run(nonce+tip, func(t *testing.T) {
				f, key, c := artifactFixture(t)
				f.Nonce = nonce
				f.GasTipCap = tip
				raw := signedFacts(t, f, key)
				original := append([]byte(nil), raw...)
				rawFacts, digest, err := f.Canonical()
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := manualartifact.Decode(rawFacts)
				if err != nil || decoded != f {
					t.Fatal("canonical round trip", err)
				}
				v, err := manualartifact.Seal(raw, f, c)
				if err != nil || v.FactsHash != digest || v.KeyVersion != "fixture-only" || !bytes.Equal(original, raw) || bytes.Contains(v.Ciphertext, raw) {
					t.Fatal("seal", v.Version, err)
				}
				before := append([]byte(nil), v.Ciphertext...)
				checked, err := manualartifact.Open(v, f, c)
				if err != nil || checked.TxHash != v.TxHash || checked.FactsHash != digest || !bytes.Equal(before, v.Ciphertext) {
					t.Fatal("open", err)
				}
				aad, err := f.AAD()
				if err != nil || string(aad) != "rbh:4663:manual-op:manual-exec:manual-op:manual-nonce:manual-op:manual:v1:"+digest {
					t.Fatal("AAD identity", err)
				}
				// Existing legacy AAD cannot decrypt this MANUAL format.
				if _, err := c.Decrypt(v.KeyVersion, v.Ciphertext, v.EncryptionNonce, []byte("rbh:4663:manual-op:manual-exec:manual-op:manual-nonce:manual-op")); err == nil {
					t.Fatal("legacy AAD accepted")
				}
			})
		}
	}
}

func TestManualArtifactRejectsEverySignedFieldSubstitution(t *testing.T) {
	f, key, c := artifactFixture(t)
	for _, field := range []string{"chain", "nonce", "to", "creation", "value", "data", "gas", "fee", "tip", "access_list", "sender", "legacy", "access_list_type", "unsigned"} {
		t.Run(field, func(t *testing.T) {
			tx, _ := f.Transaction()
			to := *tx.To()
			d := &types.DynamicFeeTx{ChainID: big.NewInt(4663), Nonce: tx.Nonce(), To: &to, Value: tx.Value(), Data: tx.Data(), Gas: tx.Gas(), GasFeeCap: tx.GasFeeCap(), GasTipCap: tx.GasTipCap()}
			chain := big.NewInt(4663)
			useKey := key
			switch field {
			case "chain":
				d.ChainID = big.NewInt(1)
				chain = big.NewInt(1)
			case "nonce":
				d.Nonce++
			case "to":
				other := common.HexToAddress("0x3000000000000000000000000000000000000003")
				d.To = &other
			case "creation":
				d.To = nil
			case "value":
				d.Value = big.NewInt(1)
			case "data":
				d.Data = []byte{1, 2, 3}
			case "gas":
				d.Gas++
			case "fee":
				d.GasFeeCap = big.NewInt(2000000000)
			case "tip":
				d.GasTipCap = big.NewInt(1)
			case "access_list":
				d.AccessList = types.AccessList{{Address: to}}
			case "sender":
				var err error
				useKey, err = crypto.GenerateKey()
				if err != nil {
					t.Fatal(err)
				}
			}
			tx = types.NewTx(d)
			if field == "legacy" {
				tx = types.NewTx(&types.LegacyTx{Nonce: d.Nonce, To: d.To, Value: d.Value, Data: d.Data, Gas: d.Gas, GasPrice: d.GasFeeCap})
			}
			if field == "access_list_type" {
				tx = types.NewTx(&types.AccessListTx{ChainID: d.ChainID, Nonce: d.Nonce, To: d.To, Value: d.Value, Data: d.Data, Gas: d.Gas, GasPrice: d.GasFeeCap})
			}
			var raw []byte
			if field == "unsigned" {
				raw, _ = tx.MarshalBinary()
			} else {
				raw = rawSigned(t, tx, useKey, chain)
			}
			v, err := manualartifact.Verify(raw, f)
			if err == nil || v != (manualartifact.Verification{}) {
				t.Fatal("substitution accepted", field)
			}
			encrypted, err := manualartifact.Seal(raw, f, c)
			if err == nil || !reflect.DeepEqual(encrypted, manualartifact.Encrypted{}) {
				t.Fatal("invalid raw encrypted", field)
			}
		})
	}
	for _, raw := range [][]byte{nil, {}, {2, 0}, append(signedFacts(t, f, key), 0), bytes.Repeat([]byte{2}, manualartifact.MaxRawBytes+1)} {
		v, err := manualartifact.Verify(raw, f)
		if err == nil || v != (manualartifact.Verification{}) {
			t.Fatal("malformed raw accepted")
		}
	}
}

func TestManualArtifactRejectsNoncanonicalIdentity(t *testing.T) {
	f, key, _ := artifactFixture(t)
	bad := []struct {
		name   string
		change func(*manualartifact.Facts)
	}{
		{"version", func(f *manualartifact.Facts) { f.Version = 0 }}, {"chain", func(f *manualartifact.Facts) { f.ChainID = 1 }},
		{"step", func(f *manualartifact.Facts) { f.ExecutionStepID = "other" }}, {"attempt", func(f *manualartifact.Facts) { f.AttemptID = "other" }}, {"reservation", func(f *manualartifact.Facts) { f.ReservationID = "other" }},
		{"nul_identity", func(f *manualartifact.Facts) { f.WalletID += "\x00" }}, {"oversized_identity", func(f *manualartifact.Facts) { f.RequestID = strings.Repeat("a", 129) }},
		{"nonce_leading_zero", func(f *manualartifact.Facts) { f.Nonce = "00" }}, {"nonce_max", func(f *manualartifact.Facts) { f.Nonce = "18446744073709551615" }}, {"nonce_overflow", func(f *manualartifact.Facts) { f.Nonce = "18446744073709551616" }},
		{"value_plus", func(f *manualartifact.Facts) { f.Value = "+1" }}, {"value_leading_zero", func(f *manualartifact.Facts) { f.Value = "01" }}, {"value_negative", func(f *manualartifact.Facts) { f.Value = "-1" }}, {"value_overflow", func(f *manualartifact.Facts) { f.Value = new(big.Int).Lsh(big.NewInt(1), 256).String() }},
		{"zero_fee", func(f *manualartifact.Facts) { f.GasFeeCap = "0" }}, {"tip_above_fee", func(f *manualartifact.Facts) { f.GasTipCap = "1000000001" }}, {"bad_tip", func(f *manualartifact.Facts) { f.GasTipCap = " 0" }},
		{"zero_gas", func(f *manualartifact.Facts) { f.GasLimit = 0 }}, {"gas_over_SQL", func(f *manualartifact.Facts) { f.GasLimit = math.MaxUint64 }}, {"policy_over_SQL", func(f *manualartifact.Facts) { f.PolicyVersion = math.MaxUint64 }},
		{"gas_budget_overflow", func(f *manualartifact.Facts) {
			f.GasFeeCap = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).String()
		}},
		{"no_quote", func(f *manualartifact.Facts) { f.QuoteBlockNumber = 0 }}, {"zero_epoch", func(f *manualartifact.Facts) { f.AuthorizationEpoch = 0 }},
		{"zero_address", func(f *manualartifact.Facts) { f.To = "0x" + strings.Repeat("0", 40) }}, {"short_address", func(f *manualartifact.Facts) { f.From = "0x01" }},
		{"uppercase_data", func(f *manualartifact.Facts) { f.Data = "0xDEAD" }}, {"odd_data", func(f *manualartifact.Facts) { f.Data = "0x123" }}, {"empty_data", func(f *manualartifact.Facts) { f.Data = "0x" }}, {"large_data", func(f *manualartifact.Facts) { f.Data = "0x" + strings.Repeat("ab", manualartifact.MaxDataBytes+1) }},
		{"missing_hash", func(f *manualartifact.Facts) { f.PolicyHash = "" }}, {"bad_runtime", func(f *manualartifact.Facts) { f.RuntimeCodeHash = "0x" + strings.Repeat("0", 64) }},
		{"bad_quote", func(f *manualartifact.Facts) { f.QuoteBlockHash += "\x00" }}, {"bad_build", func(f *manualartifact.Facts) { f.BuildSHA = "ABC" }},
		{"expiry_offset", func(f *manualartifact.Facts) { f.ExpiresAt = "2026-10-14T00:00:30+00:00" }}, {"expiry_nul", func(f *manualartifact.Facts) { f.ExpiresAt += "\x00" }},
		{"claim_equality", func(f *manualartifact.Facts) { f.ClaimedAt = f.ExpiresAt }}, {"claim_after", func(f *manualartifact.Facts) { f.ClaimedAt = "2026-10-14T00:00:31Z" }},
		{"capability", func(f *manualartifact.Facts) { f.DeadlineCapability = "CONTRACT" }}, {"contract_deadline", func(f *manualartifact.Facts) { f.ContractDeadline = true }},
	}
	raw := signedFacts(t, f, key)
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			copy := f
			tc.change(&copy)
			if copy.Validate() == nil {
				t.Fatal("invalid identity accepted")
			}
			if tx, err := copy.Transaction(); err == nil || tx != nil {
				t.Fatal("invalid candidate transaction")
			}
			if v, err := manualartifact.Verify(raw, copy); err == nil || v != (manualartifact.Verification{}) {
				t.Fatal("invalid verification")
			}
		})
	}
	canonical, _, _ := f.Canonical()
	for _, raw := range []string{" " + canonical, canonical + "\n", canonical + "\x00", canonical + "{}", strings.Replace(canonical, `"version":1`, `"version":1,"version":1`, 1), strings.Replace(canonical, `"version":1`, `"version":1,"extra":false`, 1)} {
		if got, err := manualartifact.Decode(raw); err == nil || got != (manualartifact.Facts{}) {
			t.Fatal("noncanonical JSON accepted")
		}
	}
	// Max-sized valid calldata still round trips through canonical decoding.
	f.Data = "0x" + strings.Repeat("ab", manualartifact.MaxDataBytes)
	canonical, _, err := f.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := manualartifact.Decode(canonical); err != nil || got != f {
		t.Fatal("valid maximum identity cannot roundtrip", err)
	}
}

func TestManualArtifactAADAuthenticatesEveryMetadataField(t *testing.T) {
	f, key, c := artifactFixture(t)
	raw := signedFacts(t, f, key)
	original, err := manualartifact.Seal(raw, f, c)
	if err != nil {
		t.Fatal(err)
	}
	changes := []struct {
		name   string
		change func(*manualartifact.Facts)
	}{
		{"operation", func(f *manualartifact.Facts) {
			f.OperationID = "other-op"
			f.ExecutionStepID = "manual-exec:other-op"
			f.AttemptID = "manual-nonce:other-op"
			f.ReservationID = "manual-risk:other-op"
		}},
		{"request", func(f *manualartifact.Facts) { f.RequestID = "other" }}, {"wallet", func(f *manualartifact.Facts) { f.WalletID = "other" }},
		{"contract", func(f *manualartifact.Facts) { f.ContractHash = strings.Repeat("9", 64) }}, {"policy", func(f *manualartifact.Facts) { f.PolicyVersion++ }}, {"policy_hash", func(f *manualartifact.Facts) { f.PolicyHash = strings.Repeat("9", 64) }},
		{"quote_block", func(f *manualartifact.Facts) { f.QuoteBlockNumber++ }}, {"quote_hash", func(f *manualartifact.Facts) { f.QuoteBlockHash = "0x" + strings.Repeat("9", 64) }}, {"runtime", func(f *manualartifact.Facts) { f.RuntimeCodeHash = "0x" + strings.Repeat("9", 64) }},
		{"expiry", func(f *manualartifact.Facts) { f.ExpiresAt = "2026-10-14T00:00:31Z" }}, {"claim_time", func(f *manualartifact.Facts) { f.ClaimedAt = "2026-10-14T00:00:01Z" }}, {"claim_observation", func(f *manualartifact.Facts) { f.ClaimObservationHash = strings.Repeat("9", 64) }},
		{"auth", func(f *manualartifact.Facts) { f.AuthorizationID = "other" }}, {"epoch", func(f *manualartifact.Facts) { f.AuthorizationEpoch++ }}, {"manual_policy", func(f *manualartifact.Facts) { f.ManualPolicyID = "other" }},
		{"manual_hash", func(f *manualartifact.Facts) { f.ManualPolicyHash = strings.Repeat("9", 64) }}, {"gate_hash", func(f *manualartifact.Facts) { f.GateEvidenceSetHash = strings.Repeat("9", 64) }},
		{"environment", func(f *manualartifact.Facts) { f.Environment = "other" }}, {"build", func(f *manualartifact.Facts) { f.BuildSHA = strings.Repeat("9", 40) }}, {"release", func(f *manualartifact.Facts) { f.ReleaseID = "other" }}, {"deployment", func(f *manualartifact.Facts) { f.DeploymentID = "other" }},
	}
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			changed := f
			tc.change(&changed)
			_, newHash, err := changed.Canonical()
			if err != nil {
				t.Fatal("test mutation must remain valid", err)
			}
			// Even relabeling the untrusted hash cannot bypass authenticated AAD.
			tampered := original
			tampered.FactsHash = newHash
			v, err := manualartifact.Open(tampered, changed, c)
			if err == nil || v != (manualartifact.Verification{}) {
				t.Fatal("metadata relabel accepted")
			}
		})
	}
}

type faultCipher struct {
	manualartifact.Cipher
	mode     string
	leaked   []byte
	decrypts int
}

func (c *faultCipher) KeyVersion() string {
	if c.mode == "empty_key" {
		return ""
	}
	if c.mode == "bad_key" {
		return "bad\x00version"
	}
	if c.mode == "key_drift" && c.decrypts > 0 {
		return "drifted-key"
	}
	return c.Cipher.KeyVersion()
}

func (c *faultCipher) Encrypt(raw, aad []byte) ([]byte, []byte, error) {
	c.leaked = raw
	switch c.mode {
	case "encrypt_error":
		return nil, nil, fmt.Errorf("sensitive raw %x", raw)
	case "encrypt_panic":
		panic("sensitive panic")
	case "corrupt_encrypt":
		out, n, err := c.Cipher.Encrypt(raw, aad)
		out[0] ^= 1
		return out, n, err
	case "short_nonce":
		out, _, err := c.Cipher.Encrypt(raw, aad)
		return out, []byte{1}, err
	}
	return c.Cipher.Encrypt(raw, aad)
}
func (c *faultCipher) Decrypt(v string, ct, n, aad []byte) ([]byte, error) {
	c.decrypts++
	if c.mode == "decrypt_error" {
		return nil, errors.New("sensitive injected error")
	}
	if c.mode == "decrypt_panic" {
		panic("sensitive injected panic")
	}
	raw, err := c.Cipher.Decrypt(v, ct, n, aad)
	if c.mode == "mutate_ciphertext" && len(ct) > 0 {
		ct[0] ^= 1
	}
	if c.mode == "mutate_nonce" && len(n) > 0 {
		n[0] ^= 1
	}
	c.leaked = raw
	return raw, err
}

func TestManualArtifactCipherFaultsTamperZeroOutputAndNoLeak(t *testing.T) {
	f, key, c := artifactFixture(t)
	raw := signedFacts(t, f, key)
	original := append([]byte(nil), raw...)
	for _, mode := range []string{"encrypt_error", "encrypt_panic", "corrupt_encrypt", "short_nonce", "decrypt_error", "decrypt_panic", "empty_key", "bad_key", "key_drift", "mutate_ciphertext", "mutate_nonce"} {
		t.Run(mode, func(t *testing.T) {
			fault := &faultCipher{Cipher: c, mode: mode}
			v, err := manualartifact.Seal(raw, f, fault)
			if err == nil || !reflect.DeepEqual(v, manualartifact.Encrypted{}) || strings.Contains(err.Error(), "sensitive") || !bytes.Equal(original, raw) {
				t.Fatal("fault exposed result or detail", err)
			}
			if len(fault.leaked) > 0 && !bytes.Equal(fault.leaked, make([]byte, len(fault.leaked))) {
				t.Fatal("owned plaintext buffer not cleared")
			}
		})
	}
	v, err := manualartifact.Seal(raw, f, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"version", "facts_hash", "tx_hash", "raw_hash", "key", "ciphertext", "nonce", "nonce_short", "empty", "size"} {
		t.Run(field, func(t *testing.T) {
			copy := v
			copy.Ciphertext = append([]byte(nil), v.Ciphertext...)
			copy.EncryptionNonce = append([]byte(nil), v.EncryptionNonce...)
			switch field {
			case "version":
				copy.Version++
			case "facts_hash":
				copy.FactsHash = "bad"
			case "tx_hash":
				copy.TxHash = "bad"
			case "raw_hash":
				copy.RawSHA256 = "bad"
			case "key":
				copy.KeyVersion = "other"
			case "ciphertext":
				copy.Ciphertext[0] ^= 1
			case "nonce":
				copy.EncryptionNonce[0] ^= 1
			case "nonce_short":
				copy.EncryptionNonce = []byte{1}
			case "empty":
				copy.Ciphertext = nil
			case "size":
				copy.Ciphertext = make([]byte, manualartifact.MaxRawBytes+17)
			}
			checked, err := manualartifact.Open(copy, f, c)
			if err == nil || checked != (manualartifact.Verification{}) {
				t.Fatal("corrupt artifact accepted")
			}
		})
	}
	wrong, _ := trade.NewAESGCMCipher("fixture-only", bytes.Repeat([]byte{8}, 32))
	if got, err := manualartifact.Open(v, f, wrong); err == nil || got != (manualartifact.Verification{}) {
		t.Fatal("wrong key accepted")
	}
	var nilCipher *trade.AESGCMCipher
	for _, cipher := range []manualartifact.Cipher{nil, nilCipher} {
		if got, err := manualartifact.Seal(raw, f, cipher); err == nil || !reflect.DeepEqual(got, manualartifact.Encrypted{}) {
			t.Fatal("nil cipher accepted")
		}
	}
	spy := &faultCipher{Cipher: c}
	if _, err := manualartifact.Seal(raw, f, spy); err != nil {
		t.Fatal(err)
	}
	if len(spy.leaked) == 0 || !bytes.Equal(spy.leaked, make([]byte, len(spy.leaked))) {
		t.Fatal("successful decrypted buffer retained")
	}
}
