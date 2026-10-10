package tradeapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
)

// Private, disconnected composition. No HTTP, validator backend, signer,
// execution engine, submission, nonce, permit, authorization grant or network.
type manualAdmissionWriter struct {
	store *Store
	now   func() time.Time
	// Local fault/serialization harness; nil in ordinary composition. Receives
	// no connection and cannot expose a provisional decision to the caller.
	checkpoint func(string) error
}

type manualAdmissionResult struct {
	OperationID, ReservationID, ContractHash, ExpiresAt string
	Status                                              string
	Duplicate                                           bool
}

type manualAdmissionCommand struct {
	Operation, Request, Step, Reservation                      string
	Scope                                                      manualAdmissionScope
	PolicyHash, GateHash, ContractRaw, ContractHash, CheckedAt string
}

const manualCommandColumns = `operation_id,request_id,step_id,reservation_id,authorization_id,authorization_epoch,manual_policy_id,manual_policy_hash,environment,build_sha,release_id,deployment_id,gate_evidence_set_hash,contract_json,contract_hash,checked_at`

func scanManualCommand(row interface{ Scan(...any) error }) (manualAdmissionCommand, error) {
	var c manualAdmissionCommand
	err := row.Scan(&c.Operation, &c.Request, &c.Step, &c.Reservation, &c.Scope.AuthorizationID, &c.Scope.AuthorizationEpoch,
		&c.Scope.ManualPolicyID, &c.PolicyHash, &c.Scope.Environment, &c.Scope.BuildSHA, &c.Scope.ReleaseID,
		&c.Scope.DeploymentID, &c.GateHash, &c.ContractRaw, &c.ContractHash, &c.CheckedAt)
	return c, err
}

func (c manualAdmissionCommand) arguments() []any {
	return []any{c.Operation, c.Request, c.Step, c.Reservation, c.Scope.AuthorizationID, c.Scope.AuthorizationEpoch,
		c.Scope.ManualPolicyID, c.PolicyHash, c.Scope.Environment, c.Scope.BuildSHA, c.Scope.ReleaseID,
		c.Scope.DeploymentID, c.GateHash, c.ContractRaw, c.ContractHash, c.CheckedAt}
}

func verifyManualCommandTx(ctx context.Context, q queryRow, cmd manualAdmissionCommand) (ManualExecutionContract, error) {
	var stored ManualExecutionContract
	if json.Unmarshal([]byte(cmd.ContractRaw), &stored) != nil || encode(stored) != cmd.ContractRaw || !cmd.Scope.valid() {
		return ManualExecutionContract{}, rejectManualInput("ADMISSION_BINDING_INVALID")
	}
	id := Identity{PrincipalID: stored.PrincipalID, WalletID: stored.WalletID, WalletAddress: stored.WalletAddress}
	c, err := verifyManualContractParametersTx(ctx, q, id, cmd.Request)
	if err != nil || encode(c) != cmd.ContractRaw || c.BindingHash != cmd.ContractHash || c.OperationID != cmd.Operation || c.StepID != cmd.Step || cmd.Reservation != "manual-risk:"+cmd.Operation {
		return ManualExecutionContract{}, rejectManualInput("ADMISSION_CONTRACT_INVALID")
	}
	stamp, err := canonicalManualTime(cmd.CheckedAt)
	if err != nil || ttlRecord(Record{ExpiresAt: c.ExpiresAt}, stamp) != nil {
		return ManualExecutionContract{}, rejectManualInput("ADMISSION_TIME_INVALID")
	}
	p, digest, err := loadManualPolicy(ctx, q, cmd.Scope.ManualPolicyID, c, stamp)
	if err != nil || digest != cmd.PolicyHash || p.ID != cmd.Scope.ManualPolicyID {
		return ManualExecutionContract{}, rejectManualInput("ADMISSION_POLICY_INVALID")
	}
	gate, err := manualGateEvidenceHash(ctx, q, stamp)
	if err != nil || gate != cmd.GateHash {
		return ManualExecutionContract{}, rejectManualInput("ADMISSION_GATE_INVALID")
	}
	var complete int
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM manual_admission_complete WHERE operation_id=?`, cmd.Operation).Scan(&complete); err != nil || complete != 1 {
		return ManualExecutionContract{}, rejectManualInput("ADMISSION_LEDGER_INCOMPLETE")
	}
	// Historical verification does not require active authorization, but its
	// immutable identity/unit/time/caps MUST remain the original declaration.
	var n int
	err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM canary_runtime_authorizations a JOIN manual_authorization_units u ON u.authorization_id=a.id
 WHERE a.id=? AND a.epoch=? AND a.wallet_id=? AND a.chain_id=4663 AND a.mode='CONTROLLED_CANARY' AND a.policy_version=?
 AND a.environment=? AND a.build_sha=? AND a.release_id=? AND a.deployment_id=? AND a.gate_evidence_set_hash=?
 AND u.authorization_epoch=a.epoch AND u.manual_policy_id=? AND u.manual_policy_hash=?
 AND u.source_mode='MANUAL' AND u.chain_id=4663 AND u.input_asset=? AND u.input_decimals=? AND u.accounting_kind='TRADE_INPUT'
 AND manual_time_cmp(?,a.authorized_at)>=0 AND manual_time_cmp(?,a.created_at)>=0 AND manual_time_cmp(?,a.expires_at)<0`,
		cmd.Scope.AuthorizationID, cmd.Scope.AuthorizationEpoch, c.WalletID, c.PolicyVersion, cmd.Scope.Environment,
		cmd.Scope.BuildSHA, cmd.Scope.ReleaseID, cmd.Scope.DeploymentID, cmd.GateHash, cmd.Scope.ManualPolicyID,
		cmd.PolicyHash, c.Input.Asset, c.Input.Decimals, cmd.CheckedAt, cmd.CheckedAt, cmd.CheckedAt).Scan(&n)
	if err != nil || n != 1 {
		return ManualExecutionContract{}, rejectManualInput("ADMISSION_AUTHORIZATION_IDENTITY_INVALID")
	}
	return c, nil
}

