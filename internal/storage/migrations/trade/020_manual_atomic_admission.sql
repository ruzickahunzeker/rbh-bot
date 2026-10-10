-- One immutable command, one statement, existing financial/lifecycle owners.
-- No arming, signing, nonce, artifact, permit, PASS snapshot or HTTP wiring.
-- Shape/source equality is not cryptographic validation. The disconnected Go
-- writer MUST independently verify all canonical hashes on its owned snapshot.
CREATE TABLE manual_admission_commands(
 operation_id TEXT PRIMARY KEY REFERENCES manual_http_order_sources(operation_id),
 request_id TEXT NOT NULL UNIQUE REFERENCES manual_http_order_sources(id),
 step_id TEXT NOT NULL UNIQUE REFERENCES execution_steps(id),
 reservation_id TEXT NOT NULL UNIQUE,
 authorization_id TEXT NOT NULL REFERENCES manual_authorization_units(authorization_id),
 authorization_epoch INTEGER NOT NULL,
 manual_policy_id TEXT NOT NULL REFERENCES manual_risk_policies(id),
 manual_policy_hash TEXT NOT NULL,
 environment TEXT NOT NULL,
 build_sha TEXT NOT NULL,
 release_id TEXT NOT NULL,
 deployment_id TEXT NOT NULL,
 gate_evidence_set_hash TEXT NOT NULL,
 contract_json TEXT NOT NULL CHECK(json_valid(contract_json) AND json(contract_json)=contract_json),
 contract_hash TEXT NOT NULL CHECK(length(contract_hash)=64 AND contract_hash NOT GLOB '*[^0-9a-f]*'),
 checked_at TEXT NOT NULL,
 FOREIGN KEY(authorization_id,authorization_epoch) REFERENCES canary_runtime_authorizations(id,epoch)
);
CREATE TRIGGER manual_command_no_update BEFORE UPDATE ON manual_admission_commands
BEGIN SELECT RAISE(ABORT,'manual command immutable'); END;
CREATE TRIGGER manual_command_no_delete BEFORE DELETE ON manual_admission_commands
BEGIN SELECT RAISE(ABORT,'manual command immutable'); END;
CREATE TRIGGER manual_command_no_replace BEFORE INSERT ON manual_admission_commands
WHEN EXISTS(SELECT 1 FROM manual_admission_commands WHERE operation_id=NEW.operation_id OR request_id=NEW.request_id
 OR step_id=NEW.step_id OR reservation_id=NEW.reservation_id)
BEGIN SELECT RAISE(ABORT,'manual command already exists'); END;

