-- M3-B3 foundation only. No signing state guard is opened, no attempt/lane is
-- changed and no signer is called. This immutable marker is an ambiguity
-- boundary, NEVER proof of invocation, signer permission or a reusable permit.
-- Preserve landed 001..032 and their exact prepared-claim completeness views.
CREATE VIEW manual_signing_facts AS
SELECT q.operation_id,json_object(
 'version',1,'operation_id',q.operation_id,'request_id',b.request_id,
 'execution_step_id',q.execution_step_id,'attempt_id',q.attempt_id,'wallet_id',q.wallet_id,
 'reservation_id',q.reservation_id,'contract_hash',q.contract_hash,'chain_id',b.chain_id,
 'from',json_extract(c.contract_json,'$.output.from'),'to',json_extract(c.contract_json,'$.output.to'),
 'value',json_extract(c.contract_json,'$.output.value'),'data',json_extract(c.contract_json,'$.output.data'),
 'nonce',q.nonce,'gas_limit',json_extract(c.contract_json,'$.output.gas_limit'),
 'gas_fee_cap',json_extract(c.contract_json,'$.output.gas_fee_cap'),'gas_tip_cap',json_extract(c.contract_json,'$.output.gas_tip_cap'),
 'policy_version',b.policy_version,'policy_hash',json_extract(c.contract_json,'$.policy_hash'),
 'quote_block_number',b.quote_block_number,'quote_block_hash',b.quote_block_hash,
 'runtime_code_hash',json_extract(c.contract_json,'$.output.resolution.token.curve_runtime_code_hash'),
 'expires_at',b.expires_at,'claimed_at',q.checked_at,'claim_observation_hash',q.observation_hash,
 'deadline_capability','APPLICATION_TTL_ONLY','contract_deadline',json('false'),
 'authorization_id',b.authorization_id,'authorization_epoch',b.authorization_epoch,
 'manual_policy_id',b.manual_policy_id,'manual_policy_hash',b.manual_policy_hash,'gate_evidence_set_hash',b.gate_evidence_set_hash,
 'environment',b.environment,'build_sha',b.build_sha,'release_id',b.release_id,'deployment_id',b.deployment_id) AS facts_json
FROM manual_nonce_claims q JOIN manual_execution_bindings b ON b.operation_id=q.operation_id
JOIN manual_admission_commands c ON c.operation_id=q.operation_id
WHERE json_extract(c.contract_json,'$.deadline_capability')='APPLICATION_TTL_ONLY'
 AND json_type(c.contract_json,'$.contract_deadline')='false';

CREATE TABLE manual_signing_intents(
 operation_id TEXT NOT NULL PRIMARY KEY REFERENCES manual_nonce_claims(operation_id),
 owner_id TEXT NOT NULL UNIQUE CHECK(typeof(owner_id)='text' AND length(CAST(owner_id AS BLOB))=64 AND owner_id NOT GLOB '*[^0-9a-f]*'),
 facts_json TEXT NOT NULL CHECK(typeof(facts_json)='text' AND json_valid(facts_json)),
 facts_hash TEXT NOT NULL CHECK(typeof(facts_hash)='text'),
 observation_json TEXT NOT NULL CHECK(typeof(observation_json)='text' AND json_valid(observation_json)),
 observation_hash TEXT NOT NULL CHECK(typeof(observation_hash)='text'),
 checked_at TEXT NOT NULL CHECK(typeof(checked_at)='text')
);
CREATE TRIGGER manual_signing_intent_no_update BEFORE UPDATE ON manual_signing_intents
BEGIN SELECT RAISE(ABORT,'manual signing intent immutable'); END;
CREATE TRIGGER manual_signing_intent_no_delete BEFORE DELETE ON manual_signing_intents
BEGIN SELECT RAISE(ABORT,'manual signing ambiguity cannot be released'); END;
CREATE TRIGGER manual_signing_intent_no_replace BEFORE INSERT ON manual_signing_intents
WHEN EXISTS(SELECT 1 FROM manual_signing_intents i WHERE hex(i.operation_id)=hex(NEW.operation_id) OR hex(i.owner_id)=hex(NEW.owner_id) OR i.rowid=NEW.rowid
 OR hex(CASE WHEN json_valid(i.facts_json) THEN json_extract(i.facts_json,'$.operation_id') END)=hex(NEW.operation_id))
BEGIN SELECT RAISE(ABORT,'manual signing intent conflict victim protected'); END;

