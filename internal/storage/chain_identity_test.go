package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestVerifyOrInitChainIdentityEmptyAndMismatch(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "rbh.db")
	db, err := Open(ctx, TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rbh := ChainIdentity{ChainID: 4663, ChainSlug: "rbh", NetworkKind: "mainnet"}
	bsc := ChainIdentity{ChainID: 56, ChainSlug: "bsc", NetworkKind: "mainnet"}

	if err := db.VerifyOrInitChainIdentity(ctx, rbh); err != nil {
		t.Fatalf("empty DB auto-init failed: %v", err)
	}
	if err := db.VerifyOrInitChainIdentity(ctx, rbh); err != nil {
		t.Fatalf("repeat verification failed: %v", err)
	}
	if err := db.VerifyOrInitChainIdentity(ctx, bsc); !errors.Is(err, ErrWrongChainDatabase) {
		t.Fatalf("expected ErrWrongChainDatabase for wrong chain, got %v", err)
	}
	// Identity row must be immutable via triggers.
	if _, err := db.db.ExecContext(ctx, `UPDATE instance_chain_identity SET chain_id = 56 WHERE id = 1`); err == nil {
		t.Fatal("expected UPDATE on instance_chain_identity to be blocked")
	}
	if _, err := db.db.ExecContext(ctx, `DELETE FROM instance_chain_identity WHERE id = 1`); err == nil {
		t.Fatal("expected DELETE on instance_chain_identity to be blocked")
	}
}

func TestLegacyUnboundDatabaseRequiresExplicitBindAndRejectsConflict(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(ctx, TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Simulate an existing legacy database migrated without instance_chain_identity.
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx,
		`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at) VALUES('op-1',4663,'w1','k1','fp1','swap','submitted','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	rbh := ChainIdentity{ChainID: 4663, ChainSlug: "rbh", NetworkKind: "mainnet"}
	bsc := ChainIdentity{ChainID: 56, ChainSlug: "bsc", NetworkKind: "mainnet"}

	if err := db.VerifyOrInitChainIdentity(ctx, rbh); !errors.Is(err, ErrLegacyDatabaseUnbound) {
		t.Fatalf("expected ErrLegacyDatabaseUnbound on non-empty legacy DB, got %v", err)
	}
	if err := db.BindLegacyChainIdentity(ctx, bsc); !errors.Is(err, ErrWrongChainDatabase) {
		t.Fatalf("expected ErrWrongChainDatabase when binding conflicting chain_id=56, got %v", err)
	}
	if err := db.BindLegacyChainIdentity(ctx, rbh); err != nil {
		t.Fatalf("expected legacy bind for matching chain_id=4663 to succeed: %v", err)
	}
	if err := db.VerifyOrInitChainIdentity(ctx, rbh); err != nil {
		t.Fatalf("verification after bind failed: %v", err)
	}
}

func TestVacuumBackupProducesConsistentSnapshot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(ctx, TradeOwner, filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.VerifyOrInitChainIdentity(ctx, ChainIdentity{ChainID: 4663, ChainSlug: "rbh", NetworkKind: "mainnet"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	backupPath := filepath.Join(dir, "backups", "snap.db")
	digest, size, err := db.VacuumBackup(ctx, backupPath)
	if err != nil {
		t.Fatalf("VacuumBackup failed: %v", err)
	}
	if len(digest) != 64 || size <= 0 {
		t.Fatalf("unexpected backup metadata: sha256=%q size=%d", digest, size)
	}
	// Refuses to overwrite existing snapshot.
	if _, _, err := db.VacuumBackup(ctx, backupPath); err == nil {
		t.Fatal("expected overwrite refusal on existing backup file")
	}
	// Restored snapshot opens cleanly and retains chain identity.
	restored, err := Open(ctx, TradeOwner, backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.VerifyOrInitChainIdentity(ctx, ChainIdentity{ChainID: 4663, ChainSlug: "rbh", NetworkKind: "mainnet"}); err != nil {
		t.Fatalf("restored backup chain identity mismatch: %v", err)
	}
}

func TestReadDBPoolMultiConnectionPRAGMAs(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, TradeOwner, filepath.Join(t.TempDir(), "pool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	pool, err := db.ReadDB(TradeOwner)
	if err != nil {
		t.Fatal(err)
	}
	// Hold 4 distinct connections simultaneously to force SQLite pool expansion and verify PRAGMAs on each.
	conns := make([]*sql.Conn, 0, 4)
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for i := 0; i < 4; i++ {
		c, err := pool.Conn(ctx)
		if err != nil {
			t.Fatalf("open read conn %d: %v", i, err)
		}
		conns = append(conns, c)
		var queryOnly, fk, busy int
		if err := c.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil || queryOnly != 1 {
			t.Fatalf("conn %d query_only=%d err=%v", i, queryOnly, err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
			t.Fatalf("conn %d foreign_keys=%d err=%v", i, fk, err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil || busy != DefaultBusyTimeoutMS {
			t.Fatalf("conn %d busy_timeout=%d err=%v", i, busy, err)
		}
	}
}
