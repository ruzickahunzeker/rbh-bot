CREATE TABLE feed_receipt_audits(
  tx_hash TEXT PRIMARY KEY,
  block_number INTEGER NOT NULL,
  receipt_status INTEGER NOT NULL,
  reason TEXT NOT NULL,
  persisted_at TEXT NOT NULL
);

CREATE TABLE feed_progress(
  id INTEGER PRIMARY KEY CHECK(id = 1),
  observed_sequence INTEGER,
  durable_event_offset INTEGER NOT NULL DEFAULT 0 CHECK(durable_event_offset >= 0),
  degraded INTEGER NOT NULL DEFAULT 0 CHECK(degraded IN (0, 1)),
  degraded_reason TEXT,
  updated_at TEXT NOT NULL
);

INSERT INTO feed_progress(id, observed_sequence, durable_event_offset, degraded, degraded_reason, updated_at)
VALUES(1, NULL, 0, 0, NULL, '1970-01-01T00:00:00Z');

CREATE INDEX feed_events_source_sequence_idx ON feed_events(source, source_sequence);
CREATE INDEX feed_events_tx_hash_idx ON feed_events(tx_hash);
