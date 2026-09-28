package trade

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	GateExecutionAdmission = "EXECUTION_ADMISSION"
	GatePreSign            = "PRE_SIGN"
	GateFirstBroadcast     = "FIRST_BROADCAST"
	GateImmediatePreSend   = "IMMEDIATE_PRE_SEND"
	GateUnknownReplay      = "UNKNOWN_REPLAY"
)

var (
	ErrCanaryGateRejected     = errors.New("controlled canary gate rejected")
	ErrCanaryQueryOnly        = errors.New("controlled canary recovery is query-only")
	ErrCanaryArtifactMismatch = errors.New("controlled canary artifact identity mismatch")
)

type CanaryGateRequest struct {
	OperationID, AttemptID, AuthorizationID    string
	AuthorizationEpoch, PolicyVersion, ChainID uint64
	WalletID                                   string
	Deployment                                 DeploymentIdentity
}
type CanaryArtifactIdentity struct{ OperationID, AttemptID, TxHash, ArtifactHash string }
type CanaryArtifactVerifier interface {
	VerifyControlledArtifact(context.Context, CanaryArtifactIdentity) error
}
type CanaryRuntimeIdentityReader interface {
	VerifyControlledRuntime(context.Context, string, string) error
}
type UnknownReplayEvidence struct {
	TxFound, ReceiptFound    bool
	NonceState, EvidenceHash string
	QueriedAt                time.Time
}
type UnknownReplayQuerier interface {
	QueryUnknown(context.Context, CanaryArtifactIdentity) (UnknownReplayEvidence, error)
}
type CanaryGateDecision struct{ Stage, Decision, ReasonCode, PermitID string }
type CanaryRuntimeReadiness struct {
	CanaryAdmissionReady                                        bool
	RecoveryQueryPolicyUnblocked, ReconciliationPolicyUnblocked bool
	ReasonCode                                                  string
}

// ControlledCanaryOrchestrator evaluates gates and manages durable permits.
// It intentionally has no signer, SubmissionService or broadcaster dependency.
type ControlledCanaryOrchestrator struct {
	store    *Store
	artifact CanaryArtifactVerifier
	runtime  CanaryRuntimeIdentityReader
	querier  UnknownReplayQuerier
	now      func() time.Time
}

func NewControlledCanaryOrchestrator(s *Store, a CanaryArtifactVerifier, r CanaryRuntimeIdentityReader, q UnknownReplayQuerier) (*ControlledCanaryOrchestrator, error) {
	if s == nil || a == nil || r == nil || q == nil {
		return nil, ErrInvalidRequest
	}
	return &ControlledCanaryOrchestrator{store: s, artifact: a, runtime: r, querier: q, now: time.Now}, nil
}

func (o *ControlledCanaryOrchestrator) ExecutionAdmission(c context.Context, r CanaryGateRequest) (CanaryGateDecision, error) {
	a, v, x := o.evaluate(c, r, GateExecutionAdmission)
	return o.finish(c, r, a, v, GateExecutionAdmission, CanaryArtifactIdentity{}, x)
}
func (o *ControlledCanaryOrchestrator) PreSign(c context.Context, r CanaryGateRequest) (CanaryGateDecision, error) {
	a, v, x := o.evaluate(c, r, GatePreSign)
	return o.finish(c, r, a, v, GatePreSign, CanaryArtifactIdentity{}, x)
}

