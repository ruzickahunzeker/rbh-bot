-- Milestone 2 (Step 3A-2): Multi-chain watched_wallets, per-(chain_id, feed_instance_id)
-- independent durable cursors, multi-chain signal_inbox uniqueness, cross-source/cross-pool
-- trigger_id deduplication, and persistent serial per-(chain_id, wallet_id) submission jobs.

-- 1. Lift single-chain CHECK(chain_id = 4663) on watched_wallets to CHECK(chain_id > 0)
--    while preserving strategies and operation_intents cleanly under foreign_keys=ON.
CREATE TEMP TABLE _mig_watched_wallets AS SELECT id, chain_id, address, enabled FROM watched_wallets;
CREATE TEMP TABLE _mig_strategies AS SELECT id, watched_wallet_id, version, enabled, config_json FROM strategies;
CREATE TEMP TABLE _mig_operation_intents AS SELECT id, idempotency_key, strategy_id, source_event_id, source_observation_id, source_tx_hash, kind, token, amount_mode, amount_value, policy_version, status, payload_json, created_at, retracted_at, deadline_capability, expires_at FROM operation_intents;

DROP TABLE operation_intents;
DROP TABLE strategies;
DROP TABLE watched_wallets;

CREATE TABLE watched_wallets(
  id TEXT PRIMARY KEY,
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  address TEXT NOT NULL,
  enabled INTEGER NOT NULL
);
CREATE UNIQUE INDEX watched_wallets_chain_address_idx ON watched_wallets(chain_id, address);

CREATE TABLE strategies(
  id TEXT PRIMARY KEY,
  watched_wallet_id TEXT NOT NULL REFERENCES watched_wallets(id),
  version INTEGER NOT NULL,
  enabled INTEGER NOT NULL,
  config_json TEXT NOT NULL
);

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
  retracted_at TEXT,
  deadline_capability TEXT,
  expires_at TEXT
);
CREATE INDEX operation_intents_source_observation_idx ON operation_intents(source_observation_id);
CREATE INDEX operation_intents_strategy_idx ON operation_intents(strategy_id, created_at);

INSERT INTO watched_wallets(id, chain_id, address, enabled) SELECT id, chain_id, address, enabled FROM _mig_watched_wallets;
INSERT INTO strategies(id, watched_wallet_id, version, enabled, config_json) SELECT id, watched_wallet_id, version, enabled, config_json FROM _mig_strategies;
INSERT INTO operation_intents(id, idempotency_key, strategy_id, source_event_id, source_observation_id, source_tx_hash, kind, token, amount_mode, amount_value, policy_version, status, payload_json, created_at, retracted_at, deadline_capability, expires_at)
SELECT id, idempotency_key, strategy_id, source_event_id, source_observation_id, source_tx_hash, kind, token, amount_mode, amount_value, policy_version, status, payload_json, created_at, retracted_at, deadline_capability, expires_at FROM _mig_operation_intents;

DROP TABLE _mig_operation_intents;
DROP TABLE _mig_strategies;
DROP TABLE _mig_watched_wallets;

-- 2. Independent per-(chain_id, feed_instance_id) durable cursors.
CREATE TABLE bot_feed_cursors(
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  feed_instance_id TEXT NOT NULL CHECK(length(trim(feed_instance_id)) > 0),
  durable_event_offset INTEGER NOT NULL DEFAULT 0 CHECK(durable_event_offset >= 0),
  updated_at TEXT NOT NULL,
  PRIMARY KEY(chain_id, feed_instance_id)
);

-- 3. Replace single-chain signal_inbox(feed_offset) uniqueness with multi-chain (chain_id, feed_instance_id, feed_offset).
DROP INDEX IF EXISTS signal_inbox_feed_offset_idx;
ALTER TABLE signal_inbox ADD COLUMN chain_id INTEGER NOT NULL DEFAULT 4663;
ALTER TABLE signal_inbox ADD COLUMN feed_instance_id TEXT NOT NULL DEFAULT 'feed-rbh';
CREATE UNIQUE INDEX signal_inbox_chain_instance_offset_idx
ON signal_inbox(chain_id, feed_instance_id, feed_offset);

-- 4. Durable cross-source, cross-pool-split copy-trade decision table keyed by net trigger_id.
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

-- 5. Persistent per-(chain_id, wallet_id) submission jobs preserving immutable Idempotency-Key across retries,
--    with atomic DISPATCHING claim, exponential backoff next_retry_at, and bounded max_attempts.
CREATE TABLE submission_jobs(
  job_id TEXT PRIMARY KEY,
  decision_id TEXT NOT NULL UNIQUE REFERENCES copy_signal_decisions(decision_id),
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  wallet_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL UNIQUE,
  state TEXT NOT NULL CHECK(state IN ('PENDING', 'DISPATCHING', 'SUBMITTED', 'COMPLETED', 'EXPIRED_SKIPPED', 'FAILED_TERMINAL')),
  trade_request_id TEXT,
  attempts INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 5 CHECK(max_attempts > 0),
  next_retry_at TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX submission_jobs_wallet_lane_idx ON submission_jobs(chain_id, wallet_id, state, next_retry_at, created_at);
