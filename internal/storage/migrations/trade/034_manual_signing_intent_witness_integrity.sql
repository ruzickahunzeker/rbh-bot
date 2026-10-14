-- B3 foundation residuals only. Preserve 001..033, prepared attempt/step/lane
-- phases and every signing/send guard. Encoded comparisons below are denial
-- witnesses, NEVER normalization or permission to repair retained history.
CREATE VIEW manual_signing_audit_witnesses AS
SELECT i.operation_id,a.rowid AS witness_rowid,a.id
FROM manual_signing_intents i JOIN canary_runtime_audit a
 ON hex(a.id)=hex('manual-sign-intent:'||i.operation_id)
 OR (hex(a.event_type)=hex('MANUAL_SIGNING_INTENT') AND hex(a.operation_id)=hex(i.operation_id));
CREATE VIEW manual_signing_alert_witnesses AS
SELECT i.operation_id,a.rowid AS witness_rowid,a.id,a.audit_id
FROM manual_signing_intents i JOIN canary_alert_outbox a
 ON hex(a.id)=hex('manual-sign-intent-alert:'||i.operation_id)
 OR hex(a.audit_id)=hex('manual-sign-intent:'||i.operation_id);

-- length(TEXT) alone is insufficient for byte length, and GLOB alone stops at
-- NUL. Requiring both lengths plus the original ASCII alphabet excludes both
-- interior NULs and multibyte substitutions. Explicit ABORT also covers IGNORE.
CREATE TRIGGER manual_signing_owner_full_bytes BEFORE INSERT ON manual_signing_intents
WHEN typeof(NEW.owner_id) IS NOT 'text' OR length(CAST(NEW.owner_id AS BLOB)) IS NOT 64
 OR length(NEW.owner_id) IS NOT 64 OR NEW.owner_id GLOB '*[^0-9a-f]*'
BEGIN SELECT RAISE(ABORT,'manual signing owner exact lowercase hex required'); END;

CREATE TRIGGER manual_signing_audit_encoded_insert BEFORE INSERT ON canary_runtime_audit
WHEN EXISTS(SELECT 1 FROM manual_signing_audit_witnesses w
 WHERE w.witness_rowid=NEW.rowid OR hex(w.id)=hex(NEW.id)
 OR hex(NEW.id)=hex('manual-sign-intent:'||w.operation_id)
 OR (hex(NEW.event_type)=hex('MANUAL_SIGNING_INTENT') AND hex(NEW.operation_id)=hex(w.operation_id)))
BEGIN SELECT RAISE(ABORT,'manual signing retained audit cannot be replaced or adopted'); END;

CREATE TRIGGER manual_signing_alert_encoded_insert BEFORE INSERT ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_signing_alert_witnesses w
 WHERE w.witness_rowid=NEW.rowid OR hex(w.id)=hex(NEW.id) OR hex(w.audit_id)=hex(NEW.audit_id)
 OR hex(NEW.id)=hex('manual-sign-intent-alert:'||w.operation_id)
 OR hex(NEW.audit_id)=hex('manual-sign-intent:'||w.operation_id))
BEGIN SELECT RAISE(ABORT,'manual signing retained alert cannot be replaced or adopted'); END;
CREATE TRIGGER manual_signing_alert_encoded_update BEFORE UPDATE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_signing_alert_witnesses w WHERE w.witness_rowid=OLD.rowid
 AND (NEW.rowid IS NOT OLD.rowid OR NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id
 OR NEW.severity IS NOT OLD.severity OR NEW.created_at IS NOT OLD.created_at))
 OR EXISTS(SELECT 1 FROM manual_signing_alert_witnesses w WHERE w.witness_rowid!=OLD.rowid
 AND (w.witness_rowid=NEW.rowid OR hex(w.id)=hex(NEW.id) OR hex(w.audit_id)=hex(NEW.audit_id)
 OR hex(NEW.id)=hex('manual-sign-intent-alert:'||w.operation_id)
 OR hex(NEW.audit_id)=hex('manual-sign-intent:'||w.operation_id)))
 -- An unrelated row cannot become a second witness, even when no exact TEXT
 -- witness exists. Original 033 shape guards continue to validate all inserts.
 OR (NOT EXISTS(SELECT 1 FROM manual_signing_alert_witnesses w WHERE w.witness_rowid=OLD.rowid)
 AND EXISTS(SELECT 1 FROM manual_signing_intents i
 WHERE hex(NEW.id)=hex('manual-sign-intent-alert:'||i.operation_id)
 OR hex(NEW.audit_id)=hex('manual-sign-intent:'||i.operation_id)))