// Lifetime allowance is NEVER refunded by reservation/usage state changes.
// Every row must have a schema-complete command plus independently verified
// immutable C1/C2/C3-A/policy/auth identities. No repair/reset on corruption.
func verifyManualAuthorizationUsage(ctx context.Context, q queryRow, auth string, unit manualrisk.Unit) (uint64, manualrisk.Quantity, error) {
	zero := manualrisk.Quantity{Unit: unit, Units: "0"}
	var used, rows uint64
	var total, raw string
	err := q.QueryRowContext(ctx, `SELECT operation_count,total_input,
 (SELECT COUNT(*) FROM canary_authorization_operation_usage WHERE authorization_id=?),
 (SELECT json_group_array(operation_id) FROM manual_admission_commands WHERE authorization_id=?)
 FROM canary_authorization_usage WHERE authorization_id=?`, auth, auth, auth).Scan(&used, &total, &rows, &raw)
	var operations []string
	if err != nil || json.Unmarshal([]byte(raw), &operations) != nil || used != rows || rows != uint64(len(operations)) {
		return 0, zero, rejectManualInput("AUTHORIZATION_USAGE_INCOMPLETE")
	}
	sum := zero
	for _, operation := range operations {
		cmd, err := scanManualCommand(q.QueryRowContext(ctx, `SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE operation_id=? AND authorization_id=?`, operation, auth))
		if err != nil {
			return 0, zero, rejectManualInput("AUTHORIZATION_USAGE_INCOMPLETE")
		}
		c, err := verifyManualCommandTx(ctx, q, cmd)
		if err != nil {
			return 0, zero, err
		}
		var amount, state string
		if err = q.QueryRowContext(ctx, `SELECT amount,state FROM canary_authorization_operation_usage WHERE authorization_id=? AND operation_id=?`, auth, operation).Scan(&amount, &state); err != nil || amount != c.Input.Units ||
			(state != "RESERVED" && state != "CONSUMED" && state != "FROZEN" && state != "RELEASED") {
			return 0, zero, rejectManualInput("AUTHORIZATION_USAGE_INVALID")
		}
		sum, err = manualrisk.Add(sum, manualrisk.Quantity{Unit: manualrisk.Unit{ChainID: c.Input.ChainID, Asset: c.Input.Asset, Decimals: c.Input.Decimals, Kind: manualrisk.TradeInput}, Units: amount})
		if err != nil {
			return 0, zero, rejectManualInput("AUTHORIZATION_USAGE_UNIT_OR_OVERFLOW")
		}
	}
	if sum.Units != total || sum.Validate() != nil {
		return 0, zero, rejectManualInput("AUTHORIZATION_USAGE_CONSERVATION")
	}
	var maxOperations uint64
	var maxInput string
	if err = q.QueryRowContext(ctx, `SELECT max_operations,max_total_input FROM canary_runtime_authorizations WHERE id=?`, auth).Scan(&maxOperations, &maxInput); err != nil || used > maxOperations ||
		manualrisk.CheckCap(zero, sum, manualrisk.Quantity{Unit: unit, Units: maxInput}) != nil {
		return 0, zero, rejectManualInput("AUTHORIZATION_USAGE_CAP")
	}
	return used, sum, nil
}