CREATE TRIGGER manual_command_source BEFORE INSERT ON manual_admission_commands
WHEN NOT EXISTS(
 SELECT 1 FROM manual_http_order_sources m
 JOIN operations o ON o.id=m.operation_id JOIN execution_steps s ON s.id=m.step_id
 JOIN manual_trade_resolutions r ON r.operation_id=m.operation_id
 JOIN manual_trade_validations v ON v.operation_id=m.operation_id
 JOIN manual_risk_policies p ON p.id=NEW.manual_policy_id
 JOIN canary_policies cp ON cp.version=r.policy_version
 JOIN manual_authorization_units u ON u.authorization_id=NEW.authorization_id
 JOIN canary_runtime_authorizations a ON a.id=u.authorization_id
 JOIN canary_control_state ctl ON ctl.singleton=1
 WHERE m.operation_id=NEW.operation_id AND m.id=NEW.request_id AND m.step_id=NEW.step_id
 AND NEW.reservation_id='manual-risk:'||m.operation_id
 AND o.status='manual_validation_succeeded' AND s.status='validation_succeeded'
 AND o.wallet_id=m.wallet_id AND s.wallet_id=m.wallet_id AND o.chain_id=4663
 AND m.source='MANUAL' AND m.chain='robinhood' AND m.send_authorized=0 AND m.submission_queued=0
 AND o.expires_at=m.expires_at AND s.expires_at=m.expires_at AND r.expires_at=m.expires_at
 AND o.policy_version=r.policy_version AND s.policy_version=r.policy_version
 AND s.parameters_json=v.result_json AND s.quote_block_number=json_extract(v.result_json,'$.resolution.token.block.number')
 AND s.quote_block_hash=json_extract(v.result_json,'$.resolution.token.block.hash')
 AND p.policy_hash=NEW.manual_policy_hash AND p.source_policy_version=r.policy_version
 AND p.source_policy_hash=r.policy_hash AND cp.policy_hash=r.policy_hash
 AND u.authorization_epoch=NEW.authorization_epoch AND u.manual_policy_id=p.id AND u.manual_policy_hash=p.policy_hash
 AND u.chain_id=4663 AND u.accounting_kind='TRADE_INPUT' AND u.source_mode='MANUAL'
 AND a.epoch=NEW.authorization_epoch AND a.state='ARMED' AND a.mode='CONTROLLED_CANARY'
 AND a.wallet_id=m.wallet_id AND a.chain_id=4663 AND a.policy_version=r.policy_version
 AND a.environment=NEW.environment AND a.build_sha=NEW.build_sha AND a.release_id=NEW.release_id
 AND a.deployment_id=NEW.deployment_id AND a.gate_evidence_set_hash=NEW.gate_evidence_set_hash
 AND ctl.emergency_stopped=0 AND ctl.mode='CONTROLLED_CANARY' AND ctl.chain_id=4663 AND ctl.policy_version=r.policy_version
 AND manual_time_cmp(NEW.checked_at,ctl.effective_at)>=0 AND manual_time_cmp(NEW.checked_at,ctl.updated_at)>=0
 AND manual_time_cmp(NEW.checked_at,a.authorized_at)>=0 AND manual_time_cmp(NEW.checked_at,a.created_at)>=0
 AND manual_time_cmp(NEW.checked_at,a.updated_at)>=0 AND manual_time_cmp(NEW.checked_at,a.expires_at)<0
 AND manual_time_cmp(NEW.checked_at,m.created_at)>=0 AND manual_time_cmp(NEW.checked_at,v.created_at)>=0
 AND manual_time_cmp(NEW.checked_at,m.expires_at)<0
 AND json_extract(NEW.contract_json,'$.status')='PARAMETER_CONTRACT_NOT_ADMITTED'
 AND json_extract(NEW.contract_json,'$.version')=1
 AND json_extract(NEW.contract_json,'$.source')='MANUAL'
 AND json_extract(NEW.contract_json,'$.request_id')=m.id
 AND json_extract(NEW.contract_json,'$.principal_id')=m.principal_id
 AND json_extract(NEW.contract_json,'$.operation_id')=m.operation_id AND json_extract(NEW.contract_json,'$.step_id')=m.step_id
 AND json_extract(NEW.contract_json,'$.wallet_id')=m.wallet_id AND json_extract(NEW.contract_json,'$.wallet_address')=m.wallet_address
 AND json_extract(NEW.contract_json,'$.chain_id')=4663
 AND json_extract(NEW.contract_json,'$.request_fingerprint')=m.request_fingerprint
 AND json_extract(NEW.contract_json,'$.request')=json(m.request_json)
 AND json_extract(NEW.contract_json,'$.api_policy_version')=m.api_policy_version
 AND json_extract(NEW.contract_json,'$.api_policy_hash')=m.api_policy_hash
 AND json_extract(NEW.contract_json,'$.policy_version')=r.policy_version AND json_extract(NEW.contract_json,'$.policy_hash')=r.policy_hash
 AND json_extract(NEW.contract_json,'$.resolution_hash')=r.resolution_hash
 AND json_extract(NEW.contract_json,'$.result_hash')=v.result_hash
 AND json_extract(NEW.contract_json,'$.output')=json(v.result_json)
 AND json_extract(NEW.contract_json,'$.created_at')=m.created_at AND json_extract(NEW.contract_json,'$.expires_at')=m.expires_at
 AND json_extract(NEW.contract_json,'$.deadline_capability')='APPLICATION_TTL_ONLY'
 AND json_type(NEW.contract_json,'$.contract_deadline')='false'
 AND json_type(NEW.contract_json,'$.admission_authorized')='false' AND json_type(NEW.contract_json,'$.send_authorized')='false'
 AND json_extract(NEW.contract_json,'$.binding_hash')=NEW.contract_hash
 AND EXISTS(SELECT 1 FROM dry_run_wallets w JOIN canary_wallet_allowlist wa ON wa.wallet_id=w.id
 WHERE w.id=m.wallet_id AND w.chain_id=4663 AND w.enabled=1 AND lower(w.address)=lower(m.wallet_address)
 AND wa.chain_id=4663 AND wa.policy_version=r.policy_version AND wa.enabled=1 AND lower(wa.address)=lower(m.wallet_address))
 AND EXISTS(SELECT 1 FROM canary_contract_allowlist ca JOIN canary_token_allowlist ta ON ta.policy_version=ca.policy_version
 WHERE ca.policy_version=r.policy_version AND ca.protocol='PONS_V2_CURVE' AND ca.role='CURVE' AND ca.enabled=1 AND ca.baseline_ref!=''
 AND lower(ca.address)=json_extract(v.result_json,'$.resolution.token.curve')
 AND ca.runtime_code_hash=json_extract(v.result_json,'$.resolution.token.curve_runtime_code_hash')
 AND ta.protocol='PONS_V2_CURVE' AND ta.enabled=1 AND lower(ta.token_address)=json_extract(v.result_json,'$.resolution.token.address'))
 AND (SELECT COUNT(*) FROM canary_gate_attestations g WHERE g.status='PASS' AND manual_time_cmp(NEW.checked_at,g.attested_at)>=0
 AND ((g.gate='C04' AND g.evidence_hash='c1af9454a6a4433b488158c25224d13dfbf31178c71dae7468c80917743381fd')
 OR (g.gate='C05' AND g.evidence_hash='1a793f13d9304a31ed405f9fe25f1713cf79b6f8d28bb3da8ad16a01dc24c40b')
 OR (g.gate='C08' AND g.evidence_hash='8178e7b976dbea67be1de83cec529171e56d154cd2114f1bf290bebbd1fc77d8')))=3
 AND json_extract(NEW.contract_json,'$.input.chain_id')=u.chain_id
 AND json_extract(NEW.contract_json,'$.input.asset')=u.input_asset
 AND json_extract(NEW.contract_json,'$.input.decimals')=u.input_decimals
 AND json_type(NEW.contract_json,'$.input.units')='text'
 AND json_extract(NEW.contract_json,'$.input.units')=json_extract(v.result_json,'$.resolution.amount_in')
 AND ((json_extract(v.result_json,'$.resolution.side')='buy' AND u.input_asset='native' AND u.input_decimals=18)
 OR (json_extract(v.result_json,'$.resolution.side')='sell' AND u.input_asset=json_extract(v.result_json,'$.resolution.token.address')
 AND u.input_decimals=json_extract(v.result_json,'$.resolution.token.decimals')))
)
BEGIN SELECT RAISE(ABORT,'manual command source/control/authorization mismatch'); END;

