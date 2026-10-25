# A: MANUAL submission execution — parallel delivery plan

Planning baseline: `main@404bbff12826ba8bd6c6552512704b28fb0c9d96`.
M4-B2.4 is PASS following PR #39 and status sync PR #40.
This document is a proposed implementation plan, not an implementation,
independent review, runtime grant or change to earlier accepted semantics.

## Deliverable

Given an existing durable MANUAL signed artifact, an authenticated local test
identity and controlled synthetic authorization, complete exactly one local
FIRST_BROADCAST submission through a fake synchronous transport and preserve
its durable outcome for HTTP observation and query-only recovery.

The upstream harness may reuse the accepted admission, binding, nonce and local
signing components to prepare that artifact. The submission executor itself
has no signer, encryptor, nonce allocator or authorization-grant dependency.
Production HTTP/startup integration, real RPC send, replay execution, receipt
settlement and position effects remain outside this A batch. HTTP presentation
and recovery consumers are separate workstreams using the contracts below.

Reuse Operation -> ExecutionStep -> TransactionAttempt -> transaction_submissions.
No second economic state machine or accounting ledger is introduced. A is a
delivery grouping; it does not itself close aggregate M4-B2/M4-B/M4 gates.
Storage/outcome work maps to remaining M4-B contracts; local coordination maps
to the submission subset of M4-C; cumulative tests contribute to M4-D. These
are explicit proposed scope mappings, not retroactive PASS or permission from
the earlier B2.4 metadata-only closeout. This planning batch changes no code.

## Findings that determine the design

- `ConsumeManualFirstBroadcastPermit` commits metadata only. Calling it and
  then separately inserting a submission cannot provide atomic claim semantics.
- The existing W4-B begin/verifier uses legacy admission sources and gate
  history. Its service retains an ExecutionKernel and uses legacy artifact AAD.
  MANUAL needs its original full-Facts AAD and original typed accounting source.
- Migration 027 denies MANUAL submission writes; migration 036's complete
  signed-prebroadcast projection requires no submission. A new exact phase
  projection is necessary after claim; the old history contract stays intact.
- Generic worker scan/expiry paths lack MANUAL routing. In particular, generic
  EXPIRED updates conflict with the MANUAL consume guard. Work must be assigned
  to exactly one source-aware owner, including expiry and restart paths.
- The M4-A reader returns verified metadata, not raw bytes or send permission.
  A scoped decrypt-only adapter must authenticate and retain original bytes
  only for the bounded transport invocation, then clear them on every exit.

## A0: common contract, completed before concurrent implementation

Freeze inputs, outcomes, transaction boundaries, file ownership and the following
rules together. This is a short design checkpoint, not a separate closeout PR.

Inputs reference durable request/operation/attempt/permit identity and the
expected deployment/lease. Caller booleans, caller raw bytes, historical PASS
and an already CONSUMED permit are not permission. Current state is reloaded.

1. **Claim transaction:** current source/gates/clock/SUBMISSION lease recheck,
   exact ISSUED permit consumption, sequence-zero submitting row, immutable
   permit-submission relationship and audit/outbox all commit together.
   Implement a transaction-scoped primitive; preserve the old metadata-only API.
2. **Intent transaction:** revalidate the claimed submission with phase-aware
   checks and persist `send_intent_at`. This is the existing durable ambiguity
   boundary. It is not proof the transport was invoked.
3. **Invocation/outcome transaction:** check mutable controls and lease again
   under the writer fence, invoke the bounded fake transport at most once, and
   commit the exact outcome plus accounting/audit effects. Stop/takeover commits
   must serialize with this boundary. Only the current invocation can proceed
   after recording intent; replaying history does not recreate that right.

Historical B2.4 CONSUMED permits without submissions remain metadata-only
history. No automatic adoption, reissue, reset or inferred broadcast outcome.
New claim paths cannot commit that partial state. Cancellation does not erase
committed work or extend a send window; bounded cleanup cannot initiate send.

Outcome rules:

