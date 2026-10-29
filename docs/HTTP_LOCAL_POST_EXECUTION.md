# Local authenticated POST execution composition

Accepted scope: local first-order HTTP harness using existing MANUAL state
owners, test signing, synchronous fake first submission and controlled queries.
Base: `36ba2828c74f09291856e6fbe39722e944442c91`.
Branch: `http-local-post-execution`.
Author status: `LOCAL_HTTP_FIRST_ORDER_READY_FOR_REVIEW`; no aggregate M4/PASS,
formal closeout, production HTTP execution or real-action authority is implied.
Fresh independent review of `f792dbd1f9399615ae7cbc47b85c3f336bfe1b99`
concluded PASS for this local-only scope. The permanent target-parameter test
delta `3031cec784c8c847477f6151bf9151a552b91668` separately passed incremental
read-only review. This does not change the author READY status into a formal
aggregate closeout. Later validation exposed a real first-intake/first-admission
handoff defect despite that review and a green full ordinary run. The test-only
fix and deterministic regression are in `846a33152f8557277962c73415421ce54d66ef66`.
Final 17-test ordinary/race rechecks passed on that head (142.845s / 1730.870s),
with all-package build/vet/staticcheck/format verification also passing. Fresh
independent review of exact `846a33152f8557277962c73415421ce54d66ef66` concluded
PASS for this local-only scope: independent focused tests passed (8.286s),
restoring the old duplicate condition failed the deterministic regression as
expected (0.468s), and additional historical-admission/reopen adversarial plus
focused acceptance passed (10.006s). Those independent checks did not rerun the
whole repository or all race tests. Earlier
failures, interrupted runs and command/head attribution are retained in additive
`evidence/http-local-post-execution.json`.

## Composition and ownership

Every new composition type and constructor is in `*_test.go`. The production
`API`, `Options`, configuration, cmd/app startup and transport graph are
unchanged. The CI change only adds a race selection for this local acceptance.
There is no public listener, production signer/custody or real RPC send adapter.
No migration or existing test/guard is changed.

The local handler delegates authentication, strict user parameters, immutable
intake, validation and atomic admission to the existing `API.ServeHTTP`.
Only the unique successful FIRST admission commit can continue on that HTTP
stack. Intake and admission winners may be different requests: the composite
public `duplicate` flag is NOT continuation identity or permission. A request
with duplicate intake may commit the first admission and start the ONE original
action; a historical admission can never continue. The trusted internal
first-admission response selects a request ID, not security
facts or execution permission: each subsequent existing component independently
reloads the original durable owner/source and current controls.

`authenticated POST -> original MANUAL admission -> execution binding -> nonce
claim -> local signing -> submission foundation -> fresh verifier/permit -> A
single first-submission executor -> verified GET/events`

Operation -> ExecutionStep -> TransactionAttempt and existing submissions,
receipts and position effects remain authoritative. There is no new lifecycle
table, parallel accounting ledger, HTTP status owner or durable retry queue.
The test fake computes its returned hash by decoding the original borrowed
signed bytes; the bytes must be cleared after invocation. Source, fees, units,
absolute TTL, nonce, signature and artifact identities come from the original
durable bindings, never client-supplied security fields.

## Initial state and authority limits

The setup generates an ephemeral key and seeds local policy/configuration plus
one unrelated validated-only source. The target order does not exist before
POST. Before target execution there are zero admissions, bindings, nonce claims,
signing intents, signed artifacts, permits or submissions.

The reused old fixture temporarily removes the no-arm guard to seed a synthetic
ARMED authorization. Its exact original SQL is restored and asserted BEFORE
every new HTTP/action test; a further arming UPDATE must reject. This is NOT
evidence that the intact production schema can grant MANUAL authorization.
No fixture key, permission or fake transport is a real operator grant.

## Synchronous wait, duplicate and restart contract

The local action sequence is bounded by the original request context and an
explicit TEST-ONLY wait budget after admission, capped at five minutes. Slow
race-instrumented SQLite success fixtures use that five-minute cap, not a
production timeout setting. A separate 10ms-budget test must return a deadline
error with durable Location and zero nonce/sign/send work; increasing a later
duplicate's budget cannot restore continuation. The underlying validation keeps
its existing bounds; this is not a claim of a new total production HTTP SLA.
The A invocation has its own one-second trusted synchronous fake deadline.
Waiting never extends absolute `expires_at` or waits for canonical confirmation.

HTTP 202 still describes durable acceptance, not confirmed trade success or a
production send grant. `execution` carries the current historical submission,
receipt and effect facts; all production authorization/capability flags stay
false. A phase error may return 503 after acceptance. `Location`, request ID
and the original idempotency key remain the recovery handles; this is not proof
that the committed intake/claim was rolled back.

An exact duplicate of an already ADMITTED order returns verified history without
reentering the composition, even when the first response was lost or a stage
failed. Intake-only duplicates may still compete for the first admission, as
the existing API already permits; only its unique commit winner can continue.
The public duplicate flag is preserved even when that first admission produces
the original submission. This never retries a committed admission/execution.
Same-key changed
payload stays 409. Concurrent duplicates on independent SQLite handles cannot
create another claim/signature/permit/submission/invocation. A duplicate may
observe the first request before it finishes; it does not wait on a work queue.
The concurrency acceptance uses explicit ordering barriers, not a production
latency assertion. Its passive deadlock guards allow two minutes for entry/
duplicates and the bounded composition budget for the winner. Early HTTP
responses are inspected, so a rejection cannot masquerade as scheduler delay.

