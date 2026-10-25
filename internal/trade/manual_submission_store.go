package trade

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ManualFirstSubmissionRequest binds the original signed MANUAL attempt. It is
// internal workflow input, never proof of permission by itself.
type ManualFirstSubmissionRequest struct {
	Gate                           CanaryGateRequest
	PermitID, ArtifactHash, TxHash string
	Lease                          SubmissionLeaseFence
}

// ManualSubmissionRecheck runs on the same fenced SQLite transaction as the
// mutation/call. It must verify the current process, clock and artifact and must
// not open a second transaction on the single-connection Store.
type ManualSubmissionRecheck func(context.Context, *sql.Tx) error

type ManualSubmissionOutcome struct {
	State, RPCHash, FailureClass, FailureDetail string
}

var ErrManualFirstSubmissionRejected = errors.New("manual first submission rejected")

// Take SQLite's writer fence without changing the evidence source timestamp.
// The caller's same-transaction recheck authenticates trusted current time.
func assertManualSubmissionLeaseTx(ctx context.Context, tx *sql.Tx, lease SubmissionLeaseFence, now time.Time) error {
	if lease.Environment == "" || lease.HolderID == "" || lease.Epoch == 0 || now.IsZero() {
		return ErrCanarySubmissionLeaseLost
	}
	if _, err := tx.ExecContext(ctx, `UPDATE canary_worker_leases SET updated_at=updated_at WHERE role='SUBMISSION' AND environment=? AND holder_id=? AND lease_epoch=?`, lease.Environment, lease.HolderID, lease.Epoch); err != nil {
		return ErrCanarySubmissionLeaseLost
	}
	var expires, updated string
	if err := tx.QueryRowContext(ctx, `SELECT expires_at,updated_at FROM canary_worker_leases WHERE role='SUBMISSION' AND environment=? AND holder_id=? AND lease_epoch=?`, lease.Environment, lease.HolderID, lease.Epoch).Scan(&expires, &updated); err != nil {
		return ErrCanarySubmissionLeaseLost
	}
	expiry, err := time.Parse(time.RFC3339Nano, expires)
	start, startErr := time.Parse(time.RFC3339Nano, updated)
	if err != nil || startErr != nil || !now.Before(expiry) || now.Before(start) {
		return ErrCanarySubmissionLeaseLost
	}
	return nil
}

func validManualSubmissionRequest(r ManualFirstSubmissionRequest, now time.Time) bool {
	return !now.IsZero() && r.Gate.ChainID == ChainID && r.Gate.OperationID != "" && r.Gate.AttemptID != "" &&
		r.Gate.AuthorizationID != "" && r.Gate.AuthorizationEpoch > 0 && r.Gate.WalletID != "" &&
		r.PermitID == "manual-first-permit:"+r.Gate.AttemptID && len(r.ArtifactHash) == 64 && len(r.TxHash) == 66 &&
		r.ArtifactHash == strings.ToLower(r.ArtifactHash) && r.TxHash == strings.ToLower(r.TxHash) &&
		r.Lease.Environment == r.Gate.Deployment.Environment && r.Lease.HolderID != "" && r.Lease.Epoch > 0
}

