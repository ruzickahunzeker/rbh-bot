package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
)

// ManualAdmittedInput is a detached, read-snapshot candidate, not a claim,
// reservation, execution gate, signing permission or send permit. Consumers
// MUST reload and enforce mutable controls on their own future writer boundary.
type ManualAdmittedInput struct {
	Status                 string                  `json:"status"`
	Contract               ManualExecutionContract `json:"contract"`
	Scope                  ManualAdmissionScope    `json:"scope"`
	ReservationID          string                  `json:"reservation_id"`
	ReservationState       string                  `json:"reservation_state"`
	ManualPolicy           manualrisk.Policy       `json:"manual_policy"`
	ManualPolicyHash       string                  `json:"manual_policy_hash"`
	GateEvidenceSetHash    string                  `json:"gate_evidence_set_hash"`
	UsedOperations         uint64                  `json:"used_operations"`
	UsedInput              manualrisk.Quantity     `json:"used_input"`
	AuthorizationExpiresAt string                  `json:"authorization_expires_at"`
	ControlUpdatedAt       string                  `json:"control_updated_at"`
	ObservedAt             string                  `json:"observed_at"`
	ExecutionAuthorized    bool                    `json:"execution_authorized"`
	SendAuthorized         bool                    `json:"send_authorized"`
}

// ManualAdmittedInputReader has only SQLite, expected operator scope and a
// clock. It is disconnected from HTTP/startup and all economic dependencies.
type ManualAdmittedInputReader struct {
	store *Store
	scope ManualAdmissionScope
	now   func() time.Time
}

func NewManualAdmittedInputReader(store *Store, scope ManualAdmissionScope) (*ManualAdmittedInputReader, error) {
	if store == nil || store.db == nil || !scope.internal().valid() {
		return nil, ErrInvalid
	}
	return &ManualAdmittedInputReader{store: store, scope: scope, now: time.Now}, nil
}

// Load accepts caller identity and the original durable request ID only. It
// does not accept calldata, quote, fee, amount, expiry or an authorization grant.
// All reads use one transaction. Snapshot success is NEVER current permission.
func (a *ManualAdmittedInputReader) Load(ctx context.Context, id Identity, requestID string) (ManualAdmittedInput, error) {
	if a == nil || a.store == nil || a.store.db == nil || a.now == nil || ctx == nil || !id.valid() || !validID(requestID) {
		return ManualAdmittedInput{}, ErrInvalid
	}
	tx, err := a.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ManualAdmittedInput{}, err
	}
	defer tx.Rollback()
	v, err := loadManualAdmittedInputTx(ctx, tx, id, requestID, a.scope, a.now)
	if err != nil {
		return ManualAdmittedInput{}, err
	}
	if err = tx.Commit(); err != nil {
		// Cancellation may let database/sql roll back before Commit, which
		// reports ErrTxDone rather than the original context cause. Preserve
		// both; unrelated commit/DB errors remain unchanged.
		if errors.Is(err, sql.ErrTxDone) && ctx.Err() != nil {
			err = errors.Join(ctx.Err(), err)
		}
		return ManualAdmittedInput{}, err
	}
	return v, nil
}

