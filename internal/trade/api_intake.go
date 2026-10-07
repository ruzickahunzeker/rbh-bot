package trade

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrTradeAPINotFound = errors.New("trade operation not found")

// SubmissionIntakeRequest references existing durable work; callers cannot
// supply calldata, artifacts, permits, authorization or execution controls.
type SubmissionIntakeRequest struct {
	WalletID       string `json:"wallet_id"`
	IdempotencyKey string `json:"idempotency_key"`
	PolicyVersion  uint64 `json:"policy_version"`
	ExpiresAt      string `json:"expires_at"`
}

type SubmissionIntakeRecord struct {
	ID               string `json:"id"`
	OperationID      string `json:"operation_id"`
	WalletID         string `json:"wallet_id"`
	IdempotencyKey   string `json:"idempotency_key"`
	PolicyVersion    uint64 `json:"policy_version"`
	ExpiresAt        string `json:"expires_at"`
	Outcome          string `json:"outcome"`
	ReasonCode       string `json:"reason_code"`
	CreatedAt        string `json:"created_at"`
	Duplicate        bool   `json:"duplicate"`
	SendAuthorized   bool   `json:"send_authorized"`
	SubmissionQueued bool   `json:"submission_queued"`
}

func validAPIID(v string) bool {
	if len(v) == 0 || len(v) > 128 {
		return false
	}
	for _, ch := range v {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("._:-", ch)) {
			return false
		}
	}
	return true
}

