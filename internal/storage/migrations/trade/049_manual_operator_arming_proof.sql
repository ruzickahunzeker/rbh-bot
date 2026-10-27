-- A4-B2 proof hardening.  Bootstrap and gate witnesses require a deployment
-- secret that is available to the trusted composition only; raw SQL cannot
-- self-create a valid witness without that secret.
ALTER TABLE manual_operator_trust_root_bootstrap ADD COLUMN attestation_mac TEXT NOT NULL DEFAULT '';
ALTER TABLE manual_operator_c07_attestations ADD COLUMN attestation_mac TEXT NOT NULL DEFAULT '';

CREATE TRIGGER manual_operator_bootstrap_proof
BEFORE INSERT ON manual_operator_trust_root_bootstrap
WHEN manual_operator_bootstrap_valid(NEW.operator_key_id,NEW.public_key_hex,NEW.root_fingerprint,NEW.bootstrap_evidence_hash,NEW.deployment_id,NEW.attestation_mac)!=1
BEGIN SELECT RAISE(ABORT,'operator trust bootstrap proof invalid'); END;

CREATE TRIGGER manual_operator_c07_proof
BEFORE INSERT ON manual_operator_c07_attestations
WHEN manual_operator_bootstrap_valid(NEW.authorization_id,NEW.authorization_epoch,NEW.evidence_hash,NEW.reviewed_head,NEW.attested_at,NEW.attestation_mac)!=1
BEGIN SELECT RAISE(ABORT,'operator C07 attestation proof invalid'); END;

CREATE TRIGGER manual_operator_request_nonce_unique
BEFORE INSERT ON manual_operator_authorization_requests
WHEN EXISTS(
  SELECT 1 FROM manual_operator_authorization_requests prior
  WHERE json_extract(prior.canonical_envelope,'$.nonce')=json_extract(NEW.canonical_envelope,'$.nonce')
)
BEGIN SELECT RAISE(ABORT,'operator approval nonce already used'); END;

