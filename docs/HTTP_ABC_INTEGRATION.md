# HTTP A/B/C shared integration baseline

This is the historical shared handoff receipt. The newer recovery/history batch
is tracked in `HTTP_ABC_RECOVERY_INTEGRATION.md`; it does not rewrite the baseline
evidence below.

## Source and status

Coordinator branch: `http-abc-local-integration`.
Worktree: `/root/http/worktrees/http-abc-local-integration`.
Status: `ABC_LOCAL_BASELINE_PREPARED`.
Production code baseline: `3ce8105c7ccc4bb0c307d5c20d945094b14e7e51`.
Integrated test head: `71a48b244bde8ff03d2920852e4c7bdef7080e95`.
The handoff commit also includes this plan, review receipt and validation
evidence. Pin the full handoff SHA reported by the coordinator when starting
downstream work; do not assume a branch name is immutable.

| Input | Full source commit | Accepted meaning |
| --- | --- | --- |
| Common main ancestor | `404bbff12826ba8bd6c6552512704b28fb0c9d96` | Shared local starting point; no remote refresh claimed |
| A | `c0945efb514d47d1c808668e9eb4e00966533fbb` | Independent review PASS, private local FIRST_BROADCAST fake transport only |
| B | `e77e8f39de12874ba216ec13e9c9749e73566700` | Query/AAD foundation, not completed recovery or independent PASS |
| C | `f0f9442eff50b142fd97ef97872a00fbc2305bd4` | C1 progress projection plus history/corruption tests, not completed HTTP execution or independent PASS |

This is a development baseline, not `ABC_READY_FOR_REVIEW`, aggregate M4 PASS
or production trading readiness. The coordinator does not edit or move the
original A/B/C worktrees and branches. C's test-only follow-up `f0f9442` was
imported from its own branch after the full run; the initial C input was
`29a0b8cfd700ccbb3f216b071f6152a4e29319d5`. The A report is recorded in
`HTTP_MANUAL_SUBMISSION_EXECUTION_REVIEW.md`; its original author evidence is
retained unchanged, including its earlier NOT_PERFORMED review snapshot and
failed/timed-out validation commands.

## Coordination and file ownership

- Coordinator: this A/integration thread,
  `codex://threads/01a0e8f6-10f6-7870-95de-a908f5c96dff`.
- B: query/recovery thread,
  `codex://threads/01a174cd-6495-7e81-b03a-f23e44dd2e08`.
- C: HTTP thread,
  `codex://threads/01a174ce-7270-7392-b446-8246896dee87`.

The host-qualified deep links remain in the coordinator conversation. Use that
host context when opening these thread IDs from another Codex window.

The coordinator owns merges, the shared `internal/tradeapi/store.go`, this
baseline ledger and checksum assembly. B owns execution/history readers,
recovery source/service integration, the next additive migration and lifecycle
events. C owns HTTP response presentation, OpenAPI, client documentation and
HTTP tests. A change to the shared Record/Get contract goes through the
coordinator rather than being independently overwritten by B and C.

Prepared downstream worktrees use the final handoff commit as their common
parent:

| Workstream | Branch | Worktree |
| --- | --- | --- |
| B continuation | `http-b-query-recovery-integration` | `/root/http/worktrees/http-b-query-recovery-integration` |
| C continuation | `http-c-http-integration` | `/root/http/worktrees/http-c-http-integration` |

The new worktrees are starting points only. No B/C continuation implementation
or task dispatch is implied by creating them. In particular, the shared
`/root/http/rbh-bot` checkout remains on its original C1 branch.

## Shared HTTP and identity contract

The Record merge retains both `progress` and optional `execution` fields.
Top-level `status` and `reason_code` remain intake history. `progress.stage`
currently reports only rejection/intake/validation/admission milestones.
GET by request ID and by idempotency key also expose B's execution snapshot.
POST does not yet promise an execution snapshot or invoke the A coordinator.

A transaction hash can exist before a submission. A durable `submitted` outcome
is not a receipt success or a canonical position effect. Unknown and retained
known-unsent work must not be presented as retry permission. No response may
expose raw bytes, ciphertext, encryption nonces, private keys or permits.

The authoritative identity chain for B's follow-up is:

`MANUAL request -> immutable execution/nonce/artifact identity ->
manual_first_submission_bindings -> exact transaction_submissions row`.