func (o *ControlledCanaryOrchestrator) FirstBroadcast(c context.Context, r CanaryGateRequest, x CanaryArtifactIdentity) (CanaryGateDecision, error) {
	a, v, reason := o.evaluate(c, r, GateFirstBroadcast)
	if reason == "" {
		if e := o.verifyArtifactAndRuntime(c, r, x); e != nil {
			reason = reasonForCanaryError(e)
		}
	}
	if reason == "" {
		latest, found, e := o.store.LatestSubmission(c, x.AttemptID)
		if e != nil {
			reason = "SUBMISSION_STATE_UNAVAILABLE"
		} else if found {
			if latest.State == "broadcast_unknown" || latest.State == "submitting" {
				reason = "AMBIGUOUS_SUBMISSION_REQUIRES_QUERY"
			} else {
				reason = "FIRST_BROADCAST_NOT_AVAILABLE"
			}
		}
	}
	if reason != "" {
		return o.finish(c, r, a, v, GateFirstBroadcast, x, reason)
	}
	n := o.now().UTC()
	id := deterministicID("canary-permit", PermitFirstBroadcast, x.AttemptID, r.AuthorizationID, strconv.FormatUint(r.AuthorizationEpoch, 10))
	snapshot, e := o.snapshotValue(c, r, a, GateFirstBroadcast, x, v, "")
	if e != nil {
		return CanaryGateDecision{Stage: GateFirstBroadcast, Decision: "REJECT", ReasonCode: "SNAPSHOT_BUILD_FAILED"}, e
	}
	if e = o.store.IssueCanaryPermitAfterGate(c, snapshot, SendPermit{ID: id, OperationID: x.OperationID, AttemptID: x.AttemptID, AuthorizationID: r.AuthorizationID, AuthorizationEpoch: r.AuthorizationEpoch, Purpose: PermitFirstBroadcast, ArtifactHash: x.ArtifactHash, TxHash: x.TxHash, IssuedAt: n, ExpiresAt: n.Add(30 * time.Second)}); e != nil {
		return CanaryGateDecision{Stage: GateFirstBroadcast, Decision: "REJECT", ReasonCode: "PERMIT_ISSUE_FAILED"}, e
	}
	return CanaryGateDecision{Stage: GateFirstBroadcast, Decision: "PASS", ReasonCode: "CANARY_GATE_PASS", PermitID: id}, nil
}

func (o *ControlledCanaryOrchestrator) UnknownReplay(c context.Context, r CanaryGateRequest, x CanaryArtifactIdentity) (CanaryGateDecision, error) {
	// Emergency stop has priority over query work.
	_, _, _, stop := o.readControl(c)
	if stop != "" {
		a, _ := o.readAuthorization(c, r)
		return o.finish(c, r, a, 0, GateUnknownReplay, x, stop)
	}
	latest, found, e := o.store.LatestSubmission(c, x.AttemptID)
	if e != nil || !found || latest.State != "broadcast_unknown" {
		a, v, z := o.evaluate(c, r, GateUnknownReplay)
		if z == "" {
			z = "NOT_BROADCAST_UNKNOWN"
		}
		return o.finish(c, r, a, v, GateUnknownReplay, x, z)
	}
	proof, qerr := o.querier.QueryUnknown(c, x)
	if qerr != nil || proof.QueriedAt.IsZero() || len(proof.EvidenceHash) != 64 {
		a, v, z := o.evaluate(c, r, GateUnknownReplay)
		if z == "" {
			z = "QUERY_EVIDENCE_UNAVAILABLE"
		}
		return o.finish(c, r, a, v, GateUnknownReplay, x, z)
	}
	if proof.TxFound || proof.ReceiptFound || proof.NonceState != "RESERVED_UNRESOLVED" {
		a, v, z := o.evaluate(c, r, GateUnknownReplay)
		if z == "" {
			z = "QUERY_REQUIRES_RECONCILIATION"
		}
		d, _ := o.finish(c, r, a, v, GateUnknownReplay, x, z)
		return d, ErrCanaryQueryOnly
	}
	a, v, reason := o.evaluate(c, r, GateUnknownReplay)
	if reason == "" {
		if e = o.verifyArtifactAndRuntime(c, r, x); e != nil {
			reason = reasonForCanaryError(e)
		}
	}
	if reason != "" {
		return o.finish(c, r, a, v, GateUnknownReplay, x, reason)
	}
	n := o.now().UTC()
	id := deterministicID("canary-permit", PermitUnknownReplay, x.AttemptID, r.AuthorizationID, strconv.FormatUint(r.AuthorizationEpoch, 10), proof.EvidenceHash)
	snapshot, e := o.snapshotValue(c, r, a, GateUnknownReplay, x, v, "")
	if e != nil {
		return CanaryGateDecision{Stage: GateUnknownReplay, Decision: "REJECT", ReasonCode: "SNAPSHOT_BUILD_FAILED"}, e
	}
	if e = o.store.IssueCanaryPermitAfterGate(c, snapshot, SendPermit{ID: id, OperationID: x.OperationID, AttemptID: x.AttemptID, AuthorizationID: r.AuthorizationID, AuthorizationEpoch: r.AuthorizationEpoch, Purpose: PermitUnknownReplay, ArtifactHash: x.ArtifactHash, TxHash: x.TxHash, QueryEvidenceHash: proof.EvidenceHash, QueriedAt: proof.QueriedAt, IssuedAt: n, ExpiresAt: n.Add(30 * time.Second)}); e != nil {
		return CanaryGateDecision{Stage: GateUnknownReplay, Decision: "REJECT", ReasonCode: "PERMIT_ISSUE_FAILED"}, e
	}
	return CanaryGateDecision{Stage: GateUnknownReplay, Decision: "PASS", ReasonCode: "CANARY_GATE_PASS", PermitID: id}, nil
}

