CREATE TABLE IF NOT EXISTS copy_wallet_profiles (
    profile_key TEXT PRIMARY KEY,
    chain_id INTEGER NOT NULL CHECK (chain_id > 0),
    address TEXT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    exec_mode TEXT NOT NULL DEFAULT 'shadow' CHECK (exec_mode IN ('shadow', 'live')),
    copy_buy INTEGER NOT NULL DEFAULT 1 CHECK (copy_buy IN (0, 1)),
    copy_sell INTEGER NOT NULL DEFAULT 1 CHECK (copy_sell IN (0, 1)),
    pool_filter TEXT NOT NULL DEFAULT 'all' CHECK (pool_filter IN ('all', 'launchpad', 'dex', 'pons')),
    sizing_mode TEXT NOT NULL DEFAULT 'fixed' CHECK (sizing_mode IN ('fixed', 'ratio')),
    buy_amount_native TEXT NOT NULL DEFAULT '0.1',
    buy_ratio_pct INTEGER NOT NULL DEFAULT 20 CHECK (buy_ratio_pct BETWEEN 1 AND 100),
    sell_ratio_pct INTEGER NOT NULL DEFAULT 100 CHECK (sell_ratio_pct BETWEEN 1 AND 100),
    min_mcap_usd INTEGER NOT NULL DEFAULT 0,
    max_mcap_usd INTEGER NOT NULL DEFAULT 0,
    min_wallet_buy_native TEXT NOT NULL DEFAULT '0',
    max_wallet_buy_native TEXT NOT NULL DEFAULT '0',
    gas_tier TEXT NOT NULL DEFAULT 'fast' CHECK (gas_tier IN ('standard', 'fast', 'turbo')),
    anti_mev INTEGER NOT NULL DEFAULT 1 CHECK (anti_mev IN (0, 1)),
    slippage_bps INTEGER NOT NULL DEFAULT 1500 CHECK (slippage_bps BETWEEN 10 AND 5000),
    max_delay_sec INTEGER NOT NULL DEFAULT 60,
    wallet_id TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    UNIQUE(chain_id, address)
);

ALTER TABLE sniper_tasks ADD COLUMN gas_tier TEXT NOT NULL DEFAULT 'turbo';
ALTER TABLE sniper_tasks ADD COLUMN max_tax_bps INTEGER NOT NULL DEFAULT 1000;
ALTER TABLE sniper_tasks ADD COLUMN max_delay_blocks INTEGER NOT NULL DEFAULT 3;
ALTER TABLE sniper_tasks ADD COLUMN auto_tpsl INTEGER NOT NULL DEFAULT 0;
