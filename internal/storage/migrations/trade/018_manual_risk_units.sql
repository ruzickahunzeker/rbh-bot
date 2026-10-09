-- C3-B/B1 metadata only. No admission/reservation/usage/authorization grant.
-- The isolation triggers intentionally stop declared MANUAL authorizations
-- before arming/budget/send until a reviewed atomic admission slice replaces
-- these guards. Undeclared legacy authorizations retain their existing path.
CREATE TABLE manual_risk_policies(
 id TEXT NOT NULL PRIMARY KEY CHECK(length(id) BETWEEN 1 AND 128 AND id NOT GLOB '*[^A-Za-z0-9_.-]*'),
 source_policy_version INTEGER NOT NULL REFERENCES canary_policies(version),
 source_policy_hash TEXT NOT NULL CHECK(length(source_policy_hash)=64 AND source_policy_hash NOT GLOB '*[^0-9a-f]*'),
 policy_json TEXT NOT NULL CHECK(json_valid(policy_json) AND json(policy_json)=policy_json),
 policy_hash TEXT NOT NULL CHECK(length(policy_hash)=64 AND policy_hash NOT GLOB '*[^0-9a-f]*'),
 created_at TEXT NOT NULL CHECK(length(created_at)>0)
);
CREATE TRIGGER manual_risk_policy_source BEFORE INSERT ON manual_risk_policies
WHEN NOT EXISTS(SELECT 1 FROM canary_policies p WHERE p.version=NEW.source_policy_version AND p.policy_hash=NEW.source_policy_hash)
 OR COALESCE(json_extract(NEW.policy_json,'$.id')=NEW.id
 AND json_type(NEW.policy_json,'$.id')='text'
 AND json_extract(NEW.policy_json,'$.source_policy_version')=NEW.source_policy_version
 AND json_extract(NEW.policy_json,'$.source_policy_hash')=NEW.source_policy_hash
 AND json_type(NEW.policy_json,'$.source_policy_hash')='text'
 AND json_extract(NEW.policy_json,'$.chain_id')=4663
 AND json_extract(NEW.policy_json,'$.protocol')='PONS_V2_CURVE'
 AND json_type(NEW.policy_json,'$.protocol')='text'
 AND json_type(NEW.policy_json,'$.source_policy_version')='integer'
 AND typeof(json_extract(NEW.policy_json,'$.source_policy_version'))='integer'
 AND json_type(NEW.policy_json,'$.chain_id')='integer'
 AND json_type(NEW.policy_json,'$.max_sell_bps')='integer'
 AND json_extract(NEW.policy_json,'$.max_sell_bps') BETWEEN 1 AND 10000
 AND json_type(NEW.policy_json,'$.window_seconds')='integer'
 AND json_extract(NEW.policy_json,'$.window_seconds') BETWEEN 1 AND 31536000
 AND json_type(NEW.policy_json,'$.max_wallet_operations_window')='integer'
 AND json_extract(NEW.policy_json,'$.max_wallet_operations_window')>0
 AND json_type(NEW.policy_json,'$.max_global_operations_window')='integer'
 AND typeof(json_extract(NEW.policy_json,'$.max_global_operations_window'))='integer'
 AND json_extract(NEW.policy_json,'$.max_global_operations_window')>=json_extract(NEW.policy_json,'$.max_wallet_operations_window')
 AND json_type(NEW.policy_json,'$.input_limits')='array'
 AND json_array_length(NEW.policy_json,'$.input_limits') BETWEEN 1 AND 128,0)=0
 OR (SELECT COUNT(*) FROM json_each(NEW.policy_json))!=14
 OR (SELECT COUNT(DISTINCT key) FROM json_each(NEW.policy_json))!=14
 OR EXISTS(SELECT 1 FROM json_each(NEW.policy_json) WHERE key NOT IN
 ('id','source_policy_version','source_policy_hash','chain_id','protocol','input_limits',
 'max_native_gas_per_operation','max_native_wallet_outlay','max_native_global_outlay',
 'min_native_balance','max_sell_bps','max_wallet_operations_window','max_global_operations_window','window_seconds'))
BEGIN SELECT RAISE(ABORT,'manual risk policy source/header mismatch'); END;

