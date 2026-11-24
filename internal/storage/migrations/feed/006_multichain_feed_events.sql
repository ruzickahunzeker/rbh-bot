-- Milestone 5 (Module R): Lift single-chain CHECK(chain_id = 4663) on feed_events
-- to CHECK(chain_id > 0) so feed-service instances for BSC (56) and Base (8453)
-- can persist V2/V3 Swap observations into feed_events and feed_outbox.

CREATE TEMP TABLE _mig_feed_events AS
SELECT offset, observation_id, chain_id, source, source_sequence, tx_hash, stable_action_path, payload_json, observed_at, inclusion
FROM feed_events;

CREATE TEMP TABLE _mig_feed_outbox AS
SELECT offset, payload_json FROM feed_outbox;

DROP TABLE feed_outbox;
DROP TABLE feed_events;

CREATE TABLE feed_events(
  offset INTEGER PRIMARY KEY AUTOINCREMENT,
  observation_id TEXT NOT NULL UNIQUE,
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  source TEXT NOT NULL,
  source_sequence TEXT,
  tx_hash TEXT NOT NULL,
  stable_action_path TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  observed_at TEXT NOT NULL,
  inclusion TEXT NOT NULL DEFAULT 'observed' CHECK(inclusion IN ('observed', 'orphaned'))
);
CREATE INDEX feed_events_source_sequence_idx ON feed_events(source, source_sequence);
CREATE INDEX feed_events_tx_hash_idx ON feed_events(tx_hash);
CREATE INDEX feed_events_sequence_inclusion_idx ON feed_events(source, source_sequence, inclusion);

CREATE TABLE feed_outbox(
  offset INTEGER PRIMARY KEY REFERENCES feed_events(offset),
  payload_json TEXT NOT NULL
);

INSERT INTO feed_events(offset, observation_id, chain_id, source, source_sequence, tx_hash, stable_action_path, payload_json, observed_at, inclusion)
SELECT offset, observation_id, chain_id, source, source_sequence, tx_hash, stable_action_path, payload_json, observed_at, inclusion
FROM _mig_feed_events;

INSERT INTO feed_outbox(offset, payload_json)
SELECT offset, payload_json FROM _mig_feed_outbox;

DROP TABLE _mig_feed_outbox;
DROP TABLE _mig_feed_events;
