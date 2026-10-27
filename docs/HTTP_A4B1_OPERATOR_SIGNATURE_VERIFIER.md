# A4-B1 — operator approval signature verifier

Status: `A4-B1 = PASS`, formal closeout `COMPLETE`  
Scope: verification only; no arming, grant, HTTP, signer or send.

A4-B1 independent implementation review passed on code head
`1d7ba07ede12717ab9c1d0308abcb54cfb438eee`. It adds a canonical `OperatorApprovalEnvelope` and Ed25519 verification
using domain `RBH-MANUAL-A4-AUTHORIZATION-V1`. It validates canonical timestamps,
chain 4663, schema version, hex identities, approval window and exact signed
payload. `VerifyOperatorApprovalAt` rejects issued-before and expiry-equality
boundaries. Verification is read-only and cannot mutate authorization state.

The existing migration046 overlay and migration018 `manual_b1_no_arm` remain
unchanged. Durable approval-gated arming, SQL trigger integration and operator
trust-root bootstrap are deferred to A4-B2 after independent review.
