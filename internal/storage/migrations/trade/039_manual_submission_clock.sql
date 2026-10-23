-- B2.2 clock metadata only. No permission or economic state. FK_ON required.
CREATE TABLE manual_gate_clock_events(
 id TEXT NOT NULL PRIMARY KEY,
 chain_id INTEGER NOT NULL,
 environment TEXT NOT NULL,
 generation INTEGER NOT NULL,
 high_water_at TEXT NOT NULL,
 fault_latched INTEGER NOT NULL,
 fault_reason TEXT NOT NULL,
 fault_at TEXT,
 updated_at TEXT NOT NULL,
 UNIQUE(chain_id,environment,generation)
);
CREATE TABLE manual_gate_clock_empty(id INTEGER PRIMARY KEY);
CREATE TRIGGER manual_gate_clock_empty_insert BEFORE INSERT ON manual_gate_clock_empty
BEGIN SELECT RAISE(ABORT,'clock empty target retained empty'); END;
CREATE TRIGGER manual_gate_clock_empty_update BEFORE UPDATE ON manual_gate_clock_empty
BEGIN SELECT RAISE(ABORT,'clock empty target immutable'); END;
CREATE TRIGGER manual_gate_clock_empty_delete BEFORE DELETE ON manual_gate_clock_empty
BEGIN SELECT RAISE(ABORT,'clock empty target immutable'); END;
CREATE TABLE manual_gate_clock_scopes(
 id TEXT NOT NULL PRIMARY KEY REFERENCES manual_gate_clock_events(id) DEFERRABLE INITIALLY DEFERRED,
 details_json TEXT NOT NULL,
 created_at TEXT NOT NULL,
 severity TEXT NOT NULL,
 empty_ref INTEGER NOT NULL DEFAULT 1 REFERENCES manual_gate_clock_empty(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE VIEW manual_gate_clock_preclose AS
SELECT e.* FROM manual_gate_clock_events e
JOIN canary_runtime_audit a ON a.id IS 'manual-clock-audit:'||e.id AND typeof(a.id)='text'
 AND a.event_type IS 'MANUAL_SUBMISSION_CLOCK' AND a.operation_id IS NULL AND a.attempt_id IS NULL
 AND a.authorization_id IS NULL AND a.authorization_epoch IS NULL
 AND a.reason_code IS 'CLOCK_STATE_METADATA_NOT_PERMISSION' AND a.created_at IS e.updated_at
 AND typeof(a.details_json)='text' AND hex(a.details_json)=hex(json_object('chain_id',e.chain_id,'environment',e.environment,'generation',e.generation,'high_water_at',e.high_water_at,'fault_latched',e.fault_latched,'fault_reason',e.fault_reason,'fault_at',e.fault_at,'updated_at',e.updated_at))
JOIN canary_alert_outbox b ON b.id IS 'manual-clock-alert:'||e.id AND typeof(b.id)='text'
 AND b.audit_id IS a.id AND b.created_at IS e.updated_at
 AND b.severity IS CASE WHEN e.fault_latched=1 THEN 'CRITICAL' ELSE 'INFO' END
WHERE typeof(e.id)='text' AND typeof(e.environment)='text' AND typeof(e.generation)='integer'
 AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit x WHERE x.rowid!=a.rowid AND hex(x.id)=hex(a.id))
 AND NOT EXISTS(SELECT 1 FROM canary_alert_outbox x WHERE x.rowid!=b.rowid AND (hex(x.id)=hex(b.id) OR hex(x.audit_id)=hex(a.id)));
CREATE VIEW manual_gate_clock_complete AS
SELECT e.* FROM manual_gate_clock_preclose e
WHERE NOT EXISTS(SELECT 1 FROM manual_gate_clock_scopes s WHERE hex(s.id)=hex(e.id));
-- Never fall back to an earlier healthy generation if latest history is corrupt.
CREATE VIEW manual_gate_clock_state AS
SELECT e.* FROM manual_gate_clock_complete e
WHERE e.generation=(SELECT MAX(x.generation) FROM manual_gate_clock_events x WHERE hex(x.environment)=hex(e.environment) AND x.chain_id=e.chain_id);

CREATE TRIGGER manual_gate_clock_no_update BEFORE UPDATE ON manual_gate_clock_events
BEGIN SELECT RAISE(ABORT,'clock events immutable'); END;
CREATE TRIGGER manual_gate_clock_no_delete BEFORE DELETE ON manual_gate_clock_events
BEGIN SELECT RAISE(ABORT,'clock events retained'); END;
CREATE TRIGGER manual_gate_clock_scope_no_update BEFORE UPDATE ON manual_gate_clock_scopes
BEGIN SELECT RAISE(ABORT,'clock scope immutable'); END;
CREATE TRIGGER manual_gate_clock_scope_insert BEFORE INSERT ON manual_gate_clock_scopes
WHEN typeof(NEW.id)!='text' OR typeof(NEW.details_json)!='text' OR NOT json_valid(NEW.details_json)
 OR manual_artifact_time_valid(hex(NEW.created_at),typeof(NEW.created_at))!=1 OR NEW.severity NOT IN ('INFO','CRITICAL')
 OR NEW.empty_ref IS NOT 1
 OR EXISTS(SELECT 1 FROM manual_gate_clock_scopes WHERE hex(id)=hex(NEW.id) OR rowid=NEW.rowid)
 OR EXISTS(SELECT 1 FROM manual_gate_clock_events WHERE hex(id)=hex(NEW.id))
BEGIN SELECT RAISE(ABORT,'clock scope cannot reopen or adopt'); END;
CREATE TRIGGER manual_gate_clock_source BEFORE INSERT ON manual_gate_clock_events
BEGIN
 SELECT CASE WHEN typeof(NEW.id)!='text' OR typeof(NEW.chain_id)!='integer' OR NEW.chain_id!=4663
 OR typeof(NEW.environment)!='text' OR length(CAST(NEW.environment AS BLOB)) NOT BETWEEN 1 AND 128
 OR length(NEW.environment)!=length(CAST(NEW.environment AS BLOB)) OR NEW.environment GLOB '*[^A-Za-z0-9._:-]*'
 OR typeof(NEW.generation)!='integer' OR NEW.generation<1
 OR typeof(NEW.fault_latched)!='integer' OR NEW.fault_latched NOT IN (0,1) OR typeof(NEW.fault_reason)!='text'
 OR manual_artifact_time_valid(hex(NEW.high_water_at),typeof(NEW.high_water_at))!=1
 OR manual_artifact_time_valid(hex(NEW.updated_at),typeof(NEW.updated_at))!=1
 OR (NEW.fault_latched=0 AND (NEW.fault_reason!='' OR NEW.fault_at IS NOT NULL OR NEW.updated_at IS NOT NEW.high_water_at))
 OR (NEW.fault_latched=1 AND (NEW.fault_reason NOT IN ('CLOCK_REVERSAL','CLOCK_UNVERIFIABLE') OR manual_artifact_time_valid(hex(NEW.fault_at),typeof(NEW.fault_at))!=1 OR NEW.updated_at IS NOT NEW.fault_at))
 OR hex(NEW.id)!=hex('manual-clock:'||manual_artifact_bytes_sha256(hex(json_object('chain_id',NEW.chain_id,'environment',NEW.environment,'generation',NEW.generation))))
 OR EXISTS(SELECT 1 FROM manual_gate_clock_events WHERE hex(id)=hex(NEW.id) OR rowid=NEW.rowid)
 OR EXISTS(SELECT 1 FROM manual_gate_clock_scopes WHERE hex(id)=hex(NEW.id))
 OR EXISTS(SELECT 1 FROM canary_runtime_audit WHERE hex(id)=hex('manual-clock-audit:'||NEW.id))
 OR EXISTS(SELECT 1 FROM canary_alert_outbox WHERE hex(id)=hex('manual-clock-alert:'||NEW.id) OR hex(audit_id)=hex('manual-clock-audit:'||NEW.id))
 THEN RAISE(ABORT,'clock exact canonical metadata required') END;
 SELECT CASE WHEN NOT (
  (NEW.generation=1 AND NEW.fault_latched=0 AND NOT EXISTS(SELECT 1 FROM manual_gate_clock_events WHERE hex(environment)=hex(NEW.environment) AND chain_id=NEW.chain_id))
  OR EXISTS(SELECT 1 FROM manual_gate_clock_state old WHERE old.environment IS NEW.environment AND old.chain_id IS NEW.chain_id
    AND old.fault_latched=0 AND old.generation<9223372036854775807 AND NEW.generation=old.generation+1
    AND ((NEW.fault_latched=0 AND manual_time_cmp(NEW.high_water_at,old.high_water_at)>=0)
      OR (NEW.fault_latched=1 AND NEW.high_water_at IS old.high_water_at)))
 ) THEN RAISE(ABORT,'clock invalid transition or latched fault') END;
 INSERT INTO manual_gate_clock_scopes(id,details_json,created_at,severity)
 VALUES(NEW.id,json_object('chain_id',NEW.chain_id,'environment',NEW.environment,'generation',NEW.generation,'high_water_at',NEW.high_water_at,'fault_latched',NEW.fault_latched,'fault_reason',NEW.fault_reason,'fault_at',NEW.fault_at,'updated_at',NEW.updated_at),NEW.updated_at,CASE WHEN NEW.fault_latched=1 THEN 'CRITICAL' ELSE 'INFO' END);
 -- Witness faults precede the actual parent: no later-statement adoption.
 INSERT INTO canary_runtime_audit(id,event_type,reason_code,details_json,created_at)
 VALUES('manual-clock-audit:'||NEW.id,'MANUAL_SUBMISSION_CLOCK','CLOCK_STATE_METADATA_NOT_PERMISSION',json_object('chain_id',NEW.chain_id,'environment',NEW.environment,'generation',NEW.generation,'high_water_at',NEW.high_water_at,'fault_latched',NEW.fault_latched,'fault_reason',NEW.fault_reason,'fault_at',NEW.fault_at,'updated_at',NEW.updated_at),NEW.updated_at);
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-clock-alert:'||NEW.id,'manual-clock-audit:'||NEW.id,CASE WHEN NEW.fault_latched=1 THEN 'CRITICAL' ELSE 'INFO' END,'PENDING',NEW.updated_at);
END;
CREATE TRIGGER manual_gate_clock_scope_close BEFORE DELETE ON manual_gate_clock_scopes
WHEN NOT EXISTS(SELECT 1 FROM manual_gate_clock_preclose e WHERE e.id IS OLD.id AND hex(json_object('chain_id',e.chain_id,'environment',e.environment,'generation',e.generation,'high_water_at',e.high_water_at,'fault_latched',e.fault_latched,'fault_reason',e.fault_reason,'fault_at',e.fault_at,'updated_at',e.updated_at))=hex(OLD.details_json) AND e.updated_at IS OLD.created_at)
BEGIN SELECT RAISE(ABORT,'clock scope incomplete cannot close'); END;
CREATE TRIGGER manual_gate_clock_effect AFTER INSERT ON manual_gate_clock_events
BEGIN
 DELETE FROM manual_gate_clock_scopes WHERE id=NEW.id;
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM manual_gate_clock_complete WHERE id IS NEW.id)
 THEN RAISE(ABORT,'clock atomic metadata incomplete') END;
