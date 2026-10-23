-- M3-B2: one immutable coordination command, existing step/lane/attempt.
-- No second financial ledger/state machine, signing, artifact or send permit.
-- Keep 001..026 immutable and M2's initial-phase views unchanged.
CREATE TABLE manual_nonce_claims(
 operation_id TEXT NOT NULL PRIMARY KEY REFERENCES manual_execution_bindings(operation_id),
 execution_step_id TEXT NOT NULL UNIQUE REFERENCES execution_steps(id),
 attempt_id TEXT NOT NULL UNIQUE REFERENCES transaction_attempts(id) DEFERRABLE INITIALLY DEFERRED,
 wallet_id TEXT NOT NULL REFERENCES dry_run_wallets(id),
 wallet_address TEXT NOT NULL,
 nonce TEXT NOT NULL,
 reservation_id TEXT NOT NULL REFERENCES manual_admission_commands(reservation_id),
 contract_hash TEXT NOT NULL,
 observation_json TEXT NOT NULL CHECK(json_valid(observation_json)),
 observation_hash TEXT NOT NULL,
 checked_at TEXT NOT NULL,
 UNIQUE(wallet_id,nonce), UNIQUE(wallet_address,nonce)
);
CREATE TRIGGER manual_nonce_claim_no_update BEFORE UPDATE ON manual_nonce_claims
BEGIN SELECT RAISE(ABORT,'manual nonce claim immutable'); END;
CREATE TRIGGER manual_nonce_claim_no_delete BEFORE DELETE ON manual_nonce_claims
BEGIN SELECT RAISE(ABORT,'manual nonce claim immutable'); END;
CREATE TRIGGER manual_nonce_claim_insert_victim BEFORE INSERT ON manual_nonce_claims
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims q WHERE q.operation_id=NEW.operation_id
 OR q.execution_step_id=NEW.execution_step_id OR q.attempt_id=NEW.attempt_id OR q.rowid=NEW.rowid
 OR (q.wallet_id=NEW.wallet_id AND q.nonce=NEW.nonce) OR (q.wallet_address=NEW.wallet_address AND q.nonce=NEW.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce claim conflict victim protected'); END;

-- Pure scalar compares nonce, durable identity, SHA256, canonical times and
-- bounded query age without TEXT/INTEGER overflow or caller-supplied facts.
CREATE VIEW manual_nonce_binding_facts AS
SELECT b.operation_id,json_object('operation_id',b.operation_id,'request_id',b.request_id,
 'binding_hash',b.contract_hash,'chain_id',b.chain_id,'wallet_id',b.wallet_id,
 'wallet_address',lower(b.wallet_address),'contract_address',lower(json_extract(c.contract_json,'$.output.to')),
 'runtime_code_hash',json_extract(c.contract_json,'$.output.resolution.token.curve_runtime_code_hash'),
 'quote_block',b.quote_block_number,'quote_hash',b.quote_block_hash,'bound_at',b.checked_at,'expires_at',b.expires_at) AS facts_json
FROM manual_execution_bindings b JOIN manual_admission_commands c ON c.operation_id=b.operation_id;

CREATE TRIGGER manual_nonce_claim_source BEFORE INSERT ON manual_nonce_claims
WHEN NOT EXISTS(
 SELECT 1 FROM manual_execution_binding_integrity b JOIN manual_admission_commands c ON c.operation_id=b.operation_id
 JOIN manual_nonce_binding_facts f ON f.operation_id=b.operation_id
 JOIN canary_runtime_authorizations a ON a.id=b.authorization_id AND a.epoch=b.authorization_epoch
 JOIN canary_control_state ctl ON ctl.singleton=1
 JOIN canary_risk_reservations rr ON rr.id=c.reservation_id AND rr.state='reserved'
 JOIN canary_authorization_operation_usage u ON u.operation_id=b.operation_id AND u.authorization_id=a.id AND u.state='RESERVED'
 JOIN dry_run_wallets w ON w.id=b.wallet_id AND w.enabled=1 AND w.chain_id=4663
 JOIN canary_wallet_allowlist wa ON wa.wallet_id=w.id AND wa.chain_id=4663 AND wa.policy_version=b.policy_version
 WHERE b.operation_id=NEW.operation_id AND b.execution_step_id=NEW.execution_step_id
 AND NEW.attempt_id='manual-nonce:'||b.operation_id AND NEW.wallet_id=b.wallet_id
 AND NEW.wallet_address=lower(b.wallet_address) AND NEW.reservation_id=c.reservation_id AND NEW.contract_hash=b.contract_hash
 AND lower(w.address)=NEW.wallet_address AND wa.enabled=1 AND lower(wa.address)=NEW.wallet_address
 AND manual_nonce_observation_valid(NEW.observation_json,NEW.observation_hash,NEW.nonce,f.facts_json,NEW.checked_at)=1
 AND typeof(json_extract(c.contract_json,'$.output.gas_limit'))='integer'
 AND json_extract(c.contract_json,'$.output.gas_limit')>0
 AND a.state='ARMED' AND a.mode='CONTROLLED_CANARY' AND a.chain_id=b.chain_id AND a.wallet_id=b.wallet_id
 AND a.policy_version=b.policy_version AND a.environment=b.environment AND a.build_sha=b.build_sha
 AND a.release_id=b.release_id AND a.deployment_id=b.deployment_id AND a.gate_evidence_set_hash=b.gate_evidence_set_hash
 AND ctl.emergency_stopped=0 AND ctl.mode='CONTROLLED_CANARY' AND ctl.chain_id=4663 AND ctl.policy_version=b.policy_version
 AND manual_time_cmp(NEW.checked_at,a.authorized_at)>=0 AND manual_time_cmp(NEW.checked_at,a.created_at)>=0
 AND manual_time_cmp(NEW.checked_at,a.updated_at)>=0 AND manual_time_cmp(NEW.checked_at,a.expires_at)<0
 AND manual_time_cmp(NEW.checked_at,ctl.effective_at)>=0 AND manual_time_cmp(NEW.checked_at,ctl.updated_at)>=0
 AND EXISTS(SELECT 1 FROM canary_contract_allowlist ca JOIN canary_token_allowlist ta ON ta.policy_version=ca.policy_version
 WHERE ca.policy_version=b.policy_version AND ca.role='CURVE' AND ca.protocol='PONS_V2_CURVE' AND ca.enabled=1 AND ca.baseline_ref!=''
 AND lower(ca.address)=json_extract(c.contract_json,'$.output.resolution.token.curve')
 AND ca.runtime_code_hash=json_extract(c.contract_json,'$.output.resolution.token.curve_runtime_code_hash')
 AND ta.protocol='PONS_V2_CURVE' AND ta.enabled=1 AND lower(ta.token_address)=json_extract(c.contract_json,'$.output.resolution.token.address'))
 AND (SELECT COUNT(*) FROM canary_gate_attestations g WHERE g.status='PASS' AND manual_time_cmp(NEW.checked_at,g.attested_at)>=0
 AND ((g.gate='C04' AND g.evidence_hash='c1af9454a6a4433b488158c25224d13dfbf31178c71dae7468c80917743381fd')
 OR (g.gate='C05' AND g.evidence_hash='1a793f13d9304a31ed405f9fe25f1713cf79b6f8d28bb3da8ad16a01dc24c40b')
 OR (g.gate='C08' AND g.evidence_hash='8178e7b976dbea67be1de83cec529171e56d154cd2114f1bf290bebbd1fc77d8')))=3
 AND EXISTS(SELECT 1 FROM canary_authorization_usage usage WHERE usage.authorization_id=a.id
 AND usage.operation_count<=a.max_operations AND manual_u256_cmp(usage.total_input,a.max_total_input)<=0
 AND usage.operation_count=(SELECT COUNT(*) FROM canary_authorization_operation_usage WHERE authorization_id=a.id)
 AND usage.operation_count=(SELECT COUNT(*) FROM manual_admission_complete WHERE authorization_id=a.id)
 AND usage.total_input=(SELECT manual_u256_sum_json(json_group_array(amount)) FROM canary_authorization_operation_usage WHERE authorization_id=a.id))
)
BEGIN SELECT RAISE(ABORT,'manual nonce source/control/evidence invalid'); END;
CREATE TRIGGER manual_nonce_claim_no_halves BEFORE INSERT ON manual_nonce_claims
WHEN EXISTS(SELECT 1 FROM transaction_attempts t JOIN execution_steps s ON s.id=t.step_id JOIN operations own ON own.id=s.operation_id
 WHERE s.operation_id=NEW.operation_id OR t.id=NEW.attempt_id
 OR (own.chain_id=4663 AND (t.wallet_id=NEW.wallet_id OR s.wallet_id=NEW.wallet_id OR own.wallet_id=NEW.wallet_id
 OR lower(t.from_address)=NEW.wallet_address) AND t.nonce=NEW.nonce))
 OR EXISTS(SELECT 1 FROM execution_reservations WHERE wallet_id=NEW.wallet_id AND status IN ('reserved','signed','frozen'))
 OR EXISTS(SELECT 1 FROM canary_risk_reservations WHERE wallet_id=NEW.wallet_id AND operation_id!=NEW.operation_id AND state IN ('reserved','frozen'))
 OR EXISTS(SELECT 1 FROM execution_steps s JOIN operations o ON o.id=s.operation_id
 WHERE (s.wallet_id=NEW.wallet_id OR o.wallet_id=NEW.wallet_id) AND s.id!=NEW.execution_step_id
 AND s.status IN ('manual_execution_bound','nonce_reserved','signing','signed','submitted','broadcast_unknown','reconciling','manual_resolution'))
 OR EXISTS(SELECT 1 FROM canary_runtime_audit WHERE id='manual-nonce-audit:'||NEW.operation_id
 OR (event_type='MANUAL_NONCE_CLAIMED' AND operation_id=NEW.operation_id))
 OR EXISTS(SELECT 1 FROM canary_alert_outbox WHERE id='manual-nonce-alert:'||NEW.operation_id OR audit_id='manual-nonce-audit:'||NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual nonce preexisting half or wallet work'); END;

-- A serialization of EVERY attempt column prevents NULL/raw/fee/identity holes.
CREATE VIEW manual_nonce_expected_attempt AS
SELECT q.operation_id,q.attempt_id,json_object('id',q.attempt_id,'step_id',q.execution_step_id,'nonce',q.nonce,
 'tx_hash',NULL,'status','prepared','created_at',q.checked_at,'chain_id',b.chain_id,'wallet_id',b.wallet_id,
 'key_version',NULL,'encryption_nonce',NULL,'encrypted_raw_tx',NULL,'raw_tx_hash',NULL,
 'from_address',json_extract(c.contract_json,'$.output.from'),'to_address',json_extract(c.contract_json,'$.output.to'),
 'value',json_extract(c.contract_json,'$.output.value'),'calldata',json_extract(c.contract_json,'$.output.data'),
 'gas_limit',CAST(json_extract(c.contract_json,'$.output.gas_limit') AS TEXT),
 'gas_tip_cap',json_extract(c.contract_json,'$.output.gas_tip_cap'),'gas_fee_cap',json_extract(c.contract_json,'$.output.gas_fee_cap'),
 'updated_at',q.checked_at,'policy_version',b.policy_version,'quote_block_number',b.quote_block_number,
 'quote_block_hash',b.quote_block_hash,'expires_at',b.expires_at) AS attempt_json
FROM manual_nonce_claims q JOIN manual_execution_bindings b ON b.operation_id=q.operation_id
JOIN manual_admission_commands c ON c.operation_id=q.operation_id;
CREATE VIEW manual_nonce_actual_attempt AS
SELECT t.rowid,t.id,t.step_id,json_object('id',t.id,'step_id',t.step_id,'nonce',t.nonce,
 'tx_hash',t.tx_hash,'status',t.status,'created_at',t.created_at,'chain_id',t.chain_id,'wallet_id',t.wallet_id,
 'key_version',t.key_version,'encryption_nonce',CASE WHEN t.encryption_nonce IS NULL THEN NULL ELSE hex(t.encryption_nonce) END,
 'encrypted_raw_tx',CASE WHEN t.encrypted_raw_tx IS NULL THEN NULL ELSE hex(t.encrypted_raw_tx) END,'raw_tx_hash',t.raw_tx_hash,
 'from_address',t.from_address,'to_address',t.to_address,'value',t.value,'calldata',t.calldata,'gas_limit',t.gas_limit,
 'gas_tip_cap',t.gas_tip_cap,'gas_fee_cap',t.gas_fee_cap,'updated_at',t.updated_at,'policy_version',t.policy_version,
 'quote_block_number',t.quote_block_number,'quote_block_hash',t.quote_block_hash,'expires_at',t.expires_at) AS attempt_json
FROM transaction_attempts t;

-- These views exist ONLY during the single statement's exact initial -> claimed
-- transition. Afterwards OLD phase/nonce no longer matches, so no reset/reuse.
CREATE VIEW manual_nonce_step_transition AS
SELECT q.operation_id,s.id,s.rowid,json_object('id',s.id,'operation_id',s.operation_id,'step_index',s.step_index,'kind',s.kind,
 'status','nonce_reserved','route_json',s.route_json,'parameters_json',s.parameters_json,'unsigned_call_json',s.unsigned_call_json,
 'created_at',s.created_at,'updated_at',q.checked_at,'wallet_id',s.wallet_id,'policy_version',s.policy_version,
 'quote_block_number',s.quote_block_number,'quote_block_hash',s.quote_block_hash,'expires_at',s.expires_at) AS step_json
FROM manual_nonce_claims q JOIN execution_steps s ON s.id=q.execution_step_id AND s.status='manual_execution_bound'
JOIN manual_execution_bindings b ON b.operation_id=q.operation_id AND s.updated_at=b.checked_at
JOIN manual_nonce_expected_attempt e ON e.operation_id=q.operation_id
JOIN manual_nonce_actual_attempt t ON t.id=q.attempt_id AND t.attempt_json=e.attempt_json;
CREATE VIEW manual_nonce_lane_transition AS
SELECT q.operation_id,l.wallet_id,l.rowid,json_object('wallet_id',l.wallet_id,'address',l.address,'state','reserved',
 'operation_id',l.operation_id,'step_id',l.step_id,'reserved_nonce',q.nonce,'freeze_reason',NULL,'updated_at',q.checked_at) AS lane_json
FROM manual_nonce_claims q JOIN manual_execution_bindings b ON b.operation_id=q.operation_id
JOIN execution_wallet_lanes l ON l.wallet_id=b.wallet_id AND lower(l.address)=lower(b.wallet_address)
 AND l.operation_id=b.operation_id AND l.step_id=b.execution_step_id AND l.state='reserved'
 AND l.reserved_nonce IS NULL AND l.freeze_reason IS NULL AND l.updated_at=b.checked_at
JOIN execution_steps s ON s.id=q.execution_step_id AND s.status='nonce_reserved' AND s.updated_at=q.checked_at
JOIN manual_nonce_expected_attempt e ON e.operation_id=q.operation_id
JOIN manual_nonce_actual_attempt t ON t.id=q.attempt_id AND t.attempt_json=e.attempt_json;

-- Replace only the named initial-phase guards, preserving their original deny
-- domains. The added exception is an exact command-owned one-way transition.
DROP TRIGGER manual_execution_step_no_update;
CREATE TRIGGER manual_execution_step_no_update BEFORE UPDATE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings WHERE execution_step_id=OLD.id)
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_step_transition n WHERE n.id=OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.step_json=json_object('id',NEW.id,'operation_id',NEW.operation_id,'step_index',NEW.step_index,'kind',NEW.kind,
 'status',NEW.status,'route_json',NEW.route_json,'parameters_json',NEW.parameters_json,'unsigned_call_json',NEW.unsigned_call_json,
 'created_at',NEW.created_at,'updated_at',NEW.updated_at,'wallet_id',NEW.wallet_id,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at))
