package trade

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestExecutionKernelBuySellEncryptedArtifacts(t *testing.T) {
	for _, kind := range []string{"buy", "sell"} {
		t.Run(kind, func(t *testing.T) {
			store, closeDB := openTradeStore(t)
			defer closeDB()
			backend := newFakeBackend()
			signer, cipher := executionSecrets(t)
			if err := store.RegisterDryRunWallet(context.Background(), "wallet-1", signer.Address()); err != nil {
				t.Fatal(err)
			}
			dryRun, _ := NewEngine(store, backend)
			request := buyRequest()
			if kind == "sell" {
				request = sellRequest()
			}
			result, err := dryRun.DryRun(context.Background(), request)
			if err != nil || result.Status != "success" {
				t.Fatalf("dry-run: %#v %v", result, err)
			}
			kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
			artifact, err := kernel.Prepare(context.Background(), request.Intent.ID, request.WalletID)
			if err != nil {
				t.Fatal(err)
			}
			if artifact.Nonce != 7 || artifact.TxHash == "" || artifact.KeyVersion != "test-v1" || artifact.From != signer.Address().Hex() {
				t.Fatalf("artifact=%#v", artifact)
			}
			stored, found, err := store.LoadEncryptedArtifact(context.Background(), request.Intent.ID)
			if err != nil || !found || bytes.Contains(stored.Ciphertext, commonPrivateKeyBytes(t, signer)) {
				t.Fatalf("encrypted artifact invalid found=%v err=%v", found, err)
			}
			plaintext, err := cipher.Decrypt(stored.KeyVersion, stored.Ciphertext, stored.EncryptionNonce, artifactAAD(stored.Operation, stored.StepID, stored.AttemptID))
			if err != nil || verifyRawArtifact(plaintext, stored.SignedArtifact) != nil {
				t.Fatalf("artifact integrity: %v", err)
			}
			replay, err := kernel.Prepare(context.Background(), request.Intent.ID, request.WalletID)
			if err != nil || !replay.Duplicate || replay.TxHash != artifact.TxHash || replay.Nonce != artifact.Nonce {
				t.Fatalf("replay=%#v err=%v", replay, err)
			}
			assertAttemptCount(t, store, 1)
		})
	}
}

func TestExecutionKernelOneUnresolvedStepAndNonceCollisionPrevention(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := newFakeBackend()
	signer, cipher := executionSecrets(t)
	if err := store.RegisterDryRunWallet(context.Background(), "wallet-1", signer.Address()); err != nil {
		t.Fatal(err)
	}
	dryRun, _ := NewEngine(store, backend)
	first := buyRequest()
	second := buyRequest()
	second.Intent.ID, second.Intent.IdempotencyKey, second.Intent.SourceEventID, second.Intent.SourceObservationID = "intent-buy-2", "intent-buy-2", "event-2", "observation-2"
	for _, request := range []DryRunRequest{first, second} {
		if result, err := dryRun.DryRun(context.Background(), request); err != nil || result.Status != "success" {
			t.Fatalf("seed dry-run: %#v %v", result, err)
		}
	}
	kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
	if _, err := kernel.Prepare(context.Background(), first.Intent.ID, first.WalletID); err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.Prepare(context.Background(), second.Intent.ID, second.WalletID); !errors.Is(err, ErrWalletLaneBusy) {
		t.Fatalf("second unresolved operation err=%v", err)
	}
	assertAttemptCount(t, store, 1)
}

