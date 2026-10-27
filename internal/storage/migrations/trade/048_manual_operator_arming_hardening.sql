-- A4-B2 hardening.  This migration adds only durable witnesses around the
-- existing canary_runtime_authorizations state owner.  It does not create a
-- second authorization state machine or any send capability.
CREATE TABLE manual_operator_trust_root_bootstrap(
  operator_key_id TEXT PRIMARY KEY,
  public_key_hex TEXT NOT NULL CHECK(length(public_key_hex)=64 AND public_key_hex NOT GLOB '*[^0-9A-Fa-f]*'),
  root_fingerprint TEXT NOT NULL CHECK(length(root_fingerprint)=64 AND root_fingerprint NOT GLOB '*[^0-9A-Fa-f]*'),
  bootstrap_evidence_hash TEXT NOT NULL CHECK(length(bootstrap_evidence_hash)=64 AND bootstrap_evidence_hash NOT GLOB '*[^0-9A-Fa-f]*'),
  deployment_id TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TRIGGER manual_operator_trust_root_bootstrap_immutable
BEFORE UPDATE ON manual_operator_trust_root_bootstrap
BEGIN SELECT RAISE(ABORT,'operator trust bootstrap immutable'); END;
CREATE TRIGGER manual_operator_trust_root_bootstrap_no_delete
BEFORE DELETE ON manual_operator_trust_root_bootstrap
BEGIN SELECT RAISE(ABORT,'operator trust bootstrap retained'); END;
CREATE TRIGGER manual_operator_trust_root_bootstrap_identity
BEFORE INSERT ON manual_operator_trust_root_bootstrap
WHEN length(NEW.deployment_id) NOT BETWEEN 1 AND 128
BEGIN SELECT RAISE(ABORT,'operator trust bootstrap deployment identity invalid'); END;
CREATE TRIGGER manual_operator_trust_root_requires_bootstrap
BEFORE INSERT ON manual_operator_trust_roots
WHEN NOT EXISTS(
  SELECT 1 FROM manual_operator_trust_root_bootstrap b
  WHERE b.operator_key_id=NEW.operator_key_id
    AND b.public_key_hex=NEW.public_key_hex
    AND b.root_fingerprint=NEW.root_fingerprint
    AND b.bootstrap_evidence_hash=NEW.bootstrap_evidence_hash
)
BEGIN SELECT RAISE(ABORT,'operator trust root requires authenticated bootstrap witness'); END;

CREATE TABLE manual_operator_authorization_sources(
  authorization_id TEXT NOT NULL,
  authorization_epoch INTEGER NOT NULL,
  operation_id TEXT NOT NULL UNIQUE REFERENCES operations(id),
  source_hash TEXT NOT NULL CHECK(length(source_hash)=64 AND source_hash NOT GLOB '*[^0-9A-Fa-f]*'),
  created_at TEXT NOT NULL,
  PRIMARY KEY(authorization_id,authorization_epoch),
  FOREIGN KEY(authorization_id,authorization_epoch) REFERENCES canary_runtime_authorizations(id,epoch)
);
CREATE TRIGGER manual_operator_authorization_source_immutable
BEFORE UPDATE ON manual_operator_authorization_sources
BEGIN SELECT RAISE(ABORT,'operator authorization source immutable'); END;
CREATE TRIGGER manual_operator_authorization_source_no_delete
BEFORE DELETE ON manual_operator_authorization_sources
BEGIN SELECT RAISE(ABORT,'operator authorization source retained'); END;
CREATE TRIGGER manual_operator_authorization_source_bind
BEFORE INSERT ON manual_operator_authorization_sources
WHEN NOT EXISTS(
  SELECT 1
  FROM canary_admission_sources s
  JOIN canary_runtime_authorizations a ON a.id=NEW.authorization_id AND a.epoch=NEW.authorization_epoch
  WHERE s.operation_id=NEW.operation_id
    AND s.source_hash=NEW.source_hash
    AND s.wallet_id=a.wallet_id
    AND s.policy_version=a.policy_version
)
BEGIN SELECT RAISE(ABORT,'operator authorization source binding mismatch'); END;

CREATE TABLE manual_operator_c07_attestations(
  authorization_id TEXT NOT NULL,
  authorization_epoch INTEGER NOT NULL,
  evidence_hash TEXT NOT NULL CHECK(length(evidence_hash)=64 AND evidence_hash NOT GLOB '*[^0-9A-Fa-f]*'),
  reviewed_head TEXT NOT NULL CHECK(length(reviewed_head)=40 OR length(reviewed_head)=64),
  attested_at TEXT NOT NULL,
  PRIMARY KEY(authorization_id,authorization_epoch),
  FOREIGN KEY(authorization_id,authorization_epoch) REFERENCES canary_runtime_authorizations(id,epoch)
);
CREATE TRIGGER manual_operator_c07_immutable
BEFORE UPDATE ON manual_operator_c07_attestations
BEGIN SELECT RAISE(ABORT,'operator C07 attestation immutable'); END;
CREATE TRIGGER manual_operator_c07_no_delete
BEFORE DELETE ON manual_operator_c07_attestations
BEGIN SELECT RAISE(ABORT,'operator C07 attestation retained'); END;

-- Requests are accepted only in the exact compact JSON form emitted by the
-- canonical Go envelope.  The key-set and type checks prevent extra keys,
-- duplicate keys and caller-controlled alternate encodings at the SQL edge.
CREATE TRIGGER manual_operator_request_canonical
BEFORE INSERT ON manual_operator_authorization_requests
WHEN json(NEW.canonical_envelope) != NEW.canonical_envelope
 OR (SELECT COUNT(*) FROM json_each(NEW.canonical_envelope)) != 23
 OR (SELECT COUNT(DISTINCT key) FROM json_each(NEW.canonical_envelope)) != 23
 OR EXISTS(SELECT 1 FROM json_each(NEW.canonical_envelope) WHERE key NOT IN
   ('domain','approval_id','request_id','operator_key_id','issued_at','expires_at',
    'authorization_id','authorization_epoch','build_sha','release_id','deployment_id',
    'environment','chain_id','wallet_id','wallet_address','policy_version','policy_hash',
    'gate_evidence_set_hash','max_operations','max_total_input','source_hash','nonce','schema_version'))
 OR json_type(NEW.canonical_envelope,'$.domain')!='text'
 OR json_type(NEW.canonical_envelope,'$.authorization_epoch')!='integer'
 OR json_type(NEW.canonical_envelope,'$.chain_id')!='integer'
 OR json_type(NEW.canonical_envelope,'$.policy_version')!='integer'
 OR json_type(NEW.canonical_envelope,'$.max_operations')!='integer'
 OR json_type(NEW.canonical_envelope,'$.max_total_input')!='text'
 OR json_extract(NEW.canonical_envelope,'$.max_total_input') GLOB '*[^0-9]*'
 OR length(json_extract(NEW.canonical_envelope,'$.max_total_input'))<1
 OR json_extract(NEW.canonical_envelope,'$.schema_version')!='1'
BEGIN SELECT RAISE(ABORT,'operator approval envelope is not canonical'); END;

CREATE TRIGGER manual_operator_approval_binding
BEFORE INSERT ON manual_operator_authorization_approvals
WHEN EXISTS(
  SELECT 1 FROM manual_operator_authorization_requests existing
  JOIN manual_operator_authorization_approvals prior ON prior.request_id=existing.request_id
  WHERE json_extract(existing.canonical_envelope,'$.nonce')=json_extract((SELECT canonical_envelope FROM manual_operator_authorization_requests WHERE request_id=NEW.request_id),'$.nonce')
)
 OR NOT EXISTS(
  SELECT 1 FROM manual_operator_authorization_requests r
  WHERE r.request_id=NEW.request_id
    AND json_extract(r.canonical_envelope,'$.approval_id')=NEW.approval_id
    AND json_extract(r.canonical_envelope,'$.operator_key_id')=NEW.operator_key_id
    AND json_extract(r.canonical_envelope,'$.issued_at')=NEW.issued_at
    AND json_extract(r.canonical_envelope,'$.expires_at')=NEW.expires_at
    AND NEW.envelope_hash=r.request_hash
)
BEGIN SELECT RAISE(ABORT,'operator approval envelope binding mismatch'); END;

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
     AND root.revoked_at IS NULL
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
     AND json_extract(rq.canonical_envelope,'$.source_hash')=src.source_hash
     AND json_extract(rq.canonical_envelope,'$.source_hash')=source.source_hash
     AND source.wallet_id=NEW.wallet_id AND source.policy_version=NEW.policy_version
     AND NOT EXISTS(SELECT 1 FROM manual_operator_revocations r WHERE r.authorization_id=NEW.id AND r.authorization_epoch=NEW.epoch)
     AND json_extract(rq.canonical_envelope,'$.gate_evidence_set_hash')=NEW.gate_evidence_set_hash
     AND json_extract(rq.canonical_envelope,'$.max_operations')=NEW.max_operations
     AND json_extract(rq.canonical_envelope,'$.max_total_input')=NEW.max_total_input
     AND EXISTS(SELECT 1 FROM manual_operator_gate_attestations g7 WHERE g7.authorization_id=(SELECT authorization_id FROM manual_operator_authorization_requests WHERE request_id=NEW.request_id) AND g7.authorization_epoch=(SELECT authorization_epoch FROM manual_operator_authorization_requests WHERE request_id=NEW.request_id) AND g7.gate='C07' AND g7.evidence_hash=c07.evidence_hash)
     AND (SELECT COUNT(*) FROM manual_operator_gate_attestations g WHERE g.authorization_id=NEW.id AND g.authorization_epoch=NEW.epoch)=4
     AND NOT EXISTS(SELECT 1 FROM manual_operator_gate_attestations g WHERE g.authorization_id=NEW.id AND g.authorization_epoch=NEW.epoch AND g.gate IN ('C04','C05','C08')
       AND NOT EXISTS(SELECT 1 FROM canary_gate_attestations c WHERE c.gate=g.gate AND c.status='PASS' AND c.evidence_hash=g.evidence_hash))
 )
BEGIN SELECT RAISE(ABORT,'manual authorization requires verified operator approval'); END;