END;
CREATE TRIGGER manual_gate_clock_audit_insert BEFORE INSERT ON canary_runtime_audit
WHEN (hex(NEW.event_type)=hex('MANUAL_SUBMISSION_CLOCK') OR substr(hex(NEW.id),1,length(hex('manual-clock-audit:')))=hex('manual-clock-audit:')
 OR EXISTS(SELECT 1 FROM canary_runtime_audit a JOIN manual_gate_clock_events e ON hex(a.id)=hex('manual-clock-audit:'||e.id) WHERE a.rowid=NEW.rowid OR hex(a.id)=hex(NEW.id)))
 AND NOT EXISTS(SELECT 1 FROM manual_gate_clock_scopes s
 WHERE NEW.id IS 'manual-clock-audit:'||s.id AND NEW.event_type IS 'MANUAL_SUBMISSION_CLOCK'
 AND NEW.operation_id IS NULL AND NEW.attempt_id IS NULL AND NEW.authorization_id IS NULL AND NEW.authorization_epoch IS NULL
 AND NEW.reason_code IS 'CLOCK_STATE_METADATA_NOT_PERMISSION' AND NEW.created_at IS s.created_at
 AND typeof(NEW.details_json)='text' AND hex(NEW.details_json)=hex(s.details_json)
 AND NOT EXISTS(SELECT 1 FROM manual_gate_clock_events WHERE hex(id)=hex(s.id))
 AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit WHERE hex(id)=hex(NEW.id) OR rowid=NEW.rowid))
