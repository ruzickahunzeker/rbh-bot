CREATE UNIQUE INDEX watched_wallets_chain_address_idx ON watched_wallets(chain_id, address);

ALTER TABLE signal_inbox ADD COLUMN feed_offset INTEGER;
ALTER TABLE signal_inbox ADD COLUMN observation_id TEXT;
ALTER TABLE signal_inbox ADD COLUMN disposition TEXT NOT NULL DEFAULT 'received';
CREATE UNIQUE INDEX signal_inbox_feed_offset_idx ON signal_inbox(feed_offset);
CREATE UNIQUE INDEX signal_inbox_observation_id_idx ON signal_inbox(observation_id);

CREATE TABLE bot_progress(
  id INTEGER PRIMARY KEY CHECK(id = 1),
  durable_event_offset INTEGER NOT NULL DEFAULT 0 CHECK(durable_event_offset >= 0),
  updated_at TEXT NOT NULL
);
INSERT INTO bot_progress(id, durable_event_offset, updated_at)
VALUES(1, 0, '1970-01-01T00:00:00Z');

CREATE TABLE operation_intents(
  id TEXT PRIMARY KEY,
  idempotency_key TEXT NOT NULL UNIQUE,
  strategy_id TEXT NOT NULL REFERENCES strategies(id),
  source_event_id TEXT NOT NULL,
  source_observation_id TEXT NOT NULL,
  source_tx_hash TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('copy_buy', 'copy_sell')),
  token TEXT NOT NULL,
  amount_mode TEXT NOT NULL CHECK(amount_mode IN ('fixed_input', 'balance_bps')),
  amount_value TEXT NOT NULL,
  policy_version INTEGER NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('created', 'retracted')),
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  retracted_at TEXT
);
CREATE INDEX operation_intents_source_observation_idx ON operation_intents(source_observation_id);
CREATE INDEX operation_intents_strategy_idx ON operation_intents(strategy_id, created_at);
