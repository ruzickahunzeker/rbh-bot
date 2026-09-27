CREATE TABLE canary_gate_attestations(
  gate TEXT PRIMARY KEY CHECK(gate IN ('C04','C05','C08')),
  status TEXT NOT NULL CHECK(status = 'PASS'),
  evidence_hash TEXT NOT NULL CHECK(length(evidence_hash) = 64),
  attested_at TEXT NOT NULL
);

CREATE TABLE canary_control_state(
  singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
  chain_id INTEGER NOT NULL CHECK(chain_id = 4663),
  mode TEXT NOT NULL CHECK(mode IN ('DISABLED','CONTROLLED_CANARY')),
  policy_version INTEGER NOT NULL CHECK(policy_version > 0),
  emergency_stopped INTEGER NOT NULL CHECK(emergency_stopped IN (0,1)),
  reason TEXT NOT NULL,
  authorization_ref TEXT NOT NULL,
  effective_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE canary_policies(
  version INTEGER PRIMARY KEY CHECK(version > 0),
  protocol TEXT NOT NULL CHECK(protocol = 'PONS_V2_CURVE'),
  max_operation_input TEXT NOT NULL,
  max_sell_bps INTEGER NOT NULL CHECK(max_sell_bps BETWEEN 1 AND 10000),
  max_wallet_exposure TEXT NOT NULL,
  max_token_exposure TEXT NOT NULL,
  max_total_exposure TEXT NOT NULL,
  max_operations_per_window INTEGER NOT NULL CHECK(max_operations_per_window > 0),
  window_seconds INTEGER NOT NULL CHECK(window_seconds > 0),
  max_unresolved_steps_per_wallet INTEGER NOT NULL CHECK(max_unresolved_steps_per_wallet = 1),
  max_gas_limit TEXT NOT NULL,
  max_gas_fee_cap TEXT NOT NULL,
  max_gas_tip_cap TEXT NOT NULL,
  max_slippage_bps INTEGER NOT NULL CHECK(max_slippage_bps BETWEEN 0 AND 10000),
  min_native_balance TEXT NOT NULL,
  policy_hash TEXT NOT NULL CHECK(length(policy_hash) = 64),
  created_at TEXT NOT NULL
);

CREATE TABLE canary_wallet_allowlist(wallet_id TEXT NOT NULL, address TEXT NOT NULL, chain_id INTEGER NOT NULL CHECK(chain_id=4663), policy_version INTEGER NOT NULL REFERENCES canary_policies(version), enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), created_at TEXT NOT NULL, PRIMARY KEY(wallet_id,policy_version));
CREATE TABLE canary_contract_allowlist(address TEXT NOT NULL, role TEXT NOT NULL, protocol TEXT NOT NULL CHECK(protocol='PONS_V2_CURVE'), runtime_code_hash TEXT NOT NULL, policy_version INTEGER NOT NULL REFERENCES canary_policies(version), enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), baseline_ref TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(address,role,policy_version));
CREATE TABLE canary_token_allowlist(token_address TEXT NOT NULL, protocol TEXT NOT NULL CHECK(protocol='PONS_V2_CURVE'), policy_version INTEGER NOT NULL REFERENCES canary_policies(version), enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), created_at TEXT NOT NULL, PRIMARY KEY(token_address,policy_version));

CREATE TABLE canary_window_usage(
  policy_version INTEGER NOT NULL,
  wallet_id TEXT NOT NULL,
  token_address TEXT NOT NULL,
  window_start TEXT NOT NULL,
  operation_count INTEGER NOT NULL,
  reserved_amount TEXT NOT NULL,
  committed_amount TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(policy_version,wallet_id,token_address,window_start)
);

CREATE TABLE canary_risk_reservations(
  id TEXT PRIMARY KEY,
  operation_id TEXT NOT NULL UNIQUE,
  wallet_id TEXT NOT NULL,
  token_address TEXT NOT NULL,
  policy_version INTEGER NOT NULL,
  input_amount TEXT NOT NULL,
  gas_budget TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('reserved','committed','frozen','released')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE canary_admission_decisions(
  id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL UNIQUE,
  operation_id TEXT NOT NULL,
  request_fingerprint TEXT NOT NULL,
  policy_version INTEGER NOT NULL,
  decision TEXT NOT NULL CHECK(decision IN ('ADMITTED','DEDUPED','QUEUED','REJECTED')),
  reason_code TEXT NOT NULL,
  wallet_id TEXT NOT NULL,
  token_address TEXT NOT NULL,
  amount TEXT NOT NULL,
  reservation_id TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE canary_audit_events(
  id TEXT PRIMARY KEY,
  decision_id TEXT NOT NULL REFERENCES canary_admission_decisions(id),
  event_type TEXT NOT NULL CHECK(event_type='CANARY_ADMISSION_DECISION'),
  policy_version INTEGER NOT NULL,
  reason_code TEXT NOT NULL,
  created_at TEXT NOT NULL
);