// RecordDisabledSubmissionIntake cannot enable sending. The migration permits
// only REJECTED/SUBMISSION_DISABLED and atomically appends audit + alert outbox.
// An exact duplicate returns its original rejection even after TTL expires;
// this is retrieval, never a new expiry window or a replay/send request.
func (s *Store) RecordDisabledSubmissionIntake(ctx context.Context, operation string, r SubmissionIntakeRequest, now time.Time) (SubmissionIntakeRecord, error) {
	if s == nil || s.db == nil || ctx == nil {
		return SubmissionIntakeRecord{}, ErrTradeStore
	}
	if !validAPIID(operation) || !validAPIID(r.WalletID) || !validAPIID(r.IdempotencyKey) || r.PolicyVersion == 0 || r.PolicyVersion > 1<<63-1 {
		return SubmissionIntakeRecord{}, ErrInvalidRequest
	}
	expiry, err := parseCanonicalExpiry(r.ExpiresAt)
	if err != nil || now.IsZero() {
		return SubmissionIntakeRecord{}, ErrTTLUnverifiable
	}
	encoded, _ := json.Marshal(struct {
		Operation string `json:"operation_id"`
		SubmissionIntakeRequest
	}{operation, r})
	hash := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(hash[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubmissionIntakeRecord{}, err
	}
	defer tx.Rollback()
	var prior SubmissionIntakeRecord
	var priorFingerprint string
	err = tx.QueryRowContext(ctx, `SELECT id,operation_id,wallet_id,idempotency_key,policy_version,expires_at,outcome,reason_code,created_at,request_fingerprint FROM trade_api_submission_requests WHERE wallet_id=? AND idempotency_key=?`, r.WalletID, r.IdempotencyKey).Scan(&prior.ID, &prior.OperationID, &prior.WalletID, &prior.IdempotencyKey, &prior.PolicyVersion, &prior.ExpiresAt, &prior.Outcome, &prior.ReasonCode, &prior.CreatedAt, &priorFingerprint)
	if err == nil {
		if priorFingerprint != fingerprint {
			return SubmissionIntakeRecord{}, ErrIdempotencyConflict
		}
		prior.Duplicate = true
		return prior, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SubmissionIntakeRecord{}, err
	}
	var wallet, durableExpiry, capability, sourceFingerprint string
	var policy, chain uint64
	err = tx.QueryRowContext(ctx, `SELECT wallet_id,policy_version,expires_at,deadline_capability,request_fingerprint,chain_id FROM operations WHERE id=?`, operation).Scan(&wallet, &policy, &durableExpiry, &capability, &sourceFingerprint, &chain)
	if errors.Is(err, sql.ErrNoRows) {
		return SubmissionIntakeRecord{}, ErrTradeAPINotFound
	}
	if err != nil {
		return SubmissionIntakeRecord{}, err
	}
	if wallet != r.WalletID || policy != r.PolicyVersion || durableExpiry != r.ExpiresAt || capability != "APPLICATION_TTL_ONLY" || chain != ChainID {
		return SubmissionIntakeRecord{}, ErrIdempotencyConflict
	}
	var curveSource int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM dry_run_results d JOIN execution_steps step ON step.id=d.step_id WHERE d.operation_id=? AND d.status='success' AND json_extract(step.route_json,'$.protocol')='pons-v2-curve'`, operation).Scan(&curveSource); err != nil {
		return SubmissionIntakeRecord{}, err
	}
	if curveSource != 1 {
		return SubmissionIntakeRecord{}, ErrInvalidRequest
	}
	if !now.UTC().Before(expiry) {
		return SubmissionIntakeRecord{}, ErrTTLExpired
	}
	record := SubmissionIntakeRecord{ID: deterministicID("trade-api-intake", r.WalletID, r.IdempotencyKey), OperationID: operation, WalletID: r.WalletID, IdempotencyKey: r.IdempotencyKey, PolicyVersion: policy, ExpiresAt: durableExpiry, Outcome: "REJECTED", ReasonCode: "SUBMISSION_DISABLED", CreatedAt: now.UTC().Format(time.RFC3339Nano)}
	_, err = tx.ExecContext(ctx, `INSERT INTO trade_api_submission_requests(id,operation_id,chain_id,wallet_id,idempotency_key,request_fingerprint,operation_fingerprint,policy_version,expires_at,outcome,reason_code,created_at) VALUES(?,?,4663,?,?,?,?,?,?,'REJECTED','SUBMISSION_DISABLED',?)`, record.ID, operation, wallet, r.IdempotencyKey, fingerprint, sourceFingerprint, policy, durableExpiry, record.CreatedAt)
	if err != nil {
		return SubmissionIntakeRecord{}, err
	}
	return record, tx.Commit()
}

// OperationAPIStatus is a redacted snapshot of the existing state machine.
// It never contains raw/encrypted transactions, calldata, key versions or keys.
type OperationAPIStatus struct {
	OperationID        string          `json:"operation_id"`
	ChainID            uint64          `json:"chain_id"`
	WalletID           string          `json:"wallet_id"`
	Status             string          `json:"status"`
	PolicyVersion      uint64          `json:"policy_version"`
	DeadlineCapability string          `json:"deadline_capability"`
	ContractDeadline   bool            `json:"contract_deadline"`
	ExpiresAt          string          `json:"expires_at"`
	SubmissionEnabled  bool            `json:"submission_enabled"`
	Steps              []APIStepStatus `json:"steps"`
	Submissions        []APISubmission `json:"submissions"`
	Receipts           []APIReceipt    `json:"receipts"`
}

type APIStepStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type APISubmission struct {
	ID        string `json:"id"`
	AttemptID string `json:"attempt_id"`
	TxHash    string `json:"tx_hash"`
	State     string `json:"state"`
	Sequence  uint64 `json:"sequence"`
}

type APIReceipt struct {
	ID             string `json:"id"`
	AttemptID      string `json:"attempt_id"`
	BlockNumber    uint64 `json:"block_number"`
	BlockHash      string `json:"block_hash"`
	Status         uint64 `json:"receipt_status"`
	CanonicalState string `json:"canonical_state"`
}

func (s *Store) OperationAPIStatus(ctx context.Context, operation, wallet string) (OperationAPIStatus, error) {
	if s == nil || s.db == nil || ctx == nil {
		return OperationAPIStatus{}, ErrTradeStore
	}
	if !validAPIID(operation) || !validAPIID(wallet) {
		return OperationAPIStatus{}, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return OperationAPIStatus{}, err
	}
	defer tx.Rollback()
	v := OperationAPIStatus{Steps: []APIStepStatus{}, Submissions: []APISubmission{}, Receipts: []APIReceipt{}}
	err = tx.QueryRowContext(ctx, `SELECT id,chain_id,wallet_id,status,COALESCE(policy_version,0),COALESCE(deadline_capability,''),COALESCE(expires_at,'') FROM operations WHERE id=? AND wallet_id=?`, operation, wallet).Scan(&v.OperationID, &v.ChainID, &v.WalletID, &v.Status, &v.PolicyVersion, &v.DeadlineCapability, &v.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrTradeAPINotFound
	}
	if err != nil {
		return v, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,status FROM execution_steps WHERE operation_id=? ORDER BY step_index LIMIT 101`, operation)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var step APIStepStatus
		if err := rows.Scan(&step.ID, &step.Status); err != nil {
			_ = rows.Close()
			return v, err
		}
		v.Steps = append(v.Steps, step)
	}
	if err := closeAPIRows(rows); err != nil {
		return v, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT sub.id,sub.attempt_id,sub.tx_hash,sub.state,sub.sequence FROM transaction_submissions sub JOIN transaction_attempts a ON a.id=sub.attempt_id JOIN execution_steps step ON step.id=a.step_id WHERE step.operation_id=? ORDER BY sub.attempt_id,sub.sequence LIMIT 101`, operation)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var sub APISubmission
		if err := rows.Scan(&sub.ID, &sub.AttemptID, &sub.TxHash, &sub.State, &sub.Sequence); err != nil {
			_ = rows.Close()
			return v, err
		}
		v.Submissions = append(v.Submissions, sub)
	}
	if err := closeAPIRows(rows); err != nil {
		return v, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT r.id,r.attempt_id,r.block_number,r.block_hash,r.receipt_status,r.canonical_state FROM receipt_observations r JOIN transaction_attempts a ON a.id=r.attempt_id JOIN execution_steps step ON step.id=a.step_id WHERE step.operation_id=? ORDER BY r.id LIMIT 101`, operation)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var r APIReceipt
		if err := rows.Scan(&r.ID, &r.AttemptID, &r.BlockNumber, &r.BlockHash, &r.Status, &r.CanonicalState); err != nil {
			_ = rows.Close()
			return v, err
		}
		v.Receipts = append(v.Receipts, r)
	}
	if err := closeAPIRows(rows); err != nil {
		return v, err
	}
	// Never silently truncate financial status. A paginated status API is a
	// future extension; fail closed on operations exceeding this bounded view.
	if len(v.Steps) > 100 || len(v.Submissions) > 100 || len(v.Receipts) > 100 {
		return OperationAPIStatus{}, ErrTradeStore
	}
	return v, tx.Commit()
}

