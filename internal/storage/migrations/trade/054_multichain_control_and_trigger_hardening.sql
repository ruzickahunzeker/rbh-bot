-- Milestone 3 Closeout (054): Lift single-chain CHECK(chain_id=4663) on canary_control_state,
-- add multi-chain wallet & HTTP intake journal for non-Robinhood chains (e.g. BSC chain_id=56),
-- harden manual_wallet_reuse_serial_guard against NULL/empty COALESCE collisions, and prevent
-- duplicate UNKNOWN_REPLAY permits on identical query-first evidence.

-- 1. Add multichain_control_state for non-4663 chains (e.g. BSC chain_id=56, Base chain_id=8453)
--    without renaming canary_control_state (preserving all SQLite view/trigger bindings in 006..050).
CREATE TABLE multichain_control_state(
  singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  mode TEXT NOT NULL CHECK(mode IN ('DISABLED','CONTROLLED_CANARY')),
  policy_version INTEGER NOT NULL CHECK(policy_version > 0),
  emergency_stopped INTEGER NOT NULL CHECK(emergency_stopped IN (0,1)),
  reason TEXT NOT NULL,
  authorization_ref TEXT NOT NULL,
  effective_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TRIGGER multichain_control_state_chain_immutable
BEFORE UPDATE ON multichain_control_state
WHEN NEW.chain_id != OLD.chain_id
BEGIN
  SELECT RAISE(ABORT, 'multichain_control_state chain_id is immutable');
END;

-- 2. Multi-chain wallets & HTTP trade intake journal for non-4663 chains (e.g. BSC chain_id=56),
--    leaving sealed Robinhood V1 tables/views (001..050) 100% untouched.
CREATE TABLE multichain_wallets(
  id TEXT PRIMARY KEY,
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  address TEXT NOT NULL CHECK(length(address) = 42),
  enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
  created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX one_multichain_wallet_per_chain_address
ON multichain_wallets(chain_id, lower(address));

CREATE TABLE multichain_http_trade_requests(
  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL UNIQUE,
  principal_id TEXT NOT NULL CHECK(length(principal_id) BETWEEN 1 AND 128),
  idempotency_key TEXT NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  chain TEXT NOT NULL CHECK(length(trim(chain)) > 0),
  wallet_id TEXT NOT NULL REFERENCES multichain_wallets(id),
  wallet_address TEXT NOT NULL,
  source TEXT NOT NULL CHECK(source IN ('MANUAL', 'COPY')),
  request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint) = 64),
  request_json TEXT NOT NULL CHECK(json_valid(request_json)),
  api_policy_version INTEGER NOT NULL CHECK(api_policy_version > 0),
  api_policy_hash TEXT NOT NULL CHECK(length(api_policy_hash) = 64),
  api_policy_json TEXT NOT NULL CHECK(json_valid(api_policy_json)),
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  outcome TEXT NOT NULL,
  reason_code TEXT NOT NULL,
  deadline_capability TEXT NOT NULL CHECK(deadline_capability IN ('APPLICATION_TTL_ONLY', 'CONTRACT_DEADLINE_UNIX')),
  contract_deadline INTEGER NOT NULL CHECK(contract_deadline >= 0),
  send_authorized INTEGER NOT NULL CHECK(send_authorized = 0),
  submission_queued INTEGER NOT NULL CHECK(submission_queued = 0),
  UNIQUE(principal_id, idempotency_key)
);

CREATE TRIGGER multichain_http_trade_wallet_source
BEFORE INSERT ON multichain_http_trade_requests
WHEN NOT EXISTS (
  SELECT 1 FROM multichain_wallets w
  WHERE w.id = NEW.wallet_id
    AND w.chain_id = NEW.chain_id
    AND w.enabled = 1
    AND lower(w.address) = lower(NEW.wallet_address)
)
BEGIN
  SELECT RAISE(ABORT, 'multichain HTTP wallet source mismatch');
END;

CREATE TRIGGER trade_audit_on_multichain_http_request
AFTER INSERT ON multichain_http_trade_requests
BEGIN
  INSERT INTO trade_audit_events(operation_kind, request_id, operation_id, state, payload_json, created_at)
  VALUES(
    'trade',
    NEW.id,
    NEW.id,
    NEW.outcome,
    json_object('request_id', NEW.id, 'chain_id', NEW.chain_id, 'wallet_id', NEW.wallet_id, 'outcome', NEW.outcome, 'reason_code', NEW.reason_code),
    NEW.created_at
  );
END;

-- 3. Harden manual_wallet_reuse_serial_guard (U-P1-4): eliminate COALESCE('','') == COALESCE('','') false match.
DROP TRIGGER IF EXISTS manual_wallet_reuse_serial_guard;
CREATE TRIGGER manual_wallet_reuse_serial_guard
BEFORE INSERT ON manual_admission_commands
WHEN EXISTS(
  SELECT 1 FROM canary_risk_reservations r
  LEFT JOIN dry_run_wallets w ON w.id = r.wallet_id
  WHERE r.operation_id != NEW.operation_id
    AND r.state IN ('reserved', 'frozen')
    AND (
      r.wallet_id = json_extract(NEW.contract_json, '$.wallet_id')
      OR (
        w.address IS NOT NULL AND length(trim(w.address)) > 0
        AND json_extract(NEW.contract_json, '$.wallet_address') IS NOT NULL
        AND lower(w.address) = lower(json_extract(NEW.contract_json, '$.wallet_address'))
      )
    )
)
OR EXISTS(
  SELECT 1 FROM manual_retained_wallet_claims retained
  LEFT JOIN canary_risk_reservations r ON r.operation_id = retained.operation_id
  WHERE retained.operation_id != NEW.operation_id
    AND (
      retained.wallet_id = json_extract(NEW.contract_json, '$.wallet_id')
      OR (
        retained.wallet_address IS NOT NULL AND length(trim(retained.wallet_address)) > 0
        AND json_extract(NEW.contract_json, '$.wallet_address') IS NOT NULL
        AND lower(retained.wallet_address) = lower(json_extract(NEW.contract_json, '$.wallet_address'))
      )
    )
    AND COALESCE(r.state, 'reserved') NOT IN ('committed', 'released')
)
BEGIN
  SELECT RAISE(ABORT, 'WALLET_IN_FLIGHT_OR_FROZEN');
END;

-- 4. Prevent duplicate UNKNOWN_REPLAY permits from reusing identical query-first evidence (U-P1-2).
CREATE UNIQUE INDEX unknown_replay_permits_attempt_evidence_idx
ON unknown_replay_permits(attempt_id, query_first_evidence_hash);
