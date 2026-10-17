# MANUAL M4-B2.3 — proposed private first-permit contract

Inspected clean local base: `8c635fc03187f4289b90de7a442692618db5fde9`.
Docs-only branch: `http-m4b23-permit-design`; local, not remote main.
Continuation after B22 closeout is applied to a design proposal only. This is
not scope acceptance, code/migration authorization or a new permission grant.

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4-A = PASS
M4-B1 = PASS
M4-B2.1 = PASS
M4-B2.2 = PASS
M4-B2.3 scope = PROPOSED_FOR_ACCEPTANCE
M4-B2.3 implementation = NOT_STARTED
M4-B2.3 independent review = NOT_PERFORMED
checkpoint = M4_B23_DESIGN_READY_FOR_ACCEPTANCE
M4-B2 = INCOMPLETE_NOT_READY_NOT_PASS
M4-B = INCOMPLETE_NOT_READY_NOT_PASS
M4 = INCOMPLETE_NOT_PASS
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## 1. Smallest next slice and explicit design decisions

Propose B23 as a disconnected private FIRST_BROADCAST gate/permit issuance
contract, schema + owned writer + controlled local tests. It does NOT consume
permits or reach a send/submission/transport boundary. This narrower contract
must be accepted before implementation; B22 closeout does not approve it.
UNKNOWN_REPLAY positive issuance, downstream phase/outcome schemas, permit
consumption and immediate-pre-send remain later separately scoped work.
Whole B2 cannot be called complete on this first-issuance slice alone.

Reuse existing `canary_runtime_gate_snapshots` and `canary_send_permits` plus
an exact immutable MANUAL source-binding overlay. Do not create a second
permission/transaction/reservation/financial state machine. The new permit is
local durable eligibility metadata, never an external bearer credential or
proof that current clock/process/RPC checks actually ran. A consumer MUST run
fresh verification in its own final writer; no consumer is implemented here.

No client-visible parameter/endpoint changes, public constructor, HTTP/config/
startup worker composition or legacy orchestrator reuse. Input remains trusted
authenticated owner identity and original request identity, not caller-supplied
artifact/security hashes, deployment, time, epoch, quote or permit duration.

## 2. Actual current components and gaps

| Component | Reuse / required change after separate acceptance |
|---|---|
| `manualRuntimeVerifier.prepare` | Fresh bounded time/process/artifact/DB preparation and fixed read-only RPC outside writer; never use committed capture as permission |
| `manualRuntimeVerifier.finalCheck` | Stop-first process/auth/epoch/policy/source/artifact/clock/lease recheck on B23's caller-owned writer; no nested transaction or RPC |
| `manualRuntimeVerifier.capture` | Remains B22 metadata-only; MUST NOT be called as B23's authorization shortcut |
| `manual_gate_clock_events` | Append next healthy generation in issuance transaction; terminal latch and separate post-rollback fault handling preserved |
| `manual_submission_foundation_complete` / original038 source | Original MANUAL identity, signed-prebroadcast-only completeness, active typed reservation/usage and conservation; no fake legacy admission source |
| `canary_send_permits` | Reuse FIRST_BROADCAST identity and existing unique one-first-per-attempt constraint; no permit consume implementation |
| `canary_runtime_gate_snapshots` | Reuse immutable FIRST_BROADCAST gate record; its unique key means no duplicate refresh or snapshot replacement |
| migration018 `manual_b1_no_permit` / `manual_b1_no_pass_snapshot` | Currently deny MANUAL. Any future additive exception must be exact to complete new MANUAL issuance contract, never blanket removal/config flag |
| migration018 `manual_b1_no_arm` and downstream guards | Stay closed; no real authorization, submission/receipt/effect/release exception |

The suggested additive040 is only a proposal; no migration is created/reserved
by this design. Migrations001–039 and all reviewed code remain unchanged here.

## 3. Proposed binding model and immutable identity

Use existing Operation -> ExecutionStep -> TransactionAttempt. Proposed exact
MANUAL issuance binding references the original complete foundation, existing
snapshot ID and FIRST_BROADCAST permit ID. Store immutable canonical source JSON/
hash and exact runtime observation identity as metadata with atomic audit/alert.
No general `verified=true` field or DB row can attest external verification.

Minimum bound facts, loaded from original durable sources/owned composition:

