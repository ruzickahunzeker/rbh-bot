-- Protect retained wallet/address-only pollution as well as step references.
-- No cleanup or repair: an incomplete binding must remain unavailable.
CREATE TRIGGER manual_binding_lane_retained_delete BEFORE DELETE ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE OLD.operation_id=b.operation_id OR OLD.step_id IN (b.execution_step_id,b.validation_step_id)
 OR OLD.wallet_id=b.wallet_id OR lower(OLD.address)=lower(b.wallet_address)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=OLD.step_id AND s.operation_id=b.operation_id))
BEGIN SELECT RAISE(ABORT,'manual retained lane deletion forbidden'); END;

CREATE TRIGGER manual_binding_lane_retained_update BEFORE UPDATE ON execution_wallet_lanes
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE (OLD.operation_id=b.operation_id OR OLD.step_id IN (b.execution_step_id,b.validation_step_id)
 OR OLD.wallet_id=b.wallet_id OR lower(OLD.address)=lower(b.wallet_address)
 OR EXISTS(SELECT 1 FROM execution_steps s WHERE s.id=OLD.step_id AND s.operation_id=b.operation_id))
 AND NOT (OLD.state IS 'idle' AND OLD.operation_id IS NULL AND OLD.step_id IS NULL
 AND OLD.reserved_nonce IS NULL AND OLD.freeze_reason IS NULL
 AND OLD.wallet_id IS b.wallet_id AND OLD.wallet_id IS NEW.wallet_id
 AND lower(OLD.address) IS lower(b.wallet_address) AND lower(OLD.address) IS lower(NEW.address)
 AND NEW.operation_id IS b.operation_id AND NEW.step_id IS b.execution_step_id
 AND NEW.state IS 'reserved' AND NEW.reserved_nonce IS NULL AND NEW.freeze_reason IS NULL
 AND NEW.updated_at IS b.checked_at))
BEGIN SELECT RAISE(ABORT,'manual retained lane mutation forbidden'); END;
