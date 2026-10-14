-- Foundation only. An initial marker's witness creation is a single SQL
-- statement, not a reusable permission. Retained markers cannot reopen it.
-- This transient table MUST be empty after successful marker insertion; it is
-- not another execution phase, durable economic owner, signing or send permit.
CREATE TABLE manual_signing_witness_creation_scopes(
 operation_id TEXT NOT NULL PRIMARY KEY REFERENCES manual_signing_intents(operation_id) DEFERRABLE INITIALLY DEFERRED,
 owner_id TEXT NOT NULL,
 facts_hash TEXT NOT NULL
);
CREATE TRIGGER manual_signing_witness_scope_source BEFORE INSERT ON manual_signing_witness_creation_scopes
WHEN EXISTS(SELECT 1 FROM manual_signing_intents i WHERE hex(i.operation_id)=hex(NEW.operation_id))
 OR EXISTS(SELECT 1 FROM manual_signing_witness_creation_scopes s WHERE hex(s.operation_id)=hex(NEW.operation_id) OR s.rowid=NEW.rowid)
 OR NOT EXISTS(SELECT 1 FROM manual_nonce_claim_complete q JOIN manual_signing_facts f ON f.operation_id=q.operation_id
 WHERE q.operation_id IS NEW.operation_id AND typeof(NEW.operation_id)='text' AND typeof(NEW.facts_hash)='text'
 AND manual_artifact_facts_hash(hex(f.facts_json),typeof(f.facts_json))=NEW.facts_hash
 AND typeof(NEW.owner_id)='text' AND length(CAST(NEW.owner_id AS BLOB))=64 AND length(NEW.owner_id)=64
 AND NEW.owner_id NOT GLOB '*[^0-9a-f]*')
BEGIN SELECT RAISE(ABORT,'manual signing witness creation cannot reopen'); END;
CREATE TRIGGER manual_signing_witness_scope_no_update BEFORE UPDATE ON manual_signing_witness_creation_scopes
BEGIN SELECT RAISE(ABORT,'manual signing witness creation identity immutable'); END;

-- Preserve all 034 completeness predicates. The pre-close view is only for
-- verifying exact effects before scope deletion; it is NOT public history.
CREATE VIEW manual_signing_intent_034_complete AS
SELECT i.* FROM manual_signing_intent_033_complete i
JOIN canary_alert_outbox canonical ON canonical.id='manual-sign-intent-alert:'||i.operation_id
WHERE length(i.owner_id)=64
 AND NOT EXISTS(SELECT 1 FROM manual_signing_alert_witnesses w
 WHERE hex(w.operation_id)=hex(i.operation_id) AND w.witness_rowid!=canonical.rowid);
CREATE TRIGGER manual_signing_witness_scope_delete BEFORE DELETE ON manual_signing_witness_creation_scopes
WHEN NOT EXISTS(SELECT 1 FROM manual_signing_intent_034_complete i WHERE i.operation_id IS OLD.operation_id
 AND i.owner_id IS OLD.owner_id AND i.facts_hash IS OLD.facts_hash)
BEGIN SELECT RAISE(ABORT,'manual signing witness creation cannot discard incomplete effects'); END;
DROP VIEW manual_signing_intent_complete;
CREATE VIEW manual_signing_intent_complete AS
SELECT i.* FROM manual_signing_intent_034_complete i
WHERE NOT EXISTS(SELECT 1 FROM manual_signing_witness_creation_scopes s WHERE hex(s.operation_id)=hex(i.operation_id));

CREATE TRIGGER manual_signing_witness_scope_open BEFORE INSERT ON manual_signing_intents
BEGIN
 INSERT INTO manual_signing_witness_creation_scopes(operation_id,owner_id,facts_hash)
 VALUES(NEW.operation_id,NEW.owner_id,NEW.facts_hash);
END;
CREATE TRIGGER manual_signing_audit_creation_only BEFORE INSERT ON canary_runtime_audit
WHEN EXISTS(SELECT 1 FROM manual_signing_intents i
 WHERE hex(NEW.id)=hex('manual-sign-intent:'||i.operation_id)
 OR (hex(NEW.event_type)=hex('MANUAL_SIGNING_INTENT') AND hex(NEW.operation_id)=hex(i.operation_id)))
 AND NOT EXISTS(SELECT 1 FROM manual_signing_witness_creation_scopes s JOIN manual_signing_intents i
 ON i.operation_id IS s.operation_id AND i.owner_id IS s.owner_id AND i.facts_hash IS s.facts_hash
 WHERE NEW.id IS 'manual-sign-intent:'||i.operation_id AND NEW.event_type IS 'MANUAL_SIGNING_INTENT' AND NEW.operation_id IS i.operation_id)
BEGIN SELECT RAISE(ABORT,'manual signing audit initial statement only'); END;
CREATE TRIGGER manual_signing_alert_creation_only BEFORE INSERT ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_signing_intents i
 WHERE hex(NEW.id)=hex('manual-sign-intent-alert:'||i.operation_id) OR hex(NEW.audit_id)=hex('manual-sign-intent:'||i.operation_id))
 AND NOT EXISTS(SELECT 1 FROM manual_signing_witness_creation_scopes s JOIN manual_signing_intents i
 ON i.operation_id IS s.operation_id AND i.owner_id IS s.owner_id AND i.facts_hash IS s.facts_hash
 WHERE NEW.id IS 'manual-sign-intent-alert:'||i.operation_id AND NEW.audit_id IS 'manual-sign-intent:'||i.operation_id)
BEGIN SELECT RAISE(ABORT,'manual signing alert initial statement only'); END;

-- Only replace the marker's own effect trigger. Original witness payloads and
-- all unrelated/source/phase guards remain intact. A failed/ignored close rolls
-- back marker+witnesses+scope together, including outer IGNORE/REPLACE.
DROP TRIGGER manual_signing_intent_effect;
CREATE TRIGGER manual_signing_intent_effect AFTER INSERT ON manual_signing_intents
BEGIN
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'manual-sign-intent:'||NEW.operation_id,'MANUAL_SIGNING_INTENT',NEW.operation_id,q.attempt_id,b.authorization_id,b.authorization_epoch,'SIGNER_MAY_HAVE_RUN_NOT_PERMISSION',
 json_object('owner_id',NEW.owner_id,'facts_hash',NEW.facts_hash,'observation_hash',NEW.observation_hash,'execution_step_id',q.execution_step_id,'nonce',q.nonce),NEW.checked_at
 FROM manual_nonce_claims q JOIN manual_execution_bindings b ON b.operation_id=q.operation_id WHERE q.operation_id=NEW.operation_id;
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) VALUES('manual-sign-intent-alert:'||NEW.operation_id,'manual-sign-intent:'||NEW.operation_id,'WARNING','PENDING',NEW.checked_at);
 DELETE FROM manual_signing_witness_creation_scopes WHERE operation_id=NEW.operation_id;
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_signing_intent_complete WHERE operation_id=NEW.operation_id)
 OR EXISTS(SELECT 1 FROM manual_signing_witness_creation_scopes WHERE hex(operation_id)=hex(NEW.operation_id))
 THEN RAISE(ABORT,'manual signing intent incomplete audit effect') END;
END;