// SELECT-only helper; transaction ownership stays with Load/the local harness.
func loadManualAdmittedInputTx(ctx context.Context, q queryRow, id Identity, requestID string, expected ManualAdmissionScope, now func() time.Time) (ManualAdmittedInput, error) {
	var zero ManualAdmittedInput
	if ctx == nil || q == nil || now == nil {
		return zero, ErrInvalid
	}
	stamp := now().UTC()
	control, err := readManualControl(ctx, q, stamp)
	if err != nil {
		return zero, err
	}
	if !id.valid() || !validID(requestID) || !expected.internal().valid() {
		return zero, ErrInvalid
	}
	// Scope principal/wallet before reading a command owned by another caller.
	var address string
	err = q.QueryRowContext(ctx, `SELECT wallet_address FROM manual_http_order_sources WHERE id=? AND principal_id=? AND wallet_id=?`, requestID, id.PrincipalID, id.WalletID).Scan(&address)
	if err != nil {
		if err == sql.ErrNoRows {
			return zero, ErrNotFound
		}
		return zero, err
	}
	if common.HexToAddress(address) != common.HexToAddress(id.WalletAddress) {
		return zero, ErrNotFound
	}
	cmd, err := scanManualCommand(q.QueryRowContext(ctx, `SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE request_id=?`, requestID))
	if err != nil {
		return zero, manualVerificationError(err, "ADMISSION_LEDGER_INCOMPLETE")
	}
	if cmd.Scope != expected.internal() {
		return zero, ErrConflict
	}
	c, err := verifyManualCommandTx(ctx, q, cmd)
	if err != nil {
		return zero, err
	}
	if c.PrincipalID != id.PrincipalID || c.WalletID != id.WalletID || common.HexToAddress(c.WalletAddress) != common.HexToAddress(id.WalletAddress) {
		return zero, ErrNotFound
	}
	admitted, err := canonicalManualTime(cmd.CheckedAt)
	if err != nil || stamp.Before(admitted) || ttlRecord(Record{ExpiresAt: c.ExpiresAt}, stamp) != nil {
		return zero, rejectManualInput("CANDIDATE_TIME_INVALID_OR_EXPIRED")
	}
	if control.PolicyVersion != c.PolicyVersion {
		return zero, rejectManualInput("CONTROL_POLICY_MISMATCH")
	}
	p, digest, err := loadManualPolicy(ctx, q, cmd.Scope.ManualPolicyID, c, stamp)
	if err != nil {
		return zero, err
	}
	gate, err := manualGateEvidenceHash(ctx, q, stamp)
	if err != nil {
		return zero, err
	}
	auth, err := loadManualAuthorizationIdentity(ctx, q, cmd.Scope, c, p, digest, gate, stamp)
	if err != nil {
		return zero, err
	}
	// Verify the ALREADY consumed lifetime allowance; do not call the admission
	// budget checker, which would add this input for a second time at cap equality.
	unit := manualrisk.Unit{ChainID: c.Input.ChainID, Asset: c.Input.Asset, Decimals: c.Input.Decimals, Kind: manualrisk.TradeInput}
	used, total, err := verifyManualAuthorizationUsage(ctx, q, cmd.Scope.AuthorizationID, unit)
	if err != nil {
		return zero, err
	}
	var reservation, usage string
	err = q.QueryRowContext(ctx, `SELECT rr.state,u.state FROM canary_risk_reservations rr JOIN canary_authorization_operation_usage u ON u.operation_id=rr.operation_id WHERE rr.id=? AND rr.operation_id=? AND rr.wallet_id=? AND u.authorization_id=?`, cmd.Reservation, cmd.Operation, c.WalletID, cmd.Scope.AuthorizationID).Scan(&reservation, &usage)
	if err != nil {
		return zero, manualVerificationError(err, "ADMISSION_LEDGER_INCOMPLETE")
	}
	if reservation != "reserved" || usage != "RESERVED" {
		return zero, rejectManualInput("CANDIDATE_NOT_INITIAL_RESERVED")
	}
	if err = checkManualCandidateWallet(ctx, q, id, c); err != nil {
		return zero, err
	}
	final := now().UTC()
	expiry, err := canonicalManualTime(auth.ExpiresAt)
	if err != nil || final.Before(stamp) || !final.Before(expiry) || ttlRecord(Record{ExpiresAt: c.ExpiresAt}, final) != nil {
		return zero, rejectManualInput("CANDIDATE_TIME_INVALID_OR_EXPIRED")
	}
	return ManualAdmittedInput{Status: "ADMITTED_INPUT_CANDIDATE_NOT_PERMISSION", Contract: c, Scope: expected,
		ReservationID: cmd.Reservation, ReservationState: reservation, ManualPolicy: p, ManualPolicyHash: digest,
		GateEvidenceSetHash: gate, UsedOperations: used, UsedInput: total, AuthorizationExpiresAt: auth.ExpiresAt,
		ControlUpdatedAt: control.UpdatedAt, ObservedAt: final.Format(time.RFC3339Nano)}, nil
}

func checkManualCandidateWallet(ctx context.Context, q queryRow, id Identity, c ManualExecutionContract) error {
	var address string
	var blocked bool
	// Its own C3-B risk reservation is expected, not a reason to reject. Any
	// economic lane/attempt or OTHER active risk reservation is not initial work.
	err := q.QueryRowContext(ctx, `SELECT w.address,
 EXISTS(SELECT 1 FROM execution_wallet_lanes WHERE wallet_id=w.id AND state!='idle') OR
 EXISTS(SELECT 1 FROM execution_reservations WHERE wallet_id=w.id AND status IN ('reserved','signed','frozen')) OR
 EXISTS(SELECT 1 FROM canary_risk_reservations WHERE wallet_id=w.id AND operation_id!=? AND state IN ('reserved','frozen')) OR
 EXISTS(SELECT 1 FROM transaction_attempts t JOIN execution_steps s ON s.id=t.step_id WHERE s.operation_id=?) OR
 EXISTS(SELECT 1 FROM manual_execution_bindings WHERE operation_id=?) OR
 EXISTS(SELECT 1 FROM execution_steps WHERE wallet_id=w.id AND status IN ('manual_execution_bound','nonce_reserved','signing','signed','submitted','broadcast_unknown','reconciling','manual_resolution'))
 FROM dry_run_wallets w JOIN canary_wallet_allowlist a ON a.wallet_id=w.id AND a.chain_id=4663 AND a.policy_version=? AND a.enabled=1 AND lower(a.address)=lower(w.address)
 WHERE w.id=? AND w.chain_id=4663 AND w.enabled=1`, c.OperationID, c.OperationID, c.OperationID, c.PolicyVersion, id.WalletID).Scan(&address, &blocked)
	if err != nil {
		return manualVerificationError(err, "CANDIDATE_WALLET_UNAVAILABLE")
	}
	if blocked || common.HexToAddress(address) != common.HexToAddress(id.WalletAddress) {
		return ErrWallet
	}
	var matches int
	err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM canary_contract_allowlist ca JOIN canary_token_allowlist ta ON ta.policy_version=ca.policy_version
 WHERE ca.policy_version=? AND ca.role='CURVE' AND ca.protocol='PONS_V2_CURVE' AND ca.enabled=1 AND ca.baseline_ref!=''
 AND lower(ca.address)=? AND ca.runtime_code_hash=? AND ta.protocol='PONS_V2_CURVE' AND ta.enabled=1 AND lower(ta.token_address)=?`,
		c.PolicyVersion, c.Output.Resolution.Token.Curve, c.Output.Resolution.Token.CurveRuntimeCodeHash, c.Output.Resolution.Token.Address).Scan(&matches)
	if err != nil {
		return err
	}
	if matches != 1 {
		return ErrPolicy
	}
	return nil
}
