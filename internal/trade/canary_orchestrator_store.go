package trade

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func (s *Store) IssueCanaryPermitAfterGate(ctx context.Context, v RuntimeGateSnapshot, p SendPermit) error {
	if s == nil || s.db == nil || ctx == nil || v.Decision != "PASS" || p.ID == "" || p.AuthorizationID != v.AuthorizationID || p.AuthorizationEpoch != v.AuthorizationEpoch || p.OperationID != v.OperationID || p.AttemptID != v.AttemptID {
		return ErrCanaryRuntimeRejected
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var matches int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM canary_runtime_authorizations a JOIN execution_steps s ON s.operation_id=? JOIN transaction_attempts t ON t.step_id=s.id WHERE a.id=? AND a.epoch=? AND a.state='ARMED' AND a.expires_at>? AND t.id=? AND lower(t.tx_hash)=lower(?)`, p.OperationID, p.AuthorizationID, p.AuthorizationEpoch, p.IssuedAt.UTC().Format(time.RFC3339Nano), p.AttemptID, p.TxHash).Scan(&matches)
	if err != nil || matches != 1 {
		return ErrCanaryRuntimeRejected
	}
	if err = insertGateSnapshotTx(ctx, tx, v); err != nil {
		return err
	}
	var queryHash, queriedAt any
	if p.Purpose == PermitUnknownReplay {
		if len(p.QueryEvidenceHash) != 64 || p.QueriedAt.IsZero() {
			return ErrCanaryRuntimeRejected
		}
		queryHash = strings.ToLower(p.QueryEvidenceHash)
		queriedAt = p.QueriedAt.UTC().Format(time.RFC3339Nano)
	} else if p.Purpose == PermitFirstBroadcast {
		queryHash = nil
		queriedAt = nil
	} else {
		return ErrCanaryRuntimeRejected
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO canary_send_permits(id,operation_id,attempt_id,authorization_id,authorization_epoch,purpose,artifact_hash,tx_hash,query_evidence_hash,queried_at,state,issued_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,'ISSUED',?,?)`, p.ID, p.OperationID, p.AttemptID, p.AuthorizationID, p.AuthorizationEpoch, p.Purpose, strings.ToLower(p.ArtifactHash), strings.ToLower(p.TxHash), queryHash, queriedAt, p.IssuedAt.UTC().Format(time.RFC3339Nano), p.ExpiresAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ConsumeCanaryPermitAfterGate(ctx context.Context, v RuntimeGateSnapshot, r CanaryGateRequest, permitID, purpose, artifactHash string, now time.Time) (string, error) {
	if s == nil || s.db == nil || ctx == nil || v.Decision != "PASS" || now.IsZero() {
		return "", ErrCanaryRuntimeRejected
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	// This is the authoritative immediate-pre-send check. It deliberately runs
	// in the same transaction that consumes the permit, closing the gap between
	// the orchestrator's diagnostic precheck and the irreversible send boundary.
	var stopped int
	var controlPolicy uint64
	var mode, controlUpdated string
	if err = tx.QueryRowContext(ctx, `SELECT emergency_stopped,policy_version,mode,updated_at FROM canary_control_state WHERE singleton=1 AND chain_id=4663`).Scan(&stopped, &controlPolicy, &mode, &controlUpdated); err != nil {
		return "CONTROL_STATE_UNAVAILABLE", ErrCanaryRuntimeRejected
	}
	if stopped != 0 {
		return "EMERGENCY_STOPPED", ErrCanaryRuntimeRejected
	}
	if mode != CanaryControlled {
		return "CANARY_DISABLED", ErrCanaryRuntimeRejected
	}
	if controlPolicy != r.PolicyVersion {
		return "CONTROL_POLICY_MISMATCH", ErrCanaryRuntimeRejected
	}
	if updated, parseErr := time.Parse(time.RFC3339Nano, controlUpdated); parseErr != nil || uint64(updated.UnixNano()) != v.EmergencyStopRevision {
		return "CONTROL_STATE_CHANGED", ErrCanaryRuntimeRejected
	}
	if r.ChainID != ChainID {
		return "CHAIN_MISMATCH", ErrCanaryRuntimeRejected
	}
	var authState, wallet, environment, build, release, deployment, authExpiry string
	var epoch, policy uint64
	if err = tx.QueryRowContext(ctx, `SELECT state,epoch,wallet_id,policy_version,environment,build_sha,release_id,deployment_id,expires_at FROM canary_runtime_authorizations WHERE id=?`, r.AuthorizationID).Scan(&authState, &epoch, &wallet, &policy, &environment, &build, &release, &deployment, &authExpiry); err != nil {
		return "AUTHORIZATION_UNAVAILABLE", ErrCanaryRuntimeRejected
	}
	if authState != RuntimeAuthorizationArmed {
		return "AUTHORIZATION_NOT_ARMED", ErrCanaryRuntimeRejected
	}
	if epoch != r.AuthorizationEpoch {
		return "AUTHORIZATION_EPOCH_MISMATCH", ErrCanaryRuntimeRejected
	}
	if wallet != r.WalletID || policy != r.PolicyVersion || environment != r.Deployment.Environment || build != strings.ToLower(r.Deployment.BuildSHA) || release != r.Deployment.ReleaseID || deployment != r.Deployment.DeploymentID {
		return "AUTHORIZATION_BINDING_MISMATCH", ErrCanaryRuntimeRejected
	}
	if expires, parseErr := time.Parse(time.RFC3339Nano, authExpiry); parseErr != nil || !now.UTC().Before(expires) {
		return "AUTHORIZATION_EXPIRED", ErrCanaryRuntimeRejected
	}
	var reservationState, operationExpiry string
	var reservationPolicy uint64
	if err = tx.QueryRowContext(ctx, `SELECT rr.state,rr.policy_version,o.expires_at FROM canary_admission_decisions d JOIN canary_risk_reservations rr ON rr.id=d.reservation_id AND rr.operation_id=d.operation_id AND rr.wallet_id=d.wallet_id JOIN canary_admission_sources src ON src.operation_id=d.operation_id AND src.source_hash=rr.source_hash JOIN operations o ON o.id=d.operation_id WHERE d.operation_id=? AND d.decision='ADMITTED' AND d.wallet_id=?`, r.OperationID, r.WalletID).Scan(&reservationState, &reservationPolicy, &operationExpiry); err != nil || (reservationState != "reserved" && reservationState != "frozen") || reservationPolicy != r.PolicyVersion {
		return "C07_ADMISSION_BINDING_INVALID", ErrCanaryRuntimeRejected
	}
	if expires, parseErr := time.Parse(time.RFC3339Nano, operationExpiry); parseErr != nil || !now.UTC().Before(expires) {
		return "TTL_INVALID_OR_EXPIRED", ErrCanaryRuntimeRejected
	}
	if err = insertGateSnapshotTx(ctx, tx, v); err != nil {
		return "", err
	}
	result, err := tx.ExecContext(ctx, `UPDATE canary_send_permits SET state='CONSUMED',consumed_at=? WHERE id=? AND operation_id=? AND attempt_id=? AND authorization_id=? AND authorization_epoch=? AND purpose=? AND artifact_hash=? AND state='ISSUED' AND expires_at>?`, now.UTC().Format(time.RFC3339Nano), permitID, v.OperationID, v.AttemptID, v.AuthorizationID, v.AuthorizationEpoch, purpose, strings.ToLower(artifactHash), now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return "", err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return "PERMIT_IDENTITY_MISMATCH", ErrCanaryRuntimeRejected
	}
	return "", tx.Commit()
}

func insertGateSnapshotTx(ctx context.Context, tx *sql.Tx, v RuntimeGateSnapshot) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO canary_runtime_gate_snapshots(id,operation_id,attempt_id,stage,authorization_id,authorization_epoch,build_sha,release_id,deployment_id,policy_version,policy_hash,gate_evidence_set_hash,emergency_stop_revision,decision,reason_code,checked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ID, v.OperationID, nullText(v.AttemptID), v.Stage, v.AuthorizationID, v.AuthorizationEpoch, strings.ToLower(v.Deployment.BuildSHA), v.Deployment.ReleaseID, v.Deployment.DeploymentID, v.PolicyVersion, strings.ToLower(v.PolicyHash), strings.ToLower(v.GateEvidenceSetHash), v.EmergencyStopRevision, v.Decision, v.ReasonCode, v.CheckedAt.UTC().Format(time.RFC3339Nano))
	return err
}