-- SQL checked_at is evidence supplied by a controlled writer, not a DB wall
-- clock or current signing permission. The future writer MUST recheck after
-- marker commit immediately before invocation under its own writer fence.
CREATE TRIGGER manual_signing_intent_source BEFORE INSERT ON manual_signing_intents
WHEN NOT EXISTS(
 SELECT 1 FROM manual_nonce_claim_complete q JOIN manual_signing_facts f ON f.operation_id=q.operation_id
 JOIN manual_nonce_binding_facts nf ON nf.operation_id=q.operation_id
 JOIN manual_execution_bindings b ON b.operation_id=q.operation_id
 JOIN manual_admission_commands c ON c.operation_id=q.operation_id
 JOIN canary_control_state ctl ON ctl.singleton=1 AND ctl.chain_id=4663 AND ctl.mode='CONTROLLED_CANARY' AND ctl.emergency_stopped=0 AND ctl.policy_version=b.policy_version
 JOIN canary_runtime_authorizations a ON a.id=b.authorization_id AND a.epoch=b.authorization_epoch
 AND a.state='ARMED' AND a.mode='CONTROLLED_CANARY' AND a.chain_id=4663 AND a.wallet_id=b.wallet_id AND a.policy_version=b.policy_version
 AND a.environment=b.environment AND a.build_sha=b.build_sha AND a.release_id=b.release_id AND a.deployment_id=b.deployment_id AND a.gate_evidence_set_hash=b.gate_evidence_set_hash
 JOIN canary_risk_reservations r ON r.id=q.reservation_id AND r.state='reserved'
 JOIN canary_authorization_operation_usage u ON u.authorization_id=a.id AND u.operation_id=q.operation_id AND u.state='RESERVED'
 JOIN dry_run_wallets w ON w.id=b.wallet_id AND w.chain_id=4663 AND w.enabled=1 AND lower(w.address)=q.wallet_address
 JOIN canary_wallet_allowlist wa ON wa.wallet_id=w.id AND wa.chain_id=4663 AND wa.policy_version=b.policy_version AND wa.enabled=1 AND lower(wa.address)=q.wallet_address
 JOIN manual_risk_policies p ON p.id=b.manual_policy_id AND p.policy_hash=b.manual_policy_hash AND p.source_policy_version=b.policy_version AND p.source_policy_hash=json_extract(c.contract_json,'$.policy_hash')
 WHERE q.operation_id=NEW.operation_id AND typeof(NEW.owner_id)='text' AND length(CAST(NEW.owner_id AS BLOB))=64 AND NEW.owner_id NOT GLOB '*[^0-9a-f]*'
 AND hex(NEW.facts_json)=hex(f.facts_json)
 AND manual_artifact_facts_hash(hex(NEW.facts_json),typeof(NEW.facts_json))=NEW.facts_hash
 AND manual_nonce_observation_bytes_valid(hex(NEW.observation_json),typeof(NEW.observation_json),hex(NEW.observation_hash),typeof(NEW.observation_hash),
 hex(q.nonce),typeof(q.nonce),hex(nf.facts_json),typeof(nf.facts_json),hex(NEW.checked_at),typeof(NEW.checked_at))=1
 AND manual_time_cmp(NEW.checked_at,q.checked_at)>=0
 AND manual_time_cmp(json_extract(NEW.observation_json,'$.observed_at'),q.checked_at)>=0
 AND manual_time_cmp(NEW.checked_at,a.authorized_at)>=0 AND manual_time_cmp(NEW.checked_at,a.created_at)>=0
 AND manual_time_cmp(NEW.checked_at,a.updated_at)>=0 AND manual_time_cmp(NEW.checked_at,a.expires_at)<0
 AND manual_time_cmp(NEW.checked_at,ctl.effective_at)>=0 AND manual_time_cmp(NEW.checked_at,ctl.updated_at)>=0
 AND manual_time_cmp(NEW.checked_at,p.created_at)>=0
 AND manual_artifact_time_valid(hex(a.authorized_at),typeof(a.authorized_at))=1
 AND manual_artifact_time_valid(hex(a.created_at),typeof(a.created_at))=1
 AND manual_artifact_time_valid(hex(a.updated_at),typeof(a.updated_at))=1
 AND manual_artifact_time_valid(hex(a.expires_at),typeof(a.expires_at))=1
 AND manual_artifact_time_valid(hex(ctl.effective_at),typeof(ctl.effective_at))=1
 AND manual_artifact_time_valid(hex(ctl.updated_at),typeof(ctl.updated_at))=1
 AND manual_artifact_time_valid(hex(p.created_at),typeof(p.created_at))=1
 AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit x WHERE hex(x.id)=hex('manual-sign-intent:'||q.operation_id)
 OR (manual_nonce_identity_relation(hex(x.event_type),typeof(x.event_type),hex('MANUAL_SIGNING_INTENT'),0)>0
 AND manual_nonce_identity_relation(hex(x.operation_id),typeof(x.operation_id),hex(q.operation_id),0)>0))
 AND NOT EXISTS(SELECT 1 FROM canary_alert_outbox x WHERE hex(x.id)=hex('manual-sign-intent-alert:'||q.operation_id) OR hex(x.audit_id)=hex('manual-sign-intent:'||q.operation_id))
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_unresolved_wallet_steps WHERE operation_id=q.operation_id)
 AND NOT EXISTS(SELECT 1 FROM execution_reservations x WHERE manual_nonce_identity_relation(hex(x.wallet_id),typeof(x.wallet_id),hex(q.wallet_id),0)>0 AND x.status IN ('reserved','signed','frozen'))
 AND NOT EXISTS(SELECT 1 FROM canary_risk_reservations x WHERE x.operation_id!=q.operation_id AND manual_nonce_identity_relation(hex(x.wallet_id),typeof(x.wallet_id),hex(q.wallet_id),0)>0 AND x.state IN ('reserved','frozen'))
 AND EXISTS(SELECT 1 FROM canary_contract_allowlist ca JOIN canary_token_allowlist ta ON ta.policy_version=ca.policy_version
 WHERE ca.policy_version=b.policy_version AND ca.role='CURVE' AND ca.protocol='PONS_V2_CURVE' AND ca.enabled=1 AND ca.baseline_ref!=''
 AND lower(ca.address)=json_extract(c.contract_json,'$.output.resolution.token.curve') AND ca.runtime_code_hash=json_extract(c.contract_json,'$.output.resolution.token.curve_runtime_code_hash')
 AND ta.protocol='PONS_V2_CURVE' AND ta.enabled=1 AND lower(ta.token_address)=json_extract(c.contract_json,'$.output.resolution.token.address'))
 AND (SELECT COUNT(*) FROM canary_gate_attestations g WHERE g.status='PASS' AND manual_artifact_time_valid(hex(g.attested_at),typeof(g.attested_at))=1 AND manual_time_cmp(NEW.checked_at,g.attested_at)>=0
 AND ((g.gate='C04' AND g.evidence_hash='c1af9454a6a4433b488158c25224d13dfbf31178c71dae7468c80917743381fd')
 OR (g.gate='C05' AND g.evidence_hash='1a793f13d9304a31ed405f9fe25f1713cf79b6f8d28bb3da8ad16a01dc24c40b')
 OR (g.gate='C08' AND g.evidence_hash='8178e7b976dbea67be1de83cec529171e56d154cd2114f1bf290bebbd1fc77d8')))=3
 AND b.gate_evidence_set_hash=(SELECT manual_artifact_bytes_sha256(hex(json_group_array(json_object('Gate',gate,'Status',status,'EvidenceHash',evidence_hash,'AttestedAt',attested_at))))
 FROM (SELECT gate,status,evidence_hash,attested_at FROM canary_gate_attestations WHERE gate IN ('C04','C05','C08') ORDER BY gate))
 AND EXISTS(SELECT 1 FROM canary_authorization_usage usage WHERE usage.authorization_id=a.id
 AND usage.operation_count<=a.max_operations AND manual_u256_cmp(usage.total_input,a.max_total_input)<=0
 AND usage.operation_count=(SELECT COUNT(*) FROM canary_authorization_operation_usage WHERE authorization_id=a.id)
 AND usage.operation_count=(SELECT COUNT(*) FROM manual_admission_complete WHERE authorization_id=a.id)
 AND usage.total_input=(SELECT manual_u256_sum_json(json_group_array(amount)) FROM canary_authorization_operation_usage WHERE authorization_id=a.id))
)
BEGIN SELECT RAISE(ABORT,'manual signing intent source/control/evidence invalid'); END;

