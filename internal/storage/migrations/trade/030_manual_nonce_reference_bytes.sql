-- M3-B2 additive 030: preserve 001..029. Complete-byte evidence and
-- projected parent reference checks. Encoded joins are DENIAL-ONLY: every
-- non-TEXT parent reference in a relevant alias is unsafe, never repaired.
DROP VIEW manual_nonce_attempt_aliases;
CREATE VIEW manual_nonce_attempt_aliases AS
SELECT b.operation_id,t.rowid AS attempt_rowid,t.id AS attempt_id,t.step_id,
 t.nonce,manual_nonce_stored_value_valid(hex(t.nonce),typeof(t.nonce)) AS nonce_valid,
 CASE WHEN manual_nonce_identity_relation(hex(s.operation_id),typeof(s.operation_id),hex(b.operation_id),0)>0
 OR manual_nonce_identity_relation(hex(t.step_id),typeof(t.step_id),hex(b.execution_step_id),0)>0
 OR manual_nonce_identity_relation(hex(t.step_id),typeof(t.step_id),hex(b.validation_step_id),0)>0
 OR s.id IS NULL OR o.id IS NULL
 OR typeof(t.step_id)!='text' OR typeof(s.id)!='text' OR typeof(s.operation_id)!='text' OR typeof(o.id)!='text'
 OR manual_nonce_identity_relation(hex(t.wallet_id),typeof(t.wallet_id),hex(b.wallet_id),0)=2
 OR manual_nonce_identity_relation(hex(s.wallet_id),typeof(s.wallet_id),hex(b.wallet_id),0)=2
 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(b.wallet_id),0)=2
 OR manual_nonce_identity_relation(hex(t.from_address),typeof(t.from_address),hex(b.wallet_address),1)=2
 THEN 1 ELSE 0 END AS unsafe_identity
FROM manual_execution_bindings b CROSS JOIN transaction_attempts t
LEFT JOIN execution_steps s ON hex(s.id)=hex(t.step_id) AND s.id IS NOT NULL AND t.step_id IS NOT NULL
LEFT JOIN operations o ON hex(o.id)=hex(s.operation_id) AND o.id IS NOT NULL AND s.operation_id IS NOT NULL
WHERE manual_nonce_identity_relation(hex(s.operation_id),typeof(s.operation_id),hex(b.operation_id),0)>0
 OR manual_nonce_identity_relation(hex(t.step_id),typeof(t.step_id),hex(b.execution_step_id),0)>0
 OR manual_nonce_identity_relation(hex(t.step_id),typeof(t.step_id),hex(b.validation_step_id),0)>0
 OR manual_nonce_identity_relation(hex(t.wallet_id),typeof(t.wallet_id),hex(b.wallet_id),0)>0
 OR manual_nonce_identity_relation(hex(t.from_address),typeof(t.from_address),hex(b.wallet_address),1)>0
 OR manual_nonce_identity_relation(hex(s.wallet_id),typeof(s.wallet_id),hex(b.wallet_id),0)>0
 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(b.wallet_id),0)>0;

-- Keep 028 victim/no-repair rules; only its NEW-value relevance needed the
-- same storage-class-aware predicate as the durable alias view.
DROP TRIGGER manual_nonce_alias_insert;
CREATE TRIGGER manual_nonce_alias_insert BEFORE INSERT ON transaction_attempts
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims q
 LEFT JOIN execution_steps s ON hex(s.id)=hex(NEW.step_id) AND s.id IS NOT NULL AND NEW.step_id IS NOT NULL LEFT JOIN operations o ON hex(o.id)=hex(s.operation_id) AND o.id IS NOT NULL AND s.operation_id IS NOT NULL
 WHERE NEW.id IS NOT q.attempt_id
 AND (manual_nonce_identity_relation(hex(s.operation_id),typeof(s.operation_id),hex(q.operation_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.step_id),typeof(NEW.step_id),hex(q.execution_step_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.from_address),typeof(NEW.from_address),hex(q.wallet_address),1)>0
 OR manual_nonce_identity_relation(hex(s.wallet_id),typeof(s.wallet_id),hex(q.wallet_id),0)>0
 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(q.wallet_id),0)>0)
 AND (manual_nonce_identity_relation(hex(s.operation_id),typeof(s.operation_id),hex(q.operation_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.step_id),typeof(NEW.step_id),hex(q.execution_step_id),0)>0 OR s.id IS NULL OR o.id IS NULL
 OR typeof(NEW.step_id)!='text' OR typeof(s.id)!='text' OR typeof(s.operation_id)!='text' OR typeof(o.id)!='text'
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)=2
 OR manual_nonce_identity_relation(hex(NEW.from_address),typeof(NEW.from_address),hex(q.wallet_address),1)=2
 OR manual_nonce_identity_relation(hex(s.wallet_id),typeof(s.wallet_id),hex(q.wallet_id),0)=2
 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(q.wallet_id),0)=2
 OR manual_nonce_stored_value_valid(hex(NEW.nonce),typeof(NEW.nonce))=0 OR NEW.nonce=q.nonce))
 OR EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 WHERE (x.attempt_id=NEW.id OR x.attempt_rowid=NEW.rowid)
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce alias or conflict victim protected'); END;

