# HTTP trading delivery — long-batch plan and stop boundaries

This is a local delivery plan, not a background task registration or runtime
authorization. The desired outcome is a normal one-request HTTP trading API
that reuses the completed RBH core. No client-side dry-run/prepare prerequisite;
internal quote/simulation and security gates remain required.

## Current checkpoints

Latest MANUAL checkpoint: M1 PASS; M2 independent PASS at
`03e333b03ce562ca72d03b0f86dc3e9be0e44216`, formal closeout COMPLETE_LOCAL_ONLY.
See `HTTP_MANUAL_M2_CLOSEOUT.md`. Earlier stage statements below retain their
historical meaning. M3-A is frozen; M3-B1 independent review PASS at
`5780e854e787a05076f7239796937921bc3c344a` and formal closeout
COMPLETE_LOCAL_ONLY. See `HTTP_MANUAL_M3_B1_CLOSEOUT.md`. This is not whole-M3
PASS or signer/send wiring;
production broadcast and authorization remain disconnected/not granted.

M3-A scope freeze is recorded in `HTTP_MANUAL_SIGNING_SCOPE.md`, based on local
M2 closeout `7e38c0cdb40bf043a3b9d66806de0ce56f433895`. Its original
implementation NOT_STARTED record is historical. M3-B1 on local branch
`http-m3-bound-signing-input`, base `7f75de8fda7f4d8d8bbfe5ff8a284b73ec667304`,
adds a disconnected SELECT-only bound input reader/pure nonce-free unsigned
envelope. See `HTTP_MANUAL_BOUND_SIGNING_INPUT.md`. It preserves complete M2
binding, current controls/auth/TTL/allowlists and conserved original accounting;
the output is detached candidate metadata with ALL permission flags false.
Snapshot stop races are explicitly not writer fencing. There is no schema,
signer, nonce, artifact, RPC or send effect, and no HTTP/config/startup wiring.
Original author checkpoint M3_B1_READY_FOR_REVIEW / review NOT_PERFORMED is
historical and preserved unchanged. Separate stage-specific reviewer authority
was granted; independent B1 review PASS and local formal closeout now record
M3-B1 PASS. Next candidate requires separate B2 implementation authorization;
B2/B3/B4 and M4/M5 are not started or authorized by this closeout.
No production lifecycle/live/authorization or remote operation is implied.

Subsequent user authorization covers M3-B2 only. Local
`http-m3b2-nonce-lifecycle`, base `09176c1143ad62ebb40cb0a58ad6ac00f68ec874`,
adds 027 and the disconnected controlled-local nonce claim writer/pure query
observation validator. See `HTTP_MANUAL_NONCE_LIFECYCLE.md`. One command owns
one existing step/lane/attempt plus audit/alert; no second risk/budget owner,
signer/cipher/artifact/permit/send or HTTP/config/startup dependency. Original
001–026, M1/M2/B1 and their initial-phase views remain unchanged; only exact
named guard exceptions in the additive migration allow one claimed phase.
Current B2 maximum checkpoint M3_B2_READY_FOR_REVIEW, independent review
NOT_PERFORMED. Earlier B2 NOT_STARTED text is the historical B1 closeout state.
Do not reuse B1 reviewer permission: next action requires separately authorized
read-only B2 independent review, not B3 implementation or automatic closeout.

Final B2 author validation on frozen source: full ordinary Go tests PASS
(trade 120.281s / tradeapi 166.588s / storage 10.079s), all 20 B2 tests plus
five pure/scalar/upgrade tests single-run race PASS (414.887s / 1.023s /
3.179s), and five critical B2 tests plus upgrade in three serial race runs
PASS (185.145s / 7.541s). Build/vet/staticcheck, 39 offline model tests,
five Curve fixtures, source hashes/mappings and the 149-entry manifest PASS.
Full-repository race was not run or claimed. Initial compilation/lint/static
guard failures, parallel fixture timeouts, mixed-source runs and subsequent
author alias/clock hardening are retained in additive evidence, not relabeled
PASS. Current checkpoint remains B2 READY_FOR_REVIEW, not independent PASS.

