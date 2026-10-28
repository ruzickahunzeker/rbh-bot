-- A4-B2 residual hardening.  This additive migration does not replace the
-- existing state owner or send path.  It adds upgrade-time predicates so
-- rows written before migrations 048/049 cannot be used to arm an operator
-- authorization, and binds a source to the same authorization namespace.
CREATE TABLE manual_operator_arm_guard_provenance(
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  prior_trigger_sql TEXT NOT NULL CHECK(
    instr(prior_trigger_sql,'manual_b1_no_arm')>0 AND
    instr(prior_trigger_sql,'complete verified approval')>0
  ),
  captured_at TEXT NOT NULL
);
CREATE TABLE manual_operator_arm_guard_precondition(v INTEGER NOT NULL CHECK(v=1));
INSERT INTO manual_operator_arm_guard_precondition(v)
SELECT CASE WHEN EXISTS(
  SELECT 1 FROM sqlite_master
  WHERE type='trigger' AND name='manual_b1_no_arm'
    AND instr(sql,'manual_b1_no_arm')>0
    AND instr(sql,'complete verified approval')>0
) THEN 1 ELSE 0 END;
DROP TABLE manual_operator_arm_guard_precondition;
INSERT INTO manual_operator_arm_guard_provenance(singleton,prior_trigger_sql,captured_at)
SELECT 1,sql,datetime('now') FROM sqlite_master
WHERE type='trigger' AND name='manual_b1_no_arm';
CREATE TRIGGER manual_operator_arm_guard_provenance_immutable
BEFORE UPDATE ON manual_operator_arm_guard_provenance
BEGIN SELECT RAISE(ABORT,'operator arm guard provenance immutable'); END;
CREATE TRIGGER manual_operator_arm_guard_provenance_no_delete
BEFORE DELETE ON manual_operator_arm_guard_provenance
BEGIN SELECT RAISE(ABORT,'operator arm guard provenance retained'); END;

-- A source cannot be adopted by another authorization.  Existing source rows
-- remain immutable; this guard applies to all future inserts.
CREATE TRIGGER manual_operator_authorization_source_cross_auth
BEFORE INSERT ON manual_operator_authorization_sources
WHEN EXISTS(
  SELECT 1 FROM canary_authorization_operation_usage u
  WHERE u.operation_id=NEW.operation_id
    AND u.authorization_id!=NEW.authorization_id
)
BEGIN SELECT RAISE(ABORT,'operator source belongs to another authorization'); END;

