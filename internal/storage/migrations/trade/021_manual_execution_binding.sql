-- M2 metadata-only handoff on the existing execution lifecycle. No nonce,
-- signing, attempt, financial reservation, permit or economic PASS snapshot.
-- Original admission operation/validation step and migration 020 stay intact.
CREATE TABLE manual_execution_bindings(
 operation_id TEXT PRIMARY KEY REFERENCES manual_admission_commands(operation_id),
 request_id TEXT NOT NULL UNIQUE REFERENCES manual_admission_commands(request_id),
 validation_step_id TEXT NOT NULL UNIQUE REFERENCES execution_steps(id),
 execution_step_id TEXT NOT NULL UNIQUE REFERENCES execution_steps(id) DEFERRABLE INITIALLY DEFERRED,
 wallet_id TEXT NOT NULL REFERENCES dry_run_wallets(id),
 wallet_address TEXT NOT NULL,
 chain_id INTEGER NOT NULL CHECK(chain_id=4663),
 contract_hash TEXT NOT NULL,
 policy_version INTEGER NOT NULL,
 quote_block_number INTEGER NOT NULL,
 quote_block_hash TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 authorization_id TEXT NOT NULL,
 authorization_epoch INTEGER NOT NULL,
 manual_policy_id TEXT NOT NULL,
 manual_policy_hash TEXT NOT NULL,
 gate_evidence_set_hash TEXT NOT NULL,
 environment TEXT NOT NULL,
 build_sha TEXT NOT NULL,
 release_id TEXT NOT NULL,
 deployment_id TEXT NOT NULL,
 checked_at TEXT NOT NULL,
 FOREIGN KEY(authorization_id,authorization_epoch) REFERENCES canary_runtime_authorizations(id,epoch)
);
CREATE TRIGGER manual_execution_binding_no_update BEFORE UPDATE ON manual_execution_bindings
BEGIN SELECT RAISE(ABORT,'manual execution binding immutable'); END;
CREATE TRIGGER manual_execution_binding_no_delete BEFORE DELETE ON manual_execution_bindings
BEGIN SELECT RAISE(ABORT,'manual execution binding immutable'); END;
CREATE TRIGGER manual_execution_binding_no_replace BEFORE INSERT ON manual_execution_bindings
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings WHERE operation_id=NEW.operation_id OR request_id=NEW.request_id
 OR execution_step_id=NEW.execution_step_id OR validation_step_id=NEW.validation_step_id)
BEGIN SELECT RAISE(ABORT,'manual execution binding exists'); END;

