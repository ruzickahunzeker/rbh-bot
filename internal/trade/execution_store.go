package trade

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type ExecutionReservation struct {
	OperationID string
	StepID      string
	AttemptID   string
	WalletID    string
	Wallet      common.Address
	Nonce       uint64
	Request     DryRunRequest
	DryRun      DryRunResult
	Duplicate   bool
}

type encryptedArtifact struct {
	SignedArtifact
	Ciphertext      []byte
	EncryptionNonce []byte
}

func (s *Store) ExecutionCandidate(ctx context.Context, operation, walletID string) (DryRunRequest, DryRunResult, common.Address, error) {
	if s == nil || s.db == nil || ctx == nil || operation == "" || walletID == "" {
		return DryRunRequest{}, DryRunResult{}, common.Address{}, ErrInvalidRequest
	}
	var requestJSON, storedFingerprint, walletAddress string
	if err := s.db.QueryRowContext(ctx, `SELECT request_json,request_fingerprint FROM operations WHERE id=? AND wallet_id=? AND status IN ('dry_run_succeeded','execution_prepared','signed_prebroadcast')`, operation, walletID).Scan(&requestJSON, &storedFingerprint); err != nil {
		return DryRunRequest{}, DryRunResult{}, common.Address{}, fmt.Errorf("execution candidate: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT address FROM dry_run_wallets WHERE id=? AND enabled=1`, walletID).Scan(&walletAddress); err != nil {
		return DryRunRequest{}, DryRunResult{}, common.Address{}, err
	}
	var request DryRunRequest
	if err := json.Unmarshal([]byte(requestJSON), &request); err != nil || request.Validate() != nil {
		return DryRunRequest{}, DryRunResult{}, common.Address{}, ErrInvalidRequest
	}
	fingerprint, err := request.Fingerprint()
	if err != nil || fingerprint != storedFingerprint || operationID(request) != operation || request.Intent.PolicyVersion == 0 {
		return DryRunRequest{}, DryRunResult{}, common.Address{}, ErrIdempotencyConflict
	}
	result, found, err := s.Result(ctx, operation)
	if err != nil || !found || result.Status != "success" {
		return DryRunRequest{}, DryRunResult{}, common.Address{}, fmt.Errorf("execution candidate dry-run: %w", err)
	}
	return request, result, common.HexToAddress(walletAddress), nil
}

func (s *Store) ExpiryBinding(ctx context.Context, operation string) (time.Time, error) {
	if s == nil || s.db == nil || ctx == nil || operation == "" {
		return time.Time{}, ErrTTLUnverifiable
	}
	var capability, expiresAt string
	if err := s.db.QueryRowContext(ctx, `SELECT deadline_capability,expires_at FROM operations WHERE id=?`, operation).Scan(&capability, &expiresAt); err != nil || capability != "APPLICATION_TTL_ONLY" {
		return time.Time{}, ErrTTLUnverifiable
	}
	return parseCanonicalExpiry(expiresAt)
}

func (s *Store) CheckOperationTTL(ctx context.Context, operation string, now time.Time) error {
	expires, err := s.ExpiryBinding(ctx, operation)
	if err != nil || now.IsZero() {
		return ErrTTLUnverifiable
	}
	if !now.UTC().Before(expires) {
		return ErrTTLExpired
	}
	return nil
}

func executionStepID(operation string) string { return deterministicID(operation, "execution", "1") }
func attemptID(step string) string            { return deterministicID(step, "attempt", "0") }

func deterministicID(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func artifactAAD(operation, step, attempt string) []byte {
	return []byte("rbh:4663:" + operation + ":" + step + ":" + attempt)
}

func (s *Store) ReserveExecution(ctx context.Context, operation, walletID string, signer common.Address, nonce uint64, inputAsset, inputAmount, gasBudget string, now time.Time) (ExecutionReservation, error) {
	if s == nil || s.db == nil || ctx == nil || operation == "" || walletID == "" || signer == (common.Address{}) || now.IsZero() {
		return ExecutionReservation{}, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExecutionReservation{}, err
	}
	defer tx.Rollback()
	var requestJSON, storedFingerprint, walletAddress string
	if err := tx.QueryRowContext(ctx, `SELECT request_json,request_fingerprint FROM operations WHERE id=? AND wallet_id=? AND status IN ('dry_run_succeeded','execution_prepared','signed_prebroadcast')`, operation, walletID).Scan(&requestJSON, &storedFingerprint); err != nil {
		return ExecutionReservation{}, fmt.Errorf("execution operation admission: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT address FROM dry_run_wallets WHERE id=? AND enabled=1`, walletID).Scan(&walletAddress); err != nil {
		return ExecutionReservation{}, fmt.Errorf("execution wallet admission: %w", err)
	}
	if common.HexToAddress(walletAddress) != signer {
		return ExecutionReservation{}, ErrWrongSigner
	}
	var request DryRunRequest
	if err := json.Unmarshal([]byte(requestJSON), &request); err != nil || request.Validate() != nil {
		return ExecutionReservation{}, ErrInvalidRequest
	}
	fingerprint, err := request.Fingerprint()
	if err != nil || fingerprint != storedFingerprint || operationID(request) != operation || request.Intent.PolicyVersion == 0 {
		return ExecutionReservation{}, ErrIdempotencyConflict
	}
	dryRun, found, err := resultTx(ctx, tx, operation)
	if err != nil || !found || dryRun.Status != "success" {
		return ExecutionReservation{}, fmt.Errorf("execution requires successful dry-run: %w", err)
	}
	step, attempt := executionStepID(operation), attemptID(executionStepID(operation))
	var existingNonce string
	err = tx.QueryRowContext(ctx, `SELECT nonce FROM transaction_attempts WHERE id=?`, attempt).Scan(&existingNonce)
	if err == nil {
		parsed, parseErr := strconv.ParseUint(existingNonce, 10, 64)
		if parseErr != nil || parsed != nonce {
			return ExecutionReservation{}, ErrNonceConflict
		}
		if err := tx.Commit(); err != nil {
			return ExecutionReservation{}, err
		}
		return ExecutionReservation{OperationID: operation, StepID: step, AttemptID: attempt, WalletID: walletID, Wallet: signer, Nonce: nonce, Request: request, DryRun: dryRun, Duplicate: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ExecutionReservation{}, err
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO execution_wallet_lanes(wallet_id,address,state,updated_at) VALUES(?,?,'idle',?) ON CONFLICT(wallet_id) DO NOTHING`, walletID, signer.Hex(), stamp); err != nil {
		return ExecutionReservation{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO execution_steps(id,operation_id,step_index,kind,status,created_at,updated_at,wallet_id,policy_version,quote_block_number,quote_block_hash,expires_at) VALUES(?,?,1,'pons_curve_execution','nonce_reserved',?,?,?,?,?,?,?)`, step, operation, stamp, stamp, walletID, request.Intent.PolicyVersion, dryRun.Block.Number, dryRun.Block.Hash.Hex(), request.Intent.ExpiresAt); err != nil {
		if isUniqueConstraint(err) {
			return ExecutionReservation{}, ErrWalletLaneBusy
		}
		return ExecutionReservation{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE execution_wallet_lanes SET state='reserved',operation_id=?,step_id=?,reserved_nonce=?,freeze_reason=NULL,updated_at=? WHERE wallet_id=? AND address=? AND state='idle'`, operation, step, strconv.FormatUint(nonce, 10), stamp, walletID, signer.Hex())
	if err != nil {
		return ExecutionReservation{}, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ExecutionReservation{}, ErrWalletLaneBusy
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO execution_reservations(operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at) VALUES(?,?,?,?,?,?,'reserved',?,?)`, operation, step, walletID, inputAsset, inputAmount, gasBudget, stamp, stamp); err != nil {
		return ExecutionReservation{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at,chain_id,wallet_id,updated_at,policy_version,quote_block_number,quote_block_hash,expires_at) VALUES(?,?,?,'prepared',?,4663,?,?,?,?,?,?)`, attempt, step, strconv.FormatUint(nonce, 10), stamp, walletID, stamp, request.Intent.PolicyVersion, dryRun.Block.Number, dryRun.Block.Hash.Hex(), request.Intent.ExpiresAt); err != nil {
		return ExecutionReservation{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operations SET status='execution_prepared',updated_at=? WHERE id=?`, stamp, operation); err != nil {
		return ExecutionReservation{}, err
	}
	if err := tx.Commit(); err != nil {
		return ExecutionReservation{}, err
	}
	return ExecutionReservation{OperationID: operation, StepID: step, AttemptID: attempt, WalletID: walletID, Wallet: signer, Nonce: nonce, Request: request, DryRun: dryRun}, nil
}

func (s *Store) CommitSigned(ctx context.Context, reservation ExecutionReservation, artifact SignedArtifact, ciphertext, encryptionNonce []byte, now time.Time) error {
	if s == nil || s.db == nil || len(ciphertext) == 0 || len(encryptionNonce) == 0 || artifact.KeyVersion == "" || artifact.AttemptID != reservation.AttemptID || artifact.Nonce != reservation.Nonce {
		return ErrArtifactIntegrity
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE transaction_attempts SET status='signed',tx_hash=?,raw_tx_hash=?,key_version=?,encryption_nonce=?,encrypted_raw_tx=?,from_address=?,to_address=?,value=?,calldata=?,gas_limit=?,gas_tip_cap=?,gas_fee_cap=?,updated_at=? WHERE id=? AND step_id=? AND nonce=? AND status='prepared' AND policy_version=? AND quote_block_number=? AND quote_block_hash=? AND expires_at=?`, artifact.TxHash, artifact.TxHash, artifact.KeyVersion, encryptionNonce, ciphertext, artifact.From, artifact.To, artifact.Value, artifact.Data, strconv.FormatUint(artifact.GasLimit, 10), artifact.GasTipCap, artifact.GasFeeCap, stamp, artifact.AttemptID, artifact.StepID, strconv.FormatUint(artifact.Nonce, 10), artifact.PolicyVersion, artifact.QuoteBlockNumber, artifact.QuoteBlockHash, artifact.ExpiresAt)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrArtifactIntegrity
	}
	if _, err := tx.ExecContext(ctx, `UPDATE execution_steps SET status='signed',updated_at=? WHERE id=?`, stamp, artifact.StepID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE execution_reservations SET status='signed',updated_at=? WHERE operation_id=?`, stamp, artifact.Operation); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE execution_wallet_lanes SET state='signed',updated_at=? WHERE wallet_id=? AND operation_id=? AND reserved_nonce=?`, stamp, artifact.WalletID, artifact.Operation, strconv.FormatUint(artifact.Nonce, 10)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operations SET status='signed_prebroadcast',updated_at=? WHERE id=?`, stamp, artifact.Operation); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) LoadEncryptedArtifact(ctx context.Context, operation string) (encryptedArtifact, bool, error) {
	var value encryptedArtifact
	var nonceText, gasText string
	err := s.db.QueryRowContext(ctx, `SELECT a.id,a.step_id,a.wallet_id,a.key_version,a.nonce,a.tx_hash,a.from_address,a.to_address,a.value,a.calldata,a.gas_limit,a.gas_tip_cap,a.gas_fee_cap,a.encrypted_raw_tx,a.encryption_nonce,a.policy_version,a.quote_block_number,a.quote_block_hash,a.expires_at FROM transaction_attempts a JOIN execution_steps s ON s.id=a.step_id JOIN operations o ON o.id=s.operation_id WHERE s.operation_id=? AND a.status='signed' AND a.policy_version=o.policy_version AND a.expires_at=o.expires_at AND a.policy_version=s.policy_version AND a.quote_block_number=s.quote_block_number AND a.quote_block_hash=s.quote_block_hash AND a.expires_at=s.expires_at`, operation).Scan(&value.AttemptID, &value.StepID, &value.WalletID, &value.KeyVersion, &nonceText, &value.TxHash, &value.From, &value.To, &value.Value, &value.Data, &gasText, &value.GasTipCap, &value.GasFeeCap, &value.Ciphertext, &value.EncryptionNonce, &value.PolicyVersion, &value.QuoteBlockNumber, &value.QuoteBlockHash, &value.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return encryptedArtifact{}, false, nil
	}
	if err != nil {
		return encryptedArtifact{}, false, err
	}
	value.Operation = operation
	value.Nonce, err = strconv.ParseUint(nonceText, 10, 64)
	if err != nil {
		return encryptedArtifact{}, false, ErrArtifactIntegrity
	}
	value.GasLimit, err = strconv.ParseUint(gasText, 10, 64)
	if err != nil {
		return encryptedArtifact{}, false, ErrArtifactIntegrity
	}
	return value, true, nil
}

func (s *Store) ExpirePreBroadcast(ctx context.Context, a SignedArtifact, submission *SubmissionRecord, now time.Time) error {
	if s == nil || s.db == nil || ctx == nil || now.IsZero() {
		return ErrTradeStore
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(time.RFC3339Nano)
	if submission == nil {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM transaction_submissions WHERE attempt_id=?`, a.AttemptID).Scan(&count); err != nil || count != 0 {
			return ErrBroadcastAmbiguous
		}
	} else {
		result, updateErr := tx.ExecContext(ctx, `UPDATE transaction_submissions SET state='expired_prebroadcast',failure_class='ttl_expired_pre_send',failure_detail='application TTL expired before SendRawTransaction',updated_at=? WHERE id=? AND attempt_id=? AND state='submitting'`, stamp, submission.ID, a.AttemptID)
		if updateErr != nil {
			return updateErr
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrBroadcastAmbiguous
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE transaction_attempts SET status='expired_prebroadcast',updated_at=? WHERE id=? AND status IN ('prepared','signed')`, stamp, a.AttemptID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE execution_steps SET status='expired_prebroadcast',updated_at=? WHERE id=?`, stamp, a.StepID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE execution_reservations SET status='released',updated_at=? WHERE operation_id=? AND status IN ('reserved','signed')`, stamp, a.Operation); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE execution_wallet_lanes SET state='idle',operation_id=NULL,step_id=NULL,reserved_nonce=NULL,freeze_reason='ttl_expired_prebroadcast',updated_at=? WHERE wallet_id=? AND operation_id=? AND state IN ('reserved','signed')`, stamp, a.WalletID, a.Operation); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE operations SET status='expired_prebroadcast',failure_code='TTL_EXPIRED',updated_at=? WHERE id=?`, stamp, a.Operation); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FreezeExecution(ctx context.Context, walletID, operation, reason string, now time.Time) error {
	if reason == "" {
		reason = "execution_state_unknown"
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE execution_wallet_lanes SET state='frozen',freeze_reason=?,updated_at=? WHERE wallet_id=? AND operation_id=?`, reason, stamp, walletID, operation); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE execution_reservations SET status='frozen',updated_at=? WHERE operation_id=?`, stamp, operation); err != nil {
		return err
	}
	return tx.Commit()
}

func resultTx(ctx context.Context, tx *sql.Tx, operation string) (DryRunResult, bool, error) {
	var result DryRunResult
	var routeJSON, parametersJSON, callJSON string
	var blockNumber sql.NullInt64
	var blockHash, returnData sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT r.operation_id,r.step_id,r.status,r.block_number,r.block_hash,r.return_data,COALESCE(s.route_json,'{}'),COALESCE(s.parameters_json,'{}'),COALESCE(s.unsigned_call_json,'{}') FROM dry_run_results r JOIN execution_steps s ON s.id=r.step_id WHERE r.operation_id=?`, operation).Scan(&result.OperationID, &result.StepID, &result.Status, &blockNumber, &blockHash, &returnData, &routeJSON, &parametersJSON, &callJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return DryRunResult{}, false, nil
	}
	if err != nil {
		return DryRunResult{}, false, err
	}
	_ = json.Unmarshal([]byte(routeJSON), &result.Route)
	_ = json.Unmarshal([]byte(parametersJSON), &result.Parameters)
	_ = json.Unmarshal([]byte(callJSON), &result.UnsignedCall)
	if blockNumber.Valid {
		result.Block.Number = uint64(blockNumber.Int64)
	}
	if blockHash.Valid {
		result.Block.Hash = common.HexToHash(blockHash.String)
	}
	result.ReturnData = returnData.String
	return result, true, nil
}