func verifyManualWriterConnection(ctx context.Context, conn *sql.Conn) error {
	// These PRAGMAs are connection-local, unlike scalar registration. Never
	// assume a replacement connection inherited storage.Open's controls.
	for _, check := range []struct {
		name string
		want int
	}{{"foreign_keys", 1}, {"synchronous", 2}, {"busy_timeout", 5000}} {
		var got int
		if err := conn.QueryRowContext(ctx, "PRAGMA "+check.name).Scan(&got); err != nil || got != check.want {
			return rejectManualInput("WRITER_CONNECTION_UNVERIFIABLE")
		}
	}
	var journal string
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil || !strings.EqualFold(journal, "wal") {
		return rejectManualInput("WRITER_CONNECTION_UNVERIFIABLE")
	}
	return nil
}

func checkManualAdmissionExposure(v manualAdmissionInputs, e manualExposure) error {
	limit, err := v.Policy.Limit(v.Input.Unit)
	if err != nil {
		return err
	}
	for _, item := range []struct {
		used manualrisk.Quantity
		cap  string
	}{
		{e.WalletInput[v.Input.Unit], limit.MaxWalletActive}, {e.GlobalInput[v.Input.Unit], limit.MaxGlobalActive},
	} {
		used := item.used
		if used.Unit == (manualrisk.Unit{}) {
			used = manualrisk.Quantity{Unit: v.Input.Unit, Units: "0"}
		}
		if manualrisk.CheckCap(used, v.Input, manualrisk.Quantity{Unit: v.Input.Unit, Units: item.cap}) != nil {
			return rejectManualInput("MANUAL_ACTIVE_INPUT_CAP")
		}
	}
	if e.WalletActiveOperations != 0 || e.WalletWindowOperations >= v.Policy.MaxWalletOperationsWindow || e.GlobalWindowOperations >= v.Policy.MaxGlobalOperationsWindow {
		return rejectManualInput("MANUAL_OPERATION_COUNT_OR_LANE_CAP")
	}
	if manualrisk.CheckCap(e.WalletOutlay, v.Outlay, manualrisk.Quantity{Unit: v.Outlay.Unit, Units: v.Policy.MaxNativeWalletOutlay}) != nil ||
		manualrisk.CheckCap(e.GlobalOutlay, v.Outlay, manualrisk.Quantity{Unit: v.Outlay.Unit, Units: v.Policy.MaxNativeGlobalOutlay}) != nil {
		return rejectManualInput("MANUAL_ACTIVE_NATIVE_OUTLAY_CAP")
	}
	outlay, err := manualrisk.Add(e.WalletOutlay, v.Outlay)
	if err != nil || manualrisk.CheckRetainedBalance(v.Contract.Output.Resolution.NativeBalance, outlay.Units, v.Policy.MinNativeBalance) != nil {
		return rejectManualInput("MANUAL_RETAINED_BALANCE_LIMIT")
	}
	return nil
}

func (w *manualAdmissionWriter) at(stage string) error {
	if w.checkpoint != nil {
		return w.checkpoint(stage)
	}
	return nil
}

