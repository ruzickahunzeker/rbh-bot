PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
CREATE TABLE watched_wallets(id TEXT PRIMARY KEY, chain_id INTEGER NOT NULL CHECK(chain_id = 4663), address TEXT NOT NULL, enabled INTEGER NOT NULL);
CREATE TABLE strategies(id TEXT PRIMARY KEY, watched_wallet_id TEXT NOT NULL REFERENCES watched_wallets(id), version INTEGER NOT NULL, enabled INTEGER NOT NULL, config_json TEXT NOT NULL);
CREATE TABLE signal_inbox(event_id TEXT PRIMARY KEY, received_at TEXT NOT NULL, payload_json TEXT NOT NULL);