-- Schema checks textual integer shape/range. Cryptographic hashes and exact
-- canonical policy structure MUST ALSO be verified by the Go policy loader.
CREATE TRIGGER manual_risk_policy_native_caps BEFORE INSERT ON manual_risk_policies
WHEN (SELECT COUNT(*) FROM json_each(NEW.policy_json) WHERE key IN
 ('max_native_gas_per_operation','max_native_wallet_outlay','max_native_global_outlay','min_native_balance'))!=4
 OR EXISTS(SELECT 1 FROM json_each(NEW.policy_json) WHERE key IN
 ('max_native_gas_per_operation','max_native_wallet_outlay','max_native_global_outlay','min_native_balance') AND
 (type!='text' OR length(value) NOT BETWEEN 1 AND 78 OR value GLOB '*[^0-9]*'
 OR (value!='0' AND substr(value,1,1)='0') OR (value='0' AND key!='min_native_balance')
 OR (length(value)=78 AND value>'115792089237316195423570985008687907853269984665640564039457584007913129639935')))
BEGIN SELECT RAISE(ABORT,'manual native cap invalid'); END;

CREATE TRIGGER manual_risk_policy_input_units BEFORE INSERT ON manual_risk_policies
WHEN EXISTS(SELECT 1 FROM json_each(NEW.policy_json,'$.input_limits') AS l WHERE
 COALESCE(l.type='object'
 AND json_type(l.value,'$.unit.chain_id')='integer' AND json_extract(l.value,'$.unit.chain_id')=4663
 AND json_type(l.value,'$.unit.decimals')='integer' AND json_extract(l.value,'$.unit.decimals') BETWEEN 0 AND 255
 AND json_type(l.value,'$.unit.asset')='text' AND json_type(l.value,'$.unit.kind')='text'
 AND json_extract(l.value,'$.unit.kind')='TRADE_INPUT'
 AND ((json_extract(l.value,'$.unit.asset')='native' AND json_extract(l.value,'$.unit.decimals')=18)
 OR (length(json_extract(l.value,'$.unit.asset'))=42
 AND substr(json_extract(l.value,'$.unit.asset'),1,2)='0x'
 AND substr(json_extract(l.value,'$.unit.asset'),3) NOT GLOB '*[^0-9a-f]*'
 AND json_extract(l.value,'$.unit.asset')!='0x0000000000000000000000000000000000000000'))
 AND json_type(l.value,'$.max_operation')='text'
 AND json_type(l.value,'$.max_wallet_active')='text'
 AND json_type(l.value,'$.max_global_active')='text',0)=0)
 OR (SELECT COUNT(DISTINCT json_extract(value,'$.unit.asset')) FROM json_each(NEW.policy_json,'$.input_limits'))!=json_array_length(NEW.policy_json,'$.input_limits')
 OR EXISTS(SELECT 1 FROM json_each(NEW.policy_json,'$.input_limits') l,
 json_each(l.value) c WHERE c.key IN ('max_operation','max_wallet_active','max_global_active') AND
 (c.type!='text' OR length(c.value) NOT BETWEEN 1 AND 78 OR c.value GLOB '*[^0-9]*'
 OR substr(c.value,1,1)='0'
 OR (length(c.value)=78 AND c.value>'115792089237316195423570985008687907853269984665640564039457584007913129639935')))
BEGIN SELECT RAISE(ABORT,'manual input cap/unit invalid'); END;

CREATE TRIGGER manual_risk_policy_structure BEFORE INSERT ON manual_risk_policies
WHEN EXISTS(SELECT 1 FROM json_each(NEW.policy_json,'$.input_limits') l WHERE
 (SELECT COUNT(*) FROM json_each(l.value))!=4
 OR (SELECT COUNT(DISTINCT key) FROM json_each(l.value))!=4
 OR EXISTS(SELECT 1 FROM json_each(l.value) WHERE key NOT IN ('unit','max_operation','max_wallet_active','max_global_active'))
 OR (SELECT COUNT(*) FROM json_each(l.value,'$.unit'))!=4
 OR (SELECT COUNT(DISTINCT key) FROM json_each(l.value,'$.unit'))!=4
 OR EXISTS(SELECT 1 FROM json_each(l.value,'$.unit') WHERE key NOT IN ('chain_id','asset','decimals','kind')))
 OR EXISTS(SELECT 1 FROM json_each(NEW.policy_json,'$.input_limits') a,
 json_each(NEW.policy_json,'$.input_limits') b WHERE b.key=a.key+1
 AND json_extract(a.value,'$.unit.asset')>=json_extract(b.value,'$.unit.asset'))
BEGIN SELECT RAISE(ABORT,'manual risk structure/order invalid'); END;