BEGIN SELECT RAISE(ABORT,'manual signing retained alert identity cannot change'); END;
CREATE TRIGGER manual_signing_alert_encoded_delete BEFORE DELETE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_signing_alert_witnesses w WHERE w.witness_rowid=OLD.rowid)
BEGIN SELECT RAISE(ABORT,'manual signing retained alert cannot be deleted'); END;

-- Preserve every 033 completeness condition verbatim in a historical layer.
CREATE VIEW manual_signing_intent_033_complete AS
SELECT i.* FROM manual_signing_intents i JOIN manual_nonce_claim_complete q ON q.operation_id=i.operation_id
 JOIN manual_signing_facts f ON f.operation_id=i.operation_id AND hex(f.facts_json)=hex(i.facts_json)
 JOIN manual_nonce_binding_facts nf ON nf.operation_id=i.operation_id
 JOIN manual_execution_bindings b ON b.operation_id=i.operation_id
 JOIN canary_runtime_audit a ON a.id='manual-sign-intent:'||i.operation_id AND a.event_type='MANUAL_SIGNING_INTENT'
 AND a.operation_id=i.operation_id AND a.attempt_id=q.attempt_id AND a.authorization_id=b.authorization_id AND a.authorization_epoch=b.authorization_epoch
 AND a.reason_code='SIGNER_MAY_HAVE_RUN_NOT_PERMISSION' AND a.created_at=i.checked_at
 AND a.details_json=json_object('owner_id',i.owner_id,'facts_hash',i.facts_hash,'observation_hash',i.observation_hash,'execution_step_id',q.execution_step_id,'nonce',q.nonce)
 JOIN canary_alert_outbox alert ON alert.id='manual-sign-intent-alert:'||i.operation_id AND alert.audit_id=a.id AND alert.severity='WARNING' AND alert.created_at=i.checked_at
WHERE typeof(i.owner_id)='text' AND length(CAST(i.owner_id AS BLOB))=64 AND i.owner_id NOT GLOB '*[^0-9a-f]*'
 AND manual_artifact_facts_hash(hex(i.facts_json),typeof(i.facts_json))=i.facts_hash
 AND manual_nonce_observation_bytes_valid(hex(i.observation_json),typeof(i.observation_json),hex(i.observation_hash),typeof(i.observation_hash),
 hex(q.nonce),typeof(q.nonce),hex(nf.facts_json),typeof(nf.facts_json),hex(i.checked_at),typeof(i.checked_at))=1
 AND manual_time_cmp(i.checked_at,q.checked_at)>=0 AND manual_time_cmp(json_extract(i.observation_json,'$.observed_at'),q.checked_at)>=0
 AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit extra WHERE extra.rowid!=a.rowid AND (hex(extra.id)=hex(a.id)
 OR (manual_nonce_identity_relation(hex(extra.event_type),typeof(extra.event_type),hex('MANUAL_SIGNING_INTENT'),0)>0
 AND manual_nonce_identity_relation(hex(extra.operation_id),typeof(extra.operation_id),hex(i.operation_id),0)>0)))
 AND NOT EXISTS(SELECT 1 FROM manual_signing_intents extra WHERE extra.rowid!=i.rowid AND (hex(extra.operation_id)=hex(i.operation_id) OR hex(extra.owner_id)=hex(i.owner_id)
 OR hex(CASE WHEN json_valid(extra.facts_json) THEN json_extract(extra.facts_json,'$.operation_id') END)=hex(i.operation_id)));
DROP VIEW manual_signing_intent_complete;
CREATE VIEW manual_signing_intent_complete AS
SELECT i.* FROM manual_signing_intent_033_complete i
JOIN canary_alert_outbox canonical ON canonical.id='manual-sign-intent-alert:'||i.operation_id
WHERE length(i.owner_id)=64
 AND NOT EXISTS(SELECT 1 FROM manual_signing_alert_witnesses w
 WHERE hex(w.operation_id)=hex(i.operation_id) AND w.witness_rowid!=canonical.rowid);
