package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
)

// ManualAdmissionScope is deployment-owned configuration, not HTTP input or
// authorization. Its expected identity is reverified against SQLite on every
// new admission. Construction neither grants authorization nor arms it.
type ManualAdmissionScope struct {
	AuthorizationID    string `json:"authorization_id"`
	AuthorizationEpoch uint64 `json:"authorization_epoch"`
	ManualPolicyID     string `json:"manual_policy_id"`
	Environment        string `json:"environment"`
	BuildSHA           string `json:"build_sha"`
	ReleaseID          string `json:"release_id"`
	DeploymentID       string `json:"deployment_id"`
}

func (s ManualAdmissionScope) internal() manualAdmissionScope {
	return manualAdmissionScope{s.AuthorizationID, s.ManualPolicyID, s.AuthorizationEpoch, s.Environment, s.BuildSHA, s.ReleaseID, s.DeploymentID}
}

// ManualAdmission is an observation, never permission to execute or send.
// GET always returns HISTORICAL_ADMISSION_NOT_PERMISSION. Only the POST that
// successfully commits a first admission reports ADMITTED_NOT_SIGNED_NOT_SUBMITTED.
type ManualAdmission struct {
	Status              string `json:"status"`
	OperationID         string `json:"operation_id"`
	ReservationID       string `json:"reservation_id"`
	ReservationState    string `json:"reservation_state"`
	ContractHash        string `json:"contract_hash"`
	ExpiresAt           string `json:"expires_at"`
	AdmittedAt          string `json:"admitted_at"`
	ExecutionAuthorized bool   `json:"execution_authorized"`
	SendAuthorized      bool   `json:"send_authorized"`
}