BEGIN SELECT RAISE(ABORT,'manual execution step exact claim only'); END;
DROP TRIGGER manual_execution_step_no_adoption;
CREATE TRIGGER manual_execution_step_no_adoption BEFORE UPDATE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings WHERE operation_id=NEW.operation_id OR execution_step_id=NEW.id)
 AND NOT EXISTS(SELECT 1 FROM manual_http_order_sources WHERE step_id=NEW.id AND operation_id=NEW.operation_id)
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_step_transition n WHERE n.id=OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.step_json=json_object('id',NEW.id,'operation_id',NEW.operation_id,'step_index',NEW.step_index,'kind',NEW.kind,
 'status',NEW.status,'route_json',NEW.route_json,'parameters_json',NEW.parameters_json,'unsigned_call_json',NEW.unsigned_call_json,
 'created_at',NEW.created_at,'updated_at',NEW.updated_at,'wallet_id',NEW.wallet_id,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at))
BEGIN SELECT RAISE(ABORT,'manual execution step adoption forbidden'); END;

DROP TRIGGER manual_execution_no_attempt;
CREATE TRIGGER manual_execution_no_attempt BEFORE INSERT ON transaction_attempts
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE EXISTS(SELECT 1 FROM execution_steps s JOIN operations own ON own.id=s.operation_id
 WHERE s.id=NEW.step_id AND (s.operation_id=b.operation_id OR s.wallet_id=b.wallet_id OR own.wallet_id=b.wallet_id))
 OR NEW.wallet_id=b.wallet_id OR lower(NEW.from_address)=lower(b.wallet_address))
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_expected_attempt e WHERE e.attempt_id=NEW.id
 AND e.attempt_json=json_object('id',NEW.id,'step_id',NEW.step_id,'nonce',NEW.nonce,'tx_hash',NEW.tx_hash,
 'status',NEW.status,'created_at',NEW.created_at,'chain_id',NEW.chain_id,'wallet_id',NEW.wallet_id,
 'key_version',NEW.key_version,'encryption_nonce',NEW.encryption_nonce,'encrypted_raw_tx',NEW.encrypted_raw_tx,'raw_tx_hash',NEW.raw_tx_hash,
 'from_address',NEW.from_address,'to_address',NEW.to_address,'value',NEW.value,'calldata',NEW.calldata,'gas_limit',NEW.gas_limit,
 'gas_tip_cap',NEW.gas_tip_cap,'gas_fee_cap',NEW.gas_fee_cap,'updated_at',NEW.updated_at,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at))
