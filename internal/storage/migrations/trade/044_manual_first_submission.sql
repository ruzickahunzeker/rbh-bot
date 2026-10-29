-- A1: private first submission. No startup, replay, receipt or position capability.
-- The link is immutable provenance, not a second submission/accounting state.
CREATE TABLE manual_first_submission_empty(id INTEGER PRIMARY KEY);
CREATE TRIGGER manual_first_submission_empty_insert BEFORE INSERT ON manual_first_submission_empty BEGIN SELECT RAISE(ABORT,'manual submission empty target'); END;
CREATE TRIGGER manual_first_submission_empty_update BEFORE UPDATE ON manual_first_submission_empty BEGIN SELECT RAISE(ABORT,'manual submission empty target'); END;
CREATE TRIGGER manual_first_submission_empty_delete BEFORE DELETE ON manual_first_submission_empty BEGIN SELECT RAISE(ABORT,'manual submission empty target'); END;
CREATE TABLE manual_first_submission_bindings(
 permit_id TEXT NOT NULL PRIMARY KEY REFERENCES canary_send_permits(id),
 submission_id TEXT NOT NULL UNIQUE REFERENCES transaction_submissions(id) DEFERRABLE INITIALLY DEFERRED,
 operation_id TEXT NOT NULL UNIQUE REFERENCES manual_submission_foundations(operation_id),
 attempt_id TEXT NOT NULL UNIQUE REFERENCES transaction_attempts(id),
 authorization_id TEXT NOT NULL, authorization_epoch INTEGER NOT NULL,
 artifact_hash TEXT NOT NULL, tx_hash TEXT NOT NULL, foundation_hash TEXT NOT NULL,
 environment TEXT NOT NULL, holder_id TEXT NOT NULL, lease_epoch INTEGER NOT NULL,
 created_at TEXT NOT NULL
);
CREATE TABLE manual_first_submission_scopes(
 permit_id TEXT NOT NULL PRIMARY KEY REFERENCES manual_first_submission_bindings(permit_id) DEFERRABLE INITIALLY DEFERRED,
 empty_ref INTEGER NOT NULL DEFAULT 1 REFERENCES manual_first_submission_empty(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER manual_first_submission_scope_insert BEFORE INSERT ON manual_first_submission_scopes
WHEN EXISTS(SELECT 1 FROM manual_first_submission_bindings b WHERE hex(b.permit_id)=hex(NEW.permit_id))
 OR EXISTS(SELECT 1 FROM manual_first_submission_scopes s WHERE hex(s.permit_id)=hex(NEW.permit_id) OR s.rowid=NEW.rowid)
 OR NOT EXISTS(SELECT 1 FROM manual_first_broadcast_consume_source p WHERE p.permit_id IS NEW.permit_id)
BEGIN SELECT RAISE(ABORT,'manual submission claim scope cannot reopen'); END;
CREATE TRIGGER manual_first_submission_scope_update BEFORE UPDATE ON manual_first_submission_scopes
BEGIN SELECT RAISE(ABORT,'manual submission claim scope immutable'); END;
CREATE TRIGGER manual_first_submission_binding_source BEFORE INSERT ON manual_first_submission_bindings
WHEN typeof(NEW.permit_id)!='text' OR typeof(NEW.submission_id)!='text'
 OR NEW.submission_id IS NOT 'manual-first-submission:'||NEW.attempt_id
 OR NOT EXISTS(SELECT 1 FROM manual_first_broadcast_consume_source p
 JOIN manual_submission_foundation_complete f ON f.operation_id=p.operation_id
 JOIN manual_execution_bindings b ON b.operation_id=p.operation_id
 JOIN canary_worker_leases l ON l.role='SUBMISSION' AND l.environment=b.environment
 WHERE p.permit_id IS NEW.permit_id AND p.operation_id IS NEW.operation_id AND p.attempt_id IS NEW.attempt_id
 AND p.authorization_id IS NEW.authorization_id AND p.authorization_epoch IS NEW.authorization_epoch
 AND p.artifact_hash IS NEW.artifact_hash AND p.tx_hash IS NEW.tx_hash AND f.binding_hash IS NEW.foundation_hash
 AND l.environment IS NEW.environment AND l.holder_id IS NEW.holder_id AND l.lease_epoch IS NEW.lease_epoch
 AND manual_artifact_time_valid(hex(NEW.created_at),typeof(NEW.created_at))=1
 AND manual_time_cmp(NEW.created_at,p.issued_at)>0
 AND NOT EXISTS(SELECT 1 FROM json_each(p.expiries) WHERE manual_time_cmp(NEW.created_at,value)>=0)
 AND NOT EXISTS(SELECT 1 FROM json_each(p.not_before) WHERE manual_time_cmp(NEW.created_at,value)<0))
 OR EXISTS(SELECT 1 FROM manual_first_submission_bindings b WHERE b.rowid=NEW.rowid OR hex(b.permit_id)=hex(NEW.permit_id)
 OR hex(b.submission_id)=hex(NEW.submission_id) OR hex(b.operation_id)=hex(NEW.operation_id) OR hex(b.attempt_id)=hex(NEW.attempt_id))
 OR EXISTS(SELECT 1 FROM transaction_submissions s WHERE hex(s.id)=hex(NEW.submission_id) OR hex(s.attempt_id)=hex(NEW.attempt_id))
BEGIN SELECT RAISE(ABORT,'manual first submission requires exact fresh unconsumed source'); END;
CREATE TRIGGER manual_first_submission_binding_open BEFORE INSERT ON manual_first_submission_bindings
BEGIN INSERT INTO manual_first_submission_scopes(permit_id) VALUES(NEW.permit_id); END;
CREATE TRIGGER manual_first_submission_binding_update BEFORE UPDATE ON manual_first_submission_bindings
BEGIN SELECT RAISE(ABORT,'manual submission binding immutable'); END;
CREATE TRIGGER manual_first_submission_binding_delete BEFORE DELETE ON manual_first_submission_bindings
BEGIN SELECT RAISE(ABORT,'manual submission binding retained'); END;

DROP TRIGGER manual_nonce_no_submission_insert;
CREATE TRIGGER manual_nonce_no_submission_insert BEFORE INSERT ON transaction_submissions
WHEN (EXISTS(SELECT 1 FROM manual_nonce_claims WHERE hex(attempt_id)=hex(NEW.attempt_id))
 OR EXISTS(SELECT 1 FROM manual_first_submission_bindings WHERE hex(submission_id)=hex(NEW.id)))
 AND NOT EXISTS(SELECT 1 FROM manual_first_submission_bindings b
 JOIN manual_first_submission_scopes scope ON scope.permit_id=b.permit_id
 JOIN canary_send_permits p ON p.id=b.permit_id AND p.state='CONSUMED' AND p.consumed_at=b.created_at
 WHERE NEW.id IS b.submission_id AND NEW.attempt_id IS b.attempt_id AND NEW.tx_hash IS b.tx_hash
 AND typeof(NEW.sequence)='integer' AND NEW.sequence=0 AND NEW.state IS 'submitting'
 AND NEW.send_intent_at IS NULL AND NEW.rpc_hash IS NULL AND NEW.failure_class IS NULL AND NEW.failure_detail IS NULL
 AND NEW.created_at IS b.created_at AND NEW.updated_at IS b.created_at)
BEGIN SELECT RAISE(ABORT,'manual submission requires atomic first claim'); END;
CREATE TRIGGER manual_first_submission_no_replace BEFORE INSERT ON transaction_submissions
WHEN EXISTS(SELECT 1 FROM transaction_submissions s JOIN manual_first_submission_bindings b ON b.submission_id=s.id
 WHERE s.rowid=NEW.rowid OR hex(s.id)=hex(NEW.id) OR (hex(s.attempt_id)=hex(NEW.attempt_id) AND s.sequence=NEW.sequence))
BEGIN SELECT RAISE(ABORT,'manual submission cannot replace retained history'); END;
CREATE TRIGGER manual_first_submission_no_delete BEFORE DELETE ON transaction_submissions
WHEN EXISTS(SELECT 1 FROM manual_first_submission_bindings b WHERE b.submission_id=OLD.id)
BEGIN SELECT RAISE(ABORT,'manual submission retained'); END;

CREATE TRIGGER manual_first_submission_claim_effect AFTER INSERT ON manual_first_submission_bindings
BEGIN
 INSERT INTO manual_first_broadcast_consume_scopes(permit_id,operation_id,attempt_id,authorization_id,authorization_epoch,artifact_hash,opened_at)
 VALUES(NEW.permit_id,NEW.operation_id,NEW.attempt_id,NEW.authorization_id,NEW.authorization_epoch,NEW.artifact_hash,NEW.created_at);
 UPDATE canary_send_permits SET state='CONSUMED',consumed_at=NEW.created_at WHERE id=NEW.permit_id AND state='ISSUED';
 INSERT INTO transaction_submissions(id,attempt_id,tx_hash,sequence,state,created_at,updated_at)
 VALUES(NEW.submission_id,NEW.attempt_id,NEW.tx_hash,0,'submitting',NEW.created_at,NEW.created_at);
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 VALUES('manual-first-submission-claim:'||NEW.permit_id,'MANUAL_FIRST_SUBMISSION_CLAIMED',NEW.operation_id,NEW.attempt_id,NEW.authorization_id,NEW.authorization_epoch,
 'CLAIMED_NOT_SEND_INTENT',json_object('permit_id',NEW.permit_id,'submission_id',NEW.submission_id,'artifact_hash',NEW.artifact_hash,'tx_hash',NEW.tx_hash,'foundation_hash',NEW.foundation_hash),NEW.created_at);
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-first-submission-claim-alert:'||NEW.permit_id,'manual-first-submission-claim:'||NEW.permit_id,'INFO','PENDING',NEW.created_at);
 DELETE FROM manual_first_submission_scopes WHERE permit_id=NEW.permit_id;
END;
CREATE TRIGGER manual_first_submission_scope_close BEFORE DELETE ON manual_first_submission_scopes
WHEN NOT EXISTS(SELECT 1 FROM manual_first_submission_bindings b
 JOIN canary_send_permits p ON p.id=b.permit_id AND p.state='CONSUMED' AND p.consumed_at=b.created_at
 JOIN transaction_submissions s ON s.id=b.submission_id AND s.attempt_id=b.attempt_id AND s.tx_hash=b.tx_hash
 AND s.sequence=0 AND s.state='submitting' AND s.send_intent_at IS NULL AND s.created_at=b.created_at AND s.updated_at=b.created_at
 JOIN canary_runtime_audit a ON a.id='manual-first-submission-claim:'||b.permit_id
 AND a.event_type='MANUAL_FIRST_SUBMISSION_CLAIMED' AND a.operation_id=b.operation_id AND a.attempt_id=b.attempt_id
 AND a.authorization_id=b.authorization_id AND a.authorization_epoch=b.authorization_epoch
 AND a.reason_code='CLAIMED_NOT_SEND_INTENT' AND a.created_at=b.created_at
 AND a.details_json=json_object('permit_id',b.permit_id,'submission_id',b.submission_id,'artifact_hash',b.artifact_hash,'tx_hash',b.tx_hash,'foundation_hash',b.foundation_hash)
 JOIN canary_alert_outbox o ON o.id='manual-first-submission-claim-alert:'||b.permit_id AND o.audit_id=a.id AND o.severity='INFO' AND o.created_at=b.created_at
 WHERE b.permit_id=OLD.permit_id)
BEGIN SELECT RAISE(ABORT,'manual first submission claim incomplete'); END;

-- Separate post-claim projection. Original 036..043 signed/prebroadcast views
-- remain unchanged and cannot grant new claims after a submission exists.
CREATE VIEW manual_first_submission_artifact_claim_base AS
SELECT q.* FROM manual_nonce_claims q
JOIN manual_signed_artifact_commands m ON m.operation_id=q.operation_id
JOIN manual_execution_bindings b ON b.operation_id=q.operation_id AND b.execution_step_id=q.execution_step_id
 AND b.wallet_id=q.wallet_id AND lower(b.wallet_address)=q.wallet_address AND b.contract_hash=q.contract_hash
JOIN manual_admission_complete c ON c.operation_id=b.operation_id AND c.request_id=b.request_id AND c.step_id=b.validation_step_id
 AND q.reservation_id=c.reservation_id AND b.execution_step_id='manual-exec:'||c.operation_id
 AND b.wallet_id=json_extract(c.contract_json,'$.wallet_id') AND b.wallet_address=json_extract(c.contract_json,'$.wallet_address')
 AND b.chain_id=json_extract(c.contract_json,'$.chain_id') AND b.contract_hash=c.contract_hash
 AND b.policy_version=json_extract(c.contract_json,'$.policy_version') AND b.expires_at=json_extract(c.contract_json,'$.expires_at')
 AND b.quote_block_number=json_extract(c.contract_json,'$.output.resolution.token.block.number')
 AND b.quote_block_hash=json_extract(c.contract_json,'$.output.resolution.token.block.hash')
 AND b.authorization_id=c.authorization_id AND b.authorization_epoch=c.authorization_epoch
 AND b.manual_policy_id=c.manual_policy_id AND b.manual_policy_hash=c.manual_policy_hash
 AND b.gate_evidence_set_hash=c.gate_evidence_set_hash AND b.environment=c.environment
 AND b.build_sha=c.build_sha AND b.release_id=c.release_id AND b.deployment_id=c.deployment_id
JOIN manual_nonce_binding_facts f ON f.operation_id=q.operation_id
 AND manual_nonce_observation_valid(q.observation_json,q.observation_hash,q.nonce,f.facts_json,q.checked_at)=1
JOIN execution_steps s ON s.id=q.execution_step_id AND s.operation_id=q.operation_id AND s.step_index=1
 AND s.kind='manual_trade_execution' AND s.wallet_id=q.wallet_id AND s.status='signed'
 AND s.parameters_json=c.contract_json AND s.route_json IS NULL AND s.unsigned_call_json IS NULL
 AND s.created_at=b.checked_at AND s.updated_at=m.checked_at AND s.policy_version=b.policy_version
 AND s.quote_block_number=b.quote_block_number AND s.quote_block_hash=b.quote_block_hash AND s.expires_at=b.expires_at
JOIN execution_wallet_lanes l ON l.wallet_id=q.wallet_id AND lower(l.address)=q.wallet_address
 AND l.operation_id=q.operation_id AND l.step_id=q.execution_step_id AND l.state='signed'
 AND l.reserved_nonce=q.nonce AND l.freeze_reason IS NULL AND l.updated_at=m.checked_at
JOIN manual_artifact_expected_attempt e ON e.operation_id=q.operation_id
JOIN manual_nonce_actual_attempt t ON t.id=q.attempt_id AND t.attempt_json=e.attempt_json
JOIN canary_runtime_audit old ON old.id='manual-execution:'||b.operation_id AND old.event_type='MANUAL_EXECUTION_BOUND'
 AND old.operation_id=b.operation_id AND old.attempt_id IS NULL AND old.authorization_id=b.authorization_id
 AND old.authorization_epoch=b.authorization_epoch AND old.reason_code='BOUND_NOT_SIGNED_NOT_PERMISSION' AND old.created_at=b.checked_at
 AND old.details_json=json_object('request_id',b.request_id,'execution_step_id',b.execution_step_id,'contract_hash',b.contract_hash,'reservation_id',c.reservation_id)
JOIN canary_alert_outbox oldalert ON oldalert.id='manual-execution-alert:'||b.operation_id AND oldalert.audit_id=old.id
 AND oldalert.severity='INFO' AND oldalert.created_at=b.checked_at
JOIN canary_runtime_audit a ON a.id='manual-nonce-audit:'||q.operation_id AND a.event_type='MANUAL_NONCE_CLAIMED'
 AND a.operation_id=q.operation_id AND a.attempt_id=q.attempt_id AND a.authorization_id=b.authorization_id
 AND a.authorization_epoch=b.authorization_epoch AND a.reason_code='NONCE_CLAIMED_NOT_SIGNED_NOT_PERMISSION' AND a.created_at=q.checked_at
 AND a.details_json=json_object('execution_step_id',q.execution_step_id,'nonce',q.nonce,'contract_hash',q.contract_hash,
 'reservation_id',q.reservation_id,'observation_hash',q.observation_hash)
JOIN canary_alert_outbox alert ON alert.id='manual-nonce-alert:'||q.operation_id AND alert.audit_id=a.id AND alert.severity='INFO' AND alert.created_at=q.checked_at
WHERE q.attempt_id='manual-nonce:'||q.operation_id
 AND manual_time_cmp(b.checked_at,c.checked_at)>=0 AND manual_time_cmp(b.checked_at,b.expires_at)<0
 AND NOT EXISTS(SELECT 1 FROM execution_reservations r WHERE r.operation_id=b.operation_id OR r.step_id IN (b.execution_step_id,b.validation_step_id)
 OR EXISTS(SELECT 1 FROM execution_steps owned WHERE owned.id=r.step_id AND owned.operation_id=b.operation_id))
 AND NOT EXISTS(SELECT 1 FROM execution_wallet_lanes extra WHERE
 (extra.operation_id=b.operation_id OR extra.step_id IN (b.execution_step_id,b.validation_step_id)
 OR extra.wallet_id=b.wallet_id OR lower(extra.address)=lower(b.wallet_address)
 OR EXISTS(SELECT 1 FROM execution_steps owned WHERE owned.id=extra.step_id AND owned.operation_id=b.operation_id)) AND extra.rowid!=l.rowid)
 AND NOT EXISTS(SELECT 1 FROM transaction_attempts extra JOIN execution_steps owned ON owned.id=extra.step_id
 WHERE (owned.operation_id=q.operation_id OR extra.id=q.attempt_id
 OR ((extra.wallet_id=q.wallet_id OR lower(extra.from_address)=q.wallet_address OR owned.wallet_id=q.wallet_id
 OR EXISTS(SELECT 1 FROM operations owner WHERE owner.id=owned.operation_id AND owner.wallet_id=q.wallet_id))
 AND extra.nonce=q.nonce)) AND extra.rowid!=t.rowid)
 AND EXISTS(SELECT 1 FROM manual_first_submission_bindings link JOIN transaction_submissions sub
 ON sub.id=link.submission_id AND sub.attempt_id=link.attempt_id AND sub.tx_hash=link.tx_hash AND sub.sequence=0
 WHERE link.operation_id=q.operation_id AND link.attempt_id=q.attempt_id AND sub.state='submitting'
 AND NOT EXISTS(SELECT 1 FROM transaction_submissions extra WHERE hex(extra.attempt_id)=hex(q.attempt_id) AND extra.rowid!=sub.rowid))
 AND NOT EXISTS(SELECT 1 FROM receipt_observations WHERE attempt_id=q.attempt_id)
 AND NOT EXISTS(SELECT 1 FROM position_effects WHERE attempt_id=q.attempt_id)
 AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit extra WHERE extra.operation_id=b.operation_id
 AND ((extra.event_type='MANUAL_EXECUTION_BOUND' AND extra.id IS NOT old.id) OR (extra.event_type='MANUAL_NONCE_CLAIMED' AND extra.id IS NOT a.id)));

CREATE VIEW manual_first_submission_artifact_claim_complete AS
SELECT q.* FROM (SELECT * FROM manual_first_submission_artifact_claim_base LIMIT -1) q
JOIN manual_nonce_binding_facts f ON f.operation_id=q.operation_id
JOIN manual_execution_bindings b ON b.operation_id=q.operation_id
JOIN canary_risk_reservations r ON r.id=q.reservation_id AND r.operation_id=q.operation_id AND r.wallet_id=q.wallet_id
JOIN canary_authorization_operation_usage u ON u.operation_id=q.operation_id AND u.authorization_id=b.authorization_id
WHERE manual_nonce_observation_bytes_valid(hex(q.observation_json),typeof(q.observation_json),hex(q.observation_hash),typeof(q.observation_hash),
 hex(q.nonce),typeof(q.nonce),hex(f.facts_json),typeof(f.facts_json),hex(q.checked_at),typeof(q.checked_at))=1
 AND typeof(r.state)='text' AND r.state IN ('reserved','frozen') AND typeof(u.state)='text' AND u.state IN ('RESERVED','FROZEN')
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x WHERE x.operation_id=q.operation_id AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce));

