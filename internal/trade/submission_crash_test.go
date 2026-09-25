package trade

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestSubmissionRecoveryRealProcessCrashWindows(t *testing.T) {
	stages := []string{"before_send", "during_send", "after_send_before_outcome_commit", "submission_committed_before_receipt", "receipt_observed_before_canonical", "after_canonical_effect_commit", "before_reorg_rollback_commit", "after_reorg_rollback_commit"}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "trade.db")
			cmd := exec.Command(os.Args[0], "-test.run=TestSubmissionRecoveryCrashWorker")
			cmd.Env = append(os.Environ(), "RBH_SUBMISSION_CRASH_WORKER=1", "RBH_SUBMISSION_CRASH_STAGE="+stage, "RBH_SUBMISSION_CRASH_DB="+path)
			if err := cmd.Run(); err == nil {
				t.Fatal("worker did not crash")
			}
			db, err := storage.Open(context.Background(), storage.TradeOwner, path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store, _ := NewStore(db)
			assertCrashStateAndRecover(t, store, stage)
		})
	}
}

type crashState struct {
	attempts, artifacts, submissions, receipts, effects, active, apply, rollback, reapply, nonceConsumed, gasConsumed int
	submission, receipt, step, lane, reservation, hash                                                                string
}

func readCrashState(t *testing.T, s *Store) crashState {
	t.Helper()
	var v crashState
	scalar := []struct {
		q   string
		dst any
	}{
		{`SELECT COUNT(*) FROM transaction_attempts`, &v.attempts}, {`SELECT COUNT(*) FROM transaction_attempts WHERE encrypted_raw_tx IS NOT NULL`, &v.artifacts}, {`SELECT COUNT(*) FROM transaction_submissions`, &v.submissions}, {`SELECT COUNT(*) FROM receipt_observations`, &v.receipts}, {`SELECT COUNT(*) FROM position_effects`, &v.effects}, {`SELECT COUNT(*) FROM position_effects WHERE state='active'`, &v.active}, {`SELECT COUNT(*) FROM position_effect_history WHERE transition='apply'`, &v.apply}, {`SELECT COUNT(*) FROM position_effect_history WHERE transition='rollback'`, &v.rollback}, {`SELECT COUNT(*) FROM position_effect_history WHERE transition='reapply'`, &v.reapply}, {`SELECT raw_tx_hash FROM transaction_attempts LIMIT 1`, &v.hash}, {`SELECT status FROM execution_steps WHERE kind='pons_curve_execution'`, &v.step}, {`SELECT state FROM execution_wallet_lanes WHERE wallet_id='wallet-1'`, &v.lane}, {`SELECT status FROM execution_reservations LIMIT 1`, &v.reservation}, {`SELECT nonce_consumed FROM execution_reservations LIMIT 1`, &v.nonceConsumed}, {`SELECT gas_consumed FROM execution_reservations LIMIT 1`, &v.gasConsumed}}
	for _, x := range scalar {
		if err := s.db.QueryRow(x.q).Scan(x.dst); err != nil {
			t.Fatalf("query %s: %v", x.q, err)
		}
	}
	_ = s.db.QueryRow(`SELECT COALESCE((SELECT state FROM transaction_submissions ORDER BY sequence DESC LIMIT 1),'')`).Scan(&v.submission)
	_ = s.db.QueryRow(`SELECT COALESCE((SELECT canonical_state FROM receipt_observations ORDER BY observed_at DESC LIMIT 1),'')`).Scan(&v.receipt)
	return v
}

