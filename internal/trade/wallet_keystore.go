package trade

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/walletmeta"
	"golang.org/x/crypto/scrypt"
)

const (
	keystoreVersion   = 1
	keystoreVersionV2 = 2
	scryptN           = 32768
	scryptR           = 8
	scryptP           = 1
	scryptKeyLen      = 32
)

// EncryptedWalletFile is an on-disk AES-256-GCM + scrypt encrypted wallet envelope (mode 0600).
// Version 1 AAD binds wallet_id, chain_id, and address (legacy); Version 2 AAD binds wallet_id and
// address only so the same EVM wallet is chain-agnostic across all EVM chains.
type EncryptedWalletFile = walletmeta.Envelope
type KeystoreSummary = walletmeta.Summary

func keystoreAAD(walletID string, chainID uint64, addr common.Address) []byte {
	return []byte(fmt.Sprintf("evm-bot:keystore:v%d|%s|%d|%s", keystoreVersion, walletID, chainID, strings.ToLower(addr.Hex())))
}

func keystoreAADv2(walletID string, addr common.Address) []byte {
	return []byte(fmt.Sprintf("evm-bot:keystore:v%d|%s|%s", keystoreVersionV2, walletID, strings.ToLower(addr.Hex())))
}

func validateWalletID(walletID string) error {
	if !ValidWalletID(walletID) {
		return errors.New("invalid wallet_id (must be non-empty safe filename token)")
	}
	return nil
}

// CreateEncryptedWallet generates a fresh secp256k1 private key, encrypts it with scrypt + AES-256-GCM,
// writes it to <dir>/<walletID>.json with mode 0600, and zeros plaintext bytes immediately.
func CreateEncryptedWallet(dir, walletID string, chainID uint64, passphrase string) (KeystoreSummary, error) {
	if err := validateWalletID(walletID); err != nil {
		return KeystoreSummary{}, err
	}
	if chainID == 0 || len(passphrase) < 8 {
		return KeystoreSummary{}, errors.New("chain_id > 0 and passphrase (>= 8 chars) are required")
	}
	priv, err := crypto.GenerateKey()
	if err != nil {
		return KeystoreSummary{}, err
	}
	privBytes := crypto.FromECDSA(priv)
	defer clear(privBytes)
	return writeEncryptedKeystore(dir, walletID, chainID, privBytes, crypto.PubkeyToAddress(priv.PublicKey), passphrase)
}

// ImportEncryptedWallet validates a hex private key, encrypts it with scrypt + AES-256-GCM,
// and writes it to <dir>/<walletID>.json with mode 0600.
func ImportEncryptedWallet(dir, walletID string, chainID uint64, privKeyHex, passphrase string) (KeystoreSummary, error) {
	if err := validateWalletID(walletID); err != nil {
		return KeystoreSummary{}, err
	}
	if chainID == 0 || len(passphrase) < 8 {
		return KeystoreSummary{}, errors.New("chain_id > 0 and passphrase (>= 8 chars) are required")
	}
	cleanHex := strings.TrimPrefix(strings.TrimSpace(privKeyHex), "0x")
	priv, err := crypto.HexToECDSA(cleanHex)
	if err != nil {
		return KeystoreSummary{}, fmt.Errorf("invalid secp256k1 private key: %w", err)
	}
	privBytes := crypto.FromECDSA(priv)
	defer clear(privBytes)
	return writeEncryptedKeystore(dir, walletID, chainID, privBytes, crypto.PubkeyToAddress(priv.PublicKey), passphrase)
}

// CreateGlobalEncryptedWallet generates a fresh secp256k1 private key and stores it as a
// chain-agnostic Keystore v2 envelope (mode 0600, AAD bound to wallet_id + address).
func CreateGlobalEncryptedWallet(dir, walletID string, ownerChatID int64, passphrase string) (KeystoreSummary, error) {
	if err := validateWalletID(walletID); err != nil {
		return KeystoreSummary{}, err
	}
	if len(passphrase) < 8 {
		return KeystoreSummary{}, errors.New("passphrase (>= 8 chars) is required")
	}
	priv, err := crypto.GenerateKey()
	if err != nil {
		return KeystoreSummary{}, err
	}
	privBytes := crypto.FromECDSA(priv)
	defer clear(privBytes)
	return writeEncryptedKeystoreEnvelope(dir, keystoreVersionV2, walletID, 0, ownerChatID, privBytes, crypto.PubkeyToAddress(priv.PublicKey), passphrase)
}

