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
