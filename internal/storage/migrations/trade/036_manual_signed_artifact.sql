-- M3-B3-B2: exact metadata/artifact phase only, no signer or send wiring.
-- Preserve 001..035. Schema completeness is NOT cryptographic validity.
CREATE TABLE manual_signed_artifact_commands(
 operation_id TEXT NOT NULL PRIMARY KEY REFERENCES manual_signing_intents(operation_id),
 owner_id TEXT NOT NULL,
 facts_json TEXT NOT NULL,
 facts_hash TEXT NOT NULL,
 envelope_json TEXT NOT NULL,
 observation_json TEXT NOT NULL,
 observation_hash TEXT NOT NULL,
 checked_at TEXT NOT NULL
);
CREATE TRIGGER manual_artifact_command_no_update BEFORE UPDATE ON manual_signed_artifact_commands
BEGIN SELECT RAISE(ABORT,'manual artifact command immutable'); END;
CREATE TRIGGER manual_artifact_command_no_delete BEFORE DELETE ON manual_signed_artifact_commands
BEGIN SELECT RAISE(ABORT,'manual artifact command cannot be discarded'); END;
CREATE TRIGGER manual_artifact_command_no_replace BEFORE INSERT ON manual_signed_artifact_commands
WHEN EXISTS(SELECT 1 FROM manual_signed_artifact_commands m WHERE hex(m.operation_id)=hex(NEW.operation_id)
 OR m.rowid=NEW.rowid OR hex(CASE WHEN json_valid(m.facts_json) THEN json_extract(m.facts_json,'$.operation_id') END)=hex(NEW.operation_id))
BEGIN SELECT RAISE(ABORT,'manual artifact retained command conflict'); END;

CREATE TABLE manual_artifact_effect_scopes(
 operation_id TEXT NOT NULL PRIMARY KEY REFERENCES manual_signed_artifact_commands(operation_id) DEFERRABLE INITIALLY DEFERRED,
 owner_id TEXT NOT NULL,
 facts_hash TEXT NOT NULL
);
CREATE TRIGGER manual_artifact_scope_source BEFORE INSERT ON manual_artifact_effect_scopes
WHEN EXISTS(SELECT 1 FROM manual_signed_artifact_commands m WHERE hex(m.operation_id)=hex(NEW.operation_id))
 OR EXISTS(SELECT 1 FROM manual_artifact_effect_scopes x WHERE hex(x.operation_id)=hex(NEW.operation_id) OR x.rowid=NEW.rowid)
 OR NOT EXISTS(SELECT 1 FROM manual_signing_intent_complete i WHERE i.operation_id IS NEW.operation_id AND i.owner_id IS NEW.owner_id AND i.facts_hash IS NEW.facts_hash)
BEGIN SELECT RAISE(ABORT,'manual artifact scope cannot reopen or adopt'); END;
CREATE TRIGGER manual_artifact_scope_no_update BEFORE UPDATE ON manual_artifact_effect_scopes
BEGIN SELECT RAISE(ABORT,'manual artifact effect scope immutable'); END;
CREATE TRIGGER manual_artifact_scope_open BEFORE INSERT ON manual_signed_artifact_commands
BEGIN
 INSERT INTO manual_artifact_effect_scopes VALUES(NEW.operation_id,NEW.owner_id,NEW.facts_hash);
END;

CREATE VIEW manual_artifact_expected_attempt AS
SELECT m.operation_id,q.attempt_id,json_set(e.attempt_json,
 '$.tx_hash',json_extract(m.envelope_json,'$.tx_hash'),'$.status','signed',
 '$.key_version',json_extract(m.envelope_json,'$.key_version'),
 '$.encryption_nonce',upper(json_extract(m.envelope_json,'$.encryption_nonce')),
 '$.encrypted_raw_tx',upper(json_extract(m.envelope_json,'$.ciphertext')),
 '$.raw_tx_hash',json_extract(m.envelope_json,'$.raw_sha256'),'$.updated_at',m.checked_at) AS attempt_json
FROM manual_signed_artifact_commands m JOIN manual_nonce_claims q ON q.operation_id=m.operation_id
JOIN manual_nonce_expected_attempt e ON e.operation_id=m.operation_id;
CREATE VIEW manual_artifact_expected_step AS
SELECT m.operation_id,q.execution_step_id AS id,json_object('id',q.execution_step_id,'operation_id',q.operation_id,'step_index',1,'kind','manual_trade_execution',
 'status','signed','route_json',NULL,'parameters_json',c.contract_json,'unsigned_call_json',NULL,
 'created_at',b.checked_at,'updated_at',m.checked_at,'wallet_id',q.wallet_id,'policy_version',b.policy_version,
 'quote_block_number',b.quote_block_number,'quote_block_hash',b.quote_block_hash,'expires_at',b.expires_at) AS step_json
