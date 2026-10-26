# HTTP A/B/C recovery and verified history integration

Continuation base: `ed0d57a6c5c081d54b32b4193ad837d99ed52625`.
Coordinator branch: `http-abc-local-integration`.
Status: implementation/validation in progress, pending fresh independent review.
This is not aggregate M4 PASS, production HTTP execution or live readiness.
Original A/B/C branches and historical evidence are retained.

## Implementation batch

The coordinator owns exact historical identity, shared POST history copies,
event pagination and integration acceptance. B owns additive migration 045 and
MANUAL recovery/settlement; C owns presentation/OpenAPI and HTTP acceptance.
Migrations 001 through 044 remain unchanged. Existing Operation -> ExecutionStep
-> TransactionAttempt, submissions, receipts and position effects remain the
outcome owners. Recovery commands and journals are witnesses, not a second
transaction state machine.

Reads follow immutable request -> execution binding -> nonce claim -> artifact
-> sole first-submission binding, not arbitrary latest rows. Missing/conflicting
provenance with dependent work fails closed. Principal/wallet/address mismatches
do not disclose history. HTTP verifies metadata/provenance without holding a
cipher or claiming cryptographic plaintext verification; recovery independently
authenticates the exact signed bytes with their full original MANUAL AAD.

Top-level status/reason and progress retain intake/admission semantics. The
MANUAL operation stays in its historical admission status; its exact execution
step, receipt and effect express the transaction lifecycle. A signed hash is not
propagation and submitted is not canonical success. Default-off duplicate POST
reads current verified history without executing A. Admission-enabled POST
retains its existing stop-first guard. Historical GET/recovery do not require
current authorization, emergency-stop clearance, application TTL or deployment
permission.

## Recovery and typed accounting

045 permits only exact-source OBSERVE/CANONICAL/ORPHAN transitions with an
independent RECOVERY lease. Economic mutation, typed settlement, audit, alert and
lifecycle event commit together. Open scopes cannot commit; direct SQL cannot
mutate protected economic rows outside the matching command scope. Lease
ownership is checked under the writer lock and rechecked before commit.

Canonical success commits risk and consumes operation usage. Canonical revert
releases risk but consumes usage: its gas/nonce are spent. Reorg freezes risk
and usage; reapply commits/consumes them again. Lifetime authorization count and
input never decrease or consume twice. MANUAL units never enter legacy
execution-reservation/window accounting.

Only one receipt for an attempt may be canonical. A new block requires the old
canonical receipt to be orphaned first. The same receipt identity has immutable
canonical effect facts, even across orphan/reapply. A different block may have a
different verified buy output. The production resolver verifies durable wallet,
curve, direction, input amount and minimum output before committing an effect.

Pending submitted work retains signed/reserved ownership. Unknown, manual
resolution and retained known-unsent outcomes remain frozen. Unknown means
durable inability to prove no invocation, not proof of propagation. Stop,
expiry, restart and query failure cannot create another economic action or
automatically release ambiguity. Known-unsent release is not added here.

## Conservative wallet reuse boundary

This slice does not support a second MANUAL admission for a wallet with a
retained nonce-claimed/signed operation, including after canonical settlement.
Wallet ID and normalized address both identify this boundary. Historical reads,
duplicate history and recovery remain available; wallet availability must not
advertise that such retained work authorizes another execution.

This restriction prevents a subsequent active reservation from conflicting with
an older receipt's reorg under C07's single-active-reservation invariant. It
does not remove that invariant, refund lifetime usage, introduce a second risk
ledger or automatically cancel a newer order. Reusable-wallet/multiple-operation
accounting requires a separate design and review. A preexisting conflicting
store must fail closed rather than be silently repaired or released.

## Durable events

`manual_lifecycle_events` is append-only and per-request. Upgrade backfills old
audits once, preserving the original intake cursor. Later events append in the
source mutation transaction, never by read-time cross-table timestamp/rowid
merging. MANUAL scope is `MANUAL_ORDER_LIFECYCLE`; rejected requests retain
`API_REJECTION_ONLY`. A page returns at most 100 events strictly after `after`.
The next cursor is the last returned event; an empty page keeps the input
cursor. Corrupt audit provenance and journal gaps fail closed rather than skip
unseen work. File-backed reopen preserves assigned cursors.

## Validation and review ledger

The initial focused integration run failed only C's assumption that submitted
must be frozen; A correctly retains signed/reserved pending ownership. The test
was corrected without changing that production behavior. Recheck on candidate
`874a9906f2486b47007691c384d7d214de2d1094` passed HTTP identity/presentation,
real Pons recovery, journal and migration acceptance (tradeapi 65.717s, storage
1.483s). This is focused evidence, not a full repository result.

Independent review of that candidate was BLOCKED by two reproduced defects:
contradictory simultaneous canonical receipts could release risk with an active
effect; unchanged receipt identity with changed output could masquerade as an
idempotent success. `69ec578559ab846c21bd28b1278033bbbfa0ffbd` integrates fixes
and permanent regressions plus final precommit lease revalidation. A fresh
independent overlay confirmed those two attacks now reject (13.565s). The batch
still awaits complete wallet-reuse restriction acceptance and final review;
this partial confirmation is not PASS for B/C as a whole.

Final command/head attribution and retained failed runs belong in the separate
batch evidence. Historical evidence is not rewritten. No remote CI result is
inferred from local tests. CI selection adds recovery/HTTP race coverage without
removing the existing groups.

## Remaining production boundary

POST still performs intake/validation/admission only; the private local A
executor has no production HTTP caller/transport. History and local recovery do
not make production trading complete. Later execution composition requires its
own frozen authority/dependency scope and review. No production broadcaster,
signer, nonce allocator, replay executor, new chain/protocol or authorization
grant is introduced by this batch.

Production broadcast=NOT_CONNECTED; authorization=NOT_GRANTED; live=false;
release_ready=false. No remote push/PR/merge or real-chain transaction occurs.