-- Reject pre-existing halves; do not adopt orphan metadata or another auth.
CREATE TRIGGER manual_command_no_halves BEFORE INSERT ON manual_admission_commands
WHEN EXISTS(SELECT 1 FROM canary_risk_reservations WHERE operation_id=NEW.operation_id OR id=NEW.reservation_id)
 OR EXISTS(SELECT 1 FROM manual_reservation_units WHERE operation_id=NEW.operation_id OR reservation_id=NEW.reservation_id)
 OR EXISTS(SELECT 1 FROM canary_authorization_operation_usage WHERE operation_id=NEW.operation_id)
 OR EXISTS(SELECT 1 FROM canary_admission_decisions WHERE operation_id=NEW.operation_id OR request_id='manual-admission:'||NEW.request_id)
 OR EXISTS(SELECT 1 FROM canary_admission_attempts WHERE operation_id=NEW.operation_id AND outcome!='REJECTED')
 OR EXISTS(SELECT 1 FROM execution_wallet_lanes WHERE wallet_id=json_extract(NEW.contract_json,'$.wallet_id') AND state!='idle')
 OR EXISTS(SELECT 1 FROM execution_reservations WHERE wallet_id=json_extract(NEW.contract_json,'$.wallet_id') AND status IN ('reserved','signed','frozen'))
 OR EXISTS(SELECT 1 FROM execution_steps WHERE wallet_id=json_extract(NEW.contract_json,'$.wallet_id')
 AND status IN ('nonce_reserved','signing','signed','submitted','broadcast_unknown','reconciling','manual_resolution'))
BEGIN SELECT RAISE(ABORT,'manual command orphan or unresolved work'); END;

CREATE TRIGGER manual_command_budget BEFORE INSERT ON manual_admission_commands
WHEN NOT EXISTS(
 SELECT 1 FROM canary_authorization_usage u JOIN canary_runtime_authorizations a ON a.id=u.authorization_id
 WHERE a.id=NEW.authorization_id
 AND u.operation_count=(SELECT COUNT(*) FROM canary_authorization_operation_usage WHERE authorization_id=a.id)
 AND u.total_input=(SELECT manual_u256_sum_json(json_group_array(amount)) FROM canary_authorization_operation_usage WHERE authorization_id=a.id)
 AND u.operation_count<a.max_operations
 AND manual_u256_cmp(manual_u256_add(u.total_input,json_extract(NEW.contract_json,'$.input.units')),a.max_total_input)<=0
 AND NOT EXISTS(SELECT 1 FROM canary_authorization_operation_usage h LEFT JOIN manual_admission_commands c
 ON c.operation_id=h.operation_id AND c.authorization_id=h.authorization_id WHERE h.authorization_id=a.id AND c.operation_id IS NULL)
 AND (SELECT COUNT(*) FROM manual_admission_complete WHERE authorization_id=a.id)=u.operation_count
)
BEGIN SELECT RAISE(ABORT,'manual command budget conservation/cap invalid'); END;

-- Exact dimensions and authoritative rows, never legacy raw window totals.
CREATE VIEW manual_atomic_exposure AS
 SELECT rr.*,u.chain_id,u.input_asset,u.input_decimals,u.accounting_kind,
 manual_u256_add(rr.gas_budget,CASE WHEN u.input_asset='native' THEN rr.input_amount ELSE '0' END) AS native_outlay
 FROM canary_risk_reservations rr JOIN manual_reservation_units u ON u.reservation_id=rr.id;
