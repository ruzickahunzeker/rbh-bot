package trade

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"strings"
	"time"
)

const (
	RuntimeAuthorizationPending = "PENDING"
	RuntimeAuthorizationArmed   = "ARMED"
	PermitFirstBroadcast        = "FIRST_BROADCAST"
	PermitUnknownReplay         = "UNKNOWN_REPLAY"
)

var ErrCanaryRuntimeRejected = errors.New("controlled canary runtime authorization rejected")

type DeploymentIdentity struct {
	Environment, BuildSHA, ReleaseID, DeploymentID string
}

type RuntimeAuthorization struct {
	ID, State, WalletID, GateEvidenceSetHash, AuthorizationRef string
	Deployment                                                 DeploymentIdentity
	Epoch, PolicyVersion, MaxOperations                        uint64
	MaxTotalInput                                              string
	AuthorizedAt, ExpiresAt                                    time.Time
}

type RuntimeGateSnapshot struct {
	ID, OperationID, AttemptID, Stage, AuthorizationID, PolicyHash, Decision, ReasonCode string
	AuthorizationEpoch, PolicyVersion, EmergencyStopRevision                             uint64
	Deployment                                                                           DeploymentIdentity
	GateEvidenceSetHash                                                                  string
	CheckedAt                                                                            time.Time
}

type SendPermit struct {
	ID, OperationID, AttemptID, AuthorizationID, Purpose, ArtifactHash, TxHash string
	QueryEvidenceHash                                                          string
	AuthorizationEpoch                                                         uint64
	IssuedAt, ExpiresAt, QueriedAt                                             time.Time
}

