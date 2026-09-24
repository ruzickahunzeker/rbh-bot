package trade

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type SubmissionRecord struct {
	ID, AttemptID, TxHash, State, RPCHash, FailureClass, FailureDetail string
	Sequence                                                           uint64
}

type ReceiptObservation struct {
	ID, AttemptID, TxHash, BlockHash, CanonicalState string
	BlockNumber                                      uint64
	Status                                           uint64
	Version                                          uint64
}

type PositionEffect struct{ ID, AttemptID, ReceiptID, WalletID, Asset, Delta, State string }

func (s *Store) BeginSubmission(ctx context.Context, a SignedArtifact, allowFrozen bool, now time.Time) (SubmissionRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubmissionRecord{}, err
	}
	defer tx.Rollback()
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM execution_wallet_lanes WHERE wallet_id=? AND operation_id=?`, a.WalletID, a.Operation).Scan(&state); err != nil || (state != "signed" && !(allowFrozen && state == "frozen")) {
		return SubmissionRecord{}, ErrWalletLaneBusy
	}
	var sequence uint64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence)+1,0) FROM transaction_submissions WHERE attempt_id=?`, a.AttemptID).Scan(&sequence); err != nil {
		return SubmissionRecord{}, err
	}
	id := deterministicID(a.AttemptID, "submission", fmt.Sprint(sequence))
	stamp := now.UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `INSERT INTO transaction_submissions(id,attempt_id,tx_hash,sequence,state,created_at,updated_at) VALUES(?,?,?,?, 'submitting',?,?)`, id, a.AttemptID, a.TxHash, sequence, stamp, stamp)
	if err != nil {
		return SubmissionRecord{}, err
	}
	if err = tx.Commit(); err != nil {
		return SubmissionRecord{}, err
	}
	return SubmissionRecord{ID: id, AttemptID: a.AttemptID, TxHash: a.TxHash, Sequence: sequence, State: "submitting"}, nil
}