- principal/request/operation/step/attempt and original reservation;
- chain4663/environment/wallet/from, policy IDs/version/hash, durable C04/C05/
  C08 evidence identity and original C07 reservation/source binding,
  exact active authorization ID/epoch/deployment/build/
  release plus measured binary/immutable manifest digest;
- original Facts/contract hash, tx hash/raw artifact hash/AAD/key version,
  nonce/calldata/value/gas/fee including zero tip, amount/minimum/side;
- original quote block/hash, curve/runtime baseline, original absolute expiry;
- active reserved typed units/usage conservation (NO second budget charge);
- SUBMISSION lease holder/epoch/environment/absolute expiry (RECOVERY separate);
- clock domain/generation/sample/error bounds, observation chain/profile/network
  checkpoint/quote/anchor/code/digest and fixed local freshness profile;
- purpose FIRST_BROADCAST, exact issued_at/permit expiry and deterministic IDs.

The overlay is not another economic ledger. Its completeness must bind the
existing permit + snapshot + source + witnesses exactly. Durable revalidation
checks old and new identities/rowid victims/types/encoded aliases, not only
INSERT values or JSON shape. Direct SQL may only create structurally complete
metadata; that is not a current runtime capability. Future consumers must not
trust it without their own fresh original-source/crypto/process/time/RPC checks.

## 4. Issuance flow and transaction boundary

1. Stop/control first; missing/unreadable/stopped denies before external work.
2. Fresh B22 preparation under operator-owned trusted composition; release read
   snapshot before bounded RPC. No detached candidate/old038 row as authority.
3. B23 owns one connection and BEGIN IMMEDIATE. Call transaction-local finalCheck
   on that connection, with original source/phase/expected lease/generation and
   process/time/crypto checks. No second writer, nested capture, network I/O or
   signing. Reversal follows existing latch-after-cleanup semantics.
4. Resolve duplicate/conflict before issuance. On first issuance only, bind exact
   source, next clock event, FIRST_BROADCAST snapshot+permit and audit/alert as
   one atomic metadata operation. No intermediate row is independently usable.
5. Final cancellation/time bounds must hold before COMMIT. Commit then return
   private metadata, not raw bytes/opaque credential or send authorization.
   Cancellation after COMMIT suppresses the response but does not uncommit.

Permit expiry is canonical absolute time, created once by the owned writer:
minimum of original operation expiry, authorization expiry, SUBMISSION lease
expiry and a strictly bounded operator-owned verification window. No invented
production default; immutable local profile is required. Boundary equality
rejects; no rounding/text order. This is NOT a new operation TTL or contract
deadline. Do not recompute/extend it on retry/restart or reuse a relative timer.

Process/clock/RPC checks are trusted runtime actions, not schema oracle proofs.
The transaction linearizes DB metadata only; subsequent stop/revoke/lease/
deployment/time/chain drift makes future use ineligible. Neither PASS snapshot
nor ISSUED permit bypasses the future immediate-pre-send recheck.

## 5. Duplicate, one-shot and failure semantics

At most one FIRST_BROADCAST permit ever per original attempt, including revoked/
expired/consumed history. Same owner and exact original binding may read that
metadata, but a fresh eligibility response must still rerun current checks.
Do not update/reissue the old snapshot or mint another permit/expiry window.
Different expiry/identity/source/owner is a conflict, not a repair. Returning
already-expired/revoked/consumed history must not be labelled eligible.

Concurrent distinct requests for the same attempt serialize to one complete
issuance. Restart discovers exact committed history; incomplete history rejects
without adoption or later-statement witness repair. No repeated accounting
consumption/refund, nonce, signed artifact or changed quote/amount/fee/expiry.

This slice only issues; it must not add a public or private economic consumer.
All consumption attempts through existing MANUAL-denied paths remain denied.
Existing008 permits ISSUED -> CONSUMED in general; its generic one-shot rule is
NOT a MANUAL consumption deny. The proposed additive guard must explicitly
reject MANUAL consumption/identity mutation via direct SQL as well as APIs.
B23 implements no permit state-transition/cleanup worker; expiry is a read-time
ineligibility check, not a reason to mint a replacement permit. Future consumed/
revoked/expired writer transitions need a separately reviewed exact contract.
Purpose cannot be changed/reused for UNKNOWN_REPLAY. Ambiguous/submitted history
is outside the signed-phase positive path and must fail closed here, not be
treated as a new first action. Query-first replay remains a later exact contract.

