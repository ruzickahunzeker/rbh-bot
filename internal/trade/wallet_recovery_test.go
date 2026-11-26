package trade

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

func TestWalletRecoveryExplicitAuthorizationAndIdempotency(t *testing.T) {
	dir := t.TempDir()
	old, err := CreateGlobalEncryptedWallet(dir, "original", 1001, "old-passphrase-2026")
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := LoadEncryptedWallet(old.FilePath, "old-passphrase-2026")
	if err != nil {
		t.Fatal(err)
	}
	private := hex.EncodeToString(crypto.FromECDSA(key))
	before, _ := os.ReadFile(old.FilePath)
	if _, _, err = ImportGlobalEncryptedWallet(dir, "new-name", 1001, private, "new-passphrase-2026"); !errors.Is(err, ErrKeystoreRecoveryRequired) || !errors.Is(err, ErrKeystoreAuthFailed) {
		t.Fatalf("expected explicit recovery requirement, got %v", err)
	}
	after, _ := os.ReadFile(old.FilePath)
	if !bytes.Equal(before, after) {
		t.Fatal("ordinary import replaced ciphertext")
	}
	if _, _, err = ImportGlobalEncryptedWalletWithOptions(dir, "new-name", 2002, private, "new-passphrase-2026", WalletImportOptions{Recover: true}); !errors.Is(err, ErrWalletOwnerDenied) {
		t.Fatalf("foreign owner recovery=%v", err)
	}
	after, _ = os.ReadFile(old.FilePath)
	if !bytes.Equal(before, after) {
		t.Fatal("unauthorized recovery changed ciphertext")
	}
	recovered, duplicate, err := ImportGlobalEncryptedWalletWithOptions(dir, "new-name", 1001, private, "new-passphrase-2026", WalletImportOptions{Recover: true})
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate || !recovered.Reencrypted || recovered.WalletID != old.WalletID || recovered.OwnerChatID != 1001 || recovered.CreatedAt != old.CreatedAt {
		t.Fatalf("recovery changed identity: %+v", recovered)
	}
	if _, _, err = LoadEncryptedWallet(old.FilePath, "new-passphrase-2026"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "new-name.json")); !os.IsNotExist(err) {
		t.Fatal("recovery created a second identity")
	}
	current, _ := os.ReadFile(old.FilePath)
	duplicateSum, duplicate, err := ImportGlobalEncryptedWallet(dir, "ignored", 1001, private, "new-passphrase-2026")
	if err != nil || !duplicate || duplicateSum.Reencrypted {
		t.Fatalf("idempotent import=%+v %v", duplicateSum, err)
	}
	after, _ = os.ReadFile(old.FilePath)
	if !bytes.Equal(current, after) {
		t.Fatal("idempotent import rewrote ciphertext")
	}
}

