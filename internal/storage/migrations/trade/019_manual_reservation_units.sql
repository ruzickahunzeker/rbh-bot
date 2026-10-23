-- Accounting foundation ONLY. No admission/authorization/budget/permit writes.
-- Existing 006..018 remain immutable; metadata never creates a reservation.
CREATE TABLE manual_reservation_units(
 reservation_id TEXT NOT NULL PRIMARY KEY REFERENCES canary_risk_reservations(id),
 operation_id TEXT NOT NULL UNIQUE REFERENCES manual_http_order_sources(operation_id),
 manual_policy_id TEXT NOT NULL REFERENCES manual_risk_policies(id),
 manual_policy_hash TEXT NOT NULL CHECK(length(manual_policy_hash)=64 AND manual_policy_hash NOT GLOB '*[^0-9a-f]*'),
 chain_id INTEGER NOT NULL CHECK(chain_id=4663),
 input_asset TEXT NOT NULL,
 input_decimals INTEGER NOT NULL CHECK(input_decimals BETWEEN 0 AND 255),
 accounting_kind TEXT NOT NULL CHECK(accounting_kind='TRADE_INPUT'),
 result_hash TEXT NOT NULL CHECK(length(result_hash)=64 AND result_hash NOT GLOB '*[^0-9a-f]*'),
 request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint)=64 AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
 expires_at TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE TRIGGER manual_reservation_unit_source BEFORE INSERT ON manual_reservation_units
WHEN NOT EXISTS(
 SELECT 1 FROM canary_risk_reservations rr
 JOIN manual_http_order_sources m ON m.operation_id=rr.operation_id
 JOIN manual_trade_validations v ON v.operation_id=m.operation_id
 JOIN manual_trade_resolutions r ON r.operation_id=m.operation_id
 JOIN manual_risk_policies p ON p.id=NEW.manual_policy_id
 JOIN canary_policies cp ON cp.version=p.source_policy_version,
 json_each(p.policy_json,'$.input_limits') l
 WHERE rr.id=NEW.reservation_id AND rr.operation_id=NEW.operation_id
 AND rr.wallet_id=m.wallet_id AND lower(rr.token_address)=json_extract(v.result_json,'$.resolution.token.address')
 AND rr.policy_version=r.policy_version AND p.source_policy_version=r.policy_version
 AND p.source_policy_hash=r.policy_hash AND cp.policy_hash=r.policy_hash
 AND p.policy_hash=NEW.manual_policy_hash AND v.result_hash=NEW.result_hash
 AND m.request_fingerprint=NEW.request_fingerprint AND m.expires_at=NEW.expires_at
 AND r.expires_at=NEW.expires_at AND m.chain='robinhood' AND m.source='MANUAL'
 AND NEW.chain_id=4663 AND rr.created_at=NEW.created_at
 AND rr.input_amount=json_extract(v.result_json,'$.resolution.amount_in')
 AND rr.gas_budget=json_extract(v.result_json,'$.gas_budget')
 AND rr.quote_block_number=json_extract(v.result_json,'$.resolution.token.block.number')
 AND rr.quote_block_hash=json_extract(v.result_json,'$.resolution.token.block.hash')
 AND rr.source_hash=NEW.result_hash
 AND json_extract(l.value,'$.unit.chain_id')=NEW.chain_id
 AND json_extract(l.value,'$.unit.asset')=NEW.input_asset
 AND json_extract(l.value,'$.unit.decimals')=NEW.input_decimals
 AND json_extract(l.value,'$.unit.kind')=NEW.accounting_kind
 AND ((json_extract(v.result_json,'$.resolution.side')='buy' AND NEW.input_asset='native' AND NEW.input_decimals=18)
 OR (json_extract(v.result_json,'$.resolution.side')='sell'
 AND NEW.input_asset=json_extract(v.result_json,'$.resolution.token.address')
 AND NEW.input_decimals=json_extract(v.result_json,'$.resolution.token.decimals')))
)
BEGIN SELECT RAISE(ABORT,'manual reservation unit source mismatch'); END;
CREATE TRIGGER manual_reservation_unit_no_update BEFORE UPDATE ON manual_reservation_units
BEGIN SELECT RAISE(ABORT,'manual reservation unit immutable'); END;
CREATE TRIGGER manual_reservation_unit_no_delete BEFORE DELETE ON manual_reservation_units
BEGIN SELECT RAISE(ABORT,'manual reservation unit immutable'); END;
CREATE TRIGGER manual_reservation_unit_no_replace BEFORE INSERT ON manual_reservation_units
WHEN EXISTS(SELECT 1 FROM manual_reservation_units WHERE reservation_id=NEW.reservation_id OR operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual reservation unit exists'); END;
-- Parent identity must not drift independently from its immutable unit/source.
CREATE TRIGGER manual_reservation_parent_identity BEFORE UPDATE ON canary_risk_reservations
WHEN EXISTS(SELECT 1 FROM manual_reservation_units WHERE reservation_id=OLD.id)
 AND (NEW.id IS NOT OLD.id OR NEW.operation_id IS NOT OLD.operation_id OR NEW.wallet_id IS NOT OLD.wallet_id
 OR NEW.token_address IS NOT OLD.token_address OR NEW.policy_version IS NOT OLD.policy_version
 OR NEW.input_amount IS NOT OLD.input_amount OR NEW.gas_budget IS NOT OLD.gas_budget
 OR NEW.quote_block_number IS NOT OLD.quote_block_number OR NEW.quote_block_hash IS NOT OLD.quote_block_hash
 OR NEW.source_hash IS NOT OLD.source_hash OR NEW.created_at IS NOT OLD.created_at)
BEGIN SELECT RAISE(ABORT,'typed reservation parent identity immutable'); END;
CREATE TRIGGER manual_reservation_parent_no_delete BEFORE DELETE ON canary_risk_reservations
WHEN EXISTS(SELECT 1 FROM manual_reservation_units WHERE reservation_id=OLD.id)
BEGIN SELECT RAISE(ABORT,'typed reservation cannot be deleted'); END;
CREATE TRIGGER manual_reservation_parent_no_replace BEFORE INSERT ON canary_risk_reservations
WHEN EXISTS(SELECT 1 FROM manual_reservation_units WHERE reservation_id=NEW.id OR operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'typed reservation cannot be replaced'); END;
CREATE TRIGGER manual_reservation_unit_audit AFTER INSERT ON manual_reservation_units
BEGIN
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,reason_code,details_json,created_at)
 VALUES('manual-reservation-unit:'||NEW.reservation_id,'MANUAL_RESERVATION_UNIT_BOUND',NEW.operation_id,'METADATA_NOT_ADMISSION',
 json_object('reservation_id',NEW.reservation_id,'policy_id',NEW.manual_policy_id,'result_hash',NEW.result_hash,'input_asset',NEW.input_asset,'input_decimals',NEW.input_decimals),NEW.created_at);
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-reservation-unit-alert:'||NEW.reservation_id,'manual-reservation-unit:'||NEW.reservation_id,'INFO','PENDING',NEW.created_at);
END;
-- Legacy admission must not use a MANUAL operation or raw-sum typed rows.
-- Schema insert guards supplement the API's transaction-local rejection reason.
CREATE TRIGGER manual_source_no_legacy_canary_source BEFORE INSERT ON canary_admission_sources
WHEN EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual operation requires typed atomic admission'); END;
CREATE TRIGGER manual_source_no_legacy_decision BEFORE INSERT ON canary_admission_decisions
WHEN NEW.decision!='REJECTED'
 AND EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual decision atomic path not connected'); END;
