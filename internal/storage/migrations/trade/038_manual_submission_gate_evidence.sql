-- M4-B2.1: disconnected DB observation contract, NOT a gate/PASS/permit.
-- No trusted current clock, running-binary/RPC or cryptographic proof is claimed.
-- No old object/deny domain is changed. Original expiry/budget/nonce stay exact.
CREATE TABLE manual_submission_gate_evidence(
 id TEXT NOT NULL PRIMARY KEY,
 operation_id TEXT NOT NULL REFERENCES manual_submission_foundations(operation_id),
 source_json TEXT NOT NULL,
 source_hash TEXT NOT NULL,
 checked_at TEXT NOT NULL
);

-- Unlike a parent-only deferred FK, this empty target makes EVERY retained
-- scope uncommittable even if a newly injected FAIL trigger interrupts effects.
CREATE TABLE manual_gate_evidence_empty(id INTEGER PRIMARY KEY);
CREATE TRIGGER manual_gate_empty_no_insert BEFORE INSERT ON manual_gate_evidence_empty
BEGIN SELECT RAISE(ABORT,'manual gate empty target must remain empty'); END;
CREATE TRIGGER manual_gate_empty_no_update BEFORE UPDATE ON manual_gate_evidence_empty
BEGIN SELECT RAISE(ABORT,'manual gate empty target immutable'); END;
CREATE TRIGGER manual_gate_empty_no_delete BEFORE DELETE ON manual_gate_evidence_empty
BEGIN SELECT RAISE(ABORT,'manual gate empty target immutable'); END;
CREATE TABLE manual_gate_evidence_scopes(
 id TEXT NOT NULL PRIMARY KEY REFERENCES manual_submission_gate_evidence(id) DEFERRABLE INITIALLY DEFERRED,
 operation_id TEXT NOT NULL,
 source_hash TEXT NOT NULL,
 checked_at TEXT NOT NULL,
 empty_ref INTEGER NOT NULL DEFAULT 1 REFERENCES manual_gate_evidence_empty(id) DEFERRABLE INITIALLY DEFERRED
);

CREATE VIEW manual_gate_evidence_expected AS
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
FROM (SELECT * FROM manual_submission_foundation_complete LIMIT -1) f
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

-- Parse absolute timestamps, never lexicographically compare fractional text.
CREATE VIEW manual_gate_evidence_valid_source AS
SELECT e.* FROM manual_gate_evidence_expected e
WHERE NOT EXISTS(SELECT 1 FROM json_each(json_array(e.foundation_created_at,e.artifact_completed_at,e.control_effective_at,e.control_updated_at,
 e.authorized_at,e.auth_created_at,e.auth_updated_at,e.auth_expires_at,e.operation_expires_at,e.lease_updated_at,e.lease_expires_at,e.policy_created_at,
 json_extract(e.source_json,'$.allowlists.wallet_created_at'),json_extract(e.source_json,'$.allowlists.contract_created_at'),json_extract(e.source_json,'$.allowlists.token_created_at'))) t
 WHERE manual_artifact_time_valid(hex(t.value),typeof(t.value))!=1)
 AND manual_time_cmp(e.control_updated_at,e.control_effective_at)>=0
 AND manual_time_cmp(e.auth_updated_at,e.auth_created_at)>=0
 AND manual_time_cmp(e.auth_expires_at,e.authorized_at)>0
 AND manual_time_cmp(e.lease_expires_at,e.lease_updated_at)>0;

