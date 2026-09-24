package trade

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type fakeBroadcaster struct {
	hash  string
	err   error
	calls int
	raws  [][]byte
}

func (f *fakeBroadcaster) SendRawTransaction(_ context.Context, raw []byte) (string, error) {
	f.calls++
	f.raws = append(f.raws, append([]byte(nil), raw...))
	return f.hash, f.err
}

func seededSignedArtifact(t *testing.T) (*Store, *ExecutionKernel, SignedArtifact, func()) {
	t.Helper()
	store, closeDB := openTradeStore(t)
	backend := newFakeBackend()
	signer, cipher := executionSecrets(t)
	if err := store.RegisterDryRunWallet(context.Background(), "wallet-1", signer.Address()); err != nil {
		t.Fatal(err)
	}
	engine, _ := NewEngine(store, backend)
	if _, err := engine.DryRun(context.Background(), buyRequest()); err != nil {
		t.Fatal(err)
	}
	kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
	a, err := kernel.Prepare(context.Background(), "intent-buy", "wallet-1")
	if err != nil {
		t.Fatal(err)
	}
	return store, kernel, a, closeDB
}

func TestSubmissionExactBytesAndDuplicateIdempotent(t *testing.T) {
	store, kernel, a, closeDB := seededSignedArtifact(t)
	defer closeDB()
	stored, _, _ := store.LoadEncryptedArtifact(context.Background(), a.Operation)
	raw, _ := kernel.cipher.Decrypt(stored.KeyVersion, stored.Ciphertext, stored.EncryptionNonce, artifactAAD(stored.Operation, stored.StepID, stored.AttemptID))
	b := &fakeBroadcaster{hash: a.TxHash}
	svc, _ := NewSubmissionService(store, kernel, b)
	first, err := svc.Submit(context.Background(), a.Operation)
	if err != nil || first.State != "submitted" {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := svc.Submit(context.Background(), a.Operation)
	if err != nil || second.ID != first.ID || b.calls != 1 {
		t.Fatalf("duplicate=%#v calls=%d err=%v", second, b.calls, err)
	}
	if !bytes.Equal(raw, b.raws[0]) {
		t.Fatal("broadcast bytes differ from durable artifact")
	}
	assertAttemptCount(t, store, 1)
}

func TestSubmissionAmbiguousFreezesAndRetains(t *testing.T) {
	store, kernel, a, closeDB := seededSignedArtifact(t)
	defer closeDB()
	b := &fakeBroadcaster{err: ErrBroadcastAmbiguous}
	svc, _ := NewSubmissionService(store, kernel, b)
	if _, err := svc.Submit(context.Background(), a.Operation); !errors.Is(err, ErrBroadcastAmbiguous) {
		t.Fatal(err)
	}
	var lane, res string
	_ = store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id='wallet-1'`).Scan(&lane)
	_ = store.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id=?`, a.Operation).Scan(&res)
	if lane != "frozen" || res != "frozen" {
		t.Fatalf("lane=%s reservation=%s", lane, res)
	}
	if _, found, err := store.LoadEncryptedArtifact(context.Background(), a.Operation); err != nil || !found {
		t.Fatalf("artifact vanished found=%v err=%v", found, err)
	}
	assertAttemptCount(t, store, 1)
}

func TestSubmissionUnknownReplayUsesExactSameBytes(t *testing.T) {
	store, kernel, a, closeDB := seededSignedArtifact(t)
	defer closeDB()
	stored, _, _ := store.LoadEncryptedArtifact(context.Background(), a.Operation)
	raw, _ := kernel.cipher.Decrypt(stored.KeyVersion, stored.Ciphertext, stored.EncryptionNonce, artifactAAD(stored.Operation, stored.StepID, stored.AttemptID))
	b := &fakeBroadcaster{err: ErrBroadcastAmbiguous}
	svc, _ := NewSubmissionService(store, kernel, b)
	_, _ = svc.Submit(context.Background(), a.Operation)
	b.err = nil
	b.hash = a.TxHash
	result, err := svc.ReplayUnknown(context.Background(), a.Operation, true)
	if err != nil || result.State != "submitted" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if b.calls != 2 || !bytes.Equal(b.raws[0], raw) || !bytes.Equal(b.raws[1], raw) {
		t.Fatal("replay did not use exact durable bytes")
	}
	assertAttemptCount(t, store, 1)
}

