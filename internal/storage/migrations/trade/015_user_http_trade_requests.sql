-- 014 is reserved by the independent, unmerged internal submission-intake
-- branch. This migration is independent and never edits 001..013/014.
-- This is a request rejection journal, NOT a second execution state machine,
-- queue, accepted operation, gate attestation or authorization.
CREATE TABLE user_http_trade_requests(
  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL UNIQUE,
  principal_id TEXT NOT NULL CHECK(length(principal_id) BETWEEN 1 AND 128),
  idempotency_key TEXT NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
  chain TEXT NOT NULL CHECK(chain='robinhood'),
  wallet_id TEXT NOT NULL REFERENCES dry_run_wallets(id),
  wallet_address TEXT NOT NULL,
  source TEXT NOT NULL CHECK(source='MANUAL'),
  request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint)=64),
  request_json TEXT NOT NULL CHECK(json_valid(request_json)),
  api_policy_version INTEGER NOT NULL CHECK(api_policy_version>0),
  api_policy_hash TEXT NOT NULL CHECK(length(api_policy_hash)=64),
  api_policy_json TEXT NOT NULL CHECK(json_valid(api_policy_json)),
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  outcome TEXT NOT NULL CHECK(outcome='REJECTED'),
  reason_code TEXT NOT NULL CHECK(reason_code='SUBMISSION_DISABLED'),
  deadline_capability TEXT NOT NULL CHECK(deadline_capability='APPLICATION_TTL_ONLY'),
  contract_deadline INTEGER NOT NULL CHECK(contract_deadline=0),
  send_authorized INTEGER NOT NULL CHECK(send_authorized=0),
  submission_queued INTEGER NOT NULL CHECK(submission_queued=0),
  UNIQUE(principal_id,idempotency_key)
);
CREATE TRIGGER user_http_trade_wallet_source BEFORE INSERT ON user_http_trade_requests
WHEN NOT EXISTS (SELECT 1 FROM dry_run_wallets w WHERE w.id=NEW.wallet_id
  AND w.chain_id=4663 AND w.enabled=1 AND w.address=NEW.wallet_address)
BEGIN SELECT RAISE(ABORT,'user HTTP wallet source mismatch'); END;
CREATE TRIGGER user_http_trade_audit AFTER INSERT ON user_http_trade_requests
BEGIN
  INSERT INTO canary_runtime_audit(id,event_type,reason_code,details_json,created_at)
  VALUES('user-http-audit:'||NEW.id,'USER_HTTP_TRADE_REJECTED','SUBMISSION_DISABLED',
    json_object('request_id',NEW.id),NEW.created_at);
  INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
  VALUES('user-http-alert:'||NEW.id,'user-http-audit:'||NEW.id,'INFO','PENDING',NEW.created_at);
END;
CREATE TRIGGER user_http_trade_no_update BEFORE UPDATE ON user_http_trade_requests
BEGIN SELECT RAISE(ABORT,'user HTTP request is immutable'); END;
CREATE TRIGGER user_http_trade_no_delete BEFORE DELETE ON user_http_trade_requests
BEGIN SELECT RAISE(ABORT,'user HTTP request is immutable'); END;