CREATE TRIGGER manual_gate_evidence_no_update BEFORE UPDATE ON manual_submission_gate_evidence
BEGIN SELECT RAISE(ABORT,'manual gate evidence immutable'); END;
CREATE TRIGGER manual_gate_evidence_no_delete BEFORE DELETE ON manual_submission_gate_evidence
BEGIN SELECT RAISE(ABORT,'manual gate evidence retained'); END;
CREATE TRIGGER manual_gate_scope_no_update BEFORE UPDATE ON manual_gate_evidence_scopes
BEGIN SELECT RAISE(ABORT,'manual gate scope immutable'); END;
CREATE TRIGGER manual_gate_scope_source BEFORE INSERT ON manual_gate_evidence_scopes
BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM canary_control_state WHERE singleton=1 AND chain_id=4663 AND emergency_stopped IS 0 AND mode='CONTROLLED_CANARY')
 THEN RAISE(ABORT,'manual gate control unavailable disabled or stopped') END;
 SELECT CASE WHEN typeof(NEW.id)!='text' OR typeof(NEW.operation_id)!='text' OR typeof(NEW.source_hash)!='text' OR NEW.empty_ref IS NOT 1
 OR manual_artifact_time_valid(hex(NEW.checked_at),typeof(NEW.checked_at))!=1
 OR hex(NEW.id)!=hex('manual-gate-evidence:'||manual_artifact_bytes_sha256(hex(json_object('operation_id',NEW.operation_id,'source_hash',NEW.source_hash,'checked_at',NEW.checked_at))))
 OR EXISTS(SELECT 1 FROM manual_submission_gate_evidence x WHERE hex(x.id)=hex(NEW.id))
 OR EXISTS(SELECT 1 FROM manual_gate_evidence_scopes x WHERE hex(x.id)=hex(NEW.id) OR x.rowid=NEW.rowid)
 OR NOT EXISTS(SELECT 1 FROM manual_gate_evidence_valid_source e WHERE e.operation_id IS NEW.operation_id AND hex(NEW.source_hash)=hex(manual_artifact_bytes_sha256(hex(e.source_json))))
 THEN RAISE(ABORT,'manual gate scope cannot reopen or adopt') END;
END;
CREATE TRIGGER manual_gate_evidence_source BEFORE INSERT ON manual_submission_gate_evidence
BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM canary_control_state WHERE singleton=1 AND chain_id=4663 AND emergency_stopped IS 0 AND mode='CONTROLLED_CANARY')
 THEN RAISE(ABORT,'manual gate control unavailable disabled or stopped') END;
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_gate_evidence_valid_source e WHERE e.operation_id IS NEW.operation_id
 AND typeof(NEW.id)='text' AND hex(NEW.id)=hex('manual-gate-evidence:'||manual_artifact_bytes_sha256(hex(json_object('operation_id',NEW.operation_id,'source_hash',NEW.source_hash,'checked_at',NEW.checked_at))))
 AND typeof(NEW.operation_id)='text' AND typeof(NEW.source_json)='text' AND hex(NEW.source_json)=hex(e.source_json)
 AND typeof(NEW.source_hash)='text' AND hex(NEW.source_hash)=hex(manual_artifact_bytes_sha256(hex(e.source_json)))
 AND manual_artifact_time_valid(hex(NEW.checked_at),typeof(NEW.checked_at))=1
 AND NOT EXISTS(SELECT 1 FROM json_each(json_array(e.foundation_created_at,e.artifact_completed_at,e.control_effective_at,e.control_updated_at,e.authorized_at,e.auth_created_at,e.auth_updated_at,e.lease_updated_at,e.policy_created_at,
 json_extract(e.source_json,'$.allowlists.wallet_created_at'),json_extract(e.source_json,'$.allowlists.contract_created_at'),json_extract(e.source_json,'$.allowlists.token_created_at'))) t WHERE manual_time_cmp(NEW.checked_at,t.value)<0)
 AND manual_time_cmp(NEW.checked_at,e.operation_expires_at)<0 AND manual_time_cmp(NEW.checked_at,e.auth_expires_at)<0 AND manual_time_cmp(NEW.checked_at,e.lease_expires_at)<0
 AND NOT EXISTS(SELECT 1 FROM canary_gate_attestations g WHERE manual_artifact_time_valid(hex(g.attested_at),typeof(g.attested_at))!=1 OR manual_time_cmp(NEW.checked_at,g.attested_at)<0))
 OR EXISTS(SELECT 1 FROM manual_submission_gate_evidence x WHERE hex(x.id)=hex(NEW.id) OR x.rowid=NEW.rowid)
 OR EXISTS(SELECT 1 FROM manual_gate_evidence_scopes x WHERE hex(x.id)=hex(NEW.id))
 OR EXISTS(SELECT 1 FROM canary_runtime_audit a WHERE hex(a.id)=hex('manual-gate-audit:'||NEW.id)
 OR (hex(a.event_type)=hex('MANUAL_SUBMISSION_GATE_EVIDENCE') AND hex(a.details_json)=hex(json_object('evidence_id',NEW.id,'source_hash',NEW.source_hash))))
 OR EXISTS(SELECT 1 FROM canary_alert_outbox a WHERE hex(a.id)=hex('manual-gate-alert:'||NEW.id) OR hex(a.audit_id)=hex('manual-gate-audit:'||NEW.id))
 THEN RAISE(ABORT,'manual gate evidence exact bounded DB source required') END;
 -- Witness faults happen BEFORE the parent row can exist. A retained failed
 -- scope cannot close without that parent, and a later parent INSERT cannot
 -- adopt the existing scope/witnesses. FK_ON then forbids partial COMMIT.
 INSERT INTO manual_gate_evidence_scopes(id,operation_id,source_hash,checked_at) VALUES(NEW.id,NEW.operation_id,NEW.source_hash,NEW.checked_at);
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'manual-gate-audit:'||NEW.id,'MANUAL_SUBMISSION_GATE_EVIDENCE',NEW.operation_id,q.attempt_id,b.authorization_id,b.authorization_epoch,
 'DB_OBSERVATION_NOT_GATE_OR_SEND_PERMISSION',json_object('evidence_id',NEW.id,'source_hash',NEW.source_hash),NEW.checked_at
 FROM manual_nonce_claims q JOIN manual_execution_bindings b ON b.operation_id=q.operation_id WHERE q.operation_id=NEW.operation_id;
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) VALUES('manual-gate-alert:'||NEW.id,'manual-gate-audit:'||NEW.id,'INFO','PENDING',NEW.checked_at);
END;

