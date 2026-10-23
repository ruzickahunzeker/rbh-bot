-- M3-B2 alias integrity hardening. Keep landed 001..027 byte-identical.
-- LEFT JOIN preserves retained orphan attempts. Direct wallet/address identity
-- never depends on a successfully joined step/operation; nonce storage is
-- validated without a cast (BLOB "23" is not a trustworthy TEXT nonce).
CREATE VIEW manual_nonce_attempt_aliases AS
SELECT b.operation_id,t.rowid AS attempt_rowid,t.id AS attempt_id,t.step_id,
 t.nonce,manual_nonce_value_valid(t.nonce) AS nonce_valid,
 CASE WHEN s.operation_id=b.operation_id OR t.step_id=b.execution_step_id
 OR t.step_id=b.validation_step_id OR s.id IS NULL OR o.id IS NULL
 THEN 1 ELSE 0 END AS unsafe_identity
FROM manual_execution_bindings b CROSS JOIN transaction_attempts t
LEFT JOIN execution_steps s ON s.id=t.step_id
LEFT JOIN operations o ON o.id=s.operation_id
WHERE s.operation_id=b.operation_id OR t.step_id IN (b.execution_step_id,b.validation_step_id)
 OR t.wallet_id=b.wallet_id OR lower(t.from_address)=lower(b.wallet_address)
 OR s.wallet_id=b.wallet_id OR o.wallet_id=b.wallet_id;

CREATE TRIGGER manual_nonce_claim_alias_source BEFORE INSERT ON manual_nonce_claims
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x
 WHERE x.operation_id=NEW.operation_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=NEW.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce malformed or conflicting attempt alias'); END;

CREATE VIEW manual_nonce_claim_027_complete AS
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

DROP VIEW manual_nonce_claim_complete;
CREATE VIEW manual_nonce_claim_complete AS
SELECT q.* FROM manual_nonce_claim_027_complete q
WHERE NOT EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x
 WHERE x.operation_id=q.operation_id AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce));

-- Claim-owned insertion remains exact via 027's expected-attempt guard.
CREATE TRIGGER manual_nonce_alias_insert BEFORE INSERT ON transaction_attempts
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims q
 LEFT JOIN execution_steps s ON s.id=NEW.step_id
 LEFT JOIN operations o ON o.id=s.operation_id
 WHERE NEW.id IS NOT q.attempt_id
 AND (s.operation_id=q.operation_id OR NEW.step_id=q.execution_step_id
 OR NEW.wallet_id=q.wallet_id OR lower(NEW.from_address)=q.wallet_address
 OR s.wallet_id=q.wallet_id OR o.wallet_id=q.wallet_id)
 AND (s.operation_id=q.operation_id OR NEW.step_id=q.execution_step_id OR s.id IS NULL OR o.id IS NULL
 OR manual_nonce_value_valid(NEW.nonce)=0 OR NEW.nonce=q.nonce))
 OR EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 WHERE (x.attempt_id=NEW.id OR x.attempt_rowid=NEW.rowid)
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce alias or conflict victim protected'); END;