CREATE VIEW manual_first_submission_artifact_marker_base AS
SELECT i.* FROM manual_signing_intents i JOIN manual_first_submission_artifact_claim_complete q ON q.operation_id=i.operation_id
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

CREATE VIEW manual_first_submission_artifact_marker_complete AS
SELECT i.* FROM (SELECT * FROM manual_first_submission_artifact_marker_base LIMIT -1) i
JOIN canary_alert_outbox canonical ON canonical.id='manual-sign-intent-alert:'||i.operation_id
WHERE length(i.owner_id)=64
 AND NOT EXISTS(SELECT 1 FROM manual_signing_alert_witnesses w WHERE hex(w.operation_id)=hex(i.operation_id) AND w.witness_rowid!=canonical.rowid)
 AND NOT EXISTS(SELECT 1 FROM manual_signing_witness_creation_scopes s WHERE hex(s.operation_id)=hex(i.operation_id));

CREATE VIEW manual_first_submission_signed_artifact_preclose AS
SELECT m.* FROM manual_signed_artifact_commands m JOIN (SELECT * FROM manual_first_submission_artifact_marker_complete LIMIT -1) i
 ON i.operation_id=m.operation_id AND i.owner_id IS m.owner_id AND i.facts_hash IS m.facts_hash AND hex(i.facts_json)=hex(m.facts_json)
