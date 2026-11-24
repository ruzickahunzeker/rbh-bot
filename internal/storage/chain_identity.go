package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrWrongChainDatabase   = errors.New("database chain identity does not match instance configuration")
	ErrLegacyDatabaseUnbound = errors.New("non-empty legacy database has no chain identity; run operator db bind-chain first")
)

// ChainIdentity uniquely identifies the chain and network bound to a SQLite database.
type ChainIdentity struct {
	ChainID     uint64
	ChainSlug   string
	NetworkKind string
}

func (c ChainIdentity) valid() bool {
	return c.ChainID > 0 && strings.TrimSpace(c.ChainSlug) != "" && strings.TrimSpace(c.NetworkKind) != ""
}

const chainIdentityDDL = `
CREATE TABLE instance_chain_identity (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	chain_id INTEGER NOT NULL CHECK (chain_id > 0),
	chain_slug TEXT NOT NULL CHECK (length(trim(chain_slug)) > 0),
	network_kind TEXT NOT NULL CHECK (length(trim(network_kind)) > 0),
	bound_at TEXT NOT NULL
);
CREATE TRIGGER instance_chain_identity_no_update
BEFORE UPDATE ON instance_chain_identity
BEGIN
	SELECT RAISE(ABORT, 'INSTANCE_CHAIN_IDENTITY_IMMUTABLE');
END;
CREATE TRIGGER instance_chain_identity_no_delete
BEFORE DELETE ON instance_chain_identity
BEGIN
	SELECT RAISE(ABORT, 'INSTANCE_CHAIN_IDENTITY_IMMUTABLE');
END;
`

// VerifyOrInitChainIdentity runs BEFORE any write migration:
// - If instance_chain_identity exists, verifies exact match (fails closed on mismatch with zero writes).
// - If DB is brand-new empty, creates instance_chain_identity and binds expected.
// - If DB is non-empty legacy without instance_chain_identity, fails closed with ErrLegacyDatabaseUnbound.
func (d *Database) VerifyOrInitChainIdentity(ctx context.Context, expected ChainIdentity) error {
	if d == nil || d.db == nil || !expected.valid() {
		return errors.New("invalid chain identity verification inputs")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin chain identity check: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	exists, err := tableExistsTx(ctx, tx, "instance_chain_identity")
	if err != nil {
		return err
	}
	if exists {
		var got ChainIdentity
		if err := tx.QueryRowContext(ctx, `SELECT chain_id, chain_slug, network_kind FROM instance_chain_identity WHERE id = 1`).Scan(&got.ChainID, &got.ChainSlug, &got.NetworkKind); err != nil {
			return fmt.Errorf("%w: unreadable identity row: %v", ErrWrongChainDatabase, err)
		}
		if got.ChainID != expected.ChainID || got.ChainSlug != expected.ChainSlug || got.NetworkKind != expected.NetworkKind {
			return fmt.Errorf("%w: db=(chain_id=%d,slug=%s,net=%s) want=(chain_id=%d,slug=%s,net=%s)",
				ErrWrongChainDatabase, got.ChainID, got.ChainSlug, got.NetworkKind, expected.ChainID, expected.ChainSlug, expected.NetworkKind)
		}
		return nil
	}

	userTables, err := countUserTablesTx(ctx, tx)
	if err != nil {
		return err
	}
	if userTables > 0 {
		return ErrLegacyDatabaseUnbound
	}
	if _, err := tx.ExecContext(ctx, chainIdentityDDL); err != nil {
		return fmt.Errorf("create instance_chain_identity: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO instance_chain_identity(id, chain_id, chain_slug, network_kind, bound_at) VALUES(1, ?, ?, ?, ?)`,
		expected.ChainID, expected.ChainSlug, expected.NetworkKind, time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("insert instance_chain_identity: %w", err)
	}
	return tx.Commit()
}

// BindLegacyChainIdentity explicitly binds a non-empty legacy database after verifying
// no historical operations belong to a conflicting chain_id.
func (d *Database) BindLegacyChainIdentity(ctx context.Context, expected ChainIdentity) error {
	if d == nil || d.db == nil || !expected.valid() {
		return errors.New("invalid chain identity bind inputs")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy chain bind: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	exists, err := tableExistsTx(ctx, tx, "instance_chain_identity")
	if err != nil {
		return err
	}
	if exists {
		var got ChainIdentity
		if err := tx.QueryRowContext(ctx, `SELECT chain_id, chain_slug, network_kind FROM instance_chain_identity WHERE id = 1`).Scan(&got.ChainID, &got.ChainSlug, &got.NetworkKind); err != nil {
			return fmt.Errorf("%w: %v", ErrWrongChainDatabase, err)
		}
		if got.ChainID != expected.ChainID || got.ChainSlug != expected.ChainSlug || got.NetworkKind != expected.NetworkKind {
			return fmt.Errorf("%w: already bound to chain_id=%d slug=%s", ErrWrongChainDatabase, got.ChainID, got.ChainSlug)
		}
		return nil
	}

	// Verify no historical operations contradict expected.ChainID.
	if hasOps, err := tableExistsTx(ctx, tx, "operations"); err != nil {
		return err
	} else if hasOps {
		var conflicting int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM operations WHERE chain_id != ?`, expected.ChainID).Scan(&conflicting); err != nil {
			return fmt.Errorf("verify legacy operations chain_id: %w", err)
		}
		if conflicting > 0 {
			return fmt.Errorf("%w: found %d historical operations with chain_id != %d", ErrWrongChainDatabase, conflicting, expected.ChainID)
		}
	}

	if _, err := tx.ExecContext(ctx, chainIdentityDDL); err != nil {
		return fmt.Errorf("create instance_chain_identity: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO instance_chain_identity(id, chain_id, chain_slug, network_kind, bound_at) VALUES(1, ?, ?, ?, ?)`,
		expected.ChainID, expected.ChainSlug, expected.NetworkKind, time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("insert instance_chain_identity: %w", err)
	}
	return tx.Commit()
}

// VacuumBackup creates a single-file consistent SQLite snapshot using VACUUM INTO
// through the single writer connection and returns its SHA-256 hex digest.
func (d *Database) VacuumBackup(ctx context.Context, destPath string) (sha256Hex string, sizeBytes int64, err error) {
	if d == nil || d.db == nil || strings.TrimSpace(destPath) == "" {
		return "", 0, errors.New("invalid backup arguments")
	}
	clean := filepath.Clean(destPath)
	if !filepath.IsAbs(clean) {
		return "", 0, errors.New("backup destination path must be absolute")
	}
	if _, statErr := os.Stat(clean); statErr == nil {
		return "", 0, fmt.Errorf("refusing to overwrite existing backup file %s", clean)
	}
	if err := os.MkdirAll(filepath.Dir(clean), 0o750); err != nil {
		return "", 0, fmt.Errorf("prepare backup directory: %w", err)
	}
	// VACUUM INTO creates an atomic, defragmented, self-contained SQLite file.
	if _, err := d.db.ExecContext(ctx, `VACUUM INTO ?`, clean); err != nil {
		return "", 0, fmt.Errorf("vacuum into %s: %w", clean, err)
	}
	f, err := os.Open(clean)
	if err != nil {
		return "", 0, fmt.Errorf("open backup snapshot: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("hash backup snapshot: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func tableExistsTx(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func countUserTablesTx(ctx context.Context, tx *sql.Tx) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