func TestExecutionKernelConcurrentDuplicateAdmissionUsesOneNonce(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := newFakeBackend()
	signer, cipher := executionSecrets(t)
	if err := store.RegisterDryRunWallet(context.Background(), "wallet-1", signer.Address()); err != nil {
		t.Fatal(err)
	}
	dryRun, _ := NewEngine(store, backend)
	request := buyRequest()
	if _, err := dryRun.DryRun(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
	const total = 64
	var wg sync.WaitGroup
	results := make(chan SignedArtifact, total)
	errorsSeen := make(chan error, total)
	for range total {
		wg.Add(1)
		go func() {
			defer wg.Done()
			artifact, err := kernel.Prepare(context.Background(), request.Intent.ID, request.WalletID)
			if err != nil {
				errorsSeen <- err
				return
			}
			results <- artifact
		}()
	}
	wg.Wait()
	close(results)
	close(errorsSeen)
	if len(errorsSeen) != 0 {
		t.Fatalf("concurrent admission errors=%d first=%v", len(errorsSeen), <-errorsSeen)
	}
	var txHash string
	accepted, deduped := 0, 0
	for artifact := range results {
		if txHash == "" {
			txHash = artifact.TxHash
		}
		if artifact.TxHash != txHash || artifact.Nonce != 7 {
			t.Fatalf("duplicate execution artifact: %#v", artifact)
		}
		if artifact.Duplicate {
			deduped++
		} else {
			accepted++
		}
	}
	if accepted+deduped != total || accepted != 1 || deduped != total-1 {
		t.Fatalf("conservation total=%d accepted=%d deduped=%d", total, accepted, deduped)
	}
	assertAttemptCount(t, store, 1)
}

func TestExecutionKernelPendingNonceDriftFreezesLane(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := &nonceDriftBackend{fakeBackend: newFakeBackend()}
	signer, cipher := executionSecrets(t)
	if err := store.RegisterDryRunWallet(context.Background(), "wallet-1", signer.Address()); err != nil {
		t.Fatal(err)
	}
	dryRun, _ := NewEngine(store, backend)
	if _, err := dryRun.DryRun(context.Background(), buyRequest()); err != nil {
		t.Fatal(err)
	}
	kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
	if _, err := kernel.Prepare(context.Background(), "intent-buy", "wallet-1"); !errors.Is(err, ErrNonceConflict) {
		t.Fatalf("nonce drift err=%v", err)
	}
	var laneState, reservationState string
	if err := store.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id='wallet-1'`).Scan(&laneState); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT status FROM execution_reservations WHERE operation_id='intent-buy'`).Scan(&reservationState); err != nil {
		t.Fatal(err)
	}
	if laneState != "frozen" || reservationState != "frozen" {
		t.Fatalf("lane=%s reservation=%s", laneState, reservationState)
	}
	var signed int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transaction_attempts WHERE status='signed'`).Scan(&signed); err != nil || signed != 0 {
		t.Fatalf("signed=%d err=%v", signed, err)
	}
}

func TestExecutionKernelIntegrityFailuresFailClosed(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := newFakeBackend()
	signer, cipher := executionSecrets(t)
	if err := store.RegisterDryRunWallet(context.Background(), "wallet-1", signer.Address()); err != nil {
		t.Fatal(err)
	}
	dryRun, _ := NewEngine(store, backend)
	request := buyRequest()
	if _, err := dryRun.DryRun(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
	if _, err := kernel.Prepare(context.Background(), request.Intent.ID, request.WalletID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE transaction_attempts SET encrypted_raw_tx=x'00' WHERE status='signed'`); err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.Prepare(context.Background(), request.Intent.ID, request.WalletID); !errors.Is(err, ErrArtifactIntegrity) {
		t.Fatalf("corruption err=%v", err)
	}
	if _, err := store.db.Exec(`UPDATE transaction_attempts SET key_version='unknown' WHERE status='signed'`); err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.Prepare(context.Background(), request.Intent.ID, request.WalletID); !errors.Is(err, ErrUnknownKeyVersion) {
		t.Fatalf("unknown key err=%v", err)
	}
}

func TestExecutionKernelWrongRPCChainFailsBeforeReservation(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := newFakeBackend()
	backend.chainID = big.NewInt(1)
	signer, cipher := executionSecrets(t)
	if err := store.RegisterDryRunWallet(context.Background(), "wallet-1", signer.Address()); err != nil {
		t.Fatal(err)
	}
	dryRun, _ := NewEngine(store, backend)
	if _, err := dryRun.DryRun(context.Background(), buyRequest()); err != nil {
		t.Fatal(err)
	}
	kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
	if _, err := kernel.Prepare(context.Background(), "intent-buy", "wallet-1"); !errors.Is(err, ErrArtifactIntegrity) {
		t.Fatalf("wrong RPC chain err=%v", err)
	}
	assertAttemptCount(t, store, 0)
}