JOIN manual_nonce_claims q ON q.operation_id=m.operation_id
JOIN manual_nonce_binding_facts nf ON nf.operation_id=m.operation_id
JOIN manual_execution_bindings b ON b.operation_id=m.operation_id
JOIN manual_artifact_expected_step es ON es.operation_id=m.operation_id
JOIN manual_artifact_actual_step actual_s ON actual_s.id=es.id AND actual_s.step_json=es.step_json
JOIN manual_artifact_expected_lane el ON el.operation_id=m.operation_id
JOIN manual_artifact_actual_lane actual_l ON actual_l.wallet_id=el.wallet_id AND actual_l.lane_json=el.lane_json
JOIN canary_runtime_audit a ON a.id='manual-artifact:'||m.operation_id AND a.event_type='MANUAL_ARTIFACT_STORED'
 AND a.operation_id=m.operation_id AND a.attempt_id=q.attempt_id AND a.authorization_id=b.authorization_id AND a.authorization_epoch=b.authorization_epoch
 AND a.reason_code='ENCRYPTED_ARTIFACT_NOT_PERMISSION' AND a.created_at=m.checked_at
 AND a.details_json=json_object('owner_id',m.owner_id,'facts_hash',m.facts_hash,'envelope_hash',manual_artifact_bytes_sha256(hex(m.envelope_json)),
 'execution_step_id',q.execution_step_id,'observation_hash',m.observation_hash,'nonce',q.nonce)
