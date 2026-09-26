ALTER TABLE operations ADD COLUMN policy_version INTEGER;
ALTER TABLE operations ADD COLUMN deadline_capability TEXT;
ALTER TABLE operations ADD COLUMN expires_at TEXT;

ALTER TABLE execution_steps ADD COLUMN policy_version INTEGER;
ALTER TABLE execution_steps ADD COLUMN quote_block_number INTEGER;
ALTER TABLE execution_steps ADD COLUMN quote_block_hash TEXT;
ALTER TABLE execution_steps ADD COLUMN expires_at TEXT;

ALTER TABLE transaction_attempts ADD COLUMN policy_version INTEGER;
ALTER TABLE transaction_attempts ADD COLUMN quote_block_number INTEGER;
ALTER TABLE transaction_attempts ADD COLUMN quote_block_hash TEXT;
ALTER TABLE transaction_attempts ADD COLUMN expires_at TEXT;

ALTER TABLE transaction_submissions RENAME TO transaction_submissions_pr006;
CREATE TABLE transaction_submissions(
  id TEXT PRIMARY KEY,
  attempt_id TEXT NOT NULL REFERENCES transaction_attempts(id),
  tx_hash TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('submitting','submitted','broadcast_unknown','reconciling','manual_resolution','expired_prebroadcast')),
  rpc_hash TEXT,
  failure_class TEXT,
  failure_detail TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(attempt_id, sequence)
);
INSERT INTO transaction_submissions SELECT * FROM transaction_submissions_pr006;
DROP TABLE transaction_submissions_pr006;
