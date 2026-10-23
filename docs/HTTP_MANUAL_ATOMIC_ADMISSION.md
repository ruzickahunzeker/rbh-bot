# HTTP C3-B — disconnected atomic MANUAL admission

Implementation branch: `http-c3b-atomic-manual-admission`.
Implementation base: `a6922fdcd159e8ef41e6f67f1926743c4423beb6`.
Reviewed implementation: `02eb0494fae28da3e88f095c73ffc4f07820986e`.
Independent review: PASS; formal closeout: COMPLETE_LOCAL_ONLY.
Closeout branch: `http-c3b-admission-closeout`.
This supersedes the pending writer tasks in the earlier prerequisite work
order, not the historical validation recorded for those earlier commits.
HTTP_C3_B is PASS for disconnected B1–B4 admission only, not C3 as a whole.
No HTTP admission route or authorization grant is implemented, and no
production/execution/send composition is added. Delivery is local only: no
GitHub push, PR, merge, remote-main or CI claim.

## One command and the existing owners

Additive migration 020 leaves 001–019 unchanged. The immutable
`manual_admission_commands` table is an identity overlay, NOT a second balance
ledger or recovery state machine. A single INSERT's trigger creates:

1. Existing `canary_risk_reservations` and its immutable unit metadata.
2. Existing authorization operation usage and cumulative aggregate usage.
3. Existing admission decision/attempt and decision audit.
4. MANUAL-specific Operation/ExecutionStep statuses.
5. Runtime audit and alert outbox, including unit-binding audit/outbox.

`manual_admission_complete` joins the exact identities of all these rows. A
partial ledger is not admission. The final trigger check also rejects a skipped
constituent under an outer `INSERT OR IGNORE`. Trigger ABORT rolls back this
statement; the owner still rolls back its whole transaction on any error.
Forced failures after each financial/lifecycle/audit step prove this distinction.

Shared guards require the original command for declared MANUAL budget writes
and positive decisions. Standalone halves, UPDATE/DELETE/REPLACE of binding,
budget identity, decisions/attempts/audits and alert identity are rejected.
Alert delivery state may still change. Existing typed parent identity and
one-active-reservation-per-wallet protection remains in force. Metadata-only
foundation fixtures without a declared authorization remain metadata-only:
they cannot create positive decisions or consume authorization allowance.

The B1 no-budget/no-usage-update guards are replaced with coupled command
guards, retaining their names for compatibility. No-arm, no-permit and
no-PASS-snapshot guards remain. Real MANUAL authorizations therefore cannot be
armed by this slice; positive tests remove only no-arm in disposable databases.
No keys, external grants, operator service or real chain are involved.

## Exact accounting and immutable identity

The command binds original request/principal/operation/step/wallet, exact
canonical C3-A contract, request/result/policy hashes, quote/absolute expiry,
MANUAL policy, authorization ID/epoch and environment/build/release/deployment/
gate-evidence identity. Public C3-A remains initial-only and SELECT-only.

Go independently verifies canonical C1/C2/C3-A and policies before insertion
and while verifying history. Schema checks source equality and structural
coupling; it does NOT cryptographically verify SHA256. Registered uint256
add/compare/string-array sum are scalar primitives, not dimensionless permission
to mix assets. SQL exposure queries filter exact chain/asset/decimals/kind.
New pure `manual_time_shift_seconds` preserves nanoseconds for bounded rolling
windows; no REAL/julianday amount or fractional-time arithmetic is used.

One TRADE_INPUT unit per authorization is unchanged. BUY native input and SELL
token input are never combined. Gas/outlay limits are separate. All-asset
operation windows count reserved/frozen/committed/released history; active
exposure retains frozen rows regardless of age. Legacy untyped active/relevant
window history fails closed. Existing legacy raw-sum admission stays isolated
from typed history and MANUAL sources.

Nonzero authorization history is accepted ONLY when every operation has a
complete binding, independently verified immutable source/policy/auth identity,
the exact declared unit, and aggregate count/sum equal durable operation rows.
All RESERVED/CONSUMED/FROZEN/RELEASED history consumes allowance. Risk release
does not refund it. Max count/input equality is valid; one unit beyond rejects.
No reset, repair, relabeling or new expiry occurs during admission or restart.

## Writer and lifecycle boundary

The package-private writer owns one `sql.Conn` and BEGIN IMMEDIATE. It verifies
that connection's FK/FULL/WAL/busy-timeout controls; global scalar registration
is not assumed to restore connection-local PRAGMAs after pool replacement.
Stop is the first control lookup. Source/control/auth/policies/allowlists and
typed exposure are checked on that same snapshot. Exact durable dedupe is
resolved before rejecting the operation's own retained wallet reservation.

After the one command, the writer verifies complete binding and allowance
conservation, rechecks control/gates/authorization and absolute time, checks
time again after the final query/barrier, then COMMITs once. Equality with either
expiry, backward/zero/unverifiable time or COMMIT failure returns no provisional
result and rolls back. SQLite writer serialization is proven with a second
handle attempting actual stop/revoke/policy/epoch/deployment/gate writes in
the final commit window. Mutable writes succeed only after commit; immutable
identity edits remain rejected even then. A stop ordered after commit is not
claimed to retroactively invalidate that earlier admission; execution/pre-sign/
send must retain their independent gates in a later scoped integration.

