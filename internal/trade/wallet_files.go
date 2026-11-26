package trade

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ruzickahunzeker/rbh-bot/internal/walletmeta"
)

var (
	ErrKeystoreAuthFailed       = errors.New("keystore authentication failed: wrong passphrase or altered ciphertext/AAD")
	ErrKeystoreMalformed        = walletmeta.ErrKeystoreMalformed
	ErrWalletOwnerDenied        = errors.New("wallet owner is not authorized")
	ErrWalletDeleting           = walletmeta.ErrWalletDeleting
	ErrKeystoreRecoveryRequired = errors.New("explicit private-key recovery is required")
	ErrKeystoreCommitUncertain  = errors.New("keystore replacement committed but directory durability is unconfirmed; retained recovery backup")
)

func ValidWalletID(id string) bool { return walletmeta.ValidWalletID(id) }
func NewGlobalWalletID(prefix string) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

// All cooperating writers, metadata resolvers and private-key loaders share a process-safe lock.
func WithWalletDirectoryLock(dir string, fn func() error) error {
	return walletmeta.WithWalletDirectoryLock(dir, fn)
}
func syncWalletDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func walletDeleteMarker(dir, id string) string  { return filepath.Join(dir, ".deleted-"+id) }
func CheckWalletAvailable(dir, id string) error { return walletmeta.CheckWalletAvailable(dir, id) }

// These three functions require WithWalletDirectoryLock at the call site. The marker is
// durable before the DB deletion transaction commits, so a crash cannot reopen key loading.
func MarkWalletDeletingLocked(dir, id string) error {
	if !ValidWalletID(id) {
		return ErrKeystoreMalformed
	}
	f, err := os.OpenFile(walletDeleteMarker(dir, id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = f.WriteString("DELETING\n"); err != nil {
		f.Close()
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return syncWalletDirectory(dir)
}
func ClearWalletDeletingLocked(dir, id string) error {
	if !ValidWalletID(id) {
		return ErrKeystoreMalformed
	}
	if err := os.Remove(walletDeleteMarker(dir, id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncWalletDirectory(dir)
}
func RemoveWalletFilesLocked(dir, id string) error {
	if !ValidWalletID(id) {
		return ErrKeystoreMalformed
	}
	if err := os.Remove(filepath.Join(dir, id+".json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	backups, err := filepath.Glob(filepath.Join(dir, "."+id+".recovery-*.bak"))
	if err != nil {
		return err
	}
	for _, p := range backups {
		if err = os.Remove(p); err != nil {
			return err
		}
	}
	return syncWalletDirectory(dir)
}

// hooks are per-operation fault-injection points used by persistence regression tests.
type walletWriteHooks struct {
	beforeCommit func() error
	syncDir      func(string) error
}

func atomicWalletWrite(dest string, raw []byte, replace bool, hooks walletWriteHooks) error {
	dir := filepath.Dir(dest)
	syncDir := hooks.syncDir
	if syncDir == nil {
		syncDir = syncWalletDirectory
	}
	temp, err := os.CreateTemp(dir, ".wallet-write-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err = temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err = temp.Write(raw); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	var backup string
	if replace {
		f, err := os.CreateTemp(dir, "."+strings.TrimSuffix(filepath.Base(dest), ".json")+".recovery-*.bak")
		if err != nil {
			return err
		}
		backup = f.Name()
		f.Close()
		if err = os.Remove(backup); err != nil {
			return err
		}
		if err = os.Link(dest, backup); err != nil {
			return err
		}
		if err = syncWalletDirectory(dir); err != nil {
			os.Remove(backup)
			return err
		}
	}
	if hooks.beforeCommit != nil {
		if err = hooks.beforeCommit(); err != nil {
			if backup != "" {
				os.Remove(backup)
			}
			return err
		}
	}
	if replace {
		err = os.Rename(name, dest)
	} else {
		err = os.Link(name, dest)
	}
	if err != nil {
		if backup != "" {
			os.Remove(backup)
		}
		return err
	}
	if err = syncDir(dir); err != nil {
		if replace {
			return fmt.Errorf("%w: %s", ErrKeystoreCommitUncertain, backup)
		}
		return fmt.Errorf("wallet file created but directory sync failed: %w", err)
	}
	if backup != "" {
		if err = os.Remove(backup); err != nil {
			return fmt.Errorf("wallet saved; recovery backup cleanup failed: %w", err)
		}
		if err = syncWalletDirectory(dir); err != nil {
			return fmt.Errorf("wallet saved; recovery cleanup sync failed: %w", err)
		}
	}
	return nil
}

// A successful authenticated import can finish cleanup after an earlier post-rename
// durability error. Sync the selected current file's directory before discarding backups.
func cleanupWalletRecoveryBackupsLocked(dir, id string) error {
	if !ValidWalletID(id) {
		return ErrKeystoreMalformed
	}
	paths, err := filepath.Glob(filepath.Join(dir, "."+id+".recovery-*.bak"))
	if err != nil || len(paths) == 0 {
		return err
	}
	if err = syncWalletDirectory(dir); err != nil {
		return err
	}
	for _, p := range paths {
		if err = os.Remove(p); err != nil {
			return err
		}
	}
	return syncWalletDirectory(dir)
}