-- Schema enforces durable identity/control and checked_at time relationships.
-- The disconnected Go writer separately verifies canonical hashes, trustworthy
-- current clock and final TTL on the same writer; SQL shape is not permission.
CREATE TRIGGER manual_execution_binding_source BEFORE INSERT ON manual_execution_bindings
WHEN NOT EXISTS(
 SELECT 1 FROM manual_admission_complete c
 JOIN canary_runtime_authorizations a ON a.id=c.authorization_id AND a.epoch=c.authorization_epoch
 JOIN canary_control_state ctl ON ctl.singleton=1
 JOIN canary_risk_reservations rr ON rr.id=c.reservation_id AND rr.state='reserved'
 JOIN canary_authorization_operation_usage u ON u.operation_id=c.operation_id AND u.authorization_id=a.id AND u.state='RESERVED'
 JOIN dry_run_wallets w ON w.id=NEW.wallet_id AND w.enabled=1 AND w.chain_id=4663
 JOIN canary_wallet_allowlist wa ON wa.wallet_id=w.id AND wa.chain_id=4663 AND wa.policy_version=NEW.policy_version
 WHERE c.operation_id=NEW.operation_id AND c.request_id=NEW.request_id AND c.step_id=NEW.validation_step_id
 AND NEW.execution_step_id='manual-exec:'||c.operation_id
 AND NEW.wallet_id=json_extract(c.contract_json,'$.wallet_id') AND NEW.wallet_address=json_extract(c.contract_json,'$.wallet_address')
 AND lower(w.address)=lower(NEW.wallet_address) AND wa.enabled=1 AND lower(wa.address)=lower(NEW.wallet_address)
 AND NEW.contract_hash=c.contract_hash AND NEW.policy_version=json_extract(c.contract_json,'$.policy_version')
 AND NEW.quote_block_number=json_extract(c.contract_json,'$.output.resolution.token.block.number')
 AND NEW.quote_block_hash=json_extract(c.contract_json,'$.output.resolution.token.block.hash')
 AND NEW.expires_at=json_extract(c.contract_json,'$.expires_at') AND NEW.authorization_id=c.authorization_id
 AND NEW.authorization_epoch=c.authorization_epoch AND NEW.manual_policy_id=c.manual_policy_id
 AND NEW.manual_policy_hash=c.manual_policy_hash AND NEW.gate_evidence_set_hash=c.gate_evidence_set_hash
 AND NEW.environment=c.environment AND NEW.build_sha=c.build_sha AND NEW.release_id=c.release_id AND NEW.deployment_id=c.deployment_id
 AND a.state='ARMED' AND a.mode='CONTROLLED_CANARY' AND a.chain_id=4663 AND a.wallet_id=NEW.wallet_id
 AND a.policy_version=NEW.policy_version AND a.environment=NEW.environment AND a.build_sha=NEW.build_sha
 AND a.release_id=NEW.release_id AND a.deployment_id=NEW.deployment_id AND a.gate_evidence_set_hash=NEW.gate_evidence_set_hash
 AND ctl.emergency_stopped=0 AND ctl.mode='CONTROLLED_CANARY' AND ctl.chain_id=4663 AND ctl.policy_version=NEW.policy_version
 AND manual_time_cmp(NEW.checked_at,c.checked_at)>=0 AND manual_time_cmp(NEW.checked_at,NEW.expires_at)<0
 AND manual_time_cmp(NEW.checked_at,a.authorized_at)>=0 AND manual_time_cmp(NEW.checked_at,a.created_at)>=0
 AND manual_time_cmp(NEW.checked_at,a.updated_at)>=0 AND manual_time_cmp(NEW.checked_at,a.expires_at)<0
 AND manual_time_cmp(NEW.checked_at,ctl.effective_at)>=0 AND manual_time_cmp(NEW.checked_at,ctl.updated_at)>=0
 AND EXISTS(SELECT 1 FROM canary_contract_allowlist ca JOIN canary_token_allowlist ta ON ta.policy_version=ca.policy_version
 WHERE ca.policy_version=NEW.policy_version AND ca.role='CURVE' AND ca.protocol='PONS_V2_CURVE' AND ca.enabled=1 AND ca.baseline_ref!=''
 AND lower(ca.address)=json_extract(c.contract_json,'$.output.resolution.token.curve')
 AND ca.runtime_code_hash=json_extract(c.contract_json,'$.output.resolution.token.curve_runtime_code_hash')
 AND ta.protocol='PONS_V2_CURVE' AND ta.enabled=1 AND lower(ta.token_address)=json_extract(c.contract_json,'$.output.resolution.token.address'))
 AND (SELECT COUNT(*) FROM canary_gate_attestations g WHERE g.status='PASS' AND manual_time_cmp(NEW.checked_at,g.attested_at)>=0
 AND ((g.gate='C04' AND g.evidence_hash='c1af9454a6a4433b488158c25224d13dfbf31178c71dae7468c80917743381fd')
 OR (g.gate='C05' AND g.evidence_hash='1a793f13d9304a31ed405f9fe25f1713cf79b6f8d28bb3da8ad16a01dc24c40b')
 OR (g.gate='C08' AND g.evidence_hash='8178e7b976dbea67be1de83cec529171e56d154cd2114f1bf290bebbd1fc77d8')))=3
)
BEGIN SELECT RAISE(ABORT,'manual execution binding source/control mismatch'); END;
CREATE TRIGGER manual_execution_binding_no_halves BEFORE INSERT ON manual_execution_bindings
WHEN EXISTS(SELECT 1 FROM execution_steps WHERE id=NEW.execution_step_id OR (operation_id=NEW.operation_id AND step_index!=0))
 OR EXISTS(SELECT 1 FROM transaction_attempts t JOIN execution_steps s ON s.id=t.step_id WHERE s.operation_id=NEW.operation_id)
 OR EXISTS(SELECT 1 FROM execution_wallet_lanes WHERE wallet_id=NEW.wallet_id AND state!='idle')
 OR EXISTS(SELECT 1 FROM execution_reservations WHERE wallet_id=NEW.wallet_id AND status IN ('reserved','signed','frozen'))
 OR EXISTS(SELECT 1 FROM canary_risk_reservations WHERE wallet_id=NEW.wallet_id AND operation_id!=NEW.operation_id AND state IN ('reserved','frozen'))
 OR EXISTS(SELECT 1 FROM execution_steps WHERE wallet_id=NEW.wallet_id AND status IN ('manual_execution_bound','nonce_reserved','signing','signed','submitted','broadcast_unknown','reconciling','manual_resolution'))
 OR EXISTS(SELECT 1 FROM canary_runtime_audit WHERE id='manual-execution:'||NEW.operation_id)
 OR EXISTS(SELECT 1 FROM canary_alert_outbox WHERE id='manual-execution-alert:'||NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual execution orphan or unresolved work'); END;
CREATE TRIGGER manual_execution_binding_budget BEFORE INSERT ON manual_execution_bindings
WHEN NOT EXISTS(
 SELECT 1 FROM canary_authorization_usage u JOIN canary_runtime_authorizations a ON a.id=u.authorization_id
 WHERE a.id=NEW.authorization_id AND u.operation_count<=a.max_operations AND manual_u256_cmp(u.total_input,a.max_total_input)<=0
 AND u.operation_count=(SELECT COUNT(*) FROM canary_authorization_operation_usage WHERE authorization_id=a.id)
 AND u.operation_count=(SELECT COUNT(*) FROM manual_admission_complete WHERE authorization_id=a.id)
 AND u.total_input=(SELECT manual_u256_sum_json(json_group_array(amount)) FROM canary_authorization_operation_usage WHERE authorization_id=a.id)
)
BEGIN SELECT RAISE(ABORT,'manual execution allowance incomplete'); END;

CREATE TRIGGER manual_execution_step_insert BEFORE INSERT ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id AND step_id!=NEW.id)
 AND NOT EXISTS(SELECT 1 FROM manual_execution_bindings b JOIN manual_admission_commands c ON c.operation_id=b.operation_id
 WHERE b.operation_id=NEW.operation_id AND b.execution_step_id=NEW.id AND NEW.step_index=1 AND NEW.kind='manual_trade_execution'
 AND NEW.status='manual_execution_bound' AND NEW.parameters_json=c.contract_json AND NEW.wallet_id=b.wallet_id
 AND NEW.policy_version=b.policy_version AND NEW.quote_block_number=b.quote_block_number AND NEW.quote_block_hash=b.quote_block_hash
 AND NEW.expires_at=b.expires_at AND NEW.created_at=b.checked_at AND NEW.updated_at=b.checked_at)