FROM manual_signed_artifact_commands m JOIN manual_nonce_claims q ON q.operation_id=m.operation_id
JOIN manual_execution_bindings b ON b.operation_id=m.operation_id JOIN manual_admission_commands c ON c.operation_id=m.operation_id;
CREATE VIEW manual_artifact_actual_step AS
SELECT s.rowid,s.id,json_object('id',s.id,'operation_id',s.operation_id,'step_index',s.step_index,'kind',s.kind,
 'status',s.status,'route_json',s.route_json,'parameters_json',s.parameters_json,'unsigned_call_json',s.unsigned_call_json,
 'created_at',s.created_at,'updated_at',s.updated_at,'wallet_id',s.wallet_id,'policy_version',s.policy_version,
 'quote_block_number',s.quote_block_number,'quote_block_hash',s.quote_block_hash,'expires_at',s.expires_at) AS step_json FROM execution_steps s;
CREATE VIEW manual_artifact_expected_lane AS
SELECT m.operation_id,q.wallet_id,json_object('wallet_id',q.wallet_id,'address',b.wallet_address,'state','signed',
 'operation_id',q.operation_id,'step_id',q.execution_step_id,'reserved_nonce',q.nonce,'freeze_reason',NULL,'updated_at',m.checked_at) AS lane_json
FROM manual_signed_artifact_commands m JOIN manual_nonce_claims q ON q.operation_id=m.operation_id
JOIN manual_execution_bindings b ON b.operation_id=m.operation_id;
CREATE VIEW manual_artifact_actual_lane AS
SELECT l.rowid,l.wallet_id,json_object('wallet_id',l.wallet_id,'address',l.address,'state',l.state,'operation_id',l.operation_id,
 'step_id',l.step_id,'reserved_nonce',l.reserved_nonce,'freeze_reason',l.freeze_reason,'updated_at',l.updated_at) AS lane_json FROM execution_wallet_lanes l;

-- Each exception verifies EVERY old column and the exact prospective row.
-- Scope exists only inside the single artifact command statement.
CREATE VIEW manual_artifact_attempt_transition AS
SELECT e.operation_id,t.id,t.rowid,e.attempt_json
FROM manual_artifact_expected_attempt e JOIN manual_signed_artifact_commands m ON m.operation_id=e.operation_id
JOIN manual_artifact_effect_scopes scope ON scope.operation_id=m.operation_id AND scope.owner_id=m.owner_id AND scope.facts_hash=m.facts_hash
JOIN manual_nonce_expected_attempt initial ON initial.operation_id=m.operation_id
JOIN manual_nonce_actual_attempt t ON t.id=e.attempt_id AND t.attempt_json=initial.attempt_json;
CREATE VIEW manual_artifact_step_transition AS
SELECT e.operation_id,s.id,s.rowid,e.step_json
FROM manual_artifact_expected_step e JOIN manual_signed_artifact_commands m ON m.operation_id=e.operation_id
JOIN manual_artifact_effect_scopes scope ON scope.operation_id=m.operation_id AND scope.owner_id=m.owner_id AND scope.facts_hash=m.facts_hash
JOIN manual_nonce_claims q ON q.operation_id=m.operation_id
JOIN manual_artifact_actual_step s ON s.id=e.id AND s.step_json=json_set(e.step_json,'$.status','nonce_reserved','$.updated_at',q.checked_at)
JOIN manual_artifact_expected_attempt a ON a.operation_id=m.operation_id
JOIN manual_nonce_actual_attempt t ON t.id=a.attempt_id AND t.attempt_json=a.attempt_json;
CREATE VIEW manual_artifact_lane_transition AS
SELECT e.operation_id,l.wallet_id,l.rowid,e.lane_json
FROM manual_artifact_expected_lane e JOIN manual_signed_artifact_commands m ON m.operation_id=e.operation_id
JOIN manual_artifact_effect_scopes scope ON scope.operation_id=m.operation_id AND scope.owner_id=m.owner_id AND scope.facts_hash=m.facts_hash
JOIN manual_nonce_claims q ON q.operation_id=m.operation_id
JOIN manual_artifact_actual_lane l ON l.wallet_id=e.wallet_id AND l.lane_json=json_set(e.lane_json,'$.state','reserved','$.updated_at',q.checked_at)
JOIN manual_artifact_expected_step s ON s.operation_id=m.operation_id
JOIN manual_artifact_actual_step actual ON actual.id=s.id AND actual.step_json=s.step_json;

