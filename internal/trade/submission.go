package trade

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var (
	ErrBroadcastAmbiguous = errors.New("broadcast outcome unknown")
	ErrBroadcastRejected  = errors.New("broadcast deterministically rejected")
	ErrRPCHashMismatch    = errors.New("RPC transaction hash mismatch")
)

type RawBroadcaster interface {
	SendRawTransaction(context.Context, []byte) (string, error)
}

// SendControlledPrepared is the only W4-B worker send boundary. The final
// mutable-control and SUBMISSION lease checks, broadcaster call, and durable
// outcome are serialized by one SQLite write transaction. A lease takeover or
// emergency-stop update therefore cannot commit between the final check and
// the send outcome commit.
func (s *SubmissionService) SendControlledPrepared(ctx context.Context, work ControlledSubmissionWork, snapshot RuntimeGateSnapshot, sub SubmissionRecord, lease SubmissionLeaseFence, now time.Time) error {
	stored, found, err := s.store.LoadEncryptedArtifact(ctx, work.Request.OperationID)
	if err != nil || !found {
		return ErrArtifactIntegrity
	}
	raw, err := s.kernel.cipher.Decrypt(stored.KeyVersion, stored.Ciphertext, stored.EncryptionNonce, artifactAAD(stored.Operation, stored.StepID, stored.AttemptID))
	if err != nil {
		return err
	}
	defer clear(raw)
	if err = verifyRawArtifact(raw, stored.SignedArtifact); err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != strings.ToLower(work.Artifact.ArtifactHash) || stored.AttemptID != work.Artifact.AttemptID || !strings.EqualFold(stored.TxHash, work.Artifact.TxHash) {
		return ErrArtifactIntegrity
	}
	if err = s.store.MarkControlledSendIntent(ctx, work, snapshot, sub, lease, now); err != nil {
		return err
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = assertSubmissionLeaseTx(ctx, tx, lease); err != nil {
		return err
	}
	if reason, checkErr := validateImmediateControlsTx(ctx, tx, snapshot, work.Request, now); checkErr != nil {
		if finishErr := finishControlledSubmissionTx(ctx, tx, stored.SignedArtifact, sub, "known_unsent", "", reason, checkErr.Error(), work.Purpose, now); finishErr != nil {
			return finishErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return commitErr
		}
		return ErrCanaryGateRejected
	}
	var permitState string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM canary_send_permits WHERE id=? AND operation_id=? AND attempt_id=? AND purpose=? AND artifact_hash=? AND tx_hash=?`, work.PermitID, work.Artifact.OperationID, work.Artifact.AttemptID, work.Purpose, strings.ToLower(work.Artifact.ArtifactHash), strings.ToLower(work.Artifact.TxHash)).Scan(&permitState); err != nil || permitState != "CONSUMED" {
		return ErrCanaryRuntimeRejected
	}
	var submissionState string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM transaction_submissions WHERE id=? AND attempt_id=? AND tx_hash=?`, sub.ID, sub.AttemptID, sub.TxHash).Scan(&submissionState); err != nil || submissionState != "submitting" {
		return ErrBroadcastAmbiguous
	}
	if s.hook != nil {
		s.hook("controlled_before_send")
	}
	hash, sendErr := s.broadcaster.SendRawTransaction(ctx, raw)
	if s.hook != nil {
		s.hook("controlled_after_send_before_outcome_commit")
	}
	state, rpcHash, class, detail := "submitted", hash, "", ""
	resultErr := error(nil)
	if sendErr != nil {
		resultErr = sendErr
		if errors.Is(sendErr, ErrBroadcastRejected) {
			state, rpcHash, class, detail = "known_unsent", "", "deterministic_rejection", sendErr.Error()
		} else {
			state, rpcHash, class, detail = "broadcast_unknown", "", "ambiguous_transport", sendErr.Error()
			resultErr = ErrBroadcastAmbiguous
		}
	} else if !strings.EqualFold(hash, stored.TxHash) {
		state, class, detail, resultErr = "manual_resolution", "hash_mismatch", ErrRPCHashMismatch.Error(), ErrRPCHashMismatch
	}
	if err = finishControlledSubmissionTx(ctx, tx, stored.SignedArtifact, sub, state, rpcHash, class, detail, work.Purpose, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return ErrBroadcastAmbiguous
	}
	return resultErr
}