BEGIN SELECT RAISE(ABORT,'clock audit initial staging only'); END;
CREATE TRIGGER manual_gate_clock_alert_insert BEFORE INSERT ON canary_alert_outbox
WHEN (substr(hex(NEW.id),1,length(hex('manual-clock-alert:')))=hex('manual-clock-alert:') OR substr(hex(NEW.audit_id),1,length(hex('manual-clock-audit:')))=hex('manual-clock-audit:')
 OR EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_gate_clock_events e ON hex(a.id)=hex('manual-clock-alert:'||e.id) OR hex(a.audit_id)=hex('manual-clock-audit:'||e.id) WHERE a.rowid=NEW.rowid OR hex(a.id)=hex(NEW.id) OR hex(a.audit_id)=hex(NEW.audit_id)))
 AND NOT EXISTS(SELECT 1 FROM manual_gate_clock_scopes s
 WHERE NEW.id IS 'manual-clock-alert:'||s.id AND NEW.audit_id IS 'manual-clock-audit:'||s.id
 AND NEW.severity IS s.severity AND NEW.created_at IS s.created_at AND NEW.state IS 'PENDING' AND NEW.delivered_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM manual_gate_clock_events WHERE hex(id)=hex(s.id))
 AND NOT EXISTS(SELECT 1 FROM canary_alert_outbox WHERE hex(id)=hex(NEW.id) OR hex(audit_id)=hex(NEW.audit_id) OR rowid=NEW.rowid))
