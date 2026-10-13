-- M3-B2 only: validate the retained CLAIMED phase, not generic admission
-- history. Preserve 001..030 and shared M2/C3 history/accounting semantics.
CREATE VIEW manual_nonce_claim_030_complete AS
SELECT q.* FROM manual_nonce_claim_028_complete q
JOIN manual_nonce_binding_facts f ON f.operation_id=q.operation_id
WHERE manual_nonce_observation_bytes_valid(hex(q.observation_json),typeof(q.observation_json),
 hex(q.observation_hash),typeof(q.observation_hash),hex(q.nonce),typeof(q.nonce),
 hex(f.facts_json),typeof(f.facts_json),hex(q.checked_at),typeof(q.checked_at))=1;
DROP VIEW manual_nonce_claim_complete;
CREATE VIEW manual_nonce_claim_complete AS
SELECT q.* FROM manual_nonce_claim_030_complete q
JOIN manual_execution_bindings b ON b.operation_id=q.operation_id
JOIN canary_risk_reservations r ON r.id=q.reservation_id AND r.operation_id=q.operation_id AND r.wallet_id=q.wallet_id
JOIN canary_authorization_operation_usage u ON u.operation_id=q.operation_id AND u.authorization_id=b.authorization_id
WHERE typeof(r.state)='text' AND r.state IN ('reserved','frozen')
 AND typeof(u.state)='text' AND u.state IN ('RESERVED','FROZEN');