// ImportGlobalEncryptedWallet is idempotent only when the existing wallet decrypts
// with the current passphrase. Authentication failure does not silently replace ciphertext.
func ImportGlobalEncryptedWallet(dir, walletID string, ownerChatID int64, privKeyHex, passphrase string) (KeystoreSummary, bool, error) {
	return ImportGlobalEncryptedWalletWithOptions(dir, walletID, ownerChatID, privKeyHex, passphrase, WalletImportOptions{})
}

type WalletImportOptions struct {
	Recover     bool // explicitly requested recovery using a freshly supplied private key
	AllowShared bool // trusted server grant, never parsed from Telegram callback input
}

func ImportGlobalEncryptedWalletWithOptions(dir, walletID string, ownerChatID int64, privKeyHex, passphrase string, options WalletImportOptions) (KeystoreSummary, bool, error) {
	if err := validateWalletID(walletID); err != nil {
		return KeystoreSummary{}, false, err
	}
	if len(passphrase) < 8 {
		return KeystoreSummary{}, false, errors.New("passphrase (>= 8 chars) is required")
	}
	priv, err := crypto.HexToECDSA(strings.TrimPrefix(strings.TrimSpace(privKeyHex), "0x"))
	if err != nil {
		return KeystoreSummary{}, false, errors.New("invalid secp256k1 private key")
	}
	privBytes := crypto.FromECDSA(priv)
	defer clear(privBytes)
	addr := crypto.PubkeyToAddress(priv.PublicKey)
	var result KeystoreSummary
	duplicate := false
	err = WithWalletDirectoryLock(dir, func() error {
		existing, err := listEncryptedWalletsUnlocked(dir)
		if err != nil {
			return err
		}
		for _, w := range existing {
			if w.Address != addr {
				continue
			}
			// Ownership is checked BEFORE any recovery or write, including shared wallets.
			if (w.OwnerChatID == 0 && !options.AllowShared) || (w.OwnerChatID != 0 && w.OwnerChatID != ownerChatID) {
				return ErrWalletOwnerDenied
			}
			if err = CheckWalletAvailable(dir, w.WalletID); err != nil {
				return err
			}
			_, loaded, loadErr := loadEncryptedWalletUnlocked(w.FilePath, passphrase)
			if loadErr == nil {
				if err := cleanupWalletRecoveryBackupsLocked(dir, w.WalletID); err != nil {
					return err
				}
				result = loaded
				duplicate = true
				return nil
			}
			if !errors.Is(loadErr, ErrKeystoreAuthFailed) {
				return loadErr
			}
			if !options.Recover {
				return fmt.Errorf("%w: %w", ErrKeystoreRecoveryRequired, loadErr)
			}
			// Auth failure is ambiguous (wrong passphrase OR tampering). Explicit recovery is
			// authorized by ownership plus the supplied key matching the existing public address.
			replacement, raw, err := encodeWalletEnvelope(keystoreVersionV2, w.WalletID, 0, w.OwnerChatID, privBytes, addr, passphrase, w.CreatedAt)
			if err != nil {
				return err
			}
			if err = atomicWalletWrite(w.FilePath, raw, true, walletWriteHooks{}); err != nil {
				return err
			}
			if err := cleanupWalletRecoveryBackupsLocked(dir, w.WalletID); err != nil {
				return err
			}
			replacement.FilePath = w.FilePath
			replacement.Reencrypted = true
			result = replacement
			duplicate = true
			return nil
		}
		if options.Recover {
			return errors.New("recovery target not found; import as a new wallet instead")
		}
		result, err = writeEncryptedKeystoreEnvelopeUnlocked(dir, keystoreVersionV2, walletID, 0, ownerChatID, privBytes, addr, passphrase)
		return err
	})
	return result, duplicate, err
}

func writeEncryptedKeystore(dir, walletID string, chainID uint64, privBytes []byte, addr common.Address, passphrase string) (KeystoreSummary, error) {
	return writeEncryptedKeystoreEnvelope(dir, keystoreVersion, walletID, chainID, 0, privBytes, addr, passphrase)
}