func (w *manualAdmissionWriter) admit(ctx context.Context, id Identity, request string, scope manualAdmissionScope) (manualAdmissionResult, error) {
	var zero manualAdmissionResult
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
	// Exact durable duplicate before rejecting its own retained wallet lane.
	cmd, err := scanManualCommand(conn.QueryRowContext(ctx, `SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE request_id=?`, request))
	if err == nil {
		c, err := verifyManualCommandTx(ctx, conn, cmd)
		if err != nil {
			return zero, err
		}
		if cmd.Scope != scope || c.PrincipalID != id.PrincipalID || c.WalletID != id.WalletID || common.HexToAddress(c.WalletAddress) != common.HexToAddress(id.WalletAddress) {
			return zero, ErrConflict
		}
		unit := manualrisk.Unit{ChainID: c.Input.ChainID, Asset: c.Input.Asset, Decimals: c.Input.Decimals, Kind: manualrisk.TradeInput}
		if _, _, err = verifyManualAuthorizationUsage(ctx, conn, scope.AuthorizationID, unit); err != nil {
			return zero, err
		}
		if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
			return zero, err
		}
		// History ONLY. Expiry/revoke does not erase history, nor does dedupe
		// renew permission. Emergency stop retains highest rejection priority.
		return manualAdmissionResult{c.OperationID, cmd.Reservation, c.BindingHash, c.ExpiresAt, "HISTORICAL_ADMISSION_NOT_PERMISSION", true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zero, err
	}
	v, err := loadManualAdmissionInputsTx(ctx, conn, id, request, scope, w.now)
	if err != nil {
		return zero, err
	}
	if v.Control != control {
		return zero, rejectManualInput("CONTROL_DRIFT")
	}
	exposure, err := loadManualExposureTx(ctx, conn, id.WalletID, v.Policy, entry)
	if err != nil {
		return zero, err
	}
	if err = checkManualAdmissionExposure(v, exposure); err != nil {
		return zero, err
	}
	if err = w.at("before_command"); err != nil {
		return zero, err
	}
	c := v.Contract
	cmd = manualAdmissionCommand{Operation: c.OperationID, Request: request, Step: c.StepID, Reservation: "manual-risk:" + c.OperationID,
		Scope: scope, PolicyHash: v.PolicyHash, GateHash: v.Authorization.GateEvidenceSetHash, ContractRaw: encode(c), ContractHash: c.BindingHash, CheckedAt: v.CheckedAt}
	if _, err = conn.ExecContext(ctx, `INSERT INTO manual_admission_commands(`+manualCommandColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, cmd.arguments()...); err != nil {
		return zero, err
	}
	if err = w.at("after_command"); err != nil {
		return zero, err
	}
	if _, err = verifyManualCommandTx(ctx, conn, cmd); err != nil {
		return zero, err
	}
	if _, _, err = verifyManualAuthorizationUsage(ctx, conn, scope.AuthorizationID, v.Input.Unit); err != nil {
		return zero, err
	}
	// Recheck after all writes, directly before COMMIT on THIS writer. Other
	// stop/revoke writers cannot commit in this interval (BEGIN IMMEDIATE).
	final := w.now().UTC()
	checked, err := canonicalManualTime(v.CheckedAt)
	expiry, ee := canonicalManualTime(v.Authorization.ExpiresAt)
	if err != nil || ee != nil || final.Before(entry) || final.Before(checked) || !final.Before(expiry) || ttlRecord(Record{ExpiresAt: c.ExpiresAt}, final) != nil {
		return zero, rejectManualInput("FINAL_TIME_INVALID_OR_EXPIRED")
	}
	last, err := readManualControl(ctx, conn, final)
	if err != nil {
		return zero, err
	}
	gate, err := manualGateEvidenceHash(ctx, conn, final)
	if err != nil || gate != cmd.GateHash || last != control {
		return zero, rejectManualInput("FINAL_CONTROL_OR_GATE_DRIFT")
	}
	if _, err = loadManualAuthorizationIdentity(ctx, conn, scope, c, v.Policy, v.PolicyHash, gate, final); err != nil {
		return zero, err
	}
	if err = w.at("before_commit"); err != nil {
		return zero, err
	}
	// SQL verification itself consumes time. Check the clock AFTER the last
	// query/barrier, not before it; equality is expired and clock reversal fails.
	commitTime := w.now().UTC()
	if commitTime.Before(final) || commitTime.Before(checked) || !commitTime.Before(expiry) || ttlRecord(Record{ExpiresAt: c.ExpiresAt}, commitTime) != nil {
		return zero, rejectManualInput("FINAL_TIME_INVALID_OR_EXPIRED")
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return zero, err
	}
	return manualAdmissionResult{c.OperationID, cmd.Reservation, c.BindingHash, c.ExpiresAt, "ADMITTED_NOT_SIGNED_NOT_SUBMITTED", false}, nil
}
