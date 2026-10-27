# A4-B2 — approval-gated atomic arming

Status: `A4-B2 = PASS`; formal closeout prepared on `a4b2-atomic-arming-closeout`
Scope: durable SQL gate only; no HTTP, signer, submission, permit, broadcast or live.

Migrations `047_manual_operator_arming`, `048_manual_operator_arming_hardening`
and `049_manual_operator_arming_proof`
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
window. No HTTP or SQL grant path is enabled by this slice.
The ordered aggregate hash and full negative/concurrency evidence were
reviewed on HEAD `8022c00fe2421aff91899e9cbb2d51acdf6b49fc`.

The existing authorization row remains the sole state owner and migration010
transition audit remains same-transaction. Direct SQL valid approval arming,
invalid approval rejection and exactly-one transition audit are covered by the
controlled fixture. Fresh independent review is PASS and the full CI suite is
green. This closeout records review only; it is not an authorization grant.
No approval is provisioned in production and no authorization grant is made by
this slice.

Formal closeout record:

- implementation head: `4e80965f024269a6c97382f0a277ad31704e9dd4`
- reviewed head: `8022c00fe2421aff91899e9cbb2d51acdf6b49fc`
- independent review: `PASS`
- CI: full Go tests, race, vet, staticcheck, formatting and validation package `PASS`
- production broadcast: `NOT_CONNECTED`
- controlled canary authorization: `NOT_GRANTED`
- live: `false`
- release readiness: `false`
- A5: `NOT_STARTED`