First success returns `ADMITTED_NOT_SIGNED_NOT_SUBMITTED`. Exact duplicate
returns `HISTORICAL_ADMISSION_NOT_PERMISSION`, never renewed permission, even
after authorization revocation/expiry or order expiry. Emergency stop still
rejects the admission command; immutable/history SELECT helpers remain usable.
Duplicates/rejections do not create another economic decision or increment
allowance. There is no claim of durable per-transport-request metrics here.

Existing operation status becomes `manual_admission_succeeded`, and step status
becomes `manual_admitted`. They are excluded from legacy `Store.Recoverable`
and schema rejects MANUAL transition to generic admitted/executing. No downstream
MANUAL lifecycle adapter is connected. Synthetic statuses in earlier immutable
reader tests deliberately remove only transition guards in those fixtures;
positive admission tests use the complete real command/schema instead.

## Author acceptance evidence

See `evidence/http-manual-atomic-admission.json` for final-source validation,
hashes, commands and actual results. Tests cover:

- BUY, fixed/percentage SELL and zero tip, one-command complete ledger.
- Duplicate/address equivalence, real file close/reopen, unchanged contract/
  expiry, revoked/expired historical result without permission renewal.
- Scope conflicts and corrupt command/parent/usage/audit identity rejection.
- SQL half writes, manual lifecycle bypass, UPDATE/DELETE/REPLACE, ignored
  constituent and audit/outbox insertion failure.
- Eleven constituent fault windows, owner rollback/final TTL/canceled COMMIT,
  fresh reopen and successful same-source retry without orphan work.
- Count/input equality and +1; released allowance still conserved.
- Two-handle same-wallet contention and actual final writer-commit barriers.
- 10,000 requests, 16 callers, two handles on ONE file, 8 dummy wallets,
  32 distinct saved BUY/SELL operations plus duplicate/invalid scope/missing
  source requests. Four native BUY and three token SELL admissions fit their
  independent unit caps; the fourth token SELL exceeds the global token cap.
  Counters conserve all requests; accepted decisions equal complete durable
  reservations/usage; overcommit and unexplained accept are zero. Dedupe/reject
  totals are scheduling-dependent, not fixed expected production telemetry.
- Expired/stopped/revoked frozen history remains queryable and consumes
  allowance. This is NOT a fabricated receipt/recovery-health test.
- Import/call graph and empty economic-output tables prove no nonce/artifact/
  signer/submission/raw-send/permit/real-RPC paths in the new composition.

The author harness is neither CI nor independent approval. No GitHub actions,
push, PR, remote merge or background automation are performed. Historical
closed gates are unchanged, and do not preapprove these new shared-core guards.

## State and next boundary

```ini
B3 = ATOMIC_COMMAND_AND_DISCONNECTED_WRITER_IMPLEMENTED
B4 = AUTHOR_ACCEPTANCE_COMPLETE_INDEPENDENT_REVIEW_PASS
HTTP_C3_B = PASS
independent_review = PASS
formal_closeout = COMPLETE_LOCAL_ONLY
C3_C = NOT_STARTED
deadline_capability = APPLICATION_TTL_ONLY
contract_deadline = false
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Final-source ordinary/full race/build/vet/staticcheck/gofmt/diff and offline
validation passed. Full race: storage 57.716s, trade 913.103s, tradeapi 998.718s.
Ordinary: trade 98.849s, tradeapi 49.496s. No subsequent source changes; only
docs/evidence/checksum reflect those results. These remain author validation,
not independent reruns. C3-C HTTP admission, MANUAL execution/signing/submission
adaptation, operator authorization, BSC/Flap/Base and real live/sends remain
separate scopes; none is automatically entered here.

## Independent review and local closeout

The user-authorized separate reviewer `http_c3b_independent_review` read the
accepted design, B1/B2/accounting prerequisites, implementation, tests and
evidence at the exact reviewed SHA above and found no blockers. The reviewer
was not the implementing agent, but did receive forked author conversation
context; this is not claimed as a blind/no-context review. Start/end HEAD
matched the reviewed SHA and the worktree remained clean during review.

Independent execution passed:

- C3-B ordinary targeted regression: tradeapi 29.190s.
- Separate verbose 10,000 mixed two-handle conservation: 29.248s, accepted=7,
  deduped=1868, rejected=8125, unexplained=0, overcommit=0. Dedupe/reject counts
  vary with scheduling; all requests and durable unique usage conserve.
- `go test -race ./internal/tradeapi ./internal/storage -run '^(TestHTTPC3B3Atomic|TestHTTPC3B3FoundationLegacy|TestManualSchema)' -count=1 -timeout=5m`:
  tradeapi 118.194s, storage 3.579s. This focused race does not include 10,000.
- Storage full tests: 8.086s; manualrisk full tests: 0.017s; C07/W1/W2 targeted
  trade regression: 3.372s.
- Fixed HEAD/clean tree/diff checks, source hashes and package manifest:
  PASS (131 entries at the reviewed implementation baseline).

An overly broad race selection was stopped (exit 130) and is NOT counted as
PASS. The reviewer did not rerun full-repository tests/race or prove current
chain freshness, recovery health, HTTP/live integration or operator grants.
All execution used disposable SQLite/dummy authorization/local fixtures;
no real-chain RPC, signing or transaction sending was performed.

Historical `http-manual-atomic-admission.json` is preserved unchanged, including
its author READY_FOR_REVIEW/NOT_PERFORMED labels. Separate
`http-c3b-joint-review.json` records independent PASS, exact test evidence and
this local formal closeout. Runtime labels, code, migrations and configuration
are unchanged. Next is a separately scoped C3-C plan; neither HTTP composition
nor downstream MANUAL signing/submission is authorized by this closeout.
