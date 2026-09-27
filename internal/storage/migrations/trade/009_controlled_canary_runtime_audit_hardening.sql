CREATE TRIGGER canary_runtime_audit_no_update
BEFORE UPDATE ON canary_runtime_audit
BEGIN SELECT RAISE(ABORT,'canary runtime audit is append-only'); END;

CREATE TRIGGER canary_runtime_audit_no_delete
BEFORE DELETE ON canary_runtime_audit
BEGIN SELECT RAISE(ABORT,'canary runtime audit is append-only'); END;