BEGIN SELECT RAISE(ABORT,'manual attempt requires exact nonce claim'); END;

-- Use a narrow transition predicate via a BEFORE-UPDATE guard and the row's
-- prospective JSON; retaining each original trigger name keeps compatibility.
DROP TRIGGER manual_execution_lane_update;
DROP TRIGGER manual_execution_lane_no_update;
DROP TRIGGER manual_binding_lane_reference_update;
DROP TRIGGER manual_binding_lane_retained_update;
CREATE TRIGGER manual_execution_lane_update BEFORE UPDATE ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
 AND NOT EXISTS(SELECT 1 FROM manual_execution_bindings b WHERE b.operation_id=NEW.operation_id AND b.wallet_id=NEW.wallet_id
 AND b.execution_step_id=NEW.step_id AND lower(b.wallet_address)=lower(NEW.address) AND NEW.state='reserved'
 AND NEW.reserved_nonce IS NULL AND NEW.freeze_reason IS NULL AND NEW.updated_at=b.checked_at)
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_lane_transition n WHERE n.wallet_id=OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual lane exact nonce transition only'); END;
CREATE TRIGGER manual_execution_lane_no_update BEFORE UPDATE ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings WHERE wallet_id=OLD.wallet_id AND execution_step_id=OLD.step_id)
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_lane_transition n WHERE n.wallet_id=OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual lane downstream not connected'); END;
CREATE TRIGGER manual_binding_lane_reference_update BEFORE UPDATE ON execution_wallet_lanes
WHEN (EXISTS(SELECT 1 FROM manual_execution_bindings b
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
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual lane reference mutation forbidden'); END;
CREATE TRIGGER manual_binding_lane_retained_update BEFORE UPDATE ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
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
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual retained lane mutation forbidden'); END;

-- Implicit rowid REPLACE victims, including recursive_triggers OFF, must be
-- checked before SQLite can erase authoritative attempt/step identities.
CREATE TRIGGER manual_nonce_attempt_insert_victim BEFORE INSERT ON transaction_attempts
WHEN EXISTS(SELECT 1 FROM transaction_attempts t JOIN execution_steps s ON s.id=t.step_id
 JOIN manual_execution_bindings b ON b.operation_id=s.operation_id WHERE t.id=NEW.id OR t.rowid=NEW.rowid)
 OR EXISTS(SELECT 1 FROM manual_nonce_claims q WHERE (NEW.id=q.attempt_id OR NEW.step_id=q.execution_step_id)
 AND EXISTS(SELECT 1 FROM transaction_attempts WHERE id=q.attempt_id))
BEGIN SELECT RAISE(ABORT,'manual attempt conflict victim protected'); END;
CREATE TRIGGER manual_nonce_attempt_update_victim BEFORE UPDATE ON transaction_attempts
WHEN EXISTS(SELECT 1 FROM transaction_attempts t JOIN execution_steps s ON s.id=t.step_id
 JOIN manual_execution_bindings b ON b.operation_id=s.operation_id
 WHERE t.rowid!=OLD.rowid AND (t.id=NEW.id OR t.rowid=NEW.rowid))
 OR EXISTS(SELECT 1 FROM manual_nonce_claims q WHERE q.attempt_id=OLD.id OR q.attempt_id=NEW.id
 OR ((OLD.wallet_id=q.wallet_id OR lower(OLD.from_address)=q.wallet_address) AND OLD.nonce=q.nonce)
 OR ((NEW.wallet_id=q.wallet_id OR lower(NEW.from_address)=q.wallet_address) AND NEW.nonce=q.nonce)
 OR EXISTS(SELECT 1 FROM execution_steps s JOIN operations own ON own.id=s.operation_id
 WHERE (s.wallet_id=q.wallet_id OR own.wallet_id=q.wallet_id)
 AND ((s.id=OLD.step_id AND OLD.nonce=q.nonce) OR (s.id=NEW.step_id AND NEW.nonce=q.nonce))))
BEGIN SELECT RAISE(ABORT,'manual claimed attempt immutable'); END;
CREATE TRIGGER manual_nonce_attempt_no_delete BEFORE DELETE ON transaction_attempts
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b JOIN execution_steps s ON s.operation_id=b.operation_id WHERE s.id=OLD.step_id)
 OR EXISTS(SELECT 1 FROM manual_nonce_claims q JOIN execution_steps s ON s.id=OLD.step_id
 JOIN operations own ON own.id=s.operation_id WHERE (s.wallet_id=q.wallet_id OR own.wallet_id=q.wallet_id
 OR OLD.wallet_id=q.wallet_id OR lower(OLD.from_address)=q.wallet_address) AND OLD.nonce=q.nonce)
BEGIN SELECT RAISE(ABORT,'manual attempt requires explicit future lifecycle'); END;
CREATE TRIGGER manual_nonce_step_insert_victim BEFORE INSERT ON execution_steps
WHEN EXISTS(SELECT 1 FROM execution_steps s JOIN manual_execution_bindings b ON b.execution_step_id=s.id
 WHERE s.rowid=NEW.rowid OR s.id=NEW.id OR (s.operation_id=NEW.operation_id AND s.step_index=NEW.step_index))
BEGIN SELECT RAISE(ABORT,'manual step conflict victim protected'); END;
CREATE TRIGGER manual_nonce_step_update_victim BEFORE UPDATE ON execution_steps
WHEN EXISTS(SELECT 1 FROM execution_steps s JOIN manual_execution_bindings b ON b.execution_step_id=s.id
 WHERE s.rowid!=OLD.rowid AND (s.rowid=NEW.rowid OR s.id=NEW.id OR (s.operation_id=NEW.operation_id AND s.step_index=NEW.step_index)))
BEGIN SELECT RAISE(ABORT,'manual step update conflict victim protected'); END;

CREATE TRIGGER manual_nonce_audit_insert BEFORE INSERT ON canary_runtime_audit
WHEN (NEW.event_type='MANUAL_NONCE_CLAIMED' OR substr(NEW.id,1,length('manual-nonce-audit:'))='manual-nonce-audit:')
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_claims q JOIN manual_execution_bindings b ON b.operation_id=q.operation_id
 WHERE NEW.id='manual-nonce-audit:'||q.operation_id AND NEW.event_type='MANUAL_NONCE_CLAIMED'
 AND NEW.operation_id IS q.operation_id AND NEW.attempt_id IS q.attempt_id
 AND NEW.authorization_id IS b.authorization_id AND NEW.authorization_epoch IS b.authorization_epoch
 AND NEW.reason_code IS 'NONCE_CLAIMED_NOT_SIGNED_NOT_PERMISSION' AND NEW.created_at IS q.checked_at
 AND NEW.details_json IS json_object('execution_step_id',q.execution_step_id,'nonce',q.nonce,'contract_hash',q.contract_hash,
 'reservation_id',q.reservation_id,'observation_hash',q.observation_hash))
