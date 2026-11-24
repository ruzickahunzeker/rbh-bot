-- Milestone 2 (Step 3A-1): Persistent feed watchlist to filter 99%+ unrelated swaps
-- on high-throughput chains (e.g. BSC) while surviving feed restarts with zero blind window.

CREATE TABLE feed_watchlist_state(
  id INTEGER PRIMARY KEY CHECK(id = 1),
  version INTEGER NOT NULL DEFAULT 0 CHECK(version >= 0),
  updated_at TEXT NOT NULL
);
INSERT INTO feed_watchlist_state(id, version, updated_at)
VALUES(1, 0, '1970-01-01T00:00:00Z');

CREATE TABLE feed_watchlist_entries(
  kind TEXT NOT NULL CHECK(kind IN ('wallet', 'token')),
  address TEXT NOT NULL,
  PRIMARY KEY(kind, address)
);