CREATE TRIGGER manual_risk_policy_hierarchy BEFORE INSERT ON manual_risk_policies
WHEN EXISTS(SELECT 1 FROM (
 SELECT json_extract(NEW.policy_json,'$.max_native_gas_per_operation') lo,json_extract(NEW.policy_json,'$.max_native_wallet_outlay') hi
 UNION ALL SELECT json_extract(NEW.policy_json,'$.max_native_wallet_outlay'),json_extract(NEW.policy_json,'$.max_native_global_outlay')
 UNION ALL SELECT json_extract(l.value,'$.max_operation'),json_extract(l.value,'$.max_wallet_active') FROM json_each(NEW.policy_json,'$.input_limits') l
 UNION ALL SELECT json_extract(l.value,'$.max_wallet_active'),json_extract(l.value,'$.max_global_active') FROM json_each(NEW.policy_json,'$.input_limits') l
 ) WHERE length(lo)>length(hi) OR (length(lo)=length(hi) AND lo>hi))
BEGIN SELECT RAISE(ABORT,'manual risk cap hierarchy invalid'); END;

CREATE TRIGGER manual_risk_policy_no_update BEFORE UPDATE ON manual_risk_policies BEGIN SELECT RAISE(ABORT,'manual risk policy immutable'); END;
CREATE TRIGGER manual_risk_policy_no_delete BEFORE DELETE ON manual_risk_policies BEGIN SELECT RAISE(ABORT,'manual risk policy immutable'); END;
CREATE TRIGGER manual_risk_policy_no_replace BEFORE INSERT ON manual_risk_policies
WHEN EXISTS(SELECT 1 FROM manual_risk_policies WHERE id=NEW.id)
BEGIN SELECT RAISE(ABORT,'manual risk policy exists'); END;
CREATE TRIGGER manual_risk_policy_audit AFTER INSERT ON manual_risk_policies
BEGIN
 INSERT INTO canary_runtime_audit(id,event_type,reason_code,details_json,created_at)
 VALUES('manual-risk-policy:'||NEW.id,'MANUAL_RISK_POLICY_RECORDED','METADATA_ONLY_NOT_ADMISSION',json_object('policy_id',NEW.id,'policy_hash',NEW.policy_hash,'source_policy_version',NEW.source_policy_version),NEW.created_at);
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-risk-policy-alert:'||NEW.id,'manual-risk-policy:'||NEW.id,'INFO','PENDING',NEW.created_at);
END;