CREATE TRIGGER manual_command_risk BEFORE INSERT ON manual_admission_commands
BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM canary_risk_reservations rr
 WHERE (rr.state IN ('reserved','frozen') OR manual_time_cmp(rr.created_at,
 manual_time_shift_seconds(NEW.checked_at,-(SELECT json_extract(policy_json,'$.window_seconds') FROM manual_risk_policies WHERE id=NEW.manual_policy_id)))>=0)
 AND NOT EXISTS(SELECT 1 FROM manual_reservation_units u WHERE u.reservation_id=rr.id))
 OR EXISTS(SELECT 1 FROM canary_window_usage w LEFT JOIN canary_policies p ON p.version=w.policy_version
 WHERE manual_time_cmp(manual_time_shift_seconds(w.window_start,p.window_seconds),
 manual_time_shift_seconds(NEW.checked_at,-(SELECT json_extract(policy_json,'$.window_seconds') FROM manual_risk_policies WHERE id=NEW.manual_policy_id)))>=0)
 THEN RAISE(ABORT,'manual command untyped history') END;
 SELECT CASE WHEN EXISTS(SELECT 1 FROM canary_risk_reservations WHERE manual_time_cmp(created_at,NEW.checked_at)>0)
 OR EXISTS(SELECT 1 FROM manual_atomic_exposure e WHERE e.state IN ('reserved','frozen') AND NOT EXISTS(
 SELECT 1 FROM manual_risk_policies p,json_each(p.policy_json,'$.input_limits') l WHERE p.id=NEW.manual_policy_id
 AND json_extract(l.value,'$.unit.chain_id')=e.chain_id AND json_extract(l.value,'$.unit.asset')=e.input_asset
 AND json_extract(l.value,'$.unit.decimals')=e.input_decimals AND json_extract(l.value,'$.unit.kind')=e.accounting_kind))
 THEN RAISE(ABORT,'manual command exposure time/unit invalid') END;
 SELECT CASE WHEN NOT EXISTS(
 SELECT 1 FROM manual_risk_policies p,json_each(p.policy_json,'$.input_limits') l
 WHERE p.id=NEW.manual_policy_id
 AND json_extract(l.value,'$.unit.chain_id')=4663 AND json_extract(l.value,'$.unit.kind')='TRADE_INPUT'
 AND json_extract(l.value,'$.unit.asset')=json_extract(NEW.contract_json,'$.input.asset')
 AND json_extract(l.value,'$.unit.decimals')=json_extract(NEW.contract_json,'$.input.decimals')
 AND manual_u256_cmp(json_extract(NEW.contract_json,'$.input.units'),json_extract(l.value,'$.max_operation'))<=0
 AND manual_u256_cmp(manual_u256_add(json_extract(NEW.contract_json,'$.input.units'),
 (SELECT manual_u256_sum_json(json_group_array(input_amount)) FROM manual_atomic_exposure WHERE state IN ('reserved','frozen')
 AND chain_id=4663 AND accounting_kind='TRADE_INPUT' AND input_asset=json_extract(l.value,'$.unit.asset') AND input_decimals=json_extract(l.value,'$.unit.decimals'))),json_extract(l.value,'$.max_global_active'))<=0
 AND manual_u256_cmp(manual_u256_add(json_extract(NEW.contract_json,'$.input.units'),
 (SELECT manual_u256_sum_json(json_group_array(input_amount)) FROM manual_atomic_exposure WHERE state IN ('reserved','frozen')
 AND wallet_id=json_extract(NEW.contract_json,'$.wallet_id') AND chain_id=4663 AND accounting_kind='TRADE_INPUT'
 AND input_asset=json_extract(l.value,'$.unit.asset') AND input_decimals=json_extract(l.value,'$.unit.decimals'))),json_extract(l.value,'$.max_wallet_active'))<=0
 AND manual_u256_cmp(json_extract(NEW.contract_json,'$.gas_budget.units'),json_extract(p.policy_json,'$.max_native_gas_per_operation'))<=0
 AND json_extract(NEW.contract_json,'$.gas_budget.units')=json_extract(NEW.contract_json,'$.output.gas_budget')
 AND json_extract(NEW.contract_json,'$.native_value.units')=CASE WHEN json_extract(NEW.contract_json,'$.input.asset')='native' THEN json_extract(NEW.contract_json,'$.input.units') ELSE '0' END
 AND manual_u256_cmp(manual_u256_add(manual_u256_add(json_extract(NEW.contract_json,'$.native_value.units'),json_extract(NEW.contract_json,'$.gas_budget.units')),
 (SELECT manual_u256_sum_json(json_group_array(native_outlay)) FROM manual_atomic_exposure WHERE state IN ('reserved','frozen'))),json_extract(p.policy_json,'$.max_native_global_outlay'))<=0
 AND manual_u256_cmp(manual_u256_add(manual_u256_add(json_extract(NEW.contract_json,'$.native_value.units'),json_extract(NEW.contract_json,'$.gas_budget.units')),
 (SELECT manual_u256_sum_json(json_group_array(native_outlay)) FROM manual_atomic_exposure WHERE state IN ('reserved','frozen') AND wallet_id=json_extract(NEW.contract_json,'$.wallet_id'))),json_extract(p.policy_json,'$.max_native_wallet_outlay'))<=0
 AND manual_u256_cmp(manual_u256_add(manual_u256_add(json_extract(NEW.contract_json,'$.native_value.units'),json_extract(NEW.contract_json,'$.gas_budget.units')),
 json_extract(p.policy_json,'$.min_native_balance')),json_extract(NEW.contract_json,'$.output.resolution.native_balance'))<=0
 AND (SELECT COUNT(*) FROM canary_risk_reservations WHERE manual_time_cmp(created_at,manual_time_shift_seconds(NEW.checked_at,-json_extract(p.policy_json,'$.window_seconds')))>=0)<json_extract(p.policy_json,'$.max_global_operations_window')
 AND (SELECT COUNT(*) FROM canary_risk_reservations WHERE wallet_id=json_extract(NEW.contract_json,'$.wallet_id')
 AND manual_time_cmp(created_at,manual_time_shift_seconds(NEW.checked_at,-json_extract(p.policy_json,'$.window_seconds')))>=0)<json_extract(p.policy_json,'$.max_wallet_operations_window')
 ) THEN RAISE(ABORT,'manual command risk cap invalid') END;
END;

