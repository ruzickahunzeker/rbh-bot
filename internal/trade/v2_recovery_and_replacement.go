package trade

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

// RelayOutcome represents a single relay's broadcast response for one signed tx_hash.
type RelayOutcome string

const (
	RelaySubmitted    RelayOutcome = "submitted"
	RelayAlreadyKnown RelayOutcome = "already_known"
	RelayRejected     RelayOutcome = "rejected"
	RelayTimeout      RelayOutcome = "timeout"
	RelayNetworkError RelayOutcome = "network_error"
)

// MergedBroadcastState is the conservative aggregate outcome across N relays.
type MergedBroadcastState string

const (
	MergedSubmitted        MergedBroadcastState = "submitted"
	MergedBroadcastUnknown MergedBroadcastState = "broadcast_unknown"
	MergedKnownUnsent      MergedBroadcastState = "known_unsent"
)

// MergeMultiRelayOutcomes implements the V3 conservative multi-relay merge rule (AT-06/AT-19):
//   - If ANY relay confirms submitted or already_known -> "submitted".
//   - If ANY relay timed out or errored (even if all other relays explicitly rejected) -> "broadcast_unknown".
//   - Only if ALL relays (at least 1) explicitly rejected -> "known_unsent".
func MergeMultiRelayOutcomes(outcomes []RelayOutcome) MergedBroadcastState {
	if len(outcomes) == 0 {
		return MergedBroadcastUnknown
	}
	hasUnknown := false
	for _, o := range outcomes {
		switch o {
		case RelaySubmitted, RelayAlreadyKnown:
			return MergedSubmitted
		case RelayRejected:
			// continue checking other relays
		default:
			hasUnknown = true
		}
	}
	if hasUnknown {
		return MergedBroadcastUnknown
	}
	return MergedKnownUnsent
}

// ValidatePermit2Deadline enforces AT-10: expired Permit2 signatures fail closed before signing/broadcast.
func ValidatePermit2Deadline(sigDeadlineUnix int64, now time.Time) error {
	if sigDeadlineUnix <= 0 {
		return errors.New("permit2 deadline must be positive")
	}
	if now.UTC().Unix() >= sigDeadlineUnix {
		return fmt.Errorf("permit2 authorization expired at %d (now=%d)", sigDeadlineUnix, now.UTC().Unix())
	}
	return nil
}

// QueryFirstEvidence captures the mandatory read-only chain query performed before issuing
// an UNKNOWN_REPLAY permit.
type QueryFirstEvidence struct {
	TxFoundOnChain      bool
	ReceiptFoundOnChain bool
	WalletOnChainNonce  uint64
	AttemptNonce        uint64
	QueriedBlockNumber  uint64
	QueriedBlockHash    common.Hash
}

func (e QueryFirstEvidence) HashHex() string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "tx=%t|rcpt=%t|onchain=%d|attempt=%d|blk=%d|hash=%s",
		e.TxFoundOnChain, e.ReceiptFoundOnChain, e.WalletOnChainNonce, e.AttemptNonce, e.QueriedBlockNumber, strings.ToLower(e.QueriedBlockHash.Hex()))
	return hex.EncodeToString(h.Sum(nil))
}

// StepSpec defines one step in a multi-step V2 operation (e.g. approve_exact -> sell).
type StepSpec struct {
	StepID    string
	StepIndex int
	StepKind  string
}

