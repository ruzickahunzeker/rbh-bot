-- B: recovery of the retained exact MANUAL first submission, not a send grant.
-- Existing 001..044 are immutable. No current authorization/TTL/control gate
-- participates in historical identity or propagated-transaction recovery.
CREATE VIEW manual_submission_history_source AS
SELECT src.id AS request_id,src.principal_id,b.operation_id,b.validation_step_id,
 b.execution_step_id,b.wallet_id,b.wallet_address,q.attempt_id,q.nonce,
 first.submission_id,sub.state AS submission_state,first.tx_hash,first.artifact_hash,
 q.reservation_id,r.state AS reservation_state,b.authorization_id,b.authorization_epoch,
 usage.state AS usage_state,b.policy_version,b.contract_hash,m.facts_hash,
 f.binding_hash AS foundation_hash,b.environment,unit.input_asset,unit.input_decimals,
 r.input_amount,r.gas_budget,b.expires_at
FROM manual_http_order_sources src
JOIN manual_admission_commands c ON c.operation_id=src.operation_id AND c.request_id=src.id AND c.step_id=src.step_id
JOIN manual_execution_bindings b ON b.operation_id=c.operation_id AND b.request_id=c.request_id
 AND b.validation_step_id=c.step_id AND b.wallet_id=src.wallet_id AND b.wallet_address=src.wallet_address
 AND b.contract_hash=c.contract_hash AND b.authorization_id=c.authorization_id AND b.authorization_epoch=c.authorization_epoch
 AND b.environment=c.environment AND b.policy_version=json_extract(c.contract_json,'$.policy_version')
 AND b.expires_at=src.expires_at AND b.expires_at=json_extract(c.contract_json,'$.expires_at')
JOIN manual_nonce_claims q ON q.operation_id=b.operation_id AND q.execution_step_id=b.execution_step_id
 AND q.wallet_id=b.wallet_id AND q.wallet_address=lower(b.wallet_address) AND q.contract_hash=b.contract_hash
 AND q.reservation_id=c.reservation_id
JOIN manual_signed_artifact_commands m ON m.operation_id=q.operation_id
JOIN manual_artifact_expected_attempt expected ON expected.operation_id=m.operation_id AND expected.attempt_id=q.attempt_id
JOIN manual_nonce_actual_attempt actual ON actual.id=q.attempt_id AND actual.attempt_json=expected.attempt_json
JOIN manual_artifact_expected_step es ON es.operation_id=b.operation_id AND es.id=b.execution_step_id
JOIN manual_artifact_actual_step actual_step ON actual_step.id=es.id
 AND json_remove(actual_step.step_json,'$.status','$.updated_at')=json_remove(es.step_json,'$.status','$.updated_at')
JOIN execution_steps step ON step.id=b.execution_step_id
 AND step.status IN ('signed','canonical_success','canonical_revert','reconciling','orphaned')
JOIN operations op ON op.id=b.operation_id AND op.wallet_id=b.wallet_id AND op.chain_id=4663
 AND op.policy_version=b.policy_version AND op.expires_at=b.expires_at
 AND op.request_fingerprint=src.request_fingerprint AND op.request_json=src.request_json
JOIN canary_risk_reservations r ON r.id=q.reservation_id AND r.operation_id=b.operation_id AND r.wallet_id=b.wallet_id
 AND r.policy_version=b.policy_version AND r.input_amount=json_extract(c.contract_json,'$.input.units')
 AND r.gas_budget=json_extract(c.contract_json,'$.gas_budget.units') AND r.source_hash=json_extract(c.contract_json,'$.result_hash')
JOIN manual_reservation_units unit ON unit.reservation_id=r.id AND unit.operation_id=b.operation_id
 AND unit.chain_id=4663 AND unit.accounting_kind='TRADE_INPUT' AND unit.manual_policy_id=b.manual_policy_id
 AND unit.manual_policy_hash=b.manual_policy_hash AND unit.input_asset=json_extract(c.contract_json,'$.input.asset')
 AND unit.input_decimals=json_extract(c.contract_json,'$.input.decimals') AND unit.result_hash=r.source_hash
JOIN canary_authorization_operation_usage usage ON usage.operation_id=b.operation_id AND usage.authorization_id=b.authorization_id
 AND usage.amount=r.input_amount AND usage.state IN ('RESERVED','FROZEN','CONSUMED')
JOIN manual_submission_foundations f ON f.operation_id=b.operation_id
 AND f.binding_hash=manual_artifact_bytes_sha256(hex(f.binding_json))
 AND json_extract(f.binding_json,'$.facts_hash')=m.facts_hash
 AND json_extract(f.binding_json,'$.facts')=json(m.facts_json)
 AND json_extract(f.binding_json,'$.contract')=json(c.contract_json)
 AND json_extract(f.binding_json,'$.contract_hash')=c.contract_hash
 AND json_extract(f.binding_json,'$.reservation.id')=q.reservation_id
 AND json_extract(f.binding_json,'$.usage.authorization_id')=b.authorization_id
JOIN manual_first_submission_bindings first ON first.operation_id=b.operation_id AND first.attempt_id=q.attempt_id
 AND first.authorization_id=b.authorization_id AND first.authorization_epoch=b.authorization_epoch
 AND first.environment=b.environment AND first.foundation_hash=f.binding_hash
 AND first.artifact_hash=json_extract(m.envelope_json,'$.raw_sha256') AND first.tx_hash=json_extract(m.envelope_json,'$.tx_hash')
JOIN transaction_submissions sub ON sub.id=first.submission_id AND sub.attempt_id=first.attempt_id
 AND sub.tx_hash=first.tx_hash AND sub.sequence=0 AND sub.created_at=first.created_at
 AND sub.state IN ('submitting','submitted','broadcast_unknown','manual_resolution','known_unsent')
JOIN canary_runtime_audit claim ON claim.id='manual-first-submission-claim:'||first.permit_id
 AND claim.event_type='MANUAL_FIRST_SUBMISSION_CLAIMED' AND claim.operation_id=b.operation_id
 AND claim.attempt_id=q.attempt_id AND claim.authorization_id=b.authorization_id AND claim.authorization_epoch=b.authorization_epoch
JOIN canary_alert_outbox claim_alert ON claim_alert.audit_id=claim.id AND claim_alert.id='manual-first-submission-claim-alert:'||first.permit_id
JOIN canary_runtime_audit execution_audit ON execution_audit.id='manual-execution:'||b.operation_id
 AND execution_audit.event_type='MANUAL_EXECUTION_BOUND' AND execution_audit.operation_id=b.operation_id
 AND execution_audit.authorization_id=b.authorization_id AND execution_audit.authorization_epoch=b.authorization_epoch
 AND execution_audit.created_at=b.checked_at AND execution_audit.reason_code='BOUND_NOT_SIGNED_NOT_PERMISSION'
JOIN canary_alert_outbox execution_alert ON execution_alert.id='manual-execution-alert:'||b.operation_id AND execution_alert.audit_id=execution_audit.id
JOIN canary_runtime_audit nonce_audit ON nonce_audit.id='manual-nonce-audit:'||q.operation_id
 AND nonce_audit.event_type='MANUAL_NONCE_CLAIMED' AND nonce_audit.operation_id=q.operation_id AND nonce_audit.attempt_id=q.attempt_id
 AND nonce_audit.authorization_id=b.authorization_id AND nonce_audit.authorization_epoch=b.authorization_epoch AND nonce_audit.created_at=q.checked_at