CREATE TRIGGER manual_signing_intent_audit_shape BEFORE INSERT ON canary_runtime_audit
WHEN (manual_nonce_identity_relation(hex(NEW.event_type),typeof(NEW.event_type),hex('MANUAL_SIGNING_INTENT'),0)>0 OR hex(substr(CAST(NEW.id AS BLOB),1,19))=hex('manual-sign-intent:'))
 AND NOT EXISTS(SELECT 1 FROM manual_signing_intents i JOIN manual_nonce_claims q ON q.operation_id=i.operation_id JOIN manual_execution_bindings b ON b.operation_id=i.operation_id
 WHERE NEW.id='manual-sign-intent:'||i.operation_id AND NEW.event_type='MANUAL_SIGNING_INTENT' AND NEW.operation_id IS i.operation_id AND NEW.attempt_id IS q.attempt_id
 AND NEW.authorization_id IS b.authorization_id AND NEW.authorization_epoch IS b.authorization_epoch AND NEW.reason_code IS 'SIGNER_MAY_HAVE_RUN_NOT_PERMISSION' AND NEW.created_at IS i.checked_at
 AND NEW.details_json IS json_object('owner_id',i.owner_id,'facts_hash',i.facts_hash,'observation_hash',i.observation_hash,'execution_step_id',q.execution_step_id,'nonce',q.nonce))