Rejected issuance does not create known-unsent/broadcast_unknown/manual-resolution
or release ownership. No send marker/transaction submission is written. Original
send-intent conservative ambiguity and stop/TTL/auth recovery continuity remain
unchanged. Existing propagated/ambiguous transactions never need this issuer
to continue query/receipt/canonical/reorg recovery.

## 6. Schema guard and audit requirements

Future additive040 may replace only the two MANUAL permit/PASS INSERT denies
with exact deny-by-default guards accepting the complete proposed contract.
No no-arm exception; no existing migration byte rewrite. Legacy/other MANUAL
domains, unknown/submitted phase and all other stages/purposes stay denied.
All unrelated existing safety triggers remain in effect.
Add an exact MANUAL permit mutation/consumption deny for the newly issuable
domain, so admitting an INSERT cannot silently activate008's generic consume
transition. This is a negative guard, not another financial state model.

Bindings, snapshot, permit, clock event and audit/alert must be structurally
atomic and immutable with exact deterministic namespaces. Apply the B21 lesson:
witness FAIL must not retain an adoptable parent. Test later-statement repair,
not merely one immediate failed COMMIT. Incomplete staged rows must not become
usable/committable by later witness insertion or competing UPSERT/multirow work.
Intact FK_ON schema is required, not resilience to hostile schema removal.

No persisted scope flag/test_mode or caller token may disable guards. Controlled
positive tests may reuse explicitly synthetic authorization fixtures only;
restore original no-arm behavior before tested action, and separately prove
intact production cannot arm/grant. No operator/mainnet authorization is issued.

## 7. Proposed acceptance matrix — not yet executed

| Area | Required proof |
|---|---|
| Three sides | Exact original Facts/artifact/nonce/zero-tip/fee/quote/minimum/expiry and typed accounting; no re-resolution |
| Fresh verifier | Owner/control/source/clock/process/RPC/crypto before issuing; detached capture/DB verified JSON cannot substitute |
| Time | Whole/fractional -1ns/equality/+1ns for operation/auth/lease/permit; bounded uncertainty, provider/session/ticks reversal, floor/latch and overflow |
| Final fence | stop/revoke/source/deployment/lease/TTL drift before writer rejects; competing stop/takeover cannot commit inside final metadata writer |
| Dedupe | Single complete snapshot/permit under two-handle contention; exact duplicate no recharge/TTL/permit refresh; changed identity conflicts |
| Restart/crash | Before writer/before COMMIT/after COMMIT actual process exit and reopen; one complete issuance or none, no lost ownership or new window |
| Direct SQL | Four conflict modes, aliases/BLOB/NUL/noncanonical/rowid victims, partial history, namespace preseed, UPSERT/multirow and later witness repair cannot bypass completeness |
| Audit faults | ABORT/IGNORE/FAIL for all witnesses; exact one audit/alert; journal/COMMIT/cancel errors; uncommittable incomplete scope; old histories not repaired |
| Purpose/phase | UNKNOWN_REPLAY/consumption/unknown/submitted/cross-owner/legacy-source forgery rejected; first permit never usable as replay |
| Preservation | Exact001–039/prior evidence/closed gates; upgrade rollback/idempotence and unrelated legacy behavior unchanged |
| No reachability | No signer/encrypt/key loader/transport/submission/send marker/nonce/HTTP/startup; fixtures reported separately from production counts |

Author evidence must retain intermediate failures/negative controls, exact test
and source/log hash mappings, final-vs-earlier source attribution, fixture trust
limits and selected-race scope. Independent B23 review needs explicit stage
authorization and exact committed head; no self-PASS or automatic closeout.

## 8. Implementation order only after acceptance

1. Freeze this narrower FIRST_BROADCAST-only contract and guard exception.
2. Add exact schema/binding/completeness/audit contract and direct-SQL tests.
3. Add private writer using fresh prepare + same-writer finalCheck, no consumer.
4. Run duplicate/restart/fault/barrier/legacy regression and scoped race checks.
5. Record additive author evidence/checksum/local handoff; max
   M4_B23_READY_FOR_REVIEW, never whole B2/B/M4 readiness or milestone PASS.

Suggested future implementation branch: `http-m4b23-first-permit`; do not create
it now. No migration, product/test code, runtime suite or guard is changed by
this proposal. No B3/B4/C/D/M5/W4-D/W5, additional chain/protocol, signer/send,
production wiring, real RPC/grant/canary/live/release or remote GitHub action.
