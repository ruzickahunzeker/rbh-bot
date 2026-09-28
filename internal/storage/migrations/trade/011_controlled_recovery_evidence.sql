CREATE TABLE canary_recovery_evidence(
  id TEXT PRIMARY KEY,
  operation_id TEXT NOT NULL REFERENCES operations(id),
  attempt_id TEXT NOT NULL REFERENCES transaction_attempts(id),
  tx_hash TEXT NOT NULL,
  lease_environment TEXT NOT NULL,
  lease_holder_id TEXT NOT NULL,
  lease_epoch INTEGER NOT NULL CHECK(lease_epoch>0),
  tx_found INTEGER NOT NULL CHECK(tx_found IN (0,1)),
  receipt_found INTEGER NOT NULL CHECK(receipt_found IN (0,1)),
  nonce_state TEXT NOT NULL,
  evidence_hash TEXT NOT NULL CHECK(length(evidence_hash)=64),
  classification TEXT NOT NULL CHECK(classification IN ('PROPAGATED','AMBIGUOUS','NONCE_UNSAFE')),
  observed_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(attempt_id,evidence_hash)
);

CREATE TRIGGER canary_recovery_evidence_no_update
BEFORE UPDATE ON canary_recovery_evidence
BEGIN SELECT RAISE(ABORT,'canary recovery evidence is immutable'); END;

CREATE TRIGGER canary_recovery_evidence_no_delete
BEFORE DELETE ON canary_recovery_evidence
BEGIN SELECT RAISE(ABORT,'canary recovery evidence cannot be deleted'); END;
