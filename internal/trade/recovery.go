package trade

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

var ErrNotCanonical = errors.New("receipt is not canonical")

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
	kernel   *ExecutionKernel
	backend  ReceiptBackend
	resolver EffectResolver
	policy   CanonicalPolicy
	now      func() time.Time
}

func NewRecoveryService(store *Store, kernel *ExecutionKernel, b ReceiptBackend, r EffectResolver, p CanonicalPolicy) (*RecoveryService, error) {
	if store == nil || kernel == nil || b == nil || r == nil {
		return nil, ErrInvalidRequest
	}
	return &RecoveryService{store: store, kernel: kernel, backend: b, resolver: r, policy: p, now: time.Now}, nil
}

func (s *RecoveryService) Reconcile(ctx context.Context, operation string) error {
	stored, found, err := s.store.LoadEncryptedArtifact(ctx, operation)
	if err != nil || !found {
		return ErrArtifactIntegrity
	}
	raw, err := s.kernel.cipher.Decrypt(stored.KeyVersion, stored.Ciphertext, stored.EncryptionNonce, artifactAAD(stored.Operation, stored.StepID, stored.AttemptID))
	if err != nil {
		return err
	}
	defer clear(raw)
	if err = verifyRawArtifact(raw, stored.SignedArtifact); err != nil {
		return err
	}
	receipt, err := s.backend.TransactionReceipt(ctx, common.HexToHash(stored.TxHash))
	if err != nil || receipt == nil {
		return err
	}
	if receipt.BlockNumber == nil {
		return ErrNotCanonical
	}
	observation, err := s.store.ObserveReceipt(ctx, stored.SignedArtifact, ReceiptObservation{TxHash: stored.TxHash, BlockNumber: receipt.BlockNumber.Uint64(), BlockHash: receipt.BlockHash.Hex(), Status: receipt.Status}, s.now())
	if err != nil {
		return err
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
	return s.store.Canonicalize(ctx, stored.SignedArtifact, observation, effect, s.now())
}

func (s *RecoveryService) CheckReorg(ctx context.Context, operation string, observation ReceiptObservation) error {
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
	return s.store.Orphan(ctx, stored.SignedArtifact, observation, s.now())
}

func (b *RPCBackend) TransactionReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	return b.eth.TransactionReceipt(ctx, hash)
}
func (b *RPCBackend) HeaderByNumber(ctx context.Context, n *big.Int) (*types.Header, error) {
	return b.eth.HeaderByNumber(ctx, n)
}
