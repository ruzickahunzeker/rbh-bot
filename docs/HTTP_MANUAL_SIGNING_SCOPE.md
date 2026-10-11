# MANUAL M3-A — controlled signing / nonce scope freeze

Local scope branch: `http-m3-signing-scope-freeze`.
Base: M2 formal closeout `7e38c0cdb40bf043a3b9d66806de0ce56f433895`.
Reviewed M2 implementation: `03e333b03ce562ca72d03b0f86dc3e9be0e44216`.

```ini
M1 = PASS
M2 = PASS
M3 scope = CONTROLLED_LOCAL_SIGNING_NONCE_HARNESS
M3-A = SCOPE_FROZEN
M3 implementation = NOT_STARTED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

This is design only, not approval to operate a signer, allocate a real-wallet
nonce, grant authorization, create production raw transactions or send. Future
M3 implementation is independently scoped and reviewed; its maximum checkpoint
is M3_READY_FOR_REVIEW, never author PASS. No M4/M5 or W4-D/W5 follows from it.
Scope is Robinhood 4663, Pons v2 Curve/native quote, BUY/fixed SELL/already-resolved
percentage SELL. No BSC/Flap/Base/Solana, Long, LP or protocol/strategy expansion.

## Inspected production gaps

1. `internal/tradeapi/manual_execution_binding.go` binds immutable MANUAL input
   and a nonce-free lane only. It has no signing capability or caller-supplied
   transaction fields. `ManualAdmittedInputReader` deliberately rejects already
   bound work, so M1 cannot be reused as the M3 input loader.
2. `internal/trade/execution_engine.go`'s legacy `Prepare()` uses dry-run/copy
   lifecycle, resolves/quotes again and derives percentage quantities anew.
   It cannot be the MANUAL entry point: MANUAL amount, calldata, minimum, gas,
   fee (including zero tip), quote hash and expiry are already durable/frozen.
3. `internal/trade/execution_store.go`'s `ReserveExecution()` creates its own
   execution step and financial reservation. MANUAL already has index-1
   `manual-exec:<operation>` and the C3-B risk reservation/typed usage owner.
   Reusing this method would duplicate/conflict with ownership and identities.
4. Migrations 020/021 deliberately close downstream changes: original admission
   operation and validation step are facts; bound step/lane changes and attempts
   are denied. The strict M2 view requires the initial bound/nonce-free phase.
   Any future lifecycle extension needs an additive, exact transition contract,
   not removal of guards or treating the initial view as valid after any change.
5. `internal/trade/artifact.go` already provides signer/cipher interfaces and
   encrypted artifact representation; verifier/AAD logic lives in execution
   code. Reuse verified primitives where possible, not legacy admission logic.
   `RPCBackend` also has raw-send capability: it must not enter the M3 harness
   dependency graph, even if exposed through a narrower interface.

## Data flow and ownership

```text
durable request / admission command / M2 immutable binding
  -> bound-input read snapshot (candidate, NOT permission)
  -> same-writer controls / auth / gate / risk / TTL / lane recheck
  -> one existing-wallet nonce claim + one TransactionAttempt
  -> final fenced pre-sign recheck + exact immutable unsigned transaction
  -> controlled local counting signer
  -> signed-byte identity verification + encryption
  -> atomic artifact / execution-step / lane / audit commit
  -> historical artifact inspection only; NO submission/send
