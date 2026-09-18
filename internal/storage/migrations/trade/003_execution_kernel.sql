ALTER TABLE execution_steps ADD COLUMN wallet_id TEXT;

ALTER TABLE transaction_attempts ADD COLUMN chain_id INTEGER CHECK(chain_id = 4663);
ALTER TABLE transaction_attempts ADD COLUMN wallet_id TEXT;
ALTER TABLE transaction_attempts ADD COLUMN key_version TEXT;
ALTER TABLE transaction_attempts ADD COLUMN encryption_nonce BLOB;
ALTER TABLE transaction_attempts ADD COLUMN encrypted_raw_tx BLOB;
ALTER TABLE transaction_attempts ADD COLUMN raw_tx_hash TEXT;
ALTER TABLE transaction_attempts ADD COLUMN from_address TEXT;
ALTER TABLE transaction_attempts ADD COLUMN to_address TEXT;
ALTER TABLE transaction_attempts ADD COLUMN value TEXT;
ALTER TABLE transaction_attempts ADD COLUMN calldata TEXT;
ALTER TABLE transaction_attempts ADD COLUMN gas_limit TEXT;
ALTER TABLE transaction_attempts ADD COLUMN gas_tip_cap TEXT;
ALTER TABLE transaction_attempts ADD COLUMN gas_fee_cap TEXT;
ALTER TABLE transaction_attempts ADD COLUMN updated_at TEXT;

CREATE TABLE execution_wallet_lanes(
  wallet_id TEXT PRIMARY KEY REFERENCES dry_run_wallets(id),
  address TEXT NOT NULL UNIQUE,
  state TEXT NOT NULL CHECK(state IN ('idle', 'reserved', 'signed', 'frozen')),
  operation_id TEXT REFERENCES operations(id),
  step_id TEXT REFERENCES execution_steps(id),
  reserved_nonce TEXT,
  freeze_reason TEXT,
  updated_at TEXT NOT NULL
);

CREATE TABLE execution_reservations(
  operation_id TEXT PRIMARY KEY REFERENCES operations(id),
  step_id TEXT NOT NULL UNIQUE REFERENCES execution_steps(id),
  wallet_id TEXT NOT NULL REFERENCES dry_run_wallets(id),
  input_asset TEXT NOT NULL,
  input_amount TEXT NOT NULL,
  gas_budget TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('reserved', 'signed', 'frozen', 'released')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX one_unresolved_execution_per_wallet
ON execution_steps(wallet_id)
WHERE wallet_id IS NOT NULL AND status IN ('nonce_reserved', 'signing', 'signed', 'broadcast_unknown');

