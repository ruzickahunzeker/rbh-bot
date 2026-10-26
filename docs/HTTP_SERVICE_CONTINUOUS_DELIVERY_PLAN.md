# HTTP trading service — continuous delivery batches

Local planning baseline: `a562adef68db7ee378562b195dba9b334a029065`.
A1 closeout is COMPLETE_LOCAL_ONLY; this is not a new formal main baseline.
Branch: `http-a2-execution-connection-plan`. Status: `CONTINUATION_PLAN_PROPOSED`.
The user requested a plan and continued execution within boundaries. This batch
does design/source inspection only; no later implementation or real-action
authority is inferred. No automation, scheduled monitor or detached background
job is registered by this document.

## Delivery objective and what is already finished

Deliver one authenticated POST with ordinary-user amounts/fee in gwei and
optional limits, then verified GET/events. No required client dry-run/prepare/
sign round trip. Retain internal validation, immutable TTL and all stage gates.

Existing reviewed work is reused: intake/admission, verified history/events,
private first-submission executor, local first-order HTTP harness, original
recovery/canonical/reorg owners, and A1 recovery-only startup. Do not rebuild
them or count documentation churn as new trading capability.

Two product checkpoints are distinct:

- **Controlled first-order production candidate:** production connection under
  separate authority, retaining current wallet-generation exclusion. Not an
  unrestricted or repeatable service.
- **Continuously reusable same-wallet service:** additionally requires B's
  accepted/reviewed risk, asset-provenance and reorg model. This is unresolved.
  Neither a successful first order nor policy/key rotation works around it.

No percentage here is a code/effort estimate. Track concrete tested capabilities,
exact commit/review heads and unconnected boundaries instead.

## Work allocation and ordering

| Batch | Owner and deliverable | Dependency / stop boundary |
| --- | --- | --- |
| A2 | Private typed first-admission continuation contract and separately accepted implementation | No consumer/signing/permit/send connection; current detailed design in HTTP_A2_FIRST_ADMISSION_HANDOFF_SCOPE.md |
| A3 | Production dependency composition for existing signing, measured identity, bounded clock and original verifier, exercised locally | Accept custody/manifest/time-provider contract and exact private-boundary exceptions first; no production sender or real grant |
| A4 | Operator-only durable MANUAL authorization protocol | Separate authority/schema scope: original no-arm is not silently bypassed; additive migration only, no HTTP grant or real activation |
| A5 | Exact-byte first-send adapter and default-disabled production HTTP composition | Reviewed A2/A3/A4 contracts; controlled JSON-RPC tests only until real-action authorization; never wire sender into recovery |
| C integration | Public request/response/wait/error contract and adapter tests, then real composed response integration | Can design/test presentation in parallel; action integration follows A's typed interface; no separate result owner |
| Combined acceptance | Exact assembled local code, fault/duplicate/cancel/restart matrix and independent review | Review results apply to exact heads and scoped capability, not automatic activation or closeout |
| Deployment / controlled first order | Operator-owned credentials, identities, allowlists/caps, monitoring/disable and explicit real-action approval | No real RPC transaction, funding action or mainnet send without this separate grant; review/CI is not a grant |
| B reuse implementation | Selected model, additive schema and genuine multi-generation/reorg evidence | Cannot begin by removing the current restriction; explicit C07/asset-dependency decision first |

These identifiers organize future HTTP work; they do not reopen the previously
frozen W1/W2/W3/W4 or M-series gates. An implementation batch starts only after
its exact scope and required design/review decisions are accepted.

## Parallel lanes without shared-state conflicts

B reference remains `http-b-wallet-generation-design@f5ef62f` and C reference
`http-c-production-http-contract@0cbe3fd`. Neither branch is merged/edited here.
Their previously inspected design conclusions remain scoped; new code needs its
own review. The common next integration base must include A1, not restart from
old independent source worktrees.

- A owns admission handoff/shared constructors, production dependency graph,
  startup/config/lifecycle and final assembly. Keep the existing RECOVERY_ONLY
  profile action-free; future HTTP execution belongs to a separate explicitly
  reviewed composition, not an extra route added to A1.
- C owns user-facing contract/OpenAPI/presentation tests. It can test existing
  202/history/Location/deduplication/error semantics without B's model. Proposed
  10s/30s and timeout-202 are not current promises. A/C must select finite server
  deadlines and cancellation semantics before execution-capable integration.
- B owns wallet-generation risk/nonce/reorg facts and later schema. Current
  operative choice is one retained MANUAL generation per wallet/address, not a
  new reusable-wallet model. B research can proceed without blocking the first
  controlled connection design, but repeated trading remains explicitly absent.
- One owner changes each shared file/SQL transition in a batch. Integrate pinned
  commits in dependency order; combine checksum entries deliberately rather
  than independently replacing the common manifest. Do not merge old 5329f28.

## Continuous execution rule

Within an accepted implementation scope, finish a substantial batch without
asking after every small step: inspect -> implement -> focused ordinary/race
checks -> fix in-scope failures -> required regression/static checks -> evidence
and checksum -> local commit and review handoff. Do not repeatedly rerun the
expensive full suite for documentation-only changes. Preserve every failed or
interrupted result and distinguish final-head tests from predecessor evidence.

Pause only at meaningful gates: new scope/invariant/schema/custody/transport
authority, an unresolved material design choice, independent review requiring
its own authorization, formal closeout, external push/PR/merge, or real-action
deployment approval. A terminal request to keep going does not erase those
boundaries. No self-issued review PASS or silent feature expansion.

Do not create unattended jobs or promise work will continue after the active
turn ends. Long-running checks started during a batch must be tracked to a
result or handed off explicitly; unchanged state does not manufacture progress.

## Common acceptance and non-negotiable boundaries

Each action-capable scope must prove one original source/operation/artifact,
one first admission owner, immutable amount/fee/TTL/nonce, current authorization
and deployment identity, independent stage gates and immediate pre-send
stop/lease recheck. Preserve the marker ambiguity rule and unknown freeze.
No automatic resend/rebuild/resign/replacement/fee bump/repair nonce. Unknown
replay remains a distinct future query-first/exact-bytes/fresh-permit scope.

Recovery remains independent of current send permission. Durable budget/risk/
audit/outbox/event witnesses retain their original owners and atomicity. HTTP
success cannot assert canonical success, and a public capability boolean is
never execution authority. Do not add a second result machine or balance ledger.

Current delivery remains chain 4663 / Pons v2 Curve. No BSC/Base/Solana/new
protocol/strategy/Long/LP, public TCP listener, contract deadline, unrestricted
live, permission bypass or legacy gate rewrite. The existing local UDS is the
initial transport; remote bot access/TLS/proxy exposure needs deployment scope.

This planning batch has no production source/test/schema/config changes,
production-key signing, real sends/mainnet calls, grant, push/PR/merge or
next-stage activation.
Three inherited local POST baseline tests were rerun, covering fresh-admission
ownership, two-handle duplicates and fault/reopen no-resume (PASS, 14.067s).
They use fixture signing/fake sends; they are not A2 implementation acceptance
or proof of zero fixture signing. Exact attribution belongs in additive
`evidence/http-a2-execution-connection-plan.json`.
Production broadcast=NOT_CONNECTED; authorization=NOT_GRANTED;
live=false; release_ready=false.