CREATE TRIGGER manual_artifact_command_source BEFORE INSERT ON manual_signed_artifact_commands
WHEN NOT EXISTS(
 SELECT 1 FROM manual_nonce_claim_complete q JOIN manual_signing_facts f ON f.operation_id=q.operation_id
 JOIN manual_nonce_binding_facts nf ON nf.operation_id=q.operation_id
 JOIN (SELECT * FROM manual_signing_intent_complete LIMIT -1) i ON i.operation_id=q.operation_id AND i.owner_id IS NEW.owner_id AND i.facts_hash IS NEW.facts_hash AND hex(i.facts_json)=hex(NEW.facts_json)
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
 AND manual_artifact_envelope_valid(hex(NEW.facts_json),typeof(NEW.facts_json),hex(NEW.envelope_json),typeof(NEW.envelope_json))=1
 AND json_extract(NEW.envelope_json,'$.completed_at') IS NEW.checked_at
 AND manual_time_cmp(NEW.checked_at,i.checked_at)>=0
 AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit x WHERE hex(x.id)=hex('manual-artifact:'||q.operation_id)
 OR (hex(x.event_type)=hex('MANUAL_ARTIFACT_STORED') AND hex(x.operation_id)=hex(q.operation_id)))
 AND NOT EXISTS(SELECT 1 FROM canary_alert_outbox x WHERE hex(x.id)=hex('manual-artifact-alert:'||q.operation_id) OR hex(x.audit_id)=hex('manual-artifact:'||q.operation_id))
)
BEGIN SELECT RAISE(ABORT,'manual artifact source/control/evidence invalid'); END;


-- Original deny domain retained; one exact command-owned exception only.
DROP TRIGGER manual_execution_no_attempt_update;
CREATE TRIGGER manual_execution_no_attempt_update BEFORE UPDATE ON transaction_attempts
WHEN (EXISTS(SELECT 1 FROM execution_steps s JOIN manual_execution_bindings b ON b.operation_id=s.operation_id WHERE s.id=NEW.step_id))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_attempt_transition n WHERE n.id IS OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.attempt_json=json_object('id',NEW.id,'step_id',NEW.step_id,'nonce',NEW.nonce,
 'tx_hash',NEW.tx_hash,'status',NEW.status,'created_at',NEW.created_at,'chain_id',NEW.chain_id,'wallet_id',NEW.wallet_id,
 'key_version',NEW.key_version,'encryption_nonce',CASE WHEN NEW.encryption_nonce IS NULL THEN NULL ELSE hex(NEW.encryption_nonce) END,
 'encrypted_raw_tx',CASE WHEN NEW.encrypted_raw_tx IS NULL THEN NULL ELSE hex(NEW.encrypted_raw_tx) END,'raw_tx_hash',NEW.raw_tx_hash,
 'from_address',NEW.from_address,'to_address',NEW.to_address,'value',NEW.value,'calldata',NEW.calldata,'gas_limit',NEW.gas_limit,
 'gas_tip_cap',NEW.gas_tip_cap,'gas_fee_cap',NEW.gas_fee_cap,'updated_at',NEW.updated_at,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at))
BEGIN SELECT RAISE(ABORT,'manual nonce/signing not connected'); END;