JOIN canary_alert_outbox alert ON alert.id='manual-artifact-alert:'||m.operation_id AND alert.audit_id=a.id AND alert.severity='INFO' AND alert.created_at=m.checked_at
WHERE manual_artifact_envelope_valid(hex(m.facts_json),typeof(m.facts_json),hex(m.envelope_json),typeof(m.envelope_json))=1
 AND EXISTS(SELECT 1 FROM transaction_attempts bytes WHERE bytes.id=q.attempt_id
 AND typeof(bytes.encrypted_raw_tx)='blob' AND typeof(bytes.encryption_nonce)='blob')
 AND json_extract(m.envelope_json,'$.completed_at') IS m.checked_at
 AND manual_time_cmp(m.checked_at,i.checked_at)>=0
 AND manual_nonce_observation_bytes_valid(hex(m.observation_json),typeof(m.observation_json),hex(m.observation_hash),typeof(m.observation_hash),
 hex(q.nonce),typeof(q.nonce),hex(nf.facts_json),typeof(nf.facts_json),hex(m.checked_at),typeof(m.checked_at))=1
 AND NOT EXISTS(SELECT 1 FROM manual_signed_artifact_commands extra WHERE extra.rowid!=m.rowid
 AND (hex(extra.operation_id)=hex(m.operation_id) OR hex(CASE WHEN json_valid(extra.facts_json) THEN json_extract(extra.facts_json,'$.operation_id') END)=hex(m.operation_id)))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_audit_witnesses w WHERE hex(w.operation_id)=hex(m.operation_id) AND w.witness_rowid!=a.rowid)
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_alert_witnesses w WHERE hex(w.operation_id)=hex(m.operation_id) AND w.witness_rowid!=alert.rowid);

CREATE VIEW manual_first_submission_signed_artifact_complete AS
SELECT m.* FROM manual_first_submission_signed_artifact_preclose m
WHERE NOT EXISTS(SELECT 1 FROM manual_artifact_effect_scopes x WHERE hex(x.operation_id)=hex(m.operation_id));

CREATE VIEW manual_first_submission_submission_foundation_expected AS
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
FROM (SELECT * FROM manual_first_submission_signed_artifact_complete LIMIT -1) m
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

CREATE VIEW manual_first_submission_submission_foundation_preclose AS
SELECT f.* FROM manual_submission_foundations f
JOIN manual_first_submission_submission_foundation_expected e ON e.operation_id IS f.operation_id AND hex(e.binding_json)=hex(f.binding_json)
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

CREATE VIEW manual_first_submission_submission_foundation_complete AS
SELECT f.* FROM manual_first_submission_submission_foundation_preclose f
WHERE NOT EXISTS(SELECT 1 FROM manual_submission_foundation_scopes x WHERE hex(x.operation_id)=hex(f.operation_id));

CREATE VIEW manual_first_submission_gate_evidence_expected AS
SELECT f.operation_id,f.created_at AS foundation_created_at,
 json_extract(f.binding_json,'$.artifact_completed_at') AS artifact_completed_at,
 ctl.effective_at AS control_effective_at,ctl.updated_at AS control_updated_at,
 a.authorized_at,a.created_at AS auth_created_at,a.updated_at AS auth_updated_at,a.expires_at AS auth_expires_at,
 b.expires_at AS operation_expires_at,l.updated_at AS lease_updated_at,l.expires_at AS lease_expires_at,
 p.created_at AS policy_created_at,
 json_object('version',1,'stage','FIRST_BROADCAST_DB_EVIDENCE','status','DB_EVIDENCE_NOT_PERMISSION',
 'foundation',json(f.binding_json),'foundation_hash',f.binding_hash,
 'control',json_object('chain_id',ctl.chain_id,'mode',ctl.mode,'policy_version',ctl.policy_version,
 'emergency_stopped',ctl.emergency_stopped,'effective_at',ctl.effective_at,'updated_at',ctl.updated_at),
 'authorization',json_object('id',a.id,'epoch',a.epoch,'state',a.state,'environment',a.environment,'wallet_id',a.wallet_id,
 'policy_version',a.policy_version,'build_sha',a.build_sha,'release_id',a.release_id,'deployment_id',a.deployment_id,
 'gate_evidence_set_hash',a.gate_evidence_set_hash,'max_operations',a.max_operations,'max_total_input',a.max_total_input,
 'authorized_at',a.authorized_at,'created_at',a.created_at,'updated_at',a.updated_at,'expires_at',a.expires_at),
 'policy',json_object('manual_policy_id',p.id,'manual_policy_hash',p.policy_hash,'source_policy_version',p.source_policy_version,
 'source_policy_hash',p.source_policy_hash,'created_at',p.created_at),
 'allowlists',json_object('wallet_address',wa.address,'wallet_enabled',wa.enabled,'wallet_created_at',wa.created_at,
 'contract_address',ca.address,'runtime_code_hash',ca.runtime_code_hash,'baseline_ref',ca.baseline_ref,'contract_created_at',ca.created_at,
 'token_address',ta.token_address,'token_created_at',ta.created_at),
 'reservation_state',r.state,'operation_usage_state',u.state,
 'aggregate_usage',json_object('operation_count',usage.operation_count,'total_input',usage.total_input),
 'submission_lease',json_object('role',l.role,'environment',l.environment,'holder_id',l.holder_id,
 'epoch',l.lease_epoch,'updated_at',l.updated_at,'expires_at',l.expires_at),
 'execution_authorized',json('false'),'signing_authorized',json('false'),'send_authorized',json('false'),'replay_authorized',json('false'),
 'trusted_clock_verified',json('false'),'running_deployment_verified',json('false'),
 'RPC_runtime_verified',json('false'),'cryptographic_artifact_verified',json('false'),'fresh_gate_required',json('true')) AS source_json