The uninterrupted FIRST-admission-winning HTTP stack is the sole initial
execution owner, not necessarily the first-intake stack.
If it fails before signing/submission, its durable admission/claim is retained
for inspection, NOT automatically resumed by another POST or restart. This
batch adds no operator resume/cleanup endpoint or automatic eventual-execution
promise. Future durable scheduling/resume requires its own scope and gate
contract; a new idempotency key is not a recovery mechanism.

After claim, before intent, an older SUBMISSION epoch can be classified
known-unsent-retained only under the existing owner fence. After intent, restart
cannot prove no invocation: it conservatively records broadcast_unknown,
including a crash before the fake RPC invocation. Unknown never proves that
propagation happened. Both states retain the original signed ownership; no
automatic retry, replacement, fee bump, resign, rebuild or new nonce is added.

## Recovery and wallet boundary

The existing controlled recovery worker receives only its independent RECOVERY
lease, store, decrypt/query service and production Pons resolver. Its constructor
does not receive the local handler, signer, fake transport, submission executor,
permit issuer or nonce writer. The legacy cipher interface still includes an
Encrypt method; the test adapter rejects/counts it, rather than falsely claiming
that the interface is structurally decrypt-only.

Submitted and unknown work remains queryable/reconcilable under stop, disabled
mode, revoke and expired application TTL. Query-only unresolved/unsafe nonce
evidence retains ambiguity freeze. RPC failure must produce existing durable
audit/alert without applying an effect or releasing a lane. Recovery uses the
real local Pons effect resolver on synthetic, identity-checked receipts, not a
fake economic-effect resolver. Apply/rollback/reapply must each happen once.

File-backed close/reopen preserves request and cursor identities. A fresh
read-only HTTP handler can return duplicate/GET/events history without execution
dependencies, and a new recovery worker/store/resolver rediscovers original work
from SQLite under the still-valid independent RECOVERY lease. Higher-epoch
takeover acceptance remains in the preceding B/W3 tests, not newly claimed here.

Wallet reuse remains UNSUPPORTED while a MANUAL nonce claim is retained, even
after canonical success or revert. Stop-first priority is unchanged. Second
admission returns `MANUAL_WALLET_REUSE_UNSUPPORTED`; lifetime budget is not
refunded/recharged and C07's single-active-risk constraint is not weakened.
`HTTP_WALLET_REUSE_DESIGN.md` records the separate unresolved design decision.

## Acceptance map

- `TestLocalPostExecutionFirstOrderThreeSides`: one POST BUY, fixed SELL and
  resolved-percentage SELL; exact original artifact/fees/TTL/nonce, one signature
  and fake invocation, duplicate plus both GET routes without extra work.
- `TestLocalPostExecutionTargetParametersNotSeed`: target amount/percentage,
  fee and TTL deliberately differ from the validated-only seed. The real signer
  hook checks BUY value, fee/tip/nonce/chain; SELL input is checked through
  durable validation plus the existing exact-artifact gates, not a new independent
  ABI decoder. Seed gets no execution binding/claim, and duplicate does no work.
- `TestLocalPostExecutionAuthenticationAndStrictInput`: auth/wallet/security
  injection denial; the unchanged production API itself does not sign/submit.
- `TestLocalPostExecutionConcurrentTwoHandleDuplicates`: six simultaneous POSTs,
  two handles; five historical admissions return while the unique admission
  winner's stack is barrier-paused.
- `TestLocalPostExecutionAdmissionWinnerOwnsContinuation`: deterministically
  pause the first intake during validation so another intake wins first
  admission. The latter must submit once while preserving duplicate=true;
  the former and later retries return history without additional actions.
- `TestLocalPostExecutionOutcomeAndLostResponse`: deterministic rejection,
  ambiguity, contradiction, timeout and lost response remain distinct.
- `TestLocalPostExecutionFaultReopenNeverResumes`: binding/nonce/signing/artifact/
  permit/claim/intent/invocation fault windows, file reopen and original semantics.
- `TestLocalPostExecutionControlsAtIndependentBoundaries`: stop, TTL, revoke,
  deployment and SUBMISSION lease drift reject at the applicable next gate.
- `TestLocalPostExecutionClaimAuditAlertAndCommitRollback`: audit, alert and
  precommit failure cannot partially consume/claim; duplicate cannot retry it.
- Cancellation tests distinguish zero new work before intake from retained
  committed claim after intake.
- `TestLocalPostExecutionWaitBudgetExpiresWithoutEconomicAction`: response-wait
  expiry cannot extend application TTL, start economic work or resume on retry.
- Recovery tests cover pending/revert, all three trade sides, unknown query-only
  freeze, stop/revoke/TTL, RPC fault alerts, canonical/reorg/reapply, GET/events
  and new-worker rediscovery after real SQLite close/reopen.
- Static test verifies no product registration; no-arm rejection and the prior
  executor dependency tests remain in force.

Local signer/fake transport calls are ONE for an ordinary successful fixture,
not zero. Duplicates/reads/recovery have zero ADDITIONAL economic calls.
All real send/mainnet/replay execution and real authorization grants remain
zero/unwired. No remote push/PR/merge, production enablement, new chain/protocol,
public API exposure or live/release readiness follows from this batch.
