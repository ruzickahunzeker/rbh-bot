package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
)

// ManualUnsignedEnvelope is an exact projection of the verified durable
// parameters. It deliberately has no nonce, signature, raw bytes or tx hash.
// It is not a signable transaction or an authorization capability.
type ManualUnsignedEnvelope struct {
	ChainID   uint64 `json:"chain_id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Value     string `json:"value"`
	Data      string `json:"data"`
	GasLimit  uint64 `json:"gas_limit"`
	GasFeeCap string `json:"gas_fee_cap"`
	GasTipCap string `json:"gas_tip_cap"`
}

// ManualBoundSigningInput is detached candidate metadata, never a nonce claim
// or current execution/sign/send permission. Future writers must reload it and
// enforce their own authoritative controls immediately before each side effect.
type ManualBoundSigningInput struct {
	Status                 string                  `json:"status"`
	Contract               ManualExecutionContract `json:"contract"`
	Envelope               ManualUnsignedEnvelope  `json:"envelope"`
	Scope                  ManualAdmissionScope    `json:"scope"`
	ExecutionStepID        string                  `json:"execution_step_id"`
	BoundAt                string                  `json:"bound_at"`
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
	SigningAuthorized      bool                    `json:"signing_authorized"`
	SendAuthorized         bool                    `json:"send_authorized"`
}

// Disconnected: SQLite, expected operator-owned scope and clock only. No HTTP
// registration, backend, nonce allocator, signer, cipher or submission service.
type ManualBoundSigningInputReader struct {
	store *Store
	scope ManualAdmissionScope
	now   func() time.Time
}

func NewManualBoundSigningInputReader(store *Store, scope ManualAdmissionScope) (*ManualBoundSigningInputReader, error) {
	if store == nil || store.db == nil || !scope.internal().valid() {
		return nil, ErrInvalid
	}
	return &ManualBoundSigningInputReader{store: store, scope: scope, now: time.Now}, nil
}

// Load accepts identity and the original durable request ID, not security or
// transaction fields. All reads share one snapshot; no writes even on rejection.
func (a *ManualBoundSigningInputReader) Load(ctx context.Context, id Identity, requestID string) (ManualBoundSigningInput, error) {
	if a == nil || a.store == nil || a.store.db == nil || a.now == nil || ctx == nil {
		return ManualBoundSigningInput{}, ErrInvalid
	}
	tx, err := a.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ManualBoundSigningInput{}, err
	}
	defer tx.Rollback()
	v, err := loadManualBoundSigningInputTx(ctx, tx, id, requestID, a.scope, a.now)
	if err != nil {
		return ManualBoundSigningInput{}, err
	}
	if err = tx.Commit(); err != nil {
		// database/sql may automatically roll back a canceled read transaction
		// before Commit. Preserve operational context AND the commit cause.
		if errors.Is(err, sql.ErrTxDone) && ctx.Err() != nil {
			err = errors.Join(ctx.Err(), err)
		}
		return ManualBoundSigningInput{}, err
	}
	return v, nil
}

// SELECT-only. Transaction lifetime belongs to Load or a controlled local
// harness. A detached input/read snapshot must never substitute for a writer.
func loadManualBoundSigningInputTx(ctx context.Context, q queryRow, id Identity, requestID string, expected ManualAdmissionScope, now func() time.Time) (ManualBoundSigningInput, error) {
	var zero ManualBoundSigningInput
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
	var address string
	if err = q.QueryRowContext(ctx, `SELECT wallet_address FROM manual_http_order_sources WHERE id=? AND principal_id=? AND wallet_id=?`, requestID, id.PrincipalID, id.WalletID).Scan(&address); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return zero, ErrNotFound
		}
		return zero, err
	}
	if common.HexToAddress(address) != common.HexToAddress(id.WalletAddress) {
		return zero, ErrNotFound
	}
	b, err := scanManualExecutionBinding(q.QueryRowContext(ctx, `SELECT `+manualExecutionBindingColumns+` FROM manual_execution_bindings WHERE request_id=?`, requestID))
	if err != nil {
		return zero, manualVerificationError(err, "BOUND_INPUT_BINDING_INCOMPLETE")
	}
	c, err := verifyManualExecutionBinding(ctx, q, b, id, expected.internal())
	if err != nil {
		return zero, err
	}
	bound, be := canonicalManualTime(b.CheckedAt)
	if be != nil || stamp.Before(bound) || ttlRecord(Record{ExpiresAt: c.ExpiresAt}, stamp) != nil {
		return zero, rejectManualInput("BOUND_INPUT_TIME_INVALID_OR_EXPIRED")
	}
	if control.PolicyVersion != c.PolicyVersion {
		return zero, rejectManualInput("CONTROL_POLICY_MISMATCH")
	}
	p, digest, err := loadManualPolicy(ctx, q, expected.ManualPolicyID, c, stamp)
	if err != nil {
		return zero, err
	}
	gate, err := manualGateEvidenceHash(ctx, q, stamp)
	if err != nil {
		return zero, err
	}
	if digest != b.PolicyHash || gate != b.GateHash {
		return zero, rejectManualInput("BOUND_INPUT_POLICY_OR_GATE_DRIFT")
	}
	auth, err := loadManualAuthorizationIdentity(ctx, q, expected.internal(), c, p, digest, gate, stamp)
	if err != nil {
		return zero, err
	}
	unit := manualrisk.Unit{ChainID: c.Input.ChainID, Asset: c.Input.Asset, Decimals: c.Input.Decimals, Kind: manualrisk.TradeInput}
	used, total, err := verifyManualAuthorizationUsage(ctx, q, expected.AuthorizationID, unit)
	if err != nil {
		return zero, err
	}
	var reservationID, reservationState, usageState string
	err = q.QueryRowContext(ctx, `SELECT r.id,r.state,u.state FROM manual_admission_commands cmd
 JOIN canary_risk_reservations r ON r.id=cmd.reservation_id AND r.operation_id=cmd.operation_id AND r.wallet_id=?
 JOIN canary_authorization_operation_usage u ON u.operation_id=cmd.operation_id AND u.authorization_id=cmd.authorization_id
 WHERE cmd.operation_id=? AND cmd.authorization_id=?`, c.WalletID, c.OperationID, expected.AuthorizationID).Scan(&reservationID, &reservationState, &usageState)
	if err != nil {
		return zero, manualVerificationError(err, "BOUND_INPUT_RESERVATION_INCOMPLETE")
	}
	if reservationState != "reserved" || usageState != "RESERVED" {
		return zero, rejectManualInput("BOUND_INPUT_NOT_INITIAL_RESERVED")
	}
	if err = checkManualBoundInputWallet(ctx, q, id, c, b.ExecutionStep); err != nil {
		return zero, err
	}
	final := now().UTC()
	expiry, ee := canonicalManualTime(auth.ExpiresAt)
	if ee != nil || final.Before(stamp) || !final.Before(expiry) || ttlRecord(Record{ExpiresAt: c.ExpiresAt}, final) != nil {
		return zero, rejectManualInput("BOUND_INPUT_TIME_INVALID_OR_EXPIRED")
	}
	// Projection only: no defaults, conversion, percentage resolution or nonce.
	o := c.Output
	envelope := ManualUnsignedEnvelope{ChainID: c.ChainID, From: o.From, To: o.To, Value: o.Value, Data: o.Data,
		GasLimit: o.GasLimit, GasFeeCap: o.GasFeeCap, GasTipCap: o.GasTipCap}
	return ManualBoundSigningInput{Status: "BOUND_SIGNING_INPUT_CANDIDATE_NOT_PERMISSION", Contract: c, Envelope: envelope,
		Scope: expected, ExecutionStepID: b.ExecutionStep, BoundAt: b.CheckedAt, ReservationID: reservationID,
		ReservationState: reservationState, ManualPolicy: p, ManualPolicyHash: digest, GateEvidenceSetHash: gate,
		UsedOperations: used, UsedInput: total, AuthorizationExpiresAt: auth.ExpiresAt,
		ControlUpdatedAt: control.UpdatedAt, ObservedAt: final.Format(time.RFC3339Nano)}, nil
}

func checkManualBoundInputWallet(ctx context.Context, q queryRow, id Identity, c ManualExecutionContract, executionStep string) error {
	var address string
	var blocked bool
	// Its own complete M2 lane and C3-B risk reservation are expected. Any
	// attempt (even failed), other unresolved step or financial claim is not B1.
	err := q.QueryRowContext(ctx, `SELECT w.address,
 EXISTS(SELECT 1 FROM execution_reservations WHERE wallet_id=w.id AND status IN ('reserved','signed','frozen')) OR
 EXISTS(SELECT 1 FROM canary_risk_reservations WHERE wallet_id=w.id AND operation_id!=? AND state IN ('reserved','frozen')) OR
 EXISTS(SELECT 1 FROM transaction_attempts t JOIN execution_steps s ON s.id=t.step_id WHERE s.operation_id=?) OR
 EXISTS(SELECT 1 FROM execution_steps s JOIN operations o ON o.id=s.operation_id
 WHERE (s.wallet_id=w.id OR o.wallet_id=w.id) AND s.id!=? AND s.status IN ('manual_execution_bound','nonce_reserved','signing','signed','submitted','broadcast_unknown','reconciling','manual_resolution'))
 FROM dry_run_wallets w JOIN canary_wallet_allowlist a ON a.wallet_id=w.id AND a.chain_id=4663
 AND a.policy_version=? AND a.enabled=1 AND lower(a.address)=lower(w.address)
 WHERE w.id=? AND w.chain_id=4663 AND w.enabled=1`, c.OperationID, c.OperationID, executionStep, c.PolicyVersion, id.WalletID).Scan(&address, &blocked)
	if err != nil {
		return manualVerificationError(err, "BOUND_INPUT_WALLET_UNAVAILABLE")
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