BEGIN SELECT RAISE(ABORT,'manual execution step requires binding'); END;
CREATE TRIGGER manual_execution_step_no_update BEFORE UPDATE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings WHERE execution_step_id=OLD.id)
BEGIN SELECT RAISE(ABORT,'manual execution downstream not connected'); END;
CREATE TRIGGER manual_execution_step_no_adoption BEFORE UPDATE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings WHERE operation_id=NEW.operation_id OR execution_step_id=NEW.id)
 AND NOT EXISTS(SELECT 1 FROM manual_http_order_sources WHERE step_id=NEW.id AND operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual execution step adoption forbidden'); END;
CREATE TRIGGER manual_execution_step_no_delete BEFORE DELETE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings WHERE execution_step_id=OLD.id)
BEGIN SELECT RAISE(ABORT,'manual execution step immutable'); END;
CREATE TRIGGER manual_execution_step_no_replace BEFORE INSERT ON execution_steps
WHEN EXISTS(SELECT 1 FROM execution_steps s JOIN manual_execution_bindings b ON b.execution_step_id=s.id
 WHERE s.id=NEW.id OR (s.operation_id=NEW.operation_id AND s.step_index=NEW.step_index))
BEGIN SELECT RAISE(ABORT,'manual execution step exists'); END;

CREATE TRIGGER manual_execution_lane_insert BEFORE INSERT ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
 AND NOT EXISTS(SELECT 1 FROM manual_execution_bindings b WHERE b.operation_id=NEW.operation_id AND b.wallet_id=NEW.wallet_id
 AND b.execution_step_id=NEW.step_id AND lower(b.wallet_address)=lower(NEW.address) AND NEW.state='reserved'
 AND NEW.reserved_nonce IS NULL AND NEW.freeze_reason IS NULL AND NEW.updated_at=b.checked_at)
BEGIN SELECT RAISE(ABORT,'manual lane requires nonce-free binding'); END;
CREATE TRIGGER manual_execution_lane_update BEFORE UPDATE ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
 AND NOT EXISTS(SELECT 1 FROM manual_execution_bindings b WHERE b.operation_id=NEW.operation_id AND b.wallet_id=NEW.wallet_id
 AND b.execution_step_id=NEW.step_id AND lower(b.wallet_address)=lower(NEW.address) AND NEW.state='reserved'
 AND NEW.reserved_nonce IS NULL AND NEW.freeze_reason IS NULL AND NEW.updated_at=b.checked_at)
