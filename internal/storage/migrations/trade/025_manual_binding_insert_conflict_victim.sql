-- M2 immutable binding overlay only. INSERT OR REPLACE can delete a rowid
-- conflict victim without DELETE triggers when recursive_triggers is OFF.
-- Check every declared unique key plus physical row identity before insertion.
-- Do not rewrite landed migrations, retained history, or the existing writer.
CREATE TRIGGER manual_binding_insert_conflict_victim BEFORE INSERT ON manual_execution_bindings
WHEN EXISTS(SELECT 1 FROM manual_execution_bindings b
 WHERE b.operation_id=NEW.operation_id OR b.request_id=NEW.request_id
 OR b.validation_step_id=NEW.validation_step_id OR b.execution_step_id=NEW.execution_step_id
 OR b.rowid=NEW.rowid)
BEGIN SELECT RAISE(ABORT,'manual binding insert conflict victim protected'); END;
