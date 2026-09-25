ALTER TABLE execution_reservations RENAME TO execution_reservations_pr005;
CREATE TABLE execution_reservations(
  operation_id TEXT PRIMARY KEY REFERENCES operations(id),
  step_id TEXT NOT NULL UNIQUE REFERENCES execution_steps(id),
  wallet_id TEXT NOT NULL REFERENCES dry_run_wallets(id),
  input_asset TEXT NOT NULL,
  input_amount TEXT NOT NULL,
  gas_budget TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('reserved','signed','frozen','released','settled','reverted_released')),
  nonce_consumed INTEGER NOT NULL DEFAULT 0 CHECK(nonce_consumed IN (0,1)),
  gas_consumed INTEGER NOT NULL DEFAULT 0 CHECK(gas_consumed IN (0,1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
INSERT INTO execution_reservations(operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at)
SELECT operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at FROM execution_reservations_pr005;
DROP TABLE execution_reservations_pr005;

CREATE TABLE transaction_submissions(
  id TEXT PRIMARY KEY,
  attempt_id TEXT NOT NULL REFERENCES transaction_attempts(id),
  tx_hash TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('submitting','submitted','broadcast_unknown','reconciling','manual_resolution')),
  rpc_hash TEXT,
  failure_class TEXT,
  failure_detail TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(attempt_id, sequence)
);

CREATE TABLE receipt_observations(
  id TEXT PRIMARY KEY,
  attempt_id TEXT NOT NULL REFERENCES transaction_attempts(id),
  chain_id INTEGER NOT NULL CHECK(chain_id = 4663),
  tx_hash TEXT NOT NULL,
  block_number INTEGER NOT NULL,
  block_hash TEXT NOT NULL,
  receipt_status INTEGER NOT NULL CHECK(receipt_status IN (0,1)),
  canonical_state TEXT NOT NULL CHECK(canonical_state IN ('observed','canonical_success','canonical_revert','orphaned')),
  reconciliation_version INTEGER NOT NULL,
  observed_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(attempt_id, block_hash)
);

CREATE TABLE position_effects(
  effect_id TEXT PRIMARY KEY,
  attempt_id TEXT NOT NULL REFERENCES transaction_attempts(id),
  receipt_id TEXT NOT NULL REFERENCES receipt_observations(id),
  wallet_id TEXT NOT NULL REFERENCES dry_run_wallets(id),
  asset TEXT NOT NULL,
  delta TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('active','rolled_back')),
  applied_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(attempt_id)
);

CREATE TABLE position_effect_history(
  id TEXT PRIMARY KEY,
  effect_id TEXT NOT NULL REFERENCES position_effects(effect_id),
  transition TEXT NOT NULL CHECK(transition IN ('apply','rollback','reapply')),
  receipt_id TEXT NOT NULL REFERENCES receipt_observations(id),
  created_at TEXT NOT NULL,
  UNIQUE(effect_id, receipt_id, transition)
);

DROP INDEX one_unresolved_execution_per_wallet;
CREATE UNIQUE INDEX one_unresolved_execution_per_wallet
ON execution_steps(wallet_id)
WHERE wallet_id IS NOT NULL AND status IN ('nonce_reserved','signing','signed','broadcast_unknown','reconciling','manual_resolution');