-- A completed binding is defined by ALL authoritative rows, not a status or
-- caller Boolean. This view is also used to reject incomplete budget history.
CREATE VIEW manual_admission_complete AS
 SELECT c.* FROM manual_admission_commands c
 JOIN operations o ON o.id=c.operation_id AND o.status='manual_admission_succeeded'
 JOIN execution_steps s ON s.id=c.step_id AND s.status='manual_admitted' AND s.operation_id=c.operation_id
 JOIN canary_risk_reservations rr ON rr.id=c.reservation_id AND rr.operation_id=c.operation_id
 AND rr.wallet_id=json_extract(c.contract_json,'$.wallet_id') AND rr.input_amount=json_extract(c.contract_json,'$.input.units')
 AND rr.gas_budget=json_extract(c.contract_json,'$.gas_budget.units') AND rr.created_at=c.checked_at
 AND rr.token_address=json_extract(c.contract_json,'$.output.resolution.token.address') AND rr.policy_version=json_extract(c.contract_json,'$.policy_version')
 AND rr.quote_block_number=json_extract(c.contract_json,'$.output.resolution.token.block.number')
 AND rr.quote_block_hash=json_extract(c.contract_json,'$.output.resolution.token.block.hash') AND rr.source_hash=json_extract(c.contract_json,'$.result_hash')
 JOIN manual_reservation_units u ON u.reservation_id=c.reservation_id AND u.operation_id=c.operation_id
 AND u.manual_policy_id=c.manual_policy_id AND u.manual_policy_hash=c.manual_policy_hash
 AND u.chain_id=4663 AND u.input_asset=json_extract(c.contract_json,'$.input.asset') AND u.input_decimals=json_extract(c.contract_json,'$.input.decimals')
 AND u.accounting_kind='TRADE_INPUT' AND u.result_hash=json_extract(c.contract_json,'$.result_hash')
 AND u.request_fingerprint=json_extract(c.contract_json,'$.request_fingerprint') AND u.expires_at=json_extract(c.contract_json,'$.expires_at') AND u.created_at=c.checked_at
 JOIN canary_runtime_audit ua ON ua.id='manual-reservation-unit:'||c.reservation_id AND ua.operation_id=c.operation_id
 AND ua.event_type='MANUAL_RESERVATION_UNIT_BOUND' AND ua.reason_code='METADATA_NOT_ADMISSION' AND ua.created_at=c.checked_at
 AND json_extract(ua.details_json,'$.reservation_id')=c.reservation_id AND json_extract(ua.details_json,'$.policy_id')=c.manual_policy_id
 AND json_extract(ua.details_json,'$.result_hash')=u.result_hash AND json_extract(ua.details_json,'$.input_asset')=u.input_asset
 AND json_extract(ua.details_json,'$.input_decimals')=u.input_decimals
 JOIN canary_alert_outbox ualert ON ualert.id='manual-reservation-unit-alert:'||c.reservation_id AND ualert.audit_id=ua.id
 AND ualert.severity='INFO' AND ualert.created_at=c.checked_at
 JOIN canary_authorization_operation_usage h ON h.authorization_id=c.authorization_id AND h.operation_id=c.operation_id
 AND h.amount=json_extract(c.contract_json,'$.input.units') AND h.created_at=c.checked_at
 JOIN canary_admission_decisions d ON d.id='manual-decision:'||c.operation_id AND d.operation_id=c.operation_id AND d.reservation_id=c.reservation_id
 AND d.request_id='manual-admission:'||c.request_id AND d.decision='ADMITTED' AND d.reason_code='ADMITTED_NOT_SIGNED_NOT_SUBMITTED'
 AND d.wallet_id=rr.wallet_id AND d.token_address=rr.token_address AND d.policy_version=rr.policy_version
 AND d.amount=rr.input_amount AND d.request_fingerprint=u.request_fingerprint AND d.created_at=c.checked_at
 JOIN canary_admission_attempts t ON t.id='manual-attempt:'||c.operation_id AND t.operation_id=c.operation_id AND t.decision_id=d.id
 AND t.request_id=d.request_id AND t.outcome='ADMITTED' AND t.reason_code=d.reason_code AND t.created_at=c.checked_at
 JOIN canary_audit_events ca ON ca.id='manual-decision-audit:'||c.operation_id AND ca.decision_id=d.id
 AND ca.event_type='CANARY_ADMISSION_DECISION' AND ca.policy_version=d.policy_version AND ca.reason_code=d.reason_code AND ca.created_at=c.checked_at
 JOIN canary_runtime_audit a ON a.id='manual-admission:'||c.operation_id AND a.operation_id=c.operation_id AND a.authorization_id=c.authorization_id
 AND a.authorization_epoch=c.authorization_epoch AND a.event_type='MANUAL_ATOMIC_ADMISSION' AND a.reason_code=d.reason_code AND a.created_at=c.checked_at
 AND json_extract(a.details_json,'$.request_id')=c.request_id AND json_extract(a.details_json,'$.reservation_id')=c.reservation_id
 AND json_extract(a.details_json,'$.contract_hash')=c.contract_hash AND json_extract(a.details_json,'$.policy_hash')=c.manual_policy_hash
 AND json_extract(a.details_json,'$.gate_evidence_set_hash')=c.gate_evidence_set_hash
 JOIN canary_alert_outbox alert ON alert.id='manual-admission-alert:'||c.operation_id AND alert.audit_id=a.id AND alert.severity='INFO' AND alert.created_at=c.checked_at;

CREATE TRIGGER manual_command_audit_no_replace BEFORE INSERT ON canary_runtime_audit
WHEN EXISTS(SELECT 1 FROM canary_runtime_audit a JOIN manual_admission_commands c ON a.id='manual-admission:'||c.operation_id
 OR a.id='manual-reservation-unit:'||c.reservation_id WHERE a.id=NEW.id)
BEGIN SELECT RAISE(ABORT,'manual command audit exists'); END;
CREATE TRIGGER manual_command_legacy_audit_no_update BEFORE UPDATE ON canary_audit_events
WHEN EXISTS(SELECT 1 FROM manual_admission_commands c WHERE OLD.id='manual-decision-audit:'||c.operation_id)
BEGIN SELECT RAISE(ABORT,'manual command audit immutable'); END;
CREATE TRIGGER manual_command_legacy_audit_no_delete BEFORE DELETE ON canary_audit_events
WHEN EXISTS(SELECT 1 FROM manual_admission_commands c WHERE OLD.id='manual-decision-audit:'||c.operation_id)
BEGIN SELECT RAISE(ABORT,'manual command audit immutable'); END;
CREATE TRIGGER manual_command_legacy_audit_no_replace BEFORE INSERT ON canary_audit_events
WHEN EXISTS(SELECT 1 FROM canary_audit_events a JOIN manual_admission_commands c ON a.id='manual-decision-audit:'||c.operation_id WHERE a.id=NEW.id)
BEGIN SELECT RAISE(ABORT,'manual command audit exists'); END;

-- Replace ONLY the isolation guards for budget and positive ledger writes.
-- no-arm, no-permit and no-PASS-snapshot remain unchanged.
DROP TRIGGER manual_b1_no_budget;
CREATE TRIGGER manual_b1_no_budget BEFORE INSERT ON canary_authorization_operation_usage
WHEN EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.authorization_id)
 AND NOT EXISTS(SELECT 1 FROM manual_admission_commands c WHERE c.authorization_id=NEW.authorization_id AND c.operation_id=NEW.operation_id
 AND NEW.amount=json_extract(c.contract_json,'$.input.units') AND NEW.state='RESERVED'
 AND NEW.created_at=c.checked_at AND NEW.updated_at=c.checked_at
 AND NOT EXISTS(SELECT 1 FROM canary_authorization_operation_usage WHERE operation_id=NEW.operation_id))
