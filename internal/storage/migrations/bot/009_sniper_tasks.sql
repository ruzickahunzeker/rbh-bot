CREATE TABLE IF NOT EXISTS sniper_tasks (
    task_id TEXT PRIMARY KEY,
    chain_id INTEGER NOT NULL CHECK (chain_id > 0),
    wallet_id TEXT NOT NULL,
    token TEXT NOT NULL,
    trigger_kind TEXT NOT NULL CHECK (trigger_kind IN ('launch', 'migration')),
    amount_in_wei TEXT NOT NULL,
    max_slippage_bps INTEGER NOT NULL DEFAULT 1500 CHECK (max_slippage_bps BETWEEN 10 AND 5000),
    anti_mev INTEGER NOT NULL DEFAULT 1 CHECK (anti_mev IN (0, 1)),
    honeypot_check INTEGER NOT NULL DEFAULT 1 CHECK (honeypot_check IN (0, 1)),
    state TEXT NOT NULL CHECK (state IN ('ARMED', 'TRIGGERED', 'EXECUTED', 'CANCELLED')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS sniper_tasks_armed_idx ON sniper_tasks(chain_id, token, state);
