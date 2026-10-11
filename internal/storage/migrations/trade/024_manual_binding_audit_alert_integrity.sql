-- M2 only. Protect all unique conflict victims before SQLite can implicitly
-- remove them, independently of recursive_triggers. Keep landed 001..023.
CREATE TRIGGER manual_execution_alert_insert_victim BEFORE INSERT ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_execution_bindings b
 ON a.id='manual-execution-alert:'||b.operation_id OR a.audit_id='manual-execution:'||b.operation_id
 WHERE a.id=NEW.id OR a.audit_id=NEW.audit_id OR a.rowid=NEW.rowid)
BEGIN SELECT RAISE(ABORT,'manual execution alert insert conflict victim protected'); END;

CREATE TRIGGER manual_execution_alert_update_victim BEFORE UPDATE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_execution_bindings b
 ON a.id='manual-execution-alert:'||b.operation_id OR a.audit_id='manual-execution:'||b.operation_id
 WHERE a.rowid!=OLD.rowid AND (a.id=NEW.id OR a.audit_id=NEW.audit_id OR a.rowid=NEW.rowid))
BEGIN SELECT RAISE(ABORT,'manual execution alert update conflict victim protected'); END;

-- Delivery state/timestamp remain mutable; source identity and physical row
-- identity do not. Retained alternate-ID alerts are protected, not repaired.
CREATE TRIGGER manual_execution_alert_identity_full BEFORE UPDATE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE OLD.id='manual-execution-alert:'||b.operation_id OR OLD.audit_id='manual-execution:'||b.operation_id)
 AND (NEW.rowid IS NOT OLD.rowid OR NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id
 OR NEW.severity IS NOT OLD.severity OR NEW.created_at IS NOT OLD.created_at)
BEGIN SELECT RAISE(ABORT,'manual execution alert full identity immutable'); END;

CREATE TRIGGER manual_execution_alert_retained_no_delete BEFORE DELETE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE OLD.id='manual-execution-alert:'||b.operation_id OR OLD.audit_id='manual-execution:'||b.operation_id)
BEGIN SELECT RAISE(ABORT,'manual execution alert retained identity immutable'); END;

CREATE TRIGGER manual_execution_audit_insert_victim BEFORE INSERT ON canary_runtime_audit
WHEN EXISTS(SELECT 1 FROM canary_runtime_audit a JOIN manual_execution_bindings b
 ON a.id='manual-execution:'||b.operation_id
 OR (a.event_type='MANUAL_EXECUTION_BOUND' AND a.operation_id=b.operation_id)
 WHERE a.id=NEW.id OR a.rowid=NEW.rowid)
BEGIN SELECT RAISE(ABORT,'manual execution audit insert conflict victim protected'); END;

CREATE TRIGGER manual_execution_audit_identity_insert BEFORE INSERT ON canary_runtime_audit
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE NEW.id='manual-execution:'||b.operation_id
 OR (NEW.event_type='MANUAL_EXECUTION_BOUND' AND NEW.operation_id=b.operation_id))
 AND NOT EXISTS(SELECT 1 FROM manual_execution_bindings b
 JOIN manual_admission_commands c ON c.operation_id=b.operation_id
 WHERE NEW.id='manual-execution:'||b.operation_id AND NEW.event_type='MANUAL_EXECUTION_BOUND'
 AND NEW.operation_id IS b.operation_id AND NEW.attempt_id IS NULL
 AND NEW.authorization_id IS b.authorization_id AND NEW.authorization_epoch IS b.authorization_epoch
 AND NEW.reason_code IS 'BOUND_NOT_SIGNED_NOT_PERMISSION' AND NEW.created_at IS b.checked_at
 AND NEW.details_json IS json_object('request_id',b.request_id,'execution_step_id',b.execution_step_id,
 'contract_hash',b.contract_hash,'reservation_id',c.reservation_id))
BEGIN SELECT RAISE(ABORT,'manual execution audit exact identity required'); END;

-- Exact original M2 audit payload, including NULL attempt and canonical JSON.
-- Do not adopt reconstructed/extended history that the old positive view accepts.
CREATE VIEW manual_execution_binding_audit_integrity AS
SELECT b.* FROM manual_execution_binding_complete b
JOIN manual_admission_commands c ON c.operation_id=b.operation_id
JOIN canary_runtime_audit a ON a.id='manual-execution:'||b.operation_id
 AND a.event_type='MANUAL_EXECUTION_BOUND' AND a.operation_id=b.operation_id
 AND a.attempt_id IS NULL AND a.authorization_id=b.authorization_id
 AND a.authorization_epoch=b.authorization_epoch
 AND a.reason_code='BOUND_NOT_SIGNED_NOT_PERMISSION' AND a.created_at=b.checked_at
 AND a.details_json=json_object('request_id',b.request_id,'execution_step_id',b.execution_step_id,
 'contract_hash',b.contract_hash,'reservation_id',c.reservation_id)
WHERE NOT EXISTS(SELECT 1 FROM canary_runtime_audit extra
 WHERE extra.event_type='MANUAL_EXECUTION_BOUND' AND extra.operation_id=b.operation_id
 AND extra.id IS NOT a.id);

-- Preserve 022's negative financial/lane checks and the existing writer API.
DROP VIEW manual_execution_binding_integrity;
CREATE VIEW manual_execution_binding_integrity AS
SELECT b.* FROM manual_execution_binding_audit_integrity b
WHERE NOT EXISTS(SELECT 1 FROM execution_reservations r
 WHERE r.operation_id=b.operation_id OR r.step_id IN (b.execution_step_id,b.validation_step_id)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=r.step_id AND s.operation_id=b.operation_id))
AND NOT EXISTS(SELECT 1 FROM execution_wallet_lanes l
 WHERE (l.operation_id=b.operation_id OR l.step_id IN (b.execution_step_id,b.validation_step_id)
 OR l.wallet_id=b.wallet_id OR lower(l.address)=lower(b.wallet_address)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=l.step_id AND s.operation_id=b.operation_id))
 AND NOT (l.operation_id IS b.operation_id AND l.step_id IS b.execution_step_id
 AND l.wallet_id IS b.wallet_id AND lower(l.address) IS lower(b.wallet_address)
 AND l.state IS 'reserved' AND l.reserved_nonce IS NULL AND l.freeze_reason IS NULL AND l.updated_at IS b.checked_at));

-- 021 creates step/lane/audit before its canonical alert. Check exact effects
-- at that last insert boundary, without depending on AFTER-binding trigger order.
CREATE TRIGGER manual_execution_alert_exact_effect AFTER INSERT ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b WHERE NEW.id='manual-execution-alert:'||b.operation_id)
BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_execution_binding_integrity b
 WHERE NEW.id='manual-execution-alert:'||b.operation_id)
 THEN RAISE(ABORT,'manual execution exact audit effect incomplete') END;
END;
