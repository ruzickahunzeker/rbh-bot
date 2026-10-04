package trade

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

var (
	ErrNotCanonical   = errors.New("receipt is not canonical")
	ErrReceiptPending = errors.New("receipt is not available")
)

type ReceiptBackend interface {
	TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error)
	HeaderByNumber(context.Context, *big.Int) (*types.Header, error)
}
type EffectResolver interface {
	ResolvePositionEffect(context.Context, SignedArtifact, *types.Receipt) (PositionEffect, error)
}
type CanonicalPolicy struct{ Confirmations uint64 }

func (p CanonicalPolicy) Confirm(latest, block uint64, observed, canonical common.Hash) bool {
	return observed == canonical && latest >= block && latest-block >= p.Confirmations
}

type RecoveryService struct {
	store    *Store
	cipher   ArtifactCipher
	backend  ReceiptBackend
	resolver EffectResolver
	policy   CanonicalPolicy
	now      func() time.Time
}

func NewRecoveryService(store *Store, kernel *ExecutionKernel, b ReceiptBackend, r EffectResolver, p CanonicalPolicy) (*RecoveryService, error) {
	if kernel == nil {
		return nil, ErrInvalidRequest
	}
	return NewRecoveryServiceWithCipher(store, kernel.cipher, b, r, p)
}

// NewRecoveryServiceWithCipher is the production recovery constructor. It
// deliberately accepts only artifact decryption and read-only receipt/header
// capabilities; no signer or broadcaster is reachable from this graph.
func NewRecoveryServiceWithCipher(store *Store, artifactCipher ArtifactCipher, b ReceiptBackend, r EffectResolver, p CanonicalPolicy) (*RecoveryService, error) {
	if store == nil || artifactCipher == nil || b == nil || r == nil {
		return nil, ErrInvalidRequest
	}
	return &RecoveryService{store: store, cipher: artifactCipher, backend: b, resolver: r, policy: p, now: time.Now}, nil
}

func (s *RecoveryService) Reconcile(ctx context.Context, operation string) error {
	return s.reconcile(ctx, operation, nil)
}

// ReconcileWithLease preserves the existing recovery state machine while
// fencing every durable mutation in that mutation's SQLite transaction.
func (s *RecoveryService) ReconcileWithLease(ctx context.Context, operation string, lease func() RecoveryLeaseFence) error {
	return s.reconcile(ctx, operation, lease)
}

func (s *RecoveryService) reconcile(ctx context.Context, operation string, lease func() RecoveryLeaseFence) error {
	stored, found, err := s.store.LoadEncryptedArtifact(ctx, operation)
	if err != nil || !found {
		return ErrArtifactIntegrity
	}
	raw, err := s.cipher.Decrypt(stored.KeyVersion, stored.Ciphertext, stored.EncryptionNonce, artifactAAD(stored.Operation, stored.StepID, stored.AttemptID))
	if err != nil {
		return err
	}
	defer clear(raw)
	if err = verifyRawArtifact(raw, stored.SignedArtifact); err != nil {
		return err
	}
	receipt, err := s.backend.TransactionReceipt(ctx, common.HexToHash(stored.TxHash))
	if err != nil {
		return err
	}
	if receipt == nil {
		return ErrReceiptPending
	}
	if receipt.BlockNumber == nil {
		return ErrNotCanonical
	}
	var leaseValue *RecoveryLeaseFence
	if lease != nil {
		v := lease()
		leaseValue = &v
	}
	observation, err := s.store.ObserveReceiptFenced(ctx, stored.SignedArtifact, ReceiptObservation{TxHash: stored.TxHash, BlockNumber: receipt.BlockNumber.Uint64(), BlockHash: receipt.BlockHash.Hex(), Status: receipt.Status}, s.now(), leaseValue)
	if err != nil {
		return err
	}
	if s.store.recoveryHook != nil {
		s.store.recoveryHook("after_receipt_observation")
	}
	canonical, err := s.backend.HeaderByNumber(ctx, receipt.BlockNumber)
	if err != nil {
		return err
	}
	latest, err := s.backend.HeaderByNumber(ctx, nil)
	if err != nil {
		return err
	}
	if !s.policy.Confirm(latest.Number.Uint64(), receipt.BlockNumber.Uint64(), receipt.BlockHash, canonical.Hash()) {
		return ErrNotCanonical
	}
	var effect *PositionEffect
	if receipt.Status == types.ReceiptStatusSuccessful {
		resolved, e := s.resolver.ResolvePositionEffect(ctx, stored.SignedArtifact, receipt)
		if e != nil {
			return e
		}
		effect = &resolved
	}
	if lease != nil {
		v := lease()
		leaseValue = &v
	}
	return s.store.CanonicalizeFenced(ctx, stored.SignedArtifact, observation, effect, s.now(), leaseValue)
}

func (s *RecoveryService) CheckReorg(ctx context.Context, operation string, observation ReceiptObservation) error {
	return s.checkReorg(ctx, operation, observation, nil)
}

func (s *RecoveryService) CheckReorgWithLease(ctx context.Context, operation string, observation ReceiptObservation, lease func() RecoveryLeaseFence) error {
	return s.checkReorg(ctx, operation, observation, lease)
}

func (s *RecoveryService) checkReorg(ctx context.Context, operation string, observation ReceiptObservation, lease func() RecoveryLeaseFence) error {
	stored, found, err := s.store.LoadEncryptedArtifact(ctx, operation)
	if err != nil || !found {
		return ErrArtifactIntegrity
	}
	header, err := s.backend.HeaderByNumber(ctx, new(big.Int).SetUint64(observation.BlockNumber))
	if err != nil {
		return err
	}
	if header.Hash().Hex() == observation.BlockHash {
		return nil
	}
	var leaseValue *RecoveryLeaseFence
	if lease != nil {
		v := lease()
		leaseValue = &v
	}
	return s.store.OrphanFenced(ctx, stored.SignedArtifact, observation, s.now(), leaseValue)
}

func (b *RPCBackend) TransactionReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	return b.eth.TransactionReceipt(ctx, hash)
}
func (b *RPCBackend) HeaderByNumber(ctx context.Context, n *big.Int) (*types.Header, error) {
	return b.eth.HeaderByNumber(ctx, n)
}
