# HTTP-C3-B — unit-aware atomic admission proposal

Status: USER_SCOPE_ACCEPTED_FOR_B1_B4_IMPLEMENTATION, independent design/code
review still required; no economic authorization is implied.
Local base: `6ed6ea60fcbad7d813ae88ff709ffe2f8d6626f2` (C3-A closeout).
Design branch: `http-c3b-admission-design`.
The original design-only commit is 0b26a9952593564ab09af8081d5dfc818b5968cb.
The user's subsequent continuation accepted the single-input-unit restriction
and additive shared transaction/accounting compatibility scope. B1 is tracked
separately in HTTP_MANUAL_RISK_UNIT_SCHEMA.md. B2 transaction-local input
verification is tracked in HTTP_MANUAL_TRANSACTION_INPUTS.md; B3/B4/C3-C remain
NOT_STARTED at that B2 checkpoint and no atomic economic admission is connected.
B3 now has only the isolated accounting prerequisite in
HTTP_MANUAL_ACCOUNTING_FOUNDATION.md; complete B3/B4 remain incomplete and
HTTP_C3_B NOT_READY. This does not grant additional economic/send authority.
Affected shared compatibility changes require regression and independent review;
historical closed-gate PASS does not approve new behavior.

## Objective and unchanged boundaries

Atomically admit an already validated MANUAL order using the existing
Operation -> ExecutionStep -> TransactionAttempt hierarchy and existing
reservation/authorization ledgers. Keep Robinhood 4663, native-quote Pons v2
Curve only. No fabricated copy intent, additional economic action, signing,
submission, broadcaster, nonce allocation, permit, live or real-chain access.

Reading a C3-A contract is not admission. Reservation is not signing or send
permission. No deployment policy or authorization is created/armed from an HTTP
request. The caller continues to send ordinary order parameters only.

## Verified compatibility gaps

These are future MANUAL integration gaps, not newly declared failures of the
historically reviewed C07/W1/W2 scope.

| Existing component | Verified behavior | Consequence for MANUAL integration |
|---|---|---|
| `internal/trade/canary.go: canaryRiskReason` | tip must be positive; SELL requires SellBPS > 0 | Cannot preserve legal zero tip/fixed-amount sells through this API unchanged |
| Same function / sumReservationAmounts | global/token totals add input_amount without asset units | Cannot add native buy amounts and unrelated token sell amounts |
| `006` risk/window schema | numeric text without input asset/decimals | Need explicit unit binding, not inferred token/global monetary exposure |
| `internal/trade/canary_runtime.go: ReserveRuntimeAuthorizationBudget` | max_total_input sums raw amount; starts its own transaction | Cannot mix assets or chain this call after risk admission and claim atomicity |
| `internal/trade/canary_orchestrator.go: evaluate` | admission-stage budget is a separate store call | Not the future all-in-one MANUAL admission transaction |
| `internal/trade/execution_engine.go: finalCall` | legacy copy intent/current percentage/snapshot preparation | Not a MANUAL exact-result adapter |
| C3-A Load | independent read transaction, no reservation | Must share the future write transaction; returning a prior object is insufficient |

Old migrations 006-017 are not edited. Any shared-core compatibility extension
needs an explicitly reviewed scope plus legacy regression evidence. Historical
PASS records remain historical; they are not permission to silently change
the behavior covered by those records.

## Proposed quantity and policy contract

Every monetary quantity has a key `(chain_id, asset, decimals, accounting_kind)`.
Integers use canonical decimal strings with checked uint256 operations, never
float, SQLite REAL, saturation, rounding upward or guessed pricing.

| Accounting kind | Source | Rule |
|---|---|---|
| trade_input | saved C2 amount + side/token decimals | native for BUY; exact token units for SELL |
| native_gas | saved gas_limit * gas_fee_cap | native units only; distinct from trade input |
| native_outlay | native BUY input + native gas | native components only; SELL input is never added |
| token inventory capacity | saved verified balance and input on SELL | bound by original amount and exact sell ratio |
| operation_count | unique durable operation | integer count across tokens, not separate per-token loopholes |

Proposed policy metadata is immutable and binds an explicit MANUAL risk policy
ID/hash to the original C2 C07 policy version/hash. It does NOT rebind C2 to a
new policy, rewrite the saved quote or reinterpret a historical scalar limit.
Explicit typed limits apply in addition to the original validated caps.

Required typed limits: per-operation input per asset, active wallet/global input
per asset, native gas/outlay exposure, retained native balance, exact SELL bps,
and wallet/global operation-count windows. An asset without an explicit limit
rejects. Output token buckets may group BUY operations, but their input measure
must still be tagged native; SELL input for the same token is a different unit.
No aggregate "all-token monetary value" is claimed without a separately scoped
valuation design. That valuation is NOT proposed for this slice.

Fixed SELL uses saved_amount * 10000 <= saved_token_balance * max_sell_bps.
No invented percentage, recalculation from current balance or tip increase.
Balances are snapshot observations, not a present-balance oracle. The risk model
must conservatively subtract unresolved native commitments and use the shared
wallet lane; later execution/pre-sign still needs its own current read gates.