Final B1 author validation: full ordinary Go tests PASS (trade 177.256s /
tradeapi 154.885s / storage 11.469s); all 16 B1 tests in three consecutive race
runs PASS (715.637s); nine selected M1/M2/C3 compatibility race tests PASS
(185.661s); W4-C SQLite reopen race PASS (30.074s). Build/vet/staticcheck,
39 offline reference-model tests, five Curve fixtures, all source hashes/test
mappings and the additive 147-entry manifest PASS. Full-repository race was
not run; no author independent-review or production telemetry claim. Earlier
fixture failures are recorded, not relabeled PASS. That was the author checkpoint,
not the subsequent independent review.

B1 independent reviewer validation: all 16 ordinary tests PASS (14.094s),
single focused race PASS (186.047s), nine selected M1/M2/C3 ordinary tests PASS
(10.143s), W4-C file reopen ordinary PASS (0.879s), 17 extra durable corruption
overlay probes PASS (4.077s), and final overlay rerun including exact nonzero
tips for all three trade forms PASS (3.527s). Package vet, 147 reviewed
checksums, source hashes and mappings PASS; no independent full suite/race/
staticcheck, real-chain integration or production telemetry claim. Existing
compiled package dependencies are not confused with reader capability absence.
Separate docs/evidence/checksum closeout on `http-m3b1-bound-input-closeout`
preserves historical author records and adds `http-m3b1-joint-review.json`.
The manifest had 148 entries at B1 closeout. B1 PASS grants no nonce/signing/send
permission; subsequent local-only B2 authority is recorded above, not implied
by that closeout. Live/release readiness remain unchanged.

- HTTP C1/C2 implementation is local; C2 closeout records independent PASS.
- C3-A independent review PASS at a3a5f47b176032f223a9c5db460f309fec06df35.
- C3-A docs/evidence closeout is local commit
  6ed6ea60fcbad7d813ae88ff709ffe2f8d6626f2.
- C3-B B1-B4 unit/accounting implementation scope accepted by the user:
  one input unit per MANUAL authorization; additive shared compatibility only.
- B1 pure arithmetic and isolated metadata implemented on
  http-c3b-b1-unit-schema; ordinary tests, focused race, build/vet/staticcheck
  passed. Full repository race subsequently passed (trade 623.476s);
  independent review NOT_PERFORMED.
- B2 disconnected transaction-local inputs implemented on
  http-c3b-b2-transaction-inputs; ordinary tests and final affected-package/
  B2 three-run race passed. Combined C3-A/B2 three-run race rerun also passed
  (244.004s); final ordinary tests/build/vet/staticcheck/validation passed.
  No independent review/self-PASS.
- B3 prerequisite on http-c3b-b3-accounting-foundation adds migration 019 typed
  reservation metadata, same-writer SELECT-only exposure verification and
  explicit bidirectional legacy-mode isolation. See
  HTTP_MANUAL_ACCOUNTING_FOUNDATION.md. Complete B3/B4 atomic admission and
  C3-C/signing/send adaptation are not yet implemented; HTTP_C3_B NOT_READY.
  Final ordinary/full repository race passed (trade race 621.191s), as did
  three focused race runs, build/vet/staticcheck and validation. No independent
  review/self-PASS; budget/arming/permit isolation remains closed.
- GitHub ignored by request: no fetch/push/PR/merge or remote-main claims.
- Atomic B3 continuation on http-c3b-atomic-manual-admission adds 020's coupled
  command/writer, nonzero allowance conservation, SQL lifecycle/half-write
  protection and B4 fault/commit-window/10,000 mixed-request harness. See
  HTTP_MANUAL_ATOMIC_ADMISSION.md for current validation/review state. This is
  disconnected local admission, not C3-C HTTP/execution/send wiring. Earlier
  prerequisite checkpoints above remain historical, not current ready claims.
  Final author ordinary/full race/build/vet/staticcheck/validation passed;
  historical implementation evidence retains HTTP_C3_B READY_FOR_REVIEW and
  independent review NOT_PERFORMED. Full author race trade 913.103s / tradeapi
  998.718s; this is not an independent full-race claim.
- C3-B independent review PASS at
  02eb0494fae28da3e88f095c73ffc4f07820986e; formal closeout COMPLETE_LOCAL_ONLY
  on http-c3b-admission-closeout. Separate http-c3b-joint-review.json records
  reviewer ordinary/focused race, 10,000 conservation and scope limitations.
  Reviewer received forked author context but was separate from implementation.
  HTTP_C3_B PASS covers disconnected B1–B4 admission only, not C3 as a whole.
  No C3-C, signing/submission adaptation, operator grant or remote activity.
