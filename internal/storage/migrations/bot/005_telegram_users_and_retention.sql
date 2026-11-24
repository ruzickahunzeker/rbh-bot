-- Milestone 3 (Step 3B): Telegram allowlist with explicit active chain/wallet selection
-- and durable per-(chain_id, consumer_id) trade-updates cursors for 410 Gone reconciliation.

CREATE TABLE telegram_allowlist(
  chat_id INTEGER PRIMARY KEY,
  username TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL CHECK(role IN ('operator', 'viewer')),
  active_chain_id INTEGER NOT NULL DEFAULT 4663 CHECK(active_chain_id > 0),
  active_wallet_id TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
  updated_at TEXT NOT NULL
);

CREATE TABLE trade_update_consumer_cursors(
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  consumer_id TEXT NOT NULL CHECK(length(trim(consumer_id)) > 0),
  last_event_seq INTEGER NOT NULL DEFAULT 0 CHECK(last_event_seq >= 0),
  updated_at TEXT NOT NULL,
  PRIMARY KEY(chain_id, consumer_id)
);