FROM (SELECT * FROM manual_first_submission_submission_foundation_complete LIMIT -1) f
JOIN manual_execution_bindings b ON b.operation_id=f.operation_id
JOIN manual_admission_commands c ON c.operation_id=b.operation_id
JOIN canary_control_state ctl ON ctl.singleton=1 AND ctl.chain_id=4663 AND ctl.mode='CONTROLLED_CANARY'
 AND ctl.emergency_stopped IS 0 AND ctl.policy_version=b.policy_version
JOIN canary_runtime_authorizations a ON a.id=b.authorization_id AND a.epoch=b.authorization_epoch AND a.state='ARMED'
 AND a.mode='CONTROLLED_CANARY' AND a.chain_id=4663 AND a.wallet_id=b.wallet_id AND a.policy_version=b.policy_version
 AND a.environment=b.environment AND a.build_sha=b.build_sha AND a.release_id=b.release_id AND a.deployment_id=b.deployment_id
 AND a.gate_evidence_set_hash=b.gate_evidence_set_hash
JOIN manual_risk_policies p ON p.id=b.manual_policy_id AND p.policy_hash=b.manual_policy_hash
 AND p.source_policy_version=b.policy_version AND p.source_policy_hash=json_extract(c.contract_json,'$.policy_hash')
JOIN canary_risk_reservations r ON r.id=c.reservation_id AND r.operation_id=b.operation_id AND r.wallet_id=b.wallet_id AND r.state='reserved'
JOIN canary_authorization_operation_usage u ON u.authorization_id=a.id AND u.operation_id=b.operation_id AND u.state='RESERVED'
JOIN canary_authorization_usage usage ON usage.authorization_id=a.id
JOIN dry_run_wallets w ON w.id=b.wallet_id AND w.chain_id=4663 AND w.enabled=1 AND lower(w.address)=b.wallet_address
JOIN canary_wallet_allowlist wa ON wa.wallet_id=b.wallet_id AND wa.chain_id=4663 AND wa.policy_version=b.policy_version AND wa.enabled=1 AND lower(wa.address)=b.wallet_address
JOIN canary_contract_allowlist ca ON ca.policy_version=b.policy_version AND ca.protocol='PONS_V2_CURVE' AND ca.role='CURVE' AND ca.enabled=1 AND ca.baseline_ref!=''
 AND lower(ca.address)=json_extract(c.contract_json,'$.output.resolution.token.curve') AND ca.runtime_code_hash=json_extract(c.contract_json,'$.output.resolution.token.curve_runtime_code_hash')
JOIN canary_token_allowlist ta ON ta.policy_version=b.policy_version AND ta.protocol='PONS_V2_CURVE' AND ta.enabled=1
 AND lower(ta.token_address)=json_extract(c.contract_json,'$.output.resolution.token.address')
JOIN canary_worker_leases l ON l.role='SUBMISSION' AND l.environment=b.environment
WHERE typeof(l.role)='text' AND typeof(l.environment)='text' AND typeof(l.holder_id)='text'
 AND length(CAST(l.holder_id AS BLOB)) BETWEEN 1 AND 128 AND length(l.holder_id)=length(CAST(l.holder_id AS BLOB))
 AND l.holder_id NOT GLOB '*[^A-Za-z0-9._:-]*' AND typeof(l.lease_epoch)='integer' AND l.lease_epoch>0
 AND (SELECT COUNT(*) FROM canary_worker_leases x WHERE hex(x.role)=hex(l.role) AND hex(x.environment)=hex(l.environment))=1
 AND usage.operation_count=(SELECT COUNT(*) FROM canary_authorization_operation_usage WHERE authorization_id=a.id)
 AND usage.operation_count=(SELECT COUNT(*) FROM manual_admission_complete WHERE authorization_id=a.id)
 AND usage.total_input=(SELECT manual_u256_sum_json(json_group_array(amount)) FROM canary_authorization_operation_usage WHERE authorization_id=a.id)
 AND usage.operation_count<=a.max_operations AND manual_u256_cmp(usage.total_input,a.max_total_input)<=0
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_unresolved_wallet_steps WHERE operation_id=b.operation_id)
 AND NOT EXISTS(SELECT 1 FROM execution_reservations x WHERE manual_nonce_identity_relation(hex(x.wallet_id),typeof(x.wallet_id),hex(b.wallet_id),0)>0 AND x.status IN ('reserved','signed','frozen'))
 AND (SELECT COUNT(*) FROM canary_gate_attestations g WHERE g.status='PASS'
 AND ((g.gate='C04' AND g.evidence_hash='c1af9454a6a4433b488158c25224d13dfbf31178c71dae7468c80917743381fd')
 OR (g.gate='C05' AND g.evidence_hash='1a793f13d9304a31ed405f9fe25f1713cf79b6f8d28bb3da8ad16a01dc24c40b')
 OR (g.gate='C08' AND g.evidence_hash='8178e7b976dbea67be1de83cec529171e56d154cd2114f1bf290bebbd1fc77d8')))=3
 AND b.gate_evidence_set_hash=(SELECT manual_artifact_bytes_sha256(hex(json_group_array(json_object('Gate',gate,'Status',status,'EvidenceHash',evidence_hash,'AttestedAt',attested_at))))
 FROM (SELECT gate,status,evidence_hash,attested_at FROM canary_gate_attestations WHERE gate IN ('C04','C05','C08') ORDER BY gate));

