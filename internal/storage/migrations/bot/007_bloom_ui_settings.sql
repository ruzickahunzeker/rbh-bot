CREATE TABLE IF NOT EXISTS bloom_user_settings (
    chat_id INTEGER PRIMARY KEY,
    buy_presets_csv TEXT NOT NULL DEFAULT '0.05,0.1,0.5,1',
    buy_slippage_bps INTEGER NOT NULL DEFAULT 500 CHECK (buy_slippage_bps BETWEEN 10 AND 5000),
    sell_slippage_bps INTEGER NOT NULL DEFAULT 1000 CHECK (sell_slippage_bps BETWEEN 10 AND 5000),
    gas_tier TEXT NOT NULL DEFAULT 'fast' CHECK (gas_tier IN ('standard', 'fast', 'turbo')),
    anti_mev INTEGER NOT NULL DEFAULT 1 CHECK (anti_mev IN (0, 1)),
    confirm_before_live INTEGER NOT NULL DEFAULT 1 CHECK (confirm_before_live IN (0, 1)),
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS bloom_ui_tokens (
    token_key TEXT PRIMARY KEY,
    chain_id INTEGER NOT NULL,
    token_address TEXT NOT NULL,
    protocol_id TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
);
