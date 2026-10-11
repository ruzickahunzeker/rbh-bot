# MANUAL M2 — durable execution binding, disconnected

Implementation base: `20695c5de5f7beb99ef6346d4d21a061cf1d5b72`.
Local branch: `http-m2-durable-execution-binding`.
Scope: M2-B metadata-only handoff after M1 independent PASS; not M3–M5.

## Frozen data ownership

Reuse the existing Operation → ExecutionStep → TransactionAttempt lifecycle.
Keep the original operation `manual_admission_succeeded` and validation step
index 0 `manual_admitted` unchanged. They are historical admission facts and
remain joined by migration 020's complete-ledger view and no-reset guards.

Add migration 021 only. `manual_execution_bindings` is an immutable overlay,
not a second mutable transaction state machine. It records operation/request,
original validation step, new execution step, wallet/address/chain, canonical
contract hash, policy version/hash, quote block/hash, original absolute expiry,
authorization ID/epoch, manual policy ID, gate evidence identity and immutable
environment/build/release/deployment scope, plus the binding-check timestamp.

The existing execution step at index 1 has deterministic ID
`manual-exec:<operation_id>`, kind `manual_trade_execution`, status
`manual_execution_bound`, and the exact original canonical command JSON.
The existing wallet lane is `reserved` for that operation/step, with NULL nonce
and freeze reason. An existing idle lane may be claimed, but cannot be adopted
with a different address. The lane is coordination, NOT a second financial
reservation or budget consumption. The original C3-B risk reservation and typed
operation usage remain the sole accounting owners.

No TransactionAttempt, encrypted/raw/signed artifact, execution financial
reservation, nonce, send permit or economic PASS snapshot is created.
BUY, fixed SELL and resolved percentage SELL quantities/calldata/minimums,
gas/fees (including zero tip), quote identity and expires_at are never rebuilt,
requoted, resimulated or recalculated.

## Atomic writer and schema

The package-private disconnected writer accepts identity, original request ID
and expected operator scope, not an M1 candidate or caller-supplied security
fields. It owns one SQLite connection and BEGIN IMMEDIATE transaction. It
verifies connection durability controls, then reads emergency stop first and
reloads canonical source/command, active matching authorization, immutable
policy/allowlists/runtime baseline, attestation identity, exact own reservation
`reserved` and usage `RESERVED`, allowance conservation, and wallet exclusivity.

One binding INSERT triggers the new step, nonce-free lane claim, append-only
`MANUAL_EXECUTION_BOUND` runtime audit and INFO alert outbox. The complete-effect
view checks all identities and effects; a failed or ignored half aborts the
whole statement. Direct SQL insertion has the same effect/audit atomicity.
Before COMMIT the writer rechecks control, gates, authorization, reservation and
both intent/auth expiry in the same transaction. A final clock check rejects
equality, expiry and reversal. Operational cancellation/deadline/DB failures
return zero output and keep their causes. Unsafe PRAGMAs are verified input
rejections, not fabricated operational causes.

Schema guards block binding/step/lane alteration, deletion, replacement,
adoption of legacy steps/attempts/reservations into bound MANUAL work and
downstream attempt/nonce/signing writes. Existing landed 001–020 SQL is unchanged.
M1 gains only a bound-operation/bound-step eligibility denial, so a damaged
missing lane does not turn old bound work into a fresh candidate.

The schema validates stored identity and canonical checked_at relationships;
it does not authenticate a privileged SQL caller's assertion of current time
or manufacture cryptographic authorization. The Go writer verifies actual
canonical hashes/current clock. Neither schema shape nor binding success is
permission to execute/send, and the unchanged no-arm/no-permit/no-PASS guards
keep real authorization/send disconnected.

## Idempotency and expiry

First success reports `BOUND_NOT_SIGNED_NOT_PERMISSION`. An exact existing
complete binding reports `HISTORICAL_EXECUTION_BINDING_NOT_PERMISSION`, even
after expiry/revoke/frozen usage; it is NOT a new admission/action or renewed
expiry window. Both permission flags are always false. Scope conflicts, corrupt
or partial binding history reject without repair. Writer entry emergency stop
still takes priority; the existing read-only HTTP admission history remains
available independently.

Duplicate/response loss/restart preserve all saved IDs, parameters and expiry,
create no new audit/claim and consume no additional budget. Binding at a fully
consumed allowance cap is valid because admission already charged that action.
Distinct operations cannot share a busy wallet. M2 does not auto-release a
bound lane on TTL/auth expiry or stop: downstream known-unsent terminal cleanup
requires a separately reviewed lifecycle adaptation. This metadata-only state
does not imply broadcast_unknown, ambiguous sending or propagated transaction.

## Acceptance and limitations

