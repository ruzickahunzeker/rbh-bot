package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
)

// Intentionally package-private and disconnected. An existing RPCBackend does
// not implement this local-only boundary, and no production adapter is added.
type manualNonceObserver interface {
	ObserveManualNonce(context.Context, manualnonce.Facts) (manualnonce.Observation, error)
}

type manualNonceClaimWriter struct {
	store      *Store
	observer   manualNonceObserver
	now        func() time.Time
	checkpoint func(string) error // controlled local barriers/faults only
}

type manualNonceClaim struct {
	Operation, Step, Attempt, Wallet, Address, Nonce, Reservation, ContractHash, ObservationRaw, ObservationHash, CheckedAt string
}

const manualNonceClaimColumns = `operation_id,execution_step_id,attempt_id,wallet_id,wallet_address,nonce,reservation_id,contract_hash,observation_json,observation_hash,checked_at`

type manualNonceClaimResult struct {
	OperationID, ExecutionStepID, AttemptID, Nonce, ContractHash, ExpiresAt, Status string
	Duplicate, ExecutionAuthorized, SigningAuthorized, SendAuthorized               bool
}

func scanManualNonceClaim(row interface{ Scan(...any) error }) (manualNonceClaim, error) {
	var q manualNonceClaim
	err := row.Scan(&q.Operation, &q.Step, &q.Attempt, &q.Wallet, &q.Address, &q.Nonce, &q.Reservation, &q.ContractHash, &q.ObservationRaw, &q.ObservationHash, &q.CheckedAt)
	return q, err
}

func (q manualNonceClaim) arguments() []any {
	return []any{q.Operation, q.Step, q.Attempt, q.Wallet, q.Address, q.Nonce, q.Reservation, q.ContractHash, q.ObservationRaw, q.ObservationHash, q.CheckedAt}
}

func manualNonceFacts(c ManualExecutionContract, b manualExecutionBinding) manualnonce.Facts {
	return manualnonce.Facts{OperationID: c.OperationID, RequestID: c.RequestID, BindingHash: c.BindingHash, ChainID: c.ChainID,
		WalletID: c.WalletID, WalletAddress: strings.ToLower(c.WalletAddress), ContractAddress: strings.ToLower(c.Output.To),
		RuntimeCodeHash: c.Output.Resolution.Token.CurveRuntimeCodeHash, QuoteBlock: c.Output.Resolution.Token.Block.Number,
		QuoteHash: c.Output.Resolution.Token.Block.Hash, BoundAt: b.CheckedAt, ExpiresAt: c.ExpiresAt}
}

// Claimed-phase historical verification; it never treats the old initial M2
// view as phase-aware and never grants fresh sign/send permission.
func verifyManualNonceClaim(ctx context.Context, query queryRow, q manualNonceClaim, id Identity, scope manualAdmissionScope) (ManualExecutionContract, error) {
	var zero ManualExecutionContract
	b, err := scanManualExecutionBinding(query.QueryRowContext(ctx, `SELECT `+manualExecutionBindingColumns+` FROM manual_execution_bindings WHERE operation_id=?`, q.Operation))
	if err != nil {
		return zero, manualVerificationError(err, "NONCE_BINDING_INCOMPLETE")
	}
	cmd, err := scanManualCommand(query.QueryRowContext(ctx, `SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE operation_id=?`, q.Operation))
	if err != nil {
		return zero, manualVerificationError(err, "NONCE_COMMAND_INCOMPLETE")
	}
	c, err := verifyManualCommandTx(ctx, query, cmd)
	if err != nil {
		return zero, err
	}
	if c.PrincipalID != id.PrincipalID || c.WalletID != id.WalletID || !strings.EqualFold(c.WalletAddress, id.WalletAddress) {
		return zero, ErrNotFound
	}
	if b != executionBindingFor(c, cmd, b.CheckedAt) || b.Scope != scope {
		return zero, ErrConflict
	}
	if q.Step != b.ExecutionStep || q.Attempt != "manual-nonce:"+q.Operation || q.Wallet != b.Wallet || q.Address != strings.ToLower(b.Address) || q.Reservation != cmd.Reservation || q.ContractHash != b.ContractHash {
		return zero, rejectManualInput("NONCE_CLAIM_IDENTITY_INVALID")
	}
	bound, be := canonicalManualTime(b.CheckedAt)
	admitted, ae := canonicalManualTime(cmd.CheckedAt)
	if be != nil || ae != nil || bound.Before(admitted) || ttlRecord(Record{ExpiresAt: c.ExpiresAt}, bound) != nil {
		return zero, rejectManualInput("NONCE_BOUND_TIME_INVALID")
	}
	if _, err = manualnonce.Verify(q.ObservationRaw, q.ObservationHash, q.Nonce, manualNonceFacts(c, b), q.CheckedAt); err != nil {
		return zero, rejectManualInput("NONCE_OBSERVATION_INVALID")
	}
	var complete int
	if err = query.QueryRowContext(ctx, `SELECT COUNT(*) FROM manual_nonce_claim_complete WHERE operation_id=?`, q.Operation).Scan(&complete); err != nil || complete != 1 {
		return zero, manualVerificationError(err, "NONCE_CLAIM_INCOMPLETE")
	}
	unit := manualrisk.Unit{ChainID: c.Input.ChainID, Asset: c.Input.Asset, Decimals: c.Input.Decimals, Kind: manualrisk.TradeInput}
	if _, _, err = verifyManualAuthorizationUsage(ctx, query, scope.AuthorizationID, unit); err != nil {
		return zero, err
	}
	return c, nil
}