CREATE VIEW manual_first_submission_gate_evidence_valid_source AS
SELECT e.* FROM manual_first_submission_gate_evidence_expected e
WHERE NOT EXISTS(SELECT 1 FROM json_each(json_array(e.foundation_created_at,e.artifact_completed_at,e.control_effective_at,e.control_updated_at,
 e.authorized_at,e.auth_created_at,e.auth_updated_at,e.auth_expires_at,e.operation_expires_at,e.lease_updated_at,e.lease_expires_at,e.policy_created_at,
 json_extract(e.source_json,'$.allowlists.wallet_created_at'),json_extract(e.source_json,'$.allowlists.contract_created_at'),json_extract(e.source_json,'$.allowlists.token_created_at'))) t
 WHERE manual_artifact_time_valid(hex(t.value),typeof(t.value))!=1)
 AND manual_time_cmp(e.control_updated_at,e.control_effective_at)>=0
 AND manual_time_cmp(e.auth_updated_at,e.auth_created_at)>=0
 AND manual_time_cmp(e.auth_expires_at,e.authorized_at)>0
 AND manual_time_cmp(e.lease_expires_at,e.lease_updated_at)>0;

CREATE VIEW manual_first_submission_phase_source AS
SELECT b.*, sub.send_intent_at, e.source_json, e.auth_expires_at, e.operation_expires_at,e.lease_expires_at,
 e.foundation_created_at,e.artifact_completed_at,e.control_effective_at,e.control_updated_at,
 e.authorized_at,e.auth_created_at,e.auth_updated_at,e.lease_updated_at,e.policy_created_at,p.expires_at AS permit_expires_at,
 clock.high_water_at,clock.fault_latched,clock.generation
FROM manual_first_submission_bindings b
JOIN transaction_submissions sub ON sub.id=b.submission_id AND sub.attempt_id=b.attempt_id AND sub.tx_hash=b.tx_hash
 AND sub.sequence=0 AND sub.state='submitting'
JOIN canary_send_permits p ON p.id=b.permit_id AND p.state='CONSUMED' AND p.consumed_at=b.created_at
JOIN manual_first_submission_gate_evidence_valid_source e ON e.operation_id=b.operation_id
JOIN manual_first_broadcast_issuances i ON i.id=b.permit_id
 AND json_extract(i.source_json,'$.foundation')=json_extract(e.source_json,'$.foundation')
JOIN manual_gate_clock_state clock ON clock.environment=b.environment AND clock.chain_id=4663
 AND clock.fault_latched=0 AND clock.generation>=i.clock_generation
WHERE NOT EXISTS(SELECT 1 FROM manual_first_submission_scopes scope WHERE scope.permit_id=b.permit_id);