JOIN canary_alert_outbox nonce_alert ON nonce_alert.id='manual-nonce-alert:'||q.operation_id AND nonce_alert.audit_id=nonce_audit.id
JOIN canary_runtime_audit artifact_audit ON artifact_audit.id='manual-artifact:'||m.operation_id
 AND artifact_audit.event_type='MANUAL_ARTIFACT_STORED' AND artifact_audit.operation_id=m.operation_id AND artifact_audit.attempt_id=q.attempt_id
 AND artifact_audit.authorization_id=b.authorization_id AND artifact_audit.authorization_epoch=b.authorization_epoch AND artifact_audit.created_at=m.checked_at
JOIN canary_alert_outbox artifact_alert ON artifact_alert.id='manual-artifact-alert:'||m.operation_id AND artifact_alert.audit_id=artifact_audit.id
JOIN canary_runtime_audit foundation_audit ON foundation_audit.id='manual-submission-foundation:'||f.operation_id
 AND foundation_audit.event_type='MANUAL_SUBMISSION_FOUNDATION' AND foundation_audit.operation_id=f.operation_id AND foundation_audit.attempt_id=q.attempt_id
 AND foundation_audit.authorization_id=b.authorization_id AND foundation_audit.authorization_epoch=b.authorization_epoch AND foundation_audit.created_at=f.created_at
JOIN canary_alert_outbox foundation_alert ON foundation_alert.id='manual-submission-foundation-alert:'||f.operation_id AND foundation_alert.audit_id=foundation_audit.id
WHERE src.source='MANUAL' AND src.chain='robinhood' AND src.operation_id='manual_op_'||src.id
 AND b.execution_step_id='manual-exec:'||src.operation_id AND q.attempt_id='manual-nonce:'||b.operation_id
 AND manual_artifact_facts_hash(hex(m.facts_json),typeof(m.facts_json))=m.facts_hash
 AND manual_artifact_envelope_valid(hex(m.facts_json),typeof(m.facts_json),hex(m.envelope_json),typeof(m.envelope_json))=1
 AND ((sub.state='submitting' AND NOT EXISTS(SELECT 1 FROM manual_first_submission_transitions outcome
 WHERE outcome.submission_id=sub.id AND outcome.kind='OUTCOME'))
 OR EXISTS(SELECT 1 FROM manual_first_submission_transitions outcome
 JOIN canary_runtime_audit outcome_audit ON outcome_audit.id='manual-first-submission-transition:'||outcome.id
 AND outcome_audit.event_type='MANUAL_FIRST_SUBMISSION_OUTCOME' AND outcome_audit.operation_id=b.operation_id AND outcome_audit.attempt_id=q.attempt_id
 AND outcome_audit.authorization_id=b.authorization_id AND outcome_audit.authorization_epoch=b.authorization_epoch AND outcome_audit.created_at=outcome.checked_at
 JOIN canary_alert_outbox outcome_alert ON outcome_alert.id='manual-first-submission-transition-alert:'||outcome.id AND outcome_alert.audit_id=outcome_audit.id
 WHERE outcome.submission_id=sub.id AND outcome.kind='OUTCOME' AND outcome.result_state=sub.state
 AND outcome.rpc_hash IS sub.rpc_hash AND outcome.failure_class IS sub.failure_class AND outcome.failure_detail IS sub.failure_detail
 AND outcome.checked_at=sub.updated_at))
 AND (sub.send_intent_at IS NULL OR EXISTS(SELECT 1 FROM manual_first_submission_transitions intent
 JOIN canary_runtime_audit intent_audit ON intent_audit.id='manual-first-submission-transition:'||intent.id
 AND intent_audit.event_type='MANUAL_FIRST_SUBMISSION_INTENT' AND intent_audit.operation_id=b.operation_id AND intent_audit.attempt_id=q.attempt_id
 JOIN canary_alert_outbox intent_alert ON intent_alert.id='manual-first-submission-transition-alert:'||intent.id AND intent_alert.audit_id=intent_audit.id
 WHERE intent.submission_id=sub.id AND intent.kind='INTENT' AND intent.checked_at=sub.send_intent_at))
 AND NOT EXISTS(SELECT 1 FROM transaction_submissions extra WHERE extra.attempt_id=q.attempt_id AND extra.id!=sub.id)
 AND NOT EXISTS(SELECT 1 FROM manual_first_submission_scopes scope WHERE scope.permit_id=first.permit_id);

CREATE VIEW manual_recovery_source AS
SELECT * FROM manual_submission_history_source WHERE submission_state IN ('submitted','broadcast_unknown');

-- Per-request cursor preserves the old intake sequence; later events share a
-- single durable journal. Backfill is assigned ONCE, never sorted on a read.
CREATE TABLE manual_lifecycle_events(
 request_id TEXT NOT NULL REFERENCES manual_http_order_sources(id),
 cursor INTEGER NOT NULL CHECK(cursor>0),
 audit_id TEXT NOT NULL UNIQUE REFERENCES canary_runtime_audit(id),
 event_type TEXT NOT NULL,reason_code TEXT NOT NULL,created_at TEXT NOT NULL,
 PRIMARY KEY(request_id,cursor)
);
INSERT INTO manual_lifecycle_events(request_id,cursor,audit_id,event_type,reason_code,created_at)
 SELECT src.id,src.sequence,a.id,a.event_type,a.reason_code,a.created_at
 FROM manual_http_order_sources src JOIN canary_runtime_audit a ON a.id='manual-http-audit:'||src.id;
INSERT INTO manual_lifecycle_events(request_id,cursor,audit_id,event_type,reason_code,created_at)
 SELECT request_id,intake_cursor+ROW_NUMBER() OVER(PARTITION BY request_id ORDER BY created_at,audit_id),
 audit_id,event_type,reason_code,created_at FROM (
 SELECT src.id AS request_id,src.sequence AS intake_cursor,a.id AS audit_id,a.event_type,a.reason_code,a.created_at
 FROM manual_http_order_sources src JOIN canary_runtime_audit a ON a.operation_id=src.operation_id
 WHERE a.id!='manual-http-audit:'||src.id);
CREATE TRIGGER manual_lifecycle_immutable_update BEFORE UPDATE ON manual_lifecycle_events
BEGIN SELECT RAISE(ABORT,'manual lifecycle journal immutable'); END;
CREATE TRIGGER manual_lifecycle_immutable_delete BEFORE DELETE ON manual_lifecycle_events
BEGIN SELECT RAISE(ABORT,'manual lifecycle journal retained'); END;
CREATE TRIGGER manual_lifecycle_source BEFORE INSERT ON manual_lifecycle_events
WHEN EXISTS(SELECT 1 FROM manual_lifecycle_events old WHERE old.rowid=NEW.rowid OR old.audit_id=NEW.audit_id
 OR (old.request_id=NEW.request_id AND old.cursor=NEW.cursor))
 OR NOT EXISTS(SELECT 1 FROM manual_http_order_sources src JOIN canary_runtime_audit a ON a.id=NEW.audit_id
 WHERE src.id=NEW.request_id AND (a.operation_id=src.operation_id OR a.id='manual-http-audit:'||src.id)
 AND NEW.event_type IS a.event_type AND NEW.reason_code IS a.reason_code AND NEW.created_at IS a.created_at
 AND NEW.cursor=COALESCE((SELECT MAX(e.cursor)+1 FROM manual_lifecycle_events e WHERE e.request_id=src.id),src.sequence))
