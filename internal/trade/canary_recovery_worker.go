package trade

import (
	"context"
	"errors"
	"time"
)

var ErrCanaryRecoveryLeaseLost = errors.New("controlled recovery lease lost")

type ControlledRecoveryItem struct {
	OperationID, AttemptID, TxHash, SubmissionState string
}

type ControlledRecoveryQuery struct {
	OperationID, AttemptID, TxHash string
}

type ControlledRecoveryEvidence struct {
	TxFound, ReceiptFound    bool
	NonceState, EvidenceHash string
	ObservedAt               time.Time
}

type ControlledRecoveryQuerier interface {
	QueryRecovery(context.Context, ControlledRecoveryQuery) (ControlledRecoveryEvidence, error)
}

type ControlledRecoveryWorker struct {
	store                          *Store
	recovery                       *RecoveryService
	querier                        ControlledRecoveryQuerier
	environment, holder            string
	epoch                          uint64
	now                            func() time.Time
	beforeQuery, afterQueryForTest func(ControlledRecoveryItem)
	beforeReconcileForTest         func(ControlledRecoveryItem)
}

func (w *ControlledRecoveryWorker) leaseFence() RecoveryLeaseFence {
	return RecoveryLeaseFence{Environment: w.environment, HolderID: w.holder, Epoch: w.epoch, clock: w.now}
}

func NewControlledRecoveryWorker(store *Store, recovery *RecoveryService, querier ControlledRecoveryQuerier, environment, holder string, epoch uint64) (*ControlledRecoveryWorker, error) {
	if store == nil || recovery == nil || querier == nil || environment == "" || holder == "" || epoch == 0 {
		return nil, ErrInvalidRequest
	}
	return &ControlledRecoveryWorker{store: store, recovery: recovery, querier: querier, environment: environment, holder: holder, epoch: epoch, now: time.Now}, nil
}