// ImmediatePreSend consumes a one-shot permit after re-reading mutable controls.
// It returns no raw bytes and cannot send.
func (o *ControlledCanaryOrchestrator) ImmediatePreSend(c context.Context, r CanaryGateRequest, permit, purpose string, x CanaryArtifactIdentity) (CanaryGateDecision, error) {
	a, v, reason := o.evaluate(c, r, GateImmediatePreSend)
	if reason == "" && purpose != PermitFirstBroadcast && purpose != PermitUnknownReplay {
		reason = "INVALID_PERMIT_PURPOSE"
	}
	if reason == "" {
		if e := o.verifyArtifactAndRuntime(c, r, x); e != nil {
			reason = reasonForCanaryError(e)
		}
	}
	if reason == "" {
		var p, h, tx, state string
		e := o.store.db.QueryRowContext(c, `SELECT purpose,artifact_hash,tx_hash,state FROM canary_send_permits WHERE id=? AND operation_id=? AND attempt_id=? AND authorization_id=? AND authorization_epoch=?`, permit, x.OperationID, x.AttemptID, r.AuthorizationID, r.AuthorizationEpoch).Scan(&p, &h, &tx, &state)
		if e != nil || state != "ISSUED" || p != purpose || !strings.EqualFold(h, x.ArtifactHash) || !strings.EqualFold(tx, x.TxHash) {
			reason = "PERMIT_IDENTITY_MISMATCH"
		}
	}
	if reason != "" {
		return o.finish(c, r, a, v, GateImmediatePreSend, x, reason)
	}
	snapshot, e := o.snapshotValue(c, r, a, GateImmediatePreSend, x, v, "")
	if e != nil {
		return CanaryGateDecision{Stage: GateImmediatePreSend, Decision: "REJECT", ReasonCode: "SNAPSHOT_BUILD_FAILED"}, e
	}
	if reason, e = o.store.ConsumeCanaryPermitAfterGate(c, snapshot, r, permit, purpose, x.ArtifactHash, o.now().UTC()); e != nil {
		if reason != "" {
			return CanaryGateDecision{Stage: GateImmediatePreSend, Decision: "REJECT", ReasonCode: reason}, ErrCanaryGateRejected
		}
		return CanaryGateDecision{Stage: GateImmediatePreSend, Decision: "REJECT", ReasonCode: "PERMIT_CONSUME_FAILED"}, e
	}
	return CanaryGateDecision{Stage: GateImmediatePreSend, Decision: "PASS", ReasonCode: "CANARY_GATE_PASS", PermitID: permit}, nil
}

func (o *ControlledCanaryOrchestrator) Readiness(c context.Context, r CanaryGateRequest) CanaryRuntimeReadiness {
	// These fields deliberately describe policy only. Recovery service health is
	// owned and reported by the existing PR-006 recovery path.
	v := CanaryRuntimeReadiness{RecoveryQueryPolicyUnblocked: true, ReconciliationPolicyUnblocked: true}
	_, _, _, z := o.readControl(c)
	if z == "" {
		_, z = o.readAuthorization(c, r)
	}
	v.CanaryAdmissionReady = z == ""
	v.ReasonCode = z
	return v
}

func (o *ControlledCanaryOrchestrator) evaluate(c context.Context, r CanaryGateRequest, stage string) (RuntimeAuthorization, uint64, string) {
	_, policy, revision, reason := o.readControl(c)
	a, z := o.readAuthorization(c, r)
	if reason == "" {
		reason = z
	}
	if reason == "" && policy != r.PolicyVersion {
		reason = "CONTROL_POLICY_MISMATCH"
	}
	if reason == "" {
		reason = o.operationReason(c, r, stage)
	}
	if reason == "" {
		if e := o.store.CheckOperationTTL(c, r.OperationID, o.now().UTC()); e != nil {
			reason = "TTL_INVALID_OR_EXPIRED"
		}
	}
	if reason == "" && stage == GateExecutionAdmission {
		var amount string
		if e := o.store.db.QueryRowContext(c, `SELECT amount FROM canary_admission_sources WHERE operation_id=?`, r.OperationID).Scan(&amount); e != nil {
			reason = "ADMISSION_SOURCE_UNAVAILABLE"
		} else if _, e = o.store.ReserveRuntimeAuthorizationBudget(c, r.AuthorizationID, r.OperationID, amount, o.now().UTC()); e != nil {
			reason = "AUTHORIZATION_BUDGET_REJECTED"
		}
	}
	return a, revision, reason
}
func (o *ControlledCanaryOrchestrator) finish(c context.Context, r CanaryGateRequest, a RuntimeAuthorization, revision uint64, stage string, x CanaryArtifactIdentity, reason string) (CanaryGateDecision, error) {
	if a.ID != "" {
		if e := o.snapshot(c, r, a, stage, x, revision, reason); e != nil {
			return CanaryGateDecision{Stage: stage, Decision: "REJECT", ReasonCode: "SNAPSHOT_PERSIST_FAILED"}, e
		}
	}
	if reason != "" {
		return CanaryGateDecision{Stage: stage, Decision: "REJECT", ReasonCode: reason}, ErrCanaryGateRejected
	}
	return CanaryGateDecision{Stage: stage, Decision: "PASS", ReasonCode: "CANARY_GATE_PASS"}, nil
}

