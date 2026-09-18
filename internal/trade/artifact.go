package trade

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	ErrArtifactIntegrity = errors.New("signed artifact integrity failure")
	ErrEncryption        = errors.New("signed artifact encryption failure")
	ErrUnknownKeyVersion = errors.New("unknown artifact key version")
	ErrWrongSigner       = errors.New("signed artifact sender mismatch")
)

type TransactionSigner interface {
	Address() common.Address
	SignTransaction(context.Context, *types.Transaction, *big.Int) (*types.Transaction, error)
}

type LocalSigner struct {
	key     *ecdsa.PrivateKey
	address common.Address
}

func NewLocalSigner(key *ecdsa.PrivateKey) (*LocalSigner, error) {
	if key == nil {
		return nil, ErrWrongSigner
	}
	return &LocalSigner{key: key, address: crypto.PubkeyToAddress(key.PublicKey)}, nil
}

func (s *LocalSigner) Address() common.Address { return s.address }

func (s *LocalSigner) SignTransaction(ctx context.Context, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	if s == nil || s.key == nil || tx == nil || chainID == nil || ctx == nil {
		return nil, ErrWrongSigner
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return types.SignTx(tx, types.LatestSignerForChainID(chainID), s.key)
}

type ArtifactCipher interface {
	KeyVersion() string
	Encrypt([]byte, []byte) (ciphertext, nonce []byte, err error)
	Decrypt(string, []byte, []byte, []byte) ([]byte, error)
}

type AESGCMCipher struct {
	version string
	aead    cipher.AEAD
}

func NewAESGCMCipher(version string, key []byte) (*AESGCMCipher, error) {
	if version == "" || len(key) != 32 {
		return nil, ErrEncryption
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEncryption, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEncryption, err)
	}
	return &AESGCMCipher{version: version, aead: aead}, nil
}

func (c *AESGCMCipher) KeyVersion() string { return c.version }

func (c *AESGCMCipher) Encrypt(plaintext, aad []byte) ([]byte, []byte, error) {
	if c == nil || c.aead == nil || len(plaintext) == 0 {
		return nil, nil, ErrEncryption
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrEncryption, err)
	}
	return c.aead.Seal(nil, nonce, plaintext, aad), nonce, nil
}

func (c *AESGCMCipher) Decrypt(version string, ciphertext, nonce, aad []byte) ([]byte, error) {
	if c == nil || c.aead == nil || version != c.version {
		return nil, ErrUnknownKeyVersion
	}
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrArtifactIntegrity, err)
	}
	return plaintext, nil
}

type SignedArtifact struct {
	AttemptID  string `json:"attempt_id"`
	Operation  string `json:"operation_id"`
	StepID     string `json:"step_id"`
	WalletID   string `json:"wallet_id"`
	KeyVersion string `json:"key_version"`
	Nonce      uint64 `json:"nonce"`
	TxHash     string `json:"tx_hash"`
	From       string `json:"from"`
	To         string `json:"to"`
	Value      string `json:"value"`
	Data       string `json:"data"`
	GasLimit   uint64 `json:"gas_limit"`
	GasTipCap  string `json:"gas_tip_cap"`
	GasFeeCap  string `json:"gas_fee_cap"`
	Duplicate  bool   `json:"duplicate"`
	Recovered  bool   `json:"recovered"`
}