// Run provides the controlled local lifecycle. Cancellation drains the current
// item and stops before another durable scan; restart rediscovers from SQLite.
func (w *ControlledRecoveryWorker) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return ErrInvalidRequest
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		// Once a scan starts, drain it with the caller's values but without
		// cancellation. Every item remains fenced by the durable lease.
		if err := w.RunOnce(context.WithoutCancel(ctx)); err != nil {
			return err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

// RunOnce discovers all work from durable state. It never submits, replays,
// signs, allocates a nonce, consumes a send permit, or releases ambiguity.
func (w *ControlledRecoveryWorker) RunOnce(ctx context.Context) error {
	if err := w.fence(ctx); err != nil {
		return w.alert(ctx, ControlledRecoveryItem{}, "RECOVERY_LEASE_LOST", "CRITICAL", err)
	}
	items, err := w.store.ListControlledRecoveryItems(ctx)
	if err != nil {
		return w.alert(ctx, ControlledRecoveryItem{}, "RECOVERY_DISCOVERY_FAILED", "CRITICAL", err)
	}
	for _, item := range items {
		if err = w.process(ctx, item); err != nil {
			if errors.Is(err, ErrCanaryRecoveryLeaseLost) {
				return w.alert(ctx, item, "RECOVERY_LEASE_LOST", "CRITICAL", err)
			}
			return err
		}
	}
	return nil
}

func (w *ControlledRecoveryWorker) process(ctx context.Context, item ControlledRecoveryItem) error {
	expectedReceipt := false
	if err := w.fence(ctx); err != nil {
		return err
	}
	if w.beforeQuery != nil {
		w.beforeQuery(item)
	}
	if item.SubmissionState == "broadcast_unknown" {
		evidence, err := w.querier.QueryRecovery(ctx, ControlledRecoveryQuery{OperationID: item.OperationID, AttemptID: item.AttemptID, TxHash: item.TxHash})
		if err != nil || evidence.ObservedAt.IsZero() || len(evidence.EvidenceHash) != 64 {
			return w.alert(ctx, item, "RECOVERY_EVIDENCE_UNAVAILABLE", "WARNING", err)
		}
		if w.afterQueryForTest != nil {
			w.afterQueryForTest(item)
		}
		if err = w.fence(ctx); err != nil {
			return err
		}
		classification := "AMBIGUOUS"
		if evidence.TxFound || evidence.ReceiptFound {
			classification = "PROPAGATED"
		} else if evidence.NonceState != "RESERVED_UNRESOLVED" {
			classification = "NONCE_UNSAFE"
		}
		if err = w.store.RecordControlledRecoveryEvidence(ctx, item, evidence, classification, w.environment, w.holder, w.epoch, w.now().UTC()); err != nil {
			return err
		}
		if !evidence.TxFound && !evidence.ReceiptFound {
			reason := "BROADCAST_UNKNOWN_REMAINS_FROZEN"
			if evidence.NonceState != "RESERVED_UNRESOLVED" {
				reason = "BROADCAST_UNKNOWN_NONCE_UNSAFE"
			}
			return w.audit(ctx, item, reason, "WARNING")
		}
		expectedReceipt = evidence.ReceiptFound
		if err = w.audit(ctx, item, "PROPAGATION_EVIDENCE_OBSERVED", "INFO"); err != nil {
			return err
		}
	}
	if w.beforeReconcileForTest != nil {
		w.beforeReconcileForTest(item)
	}
	if err := w.recovery.ReconcileWithLease(ctx, item.OperationID, w.leaseFence); err != nil {
		if expectedReceipt && errors.Is(err, ErrReceiptPending) {
			return w.alert(ctx, item, "RECOVERY_EVIDENCE_CONTRADICTORY", "CRITICAL", err)
		}
		if !errors.Is(err, ErrNotCanonical) && !errors.Is(err, ErrReceiptPending) {
			return w.alert(ctx, item, "RECOVERY_RECONCILE_FAILED", "WARNING", err)
		}
	}
	observations, err := w.store.ListCanonicalReceiptObservations(ctx, item.AttemptID)
	if err != nil {
		return w.alert(ctx, item, "RECOVERY_OBSERVATION_SCAN_FAILED", "WARNING", err)
	}
	for _, observation := range observations {
		if err = w.recovery.CheckReorgWithLease(ctx, item.OperationID, observation, w.leaseFence); err != nil {
			return w.alert(ctx, item, "RECOVERY_REORG_CHECK_FAILED", "CRITICAL", err)
		}
	}
	return w.audit(ctx, item, "RECOVERY_ITEM_CHECKED", "INFO")
}

func (w *ControlledRecoveryWorker) fence(ctx context.Context) error {
	return w.store.ValidateCanaryWorkerLease(ctx, "RECOVERY", w.environment, w.holder, w.epoch, w.now().UTC())
}

func (w *ControlledRecoveryWorker) audit(ctx context.Context, item ControlledRecoveryItem, reason, severity string) error {
	return w.store.RecordRuntimeAlert(ctx, "CONTROLLED_RECOVERY_WORKER", item.OperationID, item.AttemptID, "", 0, reason, severity, w.now().UTC())
}

func (w *ControlledRecoveryWorker) alert(ctx context.Context, item ControlledRecoveryItem, reason, severity string, cause error) error {
	if err := w.audit(ctx, item, reason, severity); err != nil {
		return err
	}
	if cause != nil {
		return cause
	}
	return ErrCanaryRuntimeRejected
}

func (s *Store) ValidateCanaryWorkerLease(ctx context.Context, role, environment, holder string, epoch uint64, now time.Time) error {
	if s == nil || s.db == nil || ctx == nil || (role != "RECOVERY" && role != "SUBMISSION") || environment == "" || holder == "" || epoch == 0 || now.IsZero() {
		return ErrCanaryRecoveryLeaseLost
	}
	var expires string
	err := s.db.QueryRowContext(ctx, `SELECT expires_at FROM canary_worker_leases WHERE role=? AND environment=? AND holder_id=? AND lease_epoch=?`, role, environment, holder, epoch).Scan(&expires)
	if err != nil {
		return ErrCanaryRecoveryLeaseLost
	}
	expiry, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil || !now.UTC().Before(expiry) {
		return ErrCanaryRecoveryLeaseLost
	}
	return nil
}

func (s *Store) ListControlledRecoveryItems(ctx context.Context) ([]ControlledRecoveryItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id,a.id,a.tx_hash,sub.state FROM operations o JOIN execution_steps step ON step.operation_id=o.id JOIN transaction_attempts a ON a.step_id=step.id JOIN transaction_submissions sub ON sub.attempt_id=a.id WHERE sub.sequence=(SELECT MAX(s2.sequence) FROM transaction_submissions s2 WHERE s2.attempt_id=a.id) AND sub.state IN ('submitted','broadcast_unknown') ORDER BY o.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ControlledRecoveryItem
	for rows.Next() {
		var item ControlledRecoveryItem
		if err = rows.Scan(&item.OperationID, &item.AttemptID, &item.TxHash, &item.SubmissionState); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListCanonicalReceiptObservations(ctx context.Context, attempt string) ([]ReceiptObservation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,attempt_id,tx_hash,block_number,block_hash,receipt_status,canonical_state,reconciliation_version FROM receipt_observations WHERE attempt_id=? AND canonical_state IN ('canonical_success','canonical_revert') ORDER BY reconciliation_version`, attempt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReceiptObservation
	for rows.Next() {
		var v ReceiptObservation
		if err = rows.Scan(&v.ID, &v.AttemptID, &v.TxHash, &v.BlockNumber, &v.BlockHash, &v.Status, &v.CanonicalState, &v.Version); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) RecordControlledRecoveryEvidence(ctx context.Context, item ControlledRecoveryItem, evidence ControlledRecoveryEvidence, classification, environment, holder string, epoch uint64, now time.Time) error {
	if s == nil || s.db == nil || ctx == nil || item.OperationID == "" || item.AttemptID == "" || len(evidence.EvidenceHash) != 64 || evidence.ObservedAt.IsZero() || now.IsZero() || (classification != "PROPAGATED" && classification != "AMBIGUOUS" && classification != "NONCE_UNSAFE") {
		return ErrCanaryRuntimeRejected
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var expires string
	if err = tx.QueryRowContext(ctx, `SELECT expires_at FROM canary_worker_leases WHERE role='RECOVERY' AND environment=? AND holder_id=? AND lease_epoch=?`, environment, holder, epoch).Scan(&expires); err != nil {
		return ErrCanaryRecoveryLeaseLost
	}
	expiry, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil || !now.UTC().Before(expiry) {
		return ErrCanaryRecoveryLeaseLost
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	id := deterministicID("canary-recovery-evidence", item.AttemptID, evidence.EvidenceHash)
	_, err = tx.ExecContext(ctx, `INSERT INTO canary_recovery_evidence(id,operation_id,attempt_id,tx_hash,lease_environment,lease_holder_id,lease_epoch,tx_found,receipt_found,nonce_state,evidence_hash,classification,observed_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(attempt_id,evidence_hash) DO NOTHING`, id, item.OperationID, item.AttemptID, item.TxHash, environment, holder, epoch, evidence.TxFound, evidence.ReceiptFound, evidence.NonceState, evidence.EvidenceHash, classification, evidence.ObservedAt.UTC().Format(time.RFC3339Nano), stamp)
	if err != nil {
		return err
	}
	auditID := deterministicID("canary-runtime", "CONTROLLED_RECOVERY_EVIDENCE", item.OperationID, item.AttemptID, evidence.EvidenceHash)
	if _, err = tx.ExecContext(ctx, `INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,reason_code,details_json,created_at) VALUES(?,?,?,?,?,? ,?) ON CONFLICT(id) DO NOTHING`, auditID, "CONTROLLED_RECOVERY_EVIDENCE", item.OperationID, item.AttemptID, classification, `{"evidence":"durable"}`, stamp); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) VALUES(?,?,'INFO','PENDING',?) ON CONFLICT(id) DO NOTHING`, deterministicID("canary-alert", auditID), auditID, stamp); err != nil {
		return err
	}
	return tx.Commit()
}
