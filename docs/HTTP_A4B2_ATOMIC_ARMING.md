# A4-B2 — approval-gated atomic arming

Status: `A4-B2_HARDENING_IN_PROGRESS`
Scope: durable SQL gate only; no HTTP, signer, submission, permit, broadcast or live.

Migrations `047_manual_operator_arming` and `048_manual_operator_arming_hardening`
add the explicit C04/C05/C07/C08 attestation overlay and replace
`manual_b1_no_arm` atomically with a guard
that accepts `PENDING -> ARMED` only when the canonical approval envelope,
Ed25519 signature, trust root, strict time window, authorization identity and
four gate attestations all match. If migration replacement fails, the old
unconditional no-arm guard remains through transaction rollback.

The hardening migration adds immutable bootstrap, source identity and C07
attestation witnesses, rejects non-canonical approval JSON, binds approval
columns to the signed request and requires the A3 durable clock state for the
arming window. The trust-root bootstrap witness remains an out-of-band
deployment responsibility; no HTTP or SQL grant path is enabled by this slice.

The existing authorization row remains the sole state owner and migration010
transition audit remains same-transaction. Direct SQL valid approval arming,
invalid approval rejection and exactly-one transition audit are covered by the
controlled fixture. This branch is pending fresh independent review; it is not
an authorization grant.
No approval is provisioned in production and no authorization grant is made by
this slice.