// BeginManualFirstSubmission atomically consumes an ISSUED permit and creates
// the sole sequence-zero submission with NULL send_intent_at. Old standalone
// CONSUMED permits cannot be adopted, even when no submission exists.
func (s *Store) BeginManualFirstSubmission(ctx context.Context, r ManualFirstSubmissionRequest, now time.Time, recheck ManualSubmissionRecheck) (SubmissionRecord, error) {
	if s == nil || s.db == nil || ctx == nil || !validManualSubmissionRequest(r, now) || recheck == nil {
		return SubmissionRecord{}, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubmissionRecord{}, err
	}
	defer tx.Rollback()
	if err = assertManualSubmissionLeaseTx(ctx, tx, r.Lease, now); err != nil {
		return SubmissionRecord{}, err
	}
	if err = checkManualSubmissionIdentityTx(ctx, tx, r); err != nil {
		return SubmissionRecord{}, err
	}
	if err = recheck(ctx, tx); err != nil {
		return SubmissionRecord{}, err
	}
	sub := SubmissionRecord{ID: "manual-first-submission:" + r.Gate.AttemptID, AttemptID: r.Gate.AttemptID, TxHash: r.TxHash, Sequence: 0, State: "submitting"}
	result, err := tx.ExecContext(ctx, `INSERT INTO manual_first_submission_bindings(permit_id,submission_id,operation_id,attempt_id,authorization_id,authorization_epoch,artifact_hash,tx_hash,foundation_hash,environment,holder_id,lease_epoch,created_at)
 SELECT ?,?,?,?,?,?,?,?,f.binding_hash,?,?,?,? FROM manual_submission_foundation_complete f WHERE f.operation_id=?`,
		r.PermitID, sub.ID, r.Gate.OperationID, r.Gate.AttemptID, r.Gate.AuthorizationID, r.Gate.AuthorizationEpoch, r.ArtifactHash, r.TxHash, r.Lease.Environment, r.Lease.HolderID, r.Lease.Epoch, now.UTC().Format(time.RFC3339Nano), r.Gate.OperationID)
	if err != nil {
		return SubmissionRecord{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return SubmissionRecord{}, ErrManualFirstSubmissionRejected
	}
	if err = s.canaryFault("manual_submission_claim_before_commit"); err != nil {
		return SubmissionRecord{}, err
	}
	if err = tx.Commit(); err != nil {
		return SubmissionRecord{}, err
	}
	return sub, nil
}

func checkManualSubmissionIdentityTx(ctx context.Context, tx *sql.Tx, r ManualFirstSubmissionRequest) error {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM manual_execution_bindings b JOIN manual_nonce_claims q ON q.operation_id=b.operation_id
 JOIN manual_first_broadcast_issuances i ON i.operation_id=b.operation_id AND i.attempt_id=q.attempt_id
 WHERE b.operation_id=? AND q.attempt_id=? AND b.authorization_id=? AND b.authorization_epoch=?
 AND b.wallet_id=? AND b.policy_version=? AND b.environment=? AND b.build_sha=? AND b.release_id=? AND b.deployment_id=?
 AND i.id=? AND i.artifact_hash=? AND i.tx_hash=?`,
		r.Gate.OperationID, r.Gate.AttemptID, r.Gate.AuthorizationID, r.Gate.AuthorizationEpoch, r.Gate.WalletID, r.Gate.PolicyVersion,
		r.Gate.Deployment.Environment, strings.ToLower(r.Gate.Deployment.BuildSHA), r.Gate.Deployment.ReleaseID, r.Gate.Deployment.DeploymentID,
		r.PermitID, r.ArtifactHash, r.TxHash).Scan(&one)
	if err != nil || one != 1 {
		return ErrManualFirstSubmissionRejected
	}
	return nil
}

// ValidateManualFirstSubmissionPhaseTx checks the exact post-claim source and
// original deadlines. The caller additionally authenticates runtime/artifact
// evidence on this same transaction via its recheck callback.
func (s *Store) ValidateManualFirstSubmissionPhaseTx(ctx context.Context, tx *sql.Tx, r ManualFirstSubmissionRequest, sub SubmissionRecord, now time.Time) error {
	if tx == nil || !validManualSubmissionRequest(r, now) || sub.ID != "manual-first-submission:"+r.Gate.AttemptID || sub.AttemptID != r.Gate.AttemptID || sub.TxHash != r.TxHash || sub.Sequence != 0 {
		return ErrManualFirstSubmissionRejected
	}
	if err := checkManualSubmissionIdentityTx(ctx, tx, r); err != nil {
		return err
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM manual_first_submission_phase_source p WHERE p.permit_id=? AND p.submission_id=?
 AND p.operation_id=? AND p.attempt_id=? AND p.artifact_hash=? AND p.tx_hash=?
 AND p.environment=? AND p.holder_id=? AND p.lease_epoch=?
 AND json_extract(p.source_json,'$.submission_lease.holder_id')=? AND json_extract(p.source_json,'$.submission_lease.epoch')=?
 AND NOT EXISTS(SELECT 1 FROM json_each(json_array(p.permit_expires_at,p.auth_expires_at,p.operation_expires_at,p.lease_expires_at)) WHERE manual_time_cmp(?,value)>=0)
 AND NOT EXISTS(SELECT 1 FROM json_each(json_array(p.created_at,p.foundation_created_at,p.artifact_completed_at,p.control_effective_at,p.control_updated_at,p.authorized_at,p.auth_created_at,p.auth_updated_at,p.lease_updated_at,p.policy_created_at,p.high_water_at,
 json_extract(p.source_json,'$.allowlists.wallet_created_at'),json_extract(p.source_json,'$.allowlists.contract_created_at'),json_extract(p.source_json,'$.allowlists.token_created_at'))) WHERE manual_time_cmp(?,value)<0)`,
		r.PermitID, sub.ID, r.Gate.OperationID, r.Gate.AttemptID, r.ArtifactHash, r.TxHash, r.Lease.Environment, r.Lease.HolderID, r.Lease.Epoch, r.Lease.HolderID, r.Lease.Epoch, stamp, stamp).Scan(&one)
	if err != nil || one != 1 {
		return ErrManualFirstSubmissionRejected
	}
	return nil
}

