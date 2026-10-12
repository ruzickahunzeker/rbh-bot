# MANUAL M3-B2 — controlled local nonce lifecycle

Branch: `http-m3b2-nonce-lifecycle`.
Base: `09176c1143ad62ebb40cb0a58ad6ac00f68ec874` (M3-B1 local closeout).

```ini
M1 = PASS
M2 = PASS
M3-B1 = PASS
M3-B2 = M3_B2_READY_FOR_REVIEW
independent_review = NOT_PERFORMED
M3-B3 = NOT_STARTED
M3-B4 = NOT_STARTED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

This is a disconnected controlled/local harness, not production nonce RPC,
signing or whole-M3 PASS. Old migrations 001–026 and existing M1/M2/B1 readers,
writers and completeness views are unchanged. Only additive 027 replaces
seven specifically named initial-phase deny guards with exact one-way claim
exceptions, retaining their original deny domains and pollution protections.
No HTTP/config/startup adapter or real grant is introduced.

## Data model and exact phases

`manual_nonce_claims` is an immutable coordination command, not another state
machine or financial ledger. It references the original operation, existing
index-1 execution step, one deterministic attempt ID, wallet/canonical address,
TEXT nonce, original risk reservation/contract hash and canonical nonce
observation JSON/hash/checked_at. Authorization epoch/deployment/policy/quote/
expiry remain bound through the immutable M2 binding and original command.
The sole risk reservation and consumed lifetime budget are not copied or charged
again. A wallet/address + nonce is unique and cannot be reused by another claim.

| Phase | Existing lifecycle | Allowed B2 action |
|---|---|---|
| Bound | step `manual_execution_bound`, reserved lane with NULL nonce, no attempt | Fresh checks + one local query, then atomic claim |
| Claimed, signing excluded | same step `nonce_reserved`, same reserved lane with exact nonce, one attempt `prepared` with exact unsigned fields and NULL artifact/hash/encryption | Verified historical duplicate only; no new query/nonce or signing permission |
| Corrupt/incomplete | effects or immutable witnesses disagree | Zero-output rejection; no adoption, repair, release or refund |

Operation and validation step remain admission facts, so existing legacy
recoverable-operation scans do not pick up MANUAL claimed work. Attempt status
`prepared` reuses the existing lifecycle, with exact original chain/sender/to/
value/calldata/gas/fee/tip/policy/quote/expiry and no signature/raw artifact.
No invocation/signing ambiguity phase is implemented here; that is B3 work.

One INSERT owns attempt insertion, exact step/lane transition, append-only audit
and alert. Its final phase-aware completeness check aborts the entire statement
if a constituent was skipped even under outer OR IGNORE. Direct SQL receives
the same effects/audit or rollback. Immutable command and attempt/step/lane/
audit/alert conflict victims, including explicit/implicit rowid boundaries and
recursive_triggers OFF, are protected. Delivery state/timestamp of the alert
may still advance without changing its immutable identity.

The new `manual_nonce_claim_complete` view verifies immutable M2 facts and
original exact audit plus current claimed effects and negative alias/pollution
checks. It does NOT redefine the old initial-only view. M1/B1 and the M2 binding
writer reject claimed work rather than misreading it as a nonce-free phase.
Claimed risk/usage may freeze, never release/consume/thaw through this slice;
historical claim verification does not convert that frozen history into action.
Prepared attempts cannot acquire submission, receipt or position-effect rows.

## Query evidence and authoritative writer

The private writer accepts identity/request and immutable operator scope, not
a nonce or transaction override. Its private observer boundary has only one
local query method and no signer, send, nonce allocation or production adapter.
Initial owned read snapshot verifies current emergency stop first and B1's
full contract/controls/auth/TTL/gates/accounting/exclusive lane. Exact existing
claims are read as false-permission history, without querying a new nonce.

A first claim obtains a bounded (two-second context) controlled-local observation
outside the SQLite writer. Facts given to the observer originate from the
verified durable contract, never caller-supplied wallet/contract/runtime fields.
Observation binds operation/request/binding, chain/wallet/address, contract
runtime identity, canonical block/hash, equal confirmed and pending nonce and
canonical observed_at. A newer canonical block may be used; at the quote block
its hash must match exactly. Pending/confirmed disagreement, unavailable or
contradictory evidence rejects. No unsigned data or percentage re-resolution.

The writer then owns BEGIN IMMEDIATE with verified connection controls, reloads
current controls/auth/policy/gates/TTL/reservation/lane/complete ledger on that
same writer, and revalidates exact immutable input and observation. Competing
stop/revoke cannot commit before the protected claim outcome commits. Final
checks repeat control/auth/gates/TTL/time before commit. SQL derives facts from
durable joins and uses a pure deterministic scalar for observation SHA256,
exact canonical JSON/identity, absolute times and nonce range. A SQL client
without that scalar fails closed; it is not a network or permission capability.

A per-invocation clock tracker checks every read/query/writer timestamp,
including an intermediate reversal that later recovers; no shared mutable
clock state is added. Immediately before COMMIT it rechecks application/auth
expiry and observation age after all DB verification work. Crossing any of
those boundaries rolls back the provisional claim rather than publishing it.

Nonce zero and values above signed-int64 are valid canonical TEXT. Noncanonical
and uint64 overflow values reject; max uint64 also rejects because its successor
cannot be represented. No global nonce counter is advanced and no alternative
nonce is manufactured on conflict. Gas limit must remain positive SQL INTEGER
precision; an unrepresentable durable gas limit rejects, never coerces or changes.
Addresses are normalized only for query identity; original envelope strings
and fee/tip values are preserved exactly.

Observation time must not precede binding, exceed checked_at, expire or be more
than two seconds old. Equality to application/auth expiry and clock reversal
reject. Restart/duplicate cannot refresh original TTL, query evidence or epoch.
This query is evidence, not a guarantee the chain nonce stays unchanged later:
B3 still requires separately reviewed fresh controls/chain/runtime/nonce gates
immediately before signing the original claim, never a second claim or resign.

## Faults, concurrency and restart

Read cancellation retains operational context and commit cause; query timeout/
failure creates no claim. Before read/query/writer/insert/commit failures return
zero output with no durable claim effects. Provisional writes roll back as one
unit, including audit/alert. Response loss after a completed commit returns zero
output but restart rediscovers that exact durable claim: no second query, nonce,
attempt, reservation or budget use. TTL expiry/revoke alone do not release it.

Twenty-four contenders on two independent SQLite handles yield one first claim
and exact historical duplicates. Barrier tests show control updates commit
before the writer and reject it, or are SQLITE_BUSY inside the commit window
and can commit only afterward. Old-owner signing/lease semantics are not invented
in B2: no signer/worker/permit exists. File reopen, statement/transaction rollback
and failed migration rollback are tested, not OS power loss or external signing.

The 026-to-027 upgrade preserves legacy submitted/frozen recovery facts and
allocates nothing. A late trigger collision rolls back guard replacements,
tables and migration journal; retry and repeated migration are idempotent.

## Evidence and stop boundary

`evidence/http-manual-nonce-lifecycle.json` maps permanent B2/pure/scalar/upgrade
tests, source hashes, actual author validation commands, intermediate failures
and limitations. All historical author/reviewer evidence is preserved. There
is no independent review or full-repository race PASS implied by author checks.
Existing compiled package SDK/ethclient dependencies remain preexisting; the
new writer has no production backend, signer/cipher/submission/send capability.
Prohibited-path proof is local/static evidence, not production telemetry.

Only controlled fake fixture authorizations/nonce observations are used; no
real key, endpoint, mainnet call, authorization grant, live or release change.
Corruption fixtures explicitly remove guards/FK only in disposable test DBs.
No rebuild/resign/replacement/fee bump/new nonce on retry, new expiry window,
second risk/budget owner, permission/arming/send permit, other chain, B3/B4,
M4/M5/W4-D/W5, remote fetch/push/PR/merge or automatic PASS/closeout.

Stop at M3_B2_READY_FOR_REVIEW for separately authorized independent read-only
B2 review. Do not reuse the previous B1 reviewer permission or begin B3.