func (o *ControlledCanaryOrchestrator) readControl(c context.Context) (bool, uint64, uint64, string) {
	var stop int
	var policy uint64
	var mode, updated string
	if e := o.store.db.QueryRowContext(c, `SELECT emergency_stopped,policy_version,mode,updated_at FROM canary_control_state WHERE singleton=1 AND chain_id=4663`).Scan(&stop, &policy, &mode, &updated); e != nil {
		return false, 0, 0, "CONTROL_STATE_UNAVAILABLE"
	}
	t, e := time.Parse(time.RFC3339Nano, updated)
	if e != nil {
		return false, 0, 0, "CONTROL_STATE_UNAVAILABLE"
	}
	revision := uint64(t.UnixNano())
	if stop != 0 {
		return true, policy, revision, "EMERGENCY_STOPPED"
	}
	if mode != CanaryControlled {
		return false, policy, revision, "CANARY_DISABLED"
	}
	return false, policy, revision, ""
}
func (o *ControlledCanaryOrchestrator) readAuthorization(c context.Context, r CanaryGateRequest) (RuntimeAuthorization, string) {
	var a RuntimeAuthorization
	var env, build, release, deployment, authorized, expires string
	e := o.store.db.QueryRowContext(c, `SELECT id,epoch,state,wallet_id,policy_version,environment,build_sha,release_id,deployment_id,gate_evidence_set_hash,authorization_ref,max_operations,max_total_input,authorized_at,expires_at FROM canary_runtime_authorizations WHERE id=?`, r.AuthorizationID).Scan(&a.ID, &a.Epoch, &a.State, &a.WalletID, &a.PolicyVersion, &env, &build, &release, &deployment, &a.GateEvidenceSetHash, &a.AuthorizationRef, &a.MaxOperations, &a.MaxTotalInput, &authorized, &expires)
	if e != nil {
		return a, "AUTHORIZATION_UNAVAILABLE"
	}
	a.Deployment = DeploymentIdentity{Environment: env, BuildSHA: build, ReleaseID: release, DeploymentID: deployment}
	a.AuthorizedAt, _ = time.Parse(time.RFC3339Nano, authorized)
	a.ExpiresAt, e = time.Parse(time.RFC3339Nano, expires)
	if a.State != RuntimeAuthorizationArmed {
		return a, "AUTHORIZATION_NOT_ARMED"
	}
	if e != nil || !o.now().UTC().Before(a.ExpiresAt) {
		return a, "AUTHORIZATION_EXPIRED"
	}
	if a.Epoch != r.AuthorizationEpoch {
		return a, "AUTHORIZATION_EPOCH_MISMATCH"
	}
	if r.ChainID != ChainID {
		return a, "CHAIN_MISMATCH"
	}
	if a.WalletID != r.WalletID {
		return a, "WALLET_MISMATCH"
	}
	if a.PolicyVersion != r.PolicyVersion {
		return a, "POLICY_MISMATCH"
	}
	if a.Deployment.Environment != r.Deployment.Environment || a.Deployment.BuildSHA != strings.ToLower(r.Deployment.BuildSHA) || a.Deployment.ReleaseID != r.Deployment.ReleaseID || a.Deployment.DeploymentID != r.Deployment.DeploymentID {
		return a, "DEPLOYMENT_IDENTITY_MISMATCH"
	}
	return a, ""
}
func (o *ControlledCanaryOrchestrator) operationReason(c context.Context, r CanaryGateRequest, stage string) string {
	var wallet, decision, reservationID, reservationOperation, reservationWallet, reservationState, reservationSource, sourceHash string
	var policy uint64
	e := o.store.db.QueryRowContext(c, `SELECT d.wallet_id,d.policy_version,d.decision,d.reservation_id,r.operation_id,r.wallet_id,r.state,COALESCE(r.source_hash,''),s.source_hash FROM canary_admission_decisions d JOIN canary_risk_reservations r ON r.id=d.reservation_id JOIN canary_admission_sources s ON s.operation_id=d.operation_id WHERE d.operation_id=? ORDER BY d.created_at LIMIT 1`, r.OperationID).Scan(&wallet, &policy, &decision, &reservationID, &reservationOperation, &reservationWallet, &reservationState, &reservationSource, &sourceHash)
	if e != nil || decision != "ADMITTED" || reservationID == "" || wallet != r.WalletID || policy != r.PolicyVersion || reservationOperation != r.OperationID || reservationWallet != r.WalletID || (reservationState != "reserved" && reservationState != "frozen") || !strings.EqualFold(reservationSource, sourceHash) {
		return "C07_ADMISSION_BINDING_INVALID"
	}
	if stage != GateExecutionAdmission {
		var n int
		if e = o.store.db.QueryRowContext(c, `SELECT COUNT(*) FROM canary_authorization_operation_usage WHERE authorization_id=? AND operation_id=? AND state IN ('RESERVED','CONSUMED','FROZEN')`, r.AuthorizationID, r.OperationID).Scan(&n); e != nil || n != 1 {
			return "AUTHORIZATION_USAGE_MISSING"
		}
	}
	return ""
}
func (o *ControlledCanaryOrchestrator) verifyArtifactAndRuntime(c context.Context, r CanaryGateRequest, x CanaryArtifactIdentity) error {
	if x.OperationID != r.OperationID || x.AttemptID == "" || x.AttemptID != r.AttemptID || len(x.ArtifactHash) != 64 || len(x.TxHash) != 66 {
		return ErrCanaryArtifactMismatch
	}
	if e := o.artifact.VerifyControlledArtifact(c, x); e != nil {
		return ErrCanaryArtifactMismatch
	}
	var address, codeHash string
	if e := o.store.db.QueryRowContext(c, `SELECT contract_address,runtime_code_hash FROM canary_admission_sources WHERE operation_id=? AND wallet_id=? AND policy_version=?`, r.OperationID, r.WalletID, r.PolicyVersion).Scan(&address, &codeHash); e != nil {
		return ErrStaleState
	}
	if e := o.runtime.VerifyControlledRuntime(c, address, codeHash); e != nil {
		return ErrStaleState
	}
	return nil
}
func (o *ControlledCanaryOrchestrator) snapshot(c context.Context, r CanaryGateRequest, a RuntimeAuthorization, stage string, x CanaryArtifactIdentity, revision uint64, reason string) error {
	v, e := o.snapshotValue(c, r, a, stage, x, revision, reason)
	if e != nil {
		return e
	}
	return o.store.RecordRuntimeGateSnapshot(c, v)
}
func (o *ControlledCanaryOrchestrator) snapshotValue(c context.Context, r CanaryGateRequest, a RuntimeAuthorization, stage string, x CanaryArtifactIdentity, revision uint64, reason string) (RuntimeGateSnapshot, error) {
	var policyHash string
	if e := o.store.db.QueryRowContext(c, `SELECT policy_hash FROM canary_policies WHERE version=?`, a.PolicyVersion).Scan(&policyHash); e != nil {
		return RuntimeGateSnapshot{}, e
	}
	decision, code := "PASS", "CANARY_GATE_PASS"
	if reason != "" {
		decision, code = "REJECT", reason
	}
	id := deterministicID("canary-gate", stage, r.OperationID, x.AttemptID, r.AuthorizationID, strconv.FormatUint(a.Epoch, 10))
	return RuntimeGateSnapshot{ID: id, OperationID: r.OperationID, AttemptID: x.AttemptID, Stage: stage, AuthorizationID: r.AuthorizationID, AuthorizationEpoch: a.Epoch, Deployment: a.Deployment, PolicyVersion: a.PolicyVersion, PolicyHash: policyHash, GateEvidenceSetHash: a.GateEvidenceSetHash, EmergencyStopRevision: revision, Decision: decision, ReasonCode: code, CheckedAt: o.now().UTC()}, nil
}
func reasonForCanaryError(e error) string {
	if errors.Is(e, ErrCanaryArtifactMismatch) {
		return "ARTIFACT_IDENTITY_MISMATCH"
	}
	return "RUNTIME_IDENTITY_UNVERIFIABLE"
}
