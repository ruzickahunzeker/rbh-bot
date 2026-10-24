-- M4-B2.4: private, one-shot consumption of an exact MANUAL FIRST_BROADCAST permit.
-- This does not create a submission, invoke a signer, or connect a broadcaster.
DROP TRIGGER manual_b23_no_permit_consume_hardened;

CREATE TRIGGER manual_b24_first_permit_consume_exact
BEFORE UPDATE OF state ON canary_send_permits
WHEN EXISTS(SELECT 1 FROM manual_first_broadcast_issuances i WHERE i.id=OLD.id)
 AND (OLD.state!='ISSUED' OR NEW.state!='CONSUMED'
 OR NEW.id!=OLD.id OR NEW.operation_id!=OLD.operation_id OR NEW.attempt_id!=OLD.attempt_id
 OR NEW.authorization_id!=OLD.authorization_id OR NEW.authorization_epoch!=OLD.authorization_epoch
 OR NEW.purpose!='FIRST_BROADCAST' OR NEW.artifact_hash!=OLD.artifact_hash OR NEW.tx_hash!=OLD.tx_hash
 OR NEW.issued_at!=OLD.issued_at OR NEW.expires_at!=OLD.expires_at
 OR NEW.consumed_at IS NULL OR manual_artifact_time_valid(hex(NEW.consumed_at),typeof(NEW.consumed_at))!=1
 OR manual_time_cmp(NEW.consumed_at,NEW.issued_at)<=0
 OR manual_time_cmp(NEW.consumed_at,NEW.expires_at)>0)
BEGIN SELECT RAISE(ABORT,'manual first broadcast consume identity required'); END;

CREATE TRIGGER manual_b24_first_permit_consume_audit
AFTER UPDATE OF state ON canary_send_permits
WHEN OLD.state='ISSUED' AND NEW.state='CONSUMED'
 AND EXISTS(SELECT 1 FROM manual_first_broadcast_issuances WHERE id=NEW.id)
BEGIN
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 VALUES('manual-first-permit-consumed-audit:'||NEW.id,'MANUAL_FIRST_BROADCAST_CONSUMED',NEW.operation_id,NEW.attempt_id,NEW.authorization_id,NEW.authorization_epoch,'PRIVATE_CONSUME_ONLY',json_object('permit_id',NEW.id,'artifact_hash',NEW.artifact_hash,'tx_hash',NEW.tx_hash,'consumed_at',NEW.consumed_at),NEW.consumed_at);
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-first-permit-consumed-alert:'||NEW.id,'manual-first-permit-consumed-audit:'||NEW.id,'INFO','PENDING',NEW.consumed_at);
END;