func (s *Store) FinishSubmission(ctx context.Context, a SignedArtifact, sub SubmissionRecord, state, rpcHash, class, detail string, now time.Time) error {
	if state != "submitted" && state != "broadcast_unknown" && state != "manual_resolution" {
		return ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(time.RFC3339Nano)
	r, err := tx.ExecContext(ctx, `UPDATE transaction_submissions SET state=?,rpc_hash=?,failure_class=?,failure_detail=?,updated_at=? WHERE id=? AND attempt_id=? AND tx_hash=? AND state='submitting'`, state, nullText(rpcHash), nullText(class), nullText(detail), stamp, sub.ID, a.AttemptID, a.TxHash)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrArtifactIntegrity
	}
	if state == "broadcast_unknown" || state == "manual_resolution" {
		_, err = tx.ExecContext(ctx, `UPDATE execution_wallet_lanes SET state='frozen',freeze_reason=?,updated_at=? WHERE wallet_id=? AND operation_id=?`, state, stamp, a.WalletID, a.Operation)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE execution_reservations SET status='frozen',updated_at=? WHERE operation_id=?`, stamp, a.Operation)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE execution_steps SET status=? WHERE id=?`, map[bool]string{true: "manual_resolution", false: "broadcast_unknown"}[state == "manual_resolution"], a.StepID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LatestSubmission(ctx context.Context, attempt string) (SubmissionRecord, bool, error) {
	var v SubmissionRecord
	err := s.db.QueryRowContext(ctx, `SELECT id,attempt_id,tx_hash,sequence,state,COALESCE(rpc_hash,''),COALESCE(failure_class,''),COALESCE(failure_detail,'') FROM transaction_submissions WHERE attempt_id=? ORDER BY sequence DESC LIMIT 1`, attempt).Scan(&v.ID, &v.AttemptID, &v.TxHash, &v.Sequence, &v.State, &v.RPCHash, &v.FailureClass, &v.FailureDetail)
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}

func (s *Store) ObserveReceipt(ctx context.Context, a SignedArtifact, r ReceiptObservation, now time.Time) (ReceiptObservation, error) {
	if r.TxHash != a.TxHash || r.BlockHash == "" || r.BlockNumber == 0 || r.Status > 1 {
		return ReceiptObservation{}, ErrArtifactIntegrity
	}
	r.AttemptID = a.AttemptID
	r.ID = deterministicID(a.AttemptID, r.BlockHash)
	r.CanonicalState = "observed"
	r.Version = 1
	stamp := now.UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO receipt_observations(id,attempt_id,chain_id,tx_hash,block_number,block_hash,receipt_status,canonical_state,reconciliation_version,observed_at,updated_at) VALUES(?,?,4663,?,?,?,?, 'observed',1,?,?) ON CONFLICT(attempt_id,block_hash) DO NOTHING`, r.ID, a.AttemptID, a.TxHash, r.BlockNumber, r.BlockHash, r.Status, stamp, stamp)
	if err != nil {
		return ReceiptObservation{}, err
	}
	var txHash, blockHash string
	var blockNumber, status uint64
	if err = s.db.QueryRowContext(ctx, `SELECT tx_hash,block_number,block_hash,receipt_status FROM receipt_observations WHERE id=?`, r.ID).Scan(&txHash, &blockNumber, &blockHash, &status); err != nil {
		return ReceiptObservation{}, err
	}
	if txHash != r.TxHash || blockNumber != r.BlockNumber || blockHash != r.BlockHash || status != r.Status {
		return ReceiptObservation{}, ErrArtifactIntegrity
	}
	return r, nil
}

func (s *Store) Canonicalize(ctx context.Context, a SignedArtifact, r ReceiptObservation, effect *PositionEffect, now time.Time) error {
	state := "canonical_revert"
	if r.Status == 1 {
		state = "canonical_success"
	}
	if s.recoveryHook != nil {
		s.recoveryHook("before_canonical_effect_commit")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx, `UPDATE receipt_observations SET canonical_state=?,reconciliation_version=reconciliation_version+1,updated_at=? WHERE id=? AND attempt_id=? AND canonical_state IN ('observed','orphaned')`, state, stamp, r.ID, a.AttemptID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil
	}
	if r.Status == 1 {
		if effect == nil || effect.Asset == "" || effect.Delta == "" {
			return ErrInvalidRequest
		}
		effect.ID = deterministicID(a.AttemptID, "position-effect")
		effect.AttemptID = a.AttemptID
		effect.ReceiptID = r.ID
		effect.WalletID = a.WalletID
		transition := "apply"
		var priorState, priorAsset, priorDelta string
		if queryErr := tx.QueryRowContext(ctx, `SELECT state,asset,delta FROM position_effects WHERE attempt_id=?`, a.AttemptID).Scan(&priorState, &priorAsset, &priorDelta); queryErr == nil {
			if priorAsset != effect.Asset || priorDelta != effect.Delta {
				return ErrArtifactIntegrity
			}
			if priorState == "rolled_back" {
				transition = "reapply"
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO position_effects(effect_id,attempt_id,receipt_id,wallet_id,asset,delta,state,applied_at,updated_at) VALUES(?,?,?,?,?,?,'active',?,?) ON CONFLICT(attempt_id) DO UPDATE SET receipt_id=excluded.receipt_id,state='active',updated_at=excluded.updated_at`, effect.ID, a.AttemptID, r.ID, a.WalletID, effect.Asset, effect.Delta, stamp, stamp)
		if err != nil {
			return err
		}
		hid := deterministicID(effect.ID, r.ID, transition)
		_, err = tx.ExecContext(ctx, `INSERT INTO position_effect_history(id,effect_id,transition,receipt_id,created_at) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, hid, effect.ID, transition, r.ID, stamp)
		if err != nil {
			return err
		}
	}
	reservationState := "reverted_released"
	if r.Status == 1 {
		reservationState = "settled"
	}
	_, err = tx.ExecContext(ctx, `UPDATE execution_reservations SET status=?,nonce_consumed=1,gas_consumed=1,updated_at=? WHERE operation_id=?`, reservationState, stamp, a.Operation)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE execution_wallet_lanes SET state='idle',operation_id=NULL,step_id=NULL,reserved_nonce=NULL,freeze_reason=NULL,updated_at=? WHERE wallet_id=?`, stamp, a.WalletID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE execution_steps SET status=? WHERE id=?`, state, a.StepID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE operations SET status=? WHERE id=?`, state, a.Operation)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if s.recoveryHook != nil {
		s.recoveryHook("after_canonical_effect_commit")
	}
	return nil
}

func (s *Store) Orphan(ctx context.Context, a SignedArtifact, r ReceiptObservation, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `UPDATE receipt_observations SET canonical_state='orphaned',reconciliation_version=reconciliation_version+1,updated_at=? WHERE id=? AND canonical_state IN ('observed','canonical_success','canonical_revert')`, stamp, r.ID)
	if err != nil {
		return err
	}
	eid := deterministicID(a.AttemptID, "position-effect")
	_, err = tx.ExecContext(ctx, `UPDATE position_effects SET state='rolled_back',updated_at=? WHERE effect_id=? AND state='active'`, stamp, eid)
	if err != nil {
		return err
	}
	hid := deterministicID(eid, r.ID, "rollback")
	_, err = tx.ExecContext(ctx, `INSERT INTO position_effect_history(id,effect_id,transition,receipt_id,created_at) SELECT ?,?,'rollback',?,? WHERE EXISTS(SELECT 1 FROM position_effects WHERE effect_id=?) ON CONFLICT DO NOTHING`, hid, eid, r.ID, stamp, eid)
	if err != nil {
		return err
	}
	var laneState string
	var laneOperation sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT state,operation_id FROM execution_wallet_lanes WHERE wallet_id=?`, a.WalletID).Scan(&laneState, &laneOperation); err != nil {
		return err
	}
	sameOperation := laneState == "idle" || (laneOperation.Valid && laneOperation.String == a.Operation)
	if sameOperation {
		_, err = tx.ExecContext(ctx, `UPDATE execution_wallet_lanes SET state='frozen',operation_id=?,step_id=?,freeze_reason='canonical_reorg',updated_at=? WHERE wallet_id=?`, a.Operation, a.StepID, stamp, a.WalletID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE execution_wallet_lanes SET state='frozen',freeze_reason='prior_canonical_reorg',updated_at=? WHERE wallet_id=?`, stamp, a.WalletID)
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE execution_reservations SET status='frozen',updated_at=? WHERE operation_id=?`, stamp, a.Operation)
	if err != nil {
		return err
	}
	stepState := "reconciling"
	if !sameOperation {
		stepState = "orphaned"
	}
	_, err = tx.ExecContext(ctx, `UPDATE execution_steps SET status=? WHERE id=?`, stepState, a.StepID)
	if err != nil {
		return err
	}
	if s.recoveryHook != nil {
		s.recoveryHook("before_reorg_rollback_commit")
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if s.recoveryHook != nil {
		s.recoveryHook("after_reorg_rollback_commit")
	}
	return nil
}