func TestSubmissionHashMismatchFailsClosed(t *testing.T) {
	store, kernel, a, closeDB := seededSignedArtifact(t)
	defer closeDB()
	svc, _ := NewSubmissionService(store, kernel, &fakeBroadcaster{hash: "0xdead"})
	if _, err := svc.Submit(context.Background(), a.Operation); !errors.Is(err, ErrRPCHashMismatch) {
		t.Fatal(err)
	}
	var lane string
	_ = store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id='wallet-1'`).Scan(&lane)
	if lane != "frozen" {
		t.Fatalf("lane=%s", lane)
	}
}

func TestRestartWithInflightSubmissionDoesNotSend(t *testing.T) {
	store, kernel, a, closeDB := seededSignedArtifact(t)
	defer closeDB()
	if _, err := store.BeginSubmission(context.Background(), a, false, fixedTime()); err != nil {
		t.Fatal(err)
	}
	b := &fakeBroadcaster{hash: a.TxHash}
	svc, _ := NewSubmissionService(store, kernel, b)
	if _, err := svc.Submit(context.Background(), a.Operation); !errors.Is(err, ErrBroadcastAmbiguous) {
		t.Fatalf("err=%v", err)
	}
	if b.calls != 0 {
		t.Fatalf("broadcast calls=%d", b.calls)
	}
	var lane, res string
	_ = store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id=?`, a.WalletID).Scan(&lane)
	_ = store.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id=?`, a.Operation).Scan(&res)
	if lane != "frozen" || res != "frozen" {
		t.Fatalf("lane=%s reservation=%s", lane, res)
	}
}

func TestCanonicalApplyOrphanRecanonicalExactlyOneActiveEffect(t *testing.T) {
	store, _, a, closeDB := seededSignedArtifact(t)
	defer closeDB()
	r, err := store.ObserveReceipt(context.Background(), a, ReceiptObservation{TxHash: a.TxHash, BlockNumber: 10, BlockHash: "0xabc", Status: 1}, fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	effect := &PositionEffect{Asset: testToken.Hex(), Delta: "100"}
	if err = store.Canonicalize(context.Background(), a, r, effect, fixedTime()); err != nil {
		t.Fatal(err)
	}
	if err = store.Orphan(context.Background(), a, r, fixedTime()); err != nil {
		t.Fatal(err)
	}
	if err = store.Canonicalize(context.Background(), a, r, effect, fixedTime()); err != nil {
		t.Fatal(err)
	}
	var active, attempts int
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM position_effects WHERE attempt_id=? AND state='active'`, a.AttemptID).Scan(&active)
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM transaction_attempts WHERE step_id=?`, a.StepID).Scan(&attempts)
	if active != 1 || attempts != 1 {
		t.Fatalf("active=%d attempts=%d", active, attempts)
	}
	for _, transition := range []string{"apply", "rollback", "reapply"} {
		var count int
		_ = store.db.QueryRow(`SELECT COUNT(*) FROM position_effect_history WHERE effect_id=? AND transition=?`, effect.ID, transition).Scan(&count)
		if count != 1 {
			t.Fatalf("transition %s count=%d", transition, count)
		}
	}
}

func TestCanonicalRevertCreatesNoSuccessEffect(t *testing.T) {
	store, _, a, closeDB := seededSignedArtifact(t)
	defer closeDB()
	r, err := store.ObserveReceipt(context.Background(), a, ReceiptObservation{TxHash: a.TxHash, BlockNumber: 11, BlockHash: "0xdef", Status: 0}, fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Canonicalize(context.Background(), a, r, nil, fixedTime()); err != nil {
		t.Fatal(err)
	}
	var effects int
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM position_effects WHERE attempt_id=?`, a.AttemptID).Scan(&effects)
	if effects != 0 {
		t.Fatalf("effects=%d", effects)
	}
	var lane, res string
	_ = store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id=?`, a.WalletID).Scan(&lane)
	_ = store.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id=?`, a.Operation).Scan(&res)
	if lane != "idle" || res != "reverted_released" {
		t.Fatalf("lane=%s reservation=%s", lane, res)
	}
}