DROP TRIGGER manual_nonce_alias_update;
CREATE TRIGGER manual_nonce_alias_update BEFORE UPDATE ON transaction_attempts
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 WHERE (x.attempt_rowid=OLD.rowid OR x.attempt_id=NEW.id OR x.attempt_rowid=NEW.rowid)
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
 OR EXISTS(SELECT 1 FROM manual_nonce_claims q
 LEFT JOIN execution_steps s ON hex(s.id)=hex(NEW.step_id) AND s.id IS NOT NULL AND NEW.step_id IS NOT NULL LEFT JOIN operations o ON hex(o.id)=hex(s.operation_id) AND o.id IS NOT NULL AND s.operation_id IS NOT NULL
 WHERE (manual_nonce_identity_relation(hex(s.operation_id),typeof(s.operation_id),hex(q.operation_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.step_id),typeof(NEW.step_id),hex(q.execution_step_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.from_address),typeof(NEW.from_address),hex(q.wallet_address),1)>0
 OR manual_nonce_identity_relation(hex(s.wallet_id),typeof(s.wallet_id),hex(q.wallet_id),0)>0
 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(q.wallet_id),0)>0)
 AND (manual_nonce_identity_relation(hex(s.operation_id),typeof(s.operation_id),hex(q.operation_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.step_id),typeof(NEW.step_id),hex(q.execution_step_id),0)>0 OR s.id IS NULL OR o.id IS NULL
 OR typeof(NEW.step_id)!='text' OR typeof(s.id)!='text' OR typeof(s.operation_id)!='text' OR typeof(o.id)!='text'
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)=2
 OR manual_nonce_identity_relation(hex(NEW.from_address),typeof(NEW.from_address),hex(q.wallet_address),1)=2
 OR manual_nonce_identity_relation(hex(s.wallet_id),typeof(s.wallet_id),hex(q.wallet_id),0)=2
 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(q.wallet_id),0)=2
 OR manual_nonce_stored_value_valid(hex(NEW.nonce),typeof(NEW.nonce))=0 OR NEW.nonce=q.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce alias cannot be adopted or repaired'); END;

-- Inserting or renaming a parent must not turn an unverifiable retained alias
-- into readable history. LEFT JOIN keeps the very missing parent being tested.
DROP TRIGGER manual_nonce_missing_step_insert;
CREATE TRIGGER manual_nonce_missing_step_insert BEFORE INSERT ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 LEFT JOIN execution_steps s ON hex(s.id)=hex(x.step_id) AND s.id IS NOT NULL AND x.step_id IS NOT NULL
 WHERE x.attempt_id IS NOT q.attempt_id AND s.id IS NULL
 AND manual_nonce_identity_relation(hex(x.step_id),typeof(x.step_id),hex(NEW.id),0)>0)
BEGIN SELECT RAISE(ABORT,'manual nonce missing step cannot be adopted'); END;
DROP TRIGGER manual_nonce_missing_step_update;
CREATE TRIGGER manual_nonce_missing_step_update BEFORE UPDATE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 LEFT JOIN execution_steps s ON hex(s.id)=hex(x.step_id) AND s.id IS NOT NULL AND x.step_id IS NOT NULL
 WHERE x.attempt_id IS NOT q.attempt_id AND s.id IS NULL
 AND manual_nonce_identity_relation(hex(x.step_id),typeof(x.step_id),hex(NEW.id),0)>0)