BEGIN SELECT RAISE(ABORT,'manual signing intent audit exact identity required'); END;
CREATE TRIGGER manual_signing_intent_audit_victim BEFORE INSERT ON canary_runtime_audit
WHEN EXISTS(SELECT 1 FROM canary_runtime_audit a JOIN manual_signing_intents i ON a.id='manual-sign-intent:'||i.operation_id OR (a.event_type='MANUAL_SIGNING_INTENT' AND a.operation_id=i.operation_id)
 WHERE a.id=NEW.id OR a.rowid=NEW.rowid)
BEGIN SELECT RAISE(ABORT,'manual signing intent audit conflict victim protected'); END;
CREATE TRIGGER manual_signing_intent_alert_victim BEFORE INSERT ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_signing_intents i ON a.id='manual-sign-intent-alert:'||i.operation_id OR a.audit_id='manual-sign-intent:'||i.operation_id
 WHERE a.id=NEW.id OR a.audit_id=NEW.audit_id OR a.rowid=NEW.rowid)
BEGIN SELECT RAISE(ABORT,'manual signing intent alert conflict victim protected'); END;
CREATE TRIGGER manual_signing_intent_alert_shape BEFORE INSERT ON canary_alert_outbox
WHEN (hex(substr(CAST(NEW.id AS BLOB),1,25))=hex('manual-sign-intent-alert:') OR hex(substr(CAST(NEW.audit_id AS BLOB),1,19))=hex('manual-sign-intent:'))
 AND NOT EXISTS(SELECT 1 FROM manual_signing_intents i WHERE NEW.id='manual-sign-intent-alert:'||i.operation_id AND NEW.audit_id='manual-sign-intent:'||i.operation_id AND NEW.severity='WARNING' AND NEW.created_at=i.checked_at)
BEGIN SELECT RAISE(ABORT,'manual signing intent alert exact identity required'); END;
CREATE TRIGGER manual_signing_intent_alert_update BEFORE UPDATE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_signing_intents i WHERE (OLD.id='manual-sign-intent-alert:'||i.operation_id OR OLD.audit_id='manual-sign-intent:'||i.operation_id)
 AND (NEW.rowid IS NOT OLD.rowid OR NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id OR NEW.severity IS NOT OLD.severity OR NEW.created_at IS NOT OLD.created_at))
 OR EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_signing_intents i ON a.id='manual-sign-intent-alert:'||i.operation_id OR a.audit_id='manual-sign-intent:'||i.operation_id
 WHERE a.rowid!=OLD.rowid AND (a.id=NEW.id OR a.audit_id=NEW.audit_id OR a.rowid=NEW.rowid))
BEGIN SELECT RAISE(ABORT,'manual signing intent alert identity protected'); END;
CREATE TRIGGER manual_signing_intent_alert_no_delete BEFORE DELETE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_signing_intents i WHERE OLD.id='manual-sign-intent-alert:'||i.operation_id OR OLD.audit_id='manual-sign-intent:'||i.operation_id)
BEGIN SELECT RAISE(ABORT,'manual signing intent alert immutable'); END;

CREATE VIEW manual_signing_intent_complete AS
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

CREATE TRIGGER manual_signing_intent_effect AFTER INSERT ON manual_signing_intents
BEGIN
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'manual-sign-intent:'||NEW.operation_id,'MANUAL_SIGNING_INTENT',NEW.operation_id,q.attempt_id,b.authorization_id,b.authorization_epoch,'SIGNER_MAY_HAVE_RUN_NOT_PERMISSION',
 json_object('owner_id',NEW.owner_id,'facts_hash',NEW.facts_hash,'observation_hash',NEW.observation_hash,'execution_step_id',q.execution_step_id,'nonce',q.nonce),NEW.checked_at
 FROM manual_nonce_claims q JOIN manual_execution_bindings b ON b.operation_id=q.operation_id WHERE q.operation_id=NEW.operation_id;
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) VALUES('manual-sign-intent-alert:'||NEW.operation_id,'manual-sign-intent:'||NEW.operation_id,'WARNING','PENDING',NEW.checked_at);
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_signing_intent_complete WHERE operation_id=NEW.operation_id)
 THEN RAISE(ABORT,'manual signing intent incomplete audit effect') END;
END;