-- Revalidate legacy rows at the only security boundary that consumes them.
-- Bootstrap/C07 MACs are checked again, canonical request shape and nonce
-- uniqueness are checked again, and no source already claimed by another
-- authorization may satisfy the arming predicate.
CREATE TRIGGER manual_operator_upgrade_arm_guard
BEFORE UPDATE OF state ON canary_runtime_authorizations
WHEN NEW.state='ARMED' AND EXISTS(
  SELECT 1 FROM manual_authorization_units WHERE authorization_id=NEW.id
) AND NOT EXISTS(
  SELECT 1
  FROM manual_operator_authorization_requests rq
  JOIN manual_operator_authorization_approvals ap ON ap.request_id=rq.request_id
  JOIN manual_operator_trust_roots root ON root.operator_key_id=ap.operator_key_id
  JOIN manual_operator_trust_root_bootstrap boot
    ON boot.operator_key_id=root.operator_key_id
   AND boot.public_key_hex=root.public_key_hex
   AND boot.root_fingerprint=root.root_fingerprint
   AND boot.bootstrap_evidence_hash=root.bootstrap_evidence_hash
  JOIN manual_operator_authorization_sources src
    ON src.authorization_id=NEW.id AND src.authorization_epoch=NEW.epoch
  JOIN manual_operator_c07_attestations c07
    ON c07.authorization_id=NEW.id AND c07.authorization_epoch=NEW.epoch
  JOIN manual_gate_clock_state clock
    ON clock.environment=NEW.environment AND clock.chain_id=4663
  WHERE rq.authorization_id=NEW.id
    AND rq.authorization_epoch=NEW.epoch
    AND json(rq.canonical_envelope)=rq.canonical_envelope
    AND (SELECT COUNT(*) FROM json_each(rq.canonical_envelope))=23
    AND (SELECT COUNT(DISTINCT key) FROM json_each(rq.canonical_envelope))=23
    AND NOT EXISTS(SELECT 1 FROM json_each(rq.canonical_envelope) WHERE key NOT IN
      ('domain','approval_id','request_id','operator_key_id','issued_at','expires_at',
       'authorization_id','authorization_epoch','build_sha','release_id','deployment_id',
       'environment','chain_id','wallet_id','wallet_address','policy_version','policy_hash',
       'gate_evidence_set_hash','max_operations','max_total_input','source_hash','nonce','schema_version'))
    AND json_type(rq.canonical_envelope,'$.domain')='text'
    AND json_type(rq.canonical_envelope,'$.authorization_epoch')='integer'
    AND json_type(rq.canonical_envelope,'$.chain_id')='integer'
    AND json_type(rq.canonical_envelope,'$.policy_version')='integer'
    AND json_type(rq.canonical_envelope,'$.max_operations')='integer'
    AND json_type(rq.canonical_envelope,'$.max_total_input')='text'
    AND json_extract(rq.canonical_envelope,'$.max_total_input') NOT GLOB '*[^0-9]*'
    AND length(json_extract(rq.canonical_envelope,'$.max_total_input'))>0
    AND manual_artifact_bytes_sha256(hex(rq.canonical_envelope))=rq.request_hash
    AND manual_operator_signature_valid(root.public_key_hex,rq.canonical_envelope,ap.signature_hex)=1
    AND ap.envelope_hash=rq.request_hash
    AND json_extract(rq.canonical_envelope,'$.approval_id')=ap.approval_id
    AND json_extract(rq.canonical_envelope,'$.request_id')=rq.request_id
    AND json_extract(rq.canonical_envelope,'$.operator_key_id')=ap.operator_key_id
    AND json_extract(rq.canonical_envelope,'$.domain')='RBH-MANUAL-A4-AUTHORIZATION-V1'
    AND json_extract(rq.canonical_envelope,'$.authorization_id')=NEW.id
    AND json_extract(rq.canonical_envelope,'$.authorization_epoch')=NEW.epoch
    AND json_extract(rq.canonical_envelope,'$.deployment_id')=NEW.deployment_id
    AND root.revoked_at IS NULL
    AND clock.fault_latched=0
    AND manual_time_cmp(clock.high_water_at,ap.issued_at)>=0
    AND manual_time_cmp(clock.high_water_at,ap.expires_at)<0
    AND (SELECT COUNT(*) FROM manual_operator_gate_attestations g
         WHERE g.authorization_id=NEW.id AND g.authorization_epoch=NEW.epoch)=4
    AND EXISTS(SELECT 1 FROM manual_operator_gate_attestations g7
      WHERE g7.authorization_id=NEW.id AND g7.authorization_epoch=NEW.epoch
        AND g7.gate='C07' AND g7.evidence_hash=c07.evidence_hash
        AND g7.reviewed_head=c07.reviewed_head AND g7.attested_at=c07.attested_at)
    AND NOT EXISTS(SELECT 1 FROM manual_operator_gate_attestations g
      WHERE g.authorization_id=NEW.id AND g.authorization_epoch=NEW.epoch
        AND g.gate IN ('C04','C05','C08')
        AND NOT EXISTS(SELECT 1 FROM canary_gate_attestations c
          WHERE c.gate=g.gate AND c.status='PASS'
            AND c.evidence_hash=g.evidence_hash AND c.attested_at=g.attested_at))
    AND (SELECT COUNT(*) FROM manual_operator_authorization_requests prior
         WHERE json_extract(prior.canonical_envelope,'$.nonce')=
               json_extract(rq.canonical_envelope,'$.nonce'))=1
    AND manual_operator_bootstrap_valid(root.operator_key_id,root.public_key_hex,
        root.root_fingerprint,root.bootstrap_evidence_hash,boot.deployment_id,
        boot.attestation_mac)=1
    AND manual_operator_bootstrap_valid(c07.authorization_id,
        c07.authorization_epoch,c07.evidence_hash,c07.reviewed_head,
        c07.attested_at,c07.attestation_mac)=1
    AND boot.deployment_id=NEW.deployment_id
    AND NOT EXISTS(
      SELECT 1 FROM canary_authorization_operation_usage u
      WHERE u.operation_id=src.operation_id
        AND u.authorization_id!=NEW.id
    )
)
BEGIN SELECT RAISE(ABORT,'legacy operator evidence requires upgrade proof'); END;
