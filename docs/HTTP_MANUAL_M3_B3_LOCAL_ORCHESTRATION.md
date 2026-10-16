# M3-B3-C — disconnected controlled-local signing orchestration

Branch: `http-m3b3-controlled-local-signing`.
Batch base: `bec9ee02c6bf32e2e76e3d1158b82f87c717f889`, the local B3-B2
artifact-phase review checkpoint. Subsequent continuation authorizes this
controlled-local implementation, not production signing or send composition.

```ini
M3-B3-C = INDEPENDENT_SCOPE_PASS_AT_CE9F46A
M3-B3-D = NOT_COMPLETE
M3-B3 = IN_PROGRESS_NOT_READY_FOR_REVIEW_NOT_PASS
M3 overall = INCOMPLETE_NOT_PASS
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Independent B3-C review **PASS** at
`ce9f46a08b1e1403171ad9f603937f98640508d4`, limited to this disconnected
orchestration stage. Independent all-C ordinary, selected race and 25 original
probe cases passed. See additive local-orchestration-review evidence. Original
author evidence below retains its historical pending-review/source-hash meaning;
this checkpoint does not close whole B3 or authorize production signing/send.

## Composition and input

`manualLocalSigningWriter` is package-private with no constructor, HTTP/config/
startup caller, production adapter or key loader. It accepts the original
durable request identity and operator-owned scope, never nonce/calldata/fee/
expiry/owner/hash overrides. `internal/manualsign` has only a structural signer
interface compatible with the existing trusted local signer, exact artifact
primitives and a synchronous call. No trade service is imported by the new
product code. Tests alone supply generated ephemeral keys and controlled
query-only observations. No real key, authorization grant or endpoint is used.

The original B2 nonce is already claimed. One operation, step, attempt, lane,
risk reservation and consumed lifetime allowance remain authoritative. No
percentage re-resolution, quote, nonce allocation, fee default, tip override,
TTL renewal, authorization window, second financial owner or refund is added.
Scope remains Robinhood 4663 / Pons v2 Curve / native quote, BUY/fixed SELL/
already-resolved percentage SELL; APPLICATION_TTL_ONLY, contract_deadline=false.

## First-call and fence ordering

1. One read snapshot checks retained artifact/marker rows first. Any retained
   command blocks new action, even when incomplete/corrupt; no history repair.
   Complete artifact history authenticates/decrypts exact original bytes and
   returns metadata only. Complete marker history is signing ambiguity only.
   These reads do not require active controls, observer, signer or clock.
2. A new action requires an original complete prepared claim, emergency stop
   first, exact scope/deployment/source/policy/durable evidence identities,
   active authorization, reserved risk/usage, conserved lifetime usage/caps,
   enabled wallet/contract/token allowlists and exclusive original wallet lane.
   Initial TTL and signer address checks precede the controlled observation.
3. A bounded query-only observer runs outside the writer. Its result must match
   the original nonce, chain, runtime and canonical block facts, with no pending
   nonce divergence, stale/future time or observation older than this read.
4. `BEGIN IMMEDIATE` repeats the authoritative controls/source/claim/accounting
   checks. A fresh random 32-byte owner belongs only to this stack frame. Exact
   marker/audit/alert insertion and time/evidence checks precede marker commit.
   If another caller already marked/stored work, return history without signing.
5. After marker commit, a SECOND `BEGIN IMMEDIATE` repeats all fresh controls,
   immutable identities and original ownership checks. The marker must exactly
   match this uninterrupted first call's in-memory owner/facts/time. No API
   accepts an owner token; reading the owner later cannot resume invocation.
6. The protected signer adapter checks address and exact transaction fields,
   then calls the writer's freshness check immediately before its ONE signer
   invocation. The writer fence spans synchronous signing, exact raw verification,
   encryption and verified artifact outcome commit. No signer goroutine/retry.
7. After crypto, fresh controls, claim, accounting, time and evidence are checked
   again. Additive 036's existing command performs exact original attempt/step/
   lane/audit/alert effects atomically. Historical decrypt/verify plus final
   cancellation/time/TTL/evidence checks must pass immediately before outcome
   COMMIT. A valid committed artifact is historical metadata: later TTL/clock
   changes do not create fresh permission or invalidate identity inspection.
   Cancellation before returning a result is still checked separately.

The per-call clock rejects zero/out-of-range times and ANY reversed sample,
even if a later sample recovers. `now == expires_at` rejects; authorization
expiry and observation freshness are also rechecked. SQLite source times remain
writer evidence, not an independent clock or signing permit.

## Ambiguity, cancellation and local crypto

Marker commit means absence of signer invocation can no longer be proved after
restart. It does NOT prove signing happened. Any second-fence failure, stop/
revoke/expiry, signer/cipher error/panic, cancellation, clock reversal, malformed
signature, SQL failure or response loss returns zero usable output and retains
the original marker/claim/lane/accounting. This is SIGNING ambiguity, never
broadcast_unknown: there is no broadcaster in this graph. No automatic second
sign, artifact manufacture, repair, release, rebuild, replacement, fee bump or
new nonce follows.

The signer/cipher sequence has a one-second child context and is synchronous.
Trusted local implementations must honor bounded operation. If an implementation
returns late, its output is rejected; the caller still waits and holds the fence
until it returns. The code cannot forcibly preempt an untrusted blocking signer
and never abandons an orphan goroutine. Context cancellation has precedence over
crypto errors/panic. Raw buffers owned here are cleared; no secure erasure of
Go/crypto-library transaction copies is claimed. Cryptographic failures use fixed
sanitized errors, not signer/cipher-provided secret text. No raw bytes leave the
adapter or historical metadata result.

If artifact COMMIT succeeds but its response is lost/cancelled, a later call
inspects that exact immutable artifact; it does not invoke the signer or observer.
History after stop/revoke/expiry remains false-permission identity inspection.

## Acceptance and remaining boundary

Permanent controlled tests cover exact BUY/fixed SELL/resolved SELL, duplicate
and real file reopen with fresh composition, two-handle concurrent callers,
entry gate denials, original nonce/accounting conservation, marker/sign/artifact
fault windows and response loss, post-invocation error/panic/cancel/cipher/TTL/
clock/evidence failures, and boundary expiry before sign/insert/commit.

Stop/revoke tests commit from a second real DB handle before final acquisition
and require zero signer calls. Protected-window tests execute actual competing
stop/revoke UPDATEs from another goroutine/connection and require SQLITE_BUSY
during signer invocation and immediately before artifact commit; the same
mutation succeeds after the outcome commit. The timeout test waits for the
synchronous signer to exit before returning and verifies retained ambiguity.
Static tests ensure the private writer is not composed in other HTTP product
files and the new graph has no submission/send/nonce-allocation dependencies.

Under instrumented two-handle contention, the existing five-second SQLite busy
timeout can reject a contender before it acquires a writer. This is explicitly
a zero-result fail-closed outcome, not an implementation retry. The concurrency
test classifies only SQLite error code 5 with zero result as expected contention;
any other error or permission flag fails. It requires exactly one first outcome
and one signer invocation, then six serial historical retries must inspect the
same durable artifact without another signer call or DB mutation. No busy timeout,
production behavior or acceptance of unexplained outcomes is relaxed.

Author commands/results, intermediate failures and source hashes are recorded
in additive `evidence/http-manual-m3b3-local-orchestration.json`. Its original
NOT_PERFORMED verdict is historical; the new review evidence records independent
C-only PASS, not reused artifact/foundation approval. B3-D remains incomplete.
No whole-B3 READY/PASS, B4/M4/M5/W4-D/W5 or production-health claim follows.

All ten C tests plus the HTTP no-economic-dependency test pass on final source
(19.827s). The full C single race passed (450.009s) BEFORE the final small stop-
versus-invalid-clock priority change and stronger fault call-count assertions;
the affected three tests are rerun under race on final source separately.
The cumulative foundation/artifact/C ordinary run passed earlier (61.800s)
before later test-only cases. Build/vet/staticcheck and final storage/artifact
compatibility pass, as do 39 offline model checks and five Curve fixtures.
No final full-repository suite/race, CI or independent orchestration approval is
claimed. The evidence distinguishes each source snapshot and its limitations.

001–036, earlier history/guard implementations, financial ledger and existing
HTTP/config/startup are unchanged. No real RPC, key/grant, production signer
composition, SubmissionService, ReplayUnknown, broadcaster/raw send, permit,
mainnet, live/release change or remote GitHub action is part of this slice.
