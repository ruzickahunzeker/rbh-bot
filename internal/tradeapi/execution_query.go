package tradeapi

import (
	"context"
	"database/sql"
	"errors"
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
	var operation, step string
	err = tx.QueryRowContext(ctx, `SELECT m.operation_id,m.step_id,o.status,s.status
		FROM manual_http_order_sources m
		JOIN operations o ON o.id=m.operation_id AND o.wallet_id=m.wallet_id AND o.chain_id=4663
		JOIN execution_steps s ON s.id=m.step_id AND s.operation_id=m.operation_id AND s.wallet_id=m.wallet_id
		WHERE m.id=? AND m.principal_id=? AND m.wallet_id=? AND m.source='MANUAL' AND m.chain='robinhood'`,
		request, id.PrincipalID, id.WalletID).Scan(&operation, &step, &result.OperationStatus, &result.StepStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if operation != "manual_op_"+request || step != "manual_step_"+request {
		return nil, ErrConflict
	}
	var tradeStep string
	err = tx.QueryRowContext(ctx, `SELECT id,status FROM execution_steps WHERE operation_id=? AND wallet_id=? AND kind='manual_trade_execution' ORDER BY step_index DESC LIMIT 1`, operation, id.WalletID).Scan(&tradeStep, &result.TradeStepStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		step = tradeStep
	} else {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &result, nil
	}
	var attempt, attemptStatus string
	var txHash sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id,status,tx_hash FROM transaction_attempts WHERE step_id=? AND wallet_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, step, id.WalletID).Scan(&attempt, &attemptStatus, &txHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		result.AttemptStatus = attemptStatus
		result.TxHash = txHash.String
		if err = tx.QueryRowContext(ctx, `SELECT state FROM transaction_submissions WHERE attempt_id=? AND tx_hash=? ORDER BY sequence DESC LIMIT 1`, attempt, txHash.String).Scan(&result.SubmissionState); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT canonical_state FROM receipt_observations WHERE attempt_id=? AND tx_hash=? ORDER BY reconciliation_version DESC,updated_at DESC,id DESC LIMIT 1`, attempt, txHash.String).Scan(&result.ReceiptState); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT state FROM position_effects WHERE attempt_id=?`, attempt).Scan(&result.EffectState); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &result, nil
}