// One read snapshot, no clock or active-control requirement. Expiry/revoke/stop
// do not erase durable history. Complete schema and canonical identity/usage
// verification are still mandatory; corrupt history is not silently omitted.
func (s *Store) manualAdmissionHistory(ctx context.Context, id Identity, request string) (*ManualAdmission, error) {
	if s == nil || s.db == nil || ctx == nil || !id.valid() || !validID(request) {
		return nil, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	r, _, err := scanManualRecord(tx.QueryRowContext(ctx, selectManualRecord+`WHERE m.id=? AND m.principal_id=? AND m.wallet_id=?`, request, id.PrincipalID, id.WalletID))
	if err != nil {
		return nil, err
	}
	var address string
	if err = tx.QueryRowContext(ctx, `SELECT wallet_address FROM manual_http_order_sources WHERE id=?`, request).Scan(&address); err != nil {
		return nil, err
	}
	if common.HexToAddress(address) != common.HexToAddress(id.WalletAddress) {
		return nil, ErrNotFound
	}
	cmd, err := scanManualCommand(tx.QueryRowContext(ctx, `SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE request_id=? AND operation_id=?`, request, *r.OperationID))
	if errors.Is(err, sql.ErrNoRows) {
		var operation, step string
		if err = tx.QueryRowContext(ctx, `SELECT o.status,s.status FROM operations o JOIN execution_steps s ON s.operation_id=o.id JOIN manual_http_order_sources m ON m.step_id=s.id WHERE m.id=?`, request).Scan(&operation, &step); err != nil {
			return nil, err
		}
		if operation == "manual_admission_succeeded" || step == "manual_admitted" {
			return nil, rejectManualInput("ADMISSION_LEDGER_INCOMPLETE")
		}
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	c, err := verifyManualCommandTx(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	if c.PrincipalID != id.PrincipalID || c.WalletID != id.WalletID || common.HexToAddress(c.WalletAddress) != common.HexToAddress(id.WalletAddress) {
		return nil, ErrNotFound
	}
	unit := manualrisk.Unit{ChainID: c.Input.ChainID, Asset: c.Input.Asset, Decimals: c.Input.Decimals, Kind: manualrisk.TradeInput}
	if _, _, err = verifyManualAuthorizationUsage(ctx, tx, cmd.Scope.AuthorizationID, unit); err != nil {
		return nil, err
	}
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM canary_risk_reservations WHERE id=? AND operation_id=?`, cmd.Reservation, cmd.Operation).Scan(&state); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &ManualAdmission{Status: "HISTORICAL_ADMISSION_NOT_PERMISSION", OperationID: cmd.Operation, ReservationID: cmd.Reservation,
		ReservationState: state, ContractHash: cmd.ContractHash, ExpiresAt: c.ExpiresAt, AdmittedAt: cmd.CheckedAt}, nil
}

func (h *API) admitManualRecord(ctx context.Context, record *Record) error {
	continuation, err := h.admitManualRecordWithContinuation(ctx, record)
	continuation.discard()
	return err
}

func (h *API) admitManualRecordWithContinuation(ctx context.Context, record *Record) (*manualAdmissionContinuation, error) {
	w := &manualAdmissionWriter{store: h.store, now: func() time.Time { return h.now() }}
	result, continuation, err := w.admitWithContinuation(ctx, h.identity, record.RequestID, *h.admissionScope)
	if err != nil {
		continuation.discard()
		return nil, err
	}
	return h.completeManualAdmission(ctx, record, result, continuation)
}

// Completion owns the tentative local handoff until the historical projection
// is verified. Every failure burns ALL references, not only the return value.
func (h *API) completeManualAdmission(ctx context.Context, record *Record, result manualAdmissionResult, continuation *manualAdmissionContinuation) (*manualAdmissionContinuation, error) {
	keep := false
	defer func() {
		if !keep {
			continuation.discard()
		}
	}()
	// An observation read can fail after admission COMMIT. This is NOT rollback;
	// the caller keeps Location/idempotency key and can query/retry exact history.
	stored, err := h.store.Get(ctx, h.identity, record.RequestID, false)
	if err != nil {
		return nil, err
	}
	observation := stored.Admission
	if observation == nil || observation.ContractHash != result.ContractHash || observation.ReservationID != result.ReservationID || observation.ExpiresAt != result.ExpiresAt {
		return nil, rejectManualInput("ADMISSION_LEDGER_INCOMPLETE")
	}
	if continuation != nil {
		if continuation.state == nil {
			return nil, ErrConflict
		}
		owner := continuation.state.identity
		if result.Duplicate || owner.request != record.RequestID || owner.operation != result.OperationID ||
			owner.reservation != result.ReservationID || owner.contractHash != result.ContractHash || owner.expiresAt != result.ExpiresAt ||
			owner.identity.PrincipalID != h.identity.PrincipalID || owner.identity.WalletID != h.identity.WalletID ||
			common.HexToAddress(owner.identity.WalletAddress) != common.HexToAddress(h.identity.WalletAddress) ||
			h.admissionScope == nil || owner.scope != *h.admissionScope {
			return nil, ErrConflict
		}
	}
	observation.Status = result.Status
	record.Validation = stored.Validation
	record.Admission = observation
	record.Execution = stored.Execution
	record.Duplicate = record.Duplicate || result.Duplicate
	keep = true
	return continuation, nil
}

func (h *API) validateAndAdmit(ctx context.Context, record *Record) error {
	continuation, err := h.validateAndAdmitWithContinuation(ctx, record)
	continuation.discard()
	return err
}

func (h *API) validateAndAdmitWithContinuation(ctx context.Context, record *Record) (*manualAdmissionContinuation, error) {
	if h.admissionScope != nil {
		// Advisory stop-first preflight prevents validation RPC under a known
		// stop. It grants no permission: C3-B repeats this on its own writer.
		if _, err := readManualControl(ctx, h.store.db, h.now().UTC()); err != nil {
			return nil, err
		}
	}
	history, err := h.store.manualAdmissionHistory(ctx, h.identity, record.RequestID)
	if err != nil {
		return nil, err
	}
	if history != nil {
		return nil, h.returnExistingAdmission(ctx, record)
	}
	validation, err := h.validator.Validate(ctx, h.identity, record.RequestID)
	if err != nil {
		// Another HTTP caller may commit admission after our history snapshot.
		// Only a complete independently verified history can resolve that race;
		// no retry of validation or minting of a second economic action.
		history, historyErr := h.store.manualAdmissionHistory(ctx, h.identity, record.RequestID)
		if historyErr != nil {
			return nil, historyErr
		}
		if history != nil {
			return nil, h.returnExistingAdmission(ctx, record)
		}
		return nil, err
	}
	record.Validation = &validation
	if h.admissionScope != nil {
		return h.admitManualRecordWithContinuation(ctx, record)
	}
	return nil, nil
}

func (h *API) returnExistingAdmission(ctx context.Context, record *Record) error {
	if h.admissionScope != nil {
		return h.admitManualRecord(ctx, record)
	}
	// Disabling admission does not hide a committed order or rerun its RPCs.
	// This branch only reads history and cannot renew authority/usage/expiry.
	stored, err := h.store.Get(ctx, h.identity, record.RequestID, false)
	if err != nil {
		return err
	}
	if stored.Admission == nil {
		return rejectManualInput("ADMISSION_LEDGER_INCOMPLETE")
	}
	record.Admission, record.Validation, record.Execution = stored.Admission, stored.Validation, stored.Execution
	return nil
}
