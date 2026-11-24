-- Milestone 2 (Step 3A-2): Per-(chain_id, feed_instance_id) independent durable cursors,
-- multi-chain signal_inbox uniqueness, cross-source/cross-pool trigger_id deduplication,
-- and persistent per-(chain_id, wallet_id) copy-trade submission jobs.

CREATE TABLE bot_feed_cursors(
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  feed_instance_id TEXT NOT NULL CHECK(length(trim(feed_instance_id)) > 0),
  durable_event_offset INTEGER NOT NULL DEFAULT 0 CHECK(durable_event_offset >= 0),
  updated_at TEXT NOT NULL,
  PRIMARY KEY(chain_id, feed_instance_id)
);

-- Replace single-chain signal_inbox(feed_offset) uniqueness with multi-chain (chain_id, feed_instance_id, feed_offset).
DROP INDEX IF EXISTS signal_inbox_feed_offset_idx;
ALTER TABLE signal_inbox ADD COLUMN chain_id INTEGER NOT NULL DEFAULT 4663;
ALTER TABLE signal_inbox ADD COLUMN feed_instance_id TEXT NOT NULL DEFAULT 'feed-rbh';
CREATE UNIQUE INDEX signal_inbox_chain_instance_offset_idx
ON signal_inbox(chain_id, feed_instance_id, feed_offset);

-- Durable cross-source, cross-pool-split copy-trade decision table keyed by net trigger_id.
CREATE TABLE copy_signal_decisions(
  decision_id TEXT PRIMARY KEY,
  principal_id TEXT NOT NULL,
  strategy_id TEXT NOT NULL,
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  trigger_id TEXT NOT NULL,
  action_kind TEXT NOT NULL CHECK(action_kind IN ('copy_buy', 'copy_sell')),
  wallet_id TEXT NOT NULL,
  token TEXT NOT NULL,
  trade_idempotency_key TEXT NOT NULL UNIQUE,
  disposition TEXT NOT NULL CHECK(disposition IN ('QUEUED', 'EXPIRED_SKIPPED')),
  created_at TEXT NOT NULL,
  UNIQUE(principal_id, strategy_id, chain_id, trigger_id, action_kind)
);

-- Persistent per-(chain_id, wallet_id) submission jobs preserving immutable Idempotency-Key across retries.
CREATE TABLE submission_jobs(
  job_id TEXT PRIMARY KEY,
  decision_id TEXT NOT NULL UNIQUE REFERENCES copy_signal_decisions(decision_id),
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  wallet_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL UNIQUE,
  state TEXT NOT NULL CHECK(state IN ('PENDING', 'SUBMITTED', 'COMPLETED', 'EXPIRED_SKIPPED', 'FAILED_TERMINAL')),
  trade_request_id TEXT,
  attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX submission_jobs_wallet_lane_idx ON submission_jobs(chain_id, wallet_id, state, created_at);