func closeAPIRows(rows *sql.Rows) error {
	err := rows.Err()
	return errors.Join(err, rows.Close())
}

type OperationAPIEvent struct {
	Cursor    uint64 `json:"cursor"`
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	Reason    string `json:"reason_code"`
	CreatedAt string `json:"created_at"`
}

type OperationAPIEvents struct {
	Events     []OperationAPIEvent `json:"events"`
	NextCursor uint64              `json:"next_cursor"`
}

// Events exposes only this API's schema-owned rejection audit metadata, not
// arbitrary runtime details_json. The explicit AUTOINCREMENT ledger sequence
// preserves insertion-order cursors across restart and SQLite VACUUM.
func (s *Store) OperationAPIEvents(ctx context.Context, operation, wallet string, after uint64) (OperationAPIEvents, error) {
	if s == nil || s.db == nil || ctx == nil {
		return OperationAPIEvents{}, ErrTradeStore
	}
	if !validAPIID(operation) || !validAPIID(wallet) || after > 1<<63-1 {
		return OperationAPIEvents{}, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return OperationAPIEvents{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM operations WHERE id=? AND wallet_id=?`, operation, wallet).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return OperationAPIEvents{}, ErrTradeAPINotFound
	} else if err != nil {
		return OperationAPIEvents{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.sequence,a.id,a.event_type,a.reason_code,a.created_at FROM trade_api_submission_requests r JOIN canary_runtime_audit a ON a.id='trade-api-audit:'||r.id WHERE r.operation_id=? AND r.sequence>? ORDER BY r.sequence LIMIT 100`, operation, after)
	if err != nil {
		return OperationAPIEvents{}, err
	}
	v := OperationAPIEvents{Events: []OperationAPIEvent{}, NextCursor: after}
	for rows.Next() {
		var e OperationAPIEvent
		if err := rows.Scan(&e.Cursor, &e.ID, &e.EventType, &e.Reason, &e.CreatedAt); err != nil {
			_ = rows.Close()
			return v, err
		}
		v.Events = append(v.Events, e)
		v.NextCursor = e.Cursor
	}
	if err := closeAPIRows(rows); err != nil {
		return v, err
	}
	return v, tx.Commit()
}