-- Append-only transition witnesses drive the shared transaction_submissions row.
-- They do not own a second economic state, and are uncommittable mid-effect.
CREATE TABLE manual_first_submission_transitions(
 id TEXT NOT NULL PRIMARY KEY,
 submission_id TEXT NOT NULL REFERENCES transaction_submissions(id),
 kind TEXT NOT NULL CHECK(kind IN ('INTENT','OUTCOME')),
 result_state TEXT NOT NULL CHECK(result_state IN ('submitting','submitted','broadcast_unknown','manual_resolution','known_unsent')),
 rpc_hash TEXT, failure_class TEXT, failure_detail TEXT,
 environment TEXT NOT NULL, holder_id TEXT NOT NULL, lease_epoch INTEGER NOT NULL,
 checked_at TEXT NOT NULL,
 UNIQUE(submission_id,kind)
);
CREATE TABLE manual_first_submission_transition_scopes(
 id TEXT NOT NULL PRIMARY KEY REFERENCES manual_first_submission_transitions(id) DEFERRABLE INITIALLY DEFERRED,
 empty_ref INTEGER NOT NULL DEFAULT 1 REFERENCES manual_first_submission_empty(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER manual_first_submission_transition_source BEFORE INSERT ON manual_first_submission_transitions
WHEN typeof(NEW.id)!='text' OR NEW.id IS NOT NEW.submission_id||':'||NEW.kind
 OR manual_artifact_time_valid(hex(NEW.checked_at),typeof(NEW.checked_at))!=1
 OR EXISTS(SELECT 1 FROM manual_first_submission_transitions c WHERE c.rowid=NEW.rowid OR hex(c.id)=hex(NEW.id)
 OR (hex(c.submission_id)=hex(NEW.submission_id) AND c.kind=NEW.kind))
 OR NOT EXISTS(SELECT 1 FROM manual_first_submission_bindings b JOIN transaction_submissions s ON s.id=b.submission_id
 JOIN canary_worker_leases l ON l.role='SUBMISSION' AND l.environment=b.environment
 WHERE b.submission_id IS NEW.submission_id AND s.attempt_id=b.attempt_id AND s.tx_hash=b.tx_hash AND s.sequence=0 AND s.state='submitting'
 AND l.environment IS NEW.environment AND l.holder_id IS NEW.holder_id AND l.lease_epoch IS NEW.lease_epoch
 AND manual_time_cmp(NEW.checked_at,l.expires_at)<0 AND manual_time_cmp(NEW.checked_at,l.updated_at)>=0
 AND manual_time_cmp(NEW.checked_at,b.created_at)>=0
 AND (s.send_intent_at IS NULL OR manual_time_cmp(NEW.checked_at,s.send_intent_at)>=0))
 OR (NEW.kind='INTENT' AND (NEW.result_state!='submitting' OR NEW.rpc_hash IS NOT NULL OR NEW.failure_class IS NOT NULL OR NEW.failure_detail IS NOT NULL
 OR NOT EXISTS(SELECT 1 FROM manual_first_submission_phase_source p WHERE p.submission_id=NEW.submission_id AND p.send_intent_at IS NULL
 AND p.environment=NEW.environment AND p.holder_id=NEW.holder_id AND p.lease_epoch=NEW.lease_epoch
 AND manual_time_cmp(NEW.checked_at,p.permit_expires_at)<0 AND manual_time_cmp(NEW.checked_at,p.auth_expires_at)<0
 AND manual_time_cmp(NEW.checked_at,p.operation_expires_at)<0 AND manual_time_cmp(NEW.checked_at,p.lease_expires_at)<0
 AND manual_time_cmp(NEW.checked_at,p.high_water_at)>=0)))
 OR (NEW.kind='OUTCOME' AND (NEW.result_state='submitting'
 OR (NEW.result_state='submitted' AND (NEW.rpc_hash IS NOT (SELECT tx_hash FROM manual_first_submission_bindings WHERE submission_id=NEW.submission_id)
 OR NEW.failure_class IS NOT NULL OR NEW.failure_detail IS NOT NULL))
 OR (NEW.result_state!='submitted' AND (NEW.failure_class IS NULL OR NEW.failure_class=''))
 OR (NEW.result_state IN ('broadcast_unknown','known_unsent') AND NEW.rpc_hash IS NOT NULL)
 OR (NEW.result_state!='known_unsent' AND NOT EXISTS(SELECT 1 FROM transaction_submissions WHERE id=NEW.submission_id AND send_intent_at IS NOT NULL))))
BEGIN SELECT RAISE(ABORT,'manual submission transition requires exact phase and lease'); END;
CREATE TRIGGER manual_first_submission_transition_update BEFORE UPDATE ON manual_first_submission_transitions
BEGIN SELECT RAISE(ABORT,'manual submission transition immutable'); END;
CREATE TRIGGER manual_first_submission_transition_delete BEFORE DELETE ON manual_first_submission_transitions
BEGIN SELECT RAISE(ABORT,'manual submission transition retained'); END;
CREATE TRIGGER manual_first_submission_transition_scope_insert BEFORE INSERT ON manual_first_submission_transition_scopes
WHEN EXISTS(SELECT 1 FROM manual_first_submission_transitions c WHERE hex(c.id)=hex(NEW.id))
 OR EXISTS(SELECT 1 FROM manual_first_submission_transition_scopes c WHERE hex(c.id)=hex(NEW.id) OR c.rowid=NEW.rowid)
BEGIN SELECT RAISE(ABORT,'manual transition scope cannot reopen'); END;
CREATE TRIGGER manual_first_submission_transition_scope_update BEFORE UPDATE ON manual_first_submission_transition_scopes
BEGIN SELECT RAISE(ABORT,'manual transition scope immutable'); END;
CREATE TRIGGER manual_first_submission_transition_open BEFORE INSERT ON manual_first_submission_transitions
BEGIN INSERT INTO manual_first_submission_transition_scopes(id) VALUES(NEW.id); END;

DROP TRIGGER manual_nonce_no_submission_update;
CREATE TRIGGER manual_nonce_no_submission_update BEFORE UPDATE ON transaction_submissions
WHEN (EXISTS(SELECT 1 FROM manual_nonce_claims WHERE hex(attempt_id) IN (hex(OLD.attempt_id),hex(NEW.attempt_id)))
 OR EXISTS(SELECT 1 FROM manual_first_submission_bindings WHERE hex(submission_id) IN (hex(OLD.id),hex(NEW.id))))
 AND NOT EXISTS(SELECT 1 FROM manual_first_submission_transitions c JOIN manual_first_submission_transition_scopes scope ON scope.id=c.id
 JOIN manual_first_submission_bindings b ON b.submission_id=c.submission_id
 WHERE b.submission_id IS OLD.id AND NEW.id IS OLD.id AND NEW.rowid=OLD.rowid
 AND OLD.attempt_id IS b.attempt_id AND NEW.attempt_id IS OLD.attempt_id AND OLD.tx_hash IS b.tx_hash AND NEW.tx_hash IS OLD.tx_hash
 AND OLD.sequence=0 AND NEW.sequence IS OLD.sequence AND OLD.state='submitting' AND NEW.created_at IS OLD.created_at
 AND ((c.kind='INTENT' AND OLD.send_intent_at IS NULL AND NEW.send_intent_at IS c.checked_at AND NEW.state IS OLD.state
 AND NEW.rpc_hash IS OLD.rpc_hash AND NEW.failure_class IS OLD.failure_class AND NEW.failure_detail IS OLD.failure_detail AND NEW.updated_at IS OLD.updated_at)
 OR (c.kind='OUTCOME' AND NEW.send_intent_at IS OLD.send_intent_at AND NEW.state IS c.result_state
 AND NEW.rpc_hash IS c.rpc_hash AND NEW.failure_class IS c.failure_class AND NEW.failure_detail IS c.failure_detail AND NEW.updated_at IS c.checked_at)))
BEGIN SELECT RAISE(ABORT,'manual submission update requires exact transition'); END;

-- Known-unsent deliberately retains the signed nonce and typed accounting.
-- A future separately authorized release may prove and enact safe release.
CREATE VIEW manual_first_submission_lane_transition AS
SELECT l.wallet_id,l.rowid,
 json_set(l.lane_json,'$.state','frozen','$.freeze_reason',
 CASE WHEN c.result_state='known_unsent' THEN 'known_unsent_retained' ELSE c.result_state END,'$.updated_at',c.checked_at) AS lane_json
FROM manual_first_submission_transitions c JOIN manual_first_submission_transition_scopes scope ON scope.id=c.id
JOIN manual_first_submission_bindings b ON b.submission_id=c.submission_id
JOIN manual_nonce_claims q ON q.operation_id=b.operation_id
JOIN manual_artifact_actual_lane l ON l.wallet_id=q.wallet_id
JOIN manual_artifact_expected_lane expected ON expected.operation_id=b.operation_id AND expected.lane_json=l.lane_json
WHERE c.kind='OUTCOME' AND c.result_state!='submitted';

DROP TRIGGER manual_execution_lane_update;
CREATE TRIGGER manual_execution_lane_update BEFORE UPDATE ON execution_wallet_lanes
WHEN ((EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
 AND NOT EXISTS(SELECT 1 FROM manual_execution_bindings b WHERE b.operation_id=NEW.operation_id AND b.wallet_id=NEW.wallet_id
 AND b.execution_step_id=NEW.step_id AND lower(b.wallet_address)=lower(NEW.address) AND NEW.state='reserved'
 AND NEW.reserved_nonce IS NULL AND NEW.freeze_reason IS NULL AND NEW.updated_at=b.checked_at)
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_lane_transition n WHERE n.wallet_id=OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_first_submission_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual lane exact nonce transition only'); END;

DROP TRIGGER manual_execution_lane_no_update;
CREATE TRIGGER manual_execution_lane_no_update BEFORE UPDATE ON execution_wallet_lanes
WHEN ((EXISTS(SELECT 1 FROM manual_execution_bindings WHERE wallet_id=OLD.wallet_id AND execution_step_id=OLD.step_id)
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_lane_transition n WHERE n.wallet_id=OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_first_submission_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual lane downstream not connected'); END;

DROP TRIGGER manual_binding_lane_reference_update;
CREATE TRIGGER manual_binding_lane_reference_update BEFORE UPDATE ON execution_wallet_lanes
WHEN (((EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE (b.operation_id=NEW.operation_id OR b.execution_step_id=NEW.step_id OR b.validation_step_id=NEW.step_id
 OR b.wallet_id=NEW.wallet_id OR lower(b.wallet_address)=lower(NEW.address)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=NEW.step_id AND s.operation_id=b.operation_id))
 AND NOT (NEW.operation_id IS b.operation_id AND NEW.step_id IS b.execution_step_id
 AND NEW.wallet_id IS b.wallet_id AND lower(NEW.address) IS lower(b.wallet_address)
 AND NEW.state IS 'reserved' AND NEW.reserved_nonce IS NULL AND NEW.freeze_reason IS NULL AND NEW.updated_at IS b.checked_at))
 OR EXISTS(SELECT 1 FROM manual_execution_bindings b WHERE b.operation_id=OLD.operation_id
 OR b.execution_step_id=OLD.step_id OR b.validation_step_id=OLD.step_id
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=OLD.step_id AND s.operation_id=b.operation_id)))
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_lane_transition n WHERE n.wallet_id=OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_first_submission_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual lane reference mutation forbidden'); END;

DROP TRIGGER manual_binding_lane_retained_update;
CREATE TRIGGER manual_binding_lane_retained_update BEFORE UPDATE ON execution_wallet_lanes
WHEN ((EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE (OLD.operation_id=b.operation_id OR OLD.step_id IN (b.execution_step_id,b.validation_step_id)
 OR OLD.wallet_id=b.wallet_id OR lower(OLD.address)=lower(b.wallet_address)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=OLD.step_id AND s.operation_id=b.operation_id))
 AND NOT (OLD.state IS 'idle' AND OLD.operation_id IS NULL AND OLD.step_id IS NULL
 AND OLD.reserved_nonce IS NULL AND OLD.freeze_reason IS NULL
 AND OLD.wallet_id IS b.wallet_id AND OLD.wallet_id IS NEW.wallet_id
 AND lower(OLD.address) IS lower(b.wallet_address) AND lower(OLD.address) IS lower(NEW.address)
 AND NEW.operation_id IS b.operation_id AND NEW.step_id IS b.execution_step_id
 AND NEW.state IS 'reserved' AND NEW.reserved_nonce IS NULL AND NEW.freeze_reason IS NULL AND NEW.updated_at IS b.checked_at))
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_lane_transition n WHERE n.wallet_id=OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_first_submission_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual retained lane mutation forbidden'); END;

CREATE TRIGGER manual_first_submission_transition_effect AFTER INSERT ON manual_first_submission_transitions
BEGIN
 UPDATE transaction_submissions SET send_intent_at=NEW.checked_at WHERE id=NEW.submission_id AND NEW.kind='INTENT';
 UPDATE transaction_submissions SET state=NEW.result_state,rpc_hash=NEW.rpc_hash,failure_class=NEW.failure_class,failure_detail=NEW.failure_detail,updated_at=NEW.checked_at
 WHERE id=NEW.submission_id AND NEW.kind='OUTCOME';
 UPDATE execution_wallet_lanes SET state='frozen',freeze_reason=CASE WHEN NEW.result_state='known_unsent' THEN 'known_unsent_retained' ELSE NEW.result_state END,updated_at=NEW.checked_at
 WHERE operation_id=(SELECT operation_id FROM manual_first_submission_bindings WHERE submission_id=NEW.submission_id)
 AND NEW.kind='OUTCOME' AND NEW.result_state!='submitted';
 UPDATE canary_risk_reservations SET state='frozen',updated_at=NEW.checked_at
 WHERE operation_id=(SELECT operation_id FROM manual_first_submission_bindings WHERE submission_id=NEW.submission_id)
 AND NEW.kind='OUTCOME' AND NEW.result_state!='submitted' AND state='reserved';
 UPDATE canary_authorization_operation_usage SET state='FROZEN',updated_at=NEW.checked_at
 WHERE operation_id=(SELECT operation_id FROM manual_first_submission_bindings WHERE submission_id=NEW.submission_id)
 AND NEW.kind='OUTCOME' AND NEW.result_state!='submitted' AND state='RESERVED';
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'manual-first-submission-transition:'||NEW.id,'MANUAL_FIRST_SUBMISSION_'||NEW.kind,b.operation_id,b.attempt_id,b.authorization_id,b.authorization_epoch,
 CASE WHEN NEW.result_state='known_unsent' THEN 'KNOWN_UNSENT_RETAINED' WHEN NEW.kind='INTENT' THEN 'DURABLE_SEND_AMBIGUITY_BOUNDARY' ELSE NEW.result_state END,
 json_object('submission_id',NEW.submission_id,'permit_id',b.permit_id,'state',NEW.result_state,'rpc_hash',NEW.rpc_hash,'failure_class',NEW.failure_class,'failure_detail',NEW.failure_detail),NEW.checked_at
 FROM manual_first_submission_bindings b WHERE b.submission_id=NEW.submission_id;
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-first-submission-transition-alert:'||NEW.id,'manual-first-submission-transition:'||NEW.id,
 CASE WHEN NEW.kind='OUTCOME' AND NEW.result_state!='submitted' THEN 'WARNING' ELSE 'INFO' END,'PENDING',NEW.checked_at);
 DELETE FROM manual_first_submission_transition_scopes WHERE id=NEW.id;
END;
CREATE TRIGGER manual_first_submission_transition_close BEFORE DELETE ON manual_first_submission_transition_scopes
WHEN NOT EXISTS(SELECT 1 FROM manual_first_submission_transitions c
 JOIN manual_first_submission_bindings b ON b.submission_id=c.submission_id
 JOIN transaction_submissions s ON s.id=b.submission_id AND s.attempt_id=b.attempt_id AND s.tx_hash=b.tx_hash AND s.sequence=0
 JOIN canary_runtime_audit a ON a.id='manual-first-submission-transition:'||c.id
 AND a.event_type='MANUAL_FIRST_SUBMISSION_'||c.kind AND a.operation_id=b.operation_id AND a.attempt_id=b.attempt_id
 AND a.authorization_id=b.authorization_id AND a.authorization_epoch=b.authorization_epoch AND a.created_at=c.checked_at
 AND a.reason_code=CASE WHEN c.result_state='known_unsent' THEN 'KNOWN_UNSENT_RETAINED' WHEN c.kind='INTENT' THEN 'DURABLE_SEND_AMBIGUITY_BOUNDARY' ELSE c.result_state END
 AND a.details_json=json_object('submission_id',c.submission_id,'permit_id',b.permit_id,'state',c.result_state,'rpc_hash',c.rpc_hash,'failure_class',c.failure_class,'failure_detail',c.failure_detail)
 JOIN canary_alert_outbox o ON o.id='manual-first-submission-transition-alert:'||c.id AND o.audit_id=a.id AND o.created_at=c.checked_at
 AND o.severity=CASE WHEN c.kind='OUTCOME' AND c.result_state!='submitted' THEN 'WARNING' ELSE 'INFO' END
 WHERE c.id=OLD.id
 AND ((c.kind='INTENT' AND s.send_intent_at=c.checked_at AND s.state='submitting')
 OR (c.kind='OUTCOME' AND s.state=c.result_state AND s.rpc_hash IS c.rpc_hash AND s.failure_class IS c.failure_class
 AND s.failure_detail IS c.failure_detail AND s.updated_at=c.checked_at
 AND (c.result_state='submitted' OR EXISTS(SELECT 1 FROM manual_nonce_claims q
 JOIN execution_wallet_lanes l ON l.wallet_id=q.wallet_id AND l.operation_id=q.operation_id AND l.step_id=q.execution_step_id
 AND l.reserved_nonce=q.nonce AND l.state='frozen' AND l.updated_at=c.checked_at
 AND l.freeze_reason=CASE WHEN c.result_state='known_unsent' THEN 'known_unsent_retained' ELSE c.result_state END
 JOIN canary_risk_reservations r ON r.id=q.reservation_id AND r.state='frozen'
 JOIN canary_authorization_operation_usage u ON u.operation_id=q.operation_id AND u.authorization_id=b.authorization_id AND u.state='FROZEN'
 WHERE q.operation_id=b.operation_id)))))
BEGIN SELECT RAISE(ABORT,'manual submission transition incomplete'); END;
