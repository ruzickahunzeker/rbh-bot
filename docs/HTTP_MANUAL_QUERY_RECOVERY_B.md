# HTTP MANUAL query and recovery (B)

## Baseline and boundary

B starts from `main@404bbff12826ba8bd6c6552512704b28fb0c9d96` on
`http-manual-query-recovery-b`. Commit `42ce9bc` adds a read-only execution
snapshot and source-aware MANUAL artifact decryption for existing recovery
queries. This is not a production-send or live authorization.

A's independent review remains pending at `A_READY_FOR_REVIEW`. Its local
submission migration is `044_manual_first_submission.sql`; B must not reserve
the same migration number, copy A's unreviewed schema, or claim a submitted
MANUAL recovery acceptance result before A is reviewed and integrated.

## B1: user-visible query and event history

The existing `GET /v1/trades/{request_id}` and idempotency-key lookup retain
their intake status and expose a separate read-only `execution` snapshot.
Every lookup is constrained by durable MANUAL source, principal and wallet.
Responses must not expose raw transaction bytes, ciphertext, nonce material,
signing keys, permits or internal recovery commands.

`GET /v1/trades/{request_id}/events` still reports intake only. It must not
claim lifecycle coverage until a durable, ordered event journal exists. After
A's reviewed migration lands, add an additive B migration using the next free
number. The journal should have one global, monotonically increasing cursor
per MANUAL request and append events in the same transaction as the durable
submission, receipt, canonical or reorg mutation. Existing intake cursor
semantics and `after=` pagination need explicit compatibility tests; synthetic
cross-table row IDs or timestamps alone are not a safe ordering guarantee.

Acceptance: owner-only reads, stable pagination across restart, duplicate
reads without writes, no skipped event after a later state transition, exact
mapping to durable rows, and no PASS claim for events not yet journaled.

## B2: read-only recovery and reconciliation

The existing `Operation -> ExecutionStep -> TransactionAttempt` state machine
and controlled recovery worker remain authoritative. MANUAL artifact AAD is
selected from immutable source history; malformed or missing MANUAL history
fails closed and never falls back to legacy AAD. The worker may query tx,
receipt, header and nonce evidence, then run existing canonical/reorg
reconciliation. It may not sign, submit, replay, send, allocate a nonce, or
create a replacement artifact.

Once A is independently reviewed and integrated, use its durable submission
rows to prove the following with file-backed SQLite close/reopen and a
controlled read-only RPC harness:

| Durable state | Required recovery behavior |
| --- | --- |
| `submitted`, no receipt | Remain pending and queryable. |
| `submitted`, canonical success | Apply position effect exactly once. |
| `submitted`, canonical revert | Terminal receipt outcome; no position effect. |
| canonical mismatch / later reorg | Wait, rollback once, then reapply once if canonical again. |
| `broadcast_unknown`, tx or receipt found | Reconcile without send or replay. |
| `broadcast_unknown`, ambiguous or unsafe nonce | Keep wallet lane and reservation frozen. |
| emergency stop, authorization/TTL expiry | Stop new economic action, not existing query/reconciliation. |
| restart, duplicate scan, lease takeover | Rediscover durable work; stale owner cannot mutate. |

Each case must assert signer, `SubmissionService`, `SendRawTransaction`,
automatic replay, replacement, fee bump and new-nonce calls remain zero on
the B recovery path. `broadcast_unknown` is never released merely because
stop, TTL, authorization or shutdown changed.

## Verification and review gate

Current focused B tests and the related existing submitted/reorg and HTTP
mutable-control tests pass. A combined full `tradeapi`/`trade` run exceeded a
15-minute per-package timeout while exercising existing heavy SQLite tests;
it is **not** recorded as a full-suite PASS. Run isolated focused/race tests
and CI after the A integration, then request independent B review. The
highest pre-review state is `B_READY_FOR_REVIEW`; neither B nor HTTP live is
PASS by virtue of this document.
