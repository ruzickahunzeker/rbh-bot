# A3 scope addendum — signing clock transactions and failure cleanup

Base/design reviewed by the author: `43698ac338e93218b8e39c58ab5b892b3bcace7a`.
Status: `A3_TRUST_COMPOSITION_SCOPE_READY_FOR_REVIEW`.
Implementation: NOT_STARTED. Independent scope review: NOT_PERFORMED.

This supplements `HTTP_A3_TRUST_COMPOSITION_SCOPE.md` without rewriting that
document or its historical evidence. For the A3-B clock transaction/latch
contract, this addendum supplies the previously unspecified rules. All original
prohibitions remain. These are proposed implementation requirements, not proof
the current signing writer already meets them or acceptance of this scope.

## Existing owners and permitted change surface

Reuse migration039 `manual_gate_clock_events` / derived complete state and the
original generation, scope, audit and alert triggers. No migration or second
clock ledger. Clock history remains chain4663/environment metadata, not signing
permission or a new economic status. Do not relabel the existing submission
clock audit event as a per-operation signing audit; original intent/artifact
commands continue to own their economic audit/effects.

Only the proposed private pre-sign trust helper and original signing writer
may coordinate these checks. Helpers working within a transaction receive the
existing query/connection: no nested Begin/Commit or Store/DB connection fetch.
The existing `latchManualRuntimeClock` opens its own transaction and MUST NOT
be called while this invocation holds any read transaction or SQL connection.
Storage uses MaxOpenConns(1); a nested call can wait on its own connection.

The private immutable trust bundle may own one shared atomic unhealthy latch
for its action dependencies. This is a fail-closed process capability flag,
not a durable work queue, state machine or permission. All value/reference
copies must share it. It cannot block independent recovery or history reads.

## Healthy floor: two original transactions, no new invocation ticket

The constructor performs no clock bootstrap/write. Controlled fixture setup
explicitly initializes generation1 using the existing bootstrap before target
actions. Missing/corrupt/incomplete/latest-faulted history is a denial, never
an instruction to reinitialize, clear or fall back to an older generation.

For each action phase use the current complete clock row on THAT writer and
a fresh bounded sample. The stored high-water is canonical sample.wall, as
in the existing runtime verifier; it is not lower/upper UTC certainty or a
cross-process monotonic tick. Apply the original wall-floor rule plus separate
lower/upper causality/expiry checks. Provider/session/ticks are checked per
invocation; cross-restart guarantees come from committed wall floor/latch and
fresh trusted input, not persisted monotonic ticks that do not exist.

| Original boundary | Required clock operation on the SAME connection/transaction |
| --- | --- |
| Initial coherent read and external nonce observation | Read/check floor only; no floor advance or economic action. Release the read transaction before external work. A read snapshot is not permission. |
| Transaction 1: durable signing-intent marker | Re-read current complete healthy generation, stop/current identity/owner/source and bounded time. Insert original intent as before; after final phase checks append exactly one healthy event generation+1, high_water_at=accepted sample.wall. Intent, clock event, their original audit/outbox effects COMMIT together or all roll back. Only successful original marker COMMIT preserves this stack's existing continuation. |
| Gap after marker COMMIT | Keep the original owner only; no signing yet. A crash, cancellation, second Begin failure or gate rejection retains the committed intent and its floor. It never permits another signing stack after restart. |
| Transaction 2: final invocation fence and artifact outcome | Re-read the LATEST complete healthy clock row under BEGIN IMMEDIATE; do not assume it is transaction1's generation because another legitimate metadata writer could have advanced it in the gap. Recheck original intent owner and bounded controls/identity/time immediately before synchronous signing, after signing, and at the existing artifact final-COMMIT check. Append exactly one healthy event for the latest generation+1, using the final accepted sample.wall, in the original artifact transaction. Artifact, clock event, audit/outbox effects COMMIT together or all roll back. |
| Historical intent/artifact lookup | Original verified observation only, no signer and no healthy floor write; it never reconstructs invocation ownership. No action factory or cache is added. |

Check generation overflow before incrementing. A competing floor advance in
the gap is not corruption: reject only if latest state/sample/gates cannot be
verified, not by silently replacing an expected owner/profile. No automatic
retry of the signing invocation or fresh ownership is introduced.

Clock events and their exact witnesses use the existing INSERT helper/triggers;
no manual duplicate audit INSERT. Audit/outbox failure at either healthy event
rolls back that original transaction. A failed/uncertain marker COMMIT never
authorizes SignTransaction. A failed/uncertain artifact COMMIT never causes
resigning: independently query retained history, without inventing success or
asserting rollback unless it is actually known. The pre-existing marker remains
the conservative signing ambiguity boundary, not proof a signature was made.

No finite local COMMIT check makes a transaction atomic with an external clock
or signer. Checks bound accepted samples at the existing boundaries; no stronger
distributed-time or signer exactly-once claim is made. Timestamp encodings and
marker placement remain unchanged; positive uncertainty may conservatively deny
an immediate next phase whose lower bound precedes an earlier recorded marker.
Do not hide this with a sleep, TTL extension or scalar-time fallback.

## Clock fault: rollback/release first, then latch

On detected reversal/unverifiable provider sample/session or another defined
clock fault, immediately mark this invocation invalid and the shared action
bundle unhealthy before returning to any caller. No concurrent new action
through that bundle may proceed after observing the fault flag. Calls already
inside a writer must recheck the shared flag at their authoritative boundaries;
this is not retroactive cancellation of an already invoked signer.