func writeEncryptedKeystoreEnvelope(dir string, version int, walletID string, chainID uint64, ownerChatID int64, privBytes []byte, addr common.Address, passphrase string) (KeystoreSummary, error) {
	var result KeystoreSummary
	err := WithWalletDirectoryLock(dir, func() error {
		var err error
		result, err = writeEncryptedKeystoreEnvelopeUnlocked(dir, version, walletID, chainID, ownerChatID, privBytes, addr, passphrase)
		return err
	})
	return result, err
}
func writeEncryptedKeystoreEnvelopeUnlocked(dir string, version int, walletID string, chainID uint64, ownerChatID int64, privBytes []byte, addr common.Address, passphrase string) (KeystoreSummary, error) {
	if err := validateWalletID(walletID); err != nil {
		return KeystoreSummary{}, err
	}
	if err := CheckWalletAvailable(dir, walletID); err != nil {
		return KeystoreSummary{}, err
	}
	dest := filepath.Join(dir, walletID+".json")
	if _, err := os.Lstat(dest); err == nil {
		return KeystoreSummary{}, errors.New("wallet ID already exists")
	} else if !os.IsNotExist(err) {
		return KeystoreSummary{}, err
	}
	result, raw, err := encodeWalletEnvelope(version, walletID, chainID, ownerChatID, privBytes, addr, passphrase, "")
	if err != nil {
		return KeystoreSummary{}, err
	}
	if err = atomicWalletWrite(dest, raw, false, walletWriteHooks{}); err != nil {
		return KeystoreSummary{}, err
	}
	result.FilePath = dest
	return result, nil
}
func encodeWalletEnvelope(version int, walletID string, chainID uint64, ownerChatID int64, privBytes []byte, addr common.Address, passphrase, createdAt string) (KeystoreSummary, []byte, error) {
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return KeystoreSummary{}, nil, err
	}
	derivedKey, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return KeystoreSummary{}, nil, err
	}
	defer clear(derivedKey)

	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return KeystoreSummary{}, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return KeystoreSummary{}, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return KeystoreSummary{}, nil, err
	}
	var aad []byte
	if version == keystoreVersionV2 {
		aad = keystoreAADv2(walletID, addr)
	} else {
		aad = keystoreAAD(walletID, chainID, addr)
	}
	ct := gcm.Seal(nil, nonce, privBytes, aad)

	nowStr := createdAt
	if nowStr == "" {
		nowStr = time.Now().UTC().Format(time.RFC3339)
	}
	env := EncryptedWalletFile{
		Version:       version,
		WalletID:      walletID,
		ChainID:       chainID,
		Address:       addr.Hex(),
		OwnerChatID:   ownerChatID,
		WalletType:    "keystore",
		KDF:           "scrypt-n32768-r8-p1",
		SaltHex:       hex.EncodeToString(salt),
		NonceHex:      hex.EncodeToString(nonce),
		CiphertextHex: hex.EncodeToString(ct),
		CreatedAt:     nowStr,
	}
	raw, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return KeystoreSummary{}, nil, err
	}
	return KeystoreSummary{

		Version:     version,
		WalletID:    walletID,
		ChainID:     chainID,
		Address:     addr,
		OwnerChatID: ownerChatID,
		WalletType:  "keystore",
		CreatedAt:   nowStr,
	}, raw, nil
}

