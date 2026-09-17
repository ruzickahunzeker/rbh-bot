ALTER TABLE feed_events ADD COLUMN inclusion TEXT NOT NULL DEFAULT 'observed'
  CHECK(inclusion IN ('observed', 'orphaned'));

CREATE INDEX feed_events_sequence_inclusion_idx
  ON feed_events(source, source_sequence, inclusion);
