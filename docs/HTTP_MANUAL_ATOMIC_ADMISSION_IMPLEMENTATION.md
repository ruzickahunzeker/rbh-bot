# HTTP-C3-B/B3-B4 — atomic ledger implementation work order

Scope is the previously accepted B1-B4 design, not additional economic/send
authority. This file fixes the next implementation order after the local B2
checkpoint. The subsequent accounting prerequisite is documented in
HTTP_MANUAL_ACCOUNTING_FOUNDATION.md: migration 019 adds immutable reservation
unit metadata and a SELECT-only same-connection exposure verifier with legacy
mode guards. It does NOT implement complete B3 admission or B4 conservation.
This work order remains the incomplete atomic writer/lifecycle/dedupe plan.
Independent review remains required before formal closeout.

## First: source and lifecycle/dedupe compatibility

The C3-A/C2 readers currently accept validation lifecycle states only and reject
a busy wallet. Future admission cannot blindly reuse that initial-only loader
for an already admitted duplicate. Split full immutable contract verification
from stage-specific lifecycle/lane validation WITHOUT weakening public C3-A.
Only a transaction-local duplicate loader may accept an existing admitted state,
and it must verify its complete immutable admission binding and retained
reservation/usage, not treat that state alone as new permission.

The new admitted status must be explicitly MANUAL-specific on the existing
Operation/ExecutionStep. Avoid generic operations.status='admitted': the legacy
trade Store.Recoverable scans that state and would expose a MANUAL validation
request to the copy-oriented execution engine. Add direct-SQL transition guards
and exact audit/outbox coupling; do not create another runtime state machine.
Request/source expires_at and result parameters never change on transition.

## Second: additive binding metadata, no parallel accounting

Select the next migration number only after checking local schema ancestry.
Keep 006-019 immutable after the foundation lands. Overlay the EXISTING ledgers
with one-to-one metadata:

- operation admission binding: original request/fingerprint, C2 result and
  C3-A contract checksum, original policy/hash, MANUAL policy/hash, exact
  operation/step/wallet/chain/quote/expiry, environment/build/release/deployment,
  authorization ID/epoch and immutable gate evidence set;
- reservation binding: existing canary_risk_reservations.id, unit tuple, native
  value/gas/outlay and original snapshot identity; NO extra reserved balance;
- authorization operation metadata references the existing usage row and same
  operation admission binding; scalar lifetime budget only uses declared unit.

Direct SQL must not insert/modify one ledger half independently. Prefer one
immutable admission command/binding insert whose trigger creates all shared
rows/updates and transition audit in the same transaction, or prove equivalent
schema-complete coupling. Its numeric prechecks must use Go checked uint256,
while SQL verifies canonical shape and exact source identities. This must not
pretend SQLite cryptographically verifies hashes. Hash-validating Go loaders
remain mandatory before granting a decision, even if an isolated corrupted DB
contains shape-valid metadata. SQL alone cannot generate a valid new signed
permit or authorization.

Replace B1 opt-in budget isolation ONLY with complete source/binding/accounting
guards. Retain the no-arm operator boundary and send/PASS-snapshot isolation
until separately scoped operator/execution integration. Positive local test
fixtures may deliberately isolate these gates without real credentials.
Do not merely DROP guards and rely on an API convention.

## Third: legacy-mode and unit-aware exposure compatibility

The original C07 risk function adds raw global/token input amounts and window
projections. A MANUAL extension must prevent legacy risk queries from summing
typed BUY native units with unrelated SELL token units.

Implement a conservative accounting-mode guard on the SAME writer snapshot:

- MANUAL admission rejects relevant legacy/untyped active exposure or window
  history if units cannot be independently proven. Never infer historic units
  from token_address alone or relabel/refund historical usage.
- Legacy admission cannot bypass the typed path or interpret typed rows through
  its raw amount/window queries. Explicitly reject incompatible mixed accounting
  mode, preserving its legacy-only behavior/regression fixtures.
- Active input sums use exact chain/asset/decimals/kind keys and checked uint256.
  Native outlay combines only native call value+gas. Wallet/global operation
  counts are counts across assets, not per-token loopholes.
- Count windows and active exposure are computed from authoritative reservation/
  admission rows; any projection must be proven equal, never rebuilt/reset as
  part of admitting a request.
- Keep the existing one-active-reservation-per-wallet unique index. Existing
  execution reservations, ambiguous steps and non-idle wallet lanes also block
  NEW admission, never a second lane/nonce allocation.

## Fourth: complete single-transaction API, disconnected from HTTP

Own sql.Conn and BEGIN IMMEDIATE. Read stop first; reload source/control/policy/
attestation/auth/unit in this transaction. Resolve durable dedupe BEFORE
rejecting its own retained reservation or attempting a second consumption.
Verify binding conflict for changed policy/epoch/deployment/quote/expiry/source.
Historical duplicate result is queryable, never renewed permission.