func TestExecutionKernelEncryptionFailureCreatesNoSignedArtifact(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := newFakeBackend()
	signer, _ := executionSecrets(t)
	if err := store.RegisterDryRunWallet(context.Background(), "wallet-1", signer.Address()); err != nil {
		t.Fatal(err)
	}
	dryRun, _ := NewEngine(store, backend)
	if _, err := dryRun.DryRun(context.Background(), buyRequest()); err != nil {
		t.Fatal(err)
	}
	kernel, _ := NewExecutionKernel(store, backend, signer, failingCipher{})
	if _, err := kernel.Prepare(context.Background(), "intent-buy", "wallet-1"); !errors.Is(err, ErrEncryption) {
		t.Fatalf("encryption err=%v", err)
	}
	var signed int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transaction_attempts WHERE status='signed' OR encrypted_raw_tx IS NOT NULL`).Scan(&signed); err != nil || signed != 0 {
		t.Fatalf("signed artifacts=%d err=%v", signed, err)
	}
}

func TestVerifyRawArtifactRejectsWrongChainAndSigner(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress("0x3000000000000000000000000000000000000003")
	build := func(chainID int64) ([]byte, SignedArtifact) {
		tx := types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(chainID), Nonce: 7, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(10), Gas: 100000, To: &to, Value: big.NewInt(1), Data: []byte{1}})
		signed, signErr := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(chainID)), key)
		if signErr != nil {
			t.Fatal(signErr)
		}
		raw, _ := signed.MarshalBinary()
		return raw, SignedArtifact{Nonce: 7, TxHash: signed.Hash().Hex(), From: crypto.PubkeyToAddress(key.PublicKey).Hex(), To: to.Hex(), Value: "1", Data: "0x01", GasLimit: 100000, GasTipCap: "1", GasFeeCap: "10", PolicyVersion: 1, QuoteBlockNumber: 1, QuoteBlockHash: common.HexToHash("0x1234").Hex(), ExpiresAt: "2100-01-01T00:00:00Z"}
	}
	wrongChainRaw, wrongChainArtifact := build(1)
	if err := verifyRawArtifact(wrongChainRaw, wrongChainArtifact); !errors.Is(err, ErrArtifactIntegrity) {
		t.Fatalf("wrong chain err=%v", err)
	}
	raw, artifact := build(int64(ChainID))
	artifact.From = common.HexToAddress("0x9999999999999999999999999999999999999999").Hex()
	if err := verifyRawArtifact(raw, artifact); !errors.Is(err, ErrWrongSigner) {
		t.Fatalf("wrong signer err=%v", err)
	}
}

func TestExecutionKernelRealProcessCrashWindows(t *testing.T) {
	for _, stage := range []string{"after_nonce_reservation_commit", "after_signed_artifact_commit"} {
		t.Run(stage, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "trade.db")
			cmd := exec.Command(os.Args[0], "-test.run=TestExecutionKernelCrashWorker")
			cmd.Env = append(os.Environ(), "RBH_EXECUTION_CRASH_WORKER=1", "RBH_EXECUTION_CRASH_STAGE="+stage, "RBH_EXECUTION_CRASH_DB="+databasePath)
			if err := cmd.Run(); err == nil {
				t.Fatal("worker did not crash")
			}
			database, err := storage.Open(context.Background(), storage.TradeOwner, databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			store, _ := NewStore(database)
			var signed int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM transaction_attempts WHERE status='signed' AND encrypted_raw_tx IS NOT NULL`).Scan(&signed); err != nil {
				t.Fatal(err)
			}
			if stage == "after_nonce_reservation_commit" && signed != 0 {
				t.Fatalf("pre-commit crash left broadcastable artifact: %d", signed)
			}
			if stage == "after_signed_artifact_commit" && signed != 1 {
				t.Fatalf("post-commit crash lost artifact: %d", signed)
			}
			contents, err := os.ReadFile(databasePath)
			if err != nil {
				t.Fatal(err)
			}
			privateKeyHex := []byte("4f3edf983ac63ad25b2d3a6f0b6d4d6d4f2f5f645f3c5b4c8a07a5f7b6c9d001")
			privateKey, _ := crypto.HexToECDSA(string(privateKeyHex))
			if bytes.Contains(bytes.ToLower(contents), privateKeyHex) || bytes.Contains(contents, crypto.FromECDSA(privateKey)) {
				t.Fatal("private key leaked to SQLite")
			}
			if stage == "after_signed_artifact_commit" {
				stored, found, loadErr := store.LoadEncryptedArtifact(context.Background(), "intent-buy")
				if loadErr != nil || !found {
					t.Fatalf("restart artifact found=%v err=%v", found, loadErr)
				}
				cipher, _ := NewAESGCMCipher("test-v1", bytes.Repeat([]byte{7}, 32))
				raw, decryptErr := cipher.Decrypt(stored.KeyVersion, stored.Ciphertext, stored.EncryptionNonce, artifactAAD(stored.Operation, stored.StepID, stored.AttemptID))
				if decryptErr != nil || verifyRawArtifact(raw, stored.SignedArtifact) != nil {
					t.Fatalf("restart exact artifact err=%v", decryptErr)
				}
			}
		})
	}
}

