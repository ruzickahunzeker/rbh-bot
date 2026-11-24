package trade

import (
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

func TestEncryptedWalletCreateImportLoadAndTamperReject(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keystore")

	// 1. Create fresh encrypted wallet (mode 0600)
	sum, err := CreateEncryptedWallet(dir, "wallet-bsc-live", 56, "super-strong-passphrase-2026")
	if err != nil {
		t.Fatalf("CreateEncryptedWallet: %v", err)
	}
	info, err := os.Stat(sum.FilePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 permissions, got %v err=%v", info.Mode().Perm(), err)
	}

	// Wrong passphrase fails closed
	if _, _, err := LoadEncryptedWallet(sum.FilePath, "wrong-passphrase-123"); err == nil {
		t.Fatal("expected error with wrong passphrase")
	}
	// Correct passphrase succeeds and matches address
	priv, loadedSum, err := LoadEncryptedWallet(sum.FilePath, "super-strong-passphrase-2026")
	if err != nil {
		t.Fatalf("LoadEncryptedWallet: %v", err)
	}
	if crypto.PubkeyToAddress(priv.PublicKey) != sum.Address || loadedSum.ChainID != 56 {
		t.Fatalf("loaded wallet mismatch: addr=%s sum=%+v", crypto.PubkeyToAddress(priv.PublicKey).Hex(), loadedSum)
	}

	// 2. Import known private key
	knownKey, _ := crypto.GenerateKey()
	knownAddr := crypto.PubkeyToAddress(knownKey.PublicKey)
	knownHex := "0x" + crypto.PubkeyToAddress(knownKey.PublicKey).Hex()
	_ = knownHex
	privHex := string([]byte(crypto.FromECDSA(knownKey)))
	_ = privHex
	impSum, err := ImportEncryptedWallet(dir, "wallet-rbh-live", 4663, "0x"+string(encodeHexLower(crypto.FromECDSA(knownKey))), "another-strong-pass-2026")
	if err != nil {
		t.Fatalf("ImportEncryptedWallet: %v", err)
	}
	if impSum.Address != knownAddr {
		t.Fatalf("imported address mismatch: got %s want %s", impSum.Address.Hex(), knownAddr.Hex())
	}

	// 3. ListEncryptedWallets returns both without needing passphrases
	list, err := ListEncryptedWallets(dir)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListEncryptedWallets: len=%d err=%v", len(list), err)
	}

	// 4. AAD tamper test: modifying chain_id in JSON envelope MUST fail decryption
	raw, _ := os.ReadFile(sum.FilePath)
	var env EncryptedWalletFile
	_ = json.Unmarshal(raw, &env)
	env.ChainID = 4663 // tampered!
	tampered, _ := json.Marshal(env)
	_ = os.WriteFile(sum.FilePath, tampered, 0o600)
	if _, _, err := LoadEncryptedWallet(sum.FilePath, "super-strong-passphrase-2026"); err == nil {
		t.Fatal("expected AAD tamper rejection when chain_id is altered")
	}
}

func encodeHexLower(b []byte) []byte {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return out
}

func TestLiveExecutionPolicySafetyRails(t *testing.T) {
	// 1. live_enabled=false always refuses orders
	pOff := LiveExecutionPolicy{LiveEnabled: false, ChainID: 56}
	if err := pOff.ValidateOrder(ports.SideBuy, big.NewInt(1e15), 500); !errors.Is(err, ErrLiveExecutionDisabled) {
		t.Fatalf("expected ErrLiveExecutionDisabled, got %v", err)
	}

	// 2. BSC live mode without private_only relay URLs MUST fail closed
	pNoRelay := LiveExecutionPolicy{
		LiveEnabled:       true,
		ChainID:           56,
		MaxOrderNativeWei: big.NewInt(5e16),
		MaxSlippageBps:    1500,
		RelayMode:         RelayModePublicRPC,
	}
	if err := pNoRelay.ValidateConfig(); !errors.Is(err, ErrBSCPrivateRelayOnly) {
		t.Fatalf("expected ErrBSCPrivateRelayOnly, got %v", err)
	}

	// 3. Valid BSC private_only policy enforces max order & max slippage caps
	pOK := LiveExecutionPolicy{
		LiveEnabled:       true,
		ChainID:           56,
		MaxOrderNativeWei: big.NewInt(5e16), // 0.05 BNB cap
		MaxSlippageBps:    1500,             // 15% cap
		RelayMode:         RelayModePrivateOnly,
		PrivateRelayURLs:  []string{"https://rpc-bsc.48.club"},
	}
	if err := pOK.ValidateOrder(ports.SideBuy, big.NewInt(6e16), 500); !errors.Is(err, ErrMaxOrderCapExceeded) {
		t.Fatalf("expected ErrMaxOrderCapExceeded, got %v", err)
	}
	if err := pOK.ValidateOrder(ports.SideBuy, big.NewInt(1e16), 2000); !errors.Is(err, ErrMaxSlippageCapExceeded) {
		t.Fatalf("expected ErrMaxSlippageCapExceeded, got %v", err)
	}
	if err := pOK.ValidateOrder(ports.SideBuy, big.NewInt(1e16), 500); err != nil {
		t.Fatalf("expected valid order to pass, got %v", err)
	}
}