BEGIN SELECT RAISE(ABORT,'manual lifecycle event requires next source audit'); END;
CREATE TRIGGER manual_lifecycle_audit AFTER INSERT ON canary_runtime_audit
BEGIN
 INSERT INTO manual_lifecycle_events(request_id,cursor,audit_id,event_type,reason_code,created_at)
 SELECT src.id,COALESCE((SELECT MAX(e.cursor)+1 FROM manual_lifecycle_events e WHERE e.request_id=src.id),src.sequence),
 NEW.id,NEW.event_type,NEW.reason_code,NEW.created_at FROM manual_http_order_sources src
 WHERE NEW.operation_id=src.operation_id OR NEW.id='manual-http-audit:'||src.id;
END;

-- Command rows are append-only transition witnesses. Economic state remains
-- in the existing receipt/effect/step/risk tables, not in this overlay.
CREATE TABLE manual_recovery_commands(
 sequence INTEGER PRIMARY KEY AUTOINCREMENT CHECK(sequence>0),
 id TEXT NOT NULL UNIQUE,
 operation_id TEXT NOT NULL REFERENCES manual_first_submission_bindings(operation_id),
 kind TEXT NOT NULL CHECK(kind IN ('OBSERVE','CANONICAL','ORPHAN')),
 receipt_id TEXT NOT NULL,tx_hash TEXT NOT NULL,block_number INTEGER NOT NULL CHECK(block_number>0),
 block_hash TEXT NOT NULL,receipt_status INTEGER NOT NULL CHECK(receipt_status IN (0,1)),
 prior_version INTEGER NOT NULL CHECK(prior_version>=0),
 effect_asset TEXT,effect_delta TEXT,
 environment TEXT NOT NULL,holder_id TEXT NOT NULL,lease_epoch INTEGER NOT NULL CHECK(lease_epoch>0),
 checked_at TEXT NOT NULL,
 UNIQUE(receipt_id,prior_version)
);
CREATE TABLE manual_recovery_scopes(
 command_id TEXT NOT NULL PRIMARY KEY REFERENCES manual_recovery_commands(id) DEFERRABLE INITIALLY DEFERRED,
 empty_ref INTEGER NOT NULL DEFAULT 1 REFERENCES manual_first_submission_empty(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER manual_recovery_scope_insert BEFORE INSERT ON manual_recovery_scopes
WHEN EXISTS(SELECT 1 FROM manual_recovery_commands c WHERE c.id=NEW.command_id)
 OR EXISTS(SELECT 1 FROM manual_recovery_scopes s WHERE s.command_id=NEW.command_id OR s.rowid=NEW.rowid)
BEGIN SELECT RAISE(ABORT,'manual recovery scope cannot reopen'); END;
CREATE TRIGGER manual_recovery_scope_update BEFORE UPDATE ON manual_recovery_scopes
BEGIN SELECT RAISE(ABORT,'manual recovery scope immutable'); END;
CREATE TRIGGER manual_recovery_command_update BEFORE UPDATE ON manual_recovery_commands
BEGIN SELECT RAISE(ABORT,'manual recovery command immutable'); END;
CREATE TRIGGER manual_recovery_command_delete BEFORE DELETE ON manual_recovery_commands
BEGIN SELECT RAISE(ABORT,'manual recovery command retained'); END;
CREATE TRIGGER manual_recovery_command_source BEFORE INSERT ON manual_recovery_commands
WHEN NEW.sequence!=-1 OR typeof(NEW.id)!='text' OR NEW.id IS NOT 'manual-recovery:'||NEW.receipt_id||':'||NEW.prior_version
 OR typeof(NEW.block_number)!='integer' OR typeof(NEW.prior_version)!='integer'
 OR typeof(NEW.block_hash)!='text' OR length(NEW.block_hash)!=66 OR substr(NEW.block_hash,1,2)!='0x'
 OR substr(NEW.block_hash,3) GLOB '*[^0-9a-f]*'
 OR manual_artifact_time_valid(hex(NEW.checked_at),typeof(NEW.checked_at))!=1
 OR EXISTS(SELECT 1 FROM manual_recovery_commands c WHERE c.id=NEW.id OR c.rowid=NEW.rowid
 OR (c.receipt_id=NEW.receipt_id AND c.prior_version=NEW.prior_version))
 OR NOT EXISTS(SELECT 1 FROM manual_recovery_source src
 JOIN canary_worker_leases l ON l.role='RECOVERY' AND l.environment=src.environment
 WHERE src.operation_id=NEW.operation_id AND src.tx_hash=NEW.tx_hash
 AND l.environment=NEW.environment AND l.holder_id=NEW.holder_id AND l.lease_epoch=NEW.lease_epoch
 AND manual_time_cmp(NEW.checked_at,l.updated_at)>=0 AND manual_time_cmp(NEW.checked_at,l.expires_at)<0
 AND NEW.receipt_id=manual_artifact_bytes_sha256(hex(src.attempt_id||char(0)||NEW.block_hash||char(0))))
 OR (NEW.kind='OBSERVE' AND (NEW.prior_version!=0 OR NEW.effect_asset IS NOT NULL OR NEW.effect_delta IS NOT NULL
 OR EXISTS(SELECT 1 FROM receipt_observations r WHERE r.id=NEW.receipt_id)))
 OR (NEW.kind!='OBSERVE' AND NOT EXISTS(SELECT 1 FROM receipt_observations r
 JOIN manual_recovery_source src ON src.operation_id=NEW.operation_id AND src.attempt_id=r.attempt_id
 WHERE r.id=NEW.receipt_id AND r.tx_hash=NEW.tx_hash AND r.block_hash=NEW.block_hash
 AND r.block_number=NEW.block_number AND r.receipt_status=NEW.receipt_status AND r.reconciliation_version=NEW.prior_version
 AND ((NEW.kind='CANONICAL' AND r.canonical_state IN ('observed','orphaned'))
 OR (NEW.kind='ORPHAN' AND r.canonical_state IN ('observed','canonical_success','canonical_revert')))))
 OR (NEW.kind='CANONICAL' AND NEW.receipt_status=1 AND
 (typeof(NEW.effect_asset)!='text' OR length(NEW.effect_asset)=0 OR typeof(NEW.effect_delta)!='text'
 OR length(NEW.effect_delta)=0 OR NEW.effect_delta='0'
 OR NOT EXISTS(SELECT 1 FROM manual_admission_commands contract WHERE contract.operation_id=NEW.operation_id
 AND lower(NEW.effect_asset)=json_extract(contract.contract_json,'$.output.resolution.token.address')
 AND ((json_extract(contract.contract_json,'$.output.resolution.side')='buy'
 AND NEW.effect_delta=manual_u256_add(NEW.effect_delta,'0') AND manual_u256_cmp(NEW.effect_delta,'0')>0
 AND manual_u256_cmp(NEW.effect_delta,json_extract(contract.contract_json,'$.minimum_output.units'))>=0)
 OR (json_extract(contract.contract_json,'$.output.resolution.side')='sell'
 AND NEW.effect_delta='-'||json_extract(contract.contract_json,'$.input.units'))))
 OR EXISTS(SELECT 1 FROM position_effects p JOIN manual_recovery_source src ON src.attempt_id=p.attempt_id
 WHERE src.operation_id=NEW.operation_id AND (lower(p.asset)!=lower(NEW.effect_asset) OR p.state!='rolled_back'))))
 OR ((NEW.kind!='CANONICAL' OR NEW.receipt_status!=1) AND (NEW.effect_asset IS NOT NULL OR NEW.effect_delta IS NOT NULL))
BEGIN SELECT RAISE(ABORT,'manual recovery requires exact retained source, phase and recovery lease'); END;
CREATE TRIGGER manual_recovery_command_open BEFORE INSERT ON manual_recovery_commands
BEGIN INSERT INTO manual_recovery_scopes(command_id) VALUES(NEW.id); END;

CREATE VIEW manual_recovery_open AS
SELECT c.*,q.attempt_id,b.wallet_id,b.execution_step_id,q.nonce,q.reservation_id,
 b.authorization_id,b.authorization_epoch,
 manual_artifact_bytes_sha256(hex(q.attempt_id||char(0)||'position-effect'||char(0))) AS effect_id
FROM manual_recovery_commands c JOIN manual_recovery_scopes scope ON scope.command_id=c.id
JOIN manual_first_submission_bindings first ON first.operation_id=c.operation_id
JOIN manual_execution_bindings b ON b.operation_id=c.operation_id
JOIN manual_nonce_claims q ON q.operation_id=c.operation_id AND q.attempt_id=first.attempt_id;

CREATE VIEW manual_recovery_step_transition AS
SELECT c.id,c.operation_id,s.id AS step_id,s.rowid,
 json_set(s.step_json,'$.status',CASE WHEN c.kind='ORPHAN' THEN 'reconciling'
 WHEN c.receipt_status=1 THEN 'canonical_success' ELSE 'canonical_revert' END,'$.updated_at',c.checked_at) AS step_json
FROM manual_recovery_open c JOIN manual_artifact_actual_step s ON s.id=c.execution_step_id
WHERE c.kind IN ('CANONICAL','ORPHAN');
CREATE VIEW manual_recovery_lane_transition AS
SELECT c.id,l.wallet_id,l.rowid,
 CASE WHEN c.kind='CANONICAL' AND json_extract(l.lane_json,'$.operation_id')=c.operation_id
 AND json_extract(l.lane_json,'$.step_id')=c.execution_step_id AND json_extract(l.lane_json,'$.reserved_nonce')=c.nonce
 AND json_extract(l.lane_json,'$.freeze_reason') IS NOT 'prior_canonical_reorg' THEN json_set(l.lane_json,'$.state','idle','$.operation_id',NULL,'$.step_id',NULL,
 '$.reserved_nonce',NULL,'$.freeze_reason',NULL,'$.updated_at',c.checked_at)
 WHEN json_extract(l.lane_json,'$.state')='idle' THEN json_set(l.lane_json,'$.state','frozen','$.operation_id',c.operation_id,
 '$.step_id',c.execution_step_id,'$.reserved_nonce',c.nonce,'$.freeze_reason','canonical_reorg','$.updated_at',c.checked_at)
 ELSE json_set(l.lane_json,'$.state','frozen','$.freeze_reason',CASE WHEN json_extract(l.lane_json,'$.operation_id')=c.operation_id
 THEN 'canonical_reorg' ELSE 'prior_canonical_reorg' END,'$.updated_at',c.checked_at) END AS lane_json
FROM manual_recovery_open c JOIN manual_artifact_actual_lane l ON l.wallet_id=c.wallet_id
WHERE c.kind='ORPHAN' OR (c.kind='CANONICAL' AND json_extract(l.lane_json,'$.state')!='idle');

-- Narrow guards and effects are appended below. Scopes cannot survive commit.
DROP TRIGGER manual_nonce_no_receipt_insert;
CREATE TRIGGER manual_nonce_no_receipt_insert BEFORE INSERT ON receipt_observations
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE attempt_id=NEW.attempt_id)
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_open c WHERE c.kind='OBSERVE'
 AND NEW.id IS c.receipt_id AND NEW.attempt_id IS c.attempt_id AND NEW.chain_id=4663
 AND NEW.tx_hash IS c.tx_hash AND NEW.block_number IS c.block_number AND NEW.block_hash IS c.block_hash
 AND NEW.receipt_status IS c.receipt_status AND NEW.canonical_state IS 'observed'
 AND NEW.reconciliation_version=1 AND NEW.observed_at IS c.checked_at AND NEW.updated_at IS c.checked_at)
