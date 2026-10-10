# HTTP trading delivery — long-batch plan and stop boundaries

This is a local delivery plan, not a background task registration or runtime
authorization. The desired outcome is a normal one-request HTTP trading API
that reuses the completed RBH core. No client-side dry-run/prepare prerequisite;
internal quote/simulation and security gates remain required.

## Current checkpoints

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
  HTTP_C3_B READY_FOR_REVIEW, independent review NOT_PERFORMED. Full race
  trade 913.103s / tradeapi 998.718s. No self-PASS or C3-C entry.
- production_broadcast NOT_CONNECTED; controlled authorization NOT_GRANTED;
  live=false; release_ready=false. Other closed gates are unchanged.

## Work queue

| Order | Batch | Complete when | Requires attention |
|---|---|---|---|
| 1 | C3-A independent review + local closeout | independent evidence/checksum and docs commit, clean tree | Complete |
| 2 | C3-B compatibility/design inventory | units, data ownership, atomicity, legacy impact and test matrix documented | User scope acceptance recorded; independent review remains required |
| 3 | C3-B B1-B4 implementation/test hardening | atomic risk/budget proof at fixed head, no signing/send | Local author acceptance COMPLETE; C3-B READY_FOR_REVIEW, independent review pending |
| 4 | C3-B independent review / closeout | distinct reviewer PASS, separate docs/evidence commit | Reviewer authorization required if not already granted for that stage |
| 5 | C3-C default-off HTTP admission | one-request intake/validation/admission path in local harness | Requires its own scoped implementation/review |
| 6 | MANUAL execution/signing/submission adaptation | exact saved parameters integrate with existing core and multi-stage gates | Separate authority required; no real keys/chain sends implied |
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
