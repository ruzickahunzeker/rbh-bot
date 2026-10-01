ALTER TABLE transaction_submissions ADD COLUMN send_intent_at TEXT;

-- Rows left by the already-landed W4-B implementation cannot prove that the
-- broadcaster was not invoked. Backfill them conservatively as ambiguous.
UPDATE transaction_submissions
SET send_intent_at=created_at
WHERE state='submitting';

CREATE TRIGGER transaction_submission_send_intent_immutable
BEFORE UPDATE OF send_intent_at ON transaction_submissions
WHEN OLD.send_intent_at IS NOT NULL OR NEW.send_intent_at IS NULL
BEGIN SELECT RAISE(ABORT,'submission send intent is immutable'); END;
