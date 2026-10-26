# A2 — private first-admission handoff

Branch: `http-a2-first-admission-handoff`.
Base: local `80b35c0d9e3a37baafb11df48f1331cbe7a3feac`.
Implementation and acceptance tests: `c2fd02caa33f34808aa241a4203864cf0c63a50c`.
Author ceiling: `A2_READY_FOR_REVIEW`, NOT A2 PASS or formal closeout.

The user accepted the independently reviewed A2 scope (scope review PASS,
zero blockers, reviewed design head `80b35c0`). That accepts implementation
of the private ownership handoff only, not an execution consumer. This new
document supersedes the historical NOT_STARTED disposition in the unchanged
scope/roadmap evidence; it does not rewrite that historical report.

## What changed

The original admission writer's SQL, atomic reservation/budget/audit effects,
control/TTL rechecks and historical identity verification remain the owners.
Its private `admitWithContinuation` constructs an ownership witness ONLY after
the fresh writer's successful durable COMMIT. All rollback/error returns and
historical COMMITs return nil ownership. The original `admit` helper remains
observation-only and immediately discards any fresh handoff it receives.

The witness contains original durable request/operation/principal/wallet/address,
reservation, contract binding hash, absolute expiry and immutable deployment
admission scope. It carries no raw transaction, key, nonce override or fields
trusted from the HTTP request. A shared private state pointer holds an atomic
one-shot guard; copying the outer value or pointer cannot reset the guard.
It has no exported fields, public constructor or JSON deserializer.

The original authenticated POST stack propagates this witness privately through
validation/admission and verified history projection. A projection read/error
or provenance mismatch discards ALL copies without pretending the committed
admission rolled back. A nil/zero/used/discarded witness cannot be claimed.
Claiming with a cancelled origin or caller context burns it, including when a
new Background context is supplied after origin cancellation.

There is deliberately NO production caller of `take`. Only the test-only
ownership claimant uses it, and that claimant performs no economic action.
The POST handler always discards the witness at stack return. Compatibility
observation helpers also discard it. No API/Record/Store cache, durable ticket,
queue, callback, resume-by-ID route or startup consumer was added.

Ownership is NOT current permission. Any separately accepted future consumer
must reload durable identities and current controls at every existing stage
gate. The witness does not authorize binding, nonce allocation, signing,
verification, permit issue/consume, execution or sending. A lost stack/crash
loses ownership permanently; this slice promises no eventual execution.

## HTTP and compatibility

Public Options, constructors, routes, JSON, status/error mapping, Location and
composite Duplicate semantics are unchanged. In particular, Duplicate=true
can describe the unique fresh admission owner when a different request won
intake. A historical admission is not a continuation selector. A 202 still
means admission observation, not signing/submission or canonical success.

Exact retries after a lost response, GET/by-key/events and actual file-backed
SQLite close/reopen only observe history; none can mint a lost continuation.
Expiry and lifetime authorization usage are not renewed/refunded/reset.
Another order while the original reservation is active is still rejected by
the existing validation gate with 403 WALLET_UNAVAILABLE. This A2 fixture has
NO nonce claim; the broader retained-generation/address restriction remains
the unchanged original schema/implementation, not a new A2 wallet model.
No C response deadline changes or public OpenAPI changes are included.

## Discriminating evidence

All new tests are in `manual_admission_continuation_test.go`; no existing test
is edited. The eight top-level tests map to these requirements:

| Test suffix after TestA2 | Acceptance |
| --- | --- |
| FreshCommitWitnessAndSharedCopyConcurrency | Fresh durable source-bound witness; 64 concurrent value copies share exactly one test-only claim; original reference and historical writer cannot reclaim; database snapshot unchanged |
| InvalidCanceledDiscardedCannotReconstruct | Nil/zero, discard, origin/caller cancellation and nil context fail closed; all copies burned; JSON cannot serialize/reconstruct ownership |
| ObservationCompatibilityDoesNotRetainOwnership | Original observation helper retains public fresh/history semantics without retaining an owner |
| RollbackAndRealCommitFailureMintNothing | before/after command, before commit, cancelled commit, audit insertion fault and real deferred-FK COMMIT failure: nil witness and full row rollback |
| PostCommitProjectionFailureBurnsAllCopies | Cancelled observation read, projection/identity/scope mismatch: committed history retained but every witness copy unusable |
| DifferentIntakeAdmissionWinnersTwoHandles | Deterministic validation barrier across two independent SQLite handles; duplicate intake owns sole fresh admission witness; original intake gets history only |
| LostResponseQueriesDuplicatesAndReopenNoResume | Actual failing HTTP response writer; retry/GET/events and true close/reopen preserve history/TTL/usage, no owner or downstream writes; second order still denied |
| PrivateUncachedNoExecutionConsumerBoundary | No cache/configurable witness, no public serialization/constructor, imports restricted to context/atomic, one production mint site and zero production ownership consumers |

The inherited fixture seeds synthetic offline authorization BEFORE target A2
actions. The new helper restores the exact migration018 no-arm SQL before
every A2 target action and constructs NO signer, executor, cipher or transport.
It never alters a production deployment or weakens production guards.
Real COMMIT-failure testing adds disposable test-only FK tables/trigger under
foreign_keys=ON, never edits a migration or disables the production connection
controls. The test proves the before-commit checkpoint was reached and COMMIT
actually returned a FOREIGN KEY error, rather than injecting an earlier error.

Durable snapshots and existing admission assertions check original reservation
and lifetime usage conservation. Transaction attempts/submissions, execution
reservations/lanes, permits and runtime gate snapshots remain absent. Source
guards retain the original private-executor and no-production-harness/no-arm
tests. These lexical checks are bounded source checks, not a whole-program
formal proof. Zero economic calls on the A2 target path follow from its
dependency-free ownership claimant plus zero downstream rows; they are not
fabricated counters for all inherited test fixtures.

Inherited local first-order harness tests DO use ephemeral fixture signing and
fake transport. Those regressions are separately attributed and must not be
advertised as zero fixture signing or a newly connected production sender.
A1 regressions also use historical fixture artifacts. All containers use
network=none; no real key, mainnet RPC or transaction is provisioned.

Exact final/predecessor command attribution, successful results and the earlier
race timeout/static-check/test-expectation failures are recorded additively in
`evidence/http-a2-first-admission-handoff.json`. No full repository runtime
ordinary/race result or remote CI is inferred from focused results.

## Boundaries and next gate

Only private tradeapi plumbing, one new private file/test, this document,
additive evidence and SHA256SUMS change. Migrations001–045, no-arm, C07, wallet
reuse, immutable TTL, A1 config/startup, recovery, executor, signing, permits,
transport, CI and public OpenAPI remain unchanged. B/C worktrees are not edited.

Next gate: separately authorized independent implementation review of the exact
code/evidence heads. No self-issued PASS, formal closeout, A3/A4/A5, external
push/PR/merge, listener exposure, real signing/grant/send or live activation.

Production broadcast=NOT_CONNECTED; controlled authorization=NOT_GRANTED;
live=false; release_ready=false. A2 alone does NOT complete HTTP trading.