-- Original deny domain retained; one exact command-owned exception only.
DROP TRIGGER manual_nonce_attempt_update_victim;
CREATE TRIGGER manual_nonce_attempt_update_victim BEFORE UPDATE ON transaction_attempts
WHEN (EXISTS(SELECT 1 FROM transaction_attempts t JOIN execution_steps s ON s.id=t.step_id
 JOIN manual_execution_bindings b ON b.operation_id=s.operation_id
 WHERE t.rowid!=OLD.rowid AND (t.id=NEW.id OR t.rowid=NEW.rowid))
 OR EXISTS(SELECT 1 FROM manual_nonce_claims q WHERE q.attempt_id=OLD.id OR q.attempt_id=NEW.id
 OR ((OLD.wallet_id=q.wallet_id OR lower(OLD.from_address)=q.wallet_address) AND OLD.nonce=q.nonce)
 OR ((NEW.wallet_id=q.wallet_id OR lower(NEW.from_address)=q.wallet_address) AND NEW.nonce=q.nonce)
 OR EXISTS(SELECT 1 FROM execution_steps s JOIN operations own ON own.id=s.operation_id
 WHERE (s.wallet_id=q.wallet_id OR own.wallet_id=q.wallet_id)
 AND ((s.id=OLD.step_id AND OLD.nonce=q.nonce) OR (s.id=NEW.step_id AND NEW.nonce=q.nonce)))))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_attempt_transition n WHERE n.id IS OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.attempt_json=json_object('id',NEW.id,'step_id',NEW.step_id,'nonce',NEW.nonce,
 'tx_hash',NEW.tx_hash,'status',NEW.status,'created_at',NEW.created_at,'chain_id',NEW.chain_id,'wallet_id',NEW.wallet_id,
 'key_version',NEW.key_version,'encryption_nonce',CASE WHEN NEW.encryption_nonce IS NULL THEN NULL ELSE hex(NEW.encryption_nonce) END,
 'encrypted_raw_tx',CASE WHEN NEW.encrypted_raw_tx IS NULL THEN NULL ELSE hex(NEW.encrypted_raw_tx) END,'raw_tx_hash',NEW.raw_tx_hash,
 'from_address',NEW.from_address,'to_address',NEW.to_address,'value',NEW.value,'calldata',NEW.calldata,'gas_limit',NEW.gas_limit,
 'gas_tip_cap',NEW.gas_tip_cap,'gas_fee_cap',NEW.gas_fee_cap,'updated_at',NEW.updated_at,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at))
BEGIN SELECT RAISE(ABORT,'manual claimed attempt immutable'); END;

-- Original deny domain retained; one exact command-owned exception only.
DROP TRIGGER manual_nonce_alias_update;
CREATE TRIGGER manual_nonce_alias_update BEFORE UPDATE ON transaction_attempts
WHEN (EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 WHERE (x.attempt_rowid=OLD.rowid OR x.attempt_id=NEW.id OR x.attempt_rowid=NEW.rowid)
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
 OR EXISTS(SELECT 1 FROM manual_nonce_claims q
 LEFT JOIN execution_steps s ON hex(s.id)=hex(NEW.step_id) AND s.id IS NOT NULL AND NEW.step_id IS NOT NULL LEFT JOIN operations o ON hex(o.id)=hex(s.operation_id) AND o.id IS NOT NULL AND s.operation_id IS NOT NULL
 WHERE (manual_nonce_identity_relation(hex(s.operation_id),typeof(s.operation_id),hex(q.operation_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.step_id),typeof(NEW.step_id),hex(q.execution_step_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.from_address),typeof(NEW.from_address),hex(q.wallet_address),1)>0
 OR manual_nonce_identity_relation(hex(s.wallet_id),typeof(s.wallet_id),hex(q.wallet_id),0)>0
 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(q.wallet_id),0)>0)
 AND (manual_nonce_identity_relation(hex(s.operation_id),typeof(s.operation_id),hex(q.operation_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.step_id),typeof(NEW.step_id),hex(q.execution_step_id),0)>0 OR s.id IS NULL OR o.id IS NULL
 OR typeof(NEW.step_id)!='text' OR typeof(s.id)!='text' OR typeof(s.operation_id)!='text' OR typeof(o.id)!='text'
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)=2
 OR manual_nonce_identity_relation(hex(NEW.from_address),typeof(NEW.from_address),hex(q.wallet_address),1)=2
 OR manual_nonce_identity_relation(hex(s.wallet_id),typeof(s.wallet_id),hex(q.wallet_id),0)=2
 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(q.wallet_id),0)=2
 OR manual_nonce_stored_value_valid(hex(NEW.nonce),typeof(NEW.nonce))=0 OR NEW.nonce=q.nonce)))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_attempt_transition n WHERE n.id IS OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.attempt_json=json_object('id',NEW.id,'step_id',NEW.step_id,'nonce',NEW.nonce,
 'tx_hash',NEW.tx_hash,'status',NEW.status,'created_at',NEW.created_at,'chain_id',NEW.chain_id,'wallet_id',NEW.wallet_id,
 'key_version',NEW.key_version,'encryption_nonce',CASE WHEN NEW.encryption_nonce IS NULL THEN NULL ELSE hex(NEW.encryption_nonce) END,
 'encrypted_raw_tx',CASE WHEN NEW.encrypted_raw_tx IS NULL THEN NULL ELSE hex(NEW.encrypted_raw_tx) END,'raw_tx_hash',NEW.raw_tx_hash,
 'from_address',NEW.from_address,'to_address',NEW.to_address,'value',NEW.value,'calldata',NEW.calldata,'gas_limit',NEW.gas_limit,
 'gas_tip_cap',NEW.gas_tip_cap,'gas_fee_cap',NEW.gas_fee_cap,'updated_at',NEW.updated_at,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at))