DROP TRIGGER manual_b1_no_arm;
CREATE TRIGGER manual_b1_no_arm BEFORE UPDATE OF state ON canary_runtime_authorizations
WHEN NEW.state='ARMED' AND EXISTS(SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.id)
 AND NOT EXISTS(
   SELECT 1
   FROM manual_operator_authorization_approvals ap
   JOIN manual_operator_authorization_requests rq ON rq.request_id=ap.request_id
   JOIN manual_operator_trust_roots root ON root.operator_key_id=ap.operator_key_id
   JOIN manual_operator_authorization_sources src ON src.authorization_id=NEW.id AND src.authorization_epoch=NEW.epoch
   JOIN canary_admission_sources source ON source.operation_id=src.operation_id AND source.source_hash=src.source_hash
   JOIN manual_operator_c07_attestations c07 ON c07.authorization_id=NEW.id AND c07.authorization_epoch=NEW.epoch
   JOIN manual_gate_clock_state clock ON clock.environment=NEW.environment AND clock.chain_id=4663
   WHERE rq.authorization_id=NEW.id AND rq.authorization_epoch=NEW.epoch
     AND root.revoked_at IS NULL AND clock.fault_latched=0
     AND manual_artifact_bytes_sha256(hex(rq.canonical_envelope))=rq.request_hash
     AND manual_operator_signature_valid(root.public_key_hex,rq.canonical_envelope,ap.signature_hex)=1
     AND json_extract(rq.canonical_envelope,'$.chain_id')=4663
     AND manual_time_cmp(clock.high_water_at,ap.issued_at)>=0
     AND manual_time_cmp(clock.high_water_at,ap.expires_at)<0
     AND json_extract(rq.canonical_envelope,'$.domain')='RBH-MANUAL-A4-AUTHORIZATION-V1'
     AND json_extract(rq.canonical_envelope,'$.authorization_id')=NEW.id
     AND json_extract(rq.canonical_envelope,'$.authorization_epoch')=NEW.epoch
     AND json_extract(rq.canonical_envelope,'$.build_sha')=NEW.build_sha
     AND json_extract(rq.canonical_envelope,'$.release_id')=NEW.release_id
     AND json_extract(rq.canonical_envelope,'$.deployment_id')=NEW.deployment_id
     AND json_extract(rq.canonical_envelope,'$.environment')=NEW.environment
     AND json_extract(rq.canonical_envelope,'$.wallet_id')=NEW.wallet_id
     AND EXISTS(SELECT 1 FROM dry_run_wallets w WHERE w.id=NEW.wallet_id AND w.chain_id=4663 AND w.enabled=1 AND lower(w.address)=lower(json_extract(rq.canonical_envelope,'$.wallet_address')))
     AND json_extract(rq.canonical_envelope,'$.policy_version')=NEW.policy_version
     AND json_extract(rq.canonical_envelope,'$.policy_hash')=(SELECT p.policy_hash FROM manual_risk_policies p JOIN manual_authorization_units u ON u.manual_policy_id=p.id WHERE u.authorization_id=NEW.id)
     AND json_extract(rq.canonical_envelope,'$.source_hash')=src.source_hash AND json_extract(rq.canonical_envelope,'$.source_hash')=source.source_hash
     AND source.wallet_id=NEW.wallet_id AND source.policy_version=NEW.policy_version
     AND NOT EXISTS(SELECT 1 FROM manual_operator_revocations r WHERE r.authorization_id=NEW.id AND r.authorization_epoch=NEW.epoch)
     AND json_extract(rq.canonical_envelope,'$.max_operations')=NEW.max_operations
     AND json_extract(rq.canonical_envelope,'$.max_total_input')=NEW.max_total_input
     AND c07.evidence_hash=(SELECT g7.evidence_hash FROM manual_operator_gate_attestations g7 WHERE g7.authorization_id=NEW.id AND g7.authorization_epoch=NEW.epoch AND g7.gate='C07')
     AND c07.reviewed_head=(SELECT g7.reviewed_head FROM manual_operator_gate_attestations g7 WHERE g7.authorization_id=NEW.id AND g7.authorization_epoch=NEW.epoch AND g7.gate='C07')
     AND c07.attested_at=(SELECT g7.attested_at FROM manual_operator_gate_attestations g7 WHERE g7.authorization_id=NEW.id AND g7.authorization_epoch=NEW.epoch AND g7.gate='C07')
     AND (SELECT COUNT(*) FROM manual_operator_gate_attestations g WHERE g.authorization_id=NEW.id AND g.authorization_epoch=NEW.epoch)=4
     AND NOT EXISTS(SELECT 1 FROM manual_operator_gate_attestations g WHERE g.authorization_id=NEW.id AND g.authorization_epoch=NEW.epoch AND g.gate IN ('C04','C05','C08')
       AND NOT EXISTS(SELECT 1 FROM canary_gate_attestations c WHERE c.gate=g.gate AND c.status='PASS' AND c.evidence_hash=g.evidence_hash AND c.attested_at=g.attested_at))
     AND (SELECT COUNT(*) FROM manual_operator_gate_attestations g WHERE g.authorization_id=NEW.id AND g.authorization_epoch=NEW.epoch AND g.reviewed_head!=(SELECT reviewed_head FROM manual_operator_c07_attestations WHERE authorization_id=NEW.id AND authorization_epoch=NEW.epoch))=0
     AND json_extract(rq.canonical_envelope,'$.gate_evidence_set_hash')=manual_operator_gate_set_hash(
       (SELECT evidence_hash FROM manual_operator_gate_attestations WHERE authorization_id=NEW.id AND authorization_epoch=NEW.epoch AND gate='C04'),
       (SELECT reviewed_head FROM manual_operator_gate_attestations WHERE authorization_id=NEW.id AND authorization_epoch=NEW.epoch AND gate='C04'),
       (SELECT attested_at FROM manual_operator_gate_attestations WHERE authorization_id=NEW.id AND authorization_epoch=NEW.epoch AND gate='C04'),
       (SELECT evidence_hash FROM manual_operator_gate_attestations WHERE authorization_id=NEW.id AND authorization_epoch=NEW.epoch AND gate='C05'),
       (SELECT reviewed_head FROM manual_operator_gate_attestations WHERE authorization_id=NEW.id AND authorization_epoch=NEW.epoch AND gate='C05'),
       (SELECT attested_at FROM manual_operator_gate_attestations WHERE authorization_id=NEW.id AND authorization_epoch=NEW.epoch AND gate='C05'),
       (SELECT evidence_hash FROM manual_operator_gate_attestations WHERE authorization_id=NEW.id AND authorization_epoch=NEW.epoch AND gate='C07'),
       (SELECT reviewed_head FROM manual_operator_gate_attestations WHERE authorization_id=NEW.id AND authorization_epoch=NEW.epoch AND gate='C07'),
       (SELECT attested_at FROM manual_operator_gate_attestations WHERE authorization_id=NEW.id AND authorization_epoch=NEW.epoch AND gate='C07'),
       (SELECT evidence_hash FROM manual_operator_gate_attestations WHERE authorization_id=NEW.id AND authorization_epoch=NEW.epoch AND gate='C08'),
       (SELECT reviewed_head FROM manual_operator_gate_attestations WHERE authorization_id=NEW.id AND authorization_epoch=NEW.epoch AND gate='C08'),
       (SELECT attested_at FROM manual_operator_gate_attestations WHERE authorization_id=NEW.id AND authorization_epoch=NEW.epoch AND gate='C08'))
 )
BEGIN SELECT RAISE(ABORT,'manual authorization requires complete verified approval'); END;