Submission history must match operation, wallet, attempt, transaction hash,
artifact hash and the sole first-submission binding. B's imported query still
uses latest-step/latest-attempt selection; hardening this projection is an
explicit pending B deliverable, not acceptance granted by this merge. Missing,
conflicting or corrupt provenance must not be promoted to a successful result.

Historical reads cannot use A's current-permission phase views: those views
intentionally require a live submitting phase and current controls. Existing
query/reconciliation must remain available after emergency stop, authorization
revoke/expiry, application TTL expiry or deployment drift.

## B continuation

1. Bind history reads and durable work discovery to the identity chain above.
   Reuse Operation -> ExecutionStep -> TransactionAttempt and existing recovery;
   do not introduce a second transaction result owner.
2. Migration 044 is frozen. B owns the next free additive migration, currently
   expected to be 045; recheck numbering before creating it. Existing schema
   still rejects MANUAL receipt/position-effect writes and risk release. Design
   narrow, source-bound recovery transitions with same-transaction lease fencing,
   accounting, audit and event witnesses. Do not remove guards wholesale or
   route MANUAL units through legacy release accounting.
3. Add a durable lifecycle journal. Append events with the corresponding
   submission/receipt/canonical/reorg mutation. Preserve existing intake cursor
   semantics, strictly ordered pagination across restart and later transitions,
   and deterministic historical backfill. Cross-table row IDs/timestamps are
   not a shared cursor. An empty page must not advance past an unseen event.
4. Prove submitted pending/success/revert, canonical mismatch, reorg rollback
   and reapply exactly once. Prove unknown tx/receipt discovery and unresolved
   ambiguity without signing, sending, replay or new nonce allocation.
5. Prove file-backed close/reopen, durable rediscovery, duplicate scans,
   RECOVERY lease takeover and stale-owner mutation denial. Stop/auth/TTL cannot
   release unresolved ambiguity; A's known-unsent-retained work stays retained
   unless a separately defined release contract is implemented and reviewed.

## C continuation

1. Consume B's verified history contract and expose consistent request-ID and
   idempotency lookup results. Do not build a competing query/status owner.
2. Extend presentation and OpenAPI only when the relevant durable facts exist.
   Keep signature, submission, unknown outcome, receipt status and canonical/
   reorg effects distinct. A rollback must remain visible even after an earlier
   successful receipt; an intake milestone is not current transaction success.
3. Test caller/wallet isolation, missing/corrupt provenance, duplicate POST,
   query after stop/expiry/revoke, cursor pagination and sensitive-field absence.
   HTTP reads and duplicates must not refresh TTL or repeat economic work.

Production HTTP execution remains a separate integration item: the imported
POST handler still performs intake/validation/admission only. Completing B/C
history presentation does not connect it to A. A later controlled HTTP harness
may coordinate the workflow with fake transport; introducing production
callers/transport requires its own explicit composition scope and review.

## Integration acceptance and validation

On this baseline the new `TestHTTPABC*` tests prove both projections coexist,
signed hashes are not presented as submissions, and a local fake submission
can be read over both GET routes after stop/revoke without a second invocation.
They also check principal isolation and absence of sensitive artifact fields.
They do not prove receipt/canonical/reorg acceptance or HTTP POST execution.

Validate focused changes during B/C work, then run one integrated ordinary
regression plus targeted race/fault tests and independent review. Do not rerun
unchanged large A groups merely to update status wording. Preserve failed
commands and timeout attribution separately from successful rechecks.

Author integration results are recorded in
`rbh-validation-package/evidence/http-abc-integration-baseline.json`.
Full ordinary regression completed successfully on the integrated code:
`go test ./... -count=1 -timeout=30m` (tradeapi 912.243s, trade 378.504s,
storage 35.910s). All three new integration race tests passed in 123.882s;
build, vet, staticcheck, formatting, YAML references, curve/reference-model
and checksum checks passed. No full-repository race or remote CI result is
claimed. The later C change only strengthens `progress_http_test.go`; its
focused/race recheck is attributed separately in the evidence. Other later
changes are documentation, evidence, checksum and CI selection. Production
Go and SQL remain unchanged at the production code baseline above.
C's updated progress tests plus the three integration tests passed in 3.326s;
the strengthened admission-history test passed race in 15.371s, with affected
package vet/staticcheck/format checks also successful.
The A PASS is not inherited by later B/C code or this integration as a whole.

Production broadcaster remains NOT_CONNECTED, controlled-canary authorization
NOT_GRANTED, live=false and release_ready=false. No real send, replay,
authorization grant, new chain/protocol, W5 or deployment change is in this
baseline assembly.
