-- M4-B2.4 hardening. Migration 042 remains immutable.
DROP TRIGGER manual_b23_no_permit_consume;
DROP TRIGGER manual_b24_first_permit_consume_exact;
DROP TRIGGER manual_b24_first_permit_consume_audit;
CREATE TABLE manual_first_broadcast_consume_empty(id INTEGER PRIMARY KEY);
CREATE TRIGGER manual_b24_consume_empty_no_insert BEFORE INSERT ON manual_first_broadcast_consume_empty
BEGIN SELECT RAISE(ABORT,'manual consume empty target'); END;
CREATE TRIGGER manual_b24_consume_empty_no_update BEFORE UPDATE ON manual_first_broadcast_consume_empty
BEGIN SELECT RAISE(ABORT,'manual consume empty target'); END;
CREATE TRIGGER manual_b24_consume_empty_no_delete BEFORE DELETE ON manual_first_broadcast_consume_empty
BEGIN SELECT RAISE(ABORT,'manual consume empty target'); END;
-- Current MANUAL signed-phase source, shared by the private API and SQL fence.
-- An old issuance is never a substitute for current controls/accounting/lease.
CREATE VIEW manual_first_broadcast_consume_source AS
SELECT p.id AS permit_id,p.operation_id,p.attempt_id,p.authorization_id,p.authorization_epoch,
 p.artifact_hash,p.tx_hash,p.issued_at,p.expires_at,
 json_array(p.expires_at,e.operation_expires_at,e.auth_expires_at,e.lease_expires_at) AS expiries,
 json_array(e.foundation_created_at,e.artifact_completed_at,e.control_effective_at,e.control_updated_at,
 e.authorized_at,e.auth_created_at,e.auth_updated_at,e.lease_updated_at,e.policy_created_at,
 json_extract(e.source_json,'$.allowlists.wallet_created_at'),json_extract(e.source_json,'$.allowlists.contract_created_at'),
 json_extract(e.source_json,'$.allowlists.token_created_at'),clock.high_water_at) AS not_before
FROM canary_send_permits p
JOIN manual_first_broadcast_issuances i ON i.id=p.id AND i.operation_id=p.operation_id
 AND i.attempt_id=p.attempt_id AND i.authorization_id=p.authorization_id AND i.authorization_epoch=p.authorization_epoch
 AND i.artifact_hash=p.artifact_hash AND i.tx_hash=p.tx_hash AND i.issued_at=p.issued_at AND i.expires_at=p.expires_at
 AND i.source_hash=manual_artifact_bytes_sha256(hex(i.source_json))
JOIN manual_gate_evidence_valid_source e ON e.operation_id=i.operation_id
 AND json_extract(e.source_json,'$.foundation')=json_extract(i.source_json,'$.foundation')
 AND json_extract(e.source_json,'$.submission_lease.holder_id')=json_extract(i.source_json,'$.submission_lease.holder_id')
 AND json_extract(e.source_json,'$.submission_lease.epoch')=json_extract(i.source_json,'$.submission_lease.epoch')
JOIN manual_gate_clock_state clock ON clock.environment=json_extract(e.source_json,'$.authorization.environment')
 AND clock.chain_id=4663 AND clock.fault_latched=0 AND clock.generation>=i.clock_generation