type fakeReceiptBackend struct {
	receipt           *types.Receipt
	canonical, latest *types.Header
}

func (b *fakeReceiptBackend) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	return b.receipt, nil
}
func (b *fakeReceiptBackend) HeaderByNumber(_ context.Context, number *big.Int) (*types.Header, error) {
	if number == nil {
		return b.latest, nil
	}
	return b.canonical, nil
}

type fakeEffectResolver struct{}

func (fakeEffectResolver) ResolvePositionEffect(context.Context, SignedArtifact, *types.Receipt) (PositionEffect, error) {
	return PositionEffect{Asset: testToken.Hex(), Delta: "100"}, nil
}

func TestRecoveryRequiresCanonicalPolicyBeforePosition(t *testing.T) {
	store, kernel, artifact, closeDB := seededSignedArtifact(t)
	defer closeDB()
	header := &types.Header{Number: big.NewInt(100), Extra: []byte("canonical")}
	backend := &fakeReceiptBackend{receipt: &types.Receipt{TxHash: common.HexToHash(artifact.TxHash), BlockNumber: big.NewInt(100), BlockHash: header.Hash(), Status: 1}, canonical: header, latest: &types.Header{Number: big.NewInt(101)}}
	recovery, _ := NewRecoveryService(store, kernel, backend, fakeEffectResolver{}, CanonicalPolicy{Confirmations: 2})
	if err := recovery.Reconcile(context.Background(), artifact.Operation); !errors.Is(err, ErrNotCanonical) {
		t.Fatalf("premature canonical err=%v", err)
	}
	var effects int
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM position_effects`).Scan(&effects)
	if effects != 0 {
		t.Fatalf("premature effects=%d", effects)
	}
	backend.latest = &types.Header{Number: big.NewInt(102)}
	if err := recovery.Reconcile(context.Background(), artifact.Operation); err != nil {
		t.Fatal(err)
	}
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM position_effects WHERE state='active'`).Scan(&effects)
	if effects != 1 {
		t.Fatalf("effects=%d", effects)
	}
}

func TestPreFinalityOrphanRemainsUnresolved(t *testing.T) {
	store, _, a, closeDB := seededSignedArtifact(t)
	defer closeDB()
	r, err := store.ObserveReceipt(context.Background(), a, ReceiptObservation{TxHash: a.TxHash, BlockNumber: 12, BlockHash: "0xfeed", Status: 1}, fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Orphan(context.Background(), a, r, fixedTime()); err != nil {
		t.Fatal(err)
	}
	var receiptState, stepState, laneState string
	_ = store.db.QueryRow(`SELECT canonical_state FROM receipt_observations WHERE id=?`, r.ID).Scan(&receiptState)
	_ = store.db.QueryRow(`SELECT status FROM execution_steps WHERE id=?`, a.StepID).Scan(&stepState)
	_ = store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id=?`, a.WalletID).Scan(&laneState)
	if receiptState != "orphaned" || stepState != "reconciling" || laneState != "frozen" {
		t.Fatalf("receipt=%s step=%s lane=%s", receiptState, stepState, laneState)
	}
}

func TestC08TenThousandAdmissionConservation(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	const total = 10000
	var accepted, deduped, rejected atomic.Int64
	var wg sync.WaitGroup
	for range total {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := store.Admit(context.Background(), buyRequest(), fixedTime())
			if err != nil {
				rejected.Add(1)
				return
			}
			if a.Duplicate {
				deduped.Add(1)
			} else {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	queued := int64(0)
	if got := accepted.Load() + deduped.Load() + queued + rejected.Load(); got != total {
		t.Fatalf("conservation=%d", got)
	}
	if accepted.Load() != 1 || deduped.Load() != total-1 || rejected.Load() != 0 {
		t.Fatalf("accepted=%d deduped=%d rejected=%d", accepted.Load(), deduped.Load(), rejected.Load())
	}
	counts, err := store.Counts(context.Background())
	if err != nil || counts.Operations != 1 || counts.ExecutionSteps != 1 || counts.TransactionAttempts != 0 {
		t.Fatalf("counts=%#v err=%v", counts, err)
	}
}

func fixedTime() time.Time { return time.Unix(1234, 0).UTC() }
