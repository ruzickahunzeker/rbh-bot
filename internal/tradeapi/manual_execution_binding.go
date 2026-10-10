package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
)

// Disconnected writer: only SQLite, immutable operator scope and a clock.
// Binding/claim is coordination metadata, never execution or send permission.
type manualExecutionBindingWriter struct {
	store      *Store
	now        func() time.Time
	checkpoint func(string) error // local fault/barrier harness; nil otherwise
}

type manualExecutionBinding struct {
	Operation, Request, ValidationStep, ExecutionStep, Wallet, Address string
	Chain, PolicyVersion, QuoteBlock                                   uint64
	ContractHash, QuoteHash, ExpiresAt                                 string
	Scope                                                              manualAdmissionScope
	PolicyHash, GateHash, CheckedAt                                    string
}

type manualExecutionBindingResult struct {
	OperationID, ExecutionStepID, ContractHash, ExpiresAt, Status string
	Duplicate, ExecutionAuthorized, SendAuthorized                bool
}

const manualExecutionBindingColumns = `operation_id,request_id,validation_step_id,execution_step_id,wallet_id,wallet_address,chain_id,contract_hash,policy_version,quote_block_number,quote_block_hash,expires_at,authorization_id,authorization_epoch,manual_policy_id,manual_policy_hash,gate_evidence_set_hash,environment,build_sha,release_id,deployment_id,checked_at`

func scanManualExecutionBinding(row interface{ Scan(...any) error }) (manualExecutionBinding, error) {
	var b manualExecutionBinding
	err := row.Scan(&b.Operation, &b.Request, &b.ValidationStep, &b.ExecutionStep, &b.Wallet, &b.Address, &b.Chain, &b.ContractHash,
		&b.PolicyVersion, &b.QuoteBlock, &b.QuoteHash, &b.ExpiresAt, &b.Scope.AuthorizationID, &b.Scope.AuthorizationEpoch,
		&b.Scope.ManualPolicyID, &b.PolicyHash, &b.GateHash, &b.Scope.Environment, &b.Scope.BuildSHA, &b.Scope.ReleaseID, &b.Scope.DeploymentID, &b.CheckedAt)
	return b, err
}

func (b manualExecutionBinding) arguments() []any {
	return []any{b.Operation, b.Request, b.ValidationStep, b.ExecutionStep, b.Wallet, b.Address, b.Chain, b.ContractHash, b.PolicyVersion,
		b.QuoteBlock, b.QuoteHash, b.ExpiresAt, b.Scope.AuthorizationID, b.Scope.AuthorizationEpoch, b.Scope.ManualPolicyID,
		b.PolicyHash, b.GateHash, b.Scope.Environment, b.Scope.BuildSHA, b.Scope.ReleaseID, b.Scope.DeploymentID, b.CheckedAt}
}

func executionBindingFor(c ManualExecutionContract, cmd manualAdmissionCommand, stamp string) manualExecutionBinding {
	return manualExecutionBinding{Operation: c.OperationID, Request: c.RequestID, ValidationStep: c.StepID,
		ExecutionStep: "manual-exec:" + c.OperationID, Wallet: c.WalletID, Address: c.WalletAddress, Chain: c.ChainID, ContractHash: c.BindingHash,
		PolicyVersion: c.PolicyVersion, QuoteBlock: c.Output.Resolution.Token.Block.Number, QuoteHash: c.Output.Resolution.Token.Block.Hash,
		ExpiresAt: c.ExpiresAt, Scope: cmd.Scope, PolicyHash: cmd.PolicyHash, GateHash: cmd.GateHash, CheckedAt: stamp}
}