func assertCrashStateAndRecover(t *testing.T, store *Store, stage string) {
	t.Helper()
	before := readCrashState(t, store)
	if before.attempts != 1 || before.artifacts != 1 || before.hash == "" {
		t.Fatalf("base state=%+v", before)
	}
	switch stage {
	case "before_send", "during_send", "after_send_before_outcome_commit":
		if before.submission != "submitting" || before.receipts != 0 || before.effects != 0 || before.lane != "signed" || before.reservation != "signed" {
			t.Fatalf("pre-recovery inflight=%+v", before)
		}
	case "submission_committed_before_receipt":
		if before.submission != "submitted" || before.receipts != 0 || before.effects != 0 || before.reservation != "signed" {
			t.Fatalf("pre-recovery submitted=%+v", before)
		}
	case "receipt_observed_before_canonical":
		if before.receipt != "observed" || before.receipts != 1 || before.active != 0 || before.reservation != "signed" {
			t.Fatalf("pre-recovery receipt=%+v", before)
		}
	case "after_canonical_effect_commit", "before_reorg_rollback_commit":
		if before.receipt != "canonical_success" || before.active != 1 || before.apply != 1 || before.lane != "idle" || before.reservation != "settled" || before.nonceConsumed != 1 || before.gasConsumed != 1 {
			t.Fatalf("pre-recovery canonical=%+v", before)
		}
	case "after_reorg_rollback_commit":
		if before.receipt != "orphaned" || before.active != 0 || before.rollback != 1 || before.lane != "frozen" {
			t.Fatalf("pre-recovery rollback=%+v", before)
		}
	}
	key, _ := crypto.HexToECDSA("4f3edf983ac63ad25b2d3a6f0b6d4d6d4f2f5f645f3c5b4c8a07a5f7b6c9d001")
	signer, _ := NewLocalSigner(key)
	cipher, _ := NewAESGCMCipher("test-v1", bytes.Repeat([]byte{7}, 32))
	kernel, _ := NewExecutionKernel(store, newFakeBackend(), signer, cipher)
	artifact, found, err := store.LoadEncryptedArtifact(context.Background(), "intent-buy")
	if err != nil || !found {
		t.Fatalf("artifact found=%v err=%v", found, err)
	}
	raw, _ := cipher.Decrypt(artifact.KeyVersion, artifact.Ciphertext, artifact.EncryptionNonce, artifactAAD(artifact.Operation, artifact.StepID, artifact.AttemptID))
	defer clear(raw)
	if common.BytesToHash(crypto.Keccak256(raw)).Hex() != before.hash {
		t.Fatal("artifact digest changed after restart")
	}
	switch stage {
	case "before_send", "during_send", "after_send_before_outcome_commit":
		b := &fakeBroadcaster{hash: artifact.TxHash}
		svc, _ := NewSubmissionService(store, kernel, b)
		if _, err = svc.Submit(context.Background(), artifact.Operation); !errors.Is(err, ErrBroadcastAmbiguous) || b.calls != 0 {
			t.Fatalf("unsafe restart send calls=%d err=%v", b.calls, err)
		}
	case "submission_committed_before_receipt":
		b := &fakeBroadcaster{hash: artifact.TxHash}
		svc, _ := NewSubmissionService(store, kernel, b)
		if _, err = svc.Submit(context.Background(), artifact.Operation); err != nil || b.calls != 0 {
			t.Fatalf("duplicate send calls=%d err=%v", b.calls, err)
		}
	case "receipt_observed_before_canonical":
		r := ReceiptObservation{ID: deterministicID(artifact.AttemptID, "0xabc"), AttemptID: artifact.AttemptID, TxHash: artifact.TxHash, BlockNumber: 10, BlockHash: "0xabc", Status: 1}
		effect := &PositionEffect{Asset: testToken.Hex(), Delta: "100"}
		if err = store.Canonicalize(context.Background(), artifact.SignedArtifact, r, effect, fixedTime()); err != nil {
			t.Fatal(err)
		}
	case "after_canonical_effect_commit":
		r := ReceiptObservation{ID: deterministicID(artifact.AttemptID, "0xabc"), AttemptID: artifact.AttemptID, TxHash: artifact.TxHash, BlockNumber: 10, BlockHash: "0xabc", Status: 1}
		effect := &PositionEffect{Asset: testToken.Hex(), Delta: "100"}
		if err = store.Canonicalize(context.Background(), artifact.SignedArtifact, r, effect, fixedTime()); err != nil {
			t.Fatal(err)
		}
	case "before_reorg_rollback_commit":
		r := ReceiptObservation{ID: deterministicID(artifact.AttemptID, "0xabc"), AttemptID: artifact.AttemptID, TxHash: artifact.TxHash, BlockNumber: 10, BlockHash: "0xabc", Status: 1}
		if err = store.Orphan(context.Background(), artifact.SignedArtifact, r, fixedTime()); err != nil {
			t.Fatal(err)
		}
	case "after_reorg_rollback_commit":
		r := ReceiptObservation{ID: deterministicID(artifact.AttemptID, "0xabc"), AttemptID: artifact.AttemptID, TxHash: artifact.TxHash, BlockNumber: 10, BlockHash: "0xabc", Status: 1}
		effect := &PositionEffect{Asset: testToken.Hex(), Delta: "100"}
		if err = store.Canonicalize(context.Background(), artifact.SignedArtifact, r, effect, fixedTime()); err != nil {
			t.Fatal(err)
		}
	}
	after := readCrashState(t, store)
	if after.attempts != 1 || after.artifacts != 1 || after.hash != before.hash {
		t.Fatalf("identity changed before=%+v after=%+v", before, after)
	}
	switch stage {
	case "before_send", "during_send", "after_send_before_outcome_commit":
		if after.submission != "broadcast_unknown" || after.lane != "frozen" || after.reservation != "frozen" || after.nonceConsumed != 0 || after.gasConsumed != 0 {
			t.Fatalf("unknown state=%+v", after)
		}
	case "submission_committed_before_receipt":
		if after.submission != "submitted" || after.receipts != 0 || after.reservation != "signed" {
			t.Fatalf("submitted state=%+v", after)
		}
	case "receipt_observed_before_canonical", "after_canonical_effect_commit":
		if after.active != 1 || after.apply != 1 || after.lane != "idle" || after.reservation != "settled" || after.nonceConsumed != 1 || after.gasConsumed != 1 {
			t.Fatalf("canonical state=%+v", after)
		}
	case "before_reorg_rollback_commit":
		if after.active != 0 || after.rollback != 1 || after.lane != "frozen" {
			t.Fatalf("rollback state=%+v", after)
		}
	case "after_reorg_rollback_commit":
		if after.active != 1 || after.rollback != 1 || after.reapply != 1 {
			t.Fatalf("reapply state=%+v", after)
		}
	}
}

