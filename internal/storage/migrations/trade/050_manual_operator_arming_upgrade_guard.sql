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
  WHERE rq.authorization_id=NEW.id
    AND rq.authorization_epoch=NEW.epoch
    AND json(rq.canonical_envelope)=rq.canonical_envelope
    AND (SELECT COUNT(*) FROM json_each(rq.canonical_envelope))=23
    AND (SELECT COUNT(DISTINCT key) FROM json_each(rq.canonical_envelope))=23
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