func nonceResult(q manualNonceClaim, c ManualExecutionContract, duplicate bool) manualNonceClaimResult {
	status := "NONCE_CLAIMED_NOT_SIGNED_NOT_PERMISSION"
	if duplicate {
		status = "HISTORICAL_NONCE_CLAIM_NOT_PERMISSION"
	}
	return manualNonceClaimResult{OperationID: q.Operation, ExecutionStepID: q.Step, AttemptID: q.Attempt, Nonce: q.Nonce,
		ContractHash: q.ContractHash, ExpiresAt: c.ExpiresAt, Status: status, Duplicate: duplicate}
}

func (w *manualNonceClaimWriter) at(stage string) error {
	if w.checkpoint != nil {
		return w.checkpoint(stage)
	}
	return nil
}

func manualNonceExpectedScope(s manualAdmissionScope) ManualAdmissionScope {
	return ManualAdmissionScope{AuthorizationID: s.AuthorizationID, AuthorizationEpoch: s.AuthorizationEpoch,
		ManualPolicyID: s.ManualPolicyID, Environment: s.Environment, BuildSHA: s.BuildSHA, ReleaseID: s.ReleaseID, DeploymentID: s.DeploymentID}
}

func ownManualNonceRequest(ctx context.Context, q queryRow, id Identity, request string) error {
	if !id.valid() || !validID(request) {
		return ErrInvalid
	}
	var owned int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM manual_http_order_sources WHERE id=? AND principal_id=? AND wallet_id=? AND lower(wallet_address)=lower(?)`, request, id.PrincipalID, id.WalletID, id.WalletAddress).Scan(&owned)
	if err != nil {
		return err
	}
	if owned != 1 {
		return ErrNotFound
	}
	return nil
}

func readManualNonceClaim(ctx context.Context, q queryRow, request string) (manualNonceClaim, error) {
	return scanManualNonceClaim(q.QueryRowContext(ctx, `SELECT `+manualNonceClaimColumns+` FROM manual_nonce_claims WHERE operation_id=(SELECT operation_id FROM manual_http_order_sources WHERE id=?)`, request))
}

// B2-only denial check, not a change to the shared B1 reader. The same view is
// enforced by SQL before INSERT, after the out-of-transaction observation.
func checkManualNonceUnresolvedWallet(ctx context.Context, q queryRow, request string) error {
	var blocked bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manual_nonce_unresolved_wallet_steps WHERE operation_id=(SELECT operation_id FROM manual_execution_bindings WHERE request_id=?))`, request).Scan(&blocked); err != nil {
		return err
	}
	if blocked {
		return ErrWallet
	}
	return nil
}

