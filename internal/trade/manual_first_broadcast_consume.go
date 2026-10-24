package trade

import (
	"context"
	"strings"
	"time"
)

// ConsumeManualFirstBroadcastPermit authorizes only the metadata transition
// ISSUED -> CONSUMED for an exact B2.3 MANUAL FIRST_BROADCAST permit. It does
// not create a submission, allocate a nonce, sign, or call a transport.
func (s *Store) ConsumeManualFirstBroadcastPermit(ctx context.Context, r CanaryGateRequest, permitID, artifactHash string, now time.Time) (string, error) {
	if s == nil || s.db == nil || ctx == nil || permitID == "" || now.IsZero() {
		return "INVALID_REQUEST", ErrCanaryRuntimeRejected
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var op, attempt, auth, purpose, storedArtifact, txHash, state, issuedAt, expiresAt string
	var epoch uint64
	err = tx.QueryRowContext(ctx, `SELECT operation_id,attempt_id,authorization_id,authorization_epoch,purpose,artifact_hash,tx_hash,state,issued_at,expires_at FROM canary_send_permits WHERE id=?`, permitID).Scan(&op, &attempt, &auth, &epoch, &purpose, &storedArtifact, &txHash, &state, &issuedAt, &expiresAt)
	if err != nil || op != r.OperationID || attempt != r.AttemptID || auth != r.AuthorizationID || epoch != r.AuthorizationEpoch || purpose != PermitFirstBroadcast || state != "ISSUED" || !strings.EqualFold(storedArtifact, artifactHash) {
		return "PERMIT_IDENTITY_MISMATCH", ErrCanaryRuntimeRejected
	}
	var stopped int
	var policy uint64
	var mode, controlUpdated string
	if err = tx.QueryRowContext(ctx, `SELECT emergency_stopped,policy_version,mode,updated_at FROM canary_control_state WHERE singleton=1 AND chain_id=?`, ChainID).Scan(&stopped, &policy, &mode, &controlUpdated); err != nil {
		return "CONTROL_STATE_UNAVAILABLE", ErrCanaryRuntimeRejected
	}
	if stopped != 0 || mode != CanaryControlled {
		return "EMERGENCY_STOPPED", ErrCanaryRuntimeRejected
	}
	if policy != r.PolicyVersion || r.ChainID != ChainID {
		return "POLICY_OR_CHAIN_MISMATCH", ErrCanaryRuntimeRejected
	}
	var authState, wallet, environment, build, release, deployment, authExpiry string
	var currentEpoch, authPolicy uint64
	if err = tx.QueryRowContext(ctx, `SELECT state,epoch,wallet_id,policy_version,environment,build_sha,release_id,deployment_id,expires_at FROM canary_runtime_authorizations WHERE id=?`, auth).Scan(&authState, &currentEpoch, &wallet, &authPolicy, &environment, &build, &release, &deployment, &authExpiry); err != nil {
		return "AUTHORIZATION_UNAVAILABLE", ErrCanaryRuntimeRejected
	}
	if authState != RuntimeAuthorizationArmed || currentEpoch != epoch || wallet != r.WalletID || authPolicy != r.PolicyVersion || environment != r.Deployment.Environment || build != strings.ToLower(r.Deployment.BuildSHA) || release != r.Deployment.ReleaseID || deployment != r.Deployment.DeploymentID {
		return "AUTHORIZATION_BINDING_MISMATCH", ErrCanaryRuntimeRejected
	}
	var reservationState, operationExpiry string
	var reservationPolicy uint64
	if err = tx.QueryRowContext(ctx, `SELECT rr.state,o.expires_at,rr.policy_version FROM manual_admission_commands c JOIN manual_execution_bindings b ON b.operation_id=c.operation_id JOIN canary_risk_reservations rr ON rr.id=c.reservation_id AND rr.operation_id=c.operation_id AND rr.wallet_id=b.wallet_id JOIN manual_reservation_units unit ON unit.reservation_id=rr.id AND unit.operation_id=c.operation_id JOIN canary_authorization_operation_usage usage ON usage.operation_id=c.operation_id AND usage.authorization_id=b.authorization_id JOIN operations o ON o.id=c.operation_id WHERE c.operation_id=? AND b.wallet_id=? AND b.authorization_id=? AND b.authorization_epoch=? AND b.policy_version=? AND usage.state='RESERVED'`, op, r.WalletID, auth, epoch, r.PolicyVersion).Scan(&reservationState, &operationExpiry, &reservationPolicy); err != nil || reservationState != "reserved" || reservationPolicy != r.PolicyVersion {
		return "MANUAL_ADMISSION_BINDING_INVALID", ErrCanaryRuntimeRejected
	}
	var permitCmp, authCmp, operationCmp int
	stamp := now.UTC().Format(time.RFC3339Nano)
	if err = tx.QueryRowContext(ctx, `SELECT manual_time_cmp(?,?),manual_time_cmp(?,?),manual_time_cmp(?,?)`, stamp, expiresAt, stamp, authExpiry, stamp, operationExpiry).Scan(&permitCmp, &authCmp, &operationCmp); err != nil || permitCmp >= 0 || authCmp >= 0 || operationCmp >= 0 {
		return "PERMIT_EXPIRED", ErrCanaryRuntimeRejected
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO manual_first_broadcast_consume_scopes(permit_id,operation_id,attempt_id,authorization_id,authorization_epoch,artifact_hash,opened_at) VALUES(?,?,?,?,?,?,?)`, permitID, op, attempt, auth, epoch, strings.ToLower(artifactHash), stamp); err != nil {
		return "CONSUME_SCOPE_FAILED", err
	}
	result, err := tx.ExecContext(ctx, `UPDATE canary_send_permits SET state='CONSUMED',consumed_at=? WHERE id=? AND state='ISSUED'`, stamp, permitID)
	if err != nil {
		return "CONSUME_FAILED", err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return "PERMIT_ALREADY_CONSUMED", ErrCanaryRuntimeRejected
	}
	if err = tx.Commit(); err != nil {
		return "CONSUME_COMMIT_FAILED", err
	}
	return "", nil
}