BEGIN SELECT RAISE(ABORT,'manual budget requires atomic command'); END;
DROP TRIGGER manual_b1_no_usage_update;
CREATE TRIGGER manual_b1_no_usage_update BEFORE UPDATE ON canary_authorization_usage
WHEN EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=OLD.authorization_id OR authorization_id=NEW.authorization_id)
 AND (NEW.authorization_id IS NOT OLD.authorization_id OR NOT EXISTS(
 SELECT 1 FROM manual_admission_commands c JOIN canary_authorization_operation_usage h ON h.operation_id=c.operation_id AND h.authorization_id=c.authorization_id
 WHERE c.authorization_id=OLD.authorization_id AND c.checked_at=NEW.updated_at
 AND NEW.operation_count=OLD.operation_count+1 AND NEW.total_input=manual_u256_add(OLD.total_input,h.amount)
 AND NEW.operation_count=(SELECT COUNT(*) FROM canary_authorization_operation_usage WHERE authorization_id=c.authorization_id)
 AND NEW.total_input=(SELECT manual_u256_sum_json(json_group_array(amount)) FROM canary_authorization_operation_usage WHERE authorization_id=c.authorization_id)
 AND NOT EXISTS(SELECT 1 FROM canary_admission_decisions WHERE id='manual-decision:'||c.operation_id)))
BEGIN SELECT RAISE(ABORT,'manual aggregate requires complete atomic command'); END;

CREATE TRIGGER manual_command_usage_identity BEFORE UPDATE ON canary_authorization_operation_usage
WHEN EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=OLD.authorization_id OR authorization_id=NEW.authorization_id)
 AND (NEW.authorization_id IS NOT OLD.authorization_id OR NEW.operation_id IS NOT OLD.operation_id OR NEW.amount IS NOT OLD.amount
 OR NEW.created_at IS NOT OLD.created_at)
BEGIN SELECT RAISE(ABORT,'manual operation budget identity immutable'); END;
CREATE TRIGGER manual_command_usage_no_delete BEFORE DELETE ON canary_authorization_operation_usage
WHEN EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=OLD.authorization_id)
BEGIN SELECT RAISE(ABORT,'manual consumed allowance cannot be deleted'); END;
CREATE TRIGGER manual_command_other_auth BEFORE INSERT ON canary_authorization_operation_usage
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
 AND NOT EXISTS(SELECT 1 FROM manual_admission_commands WHERE operation_id=NEW.operation_id AND authorization_id=NEW.authorization_id)
BEGIN SELECT RAISE(ABORT,'manual operation cannot acquire another budget'); END;

-- Metadata-only foundation fixtures remain possible without a declared auth;
-- they cannot create a positive decision or usage. Declared MANUAL wallets
-- require command-owned financial writes, not standalone reservation halves.
CREATE TRIGGER manual_command_parent_insert BEFORE INSERT ON canary_risk_reservations
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources m JOIN canary_runtime_authorizations a ON a.wallet_id=m.wallet_id
 JOIN manual_authorization_units u ON u.authorization_id=a.id WHERE m.operation_id=NEW.operation_id)
 AND NOT EXISTS(SELECT 1 FROM manual_admission_commands c WHERE c.operation_id=NEW.operation_id AND c.reservation_id=NEW.id
 AND NEW.wallet_id=json_extract(c.contract_json,'$.wallet_id') AND NEW.token_address=json_extract(c.contract_json,'$.output.resolution.token.address')
 AND NEW.policy_version=json_extract(c.contract_json,'$.policy_version') AND NEW.input_amount=json_extract(c.contract_json,'$.input.units')
 AND NEW.gas_budget=json_extract(c.contract_json,'$.gas_budget.units') AND NEW.state='reserved'
 AND NEW.quote_block_number=json_extract(c.contract_json,'$.output.resolution.token.block.number')
 AND NEW.quote_block_hash=json_extract(c.contract_json,'$.output.resolution.token.block.hash') AND NEW.source_hash=json_extract(c.contract_json,'$.result_hash')
 AND NEW.created_at=c.checked_at AND NEW.updated_at=c.checked_at)
BEGIN SELECT RAISE(ABORT,'manual reservation requires atomic command'); END;

DROP TRIGGER manual_source_no_legacy_decision;
CREATE TRIGGER manual_source_no_legacy_decision BEFORE INSERT ON canary_admission_decisions
WHEN NEW.decision!='REJECTED' AND EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
 AND NOT EXISTS(SELECT 1 FROM manual_admission_commands c WHERE c.operation_id=NEW.operation_id
 AND NEW.id='manual-decision:'||c.operation_id AND NEW.request_id='manual-admission:'||c.request_id
 AND NEW.decision='ADMITTED' AND NEW.reason_code='ADMITTED_NOT_SIGNED_NOT_SUBMITTED'
 AND NEW.request_fingerprint=json_extract(c.contract_json,'$.request_fingerprint') AND NEW.reservation_id=c.reservation_id
 AND NEW.policy_version=json_extract(c.contract_json,'$.policy_version') AND NEW.wallet_id=json_extract(c.contract_json,'$.wallet_id')
 AND NEW.token_address=json_extract(c.contract_json,'$.output.resolution.token.address') AND NEW.amount=json_extract(c.contract_json,'$.input.units')
 AND NEW.created_at=c.checked_at AND EXISTS(SELECT 1 FROM manual_reservation_units WHERE reservation_id=c.reservation_id)
 AND EXISTS(SELECT 1 FROM canary_authorization_operation_usage WHERE authorization_id=c.authorization_id AND operation_id=c.operation_id))