BEGIN SELECT RAISE(ABORT,'manual lane requires nonce-free binding'); END;
CREATE TRIGGER manual_execution_lane_no_update BEFORE UPDATE ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings WHERE wallet_id=OLD.wallet_id AND execution_step_id=OLD.step_id)
BEGIN SELECT RAISE(ABORT,'manual execution lane downstream not connected'); END;
CREATE TRIGGER manual_execution_lane_no_delete BEFORE DELETE ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings WHERE wallet_id=OLD.wallet_id AND execution_step_id=OLD.step_id)
BEGIN SELECT RAISE(ABORT,'manual execution lane immutable'); END;
CREATE TRIGGER manual_execution_lane_no_replace BEFORE INSERT ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM execution_wallet_lanes l JOIN manual_execution_bindings b ON b.execution_step_id=l.step_id
 WHERE l.wallet_id=NEW.wallet_id OR lower(l.address)=lower(NEW.address))
BEGIN SELECT RAISE(ABORT,'manual execution lane exists'); END;
CREATE TRIGGER manual_execution_no_attempt BEFORE INSERT ON transaction_attempts
WHEN EXISTS(SELECT 1 FROM execution_steps s JOIN manual_execution_bindings b ON b.operation_id=s.operation_id WHERE s.id=NEW.step_id)
BEGIN SELECT RAISE(ABORT,'manual nonce/signing not connected'); END;
CREATE TRIGGER manual_execution_no_attempt_update BEFORE UPDATE ON transaction_attempts
WHEN EXISTS(SELECT 1 FROM execution_steps s JOIN manual_execution_bindings b ON b.operation_id=s.operation_id WHERE s.id=NEW.step_id)
BEGIN SELECT RAISE(ABORT,'manual nonce/signing not connected'); END;
CREATE TRIGGER manual_execution_no_financial_reservation BEFORE INSERT ON execution_reservations
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings WHERE operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual financial reservation already owned by typed admission'); END;
CREATE TRIGGER manual_execution_no_financial_reservation_update BEFORE UPDATE ON execution_reservations
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings WHERE operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual financial reservation adoption forbidden'); END;

CREATE TRIGGER manual_execution_audit_no_replace BEFORE INSERT ON canary_runtime_audit
WHEN EXISTS(SELECT 1 FROM canary_runtime_audit a JOIN manual_execution_bindings b ON a.id='manual-execution:'||b.operation_id WHERE a.id=NEW.id)
BEGIN SELECT RAISE(ABORT,'manual execution audit exists'); END;
CREATE TRIGGER manual_execution_alert_identity BEFORE UPDATE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings WHERE OLD.id='manual-execution-alert:'||operation_id)
 AND (NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id OR NEW.severity IS NOT OLD.severity OR NEW.created_at IS NOT OLD.created_at)
BEGIN SELECT RAISE(ABORT,'manual execution alert identity immutable'); END;
CREATE TRIGGER manual_execution_alert_no_delete BEFORE DELETE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings WHERE OLD.id='manual-execution-alert:'||operation_id)
BEGIN SELECT RAISE(ABORT,'manual execution alert immutable'); END;
CREATE TRIGGER manual_execution_alert_no_replace BEFORE INSERT ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_execution_bindings b ON a.id='manual-execution-alert:'||b.operation_id WHERE a.id=NEW.id)
BEGIN SELECT RAISE(ABORT,'manual execution alert exists'); END;

