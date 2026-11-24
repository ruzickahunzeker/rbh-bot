package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	DefaultBusyTimeoutMS    = 5000
	DefaultJournalSizeLimit = 64 * 1024 * 1024 // 64 MiB
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
	owner  Owner
	path   string
	db     *sql.DB
	readMu sync.Mutex
	readDB *sql.DB
	closed bool
}

func Open(ctx context.Context, owner Owner, path string) (*Database, error) {
	if ctx == nil || path == "" || !validOwner(owner) {
		return nil, errors.New("invalid database configuration")
	}
	writerDSN := buildSQLiteDSN(path, owner, false)
	db, err := sql.Open("sqlite", writerDSN)
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", owner, err)
	}
	// One process owns one database. A single writer connection serializes writes
	// cleanly while DSN PRAGMAs + _txlock=immediate make every connection deterministic.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := configure(ctx, db, owner); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Database{owner: owner, path: path, db: db}, nil
}

func (d *Database) Close() error {
	if d == nil {
		return nil
	}
	d.readMu.Lock()
	d.closed = true
	rdb := d.readDB
	d.readDB = nil
	d.readMu.Unlock()
	var errs []error
	if rdb != nil {
		if err := rdb.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if d.db != nil {
		if err := d.db.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (d *Database) Ping(ctx context.Context) error {
	if d == nil || d.db == nil {
		return errors.New("database is closed")
	}
	return d.db.PingContext(ctx)
}

// SQLDB returns the single-writer handle only to code that proves the expected
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

// ReadDB lazily opens and returns a bounded read-only SQLite connection pool
// (mode=ro, query_only=1) for read queries without contending with the single writer.
func (d *Database) ReadDB(owner Owner) (*sql.DB, error) {
	if d == nil || d.db == nil {
		return nil, errors.New("database is closed")
	}
	if owner != d.owner {
		return nil, fmt.Errorf("database owner mismatch: have %s want %s", d.owner, owner)
	}
	if d.path == ":memory:" || strings.HasPrefix(d.path, "file::memory:") {
		return d.db, nil
	}
	d.readMu.Lock()
	defer d.readMu.Unlock()
	if d.closed {
		return nil, errors.New("database is closed")
	}
	if d.readDB != nil {
		return d.readDB, nil
	}
	readDSN := buildSQLiteDSN(d.path, d.owner, true)
	rdb, err := sql.Open("sqlite", readDSN)
	if err != nil {
		return nil, fmt.Errorf("open %s read pool: %w", d.owner, err)
	}
	rdb.SetMaxOpenConns(4)
	rdb.SetMaxIdleConns(4)
	rdb.SetConnMaxIdleTime(2 * time.Minute)
	var queryOnly int
	if err := rdb.QueryRow("PRAGMA query_only").Scan(&queryOnly); err != nil || queryOnly != 1 {
		_ = rdb.Close()
		return nil, fmt.Errorf("validate %s read pool query_only: %v", d.owner, err)
	}
	d.readDB = rdb
	return d.readDB, nil
}

// WALSizeBytes returns the current on-disk size of the SQLite WAL file (0 if absent).
func (d *Database) WALSizeBytes() int64 {
	if d == nil || d.path == "" || d.path == ":memory:" || strings.HasPrefix(d.path, "file:") {
		return 0
	}
	info, err := os.Stat(d.path + "-wal")
	if err != nil {
		return 0
	}
	return info.Size()
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
	wants := map[string]int{
		"foreign_keys":       1,
		"busy_timeout":       DefaultBusyTimeoutMS,
		"journal_size_limit": DefaultJournalSizeLimit,
	}
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
	if d.readDB != nil {
		var queryOnly int
		if err := d.readDB.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil {
			return fmt.Errorf("read pool PRAGMA query_only: %w", err)
		}
		if queryOnly != 1 {
			return fmt.Errorf("read pool PRAGMA query_only=%d, want 1", queryOnly)
		}
	}
	return nil
}

func configure(ctx context.Context, db *sql.DB, owner Owner) error {
	statements := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		fmt.Sprintf("PRAGMA busy_timeout=%d", DefaultBusyTimeoutMS),
		fmt.Sprintf("PRAGMA journal_size_limit=%d", DefaultJournalSizeLimit),
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

func buildSQLiteDSN(path string, owner Owner, readOnly bool) string {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return path
	}
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", DefaultBusyTimeoutMS))
	if readOnly {
		q.Set("mode", "ro")
		q.Add("_pragma", "query_only(1)")
		q.Add("_pragma", "foreign_keys(1)")
	} else {
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "foreign_keys(1)")
		q.Add("_pragma", fmt.Sprintf("journal_size_limit(%d)", DefaultJournalSizeLimit))
		if owner == TradeOwner {
			q.Add("_pragma", "synchronous(FULL)")
		}
		q.Set("_txlock", "immediate")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	return u.String()
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
