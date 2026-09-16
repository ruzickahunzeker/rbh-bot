CREATE TABLE feed_events(
  offset INTEGER PRIMARY KEY AUTOINCREMENT,
  observation_id TEXT NOT NULL UNIQUE,
  chain_id INTEGER NOT NULL CHECK(chain_id = 4663),
  source TEXT NOT NULL,
  source_sequence TEXT,
  tx_hash TEXT NOT NULL,
  stable_action_path TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  observed_at TEXT NOT NULL
);
CREATE TABLE feed_outbox(offset INTEGER PRIMARY KEY REFERENCES feed_events(offset), payload_json TEXT NOT NULL);