Cleanup order is mandatory, including panic/error exits:

1. Stop further economic work. Roll back any still-open original phase and
   close/end its read transaction. Discard uncommitted artifact/result buffers.
   Do not undo a previously committed intent or floor.
2. Release the checked-out connection. A successful ROLLBACK alone is NOT
   sufficient while the connection remains checked out. Structure cleanup so
   rollback and connection release run before the fault-persistence handler.
   If rollback/release fails, remain unhealthy, surface the original and cleanup
   errors, and never claim a durable latch. No writer may be COMMITted merely
   to free the connection.
3. Only after confirmed release, use the existing latch helper with bounded
   cancellation-independent cleanup (existing 2s timeout) to acquire a NEW
   connection / BEGIN IMMEDIATE. Re-read latest complete healthy state, retain
   its high-water and append its next generation fault with the existing exact
   CRITICAL audit/outbox. The latch transaction performs zero economic actions.
4. Report durable latch only on a verified successful commit. Another caller
   may have latched the environment first: independently verify the complete
   latest fault and original witnesses; do not insert duplicate generations or
   call it a healthy state. Missing/corrupt or unavailable history remains a
   failure, not latch success. Keep the bundle unhealthy on every clock fault.

The existing runtime latch helper is not presumed idempotent for concurrent
already-latched outcomes. A3's private cleanup helper may classify an already
complete fault through a fresh read after connection release, without changing
the original helper/schema or fabricating a successful audit write. No retry
loop, background worker, reset/rearm endpoint or process shutdown requirement
is added by this scope.

Ordinary cancellation, a stop/revoke/expiry denial, nonce-query failure or
signer/cipher failure alone is NOT clock corruption. Roll back/release normally;
do not append a fault latch just to make every error auditable as clock drift.
Clock faults detected on a read-only preflight use the same release-before-latch
ordering. Do not initialize missing clock history as part of fault handling.

Detection and post-release latch are NOT crash-atomic. A process exit before
the separate latch COMMIT may leave no durable fault. Evidence must describe
that window honestly: only committed floor/latch survives restart; the process
flag does not. Fresh trusted samples and retained intent prevent any claimed
automatic replay of the same signing opportunity. Do not promise a global
cross-process stop before a durable latch commits. Independent recovery remains
available throughout stop, clock failure and signing-bundle unhealthiness.

## Discriminating proposed acceptance additions

These are required future tests, NOT tests executed by this docs-only addendum.

| Fault/window | Required observation |
| --- | --- |
| Fault before marker INSERT | No intent or signature; no healthy generation committed; after release, one complete fault generation/audit/alert or explicit persistence failure |
| Healthy event audit/outbox/real COMMIT failure in transaction1 | Intent and healthy event both roll back when rollback is known; signer_calls=0; no action after uncertain COMMIT |
| Marker COMMIT succeeds, crash before final writer | Intent and first healthy generation survive actual reopen; no artifact; restart/duplicate makes zero extra signer calls |
| Concurrent healthy clock writer commits in phase gap | Final writer reloads latest generation; accepted nonreversed sample appends next generation, reversed sample rejects; no stale generation reuse or retry |
| Stop/revoke/lease change commits before final writer | No signer; retained marker/floor unchanged by rollback; no fault latch solely for policy denial |
| Clock fault in final writer before signer | signer_calls=0; second phase rolls back; first marker/floor retained; no nested connection fetch |
| Fault after signer / before artifact COMMIT | Exactly one original fixture signature; artifact/second healthy floor not committed if rollback succeeds; marker retained; fault persists only after release; no resign |
| Healthy event audit/outbox/real COMMIT failure in transaction2 | No partial artifact/clock commit; signature ambiguity retained by original marker; retry/history/reopen never signs again |
| Single-connection cleanup barrier | With MaxOpenConns(1), prove rollback -> release -> latch acquire order using barriers; completion within cleanup bound, no self-wait hidden by a timeout |
| Cleanup release failure / DB unavailable / forced latch audit failure | Action bundle stays unhealthy, combined original/persistence errors returned; no false durable-latch/audit claim; recovery graph unchanged |
| Concurrent two-handle fault persistence | One complete fault transition with exact CRITICAL witnesses; losing helper verifies existing fault or reports failure; no duplicate audit/generation or healthy fallback |
| Crash after fault detection before latch COMMIT | Do not claim durable fault; previously committed floor/intent unchanged; same-operation restart has zero automatic economic action |
| Crash after latch COMMIT | Complete latest fault/audit/outbox retained after actual reopen; all new action bundles deny; no reset/fallback |
| Cancellation/stop/signing failure without clock fault | No clock fault event; original phase rollback and retained history semantics preserved |

Every target window separately counts fixture signing versus prohibited send,
permit, new nonce, rebuild/resign/replacement/replay actions. Proposed local
tests use controlled providers and ephemeral fixture keys only. No mainnet.

## Design handoff

Read this addendum together with the original A3 scope before independent scope
review and acceptance. Highest status remains scope READY_FOR_REVIEW, not A3
implementation READY or PASS. The author-identified transaction-ordering gap
has a proposed resolution; only a fresh independent scope review can accept it.

No Go/test/schema/config/startup changes or new runtime guarantees are shipped.
No signing consumer, real custody/time source, authorization grant, production
sender, push/PR/merge, A4/A5 or B/C implementation. A1/A2 remain PASS;
production_broadcast=NOT_CONNECTED; authorization=NOT_GRANTED;
live=false; release_ready=false.
