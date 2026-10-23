-- Immutable internal validation observations. Lifecycle is ONLY in the
-- existing Operation/ExecutionStep. No authorization, reservation or send queue.
CREATE TABLE manual_trade_resolutions(
 operation_id TEXT PRIMARY KEY REFERENCES manual_http_order_sources(operation_id),
 resolution_json TEXT NOT NULL CHECK(json_valid(resolution_json)),
 resolution_hash TEXT NOT NULL CHECK(length(resolution_hash)=64),
 policy_version INTEGER NOT NULL REFERENCES canary_policies(version),
 policy_hash TEXT NOT NULL CHECK(length(policy_hash)=64),
 policy_json TEXT NOT NULL CHECK(json_valid(policy_json)),
 expires_at TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE TRIGGER manual_resolution_source BEFORE INSERT ON manual_trade_resolutions
WHEN NOT EXISTS(
 SELECT 1 FROM manual_http_order_sources m JOIN operations o ON o.id=m.operation_id
 JOIN execution_steps s ON s.id=m.step_id JOIN canary_policies p ON p.version=NEW.policy_version
 WHERE m.operation_id=NEW.operation_id AND o.status='manual_validation_pending'
 AND s.status='validation_pending' AND m.expires_at=NEW.expires_at AND p.policy_hash=NEW.policy_hash
 AND json_extract(NEW.resolution_json,'$.wallet')=lower(m.wallet_address)
 AND json_extract(NEW.resolution_json,'$.token.address')=json_extract(m.request_json,'$.token')
 AND json_extract(NEW.resolution_json,'$.side')=json_extract(m.request_json,'$.side')
 AND json_extract(NEW.resolution_json,'$.token.protocol')='pons_v2_curve'
 AND json_extract(NEW.resolution_json,'$.token.quote_asset')='native'
 AND json_extract(NEW.resolution_json,'$.token.send_authorized')=0
 AND json_extract(NEW.resolution_json,'$.token.block.number')>0
 AND length(json_extract(NEW.resolution_json,'$.token.block.hash'))=66
)
BEGIN SELECT RAISE(ABORT,'manual resolution source mismatch'); END;
CREATE TRIGGER manual_resolution_effect AFTER INSERT ON manual_trade_resolutions
BEGIN
 UPDATE operations SET status='manual_parameters_resolved',policy_version=NEW.policy_version,updated_at=NEW.created_at WHERE id=NEW.operation_id;
 UPDATE execution_steps SET status='parameters_resolved',policy_version=NEW.policy_version,
 quote_block_number=json_extract(NEW.resolution_json,'$.token.block.number'),
 quote_block_hash=json_extract(NEW.resolution_json,'$.token.block.hash'),updated_at=NEW.created_at
 WHERE id=(SELECT step_id FROM manual_http_order_sources WHERE operation_id=NEW.operation_id);
 INSERT INTO canary_runtime_audit(id,event_type,reason_code,details_json,created_at)
 VALUES('manual-resolution:'||NEW.operation_id,'MANUAL_PARAMETERS_RESOLVED','NOT_EXECUTION_ADMISSION',json_object('operation_id',NEW.operation_id,'resolution_hash',NEW.resolution_hash),NEW.created_at);
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-resolution-alert:'||NEW.operation_id,'manual-resolution:'||NEW.operation_id,'INFO','PENDING',NEW.created_at);
END;
CREATE TABLE manual_trade_validations(
 operation_id TEXT PRIMARY KEY REFERENCES manual_trade_resolutions(operation_id),
 result_json TEXT NOT NULL CHECK(json_valid(result_json)),
 result_hash TEXT NOT NULL CHECK(length(result_hash)=64),
 created_at TEXT NOT NULL,
 send_authorized INTEGER NOT NULL CHECK(send_authorized=0)
);
CREATE TRIGGER manual_validation_source BEFORE INSERT ON manual_trade_validations
WHEN NOT EXISTS(SELECT 1 FROM manual_trade_resolutions r JOIN manual_http_order_sources m ON m.operation_id=r.operation_id
 JOIN operations o ON o.id=r.operation_id JOIN execution_steps s ON s.id=m.step_id
 WHERE r.operation_id=NEW.operation_id AND o.status='manual_parameters_resolved' AND s.status='parameters_resolved'
 AND json_extract(NEW.result_json,'$.resolution')=json(r.resolution_json))
BEGIN SELECT RAISE(ABORT,'manual validation source mismatch'); END;
CREATE TRIGGER manual_validation_effect AFTER INSERT ON manual_trade_validations
BEGIN
 UPDATE operations SET status='manual_validation_succeeded',updated_at=NEW.created_at WHERE id=NEW.operation_id;
 UPDATE execution_steps SET status='validation_succeeded',parameters_json=NEW.result_json,updated_at=NEW.created_at
 WHERE id=(SELECT step_id FROM manual_http_order_sources WHERE operation_id=NEW.operation_id);
 INSERT INTO canary_runtime_audit(id,event_type,reason_code,details_json,created_at)
 VALUES('manual-validation:'||NEW.operation_id,'MANUAL_VALIDATION_SUCCEEDED','NOT_EXECUTION_ADMISSION',json_object('operation_id',NEW.operation_id,'result_hash',NEW.result_hash),NEW.created_at);
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-validation-alert:'||NEW.operation_id,'manual-validation:'||NEW.operation_id,'INFO','PENDING',NEW.created_at);
END;
CREATE TRIGGER manual_resolution_no_update BEFORE UPDATE ON manual_trade_resolutions BEGIN SELECT RAISE(ABORT,'manual resolution immutable'); END;
CREATE TRIGGER manual_resolution_no_delete BEFORE DELETE ON manual_trade_resolutions BEGIN SELECT RAISE(ABORT,'manual resolution immutable'); END;
CREATE TRIGGER manual_resolution_no_replace BEFORE INSERT ON manual_trade_resolutions WHEN EXISTS(SELECT 1 FROM manual_trade_resolutions WHERE operation_id=NEW.operation_id) BEGIN SELECT RAISE(ABORT,'manual resolution exists'); END;
CREATE TRIGGER manual_validation_no_update BEFORE UPDATE ON manual_trade_validations BEGIN SELECT RAISE(ABORT,'manual validation immutable'); END;
CREATE TRIGGER manual_validation_no_delete BEFORE DELETE ON manual_trade_validations BEGIN SELECT RAISE(ABORT,'manual validation immutable'); END;
CREATE TRIGGER manual_validation_no_replace BEFORE INSERT ON manual_trade_validations WHEN EXISTS(SELECT 1 FROM manual_trade_validations WHERE operation_id=NEW.operation_id) BEGIN SELECT RAISE(ABORT,'manual validation exists'); END;
CREATE TRIGGER manual_resolved_operation_policy BEFORE UPDATE ON operations
WHEN EXISTS(SELECT 1 FROM manual_trade_resolutions WHERE operation_id=OLD.id) AND OLD.policy_version IS NOT NULL AND NEW.policy_version IS NOT OLD.policy_version
BEGIN SELECT RAISE(ABORT,'manual resolved policy immutable'); END;
CREATE TRIGGER manual_resolved_step_snapshot BEFORE UPDATE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources m JOIN manual_trade_resolutions r ON r.operation_id=m.operation_id WHERE m.step_id=OLD.id)
AND (NEW.policy_version IS NOT OLD.policy_version OR NEW.quote_block_number IS NOT OLD.quote_block_number OR NEW.quote_block_hash IS NOT OLD.quote_block_hash)
-- Initial binding occurs in the resolution INSERT trigger.
AND OLD.policy_version IS NOT NULL
BEGIN SELECT RAISE(ABORT,'manual resolved snapshot immutable'); END;