BEGIN SELECT RAISE(ABORT,'manual nonce missing step cannot be adopted'); END;
DROP TRIGGER manual_nonce_step_identity_insert;
CREATE TRIGGER manual_nonce_step_identity_insert BEFORE INSERT ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims q JOIN transaction_attempts t ON hex(t.step_id)=hex(NEW.id) AND t.step_id IS NOT NULL AND NEW.id IS NOT NULL
 LEFT JOIN operations o ON hex(o.id)=hex(NEW.operation_id) AND o.id IS NOT NULL AND NEW.operation_id IS NOT NULL
 WHERE t.id IS NOT q.attempt_id
 AND (manual_nonce_identity_relation(hex(NEW.operation_id),typeof(NEW.operation_id),hex(q.operation_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)>0 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(q.wallet_id),0)>0)
 AND (manual_nonce_identity_relation(hex(NEW.operation_id),typeof(NEW.operation_id),hex(q.operation_id),0)>0 OR o.id IS NULL
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)=2 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(q.wallet_id),0)=2
 OR typeof(t.step_id)!='text' OR typeof(NEW.id)!='text' OR typeof(NEW.operation_id)!='text' OR typeof(o.id)!='text'
 OR manual_nonce_stored_value_valid(hex(t.nonce),typeof(t.nonce))=0 OR t.nonce=q.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce inserted step identity conflicts'); END;
DROP TRIGGER manual_nonce_missing_operation_insert;
CREATE TRIGGER manual_nonce_missing_operation_insert BEFORE INSERT ON operations
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON hex(s.id)=hex(x.step_id) AND s.id IS NOT NULL AND x.step_id IS NOT NULL LEFT JOIN operations o ON hex(o.id)=hex(s.operation_id) AND o.id IS NOT NULL AND s.operation_id IS NOT NULL
 WHERE x.attempt_id IS NOT q.attempt_id AND o.id IS NULL
 AND manual_nonce_identity_relation(hex(s.operation_id),typeof(s.operation_id),hex(NEW.id),0)>0)
BEGIN SELECT RAISE(ABORT,'manual nonce missing operation cannot be adopted'); END;
DROP TRIGGER manual_nonce_missing_operation_update;
CREATE TRIGGER manual_nonce_missing_operation_update BEFORE UPDATE ON operations
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON hex(s.id)=hex(x.step_id) AND s.id IS NOT NULL AND x.step_id IS NOT NULL LEFT JOIN operations o ON hex(o.id)=hex(s.operation_id) AND o.id IS NOT NULL AND s.operation_id IS NOT NULL
 WHERE x.attempt_id IS NOT q.attempt_id AND o.id IS NULL
 AND manual_nonce_identity_relation(hex(s.operation_id),typeof(s.operation_id),hex(NEW.id),0)>0)
BEGIN SELECT RAISE(ABORT,'manual nonce missing operation cannot be adopted'); END;
DROP TRIGGER manual_nonce_operation_identity_insert;
CREATE TRIGGER manual_nonce_operation_identity_insert BEFORE INSERT ON operations
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims q JOIN execution_steps s ON hex(s.operation_id)=hex(NEW.id) AND s.operation_id IS NOT NULL AND NEW.id IS NOT NULL
 JOIN transaction_attempts t ON hex(t.step_id)=hex(s.id) AND t.step_id IS NOT NULL AND s.id IS NOT NULL
 WHERE t.id IS NOT q.attempt_id AND manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)>0
 AND (typeof(s.operation_id)!='text' OR typeof(s.id)!='text' OR typeof(t.step_id)!='text' OR typeof(NEW.id)!='text'
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)=2 OR manual_nonce_stored_value_valid(hex(t.nonce),typeof(t.nonce))=0 OR t.nonce=q.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce inserted operation identity conflicts'); END;

-- Real operation conflict victims use the existing composite uniqueness scope.
DROP TRIGGER manual_nonce_alias_operation_insert_victim;
CREATE TRIGGER manual_nonce_alias_operation_insert_victim BEFORE INSERT ON operations
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON hex(s.id)=hex(x.step_id) AND s.id IS NOT NULL AND x.step_id IS NOT NULL JOIN operations o ON hex(o.id)=hex(s.operation_id) AND o.id IS NOT NULL AND s.operation_id IS NOT NULL
 WHERE x.attempt_id IS NOT q.attempt_id AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce)
 AND (o.id=NEW.id OR o.rowid=NEW.rowid
 OR (o.chain_id=NEW.chain_id AND o.wallet_id=NEW.wallet_id AND o.idempotency_key=NEW.idempotency_key)))