type crashBroadcaster struct {
	stage string
	hash  string
}

func (b crashBroadcaster) SendRawTransaction(context.Context, []byte) (string, error) {
	if b.stage == "during_send" {
		os.Exit(92)
	}
	return b.hash, nil
}

func TestSubmissionRecoveryCrashWorker(t *testing.T) {
	if os.Getenv("RBH_SUBMISSION_CRASH_WORKER") != "1" {
		return
	}
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.TradeOwner, os.Getenv("RBH_SUBMISSION_CRASH_DB"))
	if err != nil {
		os.Exit(81)
	}
	if db.Migrate(ctx) != nil {
		os.Exit(82)
	}
	store, _ := NewStore(db)
	backend := newFakeBackend()
	key, _ := crypto.HexToECDSA("4f3edf983ac63ad25b2d3a6f0b6d4d6d4f2f5f645f3c5b4c8a07a5f7b6c9d001")
	signer, _ := NewLocalSigner(key)
	cipher, _ := NewAESGCMCipher("test-v1", bytes.Repeat([]byte{7}, 32))
	_ = store.RegisterDryRunWallet(ctx, "wallet-1", signer.Address())
	dry, _ := NewEngine(store, backend)
	_, _ = dry.DryRun(ctx, buyRequest())
	kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
	artifact, _ := kernel.Prepare(ctx, "intent-buy", "wallet-1")
	stage := os.Getenv("RBH_SUBMISSION_CRASH_STAGE")
	svc, _ := NewSubmissionService(store, kernel, crashBroadcaster{stage: stage, hash: artifact.TxHash})
	svc.SetHookForTest(func(point string) {
		if point == stage {
			os.Exit(91)
		}
	})
	_, _ = svc.Submit(ctx, artifact.Operation)
	if stage == "submission_committed_before_receipt" {
		os.Exit(91)
	}
	receipt, _ := store.ObserveReceipt(ctx, artifact, ReceiptObservation{TxHash: artifact.TxHash, BlockNumber: 10, BlockHash: "0xabc", Status: 1}, fixedTime())
	if stage == "receipt_observed_before_canonical" {
		os.Exit(91)
	}
	store.SetRecoveryHookForTest(func(point string) {
		if point == stage {
			os.Exit(91)
		}
	})
	effect := &PositionEffect{Asset: testToken.Hex(), Delta: "100"}
	_ = store.Canonicalize(ctx, artifact, receipt, effect, fixedTime())
	if stage == "after_canonical_effect_commit" {
		return
	}
	_ = store.Orphan(ctx, artifact, receipt, fixedTime())
	os.Exit(0)
}