BEGIN SELECT RAISE(ABORT,'manual decision requires complete atomic command'); END;
DROP TRIGGER manual_source_no_legacy_attempt;
CREATE TRIGGER manual_source_no_legacy_attempt BEFORE INSERT ON canary_admission_attempts
WHEN NEW.outcome!='REJECTED' AND EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
 AND NOT EXISTS(SELECT 1 FROM manual_admission_commands c WHERE c.operation_id=NEW.operation_id
 AND NEW.id='manual-attempt:'||c.operation_id AND NEW.request_id='manual-admission:'||c.request_id
 AND NEW.decision_id='manual-decision:'||c.operation_id AND NEW.outcome='ADMITTED'
 AND NEW.reason_code='ADMITTED_NOT_SIGNED_NOT_SUBMITTED' AND NEW.created_at=c.checked_at)
BEGIN SELECT RAISE(ABORT,'manual attempt requires atomic command'); END;
CREATE TRIGGER manual_command_decision_immutable BEFORE UPDATE ON canary_admission_decisions
WHEN EXISTS(SELECT 1 FROM manual_admission_commands WHERE operation_id=OLD.operation_id OR operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual admission decision immutable'); END;
CREATE TRIGGER manual_command_decision_no_delete BEFORE DELETE ON canary_admission_decisions
WHEN EXISTS(SELECT 1 FROM manual_admission_commands WHERE operation_id=OLD.operation_id)
BEGIN SELECT RAISE(ABORT,'manual admission decision immutable'); END;
CREATE TRIGGER manual_command_decision_no_replace BEFORE INSERT ON canary_admission_decisions
WHEN EXISTS(SELECT 1 FROM canary_admission_decisions d JOIN manual_admission_commands c ON c.operation_id=d.operation_id
 WHERE d.id=NEW.id OR d.request_id=NEW.request_id)
BEGIN SELECT RAISE(ABORT,'manual admission decision exists'); END;
CREATE TRIGGER manual_command_attempt_immutable BEFORE UPDATE ON canary_admission_attempts
WHEN EXISTS(SELECT 1 FROM manual_admission_commands WHERE operation_id=OLD.operation_id OR operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual admission attempt immutable'); END;
CREATE TRIGGER manual_command_attempt_no_delete BEFORE DELETE ON canary_admission_attempts
WHEN EXISTS(SELECT 1 FROM manual_admission_commands WHERE operation_id=OLD.operation_id)
BEGIN SELECT RAISE(ABORT,'manual admission attempt immutable'); END;
CREATE TRIGGER manual_command_attempt_no_replace BEFORE INSERT ON canary_admission_attempts
WHEN EXISTS(SELECT 1 FROM canary_admission_attempts h JOIN manual_admission_commands c ON c.operation_id=h.operation_id WHERE h.id=NEW.id)
BEGIN SELECT RAISE(ABORT,'manual admission attempt exists'); END;

-- Status is on the existing lifecycle, outside legacy admitted/executing scans.
CREATE TRIGGER manual_command_operation_transition BEFORE UPDATE OF status ON operations
WHEN NEW.status='manual_admission_succeeded' AND NEW.status IS NOT OLD.status
 AND NOT EXISTS(SELECT 1 FROM manual_admission_commands c JOIN canary_admission_attempts h ON h.id='manual-attempt:'||c.operation_id
 WHERE c.operation_id=OLD.id AND OLD.status='manual_validation_succeeded' AND NEW.updated_at=c.checked_at)
BEGIN SELECT RAISE(ABORT,'manual admission transition requires complete command'); END;
CREATE TRIGGER manual_command_step_transition BEFORE UPDATE OF status ON execution_steps
WHEN NEW.status='manual_admitted' AND NEW.status IS NOT OLD.status
 AND NOT EXISTS(SELECT 1 FROM manual_admission_commands c JOIN canary_admission_attempts h ON h.id='manual-attempt:'||c.operation_id
 WHERE c.step_id=OLD.id AND OLD.status='validation_succeeded' AND NEW.updated_at=c.checked_at)
BEGIN SELECT RAISE(ABORT,'manual step transition requires complete command'); END;
CREATE TRIGGER manual_command_operation_no_reset BEFORE UPDATE OF status ON operations
WHEN EXISTS(SELECT 1 FROM manual_admission_commands WHERE operation_id=OLD.id) AND NEW.status IS NOT OLD.status
 AND NOT (OLD.status='manual_validation_succeeded' AND NEW.status='manual_admission_succeeded')
BEGIN SELECT RAISE(ABORT,'manual downstream lifecycle not connected'); END;
CREATE TRIGGER manual_command_step_no_reset BEFORE UPDATE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_admission_commands WHERE step_id=OLD.id)
 AND (NEW.parameters_json IS NOT OLD.parameters_json OR
 (NEW.status IS NOT OLD.status AND NOT (OLD.status='validation_succeeded' AND NEW.status='manual_admitted')))
BEGIN SELECT RAISE(ABORT,'manual downstream lifecycle not connected'); END;
CREATE TRIGGER manual_source_no_copy_execution BEFORE UPDATE OF status ON operations
WHEN NEW.status IN ('admitted','executing') AND EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=OLD.id)
BEGIN SELECT RAISE(ABORT,'manual operation excluded from legacy execution'); END;
CREATE TRIGGER manual_command_operation_no_delete BEFORE DELETE ON operations
WHEN EXISTS(SELECT 1 FROM manual_admission_commands WHERE operation_id=OLD.id)
BEGIN SELECT RAISE(ABORT,'manual admitted operation cannot be deleted'); END;
CREATE TRIGGER manual_command_step_no_delete BEFORE DELETE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_admission_commands WHERE step_id=OLD.id)
BEGIN SELECT RAISE(ABORT,'manual admitted step cannot be deleted'); END;
CREATE TRIGGER manual_command_alert_identity BEFORE UPDATE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_admission_commands c WHERE OLD.id='manual-admission-alert:'||c.operation_id
 OR OLD.id='manual-reservation-unit-alert:'||c.reservation_id)
 AND (NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id OR NEW.severity IS NOT OLD.severity OR NEW.created_at IS NOT OLD.created_at)
