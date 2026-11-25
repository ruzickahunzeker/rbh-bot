package trade

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

func TestGlobalKeystoreV2AndLegacyV1Compat(t *testing.T) {
	dir := t.TempDir()
	pass := "StrongPassphrase!2026"

	// 1. Create a legacy v1 chain-bound wallet (chain_id=56 in AAD) and verify it still decrypts unchanged.
	v1Sum, err := CreateEncryptedWallet(dir, "legacy-bsc-1", 56, pass)
	if err != nil {
		t.Fatalf("create v1 wallet: %v", err)
	}
	v1RawBefore, err := os.ReadFile(v1Sum.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	v1Priv, v1Loaded, err := LoadEncryptedWallet(v1Sum.FilePath, pass)
	if err != nil {
		t.Fatalf("load legacy v1 wallet: %v", err)
	}
	if v1Loaded.Version != 1 || v1Loaded.ChainID != 56 || v1Loaded.Address != v1Sum.Address || crypto.PubkeyToAddress(v1Priv.PublicKey) != v1Sum.Address {
		t.Fatalf("unexpected v1 loaded summary: %+v", v1Loaded)
	}

	// 2. Create a new chain-agnostic v2 global wallet.
	v2Sum, err := CreateGlobalEncryptedWallet(dir, "w-evm-001", 7131128175, pass)
	if err != nil {
		t.Fatalf("create v2 wallet: %v", err)
	}
	if v2Sum.Version != 2 || v2Sum.ChainID != 0 || v2Sum.OwnerChatID != 7131128175 || v2Sum.WalletType != "keystore" {
		t.Fatalf("unexpected v2 summary: %+v", v2Sum)
	}
	v2Priv, v2Loaded, err := LoadEncryptedWallet(v2Sum.FilePath, pass)
	if err != nil {
		t.Fatalf("load v2 wallet: %v", err)
	}
	if v2Loaded.Version != 2 || v2Loaded.ChainID != 0 || crypto.PubkeyToAddress(v2Priv.PublicKey) != v2Sum.Address {
		t.Fatalf("unexpected v2 loaded: %+v", v2Loaded)
	}

	// 3. Re-importing the exact same private key must return duplicate=true and create NO duplicate file.
	privHex := "0x" + hex.EncodeToString(crypto.FromECDSA(v2Priv))
	dupSum, dup, err := ImportGlobalEncryptedWallet(dir, "imp-evm-dup", 7131128175, privHex, pass)
	if err != nil {
		t.Fatalf("duplicate import returned error: %v", err)
	}
	if !dup || dupSum.WalletID != v2Sum.WalletID || dupSum.Address != v2Sum.Address {
		t.Fatalf("expected duplicate hit for %s, got dup=%v sum=%+v", v2Sum.WalletID, dup, dupSum)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "imp-evm-dup.json")); !os.IsNotExist(statErr) {
		t.Fatalf("duplicate import must not write a second keystore file")
	}

	// 4. ListEncryptedWallets returns both v1 and v2 wallets, and v1 file bytes were never altered.
	all, err := ListEncryptedWallets(dir)
	if err != nil {
		t.Fatalf("list wallets: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 wallets (v1 + v2), got %d", len(all))
	}
	v1RawAfter, _ := os.ReadFile(v1Sum.FilePath)
	if string(v1RawBefore) != string(v1RawAfter) {
		t.Fatalf("legacy v1 keystore file must never be mutated")
	}
}