-- Preclose validates only this statement's exact metadata+witnesses. Deep old
-- signed-phase views are checked at source admission and again by complete
-- AFTER scope deletion; they must not be used as a recursive close oracle.
-- Historical completeness deliberately does not rejoin mutable controls/lease.
CREATE VIEW manual_gate_evidence_preclose AS
SELECT e.* FROM manual_submission_gate_evidence e
JOIN manual_submission_foundations f ON f.operation_id IS e.operation_id
JOIN manual_execution_bindings b ON b.operation_id=e.operation_id
JOIN manual_nonce_claims q ON q.operation_id=e.operation_id
JOIN canary_runtime_audit a ON a.id='manual-gate-audit:'||e.id AND typeof(a.id)='text'
 AND a.event_type IS 'MANUAL_SUBMISSION_GATE_EVIDENCE' AND a.operation_id IS e.operation_id AND a.attempt_id IS q.attempt_id
 AND a.authorization_id IS b.authorization_id AND a.authorization_epoch IS b.authorization_epoch
 AND a.reason_code IS 'DB_OBSERVATION_NOT_GATE_OR_SEND_PERMISSION' AND a.created_at IS e.checked_at
 AND typeof(a.details_json)='text' AND hex(a.details_json)=hex(json_object('evidence_id',e.id,'source_hash',e.source_hash))
JOIN canary_alert_outbox alert ON alert.id='manual-gate-alert:'||e.id AND typeof(alert.id)='text'
 AND alert.audit_id IS a.id AND alert.severity IS 'INFO' AND alert.created_at IS e.checked_at