func TestExecutionKernelCrashWorker(t *testing.T) {
	if os.Getenv("RBH_EXECUTION_CRASH_WORKER") != "1" {
		return
	}
	ctx := context.Background()
	database, err := storage.Open(ctx, storage.TradeOwner, os.Getenv("RBH_EXECUTION_CRASH_DB"))
	if err != nil {
		os.Exit(81)
	}
	if database.Migrate(ctx) != nil {
		os.Exit(82)
	}
	store, _ := NewStore(database)
	backend := newFakeBackend()
	key, _ := crypto.HexToECDSA("4f3edf983ac63ad25b2d3a6f0b6d4d6d4f2f5f645f3c5b4c8a07a5f7b6c9d001")
	signer, _ := NewLocalSigner(key)
	cipher, _ := NewAESGCMCipher("test-v1", bytes.Repeat([]byte{7}, 32))
	_ = store.RegisterDryRunWallet(ctx, "wallet-1", signer.Address())
	dryRun, _ := NewEngine(store, backend)
	_, _ = dryRun.DryRun(ctx, buyRequest())
	kernel, _ := NewExecutionKernel(store, backend, signer, cipher)
	kernel.SetHookForTest(func(stage string) {
		if stage == os.Getenv("RBH_EXECUTION_CRASH_STAGE") {
			os.Exit(91)
		}
	})
	_, _ = kernel.Prepare(ctx, "intent-buy", "wallet-1")
	os.Exit(0)
}

func executionSecrets(t *testing.T) (*LocalSigner, *AESGCMCipher) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, _ := NewLocalSigner(key)
	cipher, err := NewAESGCMCipher("test-v1", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return signer, cipher
}

func commonPrivateKeyBytes(t *testing.T, signer *LocalSigner) []byte {
	t.Helper()
	return crypto.FromECDSA(signer.key)
}

func assertAttemptCount(t *testing.T, store *Store, want int) {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transaction_attempts`).Scan(&count); err != nil || count != want {
		t.Fatalf("attempt count=%d want=%d err=%v", count, want, err)
	}
}

type failingCipher struct{}

type nonceDriftBackend struct {
	*fakeBackend
	calls atomic.Uint32
}

func (b *nonceDriftBackend) PendingNonce(context.Context, common.Address) (uint64, error) {
	if b.calls.Add(1) == 1 {
		return 7, nil
	}
	return 8, nil
}

func (failingCipher) KeyVersion() string { return "fail-v1" }
func (failingCipher) Encrypt([]byte, []byte) ([]byte, []byte, error) {
	return nil, nil, ErrEncryption
}
func (failingCipher) Decrypt(string, []byte, []byte, []byte) ([]byte, error) {
	return nil, ErrEncryption
}