BEGIN SELECT RAISE(ABORT,'clock alert initial staging only'); END;
CREATE TRIGGER manual_gate_clock_alert_update BEFORE UPDATE ON canary_alert_outbox
WHEN ((substr(hex(NEW.id),1,length(hex('manual-clock-alert:')))=hex('manual-clock-alert:') OR substr(hex(NEW.audit_id),1,length(hex('manual-clock-audit:')))=hex('manual-clock-audit:'))
 AND (NEW.rowid IS NOT OLD.rowid OR NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id))
 OR (EXISTS(SELECT 1 FROM manual_gate_clock_events e WHERE hex(OLD.id)=hex('manual-clock-alert:'||e.id) OR hex(OLD.audit_id)=hex('manual-clock-audit:'||e.id))
 AND (NEW.rowid IS NOT OLD.rowid OR NEW.id IS NOT OLD.id OR NEW.audit_id IS NOT OLD.audit_id OR NEW.severity IS NOT OLD.severity OR NEW.created_at IS NOT OLD.created_at))
 OR EXISTS(SELECT 1 FROM canary_alert_outbox a JOIN manual_gate_clock_events e ON hex(a.id)=hex('manual-clock-alert:'||e.id) OR hex(a.audit_id)=hex('manual-clock-audit:'||e.id)
 WHERE a.rowid!=OLD.rowid AND (a.rowid=NEW.rowid OR hex(a.id)=hex(NEW.id) OR hex(a.audit_id)=hex(NEW.audit_id)))
BEGIN SELECT RAISE(ABORT,'clock alert identity retained'); END;
CREATE TRIGGER manual_gate_clock_alert_delete BEFORE DELETE ON canary_alert_outbox
WHEN EXISTS(SELECT 1 FROM manual_gate_clock_events e WHERE hex(OLD.id)=hex('manual-clock-alert:'||e.id) OR hex(OLD.audit_id)=hex('manual-clock-audit:'||e.id))
BEGIN SELECT RAISE(ABORT,'clock alert retained'); END;