// RegisterStepChain persists an ordered multi-step V2 operation atomically in a single write tx.
func (s *Store) RegisterStepChain(ctx context.Context, operationID string, steps []StepSpec, now time.Time) error {
	if s == nil || s.db == nil || operationID == "" || len(steps) == 0 {
		return errors.New("invalid step chain registration")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	nowStr := now.UTC().Format(time.RFC3339Nano)
	for _, st := range steps {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO trade_operation_steps(step_id, operation_id, step_index, step_kind, status, created_at, updated_at)
			 VALUES(?,?,?,?,'PENDING',?,?)`,
			st.StepID, operationID, st.StepIndex, st.StepKind, nowStr, nowStr,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateStepStatus updates a single step's durable state (e.g. CONFIRMED_CONTINUITY or REORGED).
func (s *Store) UpdateStepStatus(ctx context.Context, stepID, status string, now time.Time) error {
	if s == nil || s.db == nil || stepID == "" {
		return errors.New("invalid step status update")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE trade_operation_steps SET status = ?, updated_at = ? WHERE step_id = ?`,
		status, now.UTC().Format(time.RFC3339Nano), stepID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// CanUnlockStep enforces AT-11: step_index N can only unlock if ALL prior steps (0..N-1)
// for operationID are in CONFIRMED_CONTINUITY. If Approve timed out or was REORGED, Sell stays locked.
func (s *Store) CanUnlockStep(ctx context.Context, operationID string, stepIndex int) (bool, error) {
	if s == nil || s.db == nil || operationID == "" || stepIndex < 0 {
		return false, errors.New("invalid unlock check")
	}
	if stepIndex == 0 {
		return true, nil
	}
	var unconfirmed int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM trade_operation_steps
		 WHERE operation_id = ? AND step_index < ? AND status != 'CONFIRMED_CONTINUITY'`,
		operationID, stepIndex,
	).Scan(&unconfirmed); err != nil {
		return false, err
	}
	return unconfirmed == 0, nil
}

// StepAttemptRecord captures one signed candidate tx under a step (initial / speed_up / cancel).
type StepAttemptRecord struct {
	AttemptID         string
	StepID            string
	OperationID       string
	WalletAddress     common.Address
	Nonce             uint64
	AttemptKind       string // initial | speed_up | cancel
	TxHash            common.Hash
	ArtifactHash      string
	MaxFeePerGasWei   string
	MaxPriorityFeeWei string
	State             string
}

// CommitPreSendBatch atomically records a step transition to IN_FLIGHT and its signed attempt
// inside a single SQLite write transaction (Step 2B pre-send fsync reduction).
func (s *Store) CommitPreSendBatch(ctx context.Context, attempt StepAttemptRecord, now time.Time) error {
	if s == nil || s.db == nil || attempt.AttemptID == "" || attempt.StepID == "" {
		return errors.New("invalid pre-send batch")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	nowStr := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`UPDATE trade_operation_steps SET status = 'IN_FLIGHT', updated_at = ? WHERE step_id = ?`,
		nowStr, attempt.StepID,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO trade_step_attempts(attempt_id, step_id, operation_id, wallet_address, nonce, attempt_kind, tx_hash, artifact_hash, max_fee_per_gas_wei, max_priority_fee_wei, state, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		attempt.AttemptID, attempt.StepID, attempt.OperationID, strings.ToLower(attempt.WalletAddress.Hex()),
		attempt.Nonce, attempt.AttemptKind, strings.ToLower(attempt.TxHash.Hex()), attempt.ArtifactHash,
		attempt.MaxFeePerGasWei, attempt.MaxPriorityFeeWei, attempt.State, nowStr, nowStr,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// IssueAndConsumeUnknownReplayPermit enforces Step 2B AT-06:
//  1. Requires read-only query-first evidence proving tx/receipt not yet on-chain and on-chain nonce <= attempt nonce.
//  2. Verifies exact raw-byte artifact hash matches the original signed attempt.
//  3. Atomically issues and consumes a one-shot permit in SQLite so the permit cannot be replayed twice.
func (s *Store) IssueAndConsumeUnknownReplayPermit(
	ctx context.Context,
	permitID, attemptID string,
	rawSignedTx []byte,
	evidence QueryFirstEvidence,
	now time.Time,
) (common.Hash, error) {
	if s == nil || s.db == nil || permitID == "" || attemptID == "" || len(rawSignedTx) == 0 {
		return common.Hash{}, errors.New("invalid unknown replay permit request")
	}
	if evidence.QueriedBlockHash == (common.Hash{}) {
		return common.Hash{}, errors.New("query-first block hash is required")
	}
	if evidence.TxFoundOnChain || evidence.ReceiptFoundOnChain {
		return common.Hash{}, errors.New("query-first found tx or receipt already on-chain; replay forbidden")
	}
	if evidence.WalletOnChainNonce > evidence.AttemptNonce {
		return common.Hash{}, errors.New("wallet on-chain nonce already advanced past attempt nonce; replay forbidden")
	}

	rawHashSum := sha256.Sum256(rawSignedTx)
	rawHashHex := hex.EncodeToString(rawHashSum[:])

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return common.Hash{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var storedTxHash, storedArtifactHash, storedState string
	var storedNonce uint64
	err = tx.QueryRowContext(ctx,
		`SELECT tx_hash, artifact_hash, state, nonce FROM trade_step_attempts WHERE attempt_id = ?`,
		attemptID,
	).Scan(&storedTxHash, &storedArtifactHash, &storedState, &storedNonce)
	if err != nil {
		return common.Hash{}, err
	}
	if storedState != "BROADCAST_UNKNOWN" {
		return common.Hash{}, fmt.Errorf("attempt %s state is %s, not BROADCAST_UNKNOWN", attemptID, storedState)
	}
	if storedNonce != evidence.AttemptNonce {
		return common.Hash{}, errors.New("query-first attempt nonce mismatch")
	}
	if storedArtifactHash != rawHashHex {
		return common.Hash{}, errors.New("exact signed raw bytes hash mismatch; replay forbidden")
	}

	nowStr := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO unknown_replay_permits(permit_id, attempt_id, tx_hash, artifact_hash, query_first_evidence_hash, issued_at, consumed_at)
		 VALUES(?,?,?,?,?,?,?)`,
		permitID, attemptID, storedTxHash, storedArtifactHash, evidence.HashHex(), nowStr, nowStr,
	); err != nil {
		return common.Hash{}, fmt.Errorf("consume unknown replay permit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return common.Hash{}, err
	}
	return common.HexToHash(storedTxHash), nil
}

// BuildSameNonceCancelTx constructs an unsigned EIP-1559 0-value self-transfer Cancel tx
// at the exact same nonce with bumped gas fees to cleanly supersede an ambiguous or stuck nonce (AT-07).
func BuildSameNonceCancelTx(chainID uint64, wallet common.Address, nonce uint64, maxFeeWei, maxTipWei *big.Int) (*gethtypes.Transaction, error) {
	if chainID == 0 || wallet == (common.Address{}) || maxFeeWei == nil || maxFeeWei.Sign() <= 0 || maxTipWei == nil || maxTipWei.Sign() <= 0 || maxTipWei.Cmp(maxFeeWei) > 0 {
		return nil, errors.New("invalid cancel tx parameters")
	}
	return gethtypes.NewTx(&gethtypes.DynamicFeeTx{
		ChainID:   new(big.Int).SetUint64(chainID),
		Nonce:     nonce,
		GasTipCap: new(big.Int).Set(maxTipWei),
		GasFeeCap: new(big.Int).Set(maxFeeWei),
		Gas:       21_000,
		To:        &wallet,
		Value:     big.NewInt(0),
		Data:      nil,
	}), nil
}

// TryUnfreezeAmbiguousNonce enforces AT-07: an ambiguous/frozen nonce generation may ONLY
// be archived/unfrozen when finalized_nonce > claimNonce AND the original tx_hash is proven
// not on-chain (e.g., superseded by a finalized same-nonce 0-value Cancel tx).
func (s *Store) TryUnfreezeAmbiguousNonce(
	ctx context.Context,
	wallet common.Address,
	claimNonce uint64,
	finalizedOnChainNonce uint64,
	originalTxOnChain bool,
	finalizedWinningTxHash common.Hash,
	now time.Time,
) (bool, error) {
	if s == nil || s.db == nil || wallet == (common.Address{}) {
		return false, errors.New("invalid unfreeze parameters")
	}
	if originalTxOnChain || finalizedOnChainNonce <= claimNonce {
		return false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	nowStr := now.UTC().Format(time.RFC3339Nano)
	winHex := strings.ToLower(finalizedWinningTxHash.Hex())
	walletHex := strings.ToLower(wallet.Hex())

	if finalizedWinningTxHash != (common.Hash{}) {
		// U-P1-2: Verify winning tx_hash is NOT an initial/speed_up swap attempt when caller claims originalTxOnChain==false.
		var winKind string
		err := tx.QueryRowContext(ctx,
			`SELECT attempt_kind FROM trade_step_attempts WHERE wallet_address = ? AND nonce = ? AND tx_hash = ?`,
			walletHex, claimNonce, winHex,
		).Scan(&winKind)
		if err == nil && winKind != "cancel" {
			return false, errors.New("winning tx at nonce is a non-cancel swap attempt; cannot archive as cancelled")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE trade_step_attempts SET state = 'FINALIZED', updated_at = ?
			 WHERE wallet_address = ? AND nonce = ? AND tx_hash = ?`,
			nowStr, walletHex, claimNonce, winHex,
		); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE trade_step_attempts SET state = 'SUPERSEDED', updated_at = ?
		 WHERE wallet_address = ? AND nonce = ? AND tx_hash != ? AND state NOT IN ('FINALIZED', 'SUPERSEDED')`,
		nowStr, walletHex, claimNonce, winHex,
	); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