BEGIN SELECT RAISE(ABORT,'manual receipt requires exact recovery observation'); END;
DROP TRIGGER manual_nonce_no_receipt_update;
CREATE TRIGGER manual_nonce_no_receipt_update BEFORE UPDATE ON receipt_observations
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE attempt_id IN (OLD.attempt_id,NEW.attempt_id))
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_open c WHERE c.kind IN ('CANONICAL','ORPHAN')
 AND OLD.id=c.receipt_id AND OLD.attempt_id=c.attempt_id AND OLD.reconciliation_version=c.prior_version
 AND OLD.tx_hash=c.tx_hash AND OLD.block_hash=c.block_hash AND OLD.block_number=c.block_number AND OLD.receipt_status=c.receipt_status
 AND NEW.rowid=OLD.rowid AND NEW.id IS OLD.id AND NEW.attempt_id IS OLD.attempt_id AND NEW.chain_id IS OLD.chain_id
 AND NEW.tx_hash IS OLD.tx_hash AND NEW.block_number IS OLD.block_number AND NEW.block_hash IS OLD.block_hash
 AND NEW.receipt_status IS OLD.receipt_status AND NEW.observed_at IS OLD.observed_at
 AND NEW.canonical_state IS CASE WHEN c.kind='ORPHAN' THEN 'orphaned' WHEN c.receipt_status=1 THEN 'canonical_success' ELSE 'canonical_revert' END
 AND NEW.reconciliation_version=OLD.reconciliation_version+1 AND NEW.updated_at IS c.checked_at)
BEGIN SELECT RAISE(ABORT,'manual receipt requires exact recovery transition'); END;
CREATE TRIGGER manual_recovery_receipt_no_replace BEFORE INSERT ON receipt_observations
WHEN EXISTS(SELECT 1 FROM receipt_observations r JOIN manual_nonce_claims q ON q.attempt_id=r.attempt_id
 WHERE r.rowid=NEW.rowid OR r.id=NEW.id OR (r.attempt_id=NEW.attempt_id AND r.block_hash=NEW.block_hash))
BEGIN SELECT RAISE(ABORT,'manual recovery receipt retained'); END;
CREATE TRIGGER manual_recovery_receipt_no_delete BEFORE DELETE ON receipt_observations
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims q WHERE q.attempt_id=OLD.attempt_id)
BEGIN SELECT RAISE(ABORT,'manual recovery receipt retained'); END;

