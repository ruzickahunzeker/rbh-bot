# A2 candidate — first-admission typed handoff, no execution consumer

Status: `A2_HANDOFF_SCOPE_DESIGN_READY_FOR_REVIEW`, not accepted/implemented.
Baseline: local `a562adef68db7ee378562b195dba9b334a029065`, A1 local closeout.
No new runtime, schema, signer, send capability or grant is created here.

## Source gap and minimal boundary

`manualAdmissionWriter.admit` already distinguishes fresh versus historical
admission in its private return after COMMIT. `API.admitManualRecord` then reads
verified history and combines intake/admission Duplicate flags. The production
handler has no execution continuation. The local test-only harness interprets
the internal fresh-admission status in its intercepted HTTP result; this must
NOT become the production composition technique.

Two concurrent requests can have different first-intake and first-admission
winners. Therefore the public composite Duplicate flag is not a continuation
selector. A committed historical admission, GET, request ID, fresh idempotency
key or process restart cannot mint a lost initial action.

Propose a private typed one-shot handoff produced only on the original fresh
admission-success path. It is an ownership witness, NOT authorization or a new
durable work state. Existing durable source/reservation/gate checks still own
whether any action may proceed. Do not persist a new queue/ticket table or add
an operator resume endpoint.

## Proposed API and lifetime properties (names not reserved)

- A private type, e.g. manualAdmissionContinuation, carries only the original
  request/operation/principal/wallet/address and immutable scope/binding
  references plus an in-memory one-shot guard. No raw bytes, nonce override,
  policy/security fields copied from the HTTP body, or public JSON fields.
- Construct it only after the fresh admission COMMIT succeeds. Historical
  dedupe and failed/rolled-back commit return no handoff. Couple construction
  to the writer's typed fresh result, not string comparison of HTTP status.
- Keep it local to the original authenticated POST call stack. Any required
  post-commit provenance read failure discards continuation; retained admission
  is queryable, not rolled back and not automatically resumed.
- No public constructor/deserializer, execute-by-ID route, configurable callback
  accepting arbitrary IDs, Store.Get side effect or API.Options execution flag.
  Original public Record fields and public Duplicate semantics remain intact.
- If later approved consumer code uses it, permit at most one start through the
  original pointer/guard; copying references does not reset it. A failed gate or
  cancellation cannot manufacture another handoff. A process crash loses it
  permanently; this candidate adds no eventual-execution guarantee.
- Handoff possession alone does not authorize binding, nonce claiming, signing,
  permits or sending. Before each existing writer/irreversible boundary, reload
  original durable identities and all current controls. No snapshot-only bypass.

A2's proposed implementation stops at handoff production/lifetime and tests.
It has NO consumer that calls binding/signing/verifier/permit/executor, no
production factory/startup registration and no new signer/transport dependency.
Disconnected private interface scaffolding must not be advertised as production
POST execution. Do not wire a no-op retry queue or change HTTP outcomes to hide
the fact that the initial owner may be lost.

## Exact proposed file ownership and compatibility

| Surface | Allowed after separate scope acceptance | Must remain unchanged |
| --- | --- | --- |
| internal/tradeapi/manual_atomic_admission.go | Typed fresh-commit handoff boundary while retaining existing admission API/result compatibility | SQL command/reservation/budget/audit atomicity, stop-first, duplicate/history identity |
| internal/tradeapi/manual_admission_http.go | Private propagation/discard under the authenticated request; keep verified history read | Public JSON/status/Duplicate/Location and historical no-action semantics |
| New private handoff file and focused test file(s) | No serialization, no consumer, single-use ownership guards | No signer/executor/transport imports or public callable execution factory |
| internal/tradeapi/http.go only if essential | Private local variable/result plumbing under existing auth | Public Options, constructor default-off behavior, routes and error mapping |
| C response tests and docs | Coordinate shared behavior changes through A owner | No simultaneous rewrite of A writer/hook or new status owner |

Do not alter migration 001–045, no-arm, wallet reuse, C07, A1 config/startup,
RECOVERY lease/worker or public OpenAPI. Existing private executor/dependency
and local-harness guards remain unchanged and must pass. A future genuine
production consumer needs an explicitly reviewed new source-owner exception
and stronger boundary tests, never blanket deletion of the old deny checks.

## Required discriminating local acceptance

1. Fresh COMMIT creates one internal handoff; rollback, failed command/audit,
   cancellation before commit and failed commit create none. A post-commit
   read failure discards the handoff without pretending admission rolled back.
2. Reproduce the different intake/admission-winner interleaving deterministically
   on two SQLite handles. Only the fresh admission winner owns the handoff,
   even with public Duplicate=true. Public response/history remains unchanged.
3. Concurrent reuse/copy/reference attempts can start the handoff at most once
   in a test-only consumer. Public JSON/headers/request ID cannot reconstruct it.
   Count test-only ownership calls separately from economic calls, which stay 0.
4. Exact duplicates before/after failure, lost response, GET/events and actual
   file close/reopen never restore continuation. Preserve source/TTL/risk/usage,
   original idempotency identity and existing rejection of a second wallet order.
5. Static boundary guards plus durable row snapshots show no binding/nonce/
   signer/artifact/permit/submission/send introduced by this batch. Do not
   replace these assertions with a fake callback that performs an economic action.
6. Existing admission/fault/HTTP identity/private-executor tests pass. Ordinary
   and selected race checks cover the changed owner path; do not repeat all
   first-order signing/recovery fixtures as a substitute for this handoff proof.

No proposed test result or independent scope PASS is claimed in this document.
Review must confirm whether the private lifetime plumbing can stay this narrow;
if it requires changing another authority boundary, stop and amend the scope.

## A/C response and deadline disposition

Preserve existing HTTP status/error mapping and Location/idempotency recovery
semantics for A2. In particular current typed AUTHORIZATION_NOT_ARMED rejection
is 422, operational errors can be 503, and 202 is not a send promise. Do not
implement C's candidate 10s/30s or timeout-202 in this plumbing batch.

The baseline read-only QueryTimeout is 10s per query stage, default application
TTL is 30s (policy maximum defaults to 300s), and several stages can be
sequential. These are inspected constants, not measured production latency or
a claim a successful first send always fits a 10s POST. A later finite
server-owned total deadline must bound all stages and writer contention,
respect a shorter client context/immutable TTL, and specify post-commit failure
handling. No client-set wait override or TTL extension; no success SLA derived
from race-instrumented fixture timing.

Next gate: independent A2 scope review and acceptance, then a separate local
implementation slice based on the accepted pinned code. No production code is
changed by this design. Real operator authorization, custody/clock composition,
exact-byte transport and startup action wiring remain later scoped work.

Production broadcast=NOT_CONNECTED; authorization=NOT_GRANTED;
live=false; release_ready=false. No push, PR, merge or real chain call.