CREATE TRIGGER manual_nonce_alias_update BEFORE UPDATE ON transaction_attempts
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 WHERE (x.attempt_rowid=OLD.rowid OR x.attempt_id=NEW.id OR x.attempt_rowid=NEW.rowid)
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
 OR EXISTS(SELECT 1 FROM manual_nonce_claims q
 LEFT JOIN execution_steps s ON s.id=NEW.step_id LEFT JOIN operations o ON o.id=s.operation_id
 WHERE (s.operation_id=q.operation_id OR NEW.step_id=q.execution_step_id
 OR NEW.wallet_id=q.wallet_id OR lower(NEW.from_address)=q.wallet_address
 OR s.wallet_id=q.wallet_id OR o.wallet_id=q.wallet_id)
 AND (s.operation_id=q.operation_id OR NEW.step_id=q.execution_step_id OR s.id IS NULL OR o.id IS NULL
 OR manual_nonce_value_valid(NEW.nonce)=0 OR NEW.nonce=q.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce alias cannot be adopted or repaired'); END;
CREATE TRIGGER manual_nonce_alias_delete BEFORE DELETE ON transaction_attempts
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 WHERE x.attempt_rowid=OLD.rowid AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce retained alias cannot be removed'); END;

-- Ownership metadata cannot hide retained pollution or adopt legacy attempts.
-- BEFORE INSERT protects implicit REPLACE victims with recursion OFF as well.
CREATE TRIGGER manual_nonce_alias_step_insert_victim BEFORE INSERT ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON s.id=x.step_id
 WHERE x.attempt_id IS NOT q.attempt_id AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce)
 AND (s.id=NEW.id OR s.rowid=NEW.rowid OR (s.operation_id=NEW.operation_id AND s.step_index=NEW.step_index)))
BEGIN SELECT RAISE(ABORT,'manual nonce alias step conflict victim protected'); END;
CREATE TRIGGER manual_nonce_alias_step_update BEFORE UPDATE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 WHERE x.step_id=OLD.id AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
 OR EXISTS(SELECT 1 FROM manual_nonce_claims q JOIN transaction_attempts t ON t.step_id=OLD.id
 LEFT JOIN operations o ON o.id=NEW.operation_id
 WHERE t.id IS NOT q.attempt_id AND (NEW.operation_id=q.operation_id OR NEW.wallet_id=q.wallet_id OR o.wallet_id=q.wallet_id)
 AND (NEW.operation_id=q.operation_id OR o.id IS NULL OR manual_nonce_value_valid(t.nonce)=0 OR t.nonce=q.nonce))
 OR EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON s.id=x.step_id
 WHERE s.rowid!=OLD.rowid AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce)
 AND (s.id=NEW.id OR s.rowid=NEW.rowid OR (s.operation_id=NEW.operation_id AND s.step_index=NEW.step_index)))
BEGIN SELECT RAISE(ABORT,'manual nonce alias step identity protected'); END;
CREATE TRIGGER manual_nonce_alias_step_delete BEFORE DELETE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 WHERE x.step_id=OLD.id AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce alias step retained'); END;
CREATE TRIGGER manual_nonce_alias_operation_insert_victim BEFORE INSERT ON operations
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON s.id=x.step_id JOIN operations o ON o.id=s.operation_id
 WHERE x.attempt_id IS NOT q.attempt_id AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce)
 AND (o.id=NEW.id OR o.rowid=NEW.rowid OR o.idempotency_key=NEW.idempotency_key))
BEGIN SELECT RAISE(ABORT,'manual nonce alias operation conflict victim protected'); END;
CREATE TRIGGER manual_nonce_alias_operation_update BEFORE UPDATE ON operations
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON s.id=x.step_id
 WHERE s.operation_id=OLD.id AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
 OR EXISTS(SELECT 1 FROM manual_nonce_claims q JOIN execution_steps s ON s.operation_id=OLD.id
 JOIN transaction_attempts t ON t.step_id=s.id
 WHERE t.id IS NOT q.attempt_id AND NEW.wallet_id=q.wallet_id AND (manual_nonce_value_valid(t.nonce)=0 OR t.nonce=q.nonce))
 OR EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON s.id=x.step_id JOIN operations o ON o.id=s.operation_id
 WHERE o.rowid!=OLD.rowid AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce)
 AND (o.id=NEW.id OR o.rowid=NEW.rowid OR o.idempotency_key=NEW.idempotency_key))
BEGIN SELECT RAISE(ABORT,'manual nonce alias operation identity protected'); END;
CREATE TRIGGER manual_nonce_alias_operation_delete BEFORE DELETE ON operations
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON s.id=x.step_id
 WHERE s.operation_id=OLD.id AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce alias operation retained'); END;