BEGIN SELECT RAISE(ABORT,'manual nonce alias operation conflict victim protected'); END;
DROP TRIGGER manual_nonce_alias_operation_update;
CREATE TRIGGER manual_nonce_alias_operation_update BEFORE UPDATE ON operations
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON hex(s.id)=hex(x.step_id) AND s.id IS NOT NULL AND x.step_id IS NOT NULL
 WHERE hex(s.operation_id)=hex(OLD.id) AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
 OR EXISTS(SELECT 1 FROM manual_nonce_claims q JOIN execution_steps s ON hex(s.operation_id)=hex(OLD.id) AND s.operation_id IS NOT NULL AND OLD.id IS NOT NULL
 JOIN transaction_attempts t ON hex(t.step_id)=hex(s.id) AND t.step_id IS NOT NULL AND s.id IS NOT NULL
 WHERE t.id IS NOT q.attempt_id AND manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)>0
 AND (manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)=2 OR manual_nonce_stored_value_valid(hex(t.nonce),typeof(t.nonce))=0 OR t.nonce=q.nonce))
 OR EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON hex(s.id)=hex(x.step_id) AND s.id IS NOT NULL AND x.step_id IS NOT NULL JOIN operations o ON hex(o.id)=hex(s.operation_id) AND o.id IS NOT NULL AND s.operation_id IS NOT NULL
 WHERE o.rowid!=OLD.rowid AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce)
 AND (o.id=NEW.id OR o.rowid=NEW.rowid
 OR (o.chain_id=NEW.chain_id AND o.wallet_id=NEW.wallet_id AND o.idempotency_key=NEW.idempotency_key)))
BEGIN SELECT RAISE(ABORT,'manual nonce alias operation identity protected'); END;

DROP TRIGGER manual_nonce_alias_step_update;
CREATE TRIGGER manual_nonce_alias_step_update BEFORE UPDATE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 WHERE hex(x.step_id)=hex(OLD.id) AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
 OR EXISTS(SELECT 1 FROM manual_nonce_claims q JOIN transaction_attempts t ON hex(t.step_id)=hex(OLD.id) AND t.step_id IS NOT NULL AND OLD.id IS NOT NULL
 LEFT JOIN operations o ON hex(o.id)=hex(NEW.operation_id) AND o.id IS NOT NULL AND NEW.operation_id IS NOT NULL
 WHERE t.id IS NOT q.attempt_id
 AND (manual_nonce_identity_relation(hex(NEW.operation_id),typeof(NEW.operation_id),hex(q.operation_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)>0 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(q.wallet_id),0)>0)
 AND (manual_nonce_identity_relation(hex(NEW.operation_id),typeof(NEW.operation_id),hex(q.operation_id),0)>0 OR o.id IS NULL
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)=2 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(q.wallet_id),0)=2
 OR manual_nonce_stored_value_valid(hex(t.nonce),typeof(t.nonce))=0 OR t.nonce=q.nonce))
 OR EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON hex(s.id)=hex(x.step_id) AND s.id IS NOT NULL AND x.step_id IS NOT NULL
 WHERE s.rowid!=OLD.rowid AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce)
 AND (s.id=NEW.id OR s.rowid=NEW.rowid OR (s.operation_id=NEW.operation_id AND s.step_index=NEW.step_index)))
BEGIN SELECT RAISE(ABORT,'manual nonce alias step identity protected'); END;

CREATE TRIGGER manual_nonce_incoming_step_update BEFORE UPDATE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims q JOIN transaction_attempts t ON hex(t.step_id)=hex(NEW.id) AND t.step_id IS NOT NULL AND NEW.id IS NOT NULL
 LEFT JOIN operations o ON hex(o.id)=hex(NEW.operation_id) AND o.id IS NOT NULL AND NEW.operation_id IS NOT NULL
 WHERE t.id IS NOT q.attempt_id
 AND (manual_nonce_identity_relation(hex(NEW.operation_id),typeof(NEW.operation_id),hex(q.operation_id),0)>0
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)>0 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(q.wallet_id),0)>0)
 AND (manual_nonce_identity_relation(hex(NEW.operation_id),typeof(NEW.operation_id),hex(q.operation_id),0)>0 OR o.id IS NULL
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)=2 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(q.wallet_id),0)=2
 OR typeof(t.step_id)!='text' OR typeof(NEW.id)!='text' OR typeof(NEW.operation_id)!='text' OR typeof(o.id)!='text'
 OR manual_nonce_stored_value_valid(hex(t.nonce),typeof(t.nonce))=0 OR t.nonce=q.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce inserted step identity conflicts'); END;
