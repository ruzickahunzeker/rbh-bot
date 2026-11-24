package tradeapi

import (
	"context"
	"errors"
	"fmt"
)

// manualSubmissionFoundationWriter formally persists manual_submission_foundations
// derived strictly from the durable manual_submission_foundation_expected projection.
type manualSubmissionFoundationWriter struct {
	store *Store
}

func newManualSubmissionFoundationWriter(store *Store) (*manualSubmissionFoundationWriter, error) {
	if store == nil || store.db == nil {
		return nil, ErrInvalid
	}
	return &manualSubmissionFoundationWriter{store: store}, nil
}

func (w *manualSubmissionFoundationWriter) write(ctx context.Context, operationID string) error {
	if w == nil || w.store == nil || w.store.db == nil || operationID == "" {
		return ErrInvalid
	}
	tx, err := w.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin submission foundation tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var binding, digest, created string
	if err := tx.QueryRowContext(ctx,
		`SELECT binding_json, manual_artifact_bytes_sha256(hex(binding_json)), artifact_completed_at
		 FROM manual_submission_foundation_expected WHERE operation_id = ?`, operationID,
	).Scan(&binding, &digest, &created); err != nil {
		return fmt.Errorf("read submission foundation expected for %s: %w", operationID, err)
	}
	if binding == "" || digest == "" || created == "" {
		return errors.New("incomplete submission foundation projection")
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO manual_submission_foundations(operation_id, binding_json, binding_hash, created_at) VALUES(?,?,?,?)`,
		operationID, binding, digest, created,
	); err != nil {
		return fmt.Errorf("insert manual_submission_foundations for %s: %w", operationID, err)
	}
	return tx.Commit()
}
