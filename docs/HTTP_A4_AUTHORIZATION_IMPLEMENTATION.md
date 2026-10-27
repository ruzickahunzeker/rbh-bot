# A4-A — durable operator evidence overlay

Status: `A4-A_READY_FOR_REVIEW`  
Base scope: `fdaa3ba31dff2d265b8d56d35c6cee601a5bc119`  
Historical implementation head before hardening: `4022f6e4379ea2b4d8f70536d48901bea6f163d0`.
Current reviewed hardening head: `1e3e09ba319ef095c09133cb861fbf3cb86a2d63`.
Reviewed implementation identity is fixed at code head `1e3e09ba319ef095c09133cb861fbf3cb86a2d63`; subsequent commits are docs/evidence carriers and are not code identity.

This slice implements only additive migration `046_manual_operator_authority`.
It adds append-only durable tables for operator trust-root metadata,
authorization requests, approval evidence and revocation evidence. Existing
`canary_runtime_authorizations` remains the sole state owner.

The migration deliberately does not replace or weaken migration018's
`manual_b1_no_arm`; a MANUAL authorization remains non-armable. Signature
verification, atomic approval-gated arming and trust-root bootstrap are not
enabled by A4-A and belong to the separately reviewed A4-B implementation.
No HTTP route, signer, submission, permit, broadcaster, mainnet or live path
is added.

Focused evidence proves migration 046 is applied, all four overlay tables exist,
updates/deletes are rejected, and the original no-arm trigger definition is
still present. This is schema/evidence progress only, not authorization or
production readiness.
