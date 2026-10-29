package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
)

// ExecutionSnapshot is a read-only projection of durable execution state.
// It deliberately contains no raw transaction, ciphertext, signing material,
// permit or recovery command. The intake status on Record remains unchanged.
type ExecutionSnapshot struct {
	OperationStatus string `json:"operation_status"`
	StepStatus      string `json:"step_status"`
	TradeStepStatus string `json:"trade_step_status,omitempty"`
	AttemptStatus   string `json:"attempt_status,omitempty"`
	SubmissionState string `json:"submission_state,omitempty"`
	TxHash          string `json:"tx_hash,omitempty"`
	ReceiptState    string `json:"receipt_state,omitempty"`
	EffectState     string `json:"position_effect_state,omitempty"`
}

func (s *Store) manualExecutionSnapshot(ctx context.Context, id Identity, request string) (*ExecutionSnapshot, error) {
	if s == nil || s.db == nil || ctx == nil || !id.valid() || !validID(request) {
		return nil, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var result ExecutionSnapshot
	var operation, step, address string
	err = tx.QueryRowContext(ctx, `SELECT m.operation_id,m.step_id,m.wallet_address,o.status,s.status
		FROM manual_http_order_sources m
		JOIN operations o ON o.id=m.operation_id AND o.wallet_id=m.wallet_id AND o.chain_id=4663
		JOIN execution_steps s ON s.id=m.step_id AND s.operation_id=m.operation_id AND s.wallet_id=m.wallet_id
		WHERE m.id=? AND m.principal_id=? AND m.wallet_id=? AND m.source='MANUAL' AND m.chain='robinhood'`,
		request, id.PrincipalID, id.WalletID).Scan(&operation, &step, &address, &result.OperationStatus, &result.StepStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if operation != "manual_op_"+request || step != "manual_step_"+request {
		return nil, ErrConflict
	}
	if !common.IsHexAddress(address) || common.HexToAddress(address) != common.HexToAddress(id.WalletAddress) {
		return nil, ErrNotFound
	}
	if err := readManualExecutionHistoryTx(ctx, tx, id, request, operation, step, &result); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &result, nil
}

// Follow retained immutable bindings, never "the latest" step/attempt. Missing
// stages may be omitted only when no dependent durable work exists. No current
// authorization, clock, signing key, cipher or permission view is consulted.
func readManualExecutionHistoryTx(ctx context.Context, tx *sql.Tx, id Identity, request, operation, validationStep string, result *ExecutionSnapshot) error {
	b, err := scanManualExecutionBinding(tx.QueryRowContext(ctx, `SELECT `+manualExecutionBindingColumns+` FROM manual_execution_bindings WHERE operation_id=?`, operation))
	if errors.Is(err, sql.ErrNoRows) {
		return manualHistoryAbsent(ctx, tx, `SELECT
 EXISTS(SELECT 1 FROM execution_steps WHERE operation_id=? AND id!=?)
 OR EXISTS(SELECT 1 FROM manual_nonce_claims WHERE operation_id=?)
 OR EXISTS(SELECT 1 FROM manual_signed_artifact_commands WHERE operation_id=?)
 OR EXISTS(SELECT 1 FROM manual_first_submission_bindings WHERE operation_id=?)`, operation, validationStep, operation, operation, operation)
	}
	if err != nil {
		return err
	}
	cmd, err := scanManualCommand(tx.QueryRowContext(ctx, `SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE operation_id=?`, operation))
	if err != nil {
		return ErrConflict
	}
	c, err := verifyManualCommandTx(ctx, tx, cmd)
	if err != nil {
		return err
	}
	if b.Request != request || b != executionBindingFor(c, cmd, b.CheckedAt) || c.PrincipalID != id.PrincipalID || c.WalletID != id.WalletID || !strings.EqualFold(c.WalletAddress, id.WalletAddress) {
		return ErrConflict
	}
	if err := manualHistoryAbsent(ctx, tx, `SELECT NOT EXISTS(
 SELECT 1 FROM canary_runtime_audit a JOIN canary_alert_outbox o ON o.audit_id=a.id
 WHERE a.id='manual-execution:'||? AND a.event_type='MANUAL_EXECUTION_BOUND' AND a.operation_id=? AND a.attempt_id IS NULL
 AND a.authorization_id=? AND a.authorization_epoch=? AND a.reason_code='BOUND_NOT_SIGNED_NOT_PERMISSION' AND a.created_at=?
 AND a.details_json=json_object('request_id',?,'execution_step_id',?,'contract_hash',?,'reservation_id',?)
 AND o.id='manual-execution-alert:'||? AND o.severity='INFO' AND o.created_at=a.created_at)
 OR (SELECT COUNT(*) FROM canary_runtime_audit WHERE operation_id=? AND event_type='MANUAL_EXECUTION_BOUND')!=1`,
		operation, operation, b.Scope.AuthorizationID, b.Scope.AuthorizationEpoch, b.CheckedAt, request, b.ExecutionStep, b.ContractHash, cmd.Reservation, operation, operation); err != nil {
		return err
	}
	if err := manualHistoryAbsent(ctx, tx, `SELECT
 EXISTS(SELECT 1 FROM execution_steps WHERE operation_id=? AND id NOT IN (?,?))
 OR NOT EXISTS(SELECT 1 FROM execution_steps WHERE id=? AND operation_id=? AND wallet_id=? AND step_index=1 AND kind='manual_trade_execution' AND parameters_json=? AND route_json IS NULL AND unsigned_call_json IS NULL AND created_at=? AND policy_version=? AND quote_block_number=? AND quote_block_hash=? AND expires_at=?)`,
		operation, validationStep, b.ExecutionStep, b.ExecutionStep, operation, id.WalletID, cmd.ContractRaw, b.CheckedAt, b.PolicyVersion, b.QuoteBlock, b.QuoteHash, b.ExpiresAt); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT status FROM execution_steps WHERE id=?`, b.ExecutionStep).Scan(&result.TradeStepStatus); err != nil {
		return err
	}
	q, err := readManualNonceClaim(ctx, tx, request)
	if errors.Is(err, sql.ErrNoRows) {
		if result.TradeStepStatus != "manual_execution_bound" {
			return ErrConflict
		}
		return manualHistoryAbsent(ctx, tx, `SELECT
 EXISTS(SELECT 1 FROM transaction_attempts WHERE step_id=?)
 OR EXISTS(SELECT 1 FROM manual_signed_artifact_commands WHERE operation_id=?)
 OR EXISTS(SELECT 1 FROM manual_first_submission_bindings WHERE operation_id=?)`, b.ExecutionStep, operation, operation)
	}
	if err != nil {
		return err
	}
	if q.Operation != operation || q.Step != b.ExecutionStep || q.Attempt != "manual-nonce:"+operation || q.Wallet != id.WalletID || q.Address != strings.ToLower(b.Address) || q.ContractHash != b.ContractHash || q.Reservation != cmd.Reservation {
		return ErrConflict
	}
	if _, err := manualnonce.Verify(q.ObservationRaw, q.ObservationHash, q.Nonce, manualNonceFacts(c, b), q.CheckedAt); err != nil {
		return ErrConflict
	}
	if err := manualHistoryAbsent(ctx, tx, `SELECT NOT EXISTS(
 SELECT 1 FROM canary_runtime_audit a JOIN canary_alert_outbox o ON o.audit_id=a.id
 WHERE a.id='manual-nonce-audit:'||? AND a.event_type='MANUAL_NONCE_CLAIMED' AND a.operation_id=? AND a.attempt_id=?
 AND a.authorization_id=? AND a.authorization_epoch=? AND a.reason_code='NONCE_CLAIMED_NOT_SIGNED_NOT_PERMISSION' AND a.created_at=?
 AND a.details_json=json_object('execution_step_id',?,'nonce',?,'contract_hash',?,'reservation_id',?,'observation_hash',?)
 AND o.id='manual-nonce-alert:'||? AND o.severity='INFO' AND o.created_at=a.created_at)
 OR (SELECT COUNT(*) FROM canary_runtime_audit WHERE operation_id=? AND event_type='MANUAL_NONCE_CLAIMED')!=1`,
		operation, operation, q.Attempt, b.Scope.AuthorizationID, b.Scope.AuthorizationEpoch, q.CheckedAt, q.Step, q.Nonce, q.ContractHash, q.Reservation, q.ObservationHash, operation, operation); err != nil {
		return err
	}
	if err := manualHistoryAbsent(ctx, tx, `SELECT EXISTS(SELECT 1 FROM transaction_attempts t JOIN execution_steps s ON s.id=t.step_id WHERE s.operation_id=? AND t.id!=?)`, operation, q.Attempt); err != nil {
		return err
	}
	var factsRaw, factsHash, envelopeRaw, artifactHash string
	err = tx.QueryRowContext(ctx, `SELECT facts_json,facts_hash,envelope_json FROM manual_signed_artifact_commands WHERE operation_id=?`, operation).Scan(&factsRaw, &factsHash, &envelopeRaw)
	expected := "manual_nonce_expected_attempt"
	if err == nil {
		f := manualArtifactFacts(c, b, q)
		raw, digest, err := f.Canonical()
		if err != nil || factsRaw != raw || factsHash != digest {
			return ErrConflict
		}
		metadata, _, err := manualartifact.DecodeEnvelopeMetadata(envelopeRaw, f)
		if err != nil {
			return ErrConflict
		}
		artifactHash = metadata.RawSHA256
		if err := manualHistoryAbsent(ctx, tx, `SELECT NOT EXISTS(
 SELECT 1 FROM manual_signed_artifact_commands m JOIN manual_signing_intents i ON i.operation_id=m.operation_id
 AND i.owner_id=m.owner_id AND i.facts_hash=m.facts_hash AND i.facts_json=m.facts_json
 JOIN canary_runtime_audit a ON a.id='manual-artifact:'||m.operation_id AND a.event_type='MANUAL_ARTIFACT_STORED'
 AND a.operation_id=m.operation_id AND a.attempt_id=? AND a.authorization_id=? AND a.authorization_epoch=?
 AND a.reason_code='ENCRYPTED_ARTIFACT_NOT_PERMISSION' AND a.created_at=m.checked_at
 AND a.details_json=json_object('owner_id',m.owner_id,'facts_hash',m.facts_hash,'envelope_hash',manual_artifact_bytes_sha256(hex(m.envelope_json)),
 'execution_step_id',?,'observation_hash',m.observation_hash,'nonce',?)
 JOIN canary_alert_outbox o ON o.id='manual-artifact-alert:'||m.operation_id AND o.audit_id=a.id AND o.severity='INFO' AND o.created_at=a.created_at
 WHERE m.operation_id=?)
 OR (SELECT COUNT(*) FROM canary_runtime_audit WHERE operation_id=? AND event_type='MANUAL_ARTIFACT_STORED')!=1`,
			q.Attempt, b.Scope.AuthorizationID, b.Scope.AuthorizationEpoch, q.Step, q.Nonce, operation, operation); err != nil {
			return err
		}
		expected = "manual_artifact_expected_attempt"
	} else if errors.Is(err, sql.ErrNoRows) {
		if result.TradeStepStatus != "nonce_reserved" && result.TradeStepStatus != "signing" && result.TradeStepStatus != "manual_resolution" {
			return ErrConflict
		}
	} else {
		return err
	}
	var hash sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT t.status,t.tx_hash FROM transaction_attempts t JOIN manual_nonce_actual_attempt actual ON actual.id=t.id
 JOIN `+expected+` e ON e.operation_id=? AND e.attempt_id=t.id AND actual.attempt_json=e.attempt_json
 WHERE t.id=? AND t.step_id=? AND t.wallet_id=?`, operation, q.Attempt, q.Step, id.WalletID).Scan(&result.AttemptStatus, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	result.TxHash = hash.String
	var submission string
	err = tx.QueryRowContext(ctx, `SELECT submission_id FROM manual_first_submission_bindings WHERE operation_id=? AND attempt_id=? AND tx_hash=? AND artifact_hash=? AND authorization_id=? AND authorization_epoch=?`,
		operation, q.Attempt, result.TxHash, artifactHash, b.Scope.AuthorizationID, b.Scope.AuthorizationEpoch).Scan(&submission)
	if errors.Is(err, sql.ErrNoRows) {
		if artifactHash != "" && result.TradeStepStatus != "signed" {
			return ErrConflict
		}
		return manualHistoryAbsent(ctx, tx, `SELECT
 EXISTS(SELECT 1 FROM manual_first_submission_bindings WHERE operation_id=?)
 OR EXISTS(SELECT 1 FROM transaction_submissions WHERE attempt_id=?)
 OR EXISTS(SELECT 1 FROM receipt_observations WHERE attempt_id=?)
 OR EXISTS(SELECT 1 FROM position_effects WHERE attempt_id=?)`, operation, q.Attempt, q.Attempt, q.Attempt)
	}
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT submission_state FROM manual_submission_history_source WHERE operation_id=? AND attempt_id=? AND submission_id=? AND tx_hash=?`, operation, q.Attempt, submission, result.TxHash).Scan(&result.SubmissionState)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	return readManualRecoveryHistoryTx(ctx, tx, q.Attempt, result)
}

func manualHistoryAbsent(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrConflict
	}
	return nil
}

func readManualRecoveryHistoryTx(ctx context.Context, tx *sql.Tx, attempt string, result *ExecutionSnapshot) error {
	err := tx.QueryRowContext(ctx, `SELECT canonical_state,effect_state FROM manual_recovery_current WHERE attempt_id=?`, attempt).Scan(&result.ReceiptState, &result.EffectState)
	if errors.Is(err, sql.ErrNoRows) {
		return manualHistoryAbsent(ctx, tx, `SELECT
 EXISTS(SELECT 1 FROM receipt_observations WHERE attempt_id=?)
 OR EXISTS(SELECT 1 FROM position_effects WHERE attempt_id=?)
 OR EXISTS(SELECT 1 FROM manual_recovery_commands c JOIN manual_first_submission_bindings b ON b.operation_id=c.operation_id WHERE b.attempt_id=?)`, attempt, attempt, attempt)
	}
	if err != nil {
		return err
	}
	// A current result must not hide an extra arbitrary receipt or an effect
	// attached to another wallet/transaction. Every observation has a retained
	// immutable source command, including observations preceding a later reorg.
	return manualHistoryAbsent(ctx, tx, `SELECT
 EXISTS(SELECT 1 FROM receipt_observations r JOIN manual_first_submission_bindings b ON b.attempt_id=r.attempt_id
 WHERE r.attempt_id=? AND (r.chain_id!=4663 OR r.tx_hash IS NOT b.tx_hash OR NOT EXISTS(
 SELECT 1 FROM manual_recovery_commands c WHERE c.operation_id=b.operation_id AND c.kind='OBSERVE'
 AND c.receipt_id=r.id AND c.tx_hash=r.tx_hash AND c.block_hash=r.block_hash AND c.block_number=r.block_number AND c.receipt_status=r.receipt_status)))
 OR EXISTS(SELECT 1 FROM position_effects p JOIN manual_first_submission_bindings b ON b.attempt_id=p.attempt_id
 JOIN manual_execution_bindings e ON e.operation_id=b.operation_id LEFT JOIN receipt_observations r ON r.id=p.receipt_id
 WHERE p.attempt_id=? AND (p.wallet_id IS NOT e.wallet_id OR r.id IS NULL OR r.attempt_id IS NOT p.attempt_id OR r.tx_hash IS NOT b.tx_hash
 OR (p.state='active' AND r.canonical_state!='canonical_success') OR (p.state='rolled_back' AND r.canonical_state!='orphaned')))`, attempt, attempt)
}
