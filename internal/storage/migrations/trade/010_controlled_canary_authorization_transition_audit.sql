CREATE TRIGGER canary_runtime_authorization_transition_audit
AFTER UPDATE OF state ON canary_runtime_authorizations
WHEN NEW.state != OLD.state
BEGIN
  INSERT INTO canary_runtime_audit(
    id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,
    reason_code,details_json,created_at
  ) VALUES(
    lower(hex(randomblob(32))),
    'AUTHORIZATION_STATE_CHANGED',
    NULL,
    NULL,
    NEW.id,
    NEW.epoch,
    NEW.state,
    '{}',
    NEW.updated_at
  );
END;