`manual_execution_binding_test.go` has 17 named tests mapped in additive author
evidence. They cover three trade modes, exact 22-field binding, no double-charge,
idle-lane reuse, scope/control/TTL denials, all Go checkpoints and schema effect
faults (including an ignored alert half), schema bypass/update/adoption attempts,
file-backed close/reopen, two-handle duplicate contests, a busy wallet's distinct
operation rejection, response loss, final expiry/reversal/cancel/real deadline,
real control-writer conflict at the commit barrier, historical duplicates after
revoke/expiry and corrupt-record no-repair.

The barrier uses a second SQLite handle with busy_timeout=0, proving stop/revoke
cannot commit inside this writer transaction. It does NOT make a later read
or a future signer/broadcaster action authorized. Future economic boundaries
must independently reload mutable controls and TTL in their own fenced writer.
The distinct-operation test preserves real admission exclusivity; it does not
claim two concurrently valid admissions on one already-busy wallet.

Storage tests construct the actual embedded predecessor schema through 020,
seed inert legacy operation/step/attempt metadata, migrate to 021 twice and
verify preserved statuses/nonce, an empty binding table and all closed guards.
These are test rows, not real signing or nonce allocation. Guard removals for
future authorization/corruption occur only in disposable controlled fixtures.
Checkpoint faults prove transaction rollback, not power-loss durability of an
OS process; file reopen separately proves durable rediscovery/idempotency.

AST/constructor inspection proves no economic dependency or HTTP/startup
registration. Empty artifact/attempt/submission/nonce/permit/PASS tables are
controlled-harness evidence, not production-wide telemetry. No recovery-health
or end-to-end live trading claim is made. Historical evidence is retained.

## Original author review checkpoint (historical)

Author validation is recorded in
`rbh-validation-package/evidence/http-manual-execution-binding.json`.
Full ordinary, all 17 M2 tests and the storage upgrade in three consecutive
race runs, build/vet/staticcheck and offline validation passed. The initial
15-minute three-run race timed out in fixture migration; the unchanged-source
30-minute retry passed (tradeapi 747.703s / storage 14.156s). Full-repository
race did NOT pass: trade and tradeapi hit their 25-minute package timeouts,
respectively in a W4-C fixture migration and the existing 10,000 mixed C3-B
conservation test. No data race was reported in the returned outputs, but that
is not proof of a complete full-race pass. This limitation remains visible for
independent review; ordinary full testing does not replace that evidence.
Seven selected M1/C3-B/C3-C compatibility race tests passed (49.595s), and
the actual M1 public late cancellation/deadline regression passed separately
(12.965s). These selected tests are not relabeled full-repository race.
This local slice creates no GitHub PR/push/merge. Independent M2 review requires
explicit stage-specific reviewer authorization; earlier M1 permission is not
reused. Stop at M2_READY_FOR_REVIEW, with no author PASS or closeout.

```ini
HTTP_C3_B = PASS
HTTP_C3_C = PASS
M1 = PASS
M2 = READY_FOR_REVIEW
M2 independent review = NOT_PERFORMED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Not included: HTTP/config/startup wiring, signer/SubmissionService/RawBroadcaster,
SendRawTransaction, RPC/mainnet, real grants, new nonce/attempt/artifact,
rebuild/resign/replacement/fee bump, extra financial reservation, M3–M5,
W4-D/W5, BSC/Flap/Base/Solana or release/live enablement.

## Current reference hardening

Independent review of `46270e8195eb76758bf978e36bb28d40b4835d06` concluded
FAIL: legacy operation IDs could bypass reservation/lane guards by referencing
the bound step, and the original complete view did not detect extra references.
The claims of complete legacy-adoption protection above describe the original
intent; that review disproved them at 021. The original author evidence/status
block is historical, not the current independent review result.

See `HTTP_MANUAL_BINDING_INTEGRITY_HARDENING.md` for the additive 022 fix,
strict integrity view, reproduced predecessor defects, actual reopen tests and
unchanged no-permission semantics. Current maximum is M2_READY_FOR_REVIEW,
with the former independent FAIL retained and fresh review NOT_PERFORMED.
No M2 PASS/closeout or M3 follows from author hardening.

Later checkpoints: 023 repaired different unique conflict victims on
reservations/lanes. Its committed head `3be7a805e693f7973d9224aaf7fd812003a7e911`
then had an interrupted full review (BLOCKED) and an independent static
alert/audit subreview (FAIL, not a runtime reproduction or full verdict).
Current authorized 024 hardening is described in
`HTTP_MANUAL_AUDIT_ALERT_HARDENING.md`; all earlier author checkpoints above
remain historical. Maximum M2_READY_FOR_REVIEW, fresh review still required.

At `5010258ab7ad2ef889f376bb280809f2087d809b`, another interrupted full review
reproduced a binding-table implicit-rowid INSERT replacement defect. See
`HTTP_MANUAL_BINDING_ROWID_HARDENING.md` for minimal additive 025 protection,
permanent regressions and unchanged no-permission scope. Review remains pending
at the new author checkpoint; no lost-history repair or M3 is authorized.
