-- M2 residual only: UPDATE OR REPLACE may delete a different conflicting row.
-- DELETE triggers are not sufficient when recursive_triggers is OFF. Protect
-- each victim before UPDATE, independent of the connection's recursion setting.
-- All actual unique keys, including SQLite's implicit rowid, are considered;
-- the current target is excluded so unrelated legacy self-updates remain valid.
CREATE TRIGGER manual_binding_reservation_update_victim BEFORE UPDATE ON execution_reservations
WHEN EXISTS(
 SELECT 1 FROM execution_reservations r JOIN manual_execution_bindings b
 ON r.operation_id=b.operation_id OR r.step_id IN (b.execution_step_id,b.validation_step_id)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=r.step_id AND s.operation_id=b.operation_id)
 WHERE r.rowid!=OLD.rowid
 AND (r.operation_id=NEW.operation_id OR r.step_id=NEW.step_id OR r.rowid=NEW.rowid)
)
BEGIN SELECT RAISE(ABORT,'manual reservation update conflict victim protected'); END;

CREATE TRIGGER manual_binding_lane_update_victim BEFORE UPDATE ON execution_wallet_lanes
WHEN EXISTS(
 SELECT 1 FROM execution_wallet_lanes l JOIN manual_execution_bindings b
 ON l.operation_id=b.operation_id OR l.step_id IN (b.execution_step_id,b.validation_step_id)
 OR l.wallet_id=b.wallet_id OR lower(l.address)=lower(b.wallet_address)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=l.step_id AND s.operation_id=b.operation_id)
 WHERE l.rowid!=OLD.rowid
 AND (l.wallet_id=NEW.wallet_id OR l.address=NEW.address OR l.rowid=NEW.rowid)
)
BEGIN SELECT RAISE(ABORT,'manual lane update conflict victim protected'); END;

-- An explicit rowid on INSERT OR REPLACE is the same implicit-victim boundary.
-- 022 already checks declared INSERT unique keys; include the implicit key too.
CREATE TRIGGER manual_binding_reservation_insert_victim BEFORE INSERT ON execution_reservations
WHEN EXISTS(
 SELECT 1 FROM execution_reservations r JOIN manual_execution_bindings b
 ON r.operation_id=b.operation_id OR r.step_id IN (b.execution_step_id,b.validation_step_id)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=r.step_id AND s.operation_id=b.operation_id)
 WHERE r.operation_id=NEW.operation_id OR r.step_id=NEW.step_id OR r.rowid=NEW.rowid
)
BEGIN SELECT RAISE(ABORT,'manual reservation insert conflict victim protected'); END;

CREATE TRIGGER manual_binding_lane_insert_victim BEFORE INSERT ON execution_wallet_lanes
WHEN EXISTS(
 SELECT 1 FROM execution_wallet_lanes l JOIN manual_execution_bindings b
 ON l.operation_id=b.operation_id OR l.step_id IN (b.execution_step_id,b.validation_step_id)
 OR l.wallet_id=b.wallet_id OR lower(l.address)=lower(b.wallet_address)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=l.step_id AND s.operation_id=b.operation_id)
 WHERE (l.wallet_id=NEW.wallet_id OR l.address=NEW.address OR l.rowid=NEW.rowid)
 AND NOT (l.state IS 'idle' AND l.operation_id IS NULL AND l.step_id IS NULL
 AND l.reserved_nonce IS NULL AND l.freeze_reason IS NULL
 AND l.wallet_id IS b.wallet_id AND l.wallet_id IS NEW.wallet_id
 AND lower(l.address) IS lower(b.wallet_address) AND lower(l.address) IS lower(NEW.address)
 AND NEW.operation_id IS b.operation_id AND NEW.step_id IS b.execution_step_id
 AND NEW.state IS 'reserved' AND NEW.reserved_nonce IS NULL AND NEW.freeze_reason IS NULL
 AND NEW.updated_at IS b.checked_at)
)
BEGIN SELECT RAISE(ABORT,'manual lane insert conflict victim protected'); END;