| Observation | Durable interpretation |
|---|---|
| Correct transaction hash returned | submitted; not mined or successful |
| Durably proven no invocation or trusted deterministic non-propagation | known_unsent |
| Intent present after crash, timeout, or uncertain outcome commit | broadcast_unknown; freeze and hand off for query |
| Contradictory transaction identity | manual_resolution; freeze |
| Duplicate/old owner/already consumed standalone permit | no new submission or invocation |

Active reservation, per-operation usage, lane and lifecycle transitions must
remain consistent. Preserve original input/gas units and lifetime authorization
usage; do not double-charge or refund cumulative authorization admission usage.
Any known-unsent release requires a precise schema-protected result transaction.
An unresolved outcome never releases ownership because TTL/auth/stop changed.

## Parallel work packages

| Owner | Exclusive files/responsibility | Can start after A0 |
|---|---|---|
| A1: storage and lifecycle | New additive migration, upgrade/SQL tests, new MANUAL submission store, transaction-scoped consume helper | Atomic claim, phase projections, intent/outcome, typed accounting and source routing contracts |
| A2: artifact and executor | New MANUAL decrypt-only adapter and coordinator files; minimal verifier adaptation | Authentic exact bytes, fresh gate composition, bounded fake transport, cancellation and durable-work dispatch against the A1 interface |
| A3: adversarial acceptance | New integrated tests and reusable local fixtures, no production schema edits | Fault schedule, two-handle concurrency, duplicate/reopen, static dependency and legacy routing checks |
| Integrator | Shared-file changes and final assembly | Resolve shared signatures, own migration numbering, review source isolation and combine all packages |

A1 alone owns schema and accounting transitions. A2 can use a store fake until
A1 is available; A3 can develop fixtures/failure scenarios against frozen types.
Only the integrator modifies shared legacy worker/service files, if required.
Subagents/worktrees use separate file ownership and commits; the final result
is one coherent implementation PR. Independent review occurs after integration,
not separately after every helper. If contracts change, notify all owners and
update the acceptance matrix before coding dependent behavior.

## HTTP and recovery handoff

Expose durable source/format, request/operation/step/attempt/submission IDs,
original tx/artifact identity, outcome/reason, send-intent and relevant epoch to
the existing owners. Do not add a second status owner just for HTTP.

HTTP may report queued only after durable acceptance, and broadcast-observed
only from the recorded submission outcome. A signed transaction hash can exist
before send and must not be presented as evidence of propagation or success.
Receipt success/failure and canonical/reorg changes remain recovery's concern.

Recovery receives propagated or ambiguous identities without transport,
signer, permit or nonce capabilities. A performs no UNKNOWN_REPLAY issuance,
consumption or resend. Missing receipts alone never justify releasing a lane.

## Integrated acceptance

- BUY, fixed SELL and already-resolved percentage SELL use the original exact
  artifact, nonce, fee and TTL; no replacement/rebuild or new economic request.
- Two SQLite handles and concurrent workers yield one claim and at most one
  fake invocation. Duplicates and restart never create a second send.
- Every claim fault rolls back consume/submission/relationship/audit together.
  Upgrade preserves old standalone CONSUMED history and denies adoption.
- Crash before intent is classified only after proving no surviving owner can
  invoke. Crash after intent/before invocation and after invocation/before
  outcome retain conservative ambiguity and original ownership.
- Stop/revoke/expiry/deployment/clock/lease drift reject at the applicable final
  fence; audit failures do not leave partial accounting or outcomes.
- Fake success, trusted rejection, timeout and hash mismatch produce distinct
  durable results. Expired MANUAL work cannot poison legacy worker scans.
- Accepted legacy artifacts and submission tests still behave as before.
- Static production dependency checks show no new HTTP/startup/send caller;
  raw bytes are cleared; replay and real chain send remain unreachable.

Validate focused tests while packages develop. After assembly run the required
ordinary suite, relevant race/fault tests, vet/staticcheck/format and validation
package once on the integrated head. Repeat for changes/failures, not routine
status wording. Record exact test scope; local fake transport is not mainnet
evidence. Final handoff is ready for independent review, never author PASS.
