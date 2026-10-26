-- A4-A durable operator evidence overlay. This migration does not arm MANUAL
-- authorization and does not replace migration018's no-arm guard. Signature
-- verification/arming is a later separately reviewed A4-B slice.
CREATE TABLE manual_operator_trust_roots(
  operator_key_id TEXT PRIMARY KEY,
  public_key_hex TEXT NOT NULL CHECK(length(public_key_hex)=64 AND public_key_hex GLOB '[0-9A-Fa-f]*'),
  root_fingerprint TEXT NOT NULL CHECK(length(root_fingerprint)=64),
  bootstrap_evidence_hash TEXT NOT NULL CHECK(length(bootstrap_evidence_hash)=64),
  created_at TEXT NOT NULL,
  revoked_at TEXT
);
CREATE TRIGGER manual_operator_trust_root_no_update BEFORE UPDATE ON manual_operator_trust_roots
BEGIN SELECT RAISE(ABORT,'operator trust root immutable'); END;
CREATE TRIGGER manual_operator_trust_root_no_delete BEFORE DELETE ON manual_operator_trust_roots
BEGIN SELECT RAISE(ABORT,'operator trust root retained'); END;

CREATE TABLE manual_operator_authorization_requests(
  request_id TEXT PRIMARY KEY,
  authorization_id TEXT NOT NULL,
  authorization_epoch INTEGER NOT NULL,
  request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
  canonical_envelope TEXT NOT NULL CHECK(json_valid(canonical_envelope)),
  created_at TEXT NOT NULL,
  FOREIGN KEY(authorization_id,authorization_epoch) REFERENCES canary_runtime_authorizations(id,epoch)
);
CREATE TRIGGER manual_operator_request_no_update BEFORE UPDATE ON manual_operator_authorization_requests
BEGIN SELECT RAISE(ABORT,'operator authorization request immutable'); END;
CREATE TRIGGER manual_operator_request_no_delete BEFORE DELETE ON manual_operator_authorization_requests
BEGIN SELECT RAISE(ABORT,'operator authorization request retained'); END;

CREATE TABLE manual_operator_authorization_approvals(
  approval_id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL REFERENCES manual_operator_authorization_requests(request_id),
  operator_key_id TEXT NOT NULL REFERENCES manual_operator_trust_roots(operator_key_id),
  envelope_hash TEXT NOT NULL CHECK(length(envelope_hash)=64),
  signature_hex TEXT NOT NULL CHECK(length(signature_hex)=128),
  issued_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX manual_operator_one_approval_per_request
ON manual_operator_authorization_approvals(request_id);
CREATE TRIGGER manual_operator_approval_no_update BEFORE UPDATE ON manual_operator_authorization_approvals
BEGIN SELECT RAISE(ABORT,'operator approval immutable'); END;
CREATE TRIGGER manual_operator_approval_no_delete BEFORE DELETE ON manual_operator_authorization_approvals
BEGIN SELECT RAISE(ABORT,'operator approval retained'); END;

CREATE TABLE manual_operator_revocations(
  revocation_id TEXT PRIMARY KEY,
  authorization_id TEXT NOT NULL,
  authorization_epoch INTEGER NOT NULL,
  reason_code TEXT NOT NULL,
  evidence_hash TEXT NOT NULL CHECK(length(evidence_hash)=64),
  created_at TEXT NOT NULL,
  FOREIGN KEY(authorization_id,authorization_epoch) REFERENCES canary_runtime_authorizations(id,epoch)
);
CREATE TRIGGER manual_operator_revocation_no_update BEFORE UPDATE ON manual_operator_revocations
BEGIN SELECT RAISE(ABORT,'operator revocation immutable'); END;
CREATE TRIGGER manual_operator_revocation_no_delete BEFORE DELETE ON manual_operator_revocations
BEGIN SELECT RAISE(ABORT,'operator revocation retained'); END;

-- A4-A deliberately keeps the original unconditional MANUAL no-arm guard.
-- No approval/evidence row may be used as a direct-SQL arming bypass.
