-- M4-B1: immutable structural history only. No arm, gate, permit, submission,
-- transport or recovery exception. Keep all original 001..036 deny domains.
-- SQL completeness does NOT authenticate ciphertext or grant action permission.
CREATE TABLE manual_submission_foundations(
 operation_id TEXT NOT NULL PRIMARY KEY REFERENCES manual_signed_artifact_commands(operation_id),
 binding_json TEXT NOT NULL,
 binding_hash TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE TABLE manual_submission_foundation_scopes(
 operation_id TEXT NOT NULL PRIMARY KEY REFERENCES manual_submission_foundations(operation_id) DEFERRABLE INITIALLY DEFERRED,
 binding_hash TEXT NOT NULL
);

CREATE VIEW manual_submission_foundation_expected AS
SELECT m.operation_id,m.checked_at AS artifact_completed_at,
 json_object('version',1,'source','MANUAL','format','MANUAL_V1_FULL_FACTS_AAD',
 'status','FOUNDATION_METADATA_NOT_PERMISSION','facts',json(m.facts_json),'facts_hash',m.facts_hash,
 'envelope_hash',manual_artifact_bytes_sha256(hex(m.envelope_json)),
 'tx_hash',json_extract(m.envelope_json,'$.tx_hash'),'raw_sha256',json_extract(m.envelope_json,'$.raw_sha256'),
 'key_version',json_extract(m.envelope_json,'$.key_version'),'artifact_completed_at',m.checked_at,
 'contract',json(c.contract_json),'contract_hash',c.contract_hash,
 'reservation',json_object('id',r.id,'operation_id',r.operation_id,'wallet_id',r.wallet_id,
 'input_amount',r.input_amount,'gas_budget',r.gas_budget,'unit_asset',unit.input_asset,
 'unit_decimals',unit.input_decimals,'unit_kind',unit.accounting_kind,'chain_id',unit.chain_id),
 'usage',json_object('authorization_id',u.authorization_id,'operation_id',u.operation_id,'amount',u.amount),
 'execution_authorized',json('false'),'signing_authorized',json('false'),
 'send_authorized',json('false'),'replay_authorized',json('false')) AS binding_json
FROM (SELECT * FROM manual_signed_artifact_complete LIMIT -1) m
JOIN manual_nonce_claims q ON q.operation_id=m.operation_id
JOIN manual_admission_commands c ON c.operation_id=m.operation_id
JOIN manual_execution_bindings b ON b.operation_id=m.operation_id
JOIN canary_risk_reservations r ON r.id=q.reservation_id AND r.operation_id=q.operation_id AND r.wallet_id=q.wallet_id
JOIN manual_reservation_units unit ON unit.reservation_id=r.id AND unit.operation_id=q.operation_id
JOIN canary_authorization_operation_usage u ON u.operation_id=q.operation_id AND u.authorization_id=b.authorization_id
JOIN manual_http_order_sources src ON src.id=c.request_id AND src.operation_id=m.operation_id
WHERE typeof(src.operation_id)='text' AND src.operation_id='manual_op_'||src.id
 AND src.source='MANUAL' AND src.chain='robinhood'
 AND (SELECT COUNT(*) FROM manual_http_order_sources x WHERE hex(x.id)=hex(src.id) OR hex(x.operation_id)=hex(src.operation_id))=1
 AND NOT EXISTS(SELECT 1 FROM canary_admission_sources x WHERE hex(x.operation_id)=hex(m.operation_id))
 AND NOT EXISTS(SELECT 1 FROM dry_run_results x WHERE hex(x.operation_id)=hex(m.operation_id));

CREATE TRIGGER manual_submission_foundation_no_update BEFORE UPDATE ON manual_submission_foundations
BEGIN SELECT RAISE(ABORT,'manual submission foundation immutable'); END;
CREATE TRIGGER manual_submission_foundation_no_delete BEFORE DELETE ON manual_submission_foundations
BEGIN SELECT RAISE(ABORT,'manual submission foundation retained'); END;
CREATE TRIGGER manual_submission_foundation_source BEFORE INSERT ON manual_submission_foundations
WHEN NOT EXISTS(SELECT 1 FROM manual_submission_foundation_expected e
 WHERE e.operation_id IS NEW.operation_id AND typeof(NEW.operation_id)='text'
 AND typeof(NEW.binding_json)='text' AND hex(NEW.binding_json)=hex(e.binding_json)
 AND typeof(NEW.binding_hash)='text' AND hex(NEW.binding_hash)=hex(manual_artifact_bytes_sha256(hex(e.binding_json)))
 AND manual_artifact_time_valid(hex(NEW.created_at),typeof(NEW.created_at))=1
 AND manual_time_cmp(NEW.created_at,e.artifact_completed_at)>=0)
 OR EXISTS(SELECT 1 FROM manual_submission_foundations x WHERE hex(x.operation_id)=hex(NEW.operation_id) OR x.rowid=NEW.rowid)
 OR EXISTS(SELECT 1 FROM canary_runtime_audit a WHERE hex(a.id)=hex('manual-submission-foundation:'||NEW.operation_id)
 OR (hex(a.event_type)=hex('MANUAL_SUBMISSION_FOUNDATION') AND hex(a.operation_id)=hex(NEW.operation_id)))
 OR EXISTS(SELECT 1 FROM canary_alert_outbox a WHERE hex(a.id)=hex('manual-submission-foundation-alert:'||NEW.operation_id)
 OR hex(a.audit_id)=hex('manual-submission-foundation:'||NEW.operation_id))
BEGIN SELECT RAISE(ABORT,'manual submission foundation exact initial source required'); END;

-- Transient same-statement witness scope, not a durable execution owner.
CREATE TRIGGER manual_submission_foundation_scope_source BEFORE INSERT ON manual_submission_foundation_scopes
WHEN typeof(NEW.operation_id)!='text' OR typeof(NEW.binding_hash)!='text'
 OR EXISTS(SELECT 1 FROM manual_submission_foundations x WHERE hex(x.operation_id)=hex(NEW.operation_id))
 OR EXISTS(SELECT 1 FROM manual_submission_foundation_scopes x WHERE hex(x.operation_id)=hex(NEW.operation_id) OR x.rowid=NEW.rowid)
 OR NOT EXISTS(SELECT 1 FROM manual_submission_foundation_expected e WHERE e.operation_id IS NEW.operation_id
 AND hex(NEW.binding_hash)=hex(manual_artifact_bytes_sha256(hex(e.binding_json))))
BEGIN SELECT RAISE(ABORT,'manual submission foundation scope cannot reopen'); END;
CREATE TRIGGER manual_submission_foundation_scope_no_update BEFORE UPDATE ON manual_submission_foundation_scopes
BEGIN SELECT RAISE(ABORT,'manual submission foundation scope immutable'); END;
CREATE TRIGGER manual_submission_foundation_scope_open BEFORE INSERT ON manual_submission_foundations
BEGIN
 INSERT INTO manual_submission_foundation_scopes VALUES(NEW.operation_id,NEW.binding_hash);
END;

CREATE VIEW manual_submission_foundation_preclose AS
SELECT f.* FROM manual_submission_foundations f
JOIN manual_submission_foundation_expected e ON e.operation_id IS f.operation_id AND hex(e.binding_json)=hex(f.binding_json)
JOIN manual_execution_bindings b ON b.operation_id=f.operation_id
JOIN manual_nonce_claims q ON q.operation_id=f.operation_id
JOIN canary_runtime_audit a ON a.id='manual-submission-foundation:'||f.operation_id
 AND typeof(a.id)='text' AND a.event_type IS 'MANUAL_SUBMISSION_FOUNDATION'
 AND a.operation_id IS f.operation_id AND a.attempt_id IS q.attempt_id
 AND a.authorization_id IS b.authorization_id AND a.authorization_epoch IS b.authorization_epoch
 AND a.reason_code IS 'METADATA_ONLY_NOT_GATE_OR_SEND_PERMISSION' AND a.created_at IS f.created_at
 AND hex(a.details_json)=hex(json_object('binding_hash',f.binding_hash,'execution_step_id',q.execution_step_id,'facts_hash',json_extract(f.binding_json,'$.facts_hash')))
JOIN canary_alert_outbox alert ON alert.id='manual-submission-foundation-alert:'||f.operation_id
 AND typeof(alert.id)='text' AND alert.audit_id IS a.id AND alert.severity IS 'INFO' AND alert.created_at IS f.created_at
WHERE typeof(f.operation_id)='text' AND typeof(f.binding_json)='text' AND typeof(f.binding_hash)='text'
 AND hex(f.binding_hash)=hex(manual_artifact_bytes_sha256(hex(e.binding_json)))
 AND manual_artifact_time_valid(hex(f.created_at),typeof(f.created_at))=1
 AND manual_time_cmp(f.created_at,e.artifact_completed_at)>=0
 AND NOT EXISTS(SELECT 1 FROM manual_submission_foundations x WHERE x.rowid!=f.rowid AND hex(x.operation_id)=hex(f.operation_id))
 AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit x WHERE x.rowid!=a.rowid AND
 (hex(x.id)=hex(a.id) OR (hex(x.event_type)=hex(a.event_type) AND hex(x.operation_id)=hex(f.operation_id))))
 AND NOT EXISTS(SELECT 1 FROM canary_alert_outbox x WHERE x.rowid!=alert.rowid AND (hex(x.id)=hex(alert.id) OR hex(x.audit_id)=hex(a.id)));