BEGIN SELECT RAISE(ABORT,'manual nonce audit exact identity required'); END;
CREATE TRIGGER manual_nonce_audit_insert_victim BEFORE INSERT ON canary_runtime_audit
WHEN EXISTS(SELECT 1 FROM canary_runtime_audit a JOIN manual_nonce_claims q
 ON a.id='manual-nonce-audit:'||q.operation_id OR (a.event_type='MANUAL_NONCE_CLAIMED' AND a.operation_id=q.operation_id)
 WHERE a.id=NEW.id OR a.rowid=NEW.rowid)
BEGIN SELECT RAISE(ABORT,'manual nonce audit conflict victim protected'); END;
CREATE TRIGGER manual_nonce_alert_insert_victim BEFORE INSERT ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_nonce_claims q
 ON a.id='manual-nonce-alert:'||q.operation_id OR a.audit_id='manual-nonce-audit:'||q.operation_id
 WHERE a.id=NEW.id OR a.audit_id=NEW.audit_id OR a.rowid=NEW.rowid)
BEGIN SELECT RAISE(ABORT,'manual nonce alert conflict victim protected'); END;
CREATE TRIGGER manual_nonce_alert_update BEFORE UPDATE ON canary_alert_outbox
WHEN (EXISTS(SELECT 1 FROM manual_nonce_claims q
 WHERE OLD.id='manual-nonce-alert:'||q.operation_id OR OLD.audit_id='manual-nonce-audit:'||q.operation_id)
 AND (NEW.rowid IS NOT OLD.rowid OR NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id
 OR NEW.severity IS NOT OLD.severity OR NEW.created_at IS NOT OLD.created_at))
 OR EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_nonce_claims q
 ON a.id='manual-nonce-alert:'||q.operation_id OR a.audit_id='manual-nonce-audit:'||q.operation_id
 WHERE a.rowid!=OLD.rowid AND (a.id=NEW.id OR a.audit_id=NEW.audit_id OR a.rowid=NEW.rowid))
