package trade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestStep2BMultiRelayMergePermit2StepChainReplayAndCancelUnfreeze(t *testing.T) {
	// 1. AT-06 / AT-19: Multi-relay conservative merge rule.
	if got := MergeMultiRelayOutcomes([]RelayOutcome{RelayTimeout, RelayRejected}); got != MergedBroadcastUnknown {
		t.Fatalf("timeout + rejected MUST remain broadcast_unknown, got %s", got)
	}
	if got := MergeMultiRelayOutcomes([]RelayOutcome{RelayRejected, RelaySubmitted}); got != MergedSubmitted {
		t.Fatalf("rejected + submitted must be submitted, got %s", got)
	}
	if got := MergeMultiRelayOutcomes([]RelayOutcome{RelayRejected, RelayRejected}); got != MergedKnownUnsent {
		t.Fatalf("all rejected must be known_unsent, got %s", got)
	}

	// 2. AT-10: Permit2 expiration fail-closed.
	now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
	if err := ValidatePermit2Deadline(now.Add(-1*time.Second).Unix(), now); err == nil {
		t.Fatal("expected expired Permit2 deadline to fail closed")
	}
	if err := ValidatePermit2Deadline(now.Add(60*time.Second).Unix(), now); err != nil {
		t.Fatalf("expected valid Permit2 deadline to pass: %v", err)
	}

	// Open trade.db with migration 053.
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.TradeOwner, filepath.Join(t.TempDir(), "trade-bsc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	// 3. AT-11: Multi-step Approve -> Sell gating (REORGED approve keeps Sell locked).
	opID := "op-step2b-1"
	if err := store.RegisterStepChain(ctx, opID, []StepSpec{
		{StepID: "step-approve", StepIndex: 0, StepKind: "approve_exact"},
		{StepID: "step-sell", StepIndex: 1, StepKind: "sell"},
	}, now); err != nil {
		t.Fatalf("RegisterStepChain failed: %v", err)
	}
	if ok, err := store.CanUnlockStep(ctx, opID, 1); err != nil || ok {
		t.Fatalf("Sell must remain locked while Approve is PENDING: ok=%v err=%v", ok, err)
	}
	if err := store.UpdateStepStatus(ctx, "step-approve", "REORGED", now); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.CanUnlockStep(ctx, opID, 1); err != nil || ok {
		t.Fatalf("Sell must remain locked when Approve is REORGED: ok=%v err=%v", ok, err)
	}
	if err := store.UpdateStepStatus(ctx, "step-approve", "CONFIRMED_CONTINUITY", now); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.CanUnlockStep(ctx, opID, 1); err != nil || !ok {
		t.Fatalf("Sell must unlock once Approve reaches CONFIRMED_CONTINUITY: ok=%v err=%v", ok, err)
	}

	// 4. AT-06: CommitPreSendBatch + query-first one-shot UNKNOWN_REPLAY permit.
	wallet := common.HexToAddress("0x000000000000000000000000000000000000CAFE")
	rawBytes := []byte{0x02, 0xf8, 0xaa, 0xbb, 0xcc}
	rawHash := sha256.Sum256(rawBytes)
	rawHashHex := hex.EncodeToString(rawHash[:])
	origTxHash := common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")

	if err := store.CommitPreSendBatch(ctx, StepAttemptRecord{
		AttemptID:         "att-initial",
		StepID:            "step-sell",
		OperationID:       opID,
		WalletAddress:     wallet,
		Nonce:             7,
		AttemptKind:       "initial",
		TxHash:            origTxHash,
		ArtifactHash:      rawHashHex,
		MaxFeePerGasWei:   "1000000000",
		MaxPriorityFeeWei: "1000000000",
		State:             "BROADCAST_UNKNOWN",
	}, now); err != nil {
		t.Fatalf("CommitPreSendBatch failed: %v", err)
	}

	ev := QueryFirstEvidence{
		TxFoundOnChain:      false,
		ReceiptFoundOnChain: false,
		WalletOnChainNonce:  7,
		AttemptNonce:        7,
		QueriedBlockNumber:  41_000_000,
		QueriedBlockHash:    common.HexToHash("0x9999"),
	}
	// Wrong raw bytes must fail closed.
	if _, err := store.IssueAndConsumeUnknownReplayPermit(ctx, "perm-bad", "att-initial", []byte{0x01}, ev, now); err == nil {
		t.Fatal("expected tampered raw bytes to fail closed")
	}
	// Valid exact raw bytes + query-first evidence succeeds once.
	gotHash, err := store.IssueAndConsumeUnknownReplayPermit(ctx, "perm-1", "att-initial", rawBytes, ev, now)
	if err != nil || gotHash != origTxHash {
		t.Fatalf("expected valid UNKNOWN_REPLAY permit consumption, got hash=%s err=%v", gotHash.Hex(), err)
	}
	// Duplicate permit ID cannot be reused.
	if _, err := store.IssueAndConsumeUnknownReplayPermit(ctx, "perm-1", "att-initial", rawBytes, ev, now); err == nil {
		t.Fatal("expected duplicate UNKNOWN_REPLAY permit reuse to fail")
	}

	// 5. AT-07: Same-nonce 0-value Cancel tx + TryUnfreezeAmbiguousNonce when finalized_nonce > claimNonce.
	cancelTx, err := BuildSameNonceCancelTx(56, wallet, 7, big.NewInt(2_000_000_000), big.NewInt(2_000_000_000))
	if err != nil || cancelTx.Nonce() != 7 || cancelTx.Value().Sign() != 0 || *cancelTx.To() != wallet {
		t.Fatalf("unexpected same-nonce cancel tx: %+v err=%v", cancelTx, err)
	}
	cancelHash := common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")
	if err := store.CommitPreSendBatch(ctx, StepAttemptRecord{
		AttemptID:         "att-cancel",
		StepID:            "step-sell",
		OperationID:       opID,
		WalletAddress:     wallet,
		Nonce:             7,
		AttemptKind:       "cancel",
		TxHash:            cancelHash,
		ArtifactHash:      rawHashHex,
		MaxFeePerGasWei:   "2000000000",
		MaxPriorityFeeWei: "2000000000",
		State:             "SUBMITTED",
	}, now); err != nil {
		t.Fatalf("record cancel attempt failed: %v", err)
	}

	// Cannot unfreeze before finalized_nonce > 7.
	if unfrozen, err := store.TryUnfreezeAmbiguousNonce(ctx, wallet, 7, 7, false, cancelHash, now); err != nil || unfrozen {
		t.Fatalf("must NOT unfreeze while finalized_nonce <= claimNonce: unfrozen=%v err=%v", unfrozen, err)
	}
	// Once Cancel tx is finalized (finalized_nonce == 8 > 7) and origTx is not on-chain -> safely unfreeze!
	if unfrozen, err := store.TryUnfreezeAmbiguousNonce(ctx, wallet, 7, 8, false, cancelHash, now); err != nil || !unfrozen {
		t.Fatalf("expected safe unfreeze once finalized_nonce=8 > 7: unfrozen=%v err=%v", unfrozen, err)
	}
	var origState, cancelState string
	_ = store.db.QueryRowContext(ctx, `SELECT state FROM trade_step_attempts WHERE attempt_id = 'att-initial'`).Scan(&origState)
	_ = store.db.QueryRowContext(ctx, `SELECT state FROM trade_step_attempts WHERE attempt_id = 'att-cancel'`).Scan(&cancelState)
	if origState != "SUPERSEDED" || cancelState != "FINALIZED" {
		t.Fatalf("expected orig=SUPERSEDED cancel=FINALIZED, got orig=%s cancel=%s", origState, cancelState)
	}
}