CREATE VIEW manual_submission_foundation_complete AS
SELECT f.* FROM manual_submission_foundation_preclose f
WHERE NOT EXISTS(SELECT 1 FROM manual_submission_foundation_scopes x WHERE hex(x.operation_id)=hex(f.operation_id));
CREATE TRIGGER manual_submission_foundation_scope_close BEFORE DELETE ON manual_submission_foundation_scopes
WHEN NOT EXISTS(SELECT 1 FROM manual_submission_foundation_preclose f WHERE f.operation_id IS OLD.operation_id AND f.binding_hash IS OLD.binding_hash)
BEGIN SELECT RAISE(ABORT,'manual submission foundation incomplete witnesses'); END;

CREATE TRIGGER manual_submission_foundation_audit_insert BEFORE INSERT ON canary_runtime_audit
WHEN (hex(NEW.event_type)=hex('MANUAL_SUBMISSION_FOUNDATION')
 OR substr(hex(NEW.id),1,length(hex('manual-submission-foundation:')))=hex('manual-submission-foundation:')
 OR EXISTS(SELECT 1 FROM canary_runtime_audit a JOIN manual_submission_foundations f
 ON hex(a.id)=hex('manual-submission-foundation:'||f.operation_id)
 WHERE a.rowid=NEW.rowid OR hex(a.id)=hex(NEW.id)))
 AND NOT EXISTS(SELECT 1 FROM manual_submission_foundation_scopes s JOIN manual_submission_foundations f
 ON f.operation_id IS s.operation_id AND f.binding_hash IS s.binding_hash
 JOIN manual_execution_bindings b ON b.operation_id=f.operation_id JOIN manual_nonce_claims q ON q.operation_id=f.operation_id
 WHERE NEW.id IS 'manual-submission-foundation:'||f.operation_id AND NEW.event_type IS 'MANUAL_SUBMISSION_FOUNDATION'
 AND NEW.operation_id IS f.operation_id AND NEW.attempt_id IS q.attempt_id
 AND NEW.authorization_id IS b.authorization_id AND NEW.authorization_epoch IS b.authorization_epoch
 AND NEW.reason_code IS 'METADATA_ONLY_NOT_GATE_OR_SEND_PERMISSION' AND NEW.created_at IS f.created_at
 AND typeof(NEW.details_json)='text' AND hex(NEW.details_json)=hex(json_object('binding_hash',f.binding_hash,'execution_step_id',q.execution_step_id,'facts_hash',json_extract(f.binding_json,'$.facts_hash')))
 AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit x WHERE hex(x.id)=hex(NEW.id) OR x.rowid=NEW.rowid
 OR (hex(x.event_type)=hex(NEW.event_type) AND hex(x.operation_id)=hex(NEW.operation_id))))