- C3-C continuation on http-c3c-default-off-admission uses local C3-B closeout
  e33c0bc32280c6599b818148913c6d55b4b71cfe. See
  HTTP_MANUAL_ADMISSION_HTTP.md for the default-off UDS composition, server-owned
  scope, historical projection and atomic admission boundary. No migration,
  arming/permit/signing/send/recovery changes. Original checkpoint was local
  author READY_FOR_REVIEW, not scoped PASS or all-C3 PASS.
  Final author full ordinary tests, three focused race runs, broader C1/C2/C3
  compatibility race (including 10,000), transport race, build/vet/staticcheck,
  YAML/reference checks and offline validation passed. No full-repository race
  or independent execution is claimed for C3-C. Evidence package: 133 entries.
- C3-C independent review at e3b1c504ed4d9bc17468e6c03b17d74c60a581a5
  concluded FAIL: late cancellation/DB read errors were incorrectly mapped to
  verified rejection (422), not operational unavailability (sanitized 503).
  Local http-c3c-operational-error-hardening preserves causes in shared MANUAL
  verifier reads and adds per-read fault regressions without changing ledger,
  risk, TTL, gates, schema or economic dependencies. Original author evidence
  remains historical and unchanged. Historical author hardening checkpoint was
  READY_FOR_REVIEW pending fresh independent review.
  Final hardening full ordinary, C3-C/baseline negative-path focused race,
  three-run new-fault-test race, build/vet/staticcheck and offline validation
  passed. Evidence package now has 134 entries. No full-repository race or
  independent hardening PASS is claimed by that author checkpoint.
- C3-C fresh independent review PASS at
  500735151be1ca9fec59ac4bf7c258bbb88cd025; formal closeout COMPLETE_LOCAL_ONLY
  on http-c3c-admission-closeout. Separate http-c3c-joint-review.json records
  selected reviewer ordinary/race/compatibility probes, the former cancellation
  failure now returning sanitized 503, and retained typed rejection/usage/TTL.
  The reviewer initially started with no forked author conversation and then
  received a scoped re-review handoff; no blind-hardening-review claim is made.
  HTTP_C3_C PASS covers default-off UDS admission/history only, not all-C3 PASS,
  real grants, execution/signing/submission or live. Evidence package: 135 entries.
- production_broadcast NOT_CONNECTED; controlled authorization NOT_GRANTED;
  live=false; release_ready=false. Other closed gates are unchanged.

### Current MANUAL adaptation checkpoint

M1 on `http-m1-admitted-execution-input-reader`, base
`708163355be0b05e247d9597ee38951dabe1ed86`, adds only a disconnected read-only
admitted input reader. See `HTTP_MANUAL_ADMITTED_INPUT.md`. A candidate is NOT
execution/sign/send permission; original parameters/expiry and consumed C3-B
budget remain unchanged. Maximum status `M1_READY_FOR_REVIEW`; independent
review was NOT_PERFORMED at that checkpoint, no self-PASS/closeout. Subsequent
user authority covers read-only M1 independent review only, not later stages.
M2–M5 remain unimplemented and separately scoped. Local-only work: no GitHub,
schema/API/startup wiring, real authorization, signer/send/RPC or live changes.
Final author full ordinary, all 14 M1 tests in three consecutive race runs
(220.719s), selected C3-B/C3-C compatibility race, build/vet/staticcheck and
offline validation passed. Full-repository race was not run. Evidence package
has 136 entries; historical evidence remains unchanged.

M1 independent review of `e0554187ecc5d664a7103f88063f3d526a18a692` concluded
FAIL: public Load's commit-window context cancellation could return ErrTxDone
without the cancellation cause (zero output/503/no writes still held). Local
`http-m1-commit-cancellation-hardening` preserves both causes in that branch
and adds a deterministic public cancellation/deadline regression. Scope is
M1 error handling/tests/evidence only; fresh M1 independent review must follow
before any closeout. Original author evidence is retained, not overwritten.
Final author hardening full ordinary (trade 149.900s / tradeapi 117.063s), all
15 M1 tests single-run race (196.327s), new public cancellation/deadline three-run
race (55.679s), selected compatibility race, build/vet/staticcheck and offline
validation passed. Regression with the old reader source fails both causes,
confirming the original defect. Package has 137 entries; no full-repository race
or author PASS is claimed. Maximum status remains M1_READY_FOR_REVIEW pending
fresh independent review.