func finishControlledSubmissionTx(ctx context.Context, tx *sql.Tx, artifact SignedArtifact, sub SubmissionRecord, state, rpcHash, class, detail, purpose string, now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE transaction_submissions SET state=?,rpc_hash=?,failure_class=?,failure_detail=?,updated_at=? WHERE id=? AND attempt_id=? AND tx_hash=? AND state='submitting'`, state, nullText(rpcHash), nullText(class), nullText(detail), stamp, sub.ID, artifact.AttemptID, artifact.TxHash)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrArtifactIntegrity
	}
	if state == "broadcast_unknown" || state == "manual_resolution" || (state == "known_unsent" && purpose == PermitUnknownReplay) {
		if _, err = tx.ExecContext(ctx, `UPDATE execution_wallet_lanes SET state='frozen',freeze_reason=?,updated_at=? WHERE wallet_id=? AND operation_id=?`, state, stamp, artifact.WalletID, artifact.Operation); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE execution_reservations SET status='frozen',updated_at=? WHERE operation_id=?`, stamp, artifact.Operation); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE canary_risk_reservations SET state='frozen',updated_at=? WHERE operation_id=? AND state IN ('reserved','frozen')`, stamp, artifact.Operation); err != nil {
			return err
		}
		return nil
	}
	if state == "known_unsent" {
		if _, err = tx.ExecContext(ctx, `UPDATE execution_wallet_lanes SET state='idle',operation_id=NULL,step_id=NULL,reserved_nonce=NULL,freeze_reason='known_unsent',updated_at=? WHERE wallet_id=? AND operation_id=?`, stamp, artifact.WalletID, artifact.Operation); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE execution_reservations SET status='released',updated_at=? WHERE operation_id=?`, stamp, artifact.Operation); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE canary_risk_reservations SET state='released',updated_at=? WHERE operation_id=? AND state='reserved'`, stamp, artifact.Operation); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE transaction_attempts SET status='known_unsent',updated_at=? WHERE id=?`, stamp, artifact.AttemptID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE execution_steps SET status='known_unsent',updated_at=? WHERE id=?`, stamp, artifact.StepID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE operations SET status='known_unsent',failure_code=?,updated_at=? WHERE id=?`, class, stamp, artifact.Operation)
		return err
	}
	return nil
}

type SubmissionService struct {
	store       *Store
	kernel      *ExecutionKernel
	broadcaster RawBroadcaster
	now         func() time.Time
	hook        func(string)
}

func (s *SubmissionService) SetHookForTest(h func(string)) { s.hook = h }

func NewSubmissionService(store *Store, kernel *ExecutionKernel, b RawBroadcaster) (*SubmissionService, error) {
	if store == nil || kernel == nil || b == nil {
		return nil, ErrInvalidRequest
	}
	return &SubmissionService{store: store, kernel: kernel, broadcaster: b, now: time.Now}, nil
}

func (s *SubmissionService) Submit(ctx context.Context, operation string) (SubmissionRecord, error) {
	return s.submit(ctx, operation, false)
}

func (s *SubmissionService) ReplayUnknown(ctx context.Context, operation string, policyAllows bool) (SubmissionRecord, error) {
	if !policyAllows {
		return SubmissionRecord{}, ErrBroadcastAmbiguous
	}
	return s.submit(ctx, operation, true)
}