BEGIN SELECT RAISE(ABORT,'manual submission foundation audit initial statement only'); END;
CREATE TRIGGER manual_submission_foundation_alert_insert BEFORE INSERT ON canary_alert_outbox
WHEN (substr(hex(NEW.id),1,length(hex('manual-submission-foundation-alert:')))=hex('manual-submission-foundation-alert:')
 OR substr(hex(NEW.audit_id),1,length(hex('manual-submission-foundation:')))=hex('manual-submission-foundation:')
 OR EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_submission_foundations f
 ON hex(a.id)=hex('manual-submission-foundation-alert:'||f.operation_id) OR hex(a.audit_id)=hex('manual-submission-foundation:'||f.operation_id)
 WHERE a.rowid=NEW.rowid OR hex(a.id)=hex(NEW.id) OR hex(a.audit_id)=hex(NEW.audit_id)))
 AND NOT EXISTS(SELECT 1 FROM manual_submission_foundation_scopes s JOIN manual_submission_foundations f
 ON f.operation_id IS s.operation_id AND f.binding_hash IS s.binding_hash
 WHERE NEW.id IS 'manual-submission-foundation-alert:'||f.operation_id AND NEW.audit_id IS 'manual-submission-foundation:'||f.operation_id
 AND NEW.severity IS 'INFO' AND NEW.state IS 'PENDING' AND NEW.created_at IS f.created_at AND NEW.delivered_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM canary_alert_outbox x WHERE hex(x.id)=hex(NEW.id) OR hex(x.audit_id)=hex(NEW.audit_id) OR x.rowid=NEW.rowid))
