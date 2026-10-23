-- M4-B2.3: exact private MANUAL FIRST_BROADCAST issuance metadata only.
-- No consume, replay, submission, transport, signer or production caller.
CREATE TABLE manual_first_broadcast_issuances(
 id TEXT NOT NULL PRIMARY KEY,
 operation_id TEXT NOT NULL REFERENCES manual_submission_foundations(operation_id),
 attempt_id TEXT NOT NULL,
 authorization_id TEXT NOT NULL,
 authorization_epoch INTEGER NOT NULL,
 source_json TEXT NOT NULL,
 source_hash TEXT NOT NULL CHECK(length(source_hash)=64),
 process_hash TEXT NOT NULL CHECK(length(process_hash)=64),
 observation_hash TEXT NOT NULL CHECK(length(observation_hash)=64),
 clock_generation INTEGER NOT NULL CHECK(clock_generation>0),
 artifact_hash TEXT NOT NULL CHECK(length(artifact_hash)=64),
 tx_hash TEXT NOT NULL CHECK(length(tx_hash)=66),
 issued_at TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state='ISSUED'),
 UNIQUE(operation_id,attempt_id)
);

CREATE TRIGGER manual_first_broadcast_source BEFORE INSERT ON manual_first_broadcast_issuances
 WHEN typeof(NEW.id)!='text' OR NEW.id!='manual-first-permit:'||NEW.attempt_id
 OR typeof(NEW.source_json)!='text' OR json_valid(NEW.source_json)!=1
 OR NEW.source_hash!=manual_artifact_bytes_sha256(hex(NEW.source_json))
 OR NEW.process_hash GLOB '*[^0-9a-f]*' OR lower(NEW.process_hash)!=NEW.process_hash
 OR NEW.observation_hash GLOB '*[^0-9a-f]*' OR lower(NEW.observation_hash)!=NEW.observation_hash
 OR manual_artifact_time_valid(hex(NEW.issued_at),typeof(NEW.issued_at))!=1
 OR manual_artifact_time_valid(hex(NEW.expires_at),typeof(NEW.expires_at))!=1
 OR manual_time_cmp(NEW.issued_at,NEW.expires_at)>=0
 OR NEW.state!='ISSUED'
 OR NOT EXISTS(SELECT 1 FROM manual_submission_foundation_complete f WHERE f.operation_id=NEW.operation_id)
 OR NOT EXISTS(SELECT 1 FROM manual_submission_foundation_complete f WHERE f.operation_id=NEW.operation_id AND f.binding_hash!='')
 OR NOT EXISTS(SELECT 1 FROM manual_gate_evidence_valid_source e WHERE e.operation_id=NEW.operation_id AND e.source_json=NEW.source_json AND manual_artifact_bytes_sha256(hex(e.source_json))=NEW.source_hash)
 OR json_extract(NEW.source_json,'$.foundation.facts.attempt_id')!=NEW.attempt_id
 OR json_extract(NEW.source_json,'$.foundation.facts.raw_sha256')!=NEW.artifact_hash
 OR json_extract(NEW.source_json,'$.foundation.facts.tx_hash')!=NEW.tx_hash
 OR EXISTS(SELECT 1 FROM manual_first_broadcast_issuances WHERE id=NEW.id OR rowid=NEW.rowid OR (operation_id=NEW.operation_id AND attempt_id=NEW.attempt_id))
BEGIN SELECT RAISE(ABORT,'manual first broadcast exact source required'); END;

CREATE TRIGGER manual_first_broadcast_no_update BEFORE UPDATE ON manual_first_broadcast_issuances
BEGIN SELECT RAISE(ABORT,'manual first broadcast issuance immutable'); END;
CREATE TRIGGER manual_first_broadcast_no_delete BEFORE DELETE ON manual_first_broadcast_issuances
BEGIN SELECT RAISE(ABORT,'manual first broadcast issuance retained'); END;

CREATE TRIGGER manual_first_broadcast_audit AFTER INSERT ON manual_first_broadcast_issuances
BEGIN
 INSERT INTO canary_runtime_audit(id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 VALUES('manual-first-permit-audit:'||NEW.id,'MANUAL_FIRST_BROADCAST_ISSUED',NEW.operation_id,NEW.attempt_id,NEW.authorization_id,NEW.authorization_epoch,'METADATA_ONLY_NOT_CONSUMED',json_object('issuance_id',NEW.id,'source_hash',NEW.source_hash,'process_hash',NEW.process_hash,'observation_hash',NEW.observation_hash,'clock_generation',NEW.clock_generation,'artifact_hash',NEW.artifact_hash,'tx_hash',NEW.tx_hash,'issued_at',NEW.issued_at,'expires_at',NEW.expires_at),NEW.issued_at);
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at)
 VALUES('manual-first-permit-alert:'||NEW.id,'manual-first-permit-audit:'||NEW.id,'INFO','PENDING',NEW.issued_at);
END;

-- Replace only the two MANUAL insert denies with an exact B23 exception.
DROP TRIGGER manual_b1_no_permit;
DROP TRIGGER manual_b1_no_pass_snapshot;
CREATE TRIGGER manual_b1_no_permit BEFORE INSERT ON canary_send_permits
WHEN EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.authorization_id)
 AND NOT EXISTS(SELECT 1 FROM manual_first_broadcast_issuances i
   WHERE i.id='manual-first-permit:'||NEW.attempt_id AND i.operation_id=NEW.operation_id AND i.attempt_id=NEW.attempt_id
   AND i.authorization_id=NEW.authorization_id AND i.authorization_epoch=NEW.authorization_epoch AND i.state='ISSUED'
   AND NEW.purpose='FIRST_BROADCAST' AND NEW.artifact_hash=i.artifact_hash AND NEW.tx_hash=i.tx_hash
   AND NEW.issued_at=i.issued_at AND NEW.expires_at=i.expires_at)
BEGIN SELECT RAISE(ABORT,'manual permit requires complete first issuance'); END;
CREATE TRIGGER manual_b1_no_pass_snapshot BEFORE INSERT ON canary_runtime_gate_snapshots
WHEN NEW.decision='PASS' AND EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.authorization_id)
 AND NOT EXISTS(SELECT 1 FROM manual_first_broadcast_issuances i WHERE i.operation_id=NEW.operation_id AND i.attempt_id=COALESCE(NEW.attempt_id,'')
   AND i.authorization_id=NEW.authorization_id AND i.authorization_epoch=NEW.authorization_epoch AND i.state='ISSUED')
BEGIN SELECT RAISE(ABORT,'manual PASS snapshot requires complete first issuance'); END;

-- B2.3 does not implement consumption. Existing generic one-shot transitions
-- remain available to non-MANUAL callers only.
CREATE TRIGGER manual_b23_no_permit_consume BEFORE UPDATE OF state ON canary_send_permits
WHEN EXISTS(SELECT 1 FROM manual_first_broadcast_issuances WHERE id='manual-first-permit:'||OLD.attempt_id)
BEGIN SELECT RAISE(ABORT,'manual permit consumption disabled in B2.3'); END;
