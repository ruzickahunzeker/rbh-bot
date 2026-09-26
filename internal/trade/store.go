package trade

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type Store struct {
	db           *sql.DB
	recoveryHook func(string)
}

func (s *Store) SetRecoveryHookForTest(h func(string)) { s.recoveryHook = h }

type Admission struct {
	OperationID string
	StepID      string
	Wallet      common.Address
	Duplicate   bool
	Terminal    *DryRunResult
}

type SafetyCounts struct {
	Operations          int `json:"operations"`
	ExecutionSteps      int `json:"execution_steps"`
	DryRunResults       int `json:"dry_run_results"`
	TransactionAttempts int `json:"transaction_attempts"`
}

func NewStore(database *storage.Database) (*Store, error) {
	if database == nil {
		return nil, ErrTradeStore
	}
	db, err := database.SQLDB(storage.TradeOwner)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) RegisterDryRunWallet(ctx context.Context, id string, address common.Address) error {
	if s == nil || s.db == nil || ctx == nil || id == "" || address == (common.Address{}) {
		return ErrInvalidRequest
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO dry_run_wallets(id, chain_id, address, enabled) VALUES(?, 4663, ?, 1)
ON CONFLICT(id) DO UPDATE SET address=excluded.address, enabled=1`, id, address.Hex())
	if err != nil {
		return fmt.Errorf("register dry-run wallet: %w", err)
	}
	return nil
}

func (s *Store) Admit(ctx context.Context, request DryRunRequest, now time.Time) (Admission, error) {
	if s == nil || s.db == nil || ctx == nil || now.IsZero() {
		return Admission{}, ErrTradeStore
	}
	fingerprint, err := request.Fingerprint()
	if err != nil {
		return Admission{}, err
	}
	if err := request.CheckTTL(now); err != nil {
		return Admission{}, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return Admission{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Admission{}, fmt.Errorf("begin admission: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var walletAddress string
	if err := tx.QueryRowContext(ctx, `SELECT address FROM dry_run_wallets WHERE id=? AND chain_id=4663 AND enabled=1`, request.WalletID).Scan(&walletAddress); err != nil {
		return Admission{}, fmt.Errorf("resolve dry-run wallet: %w", err)
	}
	if !common.IsHexAddress(walletAddress) || common.HexToAddress(walletAddress) == (common.Address{}) {
		return Admission{}, ErrInvalidRequest
	}

	operation := operationID(request)
	step := stepID(operation)
	var existingID, existingFingerprint string
	err = tx.QueryRowContext(ctx, `
SELECT id, request_fingerprint FROM operations
WHERE chain_id=4663 AND wallet_id=? AND idempotency_key=?`, request.WalletID, request.Intent.IdempotencyKey).
		Scan(&existingID, &existingFingerprint)
	if err == nil {
		if existingID != operation || existingFingerprint != fingerprint {
			return Admission{}, ErrIdempotencyConflict
		}
		if err := tx.Commit(); err != nil {
			return Admission{}, err
		}
		committed = true
		terminal, found, err := s.Result(ctx, operation)
		if err != nil {
			return Admission{}, err
		}
		admission := Admission{OperationID: operation, StepID: step, Wallet: common.HexToAddress(walletAddress), Duplicate: true}
		if found {
			terminal.Duplicate = true
			admission.Terminal = &terminal
		}
		return admission, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Admission{}, fmt.Errorf("read admission: %w", err)
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO operations(id, chain_id, wallet_id, idempotency_key, request_fingerprint, kind, status, created_at, request_json, updated_at, policy_version, deadline_capability, expires_at)
VALUES(?, 4663, ?, ?, ?, 'swap', 'admitted', ?, ?, ?, ?, ?, ?)`, operation, request.WalletID, request.Intent.IdempotencyKey, fingerprint, stamp, string(encoded), stamp, request.Intent.PolicyVersion, request.Intent.DeadlineCapability, request.Intent.ExpiresAt); err != nil {
		if isUniqueConstraint(err) {
			return Admission{}, ErrIdempotencyConflict
		}
		return Admission{}, fmt.Errorf("insert operation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO execution_steps(id, operation_id, step_index, kind, status, created_at, updated_at)
VALUES(?, ?, 0, 'pons_curve_dry_run', 'created', ?, ?)`, step, operation, stamp, stamp); err != nil {
		return Admission{}, fmt.Errorf("insert execution step: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Admission{}, fmt.Errorf("commit admission: %w", err)
	}
	committed = true
	return Admission{OperationID: operation, StepID: step, Wallet: common.HexToAddress(walletAddress)}, nil
}

func (s *Store) MarkRunning(ctx context.Context, operation, step string, route CurveRoute, parameters ExecutionParameters, call UnsignedCall, block BlockRef, policyVersion uint64, expiresAt string, now time.Time) error {
	if s == nil || s.db == nil || ctx == nil {
		return ErrTradeStore
	}
	routeJSON, _ := json.Marshal(route)
	parametersJSON, _ := json.Marshal(parameters)
	callJSON, _ := json.Marshal(call)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE execution_steps SET status='simulating', route_json=?, parameters_json=?, unsigned_call_json=?, policy_version=?, quote_block_number=?, quote_block_hash=?, expires_at=?, updated_at=? WHERE id=? AND operation_id=?`, string(routeJSON), string(parametersJSON), string(callJSON), policyVersion, block.Number, block.Hash.Hex(), expiresAt, stamp, step, operation); err != nil {
		return fmt.Errorf("persist unsigned dry-run step: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operations SET status='executing', updated_at=? WHERE id=?`, stamp, operation); err != nil {
		return fmt.Errorf("mark operation executing: %w", err)
	}
	return tx.Commit()
}

func (s *Store) Complete(ctx context.Context, result DryRunResult, now time.Time) error {
	return s.finish(ctx, result, "dry_run_succeeded", "succeeded", now)
}

func (s *Store) FailClosed(ctx context.Context, result DryRunResult, now time.Time) error {
	return s.finish(ctx, result, "dry_run_failed", "failed", now)
}

func (s *Store) ExpireUnreserved(ctx context.Context, operation string, now time.Time) error {
	if s == nil || s.db == nil || ctx == nil || operation == "" || now.IsZero() {
		return ErrTradeStore
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var attempts int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM transaction_attempts a JOIN execution_steps s ON s.id=a.step_id WHERE s.operation_id=?`, operation).Scan(&attempts); err != nil || attempts != 0 {
		return ErrArtifactIntegrity
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE operations SET status='expired_prebroadcast',failure_code='TTL_EXPIRED',updated_at=? WHERE id=? AND status='dry_run_succeeded'`, stamp, operation)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrInvalidRequest
	}
	return tx.Commit()
}

func (s *Store) finish(ctx context.Context, result DryRunResult, operationStatus, stepStatus string, now time.Time) error {
	if s == nil || s.db == nil || ctx == nil || result.OperationID == "" || result.StepID == "" {
		return ErrTradeStore
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(time.RFC3339Nano)
	var blockNumber any
	var blockHash any
	if result.Block.Number != 0 {
		blockNumber = result.Block.Number
		blockHash = result.Block.Hash.Hex()
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO dry_run_results(operation_id, step_id, status, block_number, block_hash, return_data, failure_code, failure_detail, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(operation_id) DO NOTHING`, result.OperationID, result.StepID, result.Status, blockNumber, blockHash, result.ReturnData, nullText(result.FailureCode), nullText(result.FailureDetail), stamp); err != nil {
		return fmt.Errorf("insert dry-run result: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE execution_steps SET status=?, updated_at=? WHERE id=? AND operation_id=?`, stepStatus, stamp, result.StepID, result.OperationID); err != nil {
		return fmt.Errorf("finish execution step: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operations SET status=?, failure_code=?, updated_at=? WHERE id=?`, operationStatus, nullText(result.FailureCode), stamp, result.OperationID); err != nil {
		return fmt.Errorf("finish operation: %w", err)
	}
	return tx.Commit()
}

func (s *Store) Result(ctx context.Context, operation string) (DryRunResult, bool, error) {
	if s == nil || s.db == nil || ctx == nil {
		return DryRunResult{}, false, ErrTradeStore
	}
	var result DryRunResult
	var routeJSON, parametersJSON, callJSON string
	var blockNumber sql.NullInt64
	var blockHash, returnData, failureCode, failureDetail sql.NullString
	err := s.db.QueryRowContext(ctx, `
SELECT r.operation_id, r.step_id, r.status, r.block_number, r.block_hash, r.return_data, r.failure_code, r.failure_detail,
       COALESCE(s.route_json, '{}'), COALESCE(s.parameters_json, '{}'), COALESCE(s.unsigned_call_json, '{}')
FROM dry_run_results r JOIN execution_steps s ON s.id=r.step_id
WHERE r.operation_id=?`, operation).Scan(&result.OperationID, &result.StepID, &result.Status, &blockNumber, &blockHash, &returnData, &failureCode, &failureDetail, &routeJSON, &parametersJSON, &callJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return DryRunResult{}, false, nil
	}
	if err != nil {
		return DryRunResult{}, false, fmt.Errorf("read dry-run result: %w", err)
	}
	if err := json.Unmarshal([]byte(routeJSON), &result.Route); err != nil {
		return DryRunResult{}, false, err
	}
	if err := json.Unmarshal([]byte(parametersJSON), &result.Parameters); err != nil {
		return DryRunResult{}, false, err
	}
	if err := json.Unmarshal([]byte(callJSON), &result.UnsignedCall); err != nil {
		return DryRunResult{}, false, err
	}
	if blockNumber.Valid {
		result.Block.Number = uint64(blockNumber.Int64)
	}
	if blockHash.Valid {
		result.Block.Hash = common.HexToHash(blockHash.String)
	}
	result.ReturnData = returnData.String
	result.FailureCode = failureCode.String
	result.FailureDetail = failureDetail.String
	return result, true, nil
}

func (s *Store) Recoverable(ctx context.Context) ([]DryRunRequest, error) {
	if s == nil || s.db == nil || ctx == nil {
		return nil, ErrTradeStore
	}
	rows, err := s.db.QueryContext(ctx, `SELECT request_json FROM operations WHERE status IN ('admitted','executing') ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requests := make([]DryRunRequest, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var request DryRunRequest
		if err := json.Unmarshal([]byte(payload), &request); err != nil {
			return nil, fmt.Errorf("decode recoverable request: %w", err)
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (s *Store) Counts(ctx context.Context) (SafetyCounts, error) {
	if s == nil || s.db == nil || ctx == nil {
		return SafetyCounts{}, ErrTradeStore
	}
	var counts SafetyCounts
	for _, query := range []struct {
		name   string
		target *int
	}{
		{"operations", &counts.Operations},
		{"execution_steps", &counts.ExecutionSteps},
		{"dry_run_results", &counts.DryRunResults},
		{"transaction_attempts", &counts.TransactionAttempts},
	} {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+query.name).Scan(query.target); err != nil {
			return SafetyCounts{}, fmt.Errorf("count %s: %w", query.name, err)
		}
	}
	return counts, nil
}

func nullText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func isUniqueConstraint(err error) bool {
	return err != nil && (contains(err.Error(), "UNIQUE constraint failed") || contains(err.Error(), "constraint failed"))
}

func contains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
