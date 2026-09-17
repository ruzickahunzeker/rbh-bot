CREATE TABLE feed_registry_snapshot(
  id INTEGER PRIMARY KEY CHECK(id = 1),
  payload_json TEXT NOT NULL,
  durable_event_offset INTEGER NOT NULL CHECK(durable_event_offset >= 0),
  updated_at TEXT NOT NULL
);
