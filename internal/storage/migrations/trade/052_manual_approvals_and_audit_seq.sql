-- Milestone 2 (Step 2A / 3A): Durable explicit ERC-20 approvals table and
-- append-only global trade_audit_events(event_seq) backing GET /v1/trade-updates.

CREATE TABLE manual_approval_requests(
  request_id TEXT PRIMARY KEY,
  idempotency_key TEXT NOT NULL,
  chain_id INTEGER NOT NULL CHECK(chain_id > 0),
  wallet_id TEXT NOT NULL,
  protocol TEXT NOT NULL,
  token TEXT NOT NULL,
  spender TEXT NOT NULL,
  amount_raw TEXT NOT NULL,
  native_gas_outlay_wei TEXT NOT NULL,
  native_input_wei TEXT NOT NULL CHECK(native_input_wei = '0'),
  fingerprint TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(wallet_id, idempotency_key)
);

CREATE TABLE trade_audit_events(
  event_seq INTEGER PRIMARY KEY AUTOINCREMENT,
  operation_kind TEXT NOT NULL CHECK(operation_kind IN ('trade', 'approval')),
  request_id TEXT NOT NULL,
  operation_id TEXT NOT NULL,
  state TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX trade_audit_events_request_idx ON trade_audit_events(request_id, event_seq);

CREATE TABLE trade_audit_retention_floor(
  id INTEGER PRIMARY KEY CHECK(id = 1),
  min_retained_seq INTEGER NOT NULL DEFAULT 1 CHECK(min_retained_seq >= 1),
  updated_at TEXT NOT NULL
);
INSERT INTO trade_audit_retention_floor(id, min_retained_seq, updated_at)
VALUES(1, 1, '1970-01-01T00:00:00Z');

-- Atomically project /v1/trades intake, first broadcast, and submission state transitions
-- into trade_audit_events(event_seq) so GET /v1/trade-updates observes both trades and approvals.
CREATE TRIGGER trade_audit_on_user_http_request AFTER INSERT ON user_http_trade_requests
BEGIN
  INSERT INTO trade_audit_events(operation_kind, request_id, operation_id, state, payload_json, created_at)
  VALUES(
    'trade',
    NEW.id,
    NEW.id,
    NEW.outcome,
    json_object('request_id', NEW.id, 'wallet_id', NEW.wallet_id, 'outcome', NEW.outcome, 'reason_code', NEW.reason_code),
    NEW.created_at
  );
END;

CREATE TRIGGER trade_audit_on_first_submission AFTER INSERT ON manual_first_submission_bindings
BEGIN
  INSERT INTO trade_audit_events(operation_kind, request_id, operation_id, state, payload_json, created_at)
  VALUES(
    'trade',
    NEW.operation_id,
    NEW.operation_id,
    'SUBMITTED',
    json_object('operation_id', NEW.operation_id, 'submission_id', NEW.submission_id, 'tx_hash', NEW.tx_hash),
    NEW.created_at
  );
END;

CREATE TRIGGER trade_audit_on_submission_state_change AFTER UPDATE OF state ON transaction_submissions
WHEN NEW.state != OLD.state
BEGIN
  INSERT INTO trade_audit_events(operation_kind, request_id, operation_id, state, payload_json, created_at)
  VALUES(
    'trade',
    NEW.id,
    NEW.attempt_id,
    NEW.state,
    json_object('submission_id', NEW.id, 'attempt_id', NEW.attempt_id, 'tx_hash', NEW.tx_hash, 'state', NEW.state),
    NEW.updated_at
  );
END;
