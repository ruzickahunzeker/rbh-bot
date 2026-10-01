ALTER TABLE transaction_submissions RENAME TO transaction_submissions_w4a;
CREATE TABLE transaction_submissions(
  id TEXT PRIMARY KEY,
  attempt_id TEXT NOT NULL REFERENCES transaction_attempts(id),
  tx_hash TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('submitting','submitted','broadcast_unknown','reconciling','manual_resolution','expired_prebroadcast','known_unsent')),
  rpc_hash TEXT,
  failure_class TEXT,
  failure_detail TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(attempt_id, sequence)
);
INSERT INTO transaction_submissions SELECT * FROM transaction_submissions_w4a;
DROP TABLE transaction_submissions_w4a;

DROP INDEX canary_gate_snapshot_once;
CREATE UNIQUE INDEX canary_gate_snapshot_once
ON canary_runtime_gate_snapshots(operation_id,stage,authorization_epoch,COALESCE(attempt_id,''))
WHERE stage!='IMMEDIATE_PRE_SEND';
