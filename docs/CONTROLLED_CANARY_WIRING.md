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
W2 = PASS
controlled_canary_wiring = W2_PASS
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
availability wording.

### W2 independent review closeout

The independent review of `main@eb8b5d79af8a03b4bcdefdb4962edf585f6cc236` concluded PASS after
PR #23 and PR #24 landed. It verified that runtime identity comes only from immutable admission
source data, the C07 active reservation binding is enforced, and the immediate-pre-send final
recheck is atomic with permit consumption. Recovery readiness means only `policy_unblocked` and
does not attest recovery-service health. GitHub CI #119 passed the complete suite, including
`go test -race ./...`.

W2 PASS does not connect a signer, `SubmissionService`, `SendRawTransaction`, production
broadcaster or mainnet. Production broadcast remains disconnected, controlled-canary
authorization remains ungranted, and live and release readiness remain false.

## W3 controlled recovery worker and lifecycle

W3 adds a controlled recovery coordination layer over the existing operation, execution step,
attempt, submission, receipt and position-effect state machine. It discovers `submitted` and
`broadcast_unknown` work from SQLite, uses the existing `RECOVERY` lease with monotonic fencing,
and invokes only fenced PR-006 receipt/canonical/reorg recovery.

For `broadcast_unknown`, the worker performs controlled query-only classification and persists
immutable evidence plus runtime audit/alert records. Propagation evidence may enter canonical
reconciliation; ambiguity or unsafe nonce evidence keeps the wallet lane and reservation frozen.
The worker never creates or consumes a send permit and has no signer, submission service or raw
broadcaster dependency.

Emergency stop, authorization revocation/expiry and application TTL do not suppress recovery of
an already propagated or ambiguous transaction. Lease loss, RPC failure and contradictory or
unverifiable evidence fail closed. Each mutation boundary is fenced, while the underlying
canonical apply, rollback and reapply ledger remains exactly-once and restart-safe.

W3 is not wired into `cmd/trade-service` or production startup. Its lifecycle is controlled/local
only and drains an in-progress durable scan before shutdown; restart rediscovers work from the
database.

### W3 post-merge hardening

The first independent review identified three residual evidence/safety gaps. The hardening slice
keeps the same recovery state machine and closes them without entering W4:

- the RECOVERY lease is now verified inside the same SQLite transaction as receipt observation,
  canonical apply and orphan rollback, removing the pre-mutation lease TOCTOU;
- every observed lease-loss path emits durable `RECOVERY_LEASE_LOST` runtime audit and alert
  evidence while leaving the durable recovery item untouched;
- an independent emergency-stop harness proves that stop blocks new economic action but does not
  block receipt/canonical recovery for an existing submitted transaction.

### W3 independent review closeout

The independent review of `main@26b56c411f96ef2e24c5d785e2c34b66fbad28df` concluded PASS after
PR #26, PR #27 and PR #28 landed. GitHub CI #132 passed. The review verified the recovery lease
commit-window evidence and the emergency-stop plus `broadcast_unknown` query-only/frozen
evidence. The worker remains unable to sign, submit, replay, allocate a nonce, consume a send
permit, call `SendRawTransaction`, reach a production broadcaster or contact mainnet.

W3 PASS records only the controlled recovery worker and lifecycle. It does not connect production
startup or broadcast, grant controlled-canary authorization, enable live, change release
readiness or begin W4.

```ini
W1 = PASS
W2 = PASS
W3 = PASS
controlled_canary_wiring = W3_PASS
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## W4-A production composition and disabled startup

W4-A installs only the production lifecycle boundary in `trade-service`. The configuration value
`RBH_CONTROLLED_CANARY_PRODUCTION_MODE` defaults to and only accepts `DISABLED`; missing config is
therefore disabled and any attempted enablement fails closed. Startup writes a durable
`PRODUCTION_WIRING_DISABLED` runtime audit and exposes three independent metrics:
`canary_admission_ready`, `submission_send_ready`, and `recovery_ready`.

All three dimensions are false in W4-A. Recovery is false because production recovery lifecycle
wiring belongs to W4-C, not because recovery authorization is coupled to admission or submission.
The base trade-service can still start safely in this disabled state.

The W4-A composition has no orchestrator, signer, `SubmissionService`, `RawBroadcaster`, submission
worker, recovery worker, permit-consumption or mainnet dependency. It cannot create an economic
action. W4-B submission/send semantics, W4-C recovery lifecycle, and W4-D fault hardening remain
out of scope.

```ini
W1 = PASS
W2 = PASS
W3 = PASS
controlled_canary_wiring = W4-A_READY_FOR_REVIEW
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```

## W4-B controlled submission worker and lease fencing

W4-B adds a controlled submission worker that discovers only durable `ISSUED` send permits. It
requires durable PASS snapshots for execution admission, pre-sign and first broadcast, then runs
the W2 immediate-pre-send artifact/runtime verification. The worker uses the existing operation,
execution step, transaction attempt, submission and permit records; it does not define a second
transaction state machine.

The final control, authorization epoch, deployment, C07 reservation, TTL and permit checks are
performed under the durable `SUBMISSION` lease. Permit consumption and creation of the
`submitting` row commit atomically. The controlled broadcaster call and durable outcome are then
serialized under a SQLite writer fence that revalidates the same mutable controls and lease. A
lease takeover or emergency-stop update therefore orders entirely before the final check or after
the send/outcome commit; it cannot commit inside that window.

Migration 012 adds the explicit `known_unsent` submission state so a deterministic rejection is
not represented as ambiguity or manual resolution. First-broadcast known-unsent releases the
provably unsent lane/reservations. Unknown replay known-unsent preserves the earlier ambiguous
lane/reservations as frozen. Migration 013 adds an immutable durable `send_intent_at` boundary.
`send_intent_at` does not assert that an RPC call occurred. It is the durable point after which a
restart can no longer prove that broadcaster invocation did not occur. A restart before that
boundary is provably unsent; every restart after it is conservatively `broadcast_unknown` and
remains frozen, including a crash after the marker commit but before the RPC call. That deliberate
false-positive ambiguity is safer than permitting a second economic action. Existing in-flight
rows are conservatively backfilled as post-intent because their historical phase cannot be proven.

Accordingly, `broadcast_unknown` means only that the system cannot durably prove broadcast did
not occur; it does not mean broadcast probably occurred. `known_unsent` is permitted only when
absence of broadcaster invocation remains durably provable across restart, or when a deterministic
rejection with explicitly trusted non-propagation semantics was durably recorded. Post-marker
recovery never automatically resends: it requires transaction/receipt/nonce query, a fresh
`UNKNOWN_REPLAY` permit, and the exact original raw artifact.

Expired `ISSUED` permits are atomically moved to terminal `EXPIRED`, with exactly-once durable
audit and alert records, before the worker scans unexpired work. Repeated scans therefore cannot
livelock on an expired permit or prevent valid work from being discovered. Barrier tests hold the
broadcaster inside the final SQLite writer fence and prove that neither higher-epoch lease takeover
nor emergency-stop mutation can commit before the send outcome commits.

Unknown replay still requires W2 query evidence and a fresh `UNKNOWN_REPLAY` permit. The worker
decrypts and verifies the original durable artifact, checks its SHA-256 identity, and sends the
exact same raw bytes. It cannot rebuild, resign, replace, fee-bump or allocate a nonce.

W4-B remains a controlled local harness and is not installed in production startup. W4-A remains
disabled, production broadcast remains disconnected, and no runtime authorization is granted.
The revised ambiguity semantics passed fresh independent review and landed before W4-C began.

```ini
W1 = PASS
W2 = PASS
W3 = PASS
W4-B = PASS
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## W4-C production recovery lifecycle

