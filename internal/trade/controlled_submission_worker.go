package trade

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrCanarySubmissionLeaseLost = errors.New("controlled submission lease lost")

type ControlledSubmissionWork struct {
	Request  CanaryGateRequest
	Artifact CanaryArtifactIdentity
	PermitID string
	Purpose  string
}

type ControlledSubmissionWorker struct {
	store               *Store
	orchestrator        *ControlledCanaryOrchestrator
	submission          *SubmissionService
	environment, holder string
	epoch               uint64
	now                 func() time.Time
}

func NewControlledSubmissionWorker(store *Store, orchestrator *ControlledCanaryOrchestrator, submission *SubmissionService, environment, holder string, epoch uint64) (*ControlledSubmissionWorker, error) {
	if store == nil || orchestrator == nil || submission == nil || environment == "" || holder == "" || epoch == 0 {
		return nil, ErrInvalidRequest
	}
	return &ControlledSubmissionWorker{store: store, orchestrator: orchestrator, submission: submission, environment: environment, holder: holder, epoch: epoch, now: time.Now}, nil
}

func (w *ControlledSubmissionWorker) leaseFence() SubmissionLeaseFence {
	return SubmissionLeaseFence{Environment: w.environment, HolderID: w.holder, Epoch: w.epoch, clock: w.now}
}

// RunOnce discovers only durable ISSUED permits. It never creates an artifact,
// allocates a nonce, or bypasses the W2 immediate-pre-send gate.
func (w *ControlledSubmissionWorker) RunOnce(ctx context.Context) error {
	if err := w.store.ValidateCanaryWorkerLease(ctx, "SUBMISSION", w.environment, w.holder, w.epoch, w.now().UTC()); err != nil {
		_ = w.store.RecordRuntimeAlert(ctx, "CONTROLLED_SUBMISSION_WORKER", "", "", "", 0, "SUBMISSION_LEASE_LOST", "CRITICAL", w.now().UTC())
		return fmt.Errorf("validate submission lease: %w", ErrCanarySubmissionLeaseLost)
	}
	if err := w.store.RecoverControlledInflight(ctx, w.leaseFence(), w.now().UTC()); err != nil {
		return fmt.Errorf("recover controlled inflight: %w", err)
	}
	work, err := w.store.ListControlledSubmissionWork(ctx)
	if err != nil {
		return err
	}
	for _, item := range work {
		if err = w.process(ctx, item); err != nil {
			return fmt.Errorf("process controlled submission %s: %w", item.PermitID, err)
		}
	}
	return nil
}

func (w *ControlledSubmissionWorker) process(ctx context.Context, item ControlledSubmissionWork) error {
	snapshot, decision, err := w.orchestrator.immediatePreSendSnapshot(ctx, item.Request, item.PermitID, item.Purpose, item.Artifact)
	if err != nil {
		return err
	}
	sub, reason, err := w.store.BeginControlledSubmissionAfterGate(ctx, snapshot, item.Request, item.Artifact, item.PermitID, item.Purpose, w.leaseFence(), w.now().UTC())
	if err != nil {
		if w.store.controlledWorkAlreadyClaimed(ctx, item) {
			return nil
		}
		if reason != "" {
			return ErrCanaryGateRejected
		}
		return err
	}
	if decision.Decision != "PASS" {
		return ErrCanaryGateRejected
	}
	return w.submission.SendControlledPrepared(ctx, item, snapshot, sub, w.leaseFence(), w.now().UTC())
}

func (s *Store) controlledWorkAlreadyClaimed(ctx context.Context, item ControlledSubmissionWork) bool {
	var permitState string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM canary_send_permits WHERE id=?`, item.PermitID).Scan(&permitState); err != nil || permitState != "CONSUMED" {
		return false
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transaction_submissions WHERE attempt_id=? AND tx_hash=? AND state IN ('submitting','submitted','broadcast_unknown','known_unsent','manual_resolution')`, item.Artifact.AttemptID, item.Artifact.TxHash).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

