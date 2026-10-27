-- A4-B2 approval-gated arming. The old unconditional MANUAL no-arm trigger is
-- replaced atomically by this approval predicate; all other B1 guards remain.
CREATE TABLE manual_operator_gate_attestations(
  authorization_id TEXT NOT NULL,
  authorization_epoch INTEGER NOT NULL,
  gate TEXT NOT NULL CHECK(gate IN ('C04','C05','C07','C08')),
  evidence_hash TEXT NOT NULL CHECK(length(evidence_hash)=64 AND evidence_hash NOT GLOB '*[^0-9A-Fa-f]*'),
  reviewed_head TEXT NOT NULL,
  attested_at TEXT NOT NULL,
  PRIMARY KEY(authorization_id,authorization_epoch,gate),
  FOREIGN KEY(authorization_id,authorization_epoch) REFERENCES canary_runtime_authorizations(id,epoch)
);
CREATE TRIGGER manual_operator_gate_no_update BEFORE UPDATE ON manual_operator_gate_attestations
BEGIN SELECT RAISE(ABORT,'operator gate attestation immutable'); END;
CREATE TRIGGER manual_operator_gate_no_delete BEFORE DELETE ON manual_operator_gate_attestations
BEGIN SELECT RAISE(ABORT,'operator gate attestation retained'); END;

DROP TRIGGER manual_b1_no_arm;
CREATE TRIGGER manual_b1_no_arm BEFORE UPDATE OF state ON canary_runtime_authorizations
WHEN NEW.state='ARMED' AND EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.id)
 AND NOT EXISTS(
   SELECT 1 FROM manual_operator_authorization_approvals ap
   JOIN manual_operator_authorization_requests rq ON rq.request_id=ap.request_id
   JOIN manual_operator_trust_roots root ON root.operator_key_id=ap.operator_key_id
   WHERE rq.authorization_id=NEW.id AND rq.authorization_epoch=NEW.epoch
     AND root.revoked_at IS NULL
     AND manual_artifact_bytes_sha256(hex(rq.canonical_envelope))=rq.request_hash
     AND manual_operator_signature_valid(root.public_key_hex,rq.canonical_envelope,ap.signature_hex)=1
     AND manual_time_cmp(NEW.updated_at,ap.issued_at)>=0
     AND manual_time_cmp(NEW.updated_at,ap.expires_at)<0
     AND json_extract(rq.canonical_envelope,'$.domain')='RBH-MANUAL-A4-AUTHORIZATION-V1'
     AND json_extract(rq.canonical_envelope,'$.authorization_id')=NEW.id
     AND json_extract(rq.canonical_envelope,'$.authorization_epoch')=NEW.epoch
     AND json_extract(rq.canonical_envelope,'$.build_sha')=NEW.build_sha
     AND json_extract(rq.canonical_envelope,'$.release_id')=NEW.release_id
     AND json_extract(rq.canonical_envelope,'$.deployment_id')=NEW.deployment_id
     AND json_extract(rq.canonical_envelope,'$.environment')=NEW.environment
     AND json_extract(rq.canonical_envelope,'$.wallet_id')=NEW.wallet_id
     AND json_extract(rq.canonical_envelope,'$.policy_version')=NEW.policy_version
     AND json_extract(rq.canonical_envelope,'$.gate_evidence_set_hash')=NEW.gate_evidence_set_hash
     AND json_extract(rq.canonical_envelope,'$.max_operations')=NEW.max_operations
     AND json_extract(rq.canonical_envelope,'$.max_total_input')=NEW.max_total_input
     AND (SELECT COUNT(*) FROM manual_operator_gate_attestations g WHERE g.authorization_id=NEW.id AND g.authorization_epoch=NEW.epoch)=4
 )
BEGIN SELECT RAISE(ABORT,'manual authorization requires verified operator approval'); END;