DROP TRIGGER manual_nonce_no_effect_insert;
CREATE TRIGGER manual_nonce_no_effect_insert BEFORE INSERT ON position_effects
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE attempt_id=NEW.attempt_id)
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_open c WHERE c.kind='CANONICAL' AND c.receipt_status=1
 AND NEW.effect_id IS c.effect_id AND NEW.attempt_id IS c.attempt_id AND NEW.receipt_id IS c.receipt_id
 AND NEW.wallet_id IS c.wallet_id AND NEW.asset IS c.effect_asset AND NEW.delta IS c.effect_delta
 AND NEW.state IS 'active' AND NEW.applied_at IS c.checked_at AND NEW.updated_at IS c.checked_at)
BEGIN SELECT RAISE(ABORT,'manual effect requires exact recovery apply'); END;
DROP TRIGGER manual_nonce_no_effect_update;
CREATE TRIGGER manual_nonce_no_effect_update BEFORE UPDATE ON position_effects
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE attempt_id IN (OLD.attempt_id,NEW.attempt_id))
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_open c WHERE OLD.effect_id=c.effect_id AND OLD.attempt_id=c.attempt_id
 AND NEW.rowid=OLD.rowid AND NEW.effect_id IS OLD.effect_id AND NEW.attempt_id IS OLD.attempt_id
 AND NEW.wallet_id IS OLD.wallet_id AND NEW.asset IS OLD.asset AND NEW.applied_at IS OLD.applied_at
 AND NEW.updated_at IS c.checked_at
 AND ((c.kind='CANONICAL' AND c.receipt_status=1 AND OLD.state='rolled_back' AND NEW.state='active'
 AND NEW.receipt_id IS c.receipt_id AND NEW.asset IS c.effect_asset AND NEW.delta IS c.effect_delta)
 OR (c.kind='ORPHAN' AND OLD.state='active' AND OLD.receipt_id=c.receipt_id AND NEW.state='rolled_back' AND NEW.receipt_id IS OLD.receipt_id AND NEW.delta IS OLD.delta)))
BEGIN SELECT RAISE(ABORT,'manual effect requires exact recovery transition'); END;
CREATE TRIGGER manual_recovery_effect_no_replace BEFORE INSERT ON position_effects
WHEN EXISTS(SELECT 1 FROM position_effects p JOIN manual_nonce_claims q ON q.attempt_id=p.attempt_id
 WHERE p.rowid=NEW.rowid OR p.effect_id=NEW.effect_id OR p.attempt_id=NEW.attempt_id)
BEGIN SELECT RAISE(ABORT,'manual recovery effect retained'); END;
CREATE TRIGGER manual_recovery_effect_no_delete BEFORE DELETE ON position_effects
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims q WHERE q.attempt_id=OLD.attempt_id)
BEGIN SELECT RAISE(ABORT,'manual recovery effect retained'); END;
CREATE TRIGGER manual_recovery_history_source BEFORE INSERT ON position_effect_history
WHEN EXISTS(SELECT 1 FROM position_effects p JOIN manual_nonce_claims q ON q.attempt_id=p.attempt_id WHERE p.effect_id=NEW.effect_id)
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_open c WHERE c.effect_id=NEW.effect_id AND c.receipt_id=NEW.receipt_id
 AND NEW.id IS 'manual-effect-history:'||c.id AND NEW.created_at IS c.checked_at
 AND NEW.transition IS CASE WHEN c.kind='ORPHAN' THEN 'rollback'
 WHEN EXISTS(SELECT 1 FROM manual_recovery_commands previous WHERE previous.operation_id=c.operation_id
 AND previous.kind='CANONICAL' AND previous.receipt_status=1 AND previous.sequence<c.sequence) THEN 'reapply' ELSE 'apply' END
 AND (c.kind='ORPHAN' OR (c.kind='CANONICAL' AND c.receipt_status=1)))
BEGIN SELECT RAISE(ABORT,'manual history requires exact recovery effect'); END;
CREATE TRIGGER manual_recovery_history_no_update BEFORE UPDATE ON position_effect_history
WHEN EXISTS(SELECT 1 FROM position_effects p JOIN manual_nonce_claims q ON q.attempt_id=p.attempt_id WHERE p.effect_id IN (OLD.effect_id,NEW.effect_id))
BEGIN SELECT RAISE(ABORT,'manual effect history immutable'); END;
CREATE TRIGGER manual_recovery_history_no_replace BEFORE INSERT ON position_effect_history
WHEN EXISTS(SELECT 1 FROM position_effect_history h JOIN position_effects p ON p.effect_id=h.effect_id
 JOIN manual_nonce_claims q ON q.attempt_id=p.attempt_id
 WHERE h.rowid=NEW.rowid OR h.id=NEW.id OR (h.effect_id=NEW.effect_id AND h.receipt_id=NEW.receipt_id AND h.transition=NEW.transition))
BEGIN SELECT RAISE(ABORT,'manual effect history retained'); END;
CREATE TRIGGER manual_recovery_history_no_delete BEFORE DELETE ON position_effect_history
WHEN EXISTS(SELECT 1 FROM position_effects p JOIN manual_nonce_claims q ON q.attempt_id=p.attempt_id WHERE p.effect_id=OLD.effect_id)
BEGIN SELECT RAISE(ABORT,'manual effect history retained'); END;

DROP TRIGGER manual_nonce_risk_no_release;
CREATE TRIGGER manual_nonce_risk_no_release BEFORE UPDATE ON canary_risk_reservations
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE reservation_id=OLD.id)
 AND NOT (NEW.state IS OLD.state OR (OLD.state='reserved' AND NEW.state='frozen'))
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_open c WHERE c.reservation_id=OLD.id AND NEW.rowid=OLD.rowid
 AND NEW.updated_at IS c.checked_at AND NEW.state IS CASE WHEN c.kind='ORPHAN' THEN 'frozen'
 WHEN c.receipt_status=1 THEN 'committed' ELSE 'released' END AND c.kind IN ('CANONICAL','ORPHAN'))
BEGIN SELECT RAISE(ABORT,'claimed manual risk requires canonical recovery settlement'); END;
DROP TRIGGER manual_nonce_usage_no_release;
CREATE TRIGGER manual_nonce_usage_no_release BEFORE UPDATE ON canary_authorization_operation_usage
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims WHERE operation_id=OLD.operation_id)
 AND NOT (NEW.state IS OLD.state OR (OLD.state='RESERVED' AND NEW.state='FROZEN'))
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_open c WHERE c.operation_id=OLD.operation_id AND c.authorization_id=OLD.authorization_id
 AND NEW.rowid=OLD.rowid AND NEW.updated_at IS c.checked_at
 AND NEW.state IS CASE WHEN c.kind='ORPHAN' THEN 'FROZEN' ELSE 'CONSUMED' END AND c.kind IN ('CANONICAL','ORPHAN'))
