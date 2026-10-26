package trade

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// manualRecoverySourceTx distinguishes a historical MANUAL identity from the
// legacy dry-run execution format. Missing provenance must never fall back to
// legacy accounting or release a signed MANUAL nonce.
func manualRecoverySourceTx(ctx context.Context, tx *sql.Tx, a SignedArtifact) (bool, error) {
	var manual int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=?)
 OR EXISTS(SELECT 1 FROM manual_nonce_claims WHERE operation_id=? OR attempt_id=?)`, a.Operation, a.Operation, a.AttemptID).Scan(&manual); err != nil {
		return false, err
	}
	if manual == 0 && !strings.HasPrefix(a.Operation, "manual_op_") {
		return false, nil
	}
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM manual_recovery_source
 WHERE operation_id=? AND execution_step_id=? AND attempt_id=? AND wallet_id=? AND tx_hash=? AND nonce=?`,
		a.Operation, a.StepID, a.AttemptID, a.WalletID, a.TxHash, fmt.Sprint(a.Nonce)).Scan(&count)
	if err != nil {
		return true, err
	}
	if count != 1 {
		return true, ErrArtifactIntegrity
	}
	return true, nil
}

// lockManualRecoveryTx acquires the SQLite writer lock before checking the
// lease. Its no-op write neither renews a lease nor changes its identity.
func lockManualRecoveryTx(ctx context.Context, tx *sql.Tx, lease *RecoveryLeaseFence) error {
	if lease == nil || lease.Environment == "" || lease.HolderID == "" || lease.Epoch == 0 {
		return ErrCanaryRecoveryLeaseLost
	}
	r, err := tx.ExecContext(ctx, `UPDATE canary_worker_leases SET updated_at=updated_at
 WHERE role='RECOVERY' AND environment=? AND holder_id=? AND lease_epoch=?`, lease.Environment, lease.HolderID, lease.Epoch)
	if err != nil {
		return err
	}
	if n, err := r.RowsAffected(); err != nil || n != 1 {
		return ErrCanaryRecoveryLeaseLost
	}
	return assertRecoveryLeaseTx(ctx, tx, lease)
}

func loadManualReceiptTx(ctx context.Context, tx *sql.Tx, id string) (ReceiptObservation, bool, error) {
	var r ReceiptObservation
	err := tx.QueryRowContext(ctx, `SELECT id,attempt_id,tx_hash,block_number,block_hash,receipt_status,canonical_state,reconciliation_version
 FROM receipt_observations WHERE id=?`, id).Scan(&r.ID, &r.AttemptID, &r.TxHash, &r.BlockNumber, &r.BlockHash, &r.Status, &r.CanonicalState, &r.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

func sameManualReceipt(a, b ReceiptObservation) bool {
	return a.ID == b.ID && a.AttemptID == b.AttemptID && a.TxHash == b.TxHash && a.BlockNumber == b.BlockNumber && a.BlockHash == b.BlockHash && a.Status == b.Status
}

func insertManualRecoveryCommandTx(ctx context.Context, tx *sql.Tx, a SignedArtifact, r ReceiptObservation, kind string, effect *PositionEffect, lease *RecoveryLeaseFence, now time.Time) error {
	if now.IsZero() || lease == nil {
		return ErrCanaryRecoveryLeaseLost
	}
	var asset, delta any
	if effect != nil {
		asset, delta = effect.Asset, effect.Delta
	}
	id := "manual-recovery:" + r.ID + ":" + fmt.Sprint(r.Version)
	_, err := tx.ExecContext(ctx, `INSERT INTO manual_recovery_commands(id,operation_id,kind,receipt_id,tx_hash,block_number,block_hash,receipt_status,prior_version,
 effect_asset,effect_delta,environment,holder_id,lease_epoch,checked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, a.Operation, kind, r.ID, r.TxHash, r.BlockNumber, r.BlockHash, r.Status, r.Version,
		asset, delta, lease.Environment, lease.HolderID, lease.Epoch, now.UTC().Format(time.RFC3339Nano))
	return err
}

func observeManualReceiptTx(ctx context.Context, tx *sql.Tx, a SignedArtifact, r ReceiptObservation, now time.Time, lease *RecoveryLeaseFence) (ReceiptObservation, error) {
	if err := lockManualRecoveryTx(ctx, tx, lease); err != nil {
		return ReceiptObservation{}, err
	}
	old, found, err := loadManualReceiptTx(ctx, tx, r.ID)
	if err != nil {
		return ReceiptObservation{}, err
	}
	if found {
		if !sameManualReceipt(old, r) {
			return ReceiptObservation{}, ErrArtifactIntegrity
		}
		return old, nil
	}
	r.Version = 0
	if err := insertManualRecoveryCommandTx(ctx, tx, a, r, "OBSERVE", nil, lease, now); err != nil {
		return ReceiptObservation{}, err
	}
	r.Version, r.CanonicalState = 1, "observed"
	return r, nil
}

func transitionManualReceiptTx(ctx context.Context, tx *sql.Tx, a SignedArtifact, r ReceiptObservation, kind string, effect *PositionEffect, now time.Time, lease *RecoveryLeaseFence) error {
	if err := lockManualRecoveryTx(ctx, tx, lease); err != nil {
		return err
	}
	old, found, err := loadManualReceiptTx(ctx, tx, r.ID)
	if err != nil {
		return err
	}
	if !found || !sameManualReceipt(old, r) {
		return ErrArtifactIntegrity
	}
	if (kind == "ORPHAN" && old.CanonicalState == "orphaned") ||
		(kind == "CANONICAL" && (old.CanonicalState == "canonical_success" || old.CanonicalState == "canonical_revert")) {
		return nil
	}
	// The observation read by the RPC phase is an optimistic version fence.
	// Never apply a stale pre-reorg RPC answer to a later receipt generation.
	if r.Version != 0 && r.Version != old.Version {
		return ErrArtifactIntegrity
	}
	return insertManualRecoveryCommandTx(ctx, tx, a, old, kind, effect, lease, now)
}