BEGIN SELECT RAISE(ABORT,'manual submission foundation alert initial statement only'); END;
CREATE TRIGGER manual_submission_foundation_alert_update BEFORE UPDATE ON canary_alert_outbox
WHEN ((substr(hex(NEW.id),1,length(hex('manual-submission-foundation-alert:')))=hex('manual-submission-foundation-alert:')
 OR substr(hex(NEW.audit_id),1,length(hex('manual-submission-foundation:')))=hex('manual-submission-foundation:'))
 AND (NEW.rowid IS NOT OLD.rowid OR NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id))
 OR (EXISTS(SELECT 1 FROM manual_submission_foundations f WHERE hex(OLD.id)=hex('manual-submission-foundation-alert:'||f.operation_id)
 OR hex(OLD.audit_id)=hex('manual-submission-foundation:'||f.operation_id))
 AND (NEW.rowid IS NOT OLD.rowid OR NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id
 OR NEW.severity IS NOT OLD.severity OR NEW.created_at IS NOT OLD.created_at))
 OR EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_submission_foundations f
 ON hex(a.id)=hex('manual-submission-foundation-alert:'||f.operation_id) OR hex(a.audit_id)=hex('manual-submission-foundation:'||f.operation_id)
 WHERE a.rowid!=OLD.rowid AND (a.rowid=NEW.rowid OR hex(a.id)=hex(NEW.id) OR hex(a.audit_id)=hex(NEW.audit_id)))
BEGIN SELECT RAISE(ABORT,'manual submission foundation alert identity immutable'); END;
CREATE TRIGGER manual_submission_foundation_alert_delete BEFORE DELETE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_submission_foundations f WHERE hex(OLD.id)=hex('manual-submission-foundation-alert:'||f.operation_id)
 OR hex(OLD.audit_id)=hex('manual-submission-foundation:'||f.operation_id))
BEGIN SELECT RAISE(ABORT,'manual submission foundation alert retained'); END;

CREATE TRIGGER manual_submission_foundation_effect AFTER INSERT ON manual_submission_foundations
BEGIN
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'manual-submission-foundation:'||NEW.operation_id,'MANUAL_SUBMISSION_FOUNDATION',NEW.operation_id,q.attempt_id,b.authorization_id,b.authorization_epoch,
 'METADATA_ONLY_NOT_GATE_OR_SEND_PERMISSION',json_object('binding_hash',NEW.binding_hash,'execution_step_id',q.execution_step_id,'facts_hash',json_extract(NEW.binding_json,'$.facts_hash')),NEW.created_at
 FROM manual_nonce_claims q JOIN manual_execution_bindings b ON b.operation_id=q.operation_id WHERE q.operation_id=NEW.operation_id;
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-submission-foundation-alert:'||NEW.operation_id,'manual-submission-foundation:'||NEW.operation_id,'INFO','PENDING',NEW.created_at);
 DELETE FROM manual_submission_foundation_scopes WHERE operation_id=NEW.operation_id;
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_submission_foundation_complete WHERE operation_id=NEW.operation_id)
 OR EXISTS(SELECT 1 FROM manual_submission_foundation_scopes WHERE hex(operation_id)=hex(NEW.operation_id))
 THEN RAISE(ABORT,'manual submission foundation incomplete atomic effect') END;
END;