BEGIN SELECT RAISE(ABORT,'manual nonce alias cannot be adopted or repaired'); END;

-- Original deny domain retained; one exact command-owned exception only.
DROP TRIGGER manual_execution_step_no_update;
CREATE TRIGGER manual_execution_step_no_update BEFORE UPDATE ON execution_steps
WHEN (EXISTS(SELECT 1 FROM manual_execution_bindings WHERE execution_step_id=OLD.id)
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_step_transition n WHERE n.id=OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.step_json=json_object('id',NEW.id,'operation_id',NEW.operation_id,'step_index',NEW.step_index,'kind',NEW.kind,
 'status',NEW.status,'route_json',NEW.route_json,'parameters_json',NEW.parameters_json,'unsigned_call_json',NEW.unsigned_call_json,
 'created_at',NEW.created_at,'updated_at',NEW.updated_at,'wallet_id',NEW.wallet_id,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_step_transition n WHERE n.id IS OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.step_json=json_object('id',NEW.id,'operation_id',NEW.operation_id,'step_index',NEW.step_index,'kind',NEW.kind,
 'status',NEW.status,'route_json',NEW.route_json,'parameters_json',NEW.parameters_json,'unsigned_call_json',NEW.unsigned_call_json,
 'created_at',NEW.created_at,'updated_at',NEW.updated_at,'wallet_id',NEW.wallet_id,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at))
BEGIN SELECT RAISE(ABORT,'manual execution step exact claim only'); END;

-- Original deny domain retained; one exact command-owned exception only.
DROP TRIGGER manual_execution_step_no_adoption;
CREATE TRIGGER manual_execution_step_no_adoption BEFORE UPDATE ON execution_steps
WHEN (EXISTS(SELECT 1 FROM manual_execution_bindings WHERE operation_id=NEW.operation_id OR execution_step_id=NEW.id)
 AND NOT EXISTS(SELECT 1 FROM manual_http_order_sources WHERE step_id=NEW.id AND operation_id=NEW.operation_id)
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_step_transition n WHERE n.id=OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.step_json=json_object('id',NEW.id,'operation_id',NEW.operation_id,'step_index',NEW.step_index,'kind',NEW.kind,
 'status',NEW.status,'route_json',NEW.route_json,'parameters_json',NEW.parameters_json,'unsigned_call_json',NEW.unsigned_call_json,
 'created_at',NEW.created_at,'updated_at',NEW.updated_at,'wallet_id',NEW.wallet_id,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_step_transition n WHERE n.id IS OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.step_json=json_object('id',NEW.id,'operation_id',NEW.operation_id,'step_index',NEW.step_index,'kind',NEW.kind,
 'status',NEW.status,'route_json',NEW.route_json,'parameters_json',NEW.parameters_json,'unsigned_call_json',NEW.unsigned_call_json,
 'created_at',NEW.created_at,'updated_at',NEW.updated_at,'wallet_id',NEW.wallet_id,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at))
BEGIN SELECT RAISE(ABORT,'manual execution step adoption forbidden'); END;

-- Original deny domain retained; one exact command-owned exception only.
DROP TRIGGER manual_execution_lane_update;
CREATE TRIGGER manual_execution_lane_update BEFORE UPDATE ON execution_wallet_lanes
WHEN (EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
 AND NOT EXISTS(SELECT 1 FROM manual_execution_bindings b WHERE b.operation_id=NEW.operation_id AND b.wallet_id=NEW.wallet_id
 AND b.execution_step_id=NEW.step_id AND lower(b.wallet_address)=lower(NEW.address) AND NEW.state='reserved'
 AND NEW.reserved_nonce IS NULL AND NEW.freeze_reason IS NULL AND NEW.updated_at=b.checked_at)
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_lane_transition n WHERE n.wallet_id=OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual lane exact nonce transition only'); END;

-- Original deny domain retained; one exact command-owned exception only.
DROP TRIGGER manual_execution_lane_no_update;
CREATE TRIGGER manual_execution_lane_no_update BEFORE UPDATE ON execution_wallet_lanes
WHEN (EXISTS(SELECT 1 FROM manual_execution_bindings WHERE wallet_id=OLD.wallet_id AND execution_step_id=OLD.step_id)
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_lane_transition n WHERE n.wallet_id=OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual lane downstream not connected'); END;

-- Original deny domain retained; one exact command-owned exception only.
DROP TRIGGER manual_binding_lane_reference_update;
CREATE TRIGGER manual_binding_lane_reference_update BEFORE UPDATE ON execution_wallet_lanes
WHEN ((EXISTS(SELECT 1 FROM manual_execution_bindings b
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
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual lane reference mutation forbidden'); END;

-- Original deny domain retained; one exact command-owned exception only.
DROP TRIGGER manual_binding_lane_retained_update;
CREATE TRIGGER manual_binding_lane_retained_update BEFORE UPDATE ON execution_wallet_lanes
WHEN (EXISTS(SELECT 1 FROM manual_execution_bindings b
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
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual retained lane mutation forbidden'); END;

-- Separate signed phase: original M2/B2 initial-only views remain untouched.
CREATE VIEW manual_artifact_claim_base AS
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
 AND NOT EXISTS(SELECT 1 FROM transaction_submissions WHERE attempt_id=q.attempt_id)
 AND NOT EXISTS(SELECT 1 FROM receipt_observations WHERE attempt_id=q.attempt_id)
 AND NOT EXISTS(SELECT 1 FROM position_effects WHERE attempt_id=q.attempt_id)
 AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit extra WHERE extra.operation_id=b.operation_id
 AND ((extra.event_type='MANUAL_EXECUTION_BOUND' AND extra.id IS NOT old.id) OR (extra.event_type='MANUAL_NONCE_CLAIMED' AND extra.id IS NOT a.id)));

CREATE VIEW manual_artifact_claim_complete AS
SELECT q.* FROM (SELECT * FROM manual_artifact_claim_base LIMIT -1) q
JOIN manual_nonce_binding_facts f ON f.operation_id=q.operation_id
JOIN manual_execution_bindings b ON b.operation_id=q.operation_id
JOIN canary_risk_reservations r ON r.id=q.reservation_id AND r.operation_id=q.operation_id AND r.wallet_id=q.wallet_id
JOIN canary_authorization_operation_usage u ON u.operation_id=q.operation_id AND u.authorization_id=b.authorization_id
WHERE manual_nonce_observation_bytes_valid(hex(q.observation_json),typeof(q.observation_json),hex(q.observation_hash),typeof(q.observation_hash),
 hex(q.nonce),typeof(q.nonce),hex(f.facts_json),typeof(f.facts_json),hex(q.checked_at),typeof(q.checked_at))=1
 AND typeof(r.state)='text' AND r.state IN ('reserved','frozen') AND typeof(u.state)='text' AND u.state IN ('RESERVED','FROZEN')
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x WHERE x.operation_id=q.operation_id AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce));

-- Verify the original immutable marker and original audit, not just metadata.
CREATE VIEW manual_artifact_marker_base AS
SELECT i.* FROM manual_signing_intents i JOIN manual_artifact_claim_complete q ON q.operation_id=i.operation_id
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

CREATE VIEW manual_artifact_marker_complete AS
SELECT i.* FROM (SELECT * FROM manual_artifact_marker_base LIMIT -1) i
JOIN canary_alert_outbox canonical ON canonical.id='manual-sign-intent-alert:'||i.operation_id
WHERE length(i.owner_id)=64
 AND NOT EXISTS(SELECT 1 FROM manual_signing_alert_witnesses w WHERE hex(w.operation_id)=hex(i.operation_id) AND w.witness_rowid!=canonical.rowid)
 AND NOT EXISTS(SELECT 1 FROM manual_signing_witness_creation_scopes s WHERE hex(s.operation_id)=hex(i.operation_id));

CREATE VIEW manual_artifact_audit_witnesses AS
SELECT m.operation_id,a.rowid AS witness_rowid,a.id FROM manual_signed_artifact_commands m JOIN canary_runtime_audit a
 ON hex(a.id)=hex('manual-artifact:'||m.operation_id) OR (hex(a.event_type)=hex('MANUAL_ARTIFACT_STORED') AND hex(a.operation_id)=hex(m.operation_id));
CREATE VIEW manual_artifact_alert_witnesses AS
SELECT m.operation_id,a.rowid AS witness_rowid,a.id,a.audit_id FROM manual_signed_artifact_commands m JOIN canary_alert_outbox a
 ON hex(a.id)=hex('manual-artifact-alert:'||m.operation_id) OR hex(a.audit_id)=hex('manual-artifact:'||m.operation_id);

CREATE VIEW manual_signed_artifact_preclose AS
SELECT m.* FROM manual_signed_artifact_commands m JOIN (SELECT * FROM manual_artifact_marker_complete LIMIT -1) i
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

CREATE VIEW manual_signed_artifact_complete AS
SELECT m.* FROM manual_signed_artifact_preclose m
WHERE NOT EXISTS(SELECT 1 FROM manual_artifact_effect_scopes x WHERE hex(x.operation_id)=hex(m.operation_id));
CREATE TRIGGER manual_artifact_scope_close BEFORE DELETE ON manual_artifact_effect_scopes
WHEN NOT EXISTS(SELECT 1 FROM manual_signed_artifact_preclose m WHERE m.operation_id IS OLD.operation_id AND m.owner_id IS OLD.owner_id AND m.facts_hash IS OLD.facts_hash)
BEGIN SELECT RAISE(ABORT,'manual artifact incomplete effects cannot close'); END;

CREATE TRIGGER manual_artifact_audit_insert BEFORE INSERT ON canary_runtime_audit
WHEN (hex(substr(CAST(NEW.id AS BLOB),1,length('manual-artifact:')))=hex('manual-artifact:') OR hex(NEW.event_type)=hex('MANUAL_ARTIFACT_STORED'))
 AND NOT EXISTS(SELECT 1 FROM manual_signed_artifact_commands m JOIN manual_artifact_effect_scopes x
 ON x.operation_id IS m.operation_id AND x.owner_id IS m.owner_id AND x.facts_hash IS m.facts_hash
 JOIN manual_nonce_claims q ON q.operation_id=m.operation_id JOIN manual_execution_bindings b ON b.operation_id=m.operation_id
 WHERE NEW.id IS 'manual-artifact:'||m.operation_id AND NEW.event_type IS 'MANUAL_ARTIFACT_STORED'
 AND NEW.operation_id IS m.operation_id AND NEW.attempt_id IS q.attempt_id AND NEW.authorization_id IS b.authorization_id
 AND NEW.authorization_epoch IS b.authorization_epoch AND NEW.reason_code IS 'ENCRYPTED_ARTIFACT_NOT_PERMISSION' AND NEW.created_at IS m.checked_at
 AND NEW.details_json IS json_object('owner_id',m.owner_id,'facts_hash',m.facts_hash,'envelope_hash',manual_artifact_bytes_sha256(hex(m.envelope_json)),
 'execution_step_id',q.execution_step_id,'observation_hash',m.observation_hash,'nonce',q.nonce))
BEGIN SELECT RAISE(ABORT,'manual artifact audit exact initial effect only'); END;
CREATE TRIGGER manual_artifact_audit_victim BEFORE INSERT ON canary_runtime_audit
WHEN EXISTS(SELECT 1 FROM manual_artifact_audit_witnesses w WHERE w.witness_rowid=NEW.rowid OR hex(w.id)=hex(NEW.id)
 OR hex(NEW.id)=hex('manual-artifact:'||w.operation_id) OR (hex(NEW.event_type)=hex('MANUAL_ARTIFACT_STORED') AND hex(NEW.operation_id)=hex(w.operation_id)))
BEGIN SELECT RAISE(ABORT,'manual artifact retained audit conflict protected'); END;

CREATE TRIGGER manual_artifact_alert_insert BEFORE INSERT ON canary_alert_outbox
WHEN (hex(substr(CAST(NEW.id AS BLOB),1,length('manual-artifact-alert:')))=hex('manual-artifact-alert:')
 OR hex(substr(CAST(NEW.audit_id AS BLOB),1,length('manual-artifact:')))=hex('manual-artifact:'))
 AND NOT EXISTS(SELECT 1 FROM manual_signed_artifact_commands m JOIN manual_artifact_effect_scopes x
 ON x.operation_id IS m.operation_id AND x.owner_id IS m.owner_id AND x.facts_hash IS m.facts_hash
 WHERE NEW.id IS 'manual-artifact-alert:'||m.operation_id AND NEW.audit_id IS 'manual-artifact:'||m.operation_id AND NEW.severity IS 'INFO' AND NEW.created_at IS m.checked_at)
BEGIN SELECT RAISE(ABORT,'manual artifact alert exact initial effect only'); END;
CREATE TRIGGER manual_artifact_alert_victim BEFORE INSERT ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_artifact_alert_witnesses w WHERE w.witness_rowid=NEW.rowid OR hex(w.id)=hex(NEW.id) OR hex(w.audit_id)=hex(NEW.audit_id)
 OR hex(NEW.id)=hex('manual-artifact-alert:'||w.operation_id) OR hex(NEW.audit_id)=hex('manual-artifact:'||w.operation_id))
BEGIN SELECT RAISE(ABORT,'manual artifact retained alert conflict protected'); END;
CREATE TRIGGER manual_artifact_alert_update BEFORE UPDATE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_artifact_alert_witnesses w WHERE w.witness_rowid=OLD.rowid
 AND (NEW.rowid IS NOT OLD.rowid OR NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id OR NEW.severity IS NOT OLD.severity OR NEW.created_at IS NOT OLD.created_at))
 OR EXISTS(SELECT 1 FROM manual_artifact_alert_witnesses w WHERE w.witness_rowid!=OLD.rowid
 AND (w.witness_rowid=NEW.rowid OR hex(w.id)=hex(NEW.id) OR hex(w.audit_id)=hex(NEW.audit_id)
 OR hex(NEW.id)=hex('manual-artifact-alert:'||w.operation_id) OR hex(NEW.audit_id)=hex('manual-artifact:'||w.operation_id)))
 OR (NOT EXISTS(SELECT 1 FROM manual_artifact_alert_witnesses w WHERE w.witness_rowid=OLD.rowid)
 AND EXISTS(SELECT 1 FROM manual_signed_artifact_commands m WHERE hex(NEW.id)=hex('manual-artifact-alert:'||m.operation_id) OR hex(NEW.audit_id)=hex('manual-artifact:'||m.operation_id)))
BEGIN SELECT RAISE(ABORT,'manual artifact alert identity immutable'); END;
CREATE TRIGGER manual_artifact_alert_delete BEFORE DELETE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_artifact_alert_witnesses w WHERE w.witness_rowid=OLD.rowid)
BEGIN SELECT RAISE(ABORT,'manual artifact retained alert cannot be removed'); END;

CREATE TRIGGER manual_artifact_command_effect AFTER INSERT ON manual_signed_artifact_commands
BEGIN
 UPDATE transaction_attempts SET status='signed',tx_hash=json_extract(NEW.envelope_json,'$.tx_hash'),
 key_version=json_extract(NEW.envelope_json,'$.key_version'),encryption_nonce=unhex(json_extract(NEW.envelope_json,'$.encryption_nonce')),
 encrypted_raw_tx=unhex(json_extract(NEW.envelope_json,'$.ciphertext')),raw_tx_hash=json_extract(NEW.envelope_json,'$.raw_sha256'),updated_at=NEW.checked_at
 WHERE id=(SELECT attempt_id FROM manual_nonce_claims WHERE operation_id=NEW.operation_id);
 UPDATE execution_steps SET status='signed',updated_at=NEW.checked_at WHERE id=(SELECT execution_step_id FROM manual_nonce_claims WHERE operation_id=NEW.operation_id);
 UPDATE execution_wallet_lanes SET state='signed',updated_at=NEW.checked_at WHERE operation_id=NEW.operation_id;
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'manual-artifact:'||NEW.operation_id,'MANUAL_ARTIFACT_STORED',NEW.operation_id,q.attempt_id,b.authorization_id,b.authorization_epoch,
 'ENCRYPTED_ARTIFACT_NOT_PERMISSION',json_object('owner_id',NEW.owner_id,'facts_hash',NEW.facts_hash,'envelope_hash',manual_artifact_bytes_sha256(hex(NEW.envelope_json)),
 'execution_step_id',q.execution_step_id,'observation_hash',NEW.observation_hash,'nonce',q.nonce),NEW.checked_at
 FROM manual_nonce_claims q JOIN manual_execution_bindings b ON b.operation_id=q.operation_id WHERE q.operation_id=NEW.operation_id;
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) VALUES('manual-artifact-alert:'||NEW.operation_id,'manual-artifact:'||NEW.operation_id,'INFO','PENDING',NEW.checked_at);
 DELETE FROM manual_artifact_effect_scopes WHERE operation_id=NEW.operation_id;
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_signed_artifact_complete WHERE operation_id=NEW.operation_id)
 OR EXISTS(SELECT 1 FROM manual_artifact_effect_scopes WHERE hex(operation_id)=hex(NEW.operation_id))
 THEN RAISE(ABORT,'manual artifact incomplete effect') END;
END;