WHERE typeof(e.id)='text' AND typeof(e.operation_id)='text' AND typeof(e.source_json)='text' AND typeof(e.source_hash)='text'
 AND hex(json_extract(e.source_json,'$.foundation'))=hex(json(f.binding_json)) AND json_extract(e.source_json,'$.foundation_hash') IS f.binding_hash
 AND hex(e.source_hash)=hex(manual_artifact_bytes_sha256(hex(e.source_json)))
 AND hex(e.id)=hex('manual-gate-evidence:'||manual_artifact_bytes_sha256(hex(json_object('operation_id',e.operation_id,'source_hash',e.source_hash,'checked_at',e.checked_at))))
 AND manual_artifact_time_valid(hex(e.checked_at),typeof(e.checked_at))=1
 AND NOT EXISTS(SELECT 1 FROM manual_submission_gate_evidence x WHERE x.rowid!=e.rowid AND hex(x.id)=hex(e.id))
 AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit x WHERE x.rowid!=a.rowid AND (hex(x.id)=hex(a.id)
 OR (hex(x.event_type)=hex(a.event_type) AND hex(x.details_json)=hex(a.details_json))))
 AND NOT EXISTS(SELECT 1 FROM canary_alert_outbox x WHERE x.rowid!=alert.rowid AND (hex(x.id)=hex(alert.id) OR hex(x.audit_id)=hex(a.id)));
CREATE VIEW manual_gate_evidence_complete AS
SELECT e.* FROM manual_gate_evidence_preclose e
JOIN (SELECT * FROM manual_submission_foundation_complete LIMIT -1) f ON f.operation_id IS e.operation_id
WHERE NOT EXISTS(SELECT 1 FROM manual_gate_evidence_scopes s WHERE hex(s.id)=hex(e.id));
CREATE TRIGGER manual_gate_scope_close BEFORE DELETE ON manual_gate_evidence_scopes
WHEN NOT EXISTS(SELECT 1 FROM (SELECT * FROM manual_gate_evidence_preclose LIMIT -1) e WHERE e.id IS OLD.id AND e.operation_id IS OLD.operation_id AND e.source_hash IS OLD.source_hash AND e.checked_at IS OLD.checked_at)
BEGIN SELECT RAISE(ABORT,'manual gate incomplete witnesses cannot close'); END;

CREATE TRIGGER manual_gate_audit_insert BEFORE INSERT ON canary_runtime_audit
WHEN (hex(NEW.event_type)=hex('MANUAL_SUBMISSION_GATE_EVIDENCE') OR substr(hex(NEW.id),1,length(hex('manual-gate-audit:')))=hex('manual-gate-audit:')
 OR EXISTS(SELECT 1 FROM canary_runtime_audit a JOIN manual_submission_gate_evidence e ON hex(a.id)=hex('manual-gate-audit:'||e.id) WHERE a.rowid=NEW.rowid OR hex(a.id)=hex(NEW.id)))
 AND NOT EXISTS(SELECT 1 FROM manual_gate_evidence_scopes s
 JOIN manual_execution_bindings b ON b.operation_id=s.operation_id JOIN manual_nonce_claims q ON q.operation_id=s.operation_id
 WHERE NEW.id IS 'manual-gate-audit:'||s.id AND NEW.event_type IS 'MANUAL_SUBMISSION_GATE_EVIDENCE'
 AND NEW.operation_id IS s.operation_id AND NEW.attempt_id IS q.attempt_id AND NEW.authorization_id IS b.authorization_id AND NEW.authorization_epoch IS b.authorization_epoch
 AND NEW.reason_code IS 'DB_OBSERVATION_NOT_GATE_OR_SEND_PERMISSION' AND NEW.created_at IS s.checked_at AND typeof(NEW.details_json)='text'
 AND hex(NEW.details_json)=hex(json_object('evidence_id',s.id,'source_hash',s.source_hash))
 AND NOT EXISTS(SELECT 1 FROM manual_submission_gate_evidence e WHERE hex(e.id)=hex(s.id))
 AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit x WHERE hex(x.id)=hex(NEW.id) OR x.rowid=NEW.rowid OR (hex(x.event_type)=hex(NEW.event_type) AND hex(x.details_json)=hex(NEW.details_json))))
