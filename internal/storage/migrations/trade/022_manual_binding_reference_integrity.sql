-- M2 reference hardening only. Keep 001..021 and the original accounting owner.
-- Separate foreign keys do not prove operation/step/wallet ownership.
CREATE TRIGGER manual_binding_reservation_reference_insert BEFORE INSERT ON execution_reservations
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE b.operation_id=NEW.operation_id OR b.execution_step_id=NEW.step_id OR b.validation_step_id=NEW.step_id
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=NEW.step_id AND s.operation_id=b.operation_id))
BEGIN SELECT RAISE(ABORT,'manual financial reservation reference forbidden'); END;
CREATE TRIGGER manual_binding_reservation_reference_update BEFORE UPDATE ON execution_reservations
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE b.operation_id=NEW.operation_id OR b.operation_id=OLD.operation_id
 OR b.execution_step_id IN (NEW.step_id,OLD.step_id) OR b.validation_step_id IN (NEW.step_id,OLD.step_id)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id IN (NEW.step_id,OLD.step_id) AND s.operation_id=b.operation_id))
BEGIN SELECT RAISE(ABORT,'manual financial reservation reference forbidden'); END;
CREATE TRIGGER manual_binding_reservation_reference_replace BEFORE INSERT ON execution_reservations
WHEN EXISTS(SELECT 1 FROM execution_reservations r JOIN manual_execution_bindings b
 ON b.operation_id=r.operation_id OR b.execution_step_id=r.step_id OR b.validation_step_id=r.step_id
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=r.step_id AND s.operation_id=b.operation_id)
 WHERE r.operation_id=NEW.operation_id OR r.step_id=NEW.step_id)
BEGIN SELECT RAISE(ABORT,'manual corrupt reservation replacement forbidden'); END;
CREATE TRIGGER manual_binding_reservation_reference_delete BEFORE DELETE ON execution_reservations
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE b.operation_id=OLD.operation_id OR b.execution_step_id=OLD.step_id OR b.validation_step_id=OLD.step_id
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=OLD.step_id AND s.operation_id=b.operation_id))
BEGIN SELECT RAISE(ABORT,'manual corrupt reservation requires explicit resolution'); END;

-- The only permitted MANUAL lane is the nonce-free exact binding claim.
-- This also preserves the initial idle-lane UPSERT performed by 021.
CREATE TRIGGER manual_binding_lane_reference_insert BEFORE INSERT ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE (b.operation_id=NEW.operation_id OR b.execution_step_id=NEW.step_id OR b.validation_step_id=NEW.step_id
 OR b.wallet_id=NEW.wallet_id OR lower(b.wallet_address)=lower(NEW.address)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=NEW.step_id AND s.operation_id=b.operation_id))
 AND NOT (NEW.operation_id IS b.operation_id AND NEW.step_id IS b.execution_step_id
 AND NEW.wallet_id IS b.wallet_id AND lower(NEW.address) IS lower(b.wallet_address)
 AND NEW.state IS 'reserved' AND NEW.reserved_nonce IS NULL AND NEW.freeze_reason IS NULL AND NEW.updated_at IS b.checked_at))
BEGIN SELECT RAISE(ABORT,'manual lane reference identity mismatch'); END;
CREATE TRIGGER manual_binding_lane_reference_update BEFORE UPDATE ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE (b.operation_id=NEW.operation_id OR b.execution_step_id=NEW.step_id OR b.validation_step_id=NEW.step_id
 OR b.wallet_id=NEW.wallet_id OR lower(b.wallet_address)=lower(NEW.address)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=NEW.step_id AND s.operation_id=b.operation_id))
 AND NOT (NEW.operation_id IS b.operation_id AND NEW.step_id IS b.execution_step_id
 AND NEW.wallet_id IS b.wallet_id AND lower(NEW.address) IS lower(b.wallet_address)
 AND NEW.state IS 'reserved' AND NEW.reserved_nonce IS NULL AND NEW.freeze_reason IS NULL AND NEW.updated_at IS b.checked_at))
 OR EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE b.operation_id=OLD.operation_id OR b.execution_step_id=OLD.step_id OR b.validation_step_id=OLD.step_id
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=OLD.step_id AND s.operation_id=b.operation_id))
BEGIN SELECT RAISE(ABORT,'manual lane reference mutation forbidden'); END;
CREATE TRIGGER manual_binding_lane_reference_replace BEFORE INSERT ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM execution_wallet_lanes l JOIN manual_execution_bindings b
 ON b.operation_id=l.operation_id OR b.execution_step_id=l.step_id OR b.validation_step_id=l.step_id
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=l.step_id AND s.operation_id=b.operation_id)
 WHERE l.wallet_id=NEW.wallet_id OR lower(l.address)=lower(NEW.address))
BEGIN SELECT RAISE(ABORT,'manual lane reference replacement forbidden'); END;
CREATE TRIGGER manual_binding_lane_reference_delete BEFORE DELETE ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE b.operation_id=OLD.operation_id OR b.execution_step_id=OLD.step_id OR b.validation_step_id=OLD.step_id
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=OLD.step_id AND s.operation_id=b.operation_id))
BEGIN SELECT RAISE(ABORT,'manual lane reference requires explicit resolution'); END;

-- Do not adopt a preexisting legacy reference while creating the binding,
-- including released reservations or aliases on another wallet. Direct SQL and
-- the Go writer both get this check before any binding effects are produced.
CREATE TRIGGER manual_binding_reference_no_adoption BEFORE INSERT ON manual_execution_bindings
WHEN EXISTS(SELECT 1 FROM execution_reservations r
 WHERE r.operation_id=NEW.operation_id OR r.step_id IN (NEW.execution_step_id,NEW.validation_step_id)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=r.step_id AND s.operation_id=NEW.operation_id))
 OR EXISTS(SELECT 1 FROM execution_wallet_lanes l
 WHERE l.operation_id=NEW.operation_id OR l.step_id IN (NEW.execution_step_id,NEW.validation_step_id)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=l.step_id AND s.operation_id=NEW.operation_id))
BEGIN SELECT RAISE(ABORT,'manual binding preexisting reference forbidden'); END;

-- Existing 021 pollution is not repaired. Positive joins alone are insufficient.
CREATE VIEW manual_execution_binding_integrity AS
SELECT b.* FROM manual_execution_binding_complete b
WHERE NOT EXISTS(SELECT 1 FROM execution_reservations r
 WHERE r.operation_id=b.operation_id OR r.step_id IN (b.execution_step_id,b.validation_step_id)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=r.step_id AND s.operation_id=b.operation_id))
AND NOT EXISTS(SELECT 1 FROM execution_wallet_lanes l
 WHERE (l.operation_id=b.operation_id OR l.step_id IN (b.execution_step_id,b.validation_step_id)
 OR l.wallet_id=b.wallet_id OR lower(l.address)=lower(b.wallet_address)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=l.step_id AND s.operation_id=b.operation_id))
 AND NOT (l.operation_id IS b.operation_id AND l.step_id IS b.execution_step_id
 AND l.wallet_id IS b.wallet_id AND lower(l.address) IS lower(b.wallet_address)
 AND l.state IS 'reserved' AND l.reserved_nonce IS NULL AND l.freeze_reason IS NULL AND l.updated_at IS b.checked_at));