CREATE TRIGGER manual_nonce_incoming_operation_update BEFORE UPDATE ON operations
WHEN EXISTS(SELECT 1 FROM manual_nonce_claims q JOIN execution_steps s ON hex(s.operation_id)=hex(NEW.id) AND s.operation_id IS NOT NULL AND NEW.id IS NOT NULL
 JOIN transaction_attempts t ON hex(t.step_id)=hex(s.id) AND t.step_id IS NOT NULL AND s.id IS NOT NULL
 WHERE t.id IS NOT q.attempt_id AND manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)>0
 AND (typeof(s.operation_id)!='text' OR typeof(s.id)!='text' OR typeof(t.step_id)!='text' OR typeof(NEW.id)!='text'
 OR manual_nonce_identity_relation(hex(NEW.wallet_id),typeof(NEW.wallet_id),hex(q.wallet_id),0)=2 OR manual_nonce_stored_value_valid(hex(t.nonce),typeof(t.nonce))=0 OR t.nonce=q.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce inserted operation identity conflicts'); END;

-- Own claim evidence is never exempt from full-byte validation.
CREATE TRIGGER manual_nonce_claim_bytes_source BEFORE INSERT ON manual_nonce_claims
WHEN NOT EXISTS(SELECT 1 FROM manual_nonce_binding_facts f WHERE f.operation_id=NEW.operation_id
 AND manual_nonce_observation_bytes_valid(hex(NEW.observation_json),typeof(NEW.observation_json),
 hex(NEW.observation_hash),typeof(NEW.observation_hash),hex(NEW.nonce),typeof(NEW.nonce),
 hex(f.facts_json),typeof(f.facts_json),hex(NEW.checked_at),typeof(NEW.checked_at))=1)
BEGIN SELECT RAISE(ABORT,'manual nonce full-byte evidence invalid'); END;
CREATE VIEW manual_nonce_claim_028_complete AS
SELECT q.* FROM manual_nonce_claim_027_complete q
WHERE NOT EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x
 WHERE x.operation_id=q.operation_id AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce));
DROP VIEW manual_nonce_claim_complete;
CREATE VIEW manual_nonce_claim_complete AS
SELECT q.* FROM manual_nonce_claim_028_complete q
JOIN manual_nonce_binding_facts f ON f.operation_id=q.operation_id
WHERE manual_nonce_observation_bytes_valid(hex(q.observation_json),typeof(q.observation_json),
 hex(q.observation_hash),typeof(q.observation_hash),hex(q.nonce),typeof(q.nonce),
 hex(f.facts_json),typeof(f.facts_json),hex(q.checked_at),typeof(q.checked_at))=1;

DROP TRIGGER manual_nonce_alias_step_insert_victim;
CREATE TRIGGER manual_nonce_alias_step_insert_victim BEFORE INSERT ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON hex(s.id)=hex(x.step_id) AND s.id IS NOT NULL AND x.step_id IS NOT NULL
 WHERE x.attempt_id IS NOT q.attempt_id AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce)
 AND (s.id=NEW.id OR s.rowid=NEW.rowid OR (s.operation_id=NEW.operation_id AND s.step_index=NEW.step_index)))
BEGIN SELECT RAISE(ABORT,'manual nonce alias step conflict victim protected'); END;

DROP TRIGGER manual_nonce_alias_step_delete;
CREATE TRIGGER manual_nonce_alias_step_delete BEFORE DELETE ON execution_steps
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 WHERE hex(x.step_id)=hex(OLD.id) AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce alias step retained'); END;

DROP TRIGGER manual_nonce_alias_operation_delete;
CREATE TRIGGER manual_nonce_alias_operation_delete BEFORE DELETE ON operations
WHEN EXISTS(SELECT 1 FROM manual_nonce_attempt_aliases x JOIN manual_nonce_claims q ON q.operation_id=x.operation_id
 JOIN execution_steps s ON hex(s.id)=hex(x.step_id) AND s.id IS NOT NULL AND x.step_id IS NOT NULL
 WHERE hex(s.operation_id)=hex(OLD.id) AND x.attempt_id IS NOT q.attempt_id
 AND (x.unsafe_identity=1 OR x.nonce_valid=0 OR x.nonce=q.nonce))
BEGIN SELECT RAISE(ABORT,'manual nonce alias operation retained'); END;