BEGIN SELECT RAISE(ABORT,'manual gate audit initial statement only'); END;
CREATE TRIGGER manual_gate_alert_insert BEFORE INSERT ON canary_alert_outbox
WHEN (substr(hex(NEW.id),1,length(hex('manual-gate-alert:')))=hex('manual-gate-alert:') OR substr(hex(NEW.audit_id),1,length(hex('manual-gate-audit:')))=hex('manual-gate-audit:')
 OR EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_submission_gate_evidence e ON hex(a.id)=hex('manual-gate-alert:'||e.id) OR hex(a.audit_id)=hex('manual-gate-audit:'||e.id)
 WHERE a.rowid=NEW.rowid OR hex(a.id)=hex(NEW.id) OR hex(a.audit_id)=hex(NEW.audit_id)))
 AND NOT EXISTS(SELECT 1 FROM manual_gate_evidence_scopes s
 WHERE NEW.id IS 'manual-gate-alert:'||s.id AND NEW.audit_id IS 'manual-gate-audit:'||s.id AND NEW.severity IS 'INFO' AND NEW.state IS 'PENDING' AND NEW.created_at IS s.checked_at AND NEW.delivered_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM manual_submission_gate_evidence e WHERE hex(e.id)=hex(s.id))
 AND NOT EXISTS(SELECT 1 FROM canary_alert_outbox x WHERE hex(x.id)=hex(NEW.id) OR hex(x.audit_id)=hex(NEW.audit_id) OR x.rowid=NEW.rowid))
BEGIN SELECT RAISE(ABORT,'manual gate alert initial statement only'); END;
CREATE TRIGGER manual_gate_alert_update BEFORE UPDATE ON canary_alert_outbox
WHEN ((substr(hex(NEW.id),1,length(hex('manual-gate-alert:')))=hex('manual-gate-alert:') OR substr(hex(NEW.audit_id),1,length(hex('manual-gate-audit:')))=hex('manual-gate-audit:'))
 AND (NEW.rowid IS NOT OLD.rowid OR NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id))
 OR (EXISTS(SELECT 1 FROM manual_submission_gate_evidence e WHERE hex(OLD.id)=hex('manual-gate-alert:'||e.id) OR hex(OLD.audit_id)=hex('manual-gate-audit:'||e.id))
 AND (NEW.rowid IS NOT OLD.rowid OR NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id OR NEW.severity IS NOT OLD.severity OR NEW.created_at IS NOT OLD.created_at))
 OR EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_submission_gate_evidence e ON hex(a.id)=hex('manual-gate-alert:'||e.id) OR hex(a.audit_id)=hex('manual-gate-audit:'||e.id)
 WHERE a.rowid!=OLD.rowid AND (a.rowid=NEW.rowid OR hex(a.id)=hex(NEW.id) OR hex(a.audit_id)=hex(NEW.audit_id)))
BEGIN SELECT RAISE(ABORT,'manual gate alert identity immutable'); END;
CREATE TRIGGER manual_gate_alert_delete BEFORE DELETE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_submission_gate_evidence e WHERE hex(OLD.id)=hex('manual-gate-alert:'||e.id) OR hex(OLD.audit_id)=hex('manual-gate-audit:'||e.id))
BEGIN SELECT RAISE(ABORT,'manual gate alert retained'); END;
CREATE TRIGGER manual_gate_evidence_effect AFTER INSERT ON manual_submission_gate_evidence
BEGIN
 DELETE FROM manual_gate_evidence_scopes WHERE id=NEW.id;
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_gate_evidence_complete WHERE id=NEW.id)
 OR EXISTS(SELECT 1 FROM manual_gate_evidence_scopes WHERE hex(id)=hex(NEW.id)) THEN RAISE(ABORT,'manual gate evidence incomplete atomic effect') END;
END;