// Historical complete binding remains observable after expiry/revoke. It is
// never a fresh permission and cannot repair/adopt a partial step or lane.
func verifyManualExecutionBinding(ctx context.Context, q queryRow, b manualExecutionBinding, id Identity, scope manualAdmissionScope) (ManualExecutionContract, error) {
	var zero ManualExecutionContract
	cmd, err := scanManualCommand(q.QueryRowContext(ctx, `SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE operation_id=?`, b.Operation))
	if err != nil {
		return zero, manualVerificationError(err, "EXECUTION_BINDING_COMMAND_INCOMPLETE")
	}
	c, err := verifyManualCommandTx(ctx, q, cmd)
	if err != nil {
		return zero, err
	}
	if c.PrincipalID != id.PrincipalID || c.WalletID != id.WalletID || common.HexToAddress(c.WalletAddress) != common.HexToAddress(id.WalletAddress) {
		return zero, ErrNotFound
	}
	if b != executionBindingFor(c, cmd, b.CheckedAt) || b.Scope != scope {
		return zero, ErrConflict
	}
	stamp, se := canonicalManualTime(b.CheckedAt)
	admitted, ae := canonicalManualTime(cmd.CheckedAt)
	if se != nil || ae != nil || stamp.Before(admitted) || ttlRecord(Record{ExpiresAt: b.ExpiresAt}, stamp) != nil {
		return zero, rejectManualInput("EXECUTION_BINDING_TIME_INVALID")
	}
	var complete int
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM manual_execution_binding_integrity WHERE operation_id=?`, b.Operation).Scan(&complete); err != nil || complete != 1 {
		return zero, manualVerificationError(err, "EXECUTION_BINDING_INCOMPLETE")
	}
	unit := manualrisk.Unit{ChainID: c.Input.ChainID, Asset: c.Input.Asset, Decimals: c.Input.Decimals, Kind: manualrisk.TradeInput}
	if _, _, err = verifyManualAuthorizationUsage(ctx, q, scope.AuthorizationID, unit); err != nil {
		return zero, err
	}
	return c, nil
}

func (w *manualExecutionBindingWriter) at(stage string) error {
	if w.checkpoint != nil {
		return w.checkpoint(stage)
	}
	return nil
}

func (w *manualExecutionBindingWriter) bind(ctx context.Context, id Identity, request string, scope manualAdmissionScope) (manualExecutionBindingResult, error) {
	var zero manualExecutionBindingResult
	if w == nil || w.store == nil || w.store.db == nil || w.now == nil || ctx == nil {
		return zero, ErrInvalid
	}
	conn, err := w.store.db.Conn(ctx)
	if err != nil {
		return zero, err
	}
	defer conn.Close()
	if err = verifyManualWriterConnection(ctx, conn); err != nil {
		return zero, err
	}
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return zero, err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	entry := w.now().UTC()
	control, err := readManualControl(ctx, conn, entry)
	if err != nil {
		return zero, err
	}
	if !id.valid() || !validID(request) || !scope.valid() {
		return zero, ErrInvalid
	}
	// Scope lookup before exposing another caller's binding. No current TTL or
	// auth required for an exact historical duplicate; stop still has priority.
	var owned int
	if err = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM manual_http_order_sources WHERE id=? AND principal_id=? AND wallet_id=? AND lower(wallet_address)=lower(?)`, request, id.PrincipalID, id.WalletID, id.WalletAddress).Scan(&owned); err != nil {
		return zero, err
	}
	if owned != 1 {
		return zero, ErrNotFound
	}
	b, err := scanManualExecutionBinding(conn.QueryRowContext(ctx, `SELECT `+manualExecutionBindingColumns+` FROM manual_execution_bindings WHERE request_id=?`, request))
	if err == nil {
		if _, err = verifyManualExecutionBinding(ctx, conn, b, id, scope); err != nil {
			return zero, err
		}
		if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
			return zero, err
		}
		return manualExecutionBindingResult{OperationID: b.Operation, ExecutionStepID: b.ExecutionStep, ContractHash: b.ContractHash,
			ExpiresAt: b.ExpiresAt, Status: "HISTORICAL_EXECUTION_BINDING_NOT_PERMISSION", Duplicate: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zero, err
	}
	expected := ManualAdmissionScope{AuthorizationID: scope.AuthorizationID, AuthorizationEpoch: scope.AuthorizationEpoch,
		ManualPolicyID: scope.ManualPolicyID, Environment: scope.Environment, BuildSHA: scope.BuildSHA,
		ReleaseID: scope.ReleaseID, DeploymentID: scope.DeploymentID}
	v, err := loadManualAdmittedInputTx(ctx, conn, id, request, expected, w.now)
	if err != nil {
		return zero, err
	}
	if v.ControlUpdatedAt != control.UpdatedAt || v.Contract.PolicyVersion != control.PolicyVersion {
		return zero, rejectManualInput("EXECUTION_CONTROL_DRIFT")
	}
	cmd, err := scanManualCommand(conn.QueryRowContext(ctx, `SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE request_id=?`, request))
	if err != nil {
		return zero, err
	}
	if err = w.at("before_binding"); err != nil {
		return zero, err
	}
	b = executionBindingFor(v.Contract, cmd, v.ObservedAt)
	if _, err = conn.ExecContext(ctx, `INSERT INTO manual_execution_bindings(`+manualExecutionBindingColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, b.arguments()...); err != nil {
		return zero, err
	}
	if err = w.at("after_binding"); err != nil {
		return zero, err
	}
	if _, err = verifyManualExecutionBinding(ctx, conn, b, id, scope); err != nil {
		return zero, err
	}
	// Authoritative mutable reads and final clock on the SAME writer transaction.
	final := w.now().UTC()
	checked, ce := canonicalManualTime(b.CheckedAt)
	expiry, ee := canonicalManualTime(v.AuthorizationExpiresAt)
	if ce != nil || ee != nil || final.Before(entry) || final.Before(checked) || !final.Before(expiry) || ttlRecord(Record{ExpiresAt: b.ExpiresAt}, final) != nil {
		return zero, rejectManualInput("EXECUTION_FINAL_TIME_INVALID")
	}
	last, err := readManualControl(ctx, conn, final)
	if err != nil {
		return zero, err
	}
	gate, err := manualGateEvidenceHash(ctx, conn, final)
	if err != nil {
		return zero, err
	}
	if last != control || gate != b.GateHash {
		return zero, rejectManualInput("EXECUTION_FINAL_CONTROL_DRIFT")
	}
	if _, err = loadManualAuthorizationIdentity(ctx, conn, scope, v.Contract, v.ManualPolicy, v.ManualPolicyHash, gate, final); err != nil {
		return zero, err
	}
	var initial int
	if err = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM canary_risk_reservations r JOIN canary_authorization_operation_usage u ON u.operation_id=r.operation_id WHERE r.id=? AND r.state='reserved' AND u.authorization_id=? AND u.state='RESERVED'`, v.ReservationID, scope.AuthorizationID).Scan(&initial); err != nil {
		return zero, err
	}
	if initial != 1 {
		return zero, rejectManualInput("EXECUTION_RESERVATION_DRIFT")
	}
	if err = w.at("before_commit"); err != nil {
		return zero, err
	}
	commitTime := w.now().UTC()
	if commitTime.Before(final) || !commitTime.Before(expiry) || ttlRecord(Record{ExpiresAt: b.ExpiresAt}, commitTime) != nil {
		return zero, rejectManualInput("EXECUTION_FINAL_TIME_INVALID")
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return zero, err
	}
	return manualExecutionBindingResult{OperationID: b.Operation, ExecutionStepID: b.ExecutionStep, ContractHash: b.ContractHash,
		ExpiresAt: b.ExpiresAt, Status: "BOUND_NOT_SIGNED_NOT_PERMISSION"}, nil
}
