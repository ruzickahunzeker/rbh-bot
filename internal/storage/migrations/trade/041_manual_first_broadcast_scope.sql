-- M4-B2.3 hardening: issuance is not independently committable or adoptable.
-- Migration040 remains unchanged; these additive guards close its residual SQL gaps.
CREATE TABLE manual_first_broadcast_scopes(
 issuance_id TEXT NOT NULL PRIMARY KEY REFERENCES manual_first_broadcast_issuances(id) DEFERRABLE INITIALLY DEFERRED,
 attempt_id TEXT NOT NULL,
 source_hash TEXT NOT NULL,
 permit_id TEXT NOT NULL UNIQUE REFERENCES canary_send_permits(id) DEFERRABLE INITIALLY DEFERRED,
 snapshot_id TEXT NOT NULL UNIQUE REFERENCES canary_runtime_gate_snapshots(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER manual_first_broadcast_scope_no_update BEFORE UPDATE ON manual_first_broadcast_scopes
BEGIN SELECT RAISE(ABORT,'first broadcast scope immutable'); END;
CREATE TRIGGER manual_first_broadcast_scope_insert BEFORE INSERT ON manual_first_broadcast_scopes
WHEN NEW.permit_id!=('manual-first-permit:'||NEW.attempt_id)
 OR NEW.snapshot_id!=('manual-first-gate:'||NEW.attempt_id)
 OR typeof(NEW.source_hash)!='text' OR length(NEW.source_hash)!=64
BEGIN SELECT RAISE(ABORT,'first broadcast scope identity'); END;

CREATE TRIGGER manual_first_broadcast_source_binding BEFORE INSERT ON manual_first_broadcast_issuances
WHEN json_extract(NEW.source_json,'$.foundation.facts.authorization_id') IS NOT NEW.authorization_id
 OR CAST(json_extract(NEW.source_json,'$.foundation.facts.authorization_epoch') AS INTEGER) IS NOT NEW.authorization_epoch
 OR json_extract(NEW.source_json,'$.foundation.facts.attempt_id') IS NOT NEW.attempt_id
 OR json_extract(NEW.source_json,'$.foundation.raw_sha256') IS NOT NEW.artifact_hash
 OR json_extract(NEW.source_json,'$.foundation.tx_hash') IS NOT NEW.tx_hash
 OR NEW.clock_generation<1
 OR NOT EXISTS(SELECT 1 FROM manual_first_broadcast_scopes s WHERE s.issuance_id=NEW.id AND s.attempt_id=NEW.attempt_id AND s.source_hash=NEW.source_hash AND s.permit_id=('manual-first-permit:'||NEW.attempt_id) AND s.snapshot_id=('manual-first-gate:'||NEW.attempt_id))
 OR NOT EXISTS(SELECT 1 FROM manual_gate_clock_complete c WHERE c.generation=NEW.clock_generation AND c.environment=json_extract(NEW.source_json,'$.authorization.environment'))
BEGIN SELECT RAISE(ABORT,'first broadcast source identity mismatch'); END;

CREATE TRIGGER manual_first_broadcast_permit_exact BEFORE INSERT ON canary_send_permits
WHEN EXISTS(SELECT 1 FROM manual_first_broadcast_scopes s JOIN manual_first_broadcast_issuances i ON i.id=s.issuance_id WHERE i.attempt_id=NEW.attempt_id)
 AND (NEW.id!=('manual-first-permit:'||NEW.attempt_id) OR NEW.purpose!='FIRST_BROADCAST' OR NEW.state!='ISSUED'
 OR NEW.query_evidence_hash IS NOT NULL OR NEW.queried_at IS NOT NULL
 OR NOT EXISTS(SELECT 1 FROM manual_first_broadcast_scopes s JOIN manual_first_broadcast_issuances i ON i.id=s.issuance_id
   WHERE i.id='manual-first-permit:'||NEW.attempt_id AND i.operation_id=NEW.operation_id AND i.attempt_id=NEW.attempt_id
   AND i.authorization_id=NEW.authorization_id AND i.authorization_epoch=NEW.authorization_epoch
   AND i.artifact_hash=NEW.artifact_hash AND i.tx_hash=NEW.tx_hash AND i.issued_at=NEW.issued_at AND i.expires_at=NEW.expires_at))
BEGIN SELECT RAISE(ABORT,'first broadcast permit exact identity required'); END;
CREATE TRIGGER manual_first_broadcast_permit_scope_required BEFORE INSERT ON canary_send_permits
WHEN NEW.purpose='FIRST_BROADCAST' AND NEW.id LIKE 'manual-first-permit:%'
 AND NOT EXISTS(SELECT 1 FROM manual_first_broadcast_scopes s JOIN manual_first_broadcast_issuances i ON i.id=s.issuance_id
   WHERE i.id='manual-first-permit:'||NEW.attempt_id AND i.operation_id=NEW.operation_id AND i.attempt_id=NEW.attempt_id
   AND i.authorization_id=NEW.authorization_id AND i.authorization_epoch=NEW.authorization_epoch
   AND i.artifact_hash=NEW.artifact_hash AND i.tx_hash=NEW.tx_hash AND i.issued_at=NEW.issued_at AND i.expires_at=NEW.expires_at)
BEGIN SELECT RAISE(ABORT,'first broadcast permit requires exact issuance scope'); END;
CREATE TRIGGER manual_first_broadcast_permit_no_orphan BEFORE INSERT ON canary_send_permits
WHEN NEW.id LIKE 'manual-first-permit:%' AND NOT EXISTS(SELECT 1 FROM manual_first_broadcast_issuances WHERE id=NEW.id)
BEGIN SELECT RAISE(ABORT,'first broadcast permit cannot be adopted'); END;

CREATE TRIGGER manual_first_broadcast_snapshot_exact BEFORE INSERT ON canary_runtime_gate_snapshots
WHEN EXISTS(SELECT 1 FROM manual_first_broadcast_scopes s JOIN manual_first_broadcast_issuances i ON i.id=s.issuance_id WHERE i.attempt_id=COALESCE(NEW.attempt_id,''))
 AND (NEW.id!=('manual-first-gate:'||NEW.attempt_id) OR NEW.stage!='FIRST_BROADCAST' OR NEW.decision!='PASS'
 OR NEW.reason_code!='CANARY_GATE_PASS'
 OR NOT EXISTS(SELECT 1 FROM manual_first_broadcast_scopes s JOIN manual_first_broadcast_issuances i ON i.id=s.issuance_id
   WHERE i.id='manual-first-permit:'||NEW.attempt_id AND i.operation_id=NEW.operation_id AND i.attempt_id=NEW.attempt_id
   AND i.authorization_id=NEW.authorization_id AND i.authorization_epoch=NEW.authorization_epoch
   AND NEW.build_sha=json_extract(i.source_json,'$.foundation.facts.build_sha')
   AND NEW.release_id=json_extract(i.source_json,'$.foundation.facts.release_id')
   AND NEW.deployment_id=json_extract(i.source_json,'$.foundation.facts.deployment_id')
   AND NEW.policy_version=CAST(json_extract(i.source_json,'$.foundation.facts.policy_version') AS INTEGER)
   AND NEW.policy_hash=json_extract(i.source_json,'$.foundation.facts.policy_hash')
   AND NEW.gate_evidence_set_hash=json_extract(i.source_json,'$.foundation.facts.gate_evidence_set_hash')
   AND NEW.checked_at=i.issued_at))
BEGIN SELECT RAISE(ABORT,'first broadcast snapshot exact identity required'); END;
CREATE TRIGGER manual_first_broadcast_snapshot_scope_required BEFORE INSERT ON canary_runtime_gate_snapshots
WHEN NEW.stage='FIRST_BROADCAST' AND NEW.decision='PASS' AND NEW.id LIKE 'manual-first-gate:%'
 AND NOT EXISTS(SELECT 1 FROM manual_first_broadcast_scopes s JOIN manual_first_broadcast_issuances i ON i.id=s.issuance_id
   WHERE i.id='manual-first-permit:'||COALESCE(NEW.attempt_id,'') AND i.operation_id=NEW.operation_id AND i.attempt_id=COALESCE(NEW.attempt_id,'')
   AND i.authorization_id=NEW.authorization_id AND i.authorization_epoch=NEW.authorization_epoch
   AND NEW.id='manual-first-gate:'||NEW.attempt_id)
BEGIN SELECT RAISE(ABORT,'first broadcast snapshot requires exact issuance scope'); END;
CREATE TRIGGER manual_first_broadcast_snapshot_no_orphan BEFORE INSERT ON canary_runtime_gate_snapshots
WHEN NEW.id LIKE 'manual-first-gate:%' AND NOT EXISTS(SELECT 1 FROM manual_first_broadcast_issuances WHERE id='manual-first-permit:'||NEW.attempt_id)
BEGIN SELECT RAISE(ABORT,'first broadcast snapshot cannot be adopted'); END;

CREATE TRIGGER manual_first_broadcast_scope_close_after_permit AFTER INSERT ON canary_send_permits
WHEN NEW.id='manual-first-permit:'||NEW.attempt_id
BEGIN
 DELETE FROM manual_first_broadcast_scopes WHERE issuance_id=NEW.id
  AND EXISTS(SELECT 1 FROM canary_runtime_gate_snapshots g WHERE g.id='manual-first-gate:'||NEW.attempt_id AND g.decision='PASS');
END;
CREATE TRIGGER manual_first_broadcast_scope_close_after_snapshot AFTER INSERT ON canary_runtime_gate_snapshots
WHEN NEW.id='manual-first-gate:'||NEW.attempt_id AND NEW.decision='PASS'
BEGIN
 DELETE FROM manual_first_broadcast_scopes WHERE issuance_id='manual-first-permit:'||NEW.attempt_id
  AND EXISTS(SELECT 1 FROM canary_send_permits p WHERE p.id='manual-first-permit:'||NEW.attempt_id AND p.state='ISSUED');
END;

CREATE TRIGGER manual_first_broadcast_scope_close BEFORE DELETE ON manual_first_broadcast_scopes
WHEN NOT EXISTS(SELECT 1 FROM manual_first_broadcast_issuances i JOIN canary_send_permits p ON p.id=i.id
 JOIN canary_runtime_gate_snapshots g ON g.id='manual-first-gate:'||i.attempt_id
 WHERE i.id=OLD.issuance_id AND p.id=i.id AND p.state='ISSUED' AND g.decision='PASS')
BEGIN SELECT RAISE(ABORT,'first broadcast scope incomplete'); END;

CREATE TRIGGER manual_b23_no_permit_consume_hardened BEFORE UPDATE OF state ON canary_send_permits
WHEN EXISTS(SELECT 1 FROM manual_first_broadcast_issuances WHERE id=OLD.id)
BEGIN SELECT RAISE(ABORT,'manual first permit consumption disabled'); END;
