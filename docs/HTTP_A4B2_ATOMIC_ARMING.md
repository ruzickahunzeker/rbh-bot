# A4-B2 — approval-gated atomic arming

Status: `A4-B2_READY_FOR_REVIEW`  
Scope: durable SQL gate only; no HTTP, signer, submission, permit, broadcast or live.

Migration `047_manual_operator_arming` adds the explicit C04/C05/C07/C08
attestation overlay and replaces `manual_b1_no_arm` atomically with a guard
that accepts `PENDING -> ARMED` only when the canonical approval envelope,
Ed25519 signature, trust root, strict time window, authorization identity and
four gate attestations all match. If migration replacement fails, the old
unconditional no-arm guard remains through transaction rollback.

The existing authorization row remains the sole state owner and migration010
transition audit remains same-transaction. Direct SQL valid approval arming,
invalid approval rejection and exactly-one transition audit are covered.
No approval is provisioned in production and no authorization grant is made by
this slice.
