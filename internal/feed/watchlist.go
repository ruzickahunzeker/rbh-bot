package feed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

// WatchlistSnapshot is synced via PUT /internal/v1/watchlist and persisted in feed-<chain>.db.
type WatchlistSnapshot struct {
	Version int64    `json:"version"`
	Wallets []string `json:"wallets"`
	Tokens  []string `json:"tokens"`
}

// WatchlistStore maintains the persisted watched wallets/tokens filter in feed-<chain>.db
// plus an in-memory cache restored automatically on startup so feed restarts never drop events.
type WatchlistStore struct {
	db      *sql.DB
	mu      sync.RWMutex
	version int64
	wallets map[string]struct{}
	tokens  map[string]struct{}
}

func NewWatchlistStore(database *storage.Database) (*WatchlistStore, error) {
	if database == nil {
		return nil, errors.New("feed database is required")
	}
	db, err := database.SQLDB(storage.FeedOwner)
	if err != nil {
		return nil, err
	}
	ws := &WatchlistStore{
		db:      db,
		wallets: make(map[string]struct{}),
		tokens:  make(map[string]struct{}),
	}
	if err := ws.Reload(context.Background()); err != nil {
		return nil, err
	}
	return ws, nil
}

// Reload hydrates the in-memory filter from SQLite feed_watchlist_* tables on startup.
func (w *WatchlistStore) Reload(ctx context.Context) error {
	var ver int64
	if err := w.db.QueryRowContext(ctx, `SELECT version FROM feed_watchlist_state WHERE id = 1`).Scan(&ver); err != nil {
		return fmt.Errorf("load watchlist state: %w", err)
	}
	rows, err := w.db.QueryContext(ctx, `SELECT kind, address FROM feed_watchlist_entries`)
	if err != nil {
		return fmt.Errorf("load watchlist entries: %w", err)
	}
	defer rows.Close()
	wallets := make(map[string]struct{})
	tokens := make(map[string]struct{})
	for rows.Next() {
		var kind, addr string
		if err := rows.Scan(&kind, &addr); err != nil {
			return err
		}
		switch kind {
		case "wallet":
			wallets[strings.ToLower(addr)] = struct{}{}
		case "token":
			tokens[strings.ToLower(addr)] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	w.version = ver
	w.wallets = wallets
	w.tokens = tokens
	w.mu.Unlock()
	return nil
}

// Replace atomically persists a monotonically newer watchlist snapshot into SQLite and updates memory.
func (w *WatchlistStore) Replace(ctx context.Context, snap WatchlistSnapshot) error {
	if snap.Version <= 0 {
		return errors.New("watchlist version must be positive")
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var currentVer int64
	if err := tx.QueryRowContext(ctx, `SELECT version FROM feed_watchlist_state WHERE id = 1`).Scan(&currentVer); err != nil {
		return err
	}
	if snap.Version < currentVer {
		return fmt.Errorf("stale watchlist version %d < current %d", snap.Version, currentVer)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM feed_watchlist_entries`); err != nil {
		return err
	}
	wallets := make(map[string]struct{}, len(snap.Wallets))
	for _, raw := range snap.Wallets {
		if !common.IsHexAddress(raw) {
			return fmt.Errorf("invalid wallet address %q", raw)
		}
		addr := strings.ToLower(common.HexToAddress(raw).Hex())
		wallets[addr] = struct{}{}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO feed_watchlist_entries(kind, address) VALUES('wallet', ?)`, addr); err != nil {
			return err
		}
	}
	tokens := make(map[string]struct{}, len(snap.Tokens))
	for _, raw := range snap.Tokens {
		if !common.IsHexAddress(raw) {
			return fmt.Errorf("invalid token address %q", raw)
		}
		addr := strings.ToLower(common.HexToAddress(raw).Hex())
		tokens[addr] = struct{}{}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO feed_watchlist_entries(kind, address) VALUES('token', ?)`, addr); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE feed_watchlist_state SET version = ?, updated_at = ? WHERE id = 1`,
		snap.Version, time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.mu.Lock()
	w.version = snap.Version
	w.wallets = wallets
	w.tokens = tokens
	w.mu.Unlock()
	return nil
}

// Initialized reports whether an initial watchlist snapshot (version >= 1) has been synced.
func (w *WatchlistStore) Initialized() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.version > 0
}

// Matches returns true if trader or token is in the active persisted watchlist.
func (w *WatchlistStore) Matches(trader, token string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.version == 0 {
		return false
	}
	if _, ok := w.wallets[strings.ToLower(trader)]; ok {
		return true
	}
	if _, ok := w.tokens[strings.ToLower(token)]; ok {
		return true
	}
	return false
}

// Snapshot returns a deterministic copy of the active persisted watchlist.
func (w *WatchlistStore) Snapshot() WatchlistSnapshot {
	w.mu.RLock()
	defer w.mu.RUnlock()
	wallets := make([]string, 0, len(w.wallets))
	for a := range w.wallets {
		wallets = append(wallets, a)
	}
	sort.Strings(wallets)
	tokens := make([]string, 0, len(w.tokens))
	for a := range w.tokens {
		tokens = append(tokens, a)
	}
	sort.Strings(tokens)
	return WatchlistSnapshot{
		Version: w.version,
		Wallets: wallets,
		Tokens:  tokens,
	}
}

// MatchesPayload extracts trader/sender and token addresses from a normalized observation JSON payload
// (or fallbackTrader) and returns true if either matches the active watchlist.
func (w *WatchlistStore) MatchesPayload(fallbackTrader string, payloadJSON []byte) bool {
	if w == nil || !w.Initialized() {
		return true
	}
	if fallbackTrader != "" && w.Matches(fallbackTrader, "") {
		return true
	}
	var fields struct {
		Trader string `json:"trader"`
		Sender string `json:"sender"`
		From   string `json:"from"`
		Token  string `json:"token"`
	}
	if err := json.Unmarshal(payloadJSON, &fields); err == nil {
		for _, cand := range []string{fields.Trader, fields.Sender, fields.From} {
			if cand != "" && w.Matches(cand, fields.Token) {
				return true
			}
		}
		if fields.Token != "" && w.Matches("", fields.Token) {
			return true
		}
	}
	return false
}

// NewWatchlistHTTPHandler serves GET and PUT /internal/v1/watchlist on feed-<chain>.sock.
func NewWatchlistHTTPHandler(store *WatchlistStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(store.Snapshot())
		case http.MethodPut:
			var snap WatchlistSnapshot
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&snap); err != nil {
				http.Error(w, "invalid watchlist payload", http.StatusBadRequest)
				return
			}
			if err := store.Replace(r.Context(), snap); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": snap.Version})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

// ComputeTriggerID builds the cross-source, cross-pool-split net economic action ID:
// "<chain_id>:<lower(tx_hash)>:<lower(trader)>:<lower(token)>:<side>".
// Deliberately excludes source, block_hash, and sub-pool protocol so mempool+receipt
// and multi-pool aggregator splits (e.g. Pancake V2+V3) collapse to 1 copy-trade intent.
func ComputeTriggerID(chainID uint64, txHash, trader, token, side string) string {
	return fmt.Sprintf("%d:%s:%s:%s:%s",
		chainID,
		strings.ToLower(strings.TrimSpace(txHash)),
		strings.ToLower(strings.TrimSpace(trader)),
		strings.ToLower(strings.TrimSpace(token)),
		strings.ToLower(strings.TrimSpace(side)),
	)
}
