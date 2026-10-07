-- API intake is an immutable rejection ledger, NOT a send queue or a second
-- execution state machine. No row in this slice can authorize economic work.
CREATE TABLE trade_api_submission_requests(
  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL UNIQUE,
  operation_id TEXT NOT NULL REFERENCES operations(id),
  chain_id INTEGER NOT NULL CHECK(chain_id=4663),
  wallet_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
  request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint)=64),
  operation_fingerprint TEXT NOT NULL,
  policy_version INTEGER NOT NULL CHECK(policy_version>0),
  expires_at TEXT NOT NULL,
  outcome TEXT NOT NULL CHECK(outcome='REJECTED'),
  reason_code TEXT NOT NULL CHECK(reason_code='SUBMISSION_DISABLED'),
  created_at TEXT NOT NULL,
  UNIQUE(wallet_id,idempotency_key)
);
CREATE INDEX trade_api_submission_requests_operation ON trade_api_submission_requests(operation_id,id);
CREATE TRIGGER trade_api_submission_request_source BEFORE INSERT ON trade_api_submission_requests
WHEN NOT EXISTS (
  SELECT 1 FROM operations o WHERE o.id=NEW.operation_id AND o.chain_id=NEW.chain_id
  AND o.wallet_id=NEW.wallet_id AND o.request_fingerprint=NEW.operation_fingerprint
  AND o.policy_version=NEW.policy_version AND o.expires_at=NEW.expires_at
  AND o.deadline_capability='APPLICATION_TTL_ONLY'
  AND EXISTS (
    SELECT 1 FROM dry_run_results d JOIN execution_steps s ON s.id=d.step_id
    WHERE d.operation_id=o.id AND d.status='success'
    AND json_extract(s.route_json,'$.protocol')='pons-v2-curve'
  )
)
BEGIN SELECT RAISE(ABORT,'API request durable source mismatch'); END;
CREATE TRIGGER trade_api_submission_request_audit AFTER INSERT ON trade_api_submission_requests
BEGIN
  INSERT INTO canary_runtime_audit(id,event_type,operation_id,reason_code,details_json,created_at)
  VALUES('trade-api-audit:'||NEW.id,'TRADE_API_SUBMISSION_REJECTED',NEW.operation_id,
         'SUBMISSION_DISABLED','{}',NEW.created_at);
  INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
  VALUES('trade-api-alert:'||NEW.id,'trade-api-audit:'||NEW.id,'INFO','PENDING',NEW.created_at);
END;
CREATE TRIGGER trade_api_submission_request_no_update BEFORE UPDATE ON trade_api_submission_requests
BEGIN SELECT RAISE(ABORT,'API rejection ledger is immutable'); END;
CREATE TRIGGER trade_api_submission_request_no_delete BEFORE DELETE ON trade_api_submission_requests
BEGIN SELECT RAISE(ABORT,'API rejection ledger is immutable'); END;
