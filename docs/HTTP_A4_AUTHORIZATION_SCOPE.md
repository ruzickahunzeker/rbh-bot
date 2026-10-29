# A4 — operator-only durable MANUAL authorization scope

Status: `A4_AUTHORIZATION_SCOPE_READY_FOR_REVIEW`  
Base: A3 closeout `0281d4fa91fe83d2677a94dc03dd4ca0d2d19f15`  
Implementation: NOT STARTED. This document is a scope contract, not an
authorization grant or production activation.

## Objective and hard boundary

A4 defines the operator authority required before a future production MANUAL
execution composition can arm an existing authorization row. It does not add a
HTTP grant endpoint, signer, custody loader, submission worker, permit
consumption, broadcaster, mainnet call or live enablement.

The existing `canary_runtime_authorizations` row remains the only authorization
state machine and continues to own `PENDING`, `ARMED`, `REVOKED`, `EXPIRED` and
`EXHAUSTED`. A4 adds immutable operator evidence that is required by the
existing row's guarded transition; it does not introduce a second status model.
Until the complete A4 protocol is implemented and independently approved,
`manual_b1_no_arm` remains effective and every MANUAL authorization remains
non-armable.

## Operator authority contract

An approval is an operator-signed canonical envelope, verified against an
out-of-band provisioned operator trust root. The first implementation contract
uses Ed25519 public keys identified by a durable `operator_key_id`; key
provisioning, rotation and revocation are deployment operations and are not
performed by HTTP or SQLite configuration. A config digest, readiness boolean,
API credential or caller-supplied security field is never authority.

The canonical envelope contains exactly:

```text
approval_id, request_id, operator_key_id, issued_at, expires_at,
authorization_id, authorization_epoch, build_sha, release_id, deployment_id,
environment, chain_id, wallet_id, wallet_address, policy_version,
policy_hash, gate_evidence_set_hash, max_operations, max_total_input,
source_hash, nonce, schema_version
```

The signature covers the canonical envelope and domain separator
`RBH-MANUAL-A4-AUTHORIZATION-V1`. Verification requires a trusted key,
canonical encoding, valid time window, unique approval id/nonce, and exact
durable identity matches. Approval replay, changed payload, key revocation,
epoch mismatch or deployment drift fails closed.

## Additive migration 046 design (not implemented)

The future implementation may add migration `046_manual_operator_authority`.
It must be additive and transactionally install:

- an immutable operator trust-root/key registry with key id, public key,
  validity/revocation evidence and audit binding;
- append-only `manual_authorization_requests`, keyed by request id and bound to
  one existing authorization id/epoch and the full canonical identity tuple;
- append-only `manual_authorization_approvals`, storing the canonical envelope
  hash, signature, operator key id and exact binding witnesses;
- append-only revoke/expiry evidence where the existing authorization row
  remains the state owner;
- direct-SQL triggers/views that permit `PENDING -> ARMED` only when exactly one
  valid approval matches the existing row, and reject all other bypasses.

No table in 046 may define a parallel `state` column for authorization. The
existing row transition and its migration010 durable audit must remain the
single transition and audit event. Approval insertion, transition and audit
must commit together; any audit/evidence failure rolls back the transition.
The migration must preserve the no-arm behavior until a valid approval exists,
and must not silently remove or disable the guard.

### Guard replacement is explicit and atomic

Because migration018's `manual_b1_no_arm` is an unconditional abort trigger,
046 cannot merely add a second allowing trigger. In one SQLite migration
transaction, 046 must verify the exact existing trigger definition, drop only
that named trigger, and install a replacement with the same deny-by-default
behavior plus one additional predicate: a complete, valid, non-replayed
operator approval matching the transition row and all bindings above. If any
precondition or replacement step fails, the migration rolls back and the old
guard remains. The replacement must be covered by direct-SQL tests for both
unauthorized and valid transitions; no window may exist in which a MANUAL row
can arm without the approval predicate.

The no-budget, no-permit and no-PASS guards are not replaced by 046. Any
future change to those guards requires a separately reviewed schema slice and
must remain downstream of the same approval evidence.

## Trust-root bootstrap and time source

The operator trust root is provisioned out-of-band as part of the authenticated
deployment identity. 046 stores only the pinned root/key fingerprints and
signed rotation evidence; it does not accept arbitrary SQL key insertion. The
composition layer must verify the bootstrap attestation before opening the
operator-writer capability, and append-only SQL triggers reject update/delete
or unbound key rows. Direct SQL cannot create, rotate or revoke a trusted key
without a valid signed root/rotation envelope and the exact durable audit.

Approval `issued_at`/`expires_at` and revocation checks use the already approved
bounded A3 time-source contract. `time.Now`, a request timestamp, SQLite text
ordering or an unverified provider is not acceptable. Missing, reversed,
uncertain or expired time evidence rejects the transition.

## Gate attestation binding

The approval envelope carries an ordered four-gate attestation set, not only an
opaque aggregate. It contains the exact immutable evidence identity for C04,
C05, C07 and C08, including each gate's reviewed head/evidence hash and
attested time. The current C04/C05/C08 `canary_gate_attestations` rows remain
authoritative; C07's durable admission evidence must be added as an explicit
046-bound attestation record (or a separately reviewed additive C07 source),
with no inferred C07 PASS from a configuration flag. The aggregate
`gate_evidence_set_hash` is the canonical hash of this ordered set and is
accepted only after every individual binding verifies.

## Required bindings and fail-closed rules

An approval is valid only if it matches the durable authorization row and
current attestations for build SHA, release/deployment identity, environment,
chain, wallet/address, policy version/hash, C04/C05/C07/C08 gate evidence,
source hash, authorization epoch and both durable budget caps. The current
emergency-stop and readiness controls are independent denials; readiness never
grants authority.

Any missing/corrupt/unverifiable request, key, signature, timestamp, witness,
audit row or binding rejects arming. Restart does not reset nonce/approval
uniqueness, epoch or budget. Revocation and expiry use the existing durable
authorization states and are monotonic. Recovery/query/reconciliation remains
available without an active approval and never consumes operator evidence.

## Acceptance matrix for future A4 implementation

- direct SQL `PENDING -> ARMED` without valid approval: rejected, zero audit;
- valid Store/API transition: exactly one transition audit and one approval
  binding, same transaction;
- malformed signature, unknown/revoked key, replayed nonce or changed payload:
  rejected, no state mutation;
- identity/gate/policy/epoch/deployment drift: fail closed;
- approval/audit insert failure: whole transition rollback;
- concurrent approvals/transitions: one durable winner, no duplicate arm;
- restart preserves approval identity, epoch, caps and revocation/expiry;
- emergency stop and recovery failures deny new authority but do not block
  existing recovery query/reconciliation;
- `manual_b1_no_arm` and no-budget/no-permit/no-PASS guards remain effective
  until the valid approval path is atomically proven;
- signer calls, submission calls, raw sends, permit consumption and mainnet
  calls remain zero for the A4 scope implementation.

## Explicit non-deliverables

No HTTP/operator approval route, public API field, custody or manifest loader,
production signer, submission/broadcast wiring, permit issue/consume, nonce
allocation, recovery state-machine change, schema migration execution, live
authorization, mainnet call or release-readiness change is included here.

Highest permitted status after scope review:

```ini
A3 = PASS
A4 scope = A4_AUTHORIZATION_SCOPE_READY_FOR_REVIEW
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```
