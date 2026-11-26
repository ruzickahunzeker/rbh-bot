// Package walletmeta reads public wallet metadata and lifecycle markers. It has
// no private-key loader, signer, nonce allocator, broadcaster or database writer.
package walletmeta

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/sys/unix"
)

var ErrKeystoreMalformed = errors.New("malformed keystore")
var ErrWalletDeleting = errors.New("wallet is deleting or deleted")

type Envelope struct {
	Version       int    `json:"version"`
	WalletID      string `json:"wallet_id"`
	ChainID       uint64 `json:"chain_id,omitempty"`
	Address       string `json:"address"`
	OwnerChatID   int64  `json:"owner_chat_id,omitempty"`
	WalletType    string `json:"wallet_type,omitempty"`
	KDF           string `json:"kdf"`
	SaltHex       string `json:"salt_hex"`
	NonceHex      string `json:"nonce_hex"`
	CiphertextHex string `json:"ciphertext_hex"`
	CreatedAt     string `json:"created_at"`
}

// KeystoreSummary holds non-sensitive wallet metadata for listing without decrypting.
type Summary struct {
	FilePath    string
	Version     int
	WalletID    string
	ChainID     uint64 // 0 for chain-agnostic v2 wallets; legacy chain_id for v1
	Address     common.Address
	OwnerChatID int64
	WalletType  string
	CreatedAt   string
	Reencrypted bool // explicit recovery replaced the existing ciphertext
}

func ValidWalletID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func WithWalletDirectoryLock(dir string, fn func() error) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	fd, err := unix.Open(filepath.Join(dir, ".wallets.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err = unix.Flock(fd, unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	return fn()
}

func walletDeleteMarker(dir, id string) string { return filepath.Join(dir, ".deleted-"+id) }
func CheckWalletAvailable(dir, id string) error {
	if !ValidWalletID(id) {
		return ErrKeystoreMalformed
	}
	_, err := os.Lstat(walletDeleteMarker(dir, id))
	if err == nil {
		return ErrWalletDeleting
	}
	if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func ValidateEnvelope(env Envelope, filename string) error {
	if !ValidWalletID(env.WalletID) || env.WalletID+".json" != filename || !common.IsHexAddress(env.Address) || common.HexToAddress(env.Address) == (common.Address{}) || (env.Version != 1 && env.Version != 2) ||
		(env.Version == 1 && env.ChainID == 0) || (env.WalletType != "" && env.WalletType != "keystore") {
		return ErrKeystoreMalformed
	}
	salt, e1 := hex.DecodeString(env.SaltHex)
	nonce, e2 := hex.DecodeString(env.NonceHex)
	ct, e3 := hex.DecodeString(env.CiphertextHex)
	if e1 != nil || e2 != nil || e3 != nil || len(salt) != 32 || len(nonce) != 12 || len(ct) != 48 {
		return ErrKeystoreMalformed
	}
	return nil
}

func ReadWalletSummary(dir, walletID string) (Summary, error) {
	if !ValidWalletID(walletID) {
		return Summary{}, ErrKeystoreMalformed
	}
	if _, err := os.Stat(dir); err != nil {
		return Summary{}, err
	}
	var result Summary
	err := WithWalletDirectoryLock(dir, func() error {
		if err := CheckWalletAvailable(dir, walletID); err != nil {
			return err
		}
		p := filepath.Join(dir, walletID+".json")
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return ErrKeystoreMalformed
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var env Envelope
		if err = json.Unmarshal(raw, &env); err != nil {
			return err
		}
		if err = ValidateEnvelope(env, filepath.Base(p)); err != nil {
			return err
		}
		result = Summary{FilePath: p, Version: env.Version, WalletID: env.WalletID, ChainID: env.ChainID, Address: common.HexToAddress(env.Address), OwnerChatID: env.OwnerChatID, WalletType: "keystore", CreatedAt: env.CreatedAt}
		return nil
	})
	return result, err
}
