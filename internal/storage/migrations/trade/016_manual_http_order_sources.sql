-- Immutable MANUAL source overlay. No queue, authorization or alternate
-- execution state machine: lifecycle remains operations/execution_steps.
CREATE TABLE manual_http_order_sources(
  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL UNIQUE,
  principal_id TEXT NOT NULL CHECK(length(principal_id) BETWEEN 1 AND 128),
  idempotency_key TEXT NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
  operation_id TEXT NOT NULL UNIQUE REFERENCES operations(id),
  step_id TEXT NOT NULL UNIQUE REFERENCES execution_steps(id),
  chain TEXT NOT NULL CHECK(chain='robinhood'),
  wallet_id TEXT NOT NULL REFERENCES dry_run_wallets(id),
  wallet_address TEXT NOT NULL CHECK(length(wallet_address)=42),
  source TEXT NOT NULL CHECK(source='MANUAL'),
  request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint)=64),
  request_json TEXT NOT NULL CHECK(json_valid(request_json)),
  api_policy_version INTEGER NOT NULL CHECK(api_policy_version>0),
  api_policy_hash TEXT NOT NULL CHECK(length(api_policy_hash)=64),
  api_policy_json TEXT NOT NULL CHECK(json_valid(api_policy_json)),
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  deadline_capability TEXT NOT NULL CHECK(deadline_capability='APPLICATION_TTL_ONLY'),
  contract_deadline INTEGER NOT NULL CHECK(contract_deadline=0),
  send_authorized INTEGER NOT NULL CHECK(send_authorized=0),
  submission_queued INTEGER NOT NULL CHECK(submission_queued=0),
  UNIQUE(principal_id,idempotency_key)
);
CREATE TRIGGER manual_http_no_rejected_key BEFORE INSERT ON manual_http_order_sources
WHEN EXISTS(SELECT 1 FROM user_http_trade_requests WHERE principal_id=NEW.principal_id AND idempotency_key=NEW.idempotency_key)
BEGIN SELECT RAISE(ABORT,'rejected request cannot be activated'); END;
CREATE TRIGGER user_http_no_manual_key BEFORE INSERT ON user_http_trade_requests
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources WHERE principal_id=NEW.principal_id AND idempotency_key=NEW.idempotency_key)
BEGIN SELECT RAISE(ABORT,'manual request already exists'); END;
CREATE TRIGGER manual_http_source_binding BEFORE INSERT ON manual_http_order_sources
WHEN NOT EXISTS(
 SELECT 1 FROM operations o JOIN execution_steps s ON s.operation_id=o.id
 JOIN dry_run_wallets w ON w.id=o.wallet_id
 WHERE o.id=NEW.operation_id AND s.id=NEW.step_id AND o.chain_id=4663
 AND o.wallet_id=NEW.wallet_id AND w.chain_id=4663 AND w.enabled=1
 AND json_extract(NEW.request_json,'$.chain')='robinhood'
 AND json_extract(NEW.request_json,'$.wallet_id')=NEW.wallet_id
 AND w.address=NEW.wallet_address AND o.kind='swap'
 AND o.status='manual_validation_pending' AND s.status='validation_pending'
 AND s.kind='manual_trade_validation' AND s.step_index=0 AND s.wallet_id=NEW.wallet_id
 AND o.request_fingerprint=NEW.request_fingerprint AND o.request_json=NEW.request_json
 AND o.idempotency_key='manual:'||NEW.id
 AND o.policy_version IS NULL AND s.policy_version IS NULL
 AND o.deadline_capability=NEW.deadline_capability
 AND o.expires_at=NEW.expires_at AND s.expires_at=NEW.expires_at
 AND o.created_at=NEW.created_at AND s.created_at=NEW.created_at
)
BEGIN SELECT RAISE(ABORT,'manual operation source mismatch'); END;
CREATE TRIGGER manual_http_source_audit AFTER INSERT ON manual_http_order_sources
BEGIN
 INSERT INTO canary_runtime_audit(id,event_type,reason_code,details_json,created_at)
 VALUES('manual-http-audit:'||NEW.id,'MANUAL_HTTP_ORDER_RECORDED','VALIDATION_NOT_CONNECTED',
 json_object('request_id',NEW.id,'operation_id',NEW.operation_id,'source','MANUAL'),NEW.created_at);
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-http-alert:'||NEW.id,'manual-http-audit:'||NEW.id,'INFO','PENDING',NEW.created_at);
END;
CREATE TRIGGER manual_http_source_no_update BEFORE UPDATE ON manual_http_order_sources
BEGIN SELECT RAISE(ABORT,'manual order source is immutable'); END;
CREATE TRIGGER manual_http_source_no_delete BEFORE DELETE ON manual_http_order_sources
BEGIN SELECT RAISE(ABORT,'manual order source is immutable'); END;
-- SQLite REPLACE must not depend on recursive delete-trigger settings.
CREATE TRIGGER manual_http_source_no_replace BEFORE INSERT ON manual_http_order_sources
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources WHERE sequence=NEW.sequence OR id=NEW.id OR operation_id=NEW.operation_id
 OR step_id=NEW.step_id OR (principal_id=NEW.principal_id AND idempotency_key=NEW.idempotency_key))
BEGIN SELECT RAISE(ABORT,'manual order source already exists'); END;
CREATE TRIGGER user_http_rejection_no_replace BEFORE INSERT ON user_http_trade_requests
WHEN EXISTS(SELECT 1 FROM user_http_trade_requests WHERE sequence=NEW.sequence OR id=NEW.id
 OR (principal_id=NEW.principal_id AND idempotency_key=NEW.idempotency_key))
BEGIN SELECT RAISE(ABORT,'rejected request already exists'); END;
CREATE TRIGGER manual_http_operation_no_replace BEFORE INSERT ON operations
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.id)
BEGIN SELECT RAISE(ABORT,'manual operation already exists'); END;
CREATE TRIGGER manual_http_step_no_replace BEFORE INSERT ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources WHERE step_id=NEW.id)
BEGIN SELECT RAISE(ABORT,'manual step already exists'); END;
CREATE TRIGGER manual_http_operation_identity BEFORE UPDATE ON operations
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=OLD.id)
 AND (NEW.id IS NOT OLD.id OR NEW.chain_id IS NOT OLD.chain_id OR NEW.wallet_id IS NOT OLD.wallet_id
 OR NEW.idempotency_key IS NOT OLD.idempotency_key OR NEW.request_fingerprint IS NOT OLD.request_fingerprint
 OR NEW.request_json IS NOT OLD.request_json OR NEW.created_at IS NOT OLD.created_at
 OR NEW.kind IS NOT OLD.kind OR NEW.expires_at IS NOT OLD.expires_at
 OR NEW.deadline_capability IS NOT OLD.deadline_capability)
BEGIN SELECT RAISE(ABORT,'manual operation identity is immutable'); END;
CREATE TRIGGER manual_http_step_identity BEFORE UPDATE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources WHERE step_id=OLD.id)
 AND (NEW.id IS NOT OLD.id OR NEW.operation_id IS NOT OLD.operation_id OR NEW.step_index IS NOT OLD.step_index
 OR NEW.kind IS NOT OLD.kind OR NEW.wallet_id IS NOT OLD.wallet_id
 OR NEW.expires_at IS NOT OLD.expires_at OR NEW.created_at IS NOT OLD.created_at)
BEGIN SELECT RAISE(ABORT,'manual step identity is immutable'); END;