M1 fresh independent review PASS at
`8466c83960f651730c4d7a7c48d6d709ea1c037b`. Formal closeout COMPLETE_LOCAL_ONLY
on `http-m1-admitted-input-closeout`; additive `http-m1-joint-review.json`
records the former FAIL, cancellation/deadline closure, reviewer probes and
actual independent validation. Historical implementation/hardening evidence
remains unchanged. M1 PASS covers only the disconnected read-only admitted
input reader, not execution/sign/send permission. Evidence package: 138 entries.
At that closeout, the next step was M2-A read-only scope freeze; M1 review did
not itself authorize M2-B. Subsequent user continuation authorized the frozen
metadata-only M2-B slice, recorded below. No remote/live change.

M2-B on `http-m2-durable-execution-binding`, exact base
`20695c5de5f7beb99ef6346d4d21a061cf1d5b72`, implements additive migration 021
and a disconnected atomic binding writer. See `HTTP_MANUAL_EXECUTION_BINDING.md`.
It preserves the admission fact/validation step, adds one exact execution step
and claims the existing wallet lane without nonce, artifact, second financial
reservation or extra budget. Binding/step/lane/audit/alert are one atomic effect;
same-writer final controls/auth/TTL and scope/ledger checks fail closed.
Exact complete duplicates are history-only, never execution/send permission.
The author batch stops at M2_READY_FOR_REVIEW; no independent M2 review or
self-PASS/closeout is implied. Stage-specific reviewer permission is required.
No HTTP/config/startup wiring, M3–M5, signing/send/RPC/mainnet or real grant.
Author full ordinary, all 17 M2 tests plus upgrade in three-run race
(tradeapi 747.703s / storage 14.156s), build/vet/staticcheck and offline validation
passed. Initial focused race 15-minute timeout and full-repository race
25-minute trade/tradeapi timeouts are retained in additive evidence, not relabeled
PASS. Full race remains an explicit independent-review validation limitation.
Seven selected M1/C3-B/C3-C compatibility race tests plus the separately
selected public M1 cancellation/deadline regression passed. New evidence is
additive; historical M1/C3 records remain unchanged.

M2 independent review of `46270e8195eb76758bf978e36bb28d40b4835d06` concluded
FAIL: direct SQL legacy reservation/lane writes could reference the bound step
under another operation, and positive-only completeness missed the pollution.
`http-m2-binding-integrity-hardening` starts from that exact head and adds only
022 guards, the disconnected verifier's strict-view check, tests/docs/evidence.
See `HTTP_MANUAL_BINDING_INTEGRITY_HARDENING.md`. Original 001–021 and historical
author evidence remain unchanged. Maximum checkpoint M2_READY_FOR_REVIEW;
fresh independent review NOT_PERFORMED, no self-PASS/closeout or M3–M5.
No HTTP/startup/chain/sign/send/authorization/live or remote wiring changes.
Author hardening full ordinary and all 23 M2 tests plus both storage upgrades
in three serial race runs passed (tradeapi 477.145s / storage 9.674s), as did
build/vet/staticcheck and offline model/Curve checks. Full-repository race is
not rerun or claimed PASS; the original timeout limitation remains explicit.
Eight selected M1/C3-B/C3-C compatibility race tests (56.424s) and W4-C SQLite
reopen race (4.646s) also passed. Additive evidence/checksum package has 140 entries.