## Proposed authorization budget: one input unit per authorization

The safest bounded initial design binds each NEW MANUAL-compatible authorization
to exactly one input asset unit. max_total_input then remains cumulative trade
INPUT, excluding gas, in that declared unit. Gas has a separate risk cap.

- Native-bound authorization: can admit BUYs with native input.
- Token-X-bound authorization: can admit SELLs of Token X.
- BUY + SELL, or Token-X + Token-Y input under the same authorization rejects.
- The existing one-ARMED-authorization-per-environment/chain/wallet constraint
  remains. Switching the input asset requires an operator's separately scoped
  authorization action; the order handler cannot create/arm/replace it.
- No unit declaration on an existing ARMED authorization is accepted. Do not
  retroactively label or reinterpret historical max_total_input/usage.
- A future multi-input-asset authorization requires a separate reviewed vector
  budget design, not a sum of different raw integers.

This operational restriction was accepted for the MANUAL implementation slice.
It is not enabled for economic admission here and does not change the user
request contract or allow the order handler to grant authorization.

Authorization usage is a monotonically consumed allowance: unique admitted
operation consumes count/input once; duplicate/restart consumes zero more.
Unknown retains consumption. Release of a provably-unsent risk reservation
does not silently refund authorization cumulative budget or replenish max_ops.
Any future refund policy would require its own invariant/review.

## Proposed durable data model (not migrations)

Do not add a parallel financial reservation ledger. Reuse
canary_risk_reservations, canary_admission_decisions/attempts and
canary_authorization_operation_usage/usage as authoritative records.

Additive metadata candidates:

1. MANUAL policy header and typed asset-limit rows: original C2 policy/hash,
   policy identity/hash, quantity dimensions, windows and exact caps.
2. Immutable operation admission binding: C1 source fingerprint, C2 result and
   C3-A contract hashes, operation/step/wallet/chain/expiry, policy identities,
   deployment and authorization ID/epoch. Same operation cannot acquire another
   economic admission by changing request key or authorization.
3. Immutable risk-reservation unit metadata, one-to-one with the EXISTING risk
   reservation row: input asset/decimals, gas/native dimensions, MANUAL policy,
   snapshot identity and absolute admission window. It stores no second balance.
4. Immutable authorization unit declaration, one-to-one with a NEW authorization:
   source-mode MANUAL, chain/input asset/decimals, MANUAL policy hash, declared
   before arming. A separate request cannot attach it to a historical ARMED row.
5. Optional per-unit usage projection derived exactly from the existing durable
   reservation/operation rows. It is a cache, not another owner of reservations;
   restart verifies projection equals rows. On inconsistency, reject; do not
   silently reset/rebuild budgets while admitting an order.

Keep the existing unique index for one active reserved/frozen reservation per
wallet. Never create one reservation in a MANUAL table and another in C07 for
the same action. Later execution_reservations, if required by nonce/signing
ownership, must reference the SAME admitted economic operation and must not
double-charge risk; that execution integration is a later slice.

Legacy untyped rows are not guessed into units. If needed for an active shared
budget and their units cannot be durably proven, MANUAL admission rejects
RISK_UNIT_UNVERIFIABLE. A reviewed legacy-only path must not accidentally sum
MANUAL typed rows through its old raw global-total query. This requires an
explicit shared accounting-mode guard/typed query compatibility change and its
regression review; a new MANUAL method alone is insufficient.

All new bindings are immutable and reject UPDATE/DELETE/REPLACE and direct-SQL
identity/unit mismatch. Transaction API protection alone is not enough.
Migration number is selected only after the implementation base is rechecked;
no migration file or number is allocated by this design branch.

## Proposed atomic admission procedure

One writer transaction (SQLite BEGIN IMMEDIATE or equivalent proven locking):

1. Read stop/control first. Missing/corrupt/unreadable means reject. Durable
   known emergency stop has highest rejection priority.
2. Load scoped immutable C1/C2 source plus exact lifecycle/result in THIS
   transaction. Reuse C3-A checks via a transaction-local helper, not a prior
   Load return or a nested BeginTx. The public C3-A API remains read-only.
3. Canonically check original TTL, policy hashes, exact durable C04/C05/C08
   attestations, wallet/protocol/contract/token allowlists and accounting mode.
4. Validate existing ARMED authorization and its immutable unit declaration:
   wallet/chain/environment, build/release/deployment, epoch, gate evidence,
   policy identities, absolute authorization expiry, limits. No grant action.
5. Resolve durable dedupe. Compare all original binding fields. Exact duplicate
   cannot create another reservation or usage. Historical result is not a new
   execution permission. Stop/revoke/expiry can reject new permission while
   preserving/querying the original historical decision and frozen work.
6. Check all wallet unresolved states, including legacy execution reservations/
   lanes, canary reserved/frozen rows and ambiguous steps. Compute typed exposure
   and native retained balance; fail on unknown units or inconsistent usage.
7. Check authorization lifetime count/input plus policy windows. Use checked
   integer arithmetic. Exactly-equal limit accepted, one unit beyond rejected.
