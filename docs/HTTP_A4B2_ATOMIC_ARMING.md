# A4-B2 — approval-gated atomic arming

Status: `A4-B2_HARDENING_IN_PROGRESS`
Scope: durable SQL gate only; no HTTP, signer, submission, permit, broadcast or live.

Migrations `047_manual_operator_arming`, `048_manual_operator_arming_hardening`,
`049_manual_operator_arming_proof` and additive `050_manual_operator_arming_upgrade_guard`
add the explicit C04/C05/C07/C08 attestation overlay and replace
`manual_b1_no_arm` atomically with a guard
that accepts `PENDING -> ARMED` only when the canonical approval envelope,
Ed25519 signature, trust root, strict time window, authorization identity and
four gate attestations all match. If migration replacement fails, the old
unconditional no-arm guard remains through transaction rollback.

The hardening migrations add immutable bootstrap, source identity and C07
attestation witnesses, reject non-canonical approval JSON, bind approval
columns to the signed request, require a deployment-secret proof for bootstrap
and C07 witnesses, and require the A3 durable clock state for the arming
window. Migration 050 revalidates pre-existing rows at the arming boundary,
rejects legacy proof-less roots/attestations, rejects duplicate nonces and
cross-authorization source adoption, and records the prior guard definition.
The upgrade fixture also proves a tampered prior guard causes migration 050 to
roll back without advancing the schema journal.
No HTTP or SQL grant path is enabled by this slice.
The ordered aggregate hash and full negative/concurrency evidence remain
review gates for this branch.

The existing authorization row remains the sole state owner and migration010
transition audit remains same-transaction. Direct SQL valid approval arming,
unauthorized rejection, upgrade-guard provenance and exactly-one transition
audit are covered by the controlled fixture. This branch is pending fresh
independent review; it is not an authorization grant.
No approval is provisioned in production and no authorization grant is made by
this slice.
