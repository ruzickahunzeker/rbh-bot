CREATE TABLE canary_authorization_epochs(
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  last_epoch INTEGER NOT NULL CHECK(last_epoch>=0)
);
INSERT INTO canary_authorization_epochs(singleton,last_epoch) VALUES(1,0);

CREATE TABLE canary_runtime_authorizations(
  id TEXT PRIMARY KEY,
  epoch INTEGER NOT NULL UNIQUE CHECK(epoch>0),
  mode TEXT NOT NULL CHECK(mode='CONTROLLED_CANARY'),
  environment TEXT NOT NULL,
  chain_id INTEGER NOT NULL CHECK(chain_id=4663),
  policy_version INTEGER NOT NULL REFERENCES canary_policies(version),
  wallet_id TEXT NOT NULL,
  build_sha TEXT NOT NULL CHECK(length(build_sha)=40 OR length(build_sha)=64),
  release_id TEXT NOT NULL,
  deployment_id TEXT NOT NULL,
  gate_evidence_set_hash TEXT NOT NULL CHECK(length(gate_evidence_set_hash)=64),
  authorization_ref TEXT NOT NULL,
  max_operations INTEGER NOT NULL CHECK(max_operations>0),
  max_total_input TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('PENDING','ARMED','REVOKED','EXPIRED','EXHAUSTED')),
  authorized_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(id,epoch)
);
CREATE UNIQUE INDEX one_armed_canary_authorization
ON canary_runtime_authorizations(environment,chain_id,wallet_id)
WHERE state='ARMED';
CREATE TRIGGER canary_runtime_authorization_identity_immutable
BEFORE UPDATE ON canary_runtime_authorizations
WHEN NEW.epoch!=OLD.epoch OR NEW.mode!=OLD.mode OR NEW.environment!=OLD.environment OR
     NEW.chain_id!=OLD.chain_id OR NEW.policy_version!=OLD.policy_version OR
     NEW.wallet_id!=OLD.wallet_id OR NEW.build_sha!=OLD.build_sha OR
     NEW.release_id!=OLD.release_id OR NEW.deployment_id!=OLD.deployment_id OR
     NEW.gate_evidence_set_hash!=OLD.gate_evidence_set_hash OR
     NEW.authorization_ref!=OLD.authorization_ref OR NEW.max_operations!=OLD.max_operations OR
     NEW.max_total_input!=OLD.max_total_input OR NEW.authorized_at!=OLD.authorized_at OR
     NEW.expires_at!=OLD.expires_at OR NEW.created_at!=OLD.created_at
BEGIN SELECT RAISE(ABORT,'canary runtime authorization identity is immutable'); END;
CREATE TRIGGER canary_runtime_authorization_no_delete BEFORE DELETE ON canary_runtime_authorizations
BEGIN SELECT RAISE(ABORT,'canary runtime authorization cannot be deleted'); END;
CREATE TRIGGER canary_runtime_authorization_transition
BEFORE UPDATE OF state ON canary_runtime_authorizations
WHEN NOT (
  (OLD.state='PENDING' AND NEW.state IN ('ARMED','REVOKED','EXPIRED')) OR
  (OLD.state='ARMED' AND NEW.state IN ('REVOKED','EXPIRED','EXHAUSTED')) OR
  NEW.state=OLD.state
)
BEGIN SELECT RAISE(ABORT,'invalid canary authorization transition'); END;

CREATE TABLE canary_authorization_usage(
  authorization_id TEXT PRIMARY KEY REFERENCES canary_runtime_authorizations(id),
  operation_count INTEGER NOT NULL DEFAULT 0 CHECK(operation_count>=0),
  total_input TEXT NOT NULL DEFAULT '0',
  updated_at TEXT NOT NULL
);
CREATE TABLE canary_authorization_operation_usage(
  authorization_id TEXT NOT NULL REFERENCES canary_runtime_authorizations(id),
  operation_id TEXT NOT NULL REFERENCES operations(id),
  amount TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('RESERVED','CONSUMED','FROZEN','RELEASED')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(authorization_id,operation_id)
);

CREATE TABLE canary_runtime_gate_snapshots(
  id TEXT PRIMARY KEY,
  operation_id TEXT NOT NULL REFERENCES operations(id),
  attempt_id TEXT REFERENCES transaction_attempts(id),
  stage TEXT NOT NULL CHECK(stage IN ('EXECUTION_ADMISSION','PRE_SIGN','FIRST_BROADCAST','IMMEDIATE_PRE_SEND','UNKNOWN_REPLAY')),
  authorization_id TEXT NOT NULL REFERENCES canary_runtime_authorizations(id),
  authorization_epoch INTEGER NOT NULL,
  build_sha TEXT NOT NULL,
  release_id TEXT NOT NULL,
  deployment_id TEXT NOT NULL,
  policy_version INTEGER NOT NULL,
  policy_hash TEXT NOT NULL CHECK(length(policy_hash)=64),
  gate_evidence_set_hash TEXT NOT NULL CHECK(length(gate_evidence_set_hash)=64),
  emergency_stop_revision INTEGER NOT NULL CHECK(emergency_stop_revision>=0),
  decision TEXT NOT NULL CHECK(decision IN ('PASS','REJECT')),
  reason_code TEXT NOT NULL,
  checked_at TEXT NOT NULL,
  FOREIGN KEY(authorization_id,authorization_epoch) REFERENCES canary_runtime_authorizations(id,epoch)
);
CREATE UNIQUE INDEX canary_gate_snapshot_once
ON canary_runtime_gate_snapshots(operation_id,stage,authorization_epoch,COALESCE(attempt_id,''));
CREATE TRIGGER canary_runtime_gate_snapshot_no_update BEFORE UPDATE ON canary_runtime_gate_snapshots
BEGIN SELECT RAISE(ABORT,'canary runtime gate snapshot is immutable'); END;
CREATE TRIGGER canary_runtime_gate_snapshot_no_delete BEFORE DELETE ON canary_runtime_gate_snapshots
BEGIN SELECT RAISE(ABORT,'canary runtime gate snapshot cannot be deleted'); END;