func TestWalletRecoveryMalformedAndAmbiguousAuthFailure(t *testing.T) {
	dir := t.TempDir()
	w, err := CreateGlobalEncryptedWallet(dir, "existing", 1001, "initial-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := LoadEncryptedWallet(w.FilePath, "initial-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	private := hex.EncodeToString(crypto.FromECDSA(key))
	raw, _ := os.ReadFile(w.FilePath)
	if _, _, err = LoadEncryptedWallet(w.FilePath, "wrong-passphrase"); !errors.Is(err, ErrKeystoreAuthFailed) {
		t.Fatal("wrong password must report ambiguous auth failure")
	}
	var env EncryptedWalletFile
	json.Unmarshal(raw, &env)
	ciphertext, _ := hex.DecodeString(env.CiphertextHex)
	ciphertext[0] ^= 1
	env.CiphertextHex = hex.EncodeToString(ciphertext)
	changed, _ := json.Marshal(env)
	os.WriteFile(w.FilePath, changed, 0600)
	if _, _, err = LoadEncryptedWallet(w.FilePath, "initial-passphrase"); !errors.Is(err, ErrKeystoreAuthFailed) {
		t.Fatal("tampered ciphertext must use the same ambiguous auth error")
	}
	env.NonceHex = "00"
	broken, _ := json.Marshal(env)
	os.WriteFile(w.FilePath, broken, 0600)
	if _, _, err = ImportGlobalEncryptedWalletWithOptions(dir, "ignored", 1001, private, "new-passphrase", WalletImportOptions{Recover: true}); !errors.Is(err, ErrKeystoreMalformed) {
		t.Fatalf("malformed file was treated as password rotation: %v", err)
	}
	after, _ := os.ReadFile(w.FilePath)
	if !bytes.Equal(broken, after) {
		t.Fatal("malformed file was overwritten")
	}
	os.WriteFile(w.FilePath, raw, 0644)
	os.Chmod(w.FilePath, 0644)
	if _, _, err = ImportGlobalEncryptedWalletWithOptions(dir, "ignored", 1001, private, "new-passphrase", WalletImportOptions{Recover: true}); err == nil {
		t.Fatal("unsafe file permissions accepted")
	}
	after, _ = os.ReadFile(w.FilePath)
	if !bytes.Equal(raw, after) {
		t.Fatal("permission failure overwrote file")
	}
}

func TestWalletRecoveryPersistenceFailureStages(t *testing.T) {
	for _, stage := range []string{"before-rename", "after-rename"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			old, err := CreateGlobalEncryptedWallet(dir, "existing", 1001, "old-passphrase")
			if err != nil {
				t.Fatal(err)
			}
			key, _, err := LoadEncryptedWallet(old.FilePath, "old-passphrase")
			if err != nil {
				t.Fatal(err)
			}
			rawBefore, _ := os.ReadFile(old.FilePath)
			_, newRaw, err := encodeWalletEnvelope(2, old.WalletID, 0, 1001, crypto.FromECDSA(key), old.Address, "new-passphrase", old.CreatedAt)
			if err != nil {
				t.Fatal(err)
			}
			fault := errors.New("injected persistence failure")
			hooks := walletWriteHooks{}
			if stage == "before-rename" {
				hooks.beforeCommit = func() error { return fault }
			} else {
				hooks.syncDir = func(string) error { return fault }
			}
			err = WithWalletDirectoryLock(dir, func() error { return atomicWalletWrite(old.FilePath, newRaw, true, hooks) })
			if err == nil {
				t.Fatal("injected failure was ignored")
			}
			rawAfter, _ := os.ReadFile(old.FilePath)
			backups, _ := filepath.Glob(filepath.Join(dir, ".existing.recovery-*.bak"))
			if stage == "before-rename" {
				if !bytes.Equal(rawBefore, rawAfter) {
					t.Fatal("pre-commit failure altered the old file")
				}
				if _, _, e := LoadEncryptedWallet(old.FilePath, "old-passphrase"); e != nil {
					t.Fatal(e)
				}
			} else {
				if !errors.Is(err, ErrKeystoreCommitUncertain) || len(backups) != 1 {
					t.Fatalf("post-commit failure lost recovery backup: %v %v", err, backups)
				}
				backup, _ := os.ReadFile(backups[0])
				if !bytes.Equal(backup, rawBefore) {
					t.Fatal("recovery backup does not preserve original ciphertext")
				}
				if _, _, e := LoadEncryptedWallet(old.FilePath, "new-passphrase"); e != nil {
					t.Fatal(e)
				}
				// Authenticated retry selects the valid current file and completes durable cleanup.
				if _, _, e := ImportGlobalEncryptedWallet(dir, "unused", 1001, hex.EncodeToString(crypto.FromECDSA(key)), "new-passphrase"); e != nil {
					t.Fatal(e)
				}
				backups, _ = filepath.Glob(filepath.Join(dir, ".existing.recovery-*.bak"))
				if len(backups) != 0 {
					t.Fatal("authenticated recovery retry left stale backups")
				}
			}
			info, _ := os.Stat(old.FilePath)
			if info.Mode().Perm() != 0600 {
				t.Fatal("keystore permissions changed")
			}
		})
	}
}

func TestWalletRecoveryLegacyAndConcurrentImports(t *testing.T) {
	dir := t.TempDir()
	old, err := CreateEncryptedWallet(dir, "legacy", 56, "old-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := LoadEncryptedWallet(old.FilePath, "old-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	private := hex.EncodeToString(crypto.FromECDSA(key))
	if _, _, err = ImportGlobalEncryptedWalletWithOptions(dir, "ignored", 1001, private, "new-passphrase", WalletImportOptions{Recover: true}); !errors.Is(err, ErrWalletOwnerDenied) {
		t.Fatal("shared legacy wallet was implicitly granted")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, e := ImportGlobalEncryptedWalletWithOptions(dir, "ignored", 1001, private, "new-passphrase", WalletImportOptions{Recover: true, AllowShared: true})
			failures <- e
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	_, loaded, err := LoadEncryptedWallet(old.FilePath, "new-passphrase")
	if err != nil || loaded.Version != 2 || loaded.OwnerChatID != 0 || loaded.WalletID != old.WalletID {
		t.Fatalf("legacy identity changed: %+v %v", loaded, err)
	}
	wallets, err := ListEncryptedWallets(dir)
	if err != nil || len(wallets) != 1 {
		t.Fatalf("concurrent import duplicated wallet: %v %v", wallets, err)
	}
}