func (s *Store) MarkManualFirstSendIntent(ctx context.Context, r ManualFirstSubmissionRequest, sub SubmissionRecord, now time.Time, recheck ManualSubmissionRecheck) error {
	if s == nil || s.db == nil || ctx == nil || recheck == nil {
		return ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = assertManualSubmissionLeaseTx(ctx, tx, r.Lease, now); err != nil {
		return err
	}
	if err = s.ValidateManualFirstSubmissionPhaseTx(ctx, tx, r, sub, now); err != nil {
		return err
	}
	if err = recheck(ctx, tx); err != nil {
		return err
	}
	if err = insertManualSubmissionTransitionTx(ctx, tx, sub.ID, "INTENT", ManualSubmissionOutcome{State: "submitting"}, r.Lease, now); err != nil {
		return err
	}
	if err = s.canaryFault("manual_submission_intent_before_commit"); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.manualSendMu.Lock()
	if s.manualSendTickets == nil {
		s.manualSendTickets = make(map[string]struct{})
	}
	s.manualSendTickets[sub.ID] = struct{}{}
	s.manualSendMu.Unlock()
	return nil
}

func insertManualSubmissionTransitionTx(ctx context.Context, tx *sql.Tx, submissionID, kind string, out ManualSubmissionOutcome, lease SubmissionLeaseFence, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO manual_first_submission_transitions(id,submission_id,kind,result_state,rpc_hash,failure_class,failure_detail,environment,holder_id,lease_epoch,checked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		submissionID+":"+kind, submissionID, kind, out.State, nullText(out.RPCHash), nullText(out.FailureClass), nullText(out.FailureDetail), lease.Environment, lease.HolderID, lease.Epoch, now.UTC().Format(time.RFC3339Nano))
	return err
}

// WithManualFirstSubmissionOutcome is the sole A1 callback boundary. The
// committed intent is already an ambiguity boundary: recheck/callback errors
// become broadcast_unknown, never evidence of non-invocation after restart.
// Non-success outcomes retain/freeze typed accounting and the signed nonce.
func (s *Store) WithManualFirstSubmissionOutcome(ctx context.Context, r ManualFirstSubmissionRequest, sub SubmissionRecord, now time.Time, recheck ManualSubmissionRecheck, send func(context.Context, *sql.Tx) (ManualSubmissionOutcome, error)) error {
	if s == nil || s.db == nil || ctx == nil || recheck == nil || send == nil || !validManualSubmissionRequest(r, now) {
		return ErrInvalidRequest
	}
	// A committed intent is not a reusable invocation permit. This ticket is
	// issued only by this Store's successful NULL->intent transaction and is
	// burned before any callback, including on failure. Reopening a Store or
	// process cannot reconstruct it; durable recovery handles that case.
	s.manualSendMu.Lock()
	_, ticket := s.manualSendTickets[sub.ID]
	delete(s.manualSendTickets, sub.ID)
	s.manualSendMu.Unlock()
	if !ticket {
		return ErrManualFirstSubmissionRejected
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = assertManualSubmissionLeaseTx(ctx, tx, r.Lease, now); err != nil {
		return err
	}
	// Guard before callback: only the original claiming lease can invoke, and
	// repeated calls cannot reuse a committed outcome or a recovered claim.
	var intent string
	if err = tx.QueryRowContext(ctx, `SELECT s.send_intent_at FROM transaction_submissions s JOIN manual_first_submission_bindings b ON b.submission_id=s.id
 WHERE s.id=? AND s.state='submitting' AND s.attempt_id=? AND s.tx_hash=? AND s.send_intent_at IS NOT NULL
 AND b.permit_id=? AND b.environment=? AND b.holder_id=? AND b.lease_epoch=?
 AND NOT EXISTS(SELECT 1 FROM manual_first_submission_transitions c WHERE c.submission_id=s.id AND c.kind='OUTCOME')`,
		sub.ID, r.Gate.AttemptID, r.TxHash, r.PermitID, r.Lease.Environment, r.Lease.HolderID, r.Lease.Epoch).Scan(&intent); err != nil {
		return ErrManualFirstSubmissionRejected
	}
	out := ManualSubmissionOutcome{State: "broadcast_unknown", FailureClass: "final_recheck_failed", FailureDetail: "durable send intent exists; accounting retained"}
	resultErr := s.ValidateManualFirstSubmissionPhaseTx(ctx, tx, r, sub, now)
	if resultErr == nil {
		resultErr = recheck(ctx, tx)
	}
	if resultErr == nil {
		if err = s.canaryFault("manual_submission_before_send"); err != nil {
			return err
		}
		out, resultErr = send(ctx, tx)
		if resultErr != nil {
			out = ManualSubmissionOutcome{State: "broadcast_unknown", FailureClass: "ambiguous_transport", FailureDetail: "callback failed after durable send intent"}
		}
		if err = s.canaryFault("manual_submission_after_send"); err != nil {
			return ErrBroadcastAmbiguous
		}
	}
	if out.State != "submitted" && out.State != "broadcast_unknown" && out.State != "manual_resolution" && out.State != "known_unsent" {
		out = ManualSubmissionOutcome{State: "broadcast_unknown", FailureClass: "invalid_outcome", FailureDetail: "callback returned unsupported outcome"}
		resultErr = ErrBroadcastAmbiguous
	}
	if err = insertManualSubmissionTransitionTx(ctx, tx, sub.ID, "OUTCOME", out, r.Lease, now); err != nil {
		return errors.Join(ErrBroadcastAmbiguous, err)
	}
	if err = s.canaryFault("manual_submission_outcome_before_commit"); err != nil {
		return errors.Join(ErrBroadcastAmbiguous, err)
	}
	if err = tx.Commit(); err != nil {
		return errors.Join(ErrBroadcastAmbiguous, err)
	}
	return resultErr
}

// RecoverManualFirstSubmissions only recovers claims from an older lease epoch.
// The current SUBMISSION writer fence excludes the old owner at its callback
// boundary. A durable intent is ambiguous; no intent is known-unsent-retained.
// This never invokes a transport, releases a nonce, or grants replay.
func (s *Store) RecoverManualFirstSubmissions(ctx context.Context, lease SubmissionLeaseFence, now time.Time) error {
	if s == nil || s.db == nil || ctx == nil || now.IsZero() {
		return ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = assertManualSubmissionLeaseTx(ctx, tx, lease, now); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.send_intent_at FROM manual_first_submission_bindings b JOIN transaction_submissions s ON s.id=b.submission_id
 WHERE b.environment=? AND b.lease_epoch<? AND s.state='submitting' ORDER BY s.id`, lease.Environment, lease.Epoch)
	if err != nil {
		return err
	}
	type pending struct {
		id     string
		intent sql.NullString
	}
	var work []pending
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.id, &p.intent); err != nil {
			rows.Close()
			return err
		}
		work = append(work, p)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, p := range work {
		out := ManualSubmissionOutcome{State: "known_unsent", FailureClass: "restart_before_send_intent", FailureDetail: "signed nonce and typed accounting retained; not released"}
		if p.intent.Valid {
			out = ManualSubmissionOutcome{State: "broadcast_unknown", FailureClass: "restart_after_send_intent", FailureDetail: "send outcome absent after lease takeover; accounting retained"}
		}
		if err = insertManualSubmissionTransitionTx(ctx, tx, p.id, "OUTCOME", out, lease, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
