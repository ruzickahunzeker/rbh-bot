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
controlled_canary_wiring = W1_READY_FOR_REVIEW
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