Fresh M2 independent review of that committed 022 fix at
`b47239dd07dbf62c7ac4af7c0acd1c6737e0608e` concluded FAIL: UPDATE OR REPLACE
could delete a different protected unique-key conflict victim, erase retained
pollution and make the strict view accept history. Earlier NOT_PERFORMED text
above is the historical author checkpoint, not current review status.
`http-m2-update-replace-hardening` begins at that exact head and adds only
migration 023, residual tests and docs/evidence/checksum. It covers financial
operation/step/rowid and lane wallet/address/rowid victims, including explicit
rowid INSERT OR REPLACE, independently of recursive trigger mode. Old migrations,
production Go writer and historical evidence remain unchanged. See
`HTTP_MANUAL_UPDATE_CONFLICT_HARDENING.md`, including its conservative rowid -1
compatibility limitation. Highest checkpoint M2_READY_FOR_REVIEW; new residual
fresh independent review NOT_PERFORMED, no author PASS/closeout, M3 or remote work.
Final-source full ordinary and focused tests, seven residuals plus the 022
upgrade in three race runs (tradeapi 235.317s / storage 6.044s), eight selected
M1/C3 compatibility race tests (57.453s), W4-C reopen race (7.266s), lint/build
and offline checks passed. All 30 M2 tests and three upgrades passed one race
run before a test-only unused assignment cleanup; final residual reruns cover
that cleanup. Full-repository race is not rerun or claimed PASS. New additive
evidence records exact commands, hashes and limitations; manifest has 141 entries.

At committed `3be7a805e693f7973d9224aaf7fd812003a7e911`, the full fresh review
remained BLOCKED after reviewer tooling interruption. A separate independent
read-only alert/audit static subreview concluded FAIL, not a full-M2 verdict or
runtime reproduction. It found missing alert audit_id/rowid conflict-victim
protection and a composed path altering audit history accepted by the old view.
User-authorized `http-m2-audit-alert-hardening` starts from that exact head and
adds migration 024, tests/docs/evidence/checksum only. It preserves 001–023 and
the production Go writer, upgrades the existing strict view transactionally,
protects M2 audit/alert identities and rejects observably altered history.
See `HTTP_MANUAL_AUDIT_ALERT_HARDENING.md` for limits. Highest checkpoint remains
M2_READY_FOR_REVIEW, fresh independent review NOT_PERFORMED; no author PASS,
closeout, M3, HTTP/startup/send/live or remote changes.

024 final-source author validation: full ordinary Go tests, build, vet and
staticcheck PASS; all 42 M2 tests plus four binding upgrades focused race PASS
(482.100s / 7.549s); three critical audit tests plus the 023-to-024 upgrade
three-run race PASS (103.475s / 6.233s). Eight selected M1/C3 compatibility
race tests and the W4C SQLite reopen race also PASS (72.269s / 17.401s).
The additive manifest now has 142 entries. Full-repository race was not rerun
and is not claimed PASS; prior timeouts and intermediate failures remain in
evidence. These author checks do not advance M2 past READY_FOR_REVIEW.

Fresh independent review at `5010258ab7ad2ef889f376bb280809f2087d809b` remained
BLOCKED_REVIEW_INCOMPLETE after tooling interruption. It first reproduced loss
of an immutable binding through implicit-rowid INSERT replacement with FK ON
and recursion OFF; existing focused race PASS did not close that product defect.
The authorized `http-m2-binding-rowid-hardening` slice adds 025 and permanent
regressions only; prior migrations/production writer/history remain unchanged.
See `HTTP_MANUAL_BINDING_ROWID_HARDENING.md`. Stop at M2_READY_FOR_REVIEW for a
new independent review, not author PASS/closeout or M3.

025 final-source full ordinary tests PASS; all 48 M2 tests and five upgrades
focused race PASS (638.830s / 9.338s). Six new tests plus the 024 upgrade
three-run race PASS before an unrelated test-journal assertion fix, then rerun
within final M2 race. Selected eight M1/C3 and W4C reopen race PASS; build,
vet, staticcheck, offline checks and 143-entry manifest PASS. Initial sandbox
socket failure, old hardcoded-journal assertion and interrupted pre-final runs
remain explicit in additive evidence. No full-repository race PASS is claimed.

The completed independent M2 review at `c59e13e342dfe1fb2d9ad986ac817629aee7ef05`
concluded FAIL on a static retained address-only lane blocker. The subsequent
local `http-m2-retained-lane-hardening` slice adds 026 OLD-update/DELETE relevance
protection, with reachable actual 021 pre-binding alias negative controls,
unchanged retained history and exact idle-claim compatibility. See
`HTTP_MANUAL_RETAINED_LANE_HARDENING.md`. Author checkpoint remains
M2_READY_FOR_REVIEW pending fresh independent review, not PASS or M3.

## Work queue