BEGIN SELECT RAISE(ABORT,'claimed manual usage requires canonical recovery settlement'); END;
DROP TRIGGER manual_execution_step_no_update;
CREATE TRIGGER manual_execution_step_no_update BEFORE UPDATE ON execution_steps
WHEN ((EXISTS(SELECT 1 FROM manual_execution_bindings WHERE execution_step_id=OLD.id)
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_step_transition n WHERE n.id=OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.step_json=json_object('id',NEW.id,'operation_id',NEW.operation_id,'step_index',NEW.step_index,'kind',NEW.kind,
 'status',NEW.status,'route_json',NEW.route_json,'parameters_json',NEW.parameters_json,'unsigned_call_json',NEW.unsigned_call_json,
 'created_at',NEW.created_at,'updated_at',NEW.updated_at,'wallet_id',NEW.wallet_id,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_step_transition n WHERE n.id IS OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.step_json=json_object('id',NEW.id,'operation_id',NEW.operation_id,'step_index',NEW.step_index,'kind',NEW.kind,
 'status',NEW.status,'route_json',NEW.route_json,'parameters_json',NEW.parameters_json,'unsigned_call_json',NEW.unsigned_call_json,
 'created_at',NEW.created_at,'updated_at',NEW.updated_at,'wallet_id',NEW.wallet_id,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_step_transition n WHERE n.step_id IS OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.step_json=json_object('id',NEW.id,'operation_id',NEW.operation_id,'step_index',NEW.step_index,'kind',NEW.kind,
 'status',NEW.status,'route_json',NEW.route_json,'parameters_json',NEW.parameters_json,'unsigned_call_json',NEW.unsigned_call_json,
 'created_at',NEW.created_at,'updated_at',NEW.updated_at,'wallet_id',NEW.wallet_id,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at))
BEGIN SELECT RAISE(ABORT,'manual execution step exact claim only'); END;

DROP TRIGGER manual_execution_step_no_adoption;
CREATE TRIGGER manual_execution_step_no_adoption BEFORE UPDATE ON execution_steps
WHEN ((EXISTS(SELECT 1 FROM manual_execution_bindings WHERE operation_id=NEW.operation_id OR execution_step_id=NEW.id)
 AND NOT EXISTS(SELECT 1 FROM manual_http_order_sources WHERE step_id=NEW.id AND operation_id=NEW.operation_id)
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_step_transition n WHERE n.id=OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.step_json=json_object('id',NEW.id,'operation_id',NEW.operation_id,'step_index',NEW.step_index,'kind',NEW.kind,
 'status',NEW.status,'route_json',NEW.route_json,'parameters_json',NEW.parameters_json,'unsigned_call_json',NEW.unsigned_call_json,
 'created_at',NEW.created_at,'updated_at',NEW.updated_at,'wallet_id',NEW.wallet_id,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_step_transition n WHERE n.id IS OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.step_json=json_object('id',NEW.id,'operation_id',NEW.operation_id,'step_index',NEW.step_index,'kind',NEW.kind,
 'status',NEW.status,'route_json',NEW.route_json,'parameters_json',NEW.parameters_json,'unsigned_call_json',NEW.unsigned_call_json,
 'created_at',NEW.created_at,'updated_at',NEW.updated_at,'wallet_id',NEW.wallet_id,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_step_transition n WHERE n.step_id IS OLD.id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.step_json=json_object('id',NEW.id,'operation_id',NEW.operation_id,'step_index',NEW.step_index,'kind',NEW.kind,
 'status',NEW.status,'route_json',NEW.route_json,'parameters_json',NEW.parameters_json,'unsigned_call_json',NEW.unsigned_call_json,
 'created_at',NEW.created_at,'updated_at',NEW.updated_at,'wallet_id',NEW.wallet_id,'policy_version',NEW.policy_version,
 'quote_block_number',NEW.quote_block_number,'quote_block_hash',NEW.quote_block_hash,'expires_at',NEW.expires_at))
BEGIN SELECT RAISE(ABORT,'manual execution step adoption forbidden'); END;

DROP TRIGGER manual_execution_lane_update;
CREATE TRIGGER manual_execution_lane_update BEFORE UPDATE ON execution_wallet_lanes
WHEN (((EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
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
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual lane exact nonce transition only'); END;

DROP TRIGGER manual_execution_lane_no_update;
CREATE TRIGGER manual_execution_lane_no_update BEFORE UPDATE ON execution_wallet_lanes
WHEN (((EXISTS(SELECT 1 FROM manual_execution_bindings WHERE wallet_id=OLD.wallet_id AND execution_step_id=OLD.step_id)
 AND NOT EXISTS(SELECT 1 FROM manual_nonce_lane_transition n WHERE n.wallet_id=OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_artifact_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_first_submission_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual lane downstream not connected'); END;

DROP TRIGGER manual_binding_lane_reference_update;
CREATE TRIGGER manual_binding_lane_reference_update BEFORE UPDATE ON execution_wallet_lanes
WHEN ((((EXISTS(SELECT 1 FROM manual_execution_bindings b
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
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual lane reference mutation forbidden'); END;

DROP TRIGGER manual_binding_lane_retained_update;
CREATE TRIGGER manual_binding_lane_retained_update BEFORE UPDATE ON execution_wallet_lanes
WHEN (((EXISTS(SELECT 1 FROM manual_execution_bindings b
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
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at)))
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_lane_transition n WHERE n.wallet_id IS OLD.wallet_id AND n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid
 AND n.lane_json=json_object('wallet_id',NEW.wallet_id,'address',NEW.address,'state',NEW.state,'operation_id',NEW.operation_id,
 'step_id',NEW.step_id,'reserved_nonce',NEW.reserved_nonce,'freeze_reason',NEW.freeze_reason,'updated_at',NEW.updated_at))
BEGIN SELECT RAISE(ABORT,'manual retained lane mutation forbidden'); END;

CREATE TRIGGER manual_recovery_effect AFTER INSERT ON manual_recovery_commands
BEGIN
 INSERT INTO receipt_observations(id,attempt_id,chain_id,tx_hash,block_number,block_hash,receipt_status,canonical_state,reconciliation_version,observed_at,updated_at)
 SELECT c.receipt_id,c.attempt_id,4663,c.tx_hash,c.block_number,c.block_hash,c.receipt_status,'observed',1,c.checked_at,c.checked_at
 FROM manual_recovery_open c WHERE c.id=NEW.id AND c.kind='OBSERVE';
 UPDATE receipt_observations SET canonical_state=CASE WHEN NEW.kind='ORPHAN' THEN 'orphaned'
 WHEN NEW.receipt_status=1 THEN 'canonical_success' ELSE 'canonical_revert' END,
 reconciliation_version=reconciliation_version+1,updated_at=NEW.checked_at
 WHERE id=NEW.receipt_id AND NEW.kind IN ('CANONICAL','ORPHAN');
 UPDATE position_effects SET receipt_id=NEW.receipt_id,delta=NEW.effect_delta,state='active',updated_at=NEW.checked_at
 WHERE attempt_id=(SELECT attempt_id FROM manual_recovery_open WHERE id=NEW.id)
 AND NEW.kind='CANONICAL' AND NEW.receipt_status=1;
 INSERT INTO position_effects(effect_id,attempt_id,receipt_id,wallet_id,asset,delta,state,applied_at,updated_at)
 SELECT c.effect_id,c.attempt_id,c.receipt_id,c.wallet_id,c.effect_asset,c.effect_delta,'active',c.checked_at,c.checked_at
 FROM manual_recovery_open c WHERE c.id=NEW.id AND c.kind='CANONICAL' AND c.receipt_status=1
 AND NOT EXISTS(SELECT 1 FROM position_effects existing WHERE existing.attempt_id=c.attempt_id);
 UPDATE position_effects SET state='rolled_back',updated_at=NEW.checked_at
 WHERE receipt_id=NEW.receipt_id AND state='active' AND NEW.kind='ORPHAN';
 INSERT INTO position_effect_history(id,effect_id,transition,receipt_id,created_at)
 SELECT 'manual-effect-history:'||c.id,c.effect_id,
 CASE WHEN c.kind='ORPHAN' THEN 'rollback'
 WHEN EXISTS(SELECT 1 FROM manual_recovery_commands previous WHERE previous.operation_id=c.operation_id
 AND previous.kind='CANONICAL' AND previous.receipt_status=1 AND previous.sequence<c.sequence) THEN 'reapply' ELSE 'apply' END,
 c.receipt_id,c.checked_at FROM manual_recovery_open c JOIN position_effects p ON p.effect_id=c.effect_id AND p.receipt_id=c.receipt_id
 WHERE c.id=NEW.id AND (c.kind='ORPHAN' OR (c.kind='CANONICAL' AND c.receipt_status=1))
 AND NOT EXISTS(SELECT 1 FROM position_effect_history h WHERE h.effect_id=c.effect_id AND h.receipt_id=c.receipt_id
 AND h.transition=CASE WHEN c.kind='ORPHAN' THEN 'rollback'
 WHEN EXISTS(SELECT 1 FROM manual_recovery_commands previous WHERE previous.operation_id=c.operation_id
 AND previous.kind='CANONICAL' AND previous.receipt_status=1 AND previous.sequence<c.sequence) THEN 'reapply' ELSE 'apply' END);
 UPDATE canary_risk_reservations SET state=CASE WHEN NEW.kind='ORPHAN' THEN 'frozen'
 WHEN NEW.receipt_status=1 THEN 'committed' ELSE 'released' END,updated_at=NEW.checked_at
 WHERE id=(SELECT reservation_id FROM manual_recovery_open WHERE id=NEW.id) AND NEW.kind IN ('CANONICAL','ORPHAN');
 UPDATE canary_authorization_operation_usage SET state=CASE WHEN NEW.kind='ORPHAN' THEN 'FROZEN' ELSE 'CONSUMED' END,updated_at=NEW.checked_at
 WHERE operation_id=NEW.operation_id AND NEW.kind IN ('CANONICAL','ORPHAN');
 UPDATE execution_steps SET status=CASE WHEN NEW.kind='ORPHAN' THEN 'reconciling'
 WHEN NEW.receipt_status=1 THEN 'canonical_success' ELSE 'canonical_revert' END,updated_at=NEW.checked_at
 WHERE id=(SELECT execution_step_id FROM manual_recovery_open WHERE id=NEW.id) AND NEW.kind IN ('CANONICAL','ORPHAN');
 UPDATE execution_wallet_lanes SET state=json_extract(n.lane_json,'$.state'),operation_id=json_extract(n.lane_json,'$.operation_id'),
 step_id=json_extract(n.lane_json,'$.step_id'),reserved_nonce=json_extract(n.lane_json,'$.reserved_nonce'),
 freeze_reason=json_extract(n.lane_json,'$.freeze_reason'),updated_at=json_extract(n.lane_json,'$.updated_at')
 FROM manual_recovery_lane_transition n WHERE execution_wallet_lanes.wallet_id=n.wallet_id AND n.id=NEW.id;
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT 'manual-recovery-audit:'||c.id,'MANUAL_RECOVERY_'||c.kind,c.operation_id,c.attempt_id,c.authorization_id,c.authorization_epoch,
 CASE WHEN EXISTS(SELECT 1 FROM execution_wallet_lanes l WHERE l.wallet_id=c.wallet_id AND l.freeze_reason='prior_canonical_reorg')
 THEN 'PRIOR_CANONICAL_REORG_LANE_FROZEN' ELSE c.kind END,
 json_object('command_id',c.id,'command_sequence',c.sequence,'receipt_id',c.receipt_id,'prior_version',c.prior_version,
 'block_hash',c.block_hash,'receipt_status',c.receipt_status,'lease_epoch',c.lease_epoch),c.checked_at
 FROM manual_recovery_open c WHERE c.id=NEW.id;
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 SELECT 'manual-recovery-alert:'||c.id,'manual-recovery-audit:'||c.id,
 CASE WHEN c.kind='ORPHAN' OR EXISTS(SELECT 1 FROM execution_wallet_lanes l WHERE l.wallet_id=c.wallet_id
 AND l.freeze_reason='prior_canonical_reorg') THEN 'CRITICAL' ELSE 'INFO' END,'PENDING',c.checked_at
 FROM manual_recovery_open c WHERE c.id=NEW.id;
 DELETE FROM manual_recovery_scopes WHERE command_id=NEW.id;
END;

CREATE TRIGGER manual_recovery_scope_close BEFORE DELETE ON manual_recovery_scopes
WHEN NOT EXISTS(SELECT 1 FROM manual_recovery_open c
 JOIN receipt_observations r ON r.id=c.receipt_id AND r.attempt_id=c.attempt_id AND r.tx_hash=c.tx_hash
 AND r.block_number=c.block_number AND r.block_hash=c.block_hash AND r.receipt_status=c.receipt_status
 AND r.reconciliation_version=c.prior_version+1
 AND r.canonical_state=CASE WHEN c.kind='OBSERVE' THEN 'observed' WHEN c.kind='ORPHAN' THEN 'orphaned'
 WHEN c.receipt_status=1 THEN 'canonical_success' ELSE 'canonical_revert' END
 JOIN canary_runtime_audit a ON a.id='manual-recovery-audit:'||c.id AND a.event_type='MANUAL_RECOVERY_'||c.kind
 AND a.operation_id=c.operation_id AND a.attempt_id=c.attempt_id AND a.authorization_id=c.authorization_id
 AND a.authorization_epoch=c.authorization_epoch AND a.created_at=c.checked_at
 JOIN canary_alert_outbox alert ON alert.id='manual-recovery-alert:'||c.id AND alert.audit_id=a.id AND alert.created_at=c.checked_at
 JOIN manual_http_order_sources src ON src.operation_id=c.operation_id
 JOIN manual_lifecycle_events event ON event.request_id=src.id AND event.audit_id=a.id
 WHERE c.id=OLD.command_id AND (c.kind='OBSERVE' OR (
 EXISTS(SELECT 1 FROM canary_risk_reservations risk WHERE risk.id=c.reservation_id
 AND risk.state=CASE WHEN c.kind='ORPHAN' THEN 'frozen' WHEN c.receipt_status=1 THEN 'committed' ELSE 'released' END
 AND risk.updated_at=c.checked_at)
 AND EXISTS(SELECT 1 FROM canary_authorization_operation_usage usage WHERE usage.operation_id=c.operation_id
 AND usage.authorization_id=c.authorization_id AND usage.state=CASE WHEN c.kind='ORPHAN' THEN 'FROZEN' ELSE 'CONSUMED' END
 AND usage.updated_at=c.checked_at)
 AND EXISTS(SELECT 1 FROM execution_steps step WHERE step.id=c.execution_step_id
 AND step.status=CASE WHEN c.kind='ORPHAN' THEN 'reconciling' WHEN c.receipt_status=1 THEN 'canonical_success' ELSE 'canonical_revert' END
 AND step.updated_at=c.checked_at)
 AND EXISTS(SELECT 1 FROM execution_wallet_lanes lane WHERE lane.wallet_id=c.wallet_id
 AND ((c.kind='CANONICAL' AND lane.state='idle' AND lane.operation_id IS NULL AND lane.step_id IS NULL AND lane.reserved_nonce IS NULL)
 OR (lane.state='frozen' AND lane.freeze_reason IN ('canonical_reorg','prior_canonical_reorg'))))
 AND (c.kind!='CANONICAL' OR c.receipt_status!=1 OR EXISTS(SELECT 1 FROM position_effects p
 WHERE p.effect_id=c.effect_id AND p.attempt_id=c.attempt_id AND p.receipt_id=c.receipt_id AND p.wallet_id=c.wallet_id
 AND p.asset=c.effect_asset AND p.delta=c.effect_delta AND p.state='active' AND p.updated_at=c.checked_at))
 AND (c.kind!='ORPHAN' OR NOT EXISTS(SELECT 1 FROM position_effects p WHERE p.attempt_id=c.attempt_id AND p.receipt_id=c.receipt_id AND p.state='active')))))
BEGIN SELECT RAISE(ABORT,'manual recovery incomplete transaction witnesses'); END;

CREATE TRIGGER manual_recovery_audit_source BEFORE INSERT ON canary_runtime_audit
WHEN substr(NEW.event_type,1,16)='MANUAL_RECOVERY_' OR substr(NEW.id,1,22)='manual-recovery-audit:'
BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_recovery_open c WHERE NEW.id IS 'manual-recovery-audit:'||c.id
 AND NEW.event_type IS 'MANUAL_RECOVERY_'||c.kind AND NEW.operation_id IS c.operation_id AND NEW.attempt_id IS c.attempt_id
 AND NEW.authorization_id IS c.authorization_id AND NEW.authorization_epoch IS c.authorization_epoch AND NEW.created_at IS c.checked_at
 AND NEW.reason_code IS CASE WHEN EXISTS(SELECT 1 FROM execution_wallet_lanes lane WHERE lane.wallet_id=c.wallet_id
 AND lane.freeze_reason='prior_canonical_reorg') THEN 'PRIOR_CANONICAL_REORG_LANE_FROZEN' ELSE c.kind END
 AND NEW.details_json IS json_object('command_id',c.id,'command_sequence',c.sequence,'receipt_id',c.receipt_id,'prior_version',c.prior_version,
 'block_hash',c.block_hash,'receipt_status',c.receipt_status,'lease_epoch',c.lease_epoch))
 THEN RAISE(ABORT,'manual recovery audit requires exact command scope') END;
END;
CREATE TRIGGER manual_recovery_audit_no_replace BEFORE INSERT ON canary_runtime_audit
WHEN EXISTS(SELECT 1 FROM canary_runtime_audit audit JOIN manual_recovery_commands c ON audit.id='manual-recovery-audit:'||c.id
 WHERE audit.rowid=NEW.rowid OR audit.id=NEW.id)
BEGIN SELECT RAISE(ABORT,'manual recovery audit retained'); END;
CREATE TRIGGER manual_recovery_alert_source BEFORE INSERT ON canary_alert_outbox
WHEN substr(NEW.id,1,22)='manual-recovery-alert:' OR substr(NEW.audit_id,1,22)='manual-recovery-audit:'
BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_recovery_open c WHERE NEW.id IS 'manual-recovery-alert:'||c.id
 AND NEW.audit_id IS 'manual-recovery-audit:'||c.id AND NEW.state IS 'PENDING' AND NEW.delivered_at IS NULL AND NEW.created_at IS c.checked_at
 AND NEW.severity IS CASE WHEN c.kind='ORPHAN' OR EXISTS(SELECT 1 FROM execution_wallet_lanes lane WHERE lane.wallet_id=c.wallet_id
 AND lane.freeze_reason='prior_canonical_reorg') THEN 'CRITICAL' ELSE 'INFO' END)
 THEN RAISE(ABORT,'manual recovery alert requires exact command scope') END;
END;
CREATE TRIGGER manual_recovery_alert_no_replace BEFORE INSERT ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM canary_alert_outbox alert JOIN manual_recovery_commands c ON alert.id='manual-recovery-alert:'||c.id
 WHERE alert.rowid=NEW.rowid OR alert.id=NEW.id OR alert.audit_id=NEW.audit_id)
BEGIN SELECT RAISE(ABORT,'manual recovery alert retained'); END;
CREATE TRIGGER manual_recovery_alert_identity BEFORE UPDATE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_recovery_commands c WHERE OLD.id='manual-recovery-alert:'||c.id OR NEW.id='manual-recovery-alert:'||c.id)
 AND (NEW.rowid!=OLD.rowid OR NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id OR NEW.severity IS NOT OLD.severity OR NEW.created_at IS NOT OLD.created_at)
BEGIN SELECT RAISE(ABORT,'manual recovery alert identity immutable'); END;
CREATE TRIGGER manual_recovery_alert_no_delete BEFORE DELETE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_recovery_commands c WHERE OLD.id='manual-recovery-alert:'||c.id)
BEGIN SELECT RAISE(ABORT,'manual recovery alert retained'); END;

CREATE VIEW manual_recovery_current AS
SELECT c.operation_id,first.attempt_id,c.id AS command_id,c.sequence AS command_sequence,c.receipt_id,
 r.reconciliation_version,r.canonical_state,COALESCE(p.state,'') AS effect_state
FROM manual_recovery_commands c JOIN manual_first_submission_bindings first ON first.operation_id=c.operation_id
JOIN receipt_observations r ON r.id=c.receipt_id AND r.attempt_id=first.attempt_id AND r.tx_hash=first.tx_hash
 AND r.block_hash=c.block_hash AND r.block_number=c.block_number AND r.receipt_status=c.receipt_status
 AND r.reconciliation_version=c.prior_version+1
 AND r.canonical_state=CASE WHEN c.kind='OBSERVE' THEN 'observed' WHEN c.kind='ORPHAN' THEN 'orphaned'
 WHEN c.receipt_status=1 THEN 'canonical_success' ELSE 'canonical_revert' END
JOIN canary_runtime_audit audit ON audit.id='manual-recovery-audit:'||c.id AND audit.event_type='MANUAL_RECOVERY_'||c.kind
 AND audit.operation_id=c.operation_id AND audit.attempt_id=first.attempt_id AND audit.created_at=c.checked_at
JOIN canary_alert_outbox alert ON alert.id='manual-recovery-alert:'||c.id AND alert.audit_id=audit.id AND alert.created_at=c.checked_at
JOIN manual_http_order_sources src ON src.operation_id=c.operation_id
JOIN manual_lifecycle_events event ON event.request_id=src.id AND event.audit_id=audit.id
LEFT JOIN position_effects p ON p.attempt_id=first.attempt_id
WHERE c.sequence=(SELECT MAX(latest.sequence) FROM manual_recovery_commands latest WHERE latest.operation_id=c.operation_id)
 AND NOT EXISTS(SELECT 1 FROM manual_recovery_scopes scope WHERE scope.command_id=c.id);