BEGIN SELECT RAISE(ABORT,'manual command alert identity immutable'); END;
CREATE TRIGGER manual_command_alert_no_delete BEFORE DELETE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_admission_commands c WHERE OLD.id='manual-admission-alert:'||c.operation_id
 OR OLD.id='manual-reservation-unit-alert:'||c.reservation_id)
BEGIN SELECT RAISE(ABORT,'manual command alert cannot be deleted'); END;
CREATE TRIGGER manual_command_alert_no_replace BEFORE INSERT ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_admission_commands c ON a.id='manual-admission-alert:'||c.operation_id
 OR a.id='manual-reservation-unit-alert:'||c.reservation_id WHERE a.id=NEW.id)
BEGIN SELECT RAISE(ABORT,'manual command alert exists'); END;

CREATE TRIGGER manual_command_effect AFTER INSERT ON manual_admission_commands
BEGIN
 INSERT INTO canary_risk_reservations(id,operation_id,wallet_id,token_address,policy_version,input_amount,gas_budget,state,created_at,updated_at,quote_block_number,quote_block_hash,source_hash)
 VALUES(NEW.reservation_id,NEW.operation_id,json_extract(NEW.contract_json,'$.wallet_id'),json_extract(NEW.contract_json,'$.output.resolution.token.address'),
 json_extract(NEW.contract_json,'$.policy_version'),json_extract(NEW.contract_json,'$.input.units'),json_extract(NEW.contract_json,'$.gas_budget.units'),'reserved',NEW.checked_at,NEW.checked_at,
 json_extract(NEW.contract_json,'$.output.resolution.token.block.number'),json_extract(NEW.contract_json,'$.output.resolution.token.block.hash'),json_extract(NEW.contract_json,'$.result_hash'));
 INSERT INTO manual_reservation_units VALUES(NEW.reservation_id,NEW.operation_id,NEW.manual_policy_id,NEW.manual_policy_hash,4663,
 json_extract(NEW.contract_json,'$.input.asset'),json_extract(NEW.contract_json,'$.input.decimals'),'TRADE_INPUT',
 json_extract(NEW.contract_json,'$.result_hash'),json_extract(NEW.contract_json,'$.request_fingerprint'),json_extract(NEW.contract_json,'$.expires_at'),NEW.checked_at);
 INSERT INTO canary_authorization_operation_usage VALUES(NEW.authorization_id,NEW.operation_id,json_extract(NEW.contract_json,'$.input.units'),'RESERVED',NEW.checked_at,NEW.checked_at);
 UPDATE canary_authorization_usage SET operation_count=operation_count+1,total_input=manual_u256_add(total_input,json_extract(NEW.contract_json,'$.input.units')),updated_at=NEW.checked_at WHERE authorization_id=NEW.authorization_id;
 INSERT INTO canary_admission_decisions VALUES('manual-decision:'||NEW.operation_id,'manual-admission:'||NEW.request_id,NEW.operation_id,
 json_extract(NEW.contract_json,'$.request_fingerprint'),json_extract(NEW.contract_json,'$.policy_version'),'ADMITTED','ADMITTED_NOT_SIGNED_NOT_SUBMITTED',
 json_extract(NEW.contract_json,'$.wallet_id'),json_extract(NEW.contract_json,'$.output.resolution.token.address'),json_extract(NEW.contract_json,'$.input.units'),NEW.reservation_id,NEW.checked_at);
 INSERT INTO canary_admission_attempts VALUES('manual-attempt:'||NEW.operation_id,'manual-admission:'||NEW.request_id,'manual-decision:'||NEW.operation_id,NEW.operation_id,'ADMITTED','ADMITTED_NOT_SIGNED_NOT_SUBMITTED',NEW.checked_at);
 INSERT INTO canary_audit_events VALUES('manual-decision-audit:'||NEW.operation_id,'manual-decision:'||NEW.operation_id,'CANARY_ADMISSION_DECISION',json_extract(NEW.contract_json,'$.policy_version'),'ADMITTED_NOT_SIGNED_NOT_SUBMITTED',NEW.checked_at);
 UPDATE operations SET status='manual_admission_succeeded',updated_at=NEW.checked_at WHERE id=NEW.operation_id;
 UPDATE execution_steps SET status='manual_admitted',updated_at=NEW.checked_at WHERE id=NEW.step_id;
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 VALUES('manual-admission:'||NEW.operation_id,'MANUAL_ATOMIC_ADMISSION',NEW.operation_id,NEW.authorization_id,NEW.authorization_epoch,'ADMITTED_NOT_SIGNED_NOT_SUBMITTED',
 json_object('request_id',NEW.request_id,'reservation_id',NEW.reservation_id,'contract_hash',NEW.contract_hash,'policy_hash',NEW.manual_policy_hash,'gate_evidence_set_hash',NEW.gate_evidence_set_hash),NEW.checked_at);
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-admission-alert:'||NEW.operation_id,'manual-admission:'||NEW.operation_id,'INFO','PENDING',NEW.checked_at);
 -- Even an outer INSERT OR IGNORE must not leave a skipped ledger constituent.
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_admission_complete WHERE operation_id=NEW.operation_id)
 OR NOT EXISTS(SELECT 1 FROM canary_authorization_usage u WHERE u.authorization_id=NEW.authorization_id
 AND u.operation_count=(SELECT COUNT(*) FROM canary_authorization_operation_usage WHERE authorization_id=NEW.authorization_id)
 AND u.total_input=(SELECT manual_u256_sum_json(json_group_array(amount)) FROM canary_authorization_operation_usage WHERE authorization_id=NEW.authorization_id))
 THEN RAISE(ABORT,'manual command incomplete effect') END;
END;