func (s *Store) CreateRuntimeAuthorization(ctx context.Context, a RuntimeAuthorization, now time.Time) (RuntimeAuthorization, error) {
	if s == nil || s.db == nil || ctx == nil || now.IsZero() || a.ID == "" || a.WalletID == "" ||
		a.PolicyVersion == 0 || a.MaxOperations == 0 || a.AuthorizationRef == "" ||
		a.Deployment.Environment == "" || a.Deployment.ReleaseID == "" || a.Deployment.DeploymentID == "" ||
		(len(a.Deployment.BuildSHA) != 40 && len(a.Deployment.BuildSHA) != 64) || len(a.GateEvidenceSetHash) != 64 {
		return RuntimeAuthorization{}, ErrCanaryRuntimeRejected
	}
	maximum, ok := canonicalPositive(a.MaxTotalInput)
	if !ok || maximum.String() != a.MaxTotalInput || a.ExpiresAt.IsZero() || !now.UTC().Before(a.ExpiresAt.UTC()) {
		return RuntimeAuthorization{}, ErrCanaryRuntimeRejected
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RuntimeAuthorization{}, err
	}
	defer tx.Rollback()
	var epoch uint64
	if err = tx.QueryRowContext(ctx, `UPDATE canary_authorization_epochs SET last_epoch=last_epoch+1 WHERE singleton=1 RETURNING last_epoch`).Scan(&epoch); err != nil {
		return RuntimeAuthorization{}, err
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	authorized := a.AuthorizedAt.UTC()
	if authorized.IsZero() {
		authorized = now.UTC()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO canary_runtime_authorizations(id,epoch,mode,environment,chain_id,policy_version,wallet_id,build_sha,release_id,deployment_id,gate_evidence_set_hash,authorization_ref,max_operations,max_total_input,state,authorized_at,expires_at,created_at,updated_at) VALUES(?,?,'CONTROLLED_CANARY',?,4663,?,?,?,?,?,?,?,?,?,'PENDING',?,?,?,?)`,
		a.ID, epoch, a.Deployment.Environment, a.PolicyVersion, a.WalletID, strings.ToLower(a.Deployment.BuildSHA), a.Deployment.ReleaseID, a.Deployment.DeploymentID, strings.ToLower(a.GateEvidenceSetHash), a.AuthorizationRef, a.MaxOperations, a.MaxTotalInput, authorized.Format(time.RFC3339Nano), a.ExpiresAt.UTC().Format(time.RFC3339Nano), stamp, stamp)
	if err != nil {
		return RuntimeAuthorization{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO canary_authorization_usage(authorization_id,operation_count,total_input,updated_at) VALUES(?,0,'0',?)`, a.ID, stamp); err != nil {
		return RuntimeAuthorization{}, err
	}
	if err = insertRuntimeAudit(ctx, tx, "AUTHORIZATION_CREATED", "", "", a.ID, epoch, "PENDING", stamp); err != nil {
		return RuntimeAuthorization{}, err
	}
	if err = tx.Commit(); err != nil {
		return RuntimeAuthorization{}, err
	}
	a.Epoch, a.State, a.AuthorizedAt = epoch, RuntimeAuthorizationPending, authorized
	a.Deployment.BuildSHA = strings.ToLower(a.Deployment.BuildSHA)
	return a, nil
}

func (s *Store) TransitionRuntimeAuthorization(ctx context.Context, id, from, to string, now time.Time) error {
	if s == nil || s.db == nil || ctx == nil || id == "" || now.IsZero() ||
		!((from == "PENDING" && (to == "ARMED" || to == "REVOKED" || to == "EXPIRED")) || (from == "ARMED" && (to == "REVOKED" || to == "EXPIRED" || to == "EXHAUSTED"))) {
		return ErrCanaryRuntimeRejected
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(time.RFC3339Nano)
	query := `UPDATE canary_runtime_authorizations SET state=?,updated_at=? WHERE id=? AND state=?`
	args := []any{to, stamp, id, from}
	if to == RuntimeAuthorizationArmed {
		query += ` AND expires_at>?`
		args = append(args, stamp)
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrCanaryRuntimeRejected
	}
	var epoch uint64
	if err = tx.QueryRowContext(ctx, `SELECT epoch FROM canary_runtime_authorizations WHERE id=?`, id).Scan(&epoch); err != nil {
		return err
	}
	if err = insertRuntimeAudit(ctx, tx, "AUTHORIZATION_STATE_CHANGED", "", "", id, epoch, to, stamp); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ValidateRuntimeAuthorization(ctx context.Context, id, wallet string, policy uint64, d DeploymentIdentity, now time.Time) (RuntimeAuthorization, error) {
	if s == nil || s.db == nil || ctx == nil || now.IsZero() {
		return RuntimeAuthorization{}, ErrCanaryRuntimeRejected
	}
	var a RuntimeAuthorization
	var environment, build, release, deployment, authorizedAt, expiresAt string
	err := s.db.QueryRowContext(ctx, `SELECT id,epoch,state,wallet_id,policy_version,environment,build_sha,release_id,deployment_id,gate_evidence_set_hash,authorization_ref,max_operations,max_total_input,authorized_at,expires_at FROM canary_runtime_authorizations WHERE id=?`, id).Scan(&a.ID, &a.Epoch, &a.State, &a.WalletID, &a.PolicyVersion, &environment, &build, &release, &deployment, &a.GateEvidenceSetHash, &a.AuthorizationRef, &a.MaxOperations, &a.MaxTotalInput, &authorizedAt, &expiresAt)
	if err != nil {
		return RuntimeAuthorization{}, ErrCanaryRuntimeRejected
	}
	a.Deployment = DeploymentIdentity{Environment: environment, BuildSHA: build, ReleaseID: release, DeploymentID: deployment}
	a.AuthorizedAt, _ = time.Parse(time.RFC3339Nano, authorizedAt)
	a.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil || a.State != RuntimeAuthorizationArmed || a.WalletID != wallet || a.PolicyVersion != policy ||
		a.Deployment.Environment != d.Environment || a.Deployment.BuildSHA != strings.ToLower(d.BuildSHA) || a.Deployment.ReleaseID != d.ReleaseID || a.Deployment.DeploymentID != d.DeploymentID || !now.UTC().Before(a.ExpiresAt) {
		return RuntimeAuthorization{}, ErrCanaryRuntimeRejected
	}
	return a, nil
}

func (s *Store) ReserveRuntimeAuthorizationBudget(ctx context.Context, authorizationID, operationID, amount string, now time.Time) (bool, error) {
	requested, ok := canonicalPositive(amount)
	if s == nil || s.db == nil || ctx == nil || authorizationID == "" || operationID == "" || now.IsZero() || !ok {
		return false, ErrCanaryRuntimeRejected
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var state, expires, maximum, total string
	var maxOps, count uint64
	if err = tx.QueryRowContext(ctx, `SELECT a.state,a.expires_at,a.max_operations,a.max_total_input,u.operation_count,u.total_input FROM canary_runtime_authorizations a JOIN canary_authorization_usage u ON u.authorization_id=a.id WHERE a.id=?`, authorizationID).Scan(&state, &expires, &maxOps, &maximum, &count, &total); err != nil {
		return false, ErrCanaryRuntimeRejected
	}
	if state != RuntimeAuthorizationArmed {
		return false, ErrCanaryRuntimeRejected
	}
	expiry, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil || !now.UTC().Before(expiry) {
		return false, ErrCanaryRuntimeRejected
	}
	var existing string
	if err = tx.QueryRowContext(ctx, `SELECT amount FROM canary_authorization_operation_usage WHERE authorization_id=? AND operation_id=?`, authorizationID, operationID).Scan(&existing); err == nil {
		if existing != amount {
			return false, ErrIdempotencyConflict
		}
		return true, tx.Commit()
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	nextTotal, ok := addCanonical(total, amount)
	if !ok {
		return false, ErrCanaryRuntimeRejected
	}
	maxValue, ok := canonicalPositive(maximum)
	if !ok || count+1 > maxOps || requested.Sign() <= 0 || mustBig(nextTotal).Cmp(maxValue) > 0 {
		return false, ErrCanaryRuntimeRejected
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO canary_authorization_operation_usage(authorization_id,operation_id,amount,state,created_at,updated_at) VALUES(?,?,?,'RESERVED',?,?)`, authorizationID, operationID, amount, stamp, stamp); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE canary_authorization_usage SET operation_count=?,total_input=?,updated_at=? WHERE authorization_id=?`, count+1, nextTotal, stamp, authorizationID); err != nil {
		return false, err
	}
	if err = insertRuntimeAudit(ctx, tx, "AUTHORIZATION_BUDGET_RESERVED", operationID, "", authorizationID, 0, "RESERVED", stamp); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

func mustBig(v string) *big.Int { n, _ := new(big.Int).SetString(v, 10); return n }

func (s *Store) RecordRuntimeGateSnapshot(ctx context.Context, v RuntimeGateSnapshot) error {
	if s == nil || s.db == nil || ctx == nil || v.ID == "" || v.OperationID == "" || v.AuthorizationID == "" || v.CheckedAt.IsZero() || len(v.PolicyHash) != 64 || len(v.GateEvidenceSetHash) != 64 || (v.Decision != "PASS" && v.Decision != "REJECT") {
		return ErrCanaryRuntimeRejected
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var matches int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM canary_runtime_authorizations WHERE id=? AND epoch=? AND policy_version=? AND build_sha=? AND release_id=? AND deployment_id=? AND gate_evidence_set_hash=?`, v.AuthorizationID, v.AuthorizationEpoch, v.PolicyVersion, strings.ToLower(v.Deployment.BuildSHA), v.Deployment.ReleaseID, v.Deployment.DeploymentID, strings.ToLower(v.GateEvidenceSetHash)).Scan(&matches)
	if err != nil || matches != 1 {
		return ErrCanaryRuntimeRejected
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO canary_runtime_gate_snapshots(id,operation_id,attempt_id,stage,authorization_id,authorization_epoch,build_sha,release_id,deployment_id,policy_version,policy_hash,gate_evidence_set_hash,emergency_stop_revision,decision,reason_code,checked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ID, v.OperationID, nullText(v.AttemptID), v.Stage, v.AuthorizationID, v.AuthorizationEpoch, strings.ToLower(v.Deployment.BuildSHA), v.Deployment.ReleaseID, v.Deployment.DeploymentID, v.PolicyVersion, strings.ToLower(v.PolicyHash), strings.ToLower(v.GateEvidenceSetHash), v.EmergencyStopRevision, v.Decision, v.ReasonCode, v.CheckedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) IssueSendPermit(ctx context.Context, p SendPermit) error {
	if s == nil || s.db == nil || ctx == nil || p.ID == "" || p.OperationID == "" || p.AttemptID == "" || p.AuthorizationID == "" || p.AuthorizationEpoch == 0 || len(p.ArtifactHash) != 64 || len(p.TxHash) != 66 || p.IssuedAt.IsZero() || !p.IssuedAt.Before(p.ExpiresAt) {
		return ErrCanaryRuntimeRejected
	}
	var qh, qt any
	if p.Purpose == PermitUnknownReplay {
		if len(p.QueryEvidenceHash) != 64 || p.QueriedAt.IsZero() {
			return ErrCanaryRuntimeRejected
		}
		qh = strings.ToLower(p.QueryEvidenceHash)
		qt = p.QueriedAt.UTC().Format(time.RFC3339Nano)
	} else if p.Purpose == PermitFirstBroadcast {
		qh = nil
		qt = nil
	} else {
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
	_, err = tx.ExecContext(ctx, `INSERT INTO canary_send_permits(id,operation_id,attempt_id,authorization_id,authorization_epoch,purpose,artifact_hash,tx_hash,query_evidence_hash,queried_at,state,issued_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,'ISSUED',?,?)`, p.ID, p.OperationID, p.AttemptID, p.AuthorizationID, p.AuthorizationEpoch, p.Purpose, strings.ToLower(p.ArtifactHash), strings.ToLower(p.TxHash), qh, qt, p.IssuedAt.UTC().Format(time.RFC3339Nano), p.ExpiresAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ConsumeSendPermit(ctx context.Context, id, purpose, artifactHash string, now time.Time) error {
	if s == nil || s.db == nil || ctx == nil || now.IsZero() {
		return ErrCanaryRuntimeRejected
	}
	result, err := s.db.ExecContext(ctx, `UPDATE canary_send_permits SET state='CONSUMED',consumed_at=? WHERE id=? AND purpose=? AND artifact_hash=? AND state='ISSUED' AND expires_at>?`, now.UTC().Format(time.RFC3339Nano), id, purpose, strings.ToLower(artifactHash), now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrCanaryRuntimeRejected
	}
	return nil
}

func (s *Store) AcquireCanaryWorkerLease(ctx context.Context, role, environment, holder string, epoch uint64, expires, now time.Time) error {
	if s == nil || s.db == nil || ctx == nil || (role != "SUBMISSION" && role != "RECOVERY") || environment == "" || holder == "" || epoch == 0 || now.IsZero() || !now.Before(expires) {
		return ErrCanaryRuntimeRejected
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO canary_worker_leases(role,environment,holder_id,lease_epoch,expires_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(role,environment) DO UPDATE SET holder_id=excluded.holder_id,lease_epoch=excluded.lease_epoch,expires_at=excluded.expires_at,updated_at=excluded.updated_at WHERE canary_worker_leases.expires_at<=excluded.updated_at AND excluded.lease_epoch>canary_worker_leases.lease_epoch`, role, environment, holder, epoch, expires.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrCanaryRuntimeRejected
	}
	return nil
}

func (s *Store) RecordRuntimeAlert(ctx context.Context, event, operation, attempt, authorization string, epoch uint64, reason, severity string, now time.Time) error {
	if s == nil || s.db == nil || ctx == nil || event == "" || reason == "" || now.IsZero() || (severity != "INFO" && severity != "WARNING" && severity != "CRITICAL") {
		return ErrCanaryRuntimeRejected
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(time.RFC3339Nano)
	auditID := deterministicID("canary-runtime", event, operation, attempt, authorization, reason, stamp)
	if _, err = tx.ExecContext(ctx, `INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at) VALUES(?,?,?,?,?,?,?,'{}',?)`, auditID, event, nullText(operation), nullText(attempt), nullText(authorization), nullInt(epoch), reason, stamp); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) VALUES(?, ?, ?, 'PENDING', ?)`, deterministicID("canary-alert", auditID), auditID, severity, stamp); err != nil {
		return err
	}
	return tx.Commit()
}

func insertRuntimeAudit(ctx context.Context, tx *sql.Tx, event, operation, attempt, authorization string, epoch uint64, reason, stamp string) error {
	id := deterministicID("canary-runtime", event, operation, attempt, authorization, reason, stamp)
	_, err := tx.ExecContext(ctx, `INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at) VALUES(?,?,?,?,?,?,?,'{}',?)`, id, event, nullText(operation), nullText(attempt), nullText(authorization), nullInt(epoch), reason, stamp)
	return err
}

func nullInt(v uint64) any {
	if v == 0 {
		return nil
	}
	return v
}
