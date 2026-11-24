-- Milestone 4 (Step 4): Conditional orders (Limit Buy, Take-Profit, Stop-Loss)
-- and default-off Alpha Paper/Shadow zero-broadcast execution ledger.

CREATE TABLE conditional_orders(
  order_id TEXT PRIMARY KEY,
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  wallet_id TEXT NOT NULL,
  token TEXT NOT NULL,
  order_kind TEXT NOT NULL CHECK(order_kind IN ('limit_buy', 'take_profit', 'stop_loss')),
  trigger_price_wei TEXT NOT NULL,
  amount_raw TEXT NOT NULL DEFAULT '0',
  sell_bps INTEGER NOT NULL DEFAULT 0 CHECK(sell_bps BETWEEN 0 AND 10000),
  idempotency_key TEXT NOT NULL UNIQUE,
  state TEXT NOT NULL CHECK(state IN ('ACTIVE', 'TRIGGERED', 'EXECUTED', 'CANCELLED', 'EXPIRED')),
  expires_at TEXT NOT NULL,
  triggered_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX conditional_orders_active_idx ON conditional_orders(chain_id, token, state);

CREATE TABLE paper_shadow_fills(
  fill_id TEXT PRIMARY KEY,
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  wallet_id TEXT NOT NULL,
  token TEXT NOT NULL,
  side TEXT NOT NULL CHECK(side IN ('buy', 'sell')),
  amount_in_raw TEXT NOT NULL,
  simulated_out_raw TEXT NOT NULL,
  spot_price_wei TEXT NOT NULL,
  source_ref TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX paper_shadow_fills_wallet_token_idx ON paper_shadow_fills(chain_id, wallet_id, token, created_at);