func (s *SubmissionService) submit(ctx context.Context, operation string, replayUnknown bool) (SubmissionRecord, error) {
	stored, found, err := s.store.LoadEncryptedArtifact(ctx, operation)
	if err != nil || !found {
		return SubmissionRecord{}, ErrArtifactIntegrity
	}
	raw, err := s.kernel.cipher.Decrypt(stored.KeyVersion, stored.Ciphertext, stored.EncryptionNonce, artifactAAD(stored.Operation, stored.StepID, stored.AttemptID))
	if err != nil {
		return SubmissionRecord{}, err
	}
	defer clear(raw)
	if err = verifyRawArtifact(raw, stored.SignedArtifact); err != nil {
		return SubmissionRecord{}, err
	}
	if latest, ok, e := s.store.LatestSubmission(ctx, stored.AttemptID); e != nil {
		return SubmissionRecord{}, e
	} else if ok && latest.State == "submitted" {
		return latest, nil
	} else if ok && latest.State == "submitting" {
		_ = s.store.FinishSubmission(ctx, stored.SignedArtifact, latest, "broadcast_unknown", "", "restart_with_inflight_submission", "send outcome was not durably recorded", s.now())
		latest.State = "broadcast_unknown"
		return latest, ErrBroadcastAmbiguous
	} else if ok && latest.State == "broadcast_unknown" && !replayUnknown {
		return latest, ErrBroadcastAmbiguous
	} else if replayUnknown && (!ok || latest.State != "broadcast_unknown") {
		return SubmissionRecord{}, ErrInvalidRequest
	}
	if err = s.store.CheckOperationTTL(ctx, operation, s.now().UTC()); err != nil {
		// An ambiguous submission remains frozen and query/reconcile-only after
		// expiry. A provably never-submitted artifact may be safely terminated.
		if !replayUnknown {
			if _, ok, latestErr := s.store.LatestSubmission(ctx, stored.AttemptID); latestErr == nil && !ok {
				_ = s.store.ExpirePreBroadcast(ctx, stored.SignedArtifact, nil, s.now().UTC())
			}
		}
		return SubmissionRecord{}, err
	}
	sub, err := s.store.BeginSubmission(ctx, stored.SignedArtifact, replayUnknown, s.now())
	if err != nil {
		return SubmissionRecord{}, err
	}
	if err = s.store.CheckOperationTTL(ctx, operation, s.now().UTC()); err != nil {
		if replayUnknown {
			// This sequence was created only for the attempted replay. It is known
			// unsent, but the earlier ambiguous reservation remains frozen.
			_ = s.store.FinishSubmission(ctx, stored.SignedArtifact, sub, "expired_prebroadcast", "", "ttl_expired_pre_send", err.Error(), s.now())
			return SubmissionRecord{}, err
		}
		if expireErr := s.store.ExpirePreBroadcast(ctx, stored.SignedArtifact, &sub, s.now().UTC()); expireErr != nil {
			return SubmissionRecord{}, expireErr
		}
		return SubmissionRecord{}, err
	}
	if s.hook != nil {
		s.hook("before_send")
	}
	hash, sendErr := s.broadcaster.SendRawTransaction(ctx, raw)
	if s.hook != nil {
		s.hook("after_send_before_outcome_commit")
	}
	if sendErr != nil {
		if errors.Is(sendErr, ErrBroadcastRejected) {
			_ = s.store.FinishSubmission(ctx, stored.SignedArtifact, sub, "known_unsent", "", "deterministic_rejection", sendErr.Error(), s.now())
			return sub, sendErr
		}
		_ = s.store.FinishSubmission(ctx, stored.SignedArtifact, sub, "broadcast_unknown", "", "ambiguous_transport", sendErr.Error(), s.now())
		return sub, ErrBroadcastAmbiguous
	}
	if hash != stored.TxHash {
		_ = s.store.FinishSubmission(ctx, stored.SignedArtifact, sub, "manual_resolution", hash, "hash_mismatch", ErrRPCHashMismatch.Error(), s.now())
		return sub, ErrRPCHashMismatch
	}
	if err = s.store.FinishSubmission(ctx, stored.SignedArtifact, sub, "submitted", hash, "", "", s.now()); err != nil {
		return sub, ErrBroadcastAmbiguous
	}
	sub.State = "submitted"
	sub.RPCHash = hash
	return sub, nil
}
