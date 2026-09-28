# Controlled Canary Wiring

## W1 runtime state and authorization model

W1 adds only a durable overlay on the existing
`Operation -> ExecutionStep -> TransactionAttempt` state machine. It does not add a parallel
execution state machine and does not connect a signer, `SubmissionService`,
`SendRawTransaction`, a production broadcaster or mainnet.

Runtime authorization is limited by schema to `CONTROLLED_CANARY`, chain 4663, one immutable
policy version, wallet, gate-evidence set, absolute expiry and immutable deployment identity
(`build_sha`, `release_id`, `deployment_id`, environment). Authorization epochs are allocated
monotonically. A deployment identity change makes the prior authorization unusable.

Authorization operation count and total input are reserved durably and atomically. The usage row
is separate from C07 risk accounting: it enforces only the authorization-wide caps. The unique
authorization/operation key makes duplicates idempotent, restart does not reset totals, and
unresolved or ambiguous operations retain their allocation until a later Slice applies an
explicit terminal transition.

Gate snapshots are immutable and bind the operation/attempt, authorization epoch, deployment,
policy and evidence hashes, emergency-stop revision and decision. Send permits are one-shot and
purpose-bound to either `FIRST_BROADCAST` or `UNKNOWN_REPLAY`; a replay permit additionally
requires durable query evidence. W1 only persists and tests these objects—it does not consume a
permit to send anything.

Worker leases, append-only runtime audit and an atomic alert outbox are present for later wiring.
No runtime authorization is seeded or granted by the migration.

```ini
W1 = PASS
controlled_canary_wiring = W1_PASS
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## W1 evidence hardening

Migration 009 makes `canary_runtime_audit` append-only by rejecting direct `UPDATE` and `DELETE`.
The authorization state transition and its audit insert remain in one transaction; a forced audit
insert failure rolls back the state transition.

Distinct-operation concurrency tests independently exhaust `max_operations` and
`max_total_input`. They prove zero reservation overcommit, exact equality between aggregate usage
and durable operation-usage rows, no reset after restart, no duplicate budget consumption and
zero unexplained accepts. This hardening returns W1 only to `W1_READY_FOR_REVIEW`.

Migration 010 closes the residual direct-SQL transition gap. Every legal authorization state
change now causes the schema to insert exactly one `AUTHORIZATION_STATE_CHANGED` audit in the
same SQLite statement and transaction. An audit insertion failure rolls back the state change;
invalid transitions produce no audit. The Store API relies on this schema invariant and no longer
inserts a second transition audit.

## W1 independent review closeout

The independent review at `main@df3f80a17d655ffb91d2433a1c96bcde17e252e1` concluded PASS.
PR #19, #20 and #21 are landed. The review verified the migration 008 runtime authorization,
deployment binding, gate snapshot, one-shot permit, lease, budget and audit/outbox invariants;
the migration 009 append-only audit invariant; and the migration 010 schema-enforced transition
audit invariant. The direct-SQL transition residual is closed.

W1 PASS records only the durable runtime state and authorization model. The production
broadcaster remains disconnected, controlled-canary authorization remains ungranted, and live
and release readiness remain false.

## W2 controlled orchestrator and multi-stage gates

W2 adds a controlled-only orchestrator over the existing operation, execution step, transaction
attempt, authorization and permit records. It evaluates execution admission, pre-sign,
first-broadcast, immediate-pre-send and unknown-replay gates. It has no signer,
`SubmissionService`, broadcaster or RPC dependency and returns no raw transaction bytes.

Every gate revalidates emergency stop, authorization epoch and expiry, immutable deployment
identity, chain, wallet, policy, C07 admission binding and application TTL. Artifact stages also
require a controlled exact-artifact verifier and a controlled contract runtime-identity reader.
Immediate pre-send rereads all mutable controls before atomically consuming its one-shot permit.

Unknown replay is available only from `broadcast_unknown`. It first performs controlled
transaction, receipt and nonce queries. A propagated/consumed result remains query/reconciliation
only; only an unresolved reserved nonce can receive a new `UNKNOWN_REPLAY` permit bound to the
original artifact identity. `FIRST_BROADCAST` permits cannot be reused for replay.

Gate snapshots and permit issue/consumption commit atomically. A known-unsent gate rejection does
not create `broadcast_unknown`. Emergency stop, authorization revocation/expiry, deployment drift
and TTL failure stop new admission/sign/send/replay while recovery query and canonical/reorg
reconciliation remain available.

```ini
W1 = PASS
controlled_canary_wiring = W2_READY_FOR_REVIEW
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

### W2 post-merge hardening

The post-merge independent review found four residual safety gaps. This hardening closes them
without entering W3:

- runtime address and code hash come only from immutable `canary_admission_sources`;
- each stage binds `ADMITTED` to the matching C07 reservation and accepts only `reserved` or
  `frozen`; `committed` and `released` fail closed;
- immediate pre-send repeats stop, authorization, deployment, policy, TTL and reservation checks
  in the same transaction that consumes the permit;
- readiness reports only that canary policy leaves recovery query/reconciliation unblocked. It
  does not attest that the PR-006 recovery service or dependencies are healthy.

The original W2 evidence remains historical. The hardening evidence supersedes its recovery
availability wording. Status remains `W2_READY_FOR_REVIEW` pending fresh independent review.