| Order | Batch | Complete when | Requires attention |
|---|---|---|---|
| 1 | C3-A independent review + local closeout | independent evidence/checksum and docs commit, clean tree | Complete |
| 2 | C3-B compatibility/design inventory | units, data ownership, atomicity, legacy impact and test matrix documented | Complete; integrated independent review PASS |
| 3 | C3-B B1-B4 implementation/test hardening | atomic risk/budget proof at fixed head, no signing/send | Complete at reviewed 02eb049; no HTTP/signing/send |
| 4 | C3-B independent review / closeout | distinct reviewer PASS, separate docs/evidence commit | Complete, local only; historical implementation evidence unchanged |
| 5 | C3-C default-off HTTP admission | one-request intake/validation/admission path in local harness | Complete; fresh independent PASS and separate local docs/evidence closeout |
| 6 | MANUAL execution/signing/submission adaptation | M1 reader, M2 durable binding, separately scoped M3–M5 | M1/M2/M3-B1 PASS/local closeout; M3-A frozen; B2 READY_FOR_REVIEW, independent review NOT_PERFORMED; next separately authorized B2 review; B3/B4/M4/M5 not started, no signer/send/live permission |
| 7 | HTTP status/events and recovery mapping | stable historical/current status, receipt/reorg visibility, failure semantics | No new economic route or recovery-send dependency |
| 8 | Controlled release/readiness acceptance | authentic deployment/operator authorization and limits reviewed | Explicit real signing/sending/live authorization, never automatic |
| 9 | BSC/Flap, then optional Base | independently scoped chain/protocol adapters behind same user contract | No Solana; not part of current Robinhood slice |

## Batch execution discipline

Within an explicitly authorized implementation scope, continue through
investigation -> minimal patch -> focused tests -> fault/concurrency evidence ->
regression/race/lint -> docs/checksum -> local commit/handoff, without asking
after each successful command or test. Fix in-scope failures before reporting.
Do not finish a turn solely because one substep is green if authorized work
remains. Commentary updates are informational, not requests for confirmation.

At the beginning of each batch verify project, HEAD, branch, worktree and
applicable AGENTS. Do not overwrite user edits or silently change an unrelated
branch. Use explicit file targets and preserve historical evidence. Record
actual author vs reviewer commands and limitations separately. A green test
does not replace independent review, and author work never self-labels PASS.

Only ask for a material decision/authority change: incompatible shared/closed
gate semantics, new network/deployment credentials, real authorization/signing/
send/live, changed chain/protocol scope, destructive action or unavoidable user
work conflict. Technical transient test failures alone are not permission
questions. GitHub repository creation remains out of scope until requested.

## Resume/checkpoint protocol

Resume from the latest clean local checkpoint, not an old remote main. Read
this plan plus the stage-specific document; verify the commit ancestry and
current changes. Advance only the first authorized incomplete work package.
If that package is blocked on scope acceptance, exhaust safe design/evidence
checks, state the SINGLE missing decision, and do not proceed by guessing or
hide it behind endless reruns. Keep recovery safety and live=false throughout.

Each batch handoff records branch/base/head, changed files, test commands and
actual results, remaining work, current state, and prohibited path counts or
the precise structural proof instead of invented production telemetry.

## Background execution status

The current session has no exposed automation_update/scheduled-task creation
tool. No background task, cron, daemon, auto-wakeup or continuous host process
has been created. Ending a turn does not imply automatic resumption.

When a supported product scheduler is available, use that scheduler, not a
custom script. Recommended prompt for a chat-continuation task:

> Work only in /root/http/rbh-bot. Read AGENTS, verify HEAD/worktree and the HTTP
> delivery plan. Continue the first explicitly authorized incomplete package,
> including tests, evidence and local handoff. Do not expand scope, grant
> authorization, sign/send, access real-chain RPC, enable live, push/create PR,
> or mutate closed gates without explicit permission. Remain quiet when no
> actionable state changed; notify only on meaningful completed batch, failure
> or a material decision/authority requirement. Stop on completion or a required
> user decision; do not create a parallel worker or loop indefinitely on an
> unchanged blocker. Independent reviewers require explicit stage authorization.

Scheduler cadence and activation are not configured by this document. Any
future local scheduled task also requires the desktop app/computer and project
to remain available; account availability and sandbox permissions are separate
from this repository's trading admission permissions.
