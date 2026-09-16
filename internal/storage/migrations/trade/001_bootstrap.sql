CREATE TABLE operations(
  id TEXT PRIMARY KEY,
  chain_id INTEGER NOT NULL CHECK(chain_id = 4663),
  wallet_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  request_fingerprint TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind = 'swap'),
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(chain_id, wallet_id, idempotency_key)
);
CREATE TABLE execution_steps(id TEXT PRIMARY KEY, operation_id TEXT NOT NULL REFERENCES operations(id), step_index INTEGER NOT NULL, kind TEXT NOT NULL, UNIQUE(operation_id, step_index));
CREATE TABLE transaction_attempts(id TEXT PRIMARY KEY, step_id TEXT NOT NULL REFERENCES execution_steps(id), nonce TEXT NOT NULL, tx_hash TEXT, status TEXT NOT NULL, created_at TEXT NOT NULL);

