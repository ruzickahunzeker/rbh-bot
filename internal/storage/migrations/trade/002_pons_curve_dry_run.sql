ALTER TABLE operations ADD COLUMN request_json TEXT;
ALTER TABLE operations ADD COLUMN failure_code TEXT;
ALTER TABLE operations ADD COLUMN updated_at TEXT;

ALTER TABLE execution_steps ADD COLUMN status TEXT NOT NULL DEFAULT 'created';
ALTER TABLE execution_steps ADD COLUMN route_json TEXT;
ALTER TABLE execution_steps ADD COLUMN parameters_json TEXT;
ALTER TABLE execution_steps ADD COLUMN unsigned_call_json TEXT;
ALTER TABLE execution_steps ADD COLUMN created_at TEXT;
ALTER TABLE execution_steps ADD COLUMN updated_at TEXT;

CREATE TABLE dry_run_wallets(
  id TEXT PRIMARY KEY,
  chain_id INTEGER NOT NULL CHECK(chain_id = 4663),
  address TEXT NOT NULL UNIQUE,
  enabled INTEGER NOT NULL CHECK(enabled IN (0, 1))
);

CREATE TABLE dry_run_results(
  operation_id TEXT PRIMARY KEY REFERENCES operations(id),
  step_id TEXT NOT NULL UNIQUE REFERENCES execution_steps(id),
  status TEXT NOT NULL CHECK(status IN ('success', 'fail_closed')),
  block_number INTEGER,
  block_hash TEXT,
  return_data TEXT,
  failure_code TEXT,
  failure_detail TEXT,
  created_at TEXT NOT NULL
);
