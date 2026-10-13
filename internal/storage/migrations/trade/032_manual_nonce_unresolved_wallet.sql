-- M3-B2 first-claim exclusivity only. Preserve 001..031 and historical
-- false-permission verification. Attempt-free steps must not disappear merely
-- because a wallet or parent reference uses BLOB storage. Encoded joins are
-- denial-only; this view never repairs, adopts, or grants permission.
CREATE VIEW manual_nonce_unresolved_wallet_steps AS
SELECT b.operation_id,s.rowid AS step_rowid,s.id AS step_id
FROM manual_execution_bindings b CROSS JOIN execution_steps s
LEFT JOIN operations o ON hex(o.id)=hex(s.operation_id)
 AND o.id IS NOT NULL AND s.operation_id IS NOT NULL
WHERE (s.id IS NOT b.execution_step_id OR typeof(s.id)!='text')
 AND (manual_nonce_identity_relation(hex(s.wallet_id),typeof(s.wallet_id),hex(b.wallet_id),0)>0
 OR manual_nonce_identity_relation(hex(o.wallet_id),typeof(o.wallet_id),hex(b.wallet_id),0)>0
 OR manual_nonce_identity_relation(hex(s.operation_id),typeof(s.operation_id),hex(b.operation_id),0)>0)
 AND (typeof(s.status)!='text' OR instr(s.status,char(0))>0
 OR s.status IN ('manual_execution_bound','nonce_reserved','signing','signed','submitted','broadcast_unknown','reconciling','manual_resolution'));

CREATE TRIGGER manual_nonce_unresolved_wallet_source BEFORE INSERT ON manual_nonce_claims
WHEN EXISTS(SELECT 1 FROM manual_nonce_unresolved_wallet_steps WHERE operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual nonce unresolved wallet work'); END;

-- Recheck before publishing statement success even if other BEFORE triggers
-- changed legacy metadata after the source check. All provisional claim
-- effects still roll back together on rejection.
CREATE TRIGGER manual_nonce_unresolved_wallet_final AFTER INSERT ON manual_nonce_claims
WHEN EXISTS(SELECT 1 FROM manual_nonce_unresolved_wallet_steps WHERE operation_id=NEW.operation_id)
BEGIN SELECT RAISE(ABORT,'manual nonce unresolved wallet work'); END;
