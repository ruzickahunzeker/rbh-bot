package trade

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
			var attempts int
			if err = store.db.QueryRow(`SELECT COUNT(*) FROM transaction_attempts`).Scan(&attempts); err != nil || attempts != 1 {
				t.Fatalf("attempts=%d err=%v", attempts, err)
			}
			var artifacts int
			_ = store.db.QueryRow(`SELECT COUNT(*) FROM transaction_attempts WHERE encrypted_raw_tx IS NOT NULL`).Scan(&artifacts)
			if artifacts != 1 {
				t.Fatalf("artifacts=%d", artifacts)
			}
		})
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