// LoadEncryptedWallet decrypts a 0600 keystore file (v1 legacy chain-bound or v2 chain-agnostic),
// verifies AAD + derived address, and returns the ECDSA key.
func LoadEncryptedWallet(keystorePath, passphrase string) (*ecdsa.PrivateKey, KeystoreSummary, error) {
	var key *ecdsa.PrivateKey
	var summary KeystoreSummary
	err := WithWalletDirectoryLock(filepath.Dir(keystorePath), func() error {
		var err error
		key, summary, err = loadEncryptedWalletUnlocked(keystorePath, passphrase)
		return err
	})
	return key, summary, err
}
func loadEncryptedWalletUnlocked(keystorePath, passphrase string) (*ecdsa.PrivateKey, KeystoreSummary, error) {
	if err := CheckWalletAvailable(filepath.Dir(keystorePath), strings.TrimSuffix(filepath.Base(keystorePath), ".json")); err != nil {
		return nil, KeystoreSummary{}, err
	}
	info, err := os.Lstat(keystorePath)
	if err != nil {
		return nil, KeystoreSummary{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, KeystoreSummary{}, fmt.Errorf("keystore %s permissions %04o are too open (require 0600)", keystorePath, info.Mode().Perm())
	}
	raw, err := os.ReadFile(keystorePath)
	if err != nil {
		return nil, KeystoreSummary{}, err
	}
	var env EncryptedWalletFile
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, KeystoreSummary{}, err
	}
	if err := validateWalletEnvelope(env, filepath.Base(keystorePath)); err != nil {
		return nil, KeystoreSummary{}, err
	}
	salt, err := hex.DecodeString(env.SaltHex)
	if err != nil {
		return nil, KeystoreSummary{}, err
	}
	nonce, err := hex.DecodeString(env.NonceHex)
	if err != nil {
		return nil, KeystoreSummary{}, err
	}
	ct, err := hex.DecodeString(env.CiphertextHex)
	if err != nil {
		return nil, KeystoreSummary{}, err
	}
	derivedKey, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return nil, KeystoreSummary{}, err
	}
	defer clear(derivedKey)

	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return nil, KeystoreSummary{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, KeystoreSummary{}, err
	}
	expectedAddr := common.HexToAddress(env.Address)
	var aad []byte
	if env.Version == keystoreVersionV2 {
		aad = keystoreAADv2(env.WalletID, expectedAddr)
	} else {
		aad = keystoreAAD(env.WalletID, env.ChainID, expectedAddr)
	}
	plain, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, KeystoreSummary{}, ErrKeystoreAuthFailed
	}
	defer clear(plain)

	priv, err := crypto.ToECDSA(plain)
	if err != nil {
		return nil, KeystoreSummary{}, err
	}
	if crypto.PubkeyToAddress(priv.PublicKey) != expectedAddr {
		return nil, KeystoreSummary{}, errors.New("decrypted private key does not match envelope address")
	}
	wType := env.WalletType
	if wType == "" {
		wType = "keystore"
	}
	return priv, KeystoreSummary{
		FilePath:    keystorePath,
		Version:     env.Version,
		WalletID:    env.WalletID,
		ChainID:     env.ChainID,
		Address:     expectedAddr,
		OwnerChatID: env.OwnerChatID,
		WalletType:  wType,
		CreatedAt:   env.CreatedAt,
	}, nil
}

// ListEncryptedWallets scans dir for *.json encrypted wallet envelopes (v1 and v2) and returns non-sensitive summaries.
func ListEncryptedWallets(dir string) ([]KeystoreSummary, error) {
	return listEncryptedWalletsUnlocked(dir)
}
func listEncryptedWalletsUnlocked(dir string) ([]KeystoreSummary, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []KeystoreSummary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		info, err := os.Lstat(p)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, ErrKeystoreMalformed
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var env EncryptedWalletFile
		if json.Unmarshal(raw, &env) != nil || validateWalletEnvelope(env, e.Name()) != nil {
			return nil, ErrKeystoreMalformed
		}
		if err := CheckWalletAvailable(dir, env.WalletID); errors.Is(err, ErrWalletDeleting) {
			continue
		} else if err != nil {
			return nil, err
		}
		if env.Version != keystoreVersion && env.Version != keystoreVersionV2 {
			continue
		}
		wType := env.WalletType
		if wType == "" {
			wType = "keystore"
		}
		out = append(out, KeystoreSummary{
			FilePath:    p,
			Version:     env.Version,
			WalletID:    env.WalletID,
			ChainID:     env.ChainID,
			Address:     common.HexToAddress(env.Address),
			OwnerChatID: env.OwnerChatID,
			WalletType:  wType,
			CreatedAt:   env.CreatedAt,
		})
	}
	return out, nil
}

func validateWalletEnvelope(env EncryptedWalletFile, filename string) error {
	return walletmeta.ValidateEnvelope(env, filename)
}
func ReadWalletSummary(dir, walletID string) (KeystoreSummary, error) {
	return walletmeta.ReadWalletSummary(dir, walletID)
}