// Own read snapshot before observation. Duplicate history skips the observer.
func (w *manualNonceClaimWriter) read(ctx context.Context, id Identity, request string, scope manualAdmissionScope) (ManualBoundSigningInput, *manualNonceClaimResult, error) {
	var zero ManualBoundSigningInput
	tx, err := w.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return zero, nil, err
	}
	defer tx.Rollback()
	entry := w.now().UTC()
	if _, err = readManualControl(ctx, tx, entry); err != nil {
		return zero, nil, err
	}
	if !scope.valid() {
		return zero, nil, ErrInvalid
	}
	if err = ownManualNonceRequest(ctx, tx, id, request); err != nil {
		return zero, nil, err
	}
	q, err := readManualNonceClaim(ctx, tx, request)
	var duplicate *manualNonceClaimResult
	var v ManualBoundSigningInput
	if err == nil {
		c, e := verifyManualNonceClaim(ctx, tx, q, id, scope)
		if e != nil {
			return zero, nil, e
		}
		result := nonceResult(q, c, true)
		duplicate = &result
	} else if !errors.Is(err, sql.ErrNoRows) {
		return zero, nil, err
	} else {
		if err = checkManualNonceUnresolvedWallet(ctx, tx, request); err != nil {
			return zero, nil, err
		}
		v, err = loadManualBoundSigningInputTx(ctx, tx, id, request, manualNonceExpectedScope(scope), w.now)
		if err != nil {
			return zero, nil, err
		}
		readAt, e := canonicalManualTime(v.ObservedAt)
		if e != nil || readAt.Before(entry) {
			return zero, nil, rejectManualInput("NONCE_READ_TIME_REVERSED")
		}
	}
	if err = tx.Commit(); err != nil {
		if errors.Is(err, sql.ErrTxDone) && ctx.Err() != nil {
			err = errors.Join(ctx.Err(), err)
		}
		return zero, nil, err
	}
	return v, duplicate, nil
}

