-- Milestone 3 (Step 2B): V2 multi-step operations (Approve->Sell), multi-attempt same-nonce
-- SpeedUp/Cancel tracking, one-shot UNKNOWN_REPLAY permits with query-first evidence, and
-- multi-relay broadcast outcome records.

CREATE TABLE trade_operation_steps(
  step_id TEXT PRIMARY KEY,
  operation_id TEXT NOT NULL,
  step_index INTEGER NOT NULL CHECK(step_index >= 0),
  step_kind TEXT NOT NULL CHECK(step_kind IN ('approve_zero', 'approve_exact', 'permit2', 'buy', 'sell', 'cancel')),
  status TEXT NOT NULL CHECK(status IN ('PENDING', 'IN_FLIGHT', 'CONFIRMED_CONTINUITY', 'REORGED', 'FAILED')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(operation_id, step_index)
);
CREATE INDEX trade_operation_steps_op_idx ON trade_operation_steps(operation_id, step_index);

CREATE TABLE trade_step_attempts(
  attempt_id TEXT PRIMARY KEY,
  step_id TEXT NOT NULL REFERENCES trade_operation_steps(step_id),
  operation_id TEXT NOT NULL,
  wallet_address TEXT NOT NULL,
  nonce INTEGER NOT NULL CHECK(nonce >= 0),
  attempt_kind TEXT NOT NULL CHECK(attempt_kind IN ('initial', 'speed_up', 'cancel')),
  tx_hash TEXT NOT NULL UNIQUE,
  artifact_hash TEXT NOT NULL,
  max_fee_per_gas_wei TEXT NOT NULL,
  max_priority_fee_wei TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('SIGNED', 'SUBMITTED', 'BROADCAST_UNKNOWN', 'CONFIRMED_CONTINUITY', 'FINALIZED', 'SUPERSEDED', 'KNOWN_UNSENT')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX trade_step_attempts_wallet_nonce_idx ON trade_step_attempts(wallet_address, nonce, state);

CREATE TABLE unknown_replay_permits(
  permit_id TEXT PRIMARY KEY,
  attempt_id TEXT NOT NULL REFERENCES trade_step_attempts(attempt_id),
  tx_hash TEXT NOT NULL,
  artifact_hash TEXT NOT NULL,
  query_first_evidence_hash TEXT NOT NULL CHECK(length(query_first_evidence_hash) = 64),
  issued_at TEXT NOT NULL,
  consumed_at TEXT
);

CREATE TRIGGER unknown_replay_permit_single_use BEFORE UPDATE ON unknown_replay_permits
WHEN OLD.consumed_at IS NOT NULL
BEGIN
  SELECT RAISE(ABORT, 'UNKNOWN_REPLAY permit already consumed');
END;

CREATE TABLE relay_broadcast_results(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  attempt_id TEXT NOT NULL REFERENCES trade_step_attempts(attempt_id),
  tx_hash TEXT NOT NULL,
  relay_name TEXT NOT NULL,
  outcome TEXT NOT NULL CHECK(outcome IN ('submitted', 'already_known', 'rejected', 'timeout', 'network_error')),
  detail TEXT NOT NULL DEFAULT '',
  recorded_at TEXT NOT NULL
);
CREATE INDEX relay_broadcast_results_attempt_idx ON relay_broadcast_results(attempt_id, recorded_at);