```

Keep Operation -> ExecutionStep -> TransactionAttempt. M2 binding and original
command/validation facts are immutable. No second transaction state machine,
admission ledger, risk reservation or authorization-budget consumption is added.
ExecutionStep index 1 and existing wallet lane coordinate the nonce/signing
phase. Attempt metadata references the original operation/step and accounting
owner; it is not another monetary reservation.

Unsigned identity includes chain, sender, recipient, native value, exact data,
gas limit, gas fee/tip caps, original policy/quote/expiry and binding hash. The
only newly resolved transaction field is the single claimed nonce. Never
recalculate percentage amount or ask the client for calldata/nonce/auth hashes.
Zero tip remains valid; no default fee override. An underpriced or stale
immutable transaction rejects, rather than silently requoting or fee bumping.

Artifact metadata reuses the existing attempt's encrypted-raw/key-version/
encryption-nonce/tx-hash/sender/call/gas/nonce/policy/quote/expiry fields and
operation-step-attempt AAD. A narrowly scoped additive metadata overlay may bind
MANUAL contract hash, reservation identity, authorization epoch and deployment
identity if existing columns cannot do so. It may not become a parallel state
machine. IDs, decimal values and absolute time must be canonical and verified.
Nonce zero is valid; conflicts and unsigned integer overflow fail closed.

## Frozen signing gates

- Before claiming nonce and again immediately before signer invocation, reload
  emergency stop first; active authorization ID/epoch; wallet/chain/environment/
  build/release/deployment; immutable policy/allowlists; durable C04/C05/C08
  evidence identity; exact binding/command; original reserved risk/usage and
  conserved allowance; own exclusive lane/attempt; trustworthy clock and TTL.
- Read snapshots and historical M2 duplicates never grant permission. The final
  mutable DB recheck and sign invocation must be under a fenced writer boundary
  that prevents stop/revoke/lane/owner drift committing between them. Query-only
  RPC before that boundary yields evidence, not permission. External signer
  latency, cancellation and stale-owner limits must be bounded and tested.
- `now < expires_at` only; equality, malformed/noncanonical time, unverifiable
  clock or reversal reject. Restart/duplicate/retry cannot refresh TTL or auth
  epoch. Post-sign/pre-artifact-commit expiry must not expose an expired artifact
  as new execution permission; ambiguous sign outcome remains frozen.
- Current chain, nonce and runtime identity verification uses controlled local
  query fixtures only. A production query adapter or real endpoint is not part
  of M3. Contradictory/unverifiable observations reject without new economic
  action. Runtime identity must originate from durable source, not caller fields.
- Signer output must match exact chain, nonce, sender, recipient, value, calldata,
  gas and fee fields. Encryption, verification or durable commit failure returns
  no usable artifact and never falls back to plaintext or another signer/key.

Keep MANUAL no-arm/no-send-permit/no-economic-PASS guards closed. Disposable
fixtures may use the existing explicitly documented fake authorization setup;
that never grants a real operator authorization. Do not disguise signing
validation as a send permit or bypass runtime authorization through a harness.

## Phase / duplicate / restart semantics

Exact future lifecycle names must be defined in the additive-schema slice using
the existing step/attempt/lane statuses, not a new runtime state machine:

| Durable phase | Allowed future action |
|---|---|
| Bound, no nonce/attempt | Fresh gates, then at most one nonce claim |
| Claimed, signing durably excluded | Fresh gates may sign the same exact claim; no new nonce |
| Signing invocation may have occurred, artifact absent | Freeze; no automatic second sign, rebuild or nonce allocation |
| Complete verified encrypted artifact | Historical inspection of exact artifact; never sign again or infer send permission |
| Partial/contradictory/corrupt history | Reject without repair/adoption/release |

A durable signing-invocation boundary is not proof that signing completed. Across
restart, after that boundary absence of invocation is no longer provable; do not
automatically resign. Signer success before artifact commit is a real fault
window, not eliminated by a SQLite transaction. Restart must preserve that
ambiguity with durable identity/audit, even when the final outcome commit fails.
Do not call signing ambiguity `broadcast_unknown`: M3 has no broadcaster.

For a proven never-signed/never-sent terminal path, any nonce/lane/risk cleanup
requires the separately specified exact transition rules; do not invent a blanket
refund or release. Lifetime authorization usage is never refunded. Ambiguous
signing retains the claim. Later broadcast_unknown/propagated recovery remains
the existing M4/M5 concern, never stop/TTL-driven automatic release or resend.

Future phase-aware completeness must verify immutable M2 facts AND exact valid
execution effects. It must retain 022–026 pollution/conflict/audit protections,
not relax them for unrelated legacy writes. Current M2 code/views remain intact
in M3-A/B1. A future migration replaces only specifically reviewed transition
guards; old SQL files 001–026 remain immutable. Direct SQL transitions must have
same-statement complete effects/audit or fail atomically; no API-only assurance.

## Implementation batches (separately authorized)

| Batch | Minimal scope | Maximum status |
|---|---|---|
| M3-B1 | Disconnected SELECT-only bound input reader + pure nonce-free unsigned envelope; no schema, signer, nonce or artifact effects | M3_B1_READY_FOR_REVIEW |
| M3-B2 | Additive exact step/attempt/lane lifecycle, one nonce claim, phase-aware integrity, accounting references, audit/fault/concurrency harness; no signer call | M3_B2_READY_FOR_REVIEW |
| M3-B3 | Controlled ephemeral-key local signer and existing cipher primitives; final control fence, durable invocation ambiguity, exact artifact commit and verification; no send | M3_B3_READY_FOR_REVIEW |
| M3-B4 | Cumulative duplicate/reopen/fault/concurrency/compatibility evidence, manifest and static dependency proof | M3_READY_FOR_REVIEW |
| Review / closeout | Separately authorized read-only independent review; formal docs/evidence only after PASS | M3_PASS only after closeout |

Candidate files: new `internal/tradeapi/manual_bound_signing_input.go` and beside-
package tests for B1; a disconnected orchestration package such as
`internal/manualsign` for later batches with narrow verified input/backend
boundaries; narrowly named storage helpers and the next additive migration for
B2. Package/API design must respect existing HTTP no-economic-dependency tests;
do not add imports or constructors to HTTP/startup merely to reuse a helper.

First future implementation branch: `http-m3-bound-signing-input`, based on the
committed M3-A freeze. It is NOT created by this scope-freeze slice.

## Acceptance matrix

- BUY, fixed SELL, resolved percentage SELL: exact original 22-field binding,
  unchanged amount/minimum/calldata/fee/zero-tip/gas/quote/expiry; caller security
  fields cannot enter. B1 success is a candidate with no signing/send permission.
- Entry expiry, expiry after nonce claim, final equality, malformed time, stop,
  revoke/epoch drift, deployment drift, invalid durable evidence, released/frozen
  initial reservation, wrong wallet/chain and corrupt ledger: no new signing.
- `max_operations`/input cap equality still valid because consumed once; no
  extra usage, refund, second financial reservation or nonce increment on retry.
- Two-handle concurrent claims/duplicate sign requests: one claim/attempt and
  at most one invocation; loser yields no permission or second artifact.
- Stop/revoke/lease takeover commit-window barriers reject stale signing or prove
  competing mutation cannot commit before the protected invocation boundary.
- Faults before claim, after claim, before/after invocation marker, during signer,
  after sign before encryption, encryption failure, before/after artifact commit:
  defined durable state, zero automatic resign/rebuild/new nonce, atomic effects
  and audit, file reopen rediscovery and no plaintext leak.
- Wrong chain/sender/nonce/to/value/data/gas/fee signed bytes, wrong key/AAD,
  artifact substitution, direct SQL UPDATE/DELETE/REPLACE/rowid victim/NULL alias,
  retained old pollution and ignored half-effect all fail closed without repair.
- Historical exact artifact inspection after TTL/revoke may remain readable but
  is not current sign/send permission; inaccessible keys and partial artifacts
  reject, never reconstruct or resign.
- Local counting fixtures and static graph demonstrate submission/replay/raw send/
  production broadcaster/mainnet = 0. B1/B2 signer calls = 0. B3 positive signer
  calls are controlled offline fixtures only; expired/denied paths signer = 0.
- Existing M1/M2/C3 and legacy execution compatibility stays intact. Evidence
  distinguishes author vs independent tests, focused vs full race, and file
  reopen vs power-loss proof. No author PASS or live/release readiness claim.

## Prohibited throughout current work

No production code/migration change in M3-A. No signing/nonce/artifact in B1.
No real keys/grants/RPC/mainnet, HTTP/config/startup wiring, SubmissionService,
ReplayUnknown, RawBroadcaster, SendRawTransaction, send permits, unrestricted
live, new expiry window, rebuild/resign/replacement/fee bump, separate budget
ledger, automatic retained-history repair, M4/M5/W4-D/W5 or other-chain work.