WHERE p.state='ISSUED' AND p.purpose='FIRST_BROADCAST';
CREATE TABLE manual_first_broadcast_consume_scopes(
 permit_id TEXT NOT NULL PRIMARY KEY REFERENCES canary_send_permits(id) DEFERRABLE INITIALLY DEFERRED,
 operation_id TEXT NOT NULL, attempt_id TEXT NOT NULL, authorization_id TEXT NOT NULL,
 authorization_epoch INTEGER NOT NULL, artifact_hash TEXT NOT NULL, opened_at TEXT NOT NULL,
 empty_ref INTEGER NOT NULL DEFAULT 1 REFERENCES manual_first_broadcast_consume_empty(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER manual_b24_consume_scope_no_update BEFORE UPDATE ON manual_first_broadcast_consume_scopes
BEGIN SELECT RAISE(ABORT,'manual consume scope immutable'); END;
CREATE TRIGGER manual_b24_consume_scope_exact BEFORE INSERT ON manual_first_broadcast_consume_scopes
WHEN NOT EXISTS(SELECT 1 FROM manual_first_broadcast_consume_source p
 WHERE p.permit_id=NEW.permit_id AND p.operation_id=NEW.operation_id
 AND p.attempt_id=NEW.attempt_id AND p.authorization_id=NEW.authorization_id
 AND p.authorization_epoch=NEW.authorization_epoch AND p.artifact_hash=NEW.artifact_hash
 AND manual_artifact_time_valid(hex(NEW.opened_at),typeof(NEW.opened_at))=1
 AND manual_time_cmp(NEW.opened_at,p.issued_at)>0
 AND NOT EXISTS(SELECT 1 FROM json_each(p.expiries) WHERE manual_time_cmp(NEW.opened_at,value)>=0)
 AND NOT EXISTS(SELECT 1 FROM json_each(p.not_before) WHERE manual_time_cmp(NEW.opened_at,value)<0))
BEGIN SELECT RAISE(ABORT,'manual first broadcast consume scope identity required'); END;
-- Evaluate the legacy projection in INSERT context: its internal audit alias
-- "old" must not resolve to an enclosing UPDATE trigger's OLD pseudo-row.
-- This probe is transient, uncommittable if retained, and grants no capability.
CREATE TABLE manual_first_broadcast_consume_checks(
 permit_id TEXT NOT NULL PRIMARY KEY,checked_at TEXT NOT NULL,
 empty_ref INTEGER NOT NULL DEFAULT 1 REFERENCES manual_first_broadcast_consume_empty(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER manual_b24_consume_check_source BEFORE INSERT ON manual_first_broadcast_consume_checks
WHEN NOT EXISTS(SELECT 1 FROM manual_first_broadcast_consume_source p
 JOIN manual_first_broadcast_consume_scopes s ON s.permit_id=p.permit_id
 WHERE p.permit_id=NEW.permit_id AND s.opened_at=NEW.checked_at
 AND manual_artifact_time_valid(hex(NEW.checked_at),typeof(NEW.checked_at))=1
 AND manual_time_cmp(NEW.checked_at,p.issued_at)>0
 AND NOT EXISTS(SELECT 1 FROM json_each(p.expiries) WHERE manual_time_cmp(NEW.checked_at,value)>=0)
 AND NOT EXISTS(SELECT 1 FROM json_each(p.not_before) WHERE manual_time_cmp(NEW.checked_at,value)<0))
BEGIN SELECT RAISE(ABORT,'manual consume current source required'); END;
CREATE TRIGGER manual_b24_consume_check_no_update BEFORE UPDATE ON manual_first_broadcast_consume_checks
BEGIN SELECT RAISE(ABORT,'manual consume check immutable'); END;
CREATE TRIGGER manual_b24_consume_check_close AFTER INSERT ON manual_first_broadcast_consume_checks
BEGIN DELETE FROM manual_first_broadcast_consume_checks WHERE permit_id=NEW.permit_id; END;
CREATE TRIGGER manual_b24_first_permit_consume_exact BEFORE UPDATE OF state ON canary_send_permits
WHEN EXISTS(SELECT 1 FROM manual_first_broadcast_issuances WHERE id=OLD.id)
BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_first_broadcast_consume_scopes s
 WHERE s.permit_id=OLD.id AND s.operation_id=OLD.operation_id AND s.attempt_id=OLD.attempt_id
 AND s.authorization_id=OLD.authorization_id AND s.authorization_epoch=OLD.authorization_epoch
 AND s.artifact_hash=OLD.artifact_hash AND s.opened_at=NEW.consumed_at)
 OR OLD.state!='ISSUED' OR NEW.state!='CONSUMED' OR NEW.id!=OLD.id
 OR NEW.operation_id!=OLD.operation_id OR NEW.attempt_id!=OLD.attempt_id
 OR NEW.authorization_id!=OLD.authorization_id OR NEW.authorization_epoch!=OLD.authorization_epoch
 OR NEW.purpose!='FIRST_BROADCAST' OR NEW.artifact_hash!=OLD.artifact_hash OR NEW.tx_hash!=OLD.tx_hash
 OR NEW.issued_at!=OLD.issued_at OR NEW.expires_at!=OLD.expires_at OR NEW.consumed_at IS NULL
 OR manual_artifact_time_valid(hex(NEW.consumed_at),typeof(NEW.consumed_at))!=1
 OR manual_time_cmp(NEW.consumed_at,NEW.issued_at)<=0 OR manual_time_cmp(NEW.consumed_at,NEW.expires_at)>=0
 THEN RAISE(ABORT,'manual first broadcast consume identity required') END;
 INSERT INTO manual_first_broadcast_consume_checks(permit_id,checked_at) VALUES(OLD.id,NEW.consumed_at);
END;
CREATE TRIGGER manual_b24_first_permit_consume_audit AFTER UPDATE OF state ON canary_send_permits
WHEN OLD.state='ISSUED' AND NEW.state='CONSUMED' AND EXISTS(SELECT 1 FROM manual_first_broadcast_issuances WHERE id=NEW.id)
BEGIN
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 VALUES('manual-first-permit-consumed-audit:'||NEW.id,'MANUAL_FIRST_BROADCAST_CONSUMED',NEW.operation_id,NEW.attempt_id,NEW.authorization_id,NEW.authorization_epoch,'PRIVATE_CONSUME_ONLY',json_object('permit_id',NEW.id,'artifact_hash',NEW.artifact_hash,'tx_hash',NEW.tx_hash,'consumed_at',NEW.consumed_at),NEW.consumed_at);
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) VALUES('manual-first-permit-consumed-alert:'||NEW.id,'manual-first-permit-consumed-audit:'||NEW.id,'INFO','PENDING',NEW.consumed_at);
 DELETE FROM manual_first_broadcast_consume_scopes WHERE permit_id=NEW.id;
END;
CREATE TRIGGER manual_b24_consume_scope_close BEFORE DELETE ON manual_first_broadcast_consume_scopes
WHEN NOT EXISTS(SELECT 1 FROM canary_send_permits p
 JOIN canary_runtime_audit a ON a.id='manual-first-permit-consumed-audit:'||p.id
 AND a.event_type='MANUAL_FIRST_BROADCAST_CONSUMED' AND a.operation_id=p.operation_id AND a.attempt_id=p.attempt_id
 AND a.authorization_id=p.authorization_id AND a.authorization_epoch=p.authorization_epoch
 AND a.reason_code='PRIVATE_CONSUME_ONLY' AND a.created_at=p.consumed_at
 AND a.details_json=json_object('permit_id',p.id,'artifact_hash',p.artifact_hash,'tx_hash',p.tx_hash,'consumed_at',p.consumed_at)
 JOIN canary_alert_outbox b ON b.id='manual-first-permit-consumed-alert:'||p.id AND b.audit_id=a.id
 AND b.severity='INFO' AND b.created_at=p.consumed_at
 WHERE p.id=OLD.permit_id AND p.state='CONSUMED' AND p.consumed_at=OLD.opened_at)
BEGIN SELECT RAISE(ABORT,'manual consume requires complete audit and alert'); END;