BEGIN SELECT RAISE(ABORT,'manual nonce alert identity/victim immutable'); END;
CREATE TRIGGER manual_nonce_alert_no_delete BEFORE DELETE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims q WHERE OLD.id='manual-nonce-alert:'||q.operation_id OR OLD.audit_id='manual-nonce-audit:'||q.operation_id)
BEGIN SELECT RAISE(ABORT,'manual nonce alert immutable'); END;

-- B2 has no signed artifact. SQL cannot invent submission/recovery effects for
-- this prepared attempt, or silently release the sole accounting owner.
CREATE TRIGGER manual_nonce_no_submission_insert BEFORE INSERT ON transaction_submissions
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE attempt_id=NEW.attempt_id)
BEGIN SELECT RAISE(ABORT,'manual submission not connected'); END;
CREATE TRIGGER manual_nonce_no_submission_update BEFORE UPDATE ON transaction_submissions
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE attempt_id IN (OLD.attempt_id,NEW.attempt_id))
BEGIN SELECT RAISE(ABORT,'manual submission not connected'); END;
CREATE TRIGGER manual_nonce_no_receipt_insert BEFORE INSERT ON receipt_observations
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE attempt_id=NEW.attempt_id)
BEGIN SELECT RAISE(ABORT,'manual signed receipt not available'); END;
CREATE TRIGGER manual_nonce_no_receipt_update BEFORE UPDATE ON receipt_observations
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE attempt_id IN (OLD.attempt_id,NEW.attempt_id))
BEGIN SELECT RAISE(ABORT,'manual signed receipt not available'); END;
CREATE TRIGGER manual_nonce_no_effect_insert BEFORE INSERT ON position_effects
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE attempt_id=NEW.attempt_id)
BEGIN SELECT RAISE(ABORT,'manual signed position effect not available'); END;
CREATE TRIGGER manual_nonce_no_effect_update BEFORE UPDATE ON position_effects
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE attempt_id IN (OLD.attempt_id,NEW.attempt_id))
BEGIN SELECT RAISE(ABORT,'manual signed position effect not available'); END;
CREATE TRIGGER manual_nonce_risk_no_release BEFORE UPDATE ON canary_risk_reservations
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE reservation_id=OLD.id)
 AND NOT (NEW.state IS OLD.state OR (OLD.state='reserved' AND NEW.state='frozen'))
