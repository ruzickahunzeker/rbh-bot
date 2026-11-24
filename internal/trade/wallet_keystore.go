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
	"golang.org/x/crypto/scrypt"
)

const (
	keystoreVersion = 1
	scryptN         = 32768
	scryptR         = 8
	scryptP         = 1
	scryptKeyLen    = 32
)

// EncryptedWalletFile is an on-disk AES-256-GCM + scrypt encrypted wallet envelope (mode 0600).
// AAD binds wallet_id, chain_id, and checksummed address so ciphertexts cannot be transplanted.
type EncryptedWalletFile struct {
	Version       int    `json:"version"`
	WalletID      string `json:"wallet_id"`
	ChainID       uint64 `json:"chain_id"`
	Address       string `json:"address"`
	KDF           string `json:"kdf"`
	SaltHex       string `json:"salt_hex"`
	NonceHex      string `json:"nonce_hex"`
	CiphertextHex string `json:"ciphertext_hex"`
	CreatedAt     string `json:"created_at"`
}

// KeystoreSummary holds non-sensitive wallet metadata for listing without decrypting.
type KeystoreSummary struct {
	FilePath  string
	WalletID  string
	ChainID   uint64
	Address   common.Address
	CreatedAt string
}

func keystoreAAD(walletID string, chainID uint64, addr common.Address) []byte {
	return []byte(fmt.Sprintf("evm-bot:keystore:v%d|%s|%d|%s", keystoreVersion, walletID, chainID, strings.ToLower(addr.Hex())))
}

func validateWalletID(walletID string) error {
	if walletID == "" || strings.ContainsAny(walletID, `/\.. `) {
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

func writeEncryptedKeystore(dir, walletID string, chainID uint64, privBytes []byte, addr common.Address, passphrase string) (KeystoreSummary, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return KeystoreSummary{}, err
	}
	dest := filepath.Join(dir, walletID+".json")
	if _, err := os.Stat(dest); err == nil {
		return KeystoreSummary{}, fmt.Errorf("keystore %s already exists; refusing overwrite", dest)
	}

	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return KeystoreSummary{}, err
	}
	derivedKey, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return KeystoreSummary{}, err
	}
	defer clear(derivedKey)

	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return KeystoreSummary{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return KeystoreSummary{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return KeystoreSummary{}, err
	}
	aad := keystoreAAD(walletID, chainID, addr)
	ct := gcm.Seal(nil, nonce, privBytes, aad)

	nowStr := time.Now().UTC().Format(time.RFC3339)
	env := EncryptedWalletFile{
		Version:       keystoreVersion,
		WalletID:      walletID,
		ChainID:       chainID,
		Address:       addr.Hex(),
		KDF:           "scrypt-n32768-r8-p1",
		SaltHex:       hex.EncodeToString(salt),
		NonceHex:      hex.EncodeToString(nonce),
		CiphertextHex: hex.EncodeToString(ct),
		CreatedAt:     nowStr,
	}
	raw, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return KeystoreSummary{}, err
	}
	if err := os.WriteFile(dest, raw, 0o600); err != nil {
		return KeystoreSummary{}, err
	}
	return KeystoreSummary{
		FilePath:  dest,
		WalletID:  walletID,
		ChainID:   chainID,
		Address:   addr,
		CreatedAt: nowStr,
	}, nil
}

// LoadEncryptedWallet decrypts a 0600 keystore file, verifies AAD + derived address, and returns the ECDSA key.
func LoadEncryptedWallet(keystorePath, passphrase string) (*ecdsa.PrivateKey, KeystoreSummary, error) {
	info, err := os.Stat(keystorePath)
	if err != nil {
		return nil, KeystoreSummary{}, err
	}
	if info.Mode().Perm()&0o077 != 0 {
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
	if env.Version != keystoreVersion || !common.IsHexAddress(env.Address) {
		return nil, KeystoreSummary{}, errors.New("unsupported or malformed keystore envelope")
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
	aad := keystoreAAD(env.WalletID, env.ChainID, expectedAddr)
	plain, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, KeystoreSummary{}, errors.New("keystore decryption failed (wrong passphrase or tampered AAD)")
	}
	defer clear(plain)

	priv, err := crypto.ToECDSA(plain)
	if err != nil {
		return nil, KeystoreSummary{}, err
	}
	if crypto.PubkeyToAddress(priv.PublicKey) != expectedAddr {
		return nil, KeystoreSummary{}, errors.New("decrypted private key does not match envelope address")
	}
	return priv, KeystoreSummary{
		FilePath:  keystorePath,
		WalletID:  env.WalletID,
		ChainID:   env.ChainID,
		Address:   expectedAddr,
		CreatedAt: env.CreatedAt,
	}, nil
}

// ListEncryptedWallets scans dir for *.json encrypted wallet envelopes and returns non-sensitive summaries.
func ListEncryptedWallets(dir string) ([]KeystoreSummary, error) {
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
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var env EncryptedWalletFile
		if json.Unmarshal(raw, &env) != nil || env.WalletID == "" || !common.IsHexAddress(env.Address) {
			continue
		}
		out = append(out, KeystoreSummary{
			FilePath:  p,
			WalletID:  env.WalletID,
			ChainID:   env.ChainID,
			Address:   common.HexToAddress(env.Address),
			CreatedAt: env.CreatedAt,
		})
	}
	return out, nil
}