W4-C connects the existing W3 `ControlledRecoveryWorker` to the production trade-service
lifecycle without connecting submission or broadcast. Startup acquires only the durable
`RECOVERY` lease with a monotonic epoch. The worker discovers the latest `submitted` and
`broadcast_unknown` transaction submissions directly from SQLite; it has no in-memory work queue.
Lease renewal, takeover fencing, durable evidence, audits and alerts continue to use the existing
W1/W3 state and do not introduce a second recovery state machine.

The production recovery dependency graph is deliberately read-only:

```text
ProductionComposition
  -> ControlledRecoveryWorker
     -> RecoveryService(Store, ArtifactCipher, ReceiptBackend, EffectResolver)
     -> ProductionRecoveryQuerier(Store, ArtifactCipher, ReadOnlyRecoveryRPC)
```

`ReadOnlyRecoveryRPC` exposes transaction, receipt, nonce and header queries plus chain health. It
does not implement `RawBroadcaster` or expose `SendRawTransaction`. Recovery does not hold a
signer, `ExecutionKernel`, `SubmissionService`, send permit issuer/consumer, or nonce allocator.
Successful Pons v2 Curve receipts derive their position effect from the canonical curve event
bound to the durable route; missing, contradictory or unverifiable events fail closed.

`recovery_ready` is independent from admission and send readiness. It requires database health,
read-only RPC chain health and a valid `RECOVERY` lease. Emergency stop, absent/revoked/expired
authorization, deployment drift and application TTL do not policy-block recovery of existing
durable work. Admission and submission remain false. RPC/query failure or lease loss drops
recovery readiness and emits durable audit/alert evidence.

Shutdown stops new scans and drains the current fenced scan while continuing lease renewal. Work
not completed before a fault remains in SQLite and is rediscovered after lease expiry/takeover.
`broadcast_unknown` remains frozen and recovery never rebuilds, resigns, replaces, fee-bumps,
allocates a nonce, consumes a send permit or automatically resends.

### W4-C review hardening

The branch review found readiness could revive after terminal health failure, production RPC
receipt identity was not checked before recovery, and the restart test reused the open Store.
This hardening makes terminal failure and shutdown draining irreversible for a lifecycle and
rejects a second `Run`. A stopped worker always leaves `recovery_ready=false`. Healthy shutdown
continues its lease renewal while draining, without enabling admission or sending.

The read-only RPC boundary and production querier validate transaction hashes, chain identity,
receipt block/status fields and receipt-log identities before recording propagation or allowing
canonical effects. Both reverted and successful wrong-transaction receipts fail closed, preserve
the ambiguous frozen lane/reservation, and generate durable audit/alert evidence. The existing
recovery state machine and W4-B send-intent semantics are unchanged.

File-backed SQLite close/reopen tests rebuild the production composition, acquire a higher
`RECOVERY` epoch, rediscover both durable submission states, and verify exactly-once canonical
apply/reorg rollback/reapply with the exact original artifact and nonce. HTTP shutdown failure
still waits for recovery; the app does not close DB/RPC handles on an arbitrary scan timeout.
Each individual production RPC retains its ten-second timeout. A caller's timed-out `Wait` does
not terminate or release durable recovery work. This is implementation evidence, pending fresh
independent review; W4-D and W5 remain out of scope.

```ini
W1 = PASS
W2 = PASS
W3 = PASS
W4-B = PASS
controlled_canary_wiring = W4-C_READY_FOR_REVIEW
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = health-dependent
live = false
release_ready = false
W4-D = NOT_ENTERED
W5 = NOT_ENTERED
```