CREATE TRIGGER manual_source_no_legacy_decision_update BEFORE UPDATE ON canary_admission_decisions
WHEN NEW.decision!='REJECTED'
 AND EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual decision atomic path not connected'); END;
CREATE TRIGGER manual_source_no_legacy_attempt BEFORE INSERT ON canary_admission_attempts
WHEN NEW.outcome!='REJECTED'
 AND EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual attempt atomic path not connected'); END;
CREATE TRIGGER manual_source_no_legacy_attempt_update BEFORE UPDATE ON canary_admission_attempts
WHEN NEW.outcome!='REJECTED'
 AND EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual attempt atomic path not connected'); END;
CREATE TRIGGER legacy_reservation_no_typed_mix BEFORE INSERT ON canary_risk_reservations
WHEN NOT EXISTS(SELECT 1 FROM manual_http_order_sources WHERE operation_id=NEW.operation_id)
 AND EXISTS(SELECT 1 FROM manual_reservation_units)
BEGIN SELECT RAISE(ABORT,'legacy raw accounting cannot mix typed history'); END;
CREATE TRIGGER legacy_window_no_typed_mix_insert BEFORE INSERT ON canary_window_usage
WHEN EXISTS(SELECT 1 FROM manual_reservation_units)
BEGIN SELECT RAISE(ABORT,'legacy window accounting cannot mix typed history'); END;
CREATE TRIGGER legacy_window_no_typed_mix_update BEFORE UPDATE ON canary_window_usage
WHEN EXISTS(SELECT 1 FROM manual_reservation_units)
BEGIN SELECT RAISE(ABORT,'legacy window accounting cannot mix typed history'); END;