BEGIN SELECT RAISE(ABORT,'claimed manual risk cannot release or thaw'); END;
CREATE TRIGGER manual_nonce_usage_no_release BEFORE UPDATE ON canary_authorization_operation_usage
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE operation_id=OLD.operation_id)
 AND NOT (NEW.state IS OLD.state OR (OLD.state='RESERVED' AND NEW.state='FROZEN'))
BEGIN SELECT RAISE(ABORT,'claimed manual usage cannot release or thaw'); END;

-- Immutable M2 fact identity/audit plus exact CLAIMED effects, not the old
-- initial-only view. No financial/lane/attempt aliases or reconstructed history.
CREATE VIEW manual_nonce_claim_complete AS
SELECT q.* FROM manual_nonce_claims q
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
 AND s.kind='manual_trade_execution' AND s.wallet_id=q.wallet_id AND s.status='nonce_reserved'
 AND s.parameters_json=c.contract_json AND s.route_json IS NULL AND s.unsigned_call_json IS NULL
 AND s.created_at=b.checked_at AND s.updated_at=q.checked_at AND s.policy_version=b.policy_version
 AND s.quote_block_number=b.quote_block_number AND s.quote_block_hash=b.quote_block_hash AND s.expires_at=b.expires_at
JOIN execution_wallet_lanes l ON l.wallet_id=q.wallet_id AND lower(l.address)=q.wallet_address
 AND l.operation_id=q.operation_id AND l.step_id=q.execution_step_id AND l.state='reserved'
 AND l.reserved_nonce=q.nonce AND l.freeze_reason IS NULL AND l.updated_at=q.checked_at