CREATE VIEW manual_execution_binding_complete AS
 SELECT b.* FROM manual_execution_bindings b
 JOIN manual_admission_complete c ON c.operation_id=b.operation_id AND c.request_id=b.request_id AND c.step_id=b.validation_step_id
 AND b.execution_step_id='manual-exec:'||c.operation_id AND b.wallet_id=json_extract(c.contract_json,'$.wallet_id')
 AND b.wallet_address=json_extract(c.contract_json,'$.wallet_address') AND b.chain_id=json_extract(c.contract_json,'$.chain_id')
 AND b.contract_hash=c.contract_hash AND b.policy_version=json_extract(c.contract_json,'$.policy_version')
 AND b.quote_block_number=json_extract(c.contract_json,'$.output.resolution.token.block.number')
 AND b.quote_block_hash=json_extract(c.contract_json,'$.output.resolution.token.block.hash')
 AND b.expires_at=json_extract(c.contract_json,'$.expires_at') AND b.authorization_id=c.authorization_id
 AND b.authorization_epoch=c.authorization_epoch AND b.manual_policy_id=c.manual_policy_id AND b.manual_policy_hash=c.manual_policy_hash
 AND b.gate_evidence_set_hash=c.gate_evidence_set_hash AND b.environment=c.environment
 AND b.build_sha=c.build_sha AND b.release_id=c.release_id AND b.deployment_id=c.deployment_id
 JOIN execution_steps s ON s.id=b.execution_step_id AND s.operation_id=b.operation_id AND s.step_index=1
 AND s.kind='manual_trade_execution' AND s.status='manual_execution_bound' AND s.wallet_id=b.wallet_id
 AND s.parameters_json=c.contract_json AND s.policy_version=b.policy_version AND s.quote_block_number=b.quote_block_number
 AND s.quote_block_hash=b.quote_block_hash AND s.expires_at=b.expires_at AND s.created_at=b.checked_at AND s.updated_at=b.checked_at
 JOIN execution_wallet_lanes l ON l.wallet_id=b.wallet_id AND lower(l.address)=lower(b.wallet_address)
 AND l.operation_id=b.operation_id AND l.step_id=b.execution_step_id AND l.state='reserved'
 AND l.reserved_nonce IS NULL AND l.freeze_reason IS NULL AND l.updated_at=b.checked_at
 JOIN canary_runtime_audit a ON a.id='manual-execution:'||b.operation_id AND a.event_type='MANUAL_EXECUTION_BOUND'
 AND a.operation_id=b.operation_id AND a.authorization_id=b.authorization_id AND a.authorization_epoch=b.authorization_epoch
 AND a.reason_code='BOUND_NOT_SIGNED_NOT_PERMISSION' AND a.created_at=b.checked_at
 AND json_extract(a.details_json,'$.request_id')=b.request_id AND json_extract(a.details_json,'$.execution_step_id')=b.execution_step_id
 AND json_extract(a.details_json,'$.contract_hash')=b.contract_hash AND json_extract(a.details_json,'$.reservation_id')=c.reservation_id
 JOIN canary_alert_outbox alert ON alert.id='manual-execution-alert:'||b.operation_id AND alert.audit_id=a.id
 AND alert.severity='INFO' AND alert.created_at=b.checked_at;

-- One INSERT owns all effects, including direct SQL. Audit failure or a skipped
-- OR IGNORE half aborts the entire statement; the Go writer rechecks before commit.
CREATE TRIGGER manual_execution_binding_effect AFTER INSERT ON manual_execution_bindings
BEGIN
 INSERT INTO execution_steps(id,operation_id,step_index,kind,status,parameters_json,created_at,updated_at,wallet_id,policy_version,quote_block_number,quote_block_hash,expires_at)
 SELECT NEW.execution_step_id,NEW.operation_id,1,'manual_trade_execution','manual_execution_bound',contract_json,
 NEW.checked_at,NEW.checked_at,NEW.wallet_id,NEW.policy_version,NEW.quote_block_number,NEW.quote_block_hash,NEW.expires_at
 FROM manual_admission_commands WHERE operation_id=NEW.operation_id;
 INSERT INTO execution_wallet_lanes(wallet_id,address,state,operation_id,step_id,reserved_nonce,freeze_reason,updated_at)
 VALUES(NEW.wallet_id,NEW.wallet_address,'reserved',NEW.operation_id,NEW.execution_step_id,NULL,NULL,NEW.checked_at)
 ON CONFLICT(wallet_id) DO UPDATE SET state='reserved',operation_id=excluded.operation_id,step_id=excluded.step_id,
 reserved_nonce=NULL,freeze_reason=NULL,updated_at=excluded.updated_at WHERE execution_wallet_lanes.state='idle' AND lower(execution_wallet_lanes.address)=lower(excluded.address);
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'manual-execution:'||NEW.operation_id,'MANUAL_EXECUTION_BOUND',NEW.operation_id,NEW.authorization_id,NEW.authorization_epoch,
 'BOUND_NOT_SIGNED_NOT_PERMISSION',json_object('request_id',NEW.request_id,'execution_step_id',NEW.execution_step_id,
 'contract_hash',NEW.contract_hash,'reservation_id',reservation_id),NEW.checked_at FROM manual_admission_commands WHERE operation_id=NEW.operation_id;
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-execution-alert:'||NEW.operation_id,'manual-execution:'||NEW.operation_id,'INFO','PENDING',NEW.checked_at);
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_execution_binding_complete WHERE operation_id=NEW.operation_id)
 THEN RAISE(ABORT,'manual execution binding incomplete effect') END;
END;
