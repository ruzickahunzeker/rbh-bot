# HTTP A/B/C recovery and verified history integration

Continuation base: `ed0d57a6c5c081d54b32b4193ad837d99ed52625`.
Coordinator branch: `http-abc-local-integration`.
Status: scoped independent review PASS; local validation complete.
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

The legacy `position_effect_history` uniqueness key is retained. Revision-bound
MANUAL commands/audits/journal events witness every later transition; that
legacy table is not a complete multi-cycle cursor. The current economic result
still comes from the existing receipt/position-effect tables.

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
independent overlay confirmed those two attacks now reject (13.565s). A later
independent reproduction confirmed the second-admission/reorg risk, closed by
the explicit wallet-reuse boundary above. A corrupted-import residual required
a LEFT JOIN so missing registry metadata cannot hide same-wallet conflicts.

Final independent review of code head
`4a54c7c5785e75835a53f108056ebc0a3d42fa25` concluded PASS for this local MANUAL
recovery and verified HTTP history scope. Its preceding independent attack and
focused regression run passed in 64.730s; the final import delta and independent
attack passed in 4.075s. This does not promote aggregate M4/admission/release
status or authorize production trading. No formal closeout was performed.

Author full ordinary regression and selected race group started on
`82060941c4083ec9b106e4023495bbe03a2a542f`. The final code delta only changes the
import precheck join and adds its permanent regression. That delta's import race
tests passed in 116.273s; the actual lease-expiry commit-window race passed in
158.772s. The selected integration race group passed in 879.112s (tradeapi),
76.361s (storage). A mistakenly named expiry selector in that group is not
counted as coverage; the correctly named expiry test was run separately.
The full ordinary run completed with trade/storage and other packages passing,
but tradeapi failed three old fixture tests: a fixed schema-44 checkpoint and a
023 audit-history model accidentally subject to 045 journal capture/FK rules.
Test-only commit `02495884b4399ecb51e3f3577e59e4cffd085a22` updates the checkpoint
and suspends only the later capture inside that historical fixture transaction,
restoring it before commit with foreign keys still ON. Original historical
guards/denial/no-repair assertions and all production migrations stay unchanged.
Those failed cases passed a focused recheck (3.529s) and the final three-test
race recheck (163.121s). Independent read-only fixture-delta review of exact
`02495884b4399ecb51e3f3577e59e4cffd085a22` concluded PASS without a production/
schema relaxation; those author test results were not independently rerun.
Affected-package vet/staticcheck/format also passed. The complete tradeapi
package recheck on `02495884b4399ecb51e3f3577e59e4cffd085a22` passed in 879.135s.
The failed full run is retained, not relabeled green; other packages' results
still belong to that earlier command/head, not a second whole-repository run.
The final test-only head also passed all-package build/vet/staticcheck and
format verification, without source or module writes. OpenAPI 56 references,
five expected curve events and all 39 offline reference-model tests passed.
The final validation package includes 190 hash-checked files. All 24 test
functions named in the new evidence mapping exist in the source.

Final command/head attribution and retained failed runs belong in the separate
batch evidence at `rbh-validation-package/evidence/http-abc-recovery-history-integration.json`.
Historical evidence is not rewritten. No remote CI result is
inferred from local tests. CI selection adds recovery/HTTP race coverage without
removing the existing groups.
The current docs/evidence increment also passed an independent read-only
attribution/scope check; that check did not rerun tests or verify the pending
final result/checksum.
A subsequent final read-only ledger/manifest check accepted the completed
result attribution and independently verified all 190 package files, with the
original 189 manifest entries unchanged. It did not rerun Go tests and is not
formal closeout; the additive evidence records the inspected evidence digest.

## Remaining production boundary

POST still performs intake/validation/admission only; the private local A
executor has no production HTTP caller/transport. History and local recovery do
not make production trading complete. Later execution composition requires its
own frozen authority/dependency scope and review. No production broadcaster,
signer, nonce allocator, replay executor, new chain/protocol or authorization
grant is introduced by this batch.

Production broadcast=NOT_CONNECTED; authorization=NOT_GRANTED; live=false;
release_ready=false. No remote push/PR/merge or real-chain transaction occurs.

## Next batch proposal, not implementation authority

1. Freeze reusable-wallet accounting first. An older operation's reorg must not
   conflict with a newer admission. Keep one authoritative risk model and C07's
   one-active-operation guarantee; do not remove the unique constraint, add a
   parallel financial ledger or silently cancel/refund another order. Define
   retained historical exposure, lane ownership and manual intervention before
   permitting the currently rejected second admission.
2. Design the HTTP execution composition in parallel. A local first-order-only
   harness can keep the currently enforced no-wallet-reuse boundary; it must
   not claim repeatable trading or remove that restriction. General repeated
   trading must wait for the separately accepted accounting model. Reuse the A coordinator and existing
   execution/submission/recovery state owners, never caller-supplied nonce/raw
   bytes/permit/security fields. Define bounded synchronous response behavior,
   durable request IDs, pending/unknown responses and historical duplicates.
3. Prove the combined workflow in a local authenticated harness with controlled
   transport/query primitives: POST -> original artifact -> sole submission ->
   receipt/canonical/reorg -> GET/events, including restart/fault windows. This
   is not permission to construct a production HTTP transport or send on-chain.
4. Independently review the combined implementation before any formal closeout.
   Production transport, actual signing custody, environment-specific runtime
   authority, wallet allowlist/caps and real canary authorization remain explicit
   separate admission decisions, not inferred from this local PASS.

This proposal does not create a new branch/slice, change an existing gate or
authorize production execution. The completed regression above does not
authorize that separate implementation or change these boundaries.
Detailed scope options and stop conditions are in
`HTTP_EXECUTION_COMPOSITION_NEXT_PLAN.md`; they are a proposal, not new authority.