CREATE TABLE manual_authorization_units(
 authorization_id TEXT NOT NULL PRIMARY KEY REFERENCES canary_runtime_authorizations(id),
 authorization_epoch INTEGER NOT NULL,
 manual_policy_id TEXT NOT NULL REFERENCES manual_risk_policies(id),
 manual_policy_hash TEXT NOT NULL CHECK(length(manual_policy_hash)=64 AND manual_policy_hash NOT GLOB '*[^0-9a-f]*'),
 source_mode TEXT NOT NULL CHECK(source_mode='MANUAL'),
 chain_id INTEGER NOT NULL CHECK(chain_id=4663),
 input_asset TEXT NOT NULL CHECK(input_asset='native' OR (length(input_asset)=42 AND substr(input_asset,1,2)='0x' AND substr(input_asset,3) NOT GLOB '*[^0-9a-f]*' AND input_asset!='0x0000000000000000000000000000000000000000')),
 input_decimals INTEGER NOT NULL CHECK(input_decimals BETWEEN 0 AND 255 AND (input_asset!='native' OR input_decimals=18)),
 accounting_kind TEXT NOT NULL CHECK(accounting_kind='TRADE_INPUT'),
 created_at TEXT NOT NULL CHECK(length(created_at)>0),
 FOREIGN KEY(authorization_id,authorization_epoch) REFERENCES canary_runtime_authorizations(id,epoch)
);
CREATE TRIGGER manual_authorization_unit_source BEFORE INSERT ON manual_authorization_units
WHEN NOT EXISTS(
 SELECT 1 FROM canary_runtime_authorizations a JOIN canary_authorization_usage u ON u.authorization_id=a.id
 JOIN manual_risk_policies p ON p.id=NEW.manual_policy_id,
 json_each(p.policy_json,'$.input_limits') l
 WHERE a.id=NEW.authorization_id AND a.epoch=NEW.authorization_epoch AND a.state='PENDING'
 AND a.mode='CONTROLLED_CANARY' AND a.chain_id=NEW.chain_id AND a.policy_version=p.source_policy_version
 AND NEW.created_at=a.created_at
 AND u.operation_count=0 AND u.total_input='0' AND p.policy_hash=NEW.manual_policy_hash
 AND json_extract(l.value,'$.unit.asset')=NEW.input_asset
 AND json_extract(l.value,'$.unit.decimals')=NEW.input_decimals
 AND json_extract(l.value,'$.unit.chain_id')=NEW.chain_id
 AND json_extract(l.value,'$.unit.kind')=NEW.accounting_kind
 AND NOT EXISTS(SELECT 1 FROM canary_authorization_operation_usage WHERE authorization_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM canary_send_permits WHERE authorization_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM canary_runtime_gate_snapshots WHERE authorization_id=a.id AND decision='PASS')
)
BEGIN SELECT RAISE(ABORT,'manual authorization unit requires unused pending source'); END;
CREATE TRIGGER manual_authorization_unit_no_update BEFORE UPDATE ON manual_authorization_units BEGIN SELECT RAISE(ABORT,'manual authorization unit immutable'); END;
CREATE TRIGGER manual_authorization_unit_no_delete BEFORE DELETE ON manual_authorization_units BEGIN SELECT RAISE(ABORT,'manual authorization unit immutable'); END;
CREATE TRIGGER manual_authorization_unit_no_replace BEFORE INSERT ON manual_authorization_units
WHEN EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.authorization_id)
BEGIN SELECT RAISE(ABORT,'manual authorization unit exists'); END;
CREATE TRIGGER manual_authorization_unit_audit AFTER INSERT ON manual_authorization_units
BEGIN
 INSERT INTO canary_runtime_audit(id,event_type,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 VALUES('manual-auth-unit:'||NEW.authorization_id,'MANUAL_AUTHORIZATION_UNIT_DECLARED',NEW.authorization_id,NEW.authorization_epoch,'B1_METADATA_ONLY_NOT_ARMED',json_object('manual_policy_id',NEW.manual_policy_id,'input_asset',NEW.input_asset,'input_decimals',NEW.input_decimals),NEW.created_at);
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-auth-unit-alert:'||NEW.authorization_id,'manual-auth-unit:'||NEW.authorization_id,'INFO','PENDING',NEW.created_at);
END;

-- These guards apply ONLY to opt-in declared MANUAL identities. They are not
-- a second authorization state model and never create/update a grant.
CREATE TRIGGER manual_b1_no_arm BEFORE UPDATE OF state ON canary_runtime_authorizations
WHEN NEW.state='ARMED' AND EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.id)
BEGIN SELECT RAISE(ABORT,'manual atomic admission not connected'); END;
CREATE TRIGGER manual_b1_no_auth_replace BEFORE INSERT ON canary_runtime_authorizations
WHEN EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.id OR authorization_epoch=NEW.epoch)
BEGIN SELECT RAISE(ABORT,'declared manual authorization cannot be replaced'); END;
CREATE TRIGGER manual_b1_no_budget BEFORE INSERT ON canary_authorization_operation_usage
WHEN EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.authorization_id)
BEGIN SELECT RAISE(ABORT,'manual atomic budget not connected'); END;
CREATE TRIGGER manual_b1_no_usage_update BEFORE UPDATE ON canary_authorization_usage
WHEN EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.authorization_id OR authorization_id=OLD.authorization_id)
 AND (NEW.authorization_id IS NOT OLD.authorization_id OR NEW.operation_count IS NOT OLD.operation_count OR NEW.total_input IS NOT OLD.total_input)
BEGIN SELECT RAISE(ABORT,'manual atomic usage not connected'); END;
CREATE TRIGGER manual_b1_no_usage_delete BEFORE DELETE ON canary_authorization_usage
WHEN EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=OLD.authorization_id)
BEGIN SELECT RAISE(ABORT,'manual usage cannot be deleted'); END;
CREATE TRIGGER manual_b1_no_usage_replace BEFORE INSERT ON canary_authorization_usage
WHEN EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.authorization_id)
BEGIN SELECT RAISE(ABORT,'manual usage cannot be replaced'); END;
CREATE TRIGGER manual_b1_no_permit BEFORE INSERT ON canary_send_permits
WHEN EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.authorization_id)
BEGIN SELECT RAISE(ABORT,'manual permit not connected'); END;
CREATE TRIGGER manual_b1_no_pass_snapshot BEFORE INSERT ON canary_runtime_gate_snapshots
WHEN NEW.decision='PASS' AND EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.authorization_id)
BEGIN SELECT RAISE(ABORT,'manual economic gate not connected'); END;
