package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Owner string

const (
	FeedOwner  Owner = "feed-service"
	BotOwner   Owner = "bot-service"
	TradeOwner Owner = "trade-service"
)

//go:embed migrations/*/*.sql
var migrationFiles embed.FS

type Database struct {
	owner Owner
	db    *sql.DB
}

func Open(ctx context.Context, owner Owner, path string) (*Database, error) {
	if ctx == nil || path == "" || !validOwner(owner) {
		return nil, errors.New("invalid database configuration")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", owner, err)
	}
	// One process owns one database. A single connection makes connection-local
	// SQLite PRAGMAs deterministic and leaves concurrency control to SQLite WAL.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := configure(ctx, db, owner); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Database{owner: owner, db: db}, nil
}

func (d *Database) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

func (d *Database) Ping(ctx context.Context) error {
	if d == nil || d.db == nil {
		return errors.New("database is closed")
	}
	return d.db.PingContext(ctx)
}

// SQLDB returns the underlying handle only to code that proves the expected
// service owner. This keeps service-owned write boundaries explicit while
// allowing domain stores to use transactions directly.
func (d *Database) SQLDB(owner Owner) (*sql.DB, error) {
	if d == nil || d.db == nil {
		return nil, errors.New("database is closed")
	}
	if owner != d.owner {
		return nil, fmt.Errorf("database owner mismatch: have %s want %s", d.owner, owner)
	}
	return d.db, nil
}

func (d *Database) Migrate(ctx context.Context) error {
	if d == nil || d.db == nil {
		return errors.New("database is closed")
	}
	entries, err := fs.Glob(migrationFiles, "migrations/"+ownerDir(d.owner)+"/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migration connection: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = conn.Close()
		}
	}()
	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(
        version INTEGER PRIMARY KEY,
        applied_at TEXT NOT NULL
    )`); err != nil {
		return fmt.Errorf("create migration journal: %w", err)
	}
	for _, name := range entries {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		var exists int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&exists); err != nil {
			return fmt.Errorf("read migration %d: %w", version, err)
		}
		if exists != 0 {
			continue
		}
		script, err := migrationFiles.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := conn.ExecContext(ctx, string(script)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)", version, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	committed = true
	if err := conn.Close(); err != nil {
		return fmt.Errorf("close migration connection: %w", err)
	}
	closed = true
	return d.Validate(ctx)
}

func (d *Database) Validate(ctx context.Context) error {
	wants := map[string]int{"foreign_keys": 1, "busy_timeout": 5000}
	if d.owner == TradeOwner {
		wants["synchronous"] = 2 // FULL
	}
	for pragma, want := range wants {
		var got int
		if err := d.db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
			return fmt.Errorf("read PRAGMA %s: %w", pragma, err)
		}
		if got != want {
			return fmt.Errorf("PRAGMA %s=%d, want %d", pragma, got, want)
		}
	}
	var mode string
	if err := d.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("read PRAGMA journal_mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("PRAGMA journal_mode=%s, want wal", mode)
	}
	return nil
}

func configure(ctx context.Context, db *sql.DB, owner Owner) error {
	statements := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	if owner == TradeOwner {
		statements = append(statements, "PRAGMA synchronous=FULL")
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure %s: %w", owner, err)
		}
	}
	return db.PingContext(ctx)
}

func validOwner(owner Owner) bool {
	return owner == FeedOwner || owner == BotOwner || owner == TradeOwner
}

func ownerDir(owner Owner) string {
	return strings.TrimSuffix(string(owner), "-service")
}

func migrationVersion(name string) (int64, error) {
	base := name[strings.LastIndex(name, "/")+1:]
	prefix, _, ok := strings.Cut(base, "_")
	if !ok {
		return 0, fmt.Errorf("invalid migration name %q", name)
	}
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("invalid migration version %q", name)
	}
	return version, nil
}
