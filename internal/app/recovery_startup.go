package app

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

// A1 supports one configured historical key version, not key rotation/keyring.
// The legacy interface includes Encrypt; this recovery capability rejects it.
type recoveryCipher struct{ cipher *trade.AESGCMCipher }

func (c recoveryCipher) KeyVersion() string { return c.cipher.KeyVersion() }
func (c recoveryCipher) Encrypt([]byte, []byte) ([]byte, []byte, error) {
	return nil, nil, errors.New("recovery encryption is disabled")
}
func (c recoveryCipher) Decrypt(version string, raw, nonce, aad []byte) ([]byte, error) {
	return c.cipher.Decrypt(version, raw, nonce, aad)
}

func loadRecoveryCipher(cfg config.Config) (trade.ArtifactCipher, error) {
	if cfg.RPCURL == "" || cfg.ArtifactKeyVersion == "" {
		return nil, errors.New("recovery configuration is incomplete")
	}
	key, err := hex.DecodeString(cfg.ArtifactEncryptionKey)
	if err != nil {
		return nil, errors.New("invalid recovery encryption configuration")
	}
	defer clear(key)
	cipher, err := trade.NewAESGCMCipher(cfg.ArtifactKeyVersion, key)
	if err != nil {
		return nil, errors.New("invalid recovery encryption configuration")
	}
	return recoveryCipher{cipher}, nil
}

type recoveryLifetime struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error // read only after done closes
}

func startRecoveryLifetime(ctx context.Context, recovery *trade.ProductionComposition) (*recoveryLifetime, error) {
	workerCtx, cancel := context.WithCancel(ctx)
	if err := recovery.Start(workerCtx); err != nil {
		cancel()
		// Start can acquire a lease/create a worker then fail its durable audit.
		// An already cancelled Run cannot begin a scan; it clears readiness and
		// terminates that worker lifecycle. A lease is never deleted/reset here;
		// its existing expiry/monotonic takeover rule remains authoritative.
		_ = recovery.Run(workerCtx)
		return nil, err
	}
	lifetime := &recoveryLifetime{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(lifetime.done)
		lifetime.err = recovery.Run(workerCtx)
	}()
	return lifetime, nil
}

func (r *recoveryLifetime) stopAndWait() {
	r.cancel()
	<-r.done
}