CREATE TABLE canary_send_permits(
  id TEXT PRIMARY KEY,
  operation_id TEXT NOT NULL REFERENCES operations(id),
  attempt_id TEXT NOT NULL REFERENCES transaction_attempts(id),
  authorization_id TEXT NOT NULL REFERENCES canary_runtime_authorizations(id),
  authorization_epoch INTEGER NOT NULL,
  purpose TEXT NOT NULL CHECK(purpose IN ('FIRST_BROADCAST','UNKNOWN_REPLAY')),
  artifact_hash TEXT NOT NULL CHECK(length(artifact_hash)=64),
  tx_hash TEXT NOT NULL CHECK(length(tx_hash)=66),
  query_evidence_hash TEXT,
  queried_at TEXT,
  state TEXT NOT NULL CHECK(state IN ('ISSUED','CONSUMED','REVOKED','EXPIRED')),
  issued_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  consumed_at TEXT,
  CHECK((purpose='FIRST_BROADCAST' AND query_evidence_hash IS NULL AND queried_at IS NULL) OR
        (purpose='UNKNOWN_REPLAY' AND length(query_evidence_hash)=64 AND queried_at IS NOT NULL)),
  FOREIGN KEY(authorization_id,authorization_epoch) REFERENCES canary_runtime_authorizations(id,epoch)
);
CREATE UNIQUE INDEX one_first_broadcast_permit_per_attempt
ON canary_send_permits(attempt_id) WHERE purpose='FIRST_BROADCAST';
CREATE UNIQUE INDEX one_active_replay_permit_per_attempt
ON canary_send_permits(attempt_id) WHERE purpose='UNKNOWN_REPLAY' AND state='ISSUED';
CREATE TRIGGER canary_send_permit_identity_immutable
BEFORE UPDATE ON canary_send_permits
WHEN NEW.operation_id!=OLD.operation_id OR NEW.attempt_id!=OLD.attempt_id OR
     NEW.authorization_id!=OLD.authorization_id OR NEW.authorization_epoch!=OLD.authorization_epoch OR
     NEW.purpose!=OLD.purpose OR NEW.artifact_hash!=OLD.artifact_hash OR NEW.tx_hash!=OLD.tx_hash OR
     COALESCE(NEW.query_evidence_hash,'')!=COALESCE(OLD.query_evidence_hash,'') OR
     COALESCE(NEW.queried_at,'')!=COALESCE(OLD.queried_at,'') OR
     NEW.issued_at!=OLD.issued_at OR NEW.expires_at!=OLD.expires_at
BEGIN SELECT RAISE(ABORT,'canary send permit identity is immutable'); END;
CREATE TRIGGER canary_send_permit_transition BEFORE UPDATE OF state ON canary_send_permits
WHEN NOT (OLD.state='ISSUED' AND NEW.state IN ('CONSUMED','REVOKED','EXPIRED'))
BEGIN SELECT RAISE(ABORT,'canary send permit is one-shot'); END;
CREATE TRIGGER canary_send_permit_no_delete BEFORE DELETE ON canary_send_permits
BEGIN SELECT RAISE(ABORT,'canary send permit cannot be deleted'); END;

CREATE TABLE canary_worker_leases(
  role TEXT NOT NULL CHECK(role IN ('SUBMISSION','RECOVERY')),
  environment TEXT NOT NULL,
  holder_id TEXT NOT NULL,
  lease_epoch INTEGER NOT NULL CHECK(lease_epoch>0),
  expires_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(role,environment)
);

CREATE TABLE canary_runtime_audit(
  id TEXT PRIMARY KEY,
  event_type TEXT NOT NULL,
  operation_id TEXT,
  attempt_id TEXT,
  authorization_id TEXT,
  authorization_epoch INTEGER,
  reason_code TEXT NOT NULL,
  details_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE canary_alert_outbox(
  id TEXT PRIMARY KEY,
  audit_id TEXT NOT NULL UNIQUE REFERENCES canary_runtime_audit(id),
  severity TEXT NOT NULL CHECK(severity IN ('INFO','WARNING','CRITICAL')),
  state TEXT NOT NULL CHECK(state IN ('PENDING','DELIVERED')),
  created_at TEXT NOT NULL,
  delivered_at TEXT
);