// claim accepts durable request identity, never a supplied nonce/transaction.
// Query evidence is obtained outside the writer; current controls are then
// authoritatively reloaded on the serialized writer. No signer or network.
func (w *manualNonceClaimWriter) claim(ctx context.Context, id Identity, request string, scope manualAdmissionScope) (manualNonceClaimResult, error) {
	var zero manualNonceClaimResult
	if w == nil || w.store == nil || w.store.db == nil || w.observer == nil || w.now == nil || ctx == nil {
		return zero, ErrInvalid
	}
	// Per-invocation clock tracker covers even a reversed intermediate read
	// that later recovers. Clone the writer: no shared mutable clock state.
	source := w
	local := *w
	var previous time.Time
	invalidClock := false
	local.now = func() time.Time {
		now := source.now().UTC()
		if now.IsZero() || now.Year() < 1970 || now.Year() > 9998 || (!previous.IsZero() && now.Before(previous)) {
			invalidClock = true
		}
		previous = now
		return now
	}
	w = &local
	v, duplicate, err := w.read(ctx, id, request, scope)
	if err != nil {
		return zero, err
	}
	if invalidClock {
		return zero, rejectManualInput("NONCE_TIME_REVERSED")
	}
	if duplicate != nil {
		return *duplicate, nil
	}
	readAt, err := canonicalManualTime(v.ObservedAt)
	if err != nil {
		return zero, rejectManualInput("NONCE_READ_TIME_INVALID")
	}
	if err = w.at("after_read"); err != nil {
		return zero, err
	}
	b := manualExecutionBinding{CheckedAt: v.BoundAt}
	facts := manualNonceFacts(v.Contract, b)
	queryCtx, cancel := context.WithTimeout(ctx, manualnonce.MaxObservationAge)
	o, err := w.observer.ObserveManualNonce(queryCtx, facts)
	cause := queryCtx.Err()
	cancel()
	if err != nil {
		return zero, err
	}
	if cause != nil {
		return zero, cause
	}
	raw := manualnonce.Encode(o)
	digest := manualnonce.Digest(raw)
	observedCheck := w.now().UTC()
	observationAt, oe := canonicalManualTime(o.ObservedAt)
	if oe != nil || observedCheck.Before(readAt) || observationAt.Before(readAt) {
		return zero, rejectManualInput("NONCE_TIME_REVERSED")
	}
	if _, err = manualnonce.Verify(raw, digest, o.PendingValue, facts, observedCheck.Format(time.RFC3339Nano)); err != nil {
		return zero, rejectManualInput("NONCE_OBSERVATION_INVALID")
	}
	if err = w.at("after_observation"); err != nil {
		return zero, err
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
	if entry.Before(observedCheck) {
		return zero, rejectManualInput("NONCE_TIME_REVERSED")
	}
	if err = w.at("after_begin"); err != nil {
		return zero, err
	}
	q, err := readManualNonceClaim(ctx, conn, request)
	if err == nil {
		c, e := verifyManualNonceClaim(ctx, conn, q, id, scope)
		if e != nil {
			return zero, e
		}
		if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
			return zero, err
		}
		return nonceResult(q, c, true), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zero, err
	}
	if err = checkManualNonceUnresolvedWallet(ctx, conn, request); err != nil {
		return zero, err
	}
	current, err := loadManualBoundSigningInputTx(ctx, conn, id, request, manualNonceExpectedScope(scope), w.now)
	if err != nil {
		return zero, err
	}
	if encode(current.Contract) != encode(v.Contract) || current.BoundAt != v.BoundAt {
		return zero, ErrConflict
	}
	currentAt, ce := canonicalManualTime(current.ObservedAt)
	if ce != nil || currentAt.Before(entry) {
		return zero, rejectManualInput("NONCE_TIME_REVERSED")
	}
	if err = w.at("before_insert"); err != nil {
		return zero, err
	}
	stamp := w.now().UTC()
	if invalidClock || stamp.Before(currentAt) {
		return zero, rejectManualInput("NONCE_TIME_REVERSED")
	}
	if _, err = manualnonce.Verify(raw, digest, o.PendingValue, facts, stamp.Format(time.RFC3339Nano)); err != nil {
		return zero, rejectManualInput("NONCE_OBSERVATION_INVALID")
	}
	q = manualNonceClaim{Operation: v.Contract.OperationID, Step: v.ExecutionStepID, Attempt: "manual-nonce:" + v.Contract.OperationID,
		Wallet: v.Contract.WalletID, Address: strings.ToLower(v.Contract.WalletAddress), Nonce: o.PendingValue, Reservation: v.ReservationID,
		ContractHash: v.Contract.BindingHash, ObservationRaw: raw, ObservationHash: digest, CheckedAt: stamp.Format(time.RFC3339Nano)}
	if _, err = conn.ExecContext(ctx, `INSERT INTO manual_nonce_claims(`+manualNonceClaimColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, q.arguments()...); err != nil {
		return zero, err
	}
	if err = w.at("after_insert"); err != nil {
		return zero, err
	}
	if _, err = verifyManualNonceClaim(ctx, conn, q, id, scope); err != nil {
		return zero, err
	}
	if err = w.at("before_commit"); err != nil {
		return zero, err
	}
	final := w.now().UTC()
	if invalidClock || final.Before(stamp) || ttlRecord(Record{ExpiresAt: v.Contract.ExpiresAt}, final) != nil {
		return zero, rejectManualInput("NONCE_FINAL_TIME_INVALID")
	}
	last, err := readManualControl(ctx, conn, final)
	if err != nil {
		return zero, err
	}
	gate, err := manualGateEvidenceHash(ctx, conn, final)
	if err != nil {
		return zero, err
	}
	if last != control || gate != v.GateEvidenceSetHash {
		return zero, rejectManualInput("NONCE_FINAL_CONTROL_DRIFT")
	}
	finalAuth, err := loadManualAuthorizationIdentity(ctx, conn, scope, v.Contract, current.ManualPolicy, current.ManualPolicyHash, gate, final)
	if err != nil {
		return zero, err
	}
	if _, err = manualnonce.Verify(raw, digest, q.Nonce, facts, final.Format(time.RFC3339Nano)); err != nil {
		return zero, rejectManualInput("NONCE_FINAL_OBSERVATION_EXPIRED")
	}
	commitAt := w.now().UTC()
	authExpiry, ae := canonicalManualTime(finalAuth.ExpiresAt)
	if invalidClock || ae != nil || !commitAt.Before(authExpiry) || ttlRecord(Record{ExpiresAt: v.Contract.ExpiresAt}, commitAt) != nil {
		return zero, rejectManualInput("NONCE_COMMIT_TIME_INVALID")
	}
	if _, err = manualnonce.Verify(raw, digest, q.Nonce, facts, commitAt.Format(time.RFC3339Nano)); err != nil {
		return zero, rejectManualInput("NONCE_COMMIT_OBSERVATION_EXPIRED")
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return zero, err
	}
	if err = w.at("after_commit"); err != nil {
		return zero, err
	}
	return nonceResult(q, v.Contract, false), nil
}