Check authorization usage against operation rows. Its total/count is monotonic;
reserved/frozen/consumed/released historical usage is not refunded by risk
release. Exact duplicate consumes zero; another authorization cannot charge the
same economic operation again. Retain budgets/freeze after broadcast_unknown.

Atomically persist decision/attempt, reservation, usage, lifetime authorization
budget, lifecycle transition, audit/alert outbox and immutable metadata. Reject
if any component/source/unit differs; no partial acceptance. Final absolute
TTL/auth recheck immediately before commit. A mutable control writer cannot
commit between the authoritative recheck and this commit.

Return ADMITTED_NOT_SIGNED_NOT_SUBMITTED only AFTER successful commit. A
preflight result, transaction-local contract or newly inserted metadata does
not carry execution/signing/send rights. No HTTP composition yet.

## Fifth: B4 proof, then independent review

- Fault hooks after each financial/lifecycle/audit insertion/update and before
  commit: whole transaction rollback, restart has no orphan ledger half.
- Two-handle commit barriers for stop/revoke/deployment/epoch/gate/policy drift:
  prove SQLite serialization at the actual writer commit boundary.
- 10,000 mixed distinct/duplicate/concurrent local requests, two handles:
  admitted/rejected/deduped conservation, unexplained accept=0, overcommit=0,
  usage==durable rows, one active reservation per wallet, no new nonce/artifact.
- File-backed close/reopen: duplicate zero extra usage, unchanged quote/expiry,
  no budget reset, ambiguity remains frozen, historical GET/recovery available.
- Direct SQL UPDATE/DELETE/REPLACE/half-ledger/transition bypass rejection;
  forced audit/outbox failure rolls back the legitimate transition.
- Legacy-only C07/W1/W2 regression, mixed-mode fail-closed tests, unit-cap equal
  and +1, zero-tip/fixed SELL exact parameters, typed retained-balance checks.
- Dependency/call-count proof: no signer, submission, raw send, nonce, permits,
  real-chain RPC or auth grants in the new admission graph.

Only complete B3+B4 proof can reach HTTP_C3_B_READY_FOR_REVIEW. Author testing is
not independent PASS. No C3-C route, signing/send adaptation, real authorization,
live, GitHub or other chain/protocol work follows automatically from that state.

## Concrete continuation after the accounting foundation

These are implementation work packages inside the already accepted B3/B4
boundary, NOT implemented behavior or authority to expose an HTTP/send route.
Continue locally without requesting confirmation for each green test.

1. Extract immutable source/result verification from initial lifecycle and
   busy-wallet checks. Public C3-A keeps its original checks. A private duplicate
   path reads only a complete existing admission binding; status alone is never
   sufficient. Preserve exact C1/C2 quote/calldata/expiry and original hashes.
2. Fix new lifecycle labels to MANUAL-specific operation/step statuses (e.g.
   manual_admission_succeeded / manual_admitted), outside Store.Recoverable's
   admitted/executing scan. Labels alone must not bypass direct-SQL audit or
   prerequisite financial rows. Add legacy-candidate exclusion tests first.
3. Introduce the next additive immutable admission binding/command overlay.
   It references the original operation, step, reservation and authorization
   operation row, never a second monetary ledger. INSERT command is the only
   route creating the complete financial/lifecycle/audit set. Each shared-table
   guard must reject a standalone half write or altered command identity.
   Do not use a caller-set SQL flag as an authority bypass.
4. Replace ONLY budget isolation guards when complete source/coupling/conservation
   guards are present. Keep no-arm, permit and PASS-snapshot gates. Exact uint256
   totals must be independently checked, not computed with SQLite REAL/CAST.
   If schema-side arithmetic needs a pure checked function, register/test its
   deterministic fail-closed contract on every relevant storage connection;
   plain external SQLite lacking it must reject, not silently fall back. This
   is a design option, not an already selected/registered function or hash proof.
5. Verify nonzero lifetime usage: every authoritative operation row has the
   same declared unit and complete admission binding; checked sum/count equals
   aggregate usage, including retained FROZEN/RELEASED history. Corruption
   rejects; no repair/refund/reset during admission. Across authorization IDs,
   the same economic operation cannot be charged/admitted twice.
6. Own BEGIN IMMEDIATE in a disconnected internal writer. Stop first, full
   binding conflict/dedupe, source/policy/auth/unit checks, typed active exposure
   and all-asset count caps, lane checks, one command, final TTL/auth/control
   check, COMMIT once. Querying an expired historical decision is not renewed
   authorization; errors never leak provisional success/partial exposure.
7. Prove single-request/restart/direct-SQL/fault cases before 10,000 mixed
   two-handle admissions. Include actual commit barriers for mutable controls,
   usage==durable rows, zero unexplained accept/overcommit, one active wallet
   reservation, and no nonce/artifact/send. No author self-PASS or C3-C wiring.

Each local checkpoint records base/head, exact source/test hashes, actual
ordinary/race/lint/manifest results, remaining proof and untouched safety state.
Do not claim a prior run covers code changed after it started; keep historical
validation distinct from final-source validation.
