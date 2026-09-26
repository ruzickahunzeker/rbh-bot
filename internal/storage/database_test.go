package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenMigrateAndValidateEveryOwner(t *testing.T) {
	wantMigrations := map[Owner]int{
		FeedOwner:  4,
		BotOwner:   3,
		TradeOwner: 5,
	}
	for _, owner := range []Owner{FeedOwner, BotOwner, TradeOwner} {
		t.Run(string(owner), func(t *testing.T) {
			database, err := Open(context.Background(), owner, filepath.Join(t.TempDir(), "service.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if err := database.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := database.Migrate(context.Background()); err != nil {
				t.Fatalf("migrations must be idempotent: %v", err)
			}
			var count int
			if err := database.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != wantMigrations[owner] {
				t.Fatalf("got %d migrations, want %d", count, wantMigrations[owner])
			}
		})
	}
}

func TestMigrationLockHonorsContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.db")
	database, err := Open(context.Background(), FeedOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	locker, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	connection, err := locker.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	defer connection.ExecContext(context.Background(), "ROLLBACK")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := database.Migrate(ctx); err == nil {
		t.Fatal("expected locked migration to fail closed")
	}
}

func TestRejectsUnknownOwner(t *testing.T) {
	if _, err := Open(context.Background(), Owner("other-service"), filepath.Join(t.TempDir(), "other.db")); err == nil {
		t.Fatal("expected unknown database owner to be rejected")
	}
}