func (w *ControlledSubmissionWorker) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return ErrInvalidRequest
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err := w.RunOnce(context.WithoutCancel(ctx)); err != nil {
			return err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (s *Store) ListControlledSubmissionWork(ctx context.Context) ([]ControlledSubmissionWork, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.purpose,p.operation_id,p.attempt_id,p.tx_hash,p.artifact_hash,p.authorization_id,p.authorization_epoch,a.policy_version,a.wallet_id,a.environment,a.build_sha,a.release_id,a.deployment_id FROM canary_send_permits p JOIN canary_runtime_authorizations a ON a.id=p.authorization_id AND a.epoch=p.authorization_epoch WHERE p.state='ISSUED' ORDER BY p.issued_at,p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []ControlledSubmissionWork
	for rows.Next() {
		var v ControlledSubmissionWork
		v.Request.ChainID = ChainID
		if err = rows.Scan(&v.PermitID, &v.Purpose, &v.Request.OperationID, &v.Request.AttemptID, &v.Artifact.TxHash, &v.Artifact.ArtifactHash, &v.Request.AuthorizationID, &v.Request.AuthorizationEpoch, &v.Request.PolicyVersion, &v.Request.WalletID, &v.Request.Deployment.Environment, &v.Request.Deployment.BuildSHA, &v.Request.Deployment.ReleaseID, &v.Request.Deployment.DeploymentID); err != nil {
			return nil, err
		}
		v.Artifact.OperationID, v.Artifact.AttemptID = v.Request.OperationID, v.Request.AttemptID
		values = append(values, v)
	}
	return values, rows.Err()
}

func (s *Store) BeginControlledSubmissionAfterGate(ctx context.Context, v RuntimeGateSnapshot, r CanaryGateRequest, x CanaryArtifactIdentity, permitID, purpose string, lease SubmissionLeaseFence, now time.Time) (SubmissionRecord, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubmissionRecord{}, "", err
	}
	defer tx.Rollback()
	if err = assertSubmissionLeaseTx(ctx, tx, lease); err != nil {
		return SubmissionRecord{}, "SUBMISSION_LEASE_LOST", err
	}
	if reason, checkErr := validateImmediateControlsTx(ctx, tx, v, r, now); checkErr != nil {
		return SubmissionRecord{}, reason, checkErr
	}
	var priorGates int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM canary_runtime_gate_snapshots WHERE operation_id=? AND authorization_id=? AND authorization_epoch=? AND decision='PASS' AND ((stage IN ('EXECUTION_ADMISSION','PRE_SIGN') AND attempt_id IS NULL) OR (stage='FIRST_BROADCAST' AND attempt_id=?))`, r.OperationID, r.AuthorizationID, r.AuthorizationEpoch, x.AttemptID).Scan(&priorGates); err != nil || priorGates != 3 {
		return SubmissionRecord{}, "W2_GATE_CHAIN_INCOMPLETE", ErrCanaryRuntimeRejected
	}
	if err = insertGateSnapshotTx(ctx, tx, v); err != nil {
		return SubmissionRecord{}, "", err
	}
	result, err := tx.ExecContext(ctx, `UPDATE canary_send_permits SET state='CONSUMED',consumed_at=? WHERE id=? AND operation_id=? AND attempt_id=? AND authorization_id=? AND authorization_epoch=? AND purpose=? AND artifact_hash=? AND tx_hash=? AND state='ISSUED' AND expires_at>?`, now.UTC().Format(time.RFC3339Nano), permitID, x.OperationID, x.AttemptID, r.AuthorizationID, r.AuthorizationEpoch, purpose, strings.ToLower(x.ArtifactHash), strings.ToLower(x.TxHash), now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return SubmissionRecord{}, "", err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return SubmissionRecord{}, "PERMIT_IDENTITY_MISMATCH", ErrCanaryRuntimeRejected
	}
	var lane string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM execution_wallet_lanes WHERE wallet_id=? AND operation_id=?`, r.WalletID, r.OperationID).Scan(&lane); err != nil || (purpose == PermitFirstBroadcast && lane != "signed") || (purpose == PermitUnknownReplay && lane != "frozen") {
		return SubmissionRecord{}, "WALLET_LANE_INVALID", ErrWalletLaneBusy
	}
	var existing int
	if purpose == PermitFirstBroadcast {
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM transaction_submissions WHERE attempt_id=?`, x.AttemptID).Scan(&existing); err != nil || existing != 0 {
			return SubmissionRecord{}, "FIRST_BROADCAST_NOT_AVAILABLE", ErrCanaryRuntimeRejected
		}
	} else {
		var latest string
		if err = tx.QueryRowContext(ctx, `SELECT state FROM transaction_submissions WHERE attempt_id=? ORDER BY sequence DESC LIMIT 1`, x.AttemptID).Scan(&latest); err != nil || latest != "broadcast_unknown" {
			return SubmissionRecord{}, "UNKNOWN_REPLAY_NOT_AVAILABLE", ErrCanaryRuntimeRejected
		}
	}
	var sequence uint64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence)+1,0) FROM transaction_submissions WHERE attempt_id=?`, x.AttemptID).Scan(&sequence); err != nil {
		return SubmissionRecord{}, "", err
	}
	sub := SubmissionRecord{ID: deterministicID(x.AttemptID, "submission", fmt.Sprint(sequence)), AttemptID: x.AttemptID, TxHash: x.TxHash, Sequence: sequence, State: "submitting"}
	stamp := now.UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO transaction_submissions(id,attempt_id,tx_hash,sequence,state,created_at,updated_at) VALUES(?,?,?,?, 'submitting',?,?)`, sub.ID, sub.AttemptID, sub.TxHash, sub.Sequence, stamp, stamp); err != nil {
		return SubmissionRecord{}, "", err
	}
	return sub, "", tx.Commit()
}

func (s *Store) RecoverControlledInflight(ctx context.Context, lease SubmissionLeaseFence, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = assertSubmissionLeaseTx(ctx, tx, lease); err != nil {
		return err
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE transaction_submissions AS sub SET state='broadcast_unknown',failure_class='restart_with_inflight_submission',failure_detail='send outcome was not durably recorded',updated_at=? WHERE sub.state='submitting' AND sub.created_at<? AND EXISTS(SELECT 1 FROM canary_send_permits p JOIN canary_runtime_authorizations a ON a.id=p.authorization_id AND a.epoch=p.authorization_epoch WHERE p.attempt_id=sub.attempt_id AND p.state='CONSUMED' AND a.environment=?)`, stamp, stamp, lease.Environment); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE execution_wallet_lanes SET state='frozen',freeze_reason='broadcast_unknown',updated_at=? WHERE operation_id IN (SELECT s.operation_id FROM execution_steps s JOIN transaction_attempts a ON a.step_id=s.id JOIN transaction_submissions sub ON sub.attempt_id=a.id WHERE sub.failure_class='restart_with_inflight_submission')`, stamp); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE execution_reservations SET status='frozen',updated_at=? WHERE operation_id IN (SELECT s.operation_id FROM execution_steps s JOIN transaction_attempts a ON a.step_id=s.id JOIN transaction_submissions sub ON sub.attempt_id=a.id WHERE sub.failure_class='restart_with_inflight_submission')`, stamp); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE canary_risk_reservations SET state='frozen',updated_at=? WHERE operation_id IN (SELECT s.operation_id FROM execution_steps s JOIN transaction_attempts a ON a.step_id=s.id JOIN transaction_submissions sub ON sub.attempt_id=a.id WHERE sub.failure_class='restart_with_inflight_submission') AND state IN ('reserved','frozen')`, stamp); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE execution_steps SET status='broadcast_unknown',updated_at=? WHERE id IN (SELECT a.step_id FROM transaction_attempts a JOIN transaction_submissions sub ON sub.attempt_id=a.id WHERE sub.failure_class='restart_with_inflight_submission')`, stamp); err != nil {
		return err
	}
	return tx.Commit()
}
