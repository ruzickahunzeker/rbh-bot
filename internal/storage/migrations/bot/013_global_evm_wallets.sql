-- M35: Global EVM wallet registry (chain-agnostic wallet identity with per-user ownership & type).
CREATE TABLE IF NOT EXISTS evm_wallets (
    wallet_id TEXT PRIMARY KEY,
    address TEXT NOT NULL UNIQUE,
    wallet_type TEXT NOT NULL CHECK(wallet_type IN ('keystore','readonly')),
    keystore_path TEXT NOT NULL DEFAULT '',
    owner_chat_id INTEGER NOT NULL DEFAULT 0,
    legacy_chain_id INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_evm_wallets_owner ON evm_wallets(owner_chat_id);
CREATE INDEX IF NOT EXISTS idx_evm_wallets_address ON evm_wallets(address);