8. Insert binding, one risk reservation, authorization operation usage, usage
   updates, decision/attempt, permitted lifecycle change, audit and outbox in the
   SAME transaction. All identities/time/unit hashes cross-check at schema level.
9. Final TTL/authorization absolute-time check immediately before commit.
   Stop/revoke writers cannot commit between the authoritative mutable recheck
   and this commit; barrier tests must prove the serialization boundary.
10. Commit once. Return ADMITTED_NOT_SIGNED_NOT_SUBMITTED. No side effect outside
    SQLite. Any fault/error rolls back ALL new financial/lifecycle/audit writes.

Do not call AdmitControlledCanary followed by ReserveRuntimeAuthorizationBudget
and claim atomicity. Both currently start independent transactions. Tx-aware
helpers must accept the SAME transaction, not reopen the DB's sole connection.

The eventual state transition uses the existing Operation/ExecutionStep state
owners. Exact admission status names, legacy execution-candidate exclusion,
decision schema and direct-SQL transition audit rules must be fixed together
before implementing; this document does not create a second queue/state machine.

## Recovery and termination boundary

New stop/revoke/expiry prevents new admission/permission, not queries or existing
receipt/canonical/reorg reconciliation. Unknown keeps lane/risk/authorization
usage frozen/consumed and cannot gain automatic resend, rebuild, resign,
replacement, bump, new nonce or TTL. W4-B send_intent_at semantics are untouched.

Known-unsent release must use existing safely scoped lifecycle proof and update
the single risk reservation atomically; admission code cannot invent receipt/
submission outcomes. No release by timeout, shutdown or failed authorization.
No accounting design in this document grants a recovery worker send dependency.

## Implementation work packages after scope acceptance

| Package | Deliverable | Ceiling |
|---|---|---|
| B0 | Accept/review quantity units, single-unit auth budget and legacy-mode guard | DESIGN_ACCEPTED, no code |
| B1 | Pure unit-aware arithmetic + immutable policy/binding schema in controlled fixtures | B1_READY_FOR_REVIEW, no economic wiring |
| B2 | Transaction-local source/control/auth loading; zero-tip/fixed SELL compatibility guards | B2_READY_FOR_REVIEW, no signer/send |
| B3 | Single-transaction decision/risk/usage/auth budget/lifecycle/audit/outbox | HTTP_C3_B_READY_FOR_REVIEW |
| B4 | Fault/barrier/concurrency/restart evidence and independent review | independent PASS required, then docs closeout |
| C3-C | Default-disabled scoped HTTP admission composition, no signing/send | HTTP_C3_C_READY_FOR_REVIEW |

Atomicity is accepted only at B3/B4, never from individually green B1/B2 tests.
No C3 package can grant real authorization or live access.

## Required acceptance matrix

- BUY native vs SELL token units; zero-tip; fixed and percentage SELL; optional
  min_receive/gas-cap UNSET and supplied; exact frozen calldata/fee/hash/expiry.
- Single-unit authorization accepts matching input, rejects different asset,
  decimals, chain or source mode; undeclared/legacy ARMED auth rejects.
- Global/wallet/input/gas/retained-balance/count caps: equality and +1 unit,
  max uint256 overflow; no raw cross-asset sum or buy/sell token-bucket alias.
- Legacy untyped active work remains safe; unknown units reject; C07 historical
  fixture behavior preserved; legacy API cannot bypass typed MANUAL rules.
- 10,000 mixed distinct/duplicate/concurrent requests on TWO SQLite handles:
  overcommit=0; accepted/rejected/deduped conservation; usage==durable rows;
  one active reservation per wallet; unexplained accept=0.
- File-backed close/reopen: original amount/quote/expiry retained, budget/count
  not reset, duplicate consumes zero, different binding yields conflict.
- Stop/revoke/deployment/epoch/policy/gate drift races inside commit window;
  mutable controls must serialize, not merely pass an earlier preflight check.
- Inject faults after each insert/update and just before commit: all components
  roll back, restart neither loses admitted work nor repeats consumption.
- SQL UPDATE/DELETE/REPLACE/source-unit bypass attempts fail; audit insert
  failure rolls back any allowed lifecycle transition.
- Default-disabled HTTP composition: authenticated scope only; no new public
  listener, caller-provided security fields or internal prepare/replay proxy.
- Structural dependency checks plus test counters: signer/submission/raw send/
  nonce/permit/mainnet calls and authorization grants zero in the new graph.
- Stop/no-auth/expired historical GET and recovery remain available; no healthy
  recovery claim inferred merely from policy-unblocked booleans.

## Current result

The user accepted the single-input-unit restriction and additive compatibility
scope. B1 implements pure measurement and isolated metadata; this does NOT mean
atomic ledger compatibility is solved or economic admission is connected.
B2 implements disconnected transaction-local source/control/auth verification,
but B3/B4 must implement/prove atomic accounting and lifecycle conservation.
Independent review and separate closeout remain required. Signing,
submission, sending, real authorization, live and remote writes remain prohibited.