JOIN manual_nonce_expected_attempt e ON e.operation_id=q.operation_id
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
 AND NOT EXISTS(SELECT 1 FROM transaction_submissions WHERE attempt_id=q.attempt_id)
 AND NOT EXISTS(SELECT 1 FROM receipt_observations WHERE attempt_id=q.attempt_id)
 AND NOT EXISTS(SELECT 1 FROM position_effects WHERE attempt_id=q.attempt_id)
 AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit extra WHERE extra.operation_id=b.operation_id
 AND ((extra.event_type='MANUAL_EXECUTION_BOUND' AND extra.id IS NOT old.id) OR (extra.event_type='MANUAL_NONCE_CLAIMED' AND extra.id IS NOT a.id)));

CREATE TRIGGER manual_nonce_claim_effect AFTER INSERT ON manual_nonce_claims
BEGIN
 INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at,chain_id,wallet_id,from_address,to_address,value,calldata,
 gas_limit,gas_tip_cap,gas_fee_cap,updated_at,policy_version,quote_block_number,quote_block_hash,expires_at)
 SELECT NEW.attempt_id,NEW.execution_step_id,NEW.nonce,'prepared',NEW.checked_at,b.chain_id,b.wallet_id,
 json_extract(c.contract_json,'$.output.from'),json_extract(c.contract_json,'$.output.to'),json_extract(c.contract_json,'$.output.value'),
 json_extract(c.contract_json,'$.output.data'),CAST(json_extract(c.contract_json,'$.output.gas_limit') AS TEXT),
 json_extract(c.contract_json,'$.output.gas_tip_cap'),json_extract(c.contract_json,'$.output.gas_fee_cap'),NEW.checked_at,
 b.policy_version,b.quote_block_number,b.quote_block_hash,b.expires_at
 FROM manual_execution_bindings b JOIN manual_admission_commands c ON c.operation_id=b.operation_id WHERE b.operation_id=NEW.operation_id;
 UPDATE execution_steps SET status='nonce_reserved',updated_at=NEW.checked_at WHERE id=NEW.execution_step_id;
 UPDATE execution_wallet_lanes SET reserved_nonce=NEW.nonce,updated_at=NEW.checked_at WHERE wallet_id=NEW.wallet_id;
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'manual-nonce-audit:'||NEW.operation_id,'MANUAL_NONCE_CLAIMED',NEW.operation_id,NEW.attempt_id,b.authorization_id,b.authorization_epoch,
 'NONCE_CLAIMED_NOT_SIGNED_NOT_PERMISSION',json_object('execution_step_id',NEW.execution_step_id,'nonce',NEW.nonce,'contract_hash',NEW.contract_hash,
 'reservation_id',NEW.reservation_id,'observation_hash',NEW.observation_hash),NEW.checked_at FROM manual_execution_bindings b WHERE b.operation_id=NEW.operation_id;
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-nonce-alert:'||NEW.operation_id,'manual-nonce-audit:'||NEW.operation_id,'INFO','PENDING',NEW.checked_at);
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_nonce_claim_complete WHERE operation_id=NEW.operation_id)
 THEN RAISE(ABORT,'manual nonce claim incomplete effect') END;
END;
