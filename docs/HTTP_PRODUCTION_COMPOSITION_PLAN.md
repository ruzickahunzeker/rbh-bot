# A: production MANUAL HTTP composition — design only

Branch: `http-production-composition-plan`.
Base: `80ad4e474c3016f8686dd9d9ca096302ae2cf10a` (local, not formal main).
Status ceiling: `A_PRODUCTION_COMPOSITION_DESIGN_READY_FOR_REVIEW`.
Independent review: NOT_PERFORMED. Implementation: NOT_STARTED.
Shared rules: `HTTP_ABC_SHARED_CONTRACT.md` (review candidate).

## Goal and current source facts

Design the smallest default-disabled connection from the user POST to the
existing MANUAL execution/signing/first-submission path. Do not copy the
test-only HTTP wrapper into production or repeat its completed acceptance.
Current first-order local evidence is in `HTTP_LOCAL_POST_EXECUTION.md` and
`evidence/http-local-post-execution.json`; its scoped PASS is not this design's
approval. No source, config, startup, API contract or schema changes here.

Inspected baseline facts:

1. `internal/tradeapi/http.go` Options cannot enable execution/signing/send.
   `internal/app/user_trade_api.go` registers the authenticated API on the
   existing local UDS; its options stop at admission. No TCP/TLS/multi-tenant
   expansion is needed for the first connection.
2. Binding, nonce claiming, local signing, runtime verification, first-permit
   issuance and the MANUAL submission executor already exist as private
   components. They are not a production HTTP composition. The executor's
   `manualFirstTransport` is explicitly a trusted local-harness primitive, not
   an existing production RPC adapter or a drop-in RawBroadcaster interface.
3. `manualMeasuredRuntimeIdentity` measures clean build metadata AND the running
   Linux image handle; its manifest/clock/profile have no startup loader. Do
   not replace those measurements with config strings or pathname hashing.
4. Migration 018 `manual_b1_no_arm` rejects arming declared MANUAL authorization.
   The local fixture temporarily seeds synthetic authority and restores the
   guard before actions. That setup is NOT a deployable grant protocol. A new
   composition constructor must not remove triggers or treat config as a grant.
5. `internal/app/run.go` already constructs production recovery with independent
   lease, read-only RPC, Pons resolver and artifact cipher. It also requires an
   execution private key and constructs a legacy prepare-execution kernel
   before recovery startup. Worker dependency independence is established in
   source; process startup independence from signer/key availability is NOT.
6. Existing `ProductionComposition` accepts DISABLED only; admission/send
   readiness remain false, recovery can be configured and run. Its existence
   does not mean MANUAL submission wiring or authorization exists.

## Proposed capability graph (not implemented)

```text
existing authenticated local UDS
  -> strict user API / durable intake and admission
     -> PRIVATE first-admission continuation (not a response flag)
        -> binding -> verified nonce claim -> signing intent/artifact
        -> submission foundation -> fresh verifier -> FIRST_BROADCAST permit
        -> existing MANUAL executor -> separately reviewed exact-byte transport

SQLite durable submitted / broadcast_unknown / retained history
  -> independent RECOVERY lifecycle and lease
     -> source-aware artifact authentication + read-only RPC
     -> existing receipt / canonical / orphan reconciliation
        -> original effects + risk/usage + audit/outbox + event journal
```

The two branches share durable facts, not an execution dependency. Recovery
does not capture the composition root, user API, execution kernel, signer,
sender, permit issuer or nonce writer. The application composition root may
construct both graphs; this is not permission to pass one graph into the other.
The shared Store exposes writes, and the legacy cipher exposes Encrypt, so the
proof must cover both constructor fields AND actual call paths, not claim a
fully read-only store or structurally decrypt-only legacy interface.

## Exact change surface for later scoped implementation

These are planned surfaces, not files changed in this batch. New file names
below are tentative. B/C files are not cherry-picked or merged by this plan.

| Component / existing file | Proposed minimal responsibility | Owner / limit |
| --- | --- | --- |
| `internal/tradeapi/http.go`, `manual_admission_http.go` | Typed internal handoff from the unique first admission commit; retain old constructors' non-executing behavior | A owns hook/constructor; C owns public presentation and tests |
| New `internal/tradeapi/production_manual_composition.go` | Private sequencing of existing writers/issuer/executor; no new queue or transaction-result state | A, default-denied construction; no sender in first implementation batch |
| `manual_execution_binding.go`, `manual_nonce_claim.go`, `manual_local_signing.go` | Reuse existing durable owners; adapt only after separately scoped review | A coordinates; B signs off wallet/nonce model; no blanket guard relaxation |
| New private foundation writer / migration 037's existing foundation views | Encapsulate original source-bound foundation write rather than copy test SQL into a public handler | A; original schema remains authoritative |
| `manual_runtime_identity.go`, `manual_runtime_image.go`, `manual_runtime_clock.go`, `manual_runtime_verifier.go` | Validated immutable process manifest, measured running image, bounded clock/provider/session identity and live runtime observations | A; no dirty/unmeasured fallback or caller security fields |
| `manual_submission_executor.go`, `internal/trade/manual_submission_store.go` | Preserve sole executor, exact bytes, claim/marker/outcome transactions and same-writer final checks | A; not legacy SubmissionService or controlled-worker adoption |
| New production custody/transport adapters | Wallet-bound signing and synchronous exact-byte first send with no retained bytes, hidden retries or async work | Later separate authority/review; not connected by default-disabled slice |
| `internal/app/user_trade_api.go`, `internal/app/run.go`, `internal/config/config.go` | Explicit capability construction, startup ordering, default-off config and safe drain; no activation by credential-file presence | A after design approval; C does not change these concurrently |
| `internal/trade/production_composition.go`, `production_recovery.go`, `recovery_artifact_source.go` | Reuse existing independent recovery; separate startup requirements from optional signing/prepare features | A lifecycle / B historical integrity; no broadcaster additions |
| `internal/health/readiness.go` if needed, existing metrics | Independent capability readiness and sanitized reasons | A; never turn whole-service readiness into permission |
| `internal/storage/migrations/trade/001..045` | No edits | Any future additive schema belongs to an explicitly approved B/authority slice |
| Public OpenAPI, response/error docs and presentation tests | C contract integration after joint decisions | C; no competing Store.Get or recovery implementation |

## Internal ownership and request flow

Authentication/strict parsing/durable source remain existing code. Introduce
no public `execute(request_id)` endpoint and no generic callback that executes
any historical request ID. The proposed typed continuation is private,
single-use and only constructed by the first admission commit's internal call
stack. It selects the original request, not security facts. All existing
writers still reload source, active risk/authorization and controls. A
historical admission or a public duplicate flag cannot construct that handoff.

The initial production candidate preserves the local ownership limitation:
loss of the uninterrupted initial stack does not enqueue/resume execution.
It exposes durable history, not guaranteed eventual execution. A durable
scheduler/operator resume endpoint would be another scope, not an implementation
detail. New idempotency keys cannot work around retained wallet ownership.

Current protocol transitions remain owned by their existing commands; no new
HTTP status machine is introduced:

| Durable boundary | Existing next action / restart meaning |
| --- | --- |
| Intake/admission committed, not yet continued | Queryable; no duplicate/restart recreation of the initial action |
| Binding/nonce/signing intent/artifact | Original operation only; failed signing is not permission to resign |
| Fresh FIRST permit / submission claim | One existing first-submission binding; no replay-permit substitution |
| Before durable send intent | Known-unsent only if old invocation durably excluded/fenced; retained ownership remains |
| After durable send intent, including before RPC | Conservative broadcast_unknown if invocation absence cannot be proved; no automatic second action |
| Trusted deterministic non-propagation outcome | Known-unsent retained; not refunded/released automatically |
| Exact returned original hash | Submitted, not canonical success |
| Unverifiable/contradictory result | Preserve ambiguity/manual-resolution and durable evidence |
| Receipt/canonical/orphan | Independent existing recovery; original effect apply/rollback/reapply exactly once |

## Independent gates and final send boundary

Admission, execution, pre-sign, first-broadcast and immediate-pre-send are
separate checks. Revalidate epoch/state, environment/chain/wallet/address,
immutable policy/source/contract/runtime identities, deployment/build/release,
durable gate attestations, retained reservation, TTL and relevant worker lease.
Read-only runtime RPC verifies the current authorized contract code identity;
failure, conflict or unmeasured identity denies action. Observations have bounded
age and clock uncertainty; no caller attestation substitutes for them.

Immediate-pre-send repeats mutable DB checks under the SAME writer transaction
as the existing outcome/invocation boundary, after artifact authentication.
Preserve final lease/stop/control fencing and bounded synchronous callback. Do
not change marker placement or label a post-marker pre-RPC crash known-unsent.
SQLite writer locking and an external RPC side effect are not an atomic
distributed transaction. Design must bound writer hold time and measure its
impact on emergency-stop writers/lease renewal. No preemption guarantee after
an RPC has begun, automatic provider retry or distributed exactly-once claim.

UNKNOWN_REPLAY remains disabled/unreachable. Future query-first/fresh-permit
semantics are a constraint, not authorization to implement replay here.

## Default-off configuration, custody and grant boundaries

- Keep existing defaults and legacy constructors non-executing. No public
  `live=true` switch, implicit config-file activation, generic DI broadcaster or
  re-use of a synthetic test authorization.
- A later reviewed composition config must be operator-owned and immutable for
  process lifetime, bind build SHA AND running image hash, release/deployment,
  chain/network checkpoint/provider identity, wallet and policy/epoch. Validate
  it against durable records, never write those records to match config.
- Read-only RPC, artifact decryption/keyring and signer credentials are separate
  capabilities. Reuse the currently reviewed encryption/AAD contract; retain
  historical key versions for recovery. A missing signer must not disable a
  viable recovery-only deployment. Key custody selection/loading/rotation and
  secret source are unresolved, not approved by this document.
- The no-arm guard remains. A separately reviewed operator authorization
  protocol and additive schema, if required, must precede any real grant. It
  must bind scoped budgets/epochs/deployment, durable audit and revocation;
  neither C's API credential nor startup readiness can grant that authority.
- First disabled implementation instantiates NO real transport/signer for
  MANUAL sending, grants no authorization and calls no mainnet service. Merely
  attaching a sender that config currently refuses to call is a later boundary.

## Readiness, startup and shutdown

`canary_admission_ready`: DB/current policy/attestations/controls/authorization
and appropriate capability health; not permission inferred from a bool.
`submission_send_ready`: additionally measured deployment, SUBMISSION lease,
signing/runtime/exact-artifact/transport gates. `recovery_ready`: independent
DB, read-only RPC, RECOVERY lease and worker lifecycle. All are observed health;
every action still rechecks its authoritative gates. Policy-unblocked is not
recovery service health. Default-disabled admission/send remain false.

Startup proposal: open/migrate DB -> configure historical decryption/read-only
RPC -> acquire RECOVERY lease and rediscover durable work -> expose authenticated
history/health -> optionally configure separately approved execution components
-> permit new execution only under its current authority. No startup signing,
automatic first-send resumption or replay. Existing recovery failure/exit policy
must remain explicit; do not mark recovery healthy just because it was started.

Shutdown proposal: latch action readiness false/deny new continuations -> bound
or cancel unstarted work -> drain in-flight synchronous action and reconcile
its durable outcome -> keep required lease maintenance until drain finishes or
owner is fenced -> drain recovery -> close HTTP/RPC/DB only after dependent
work exits. Never release unknown on shutdown. Restart is query/classification,
not another invocation ticket. Reusing the existing shutdown wait logic is
preferred to a second independent lifecycle manager.

Audit/metrics/alerts should distinguish denied gates, durable accepted but
unstarted work, first claims, marker ambiguity, deterministic rejection, lease
loss, query failure, canonical/reorg transitions and drain. Persist witnesses
with owning transitions using existing audit/outbox/event contracts; metrics
do not become another ledger. Logs/labels never contain keys/raw/ciphertext,
permit values, credentials or unbounded per-request labels. New public health
shape or alert reason names need C/operations review, not ad hoc promises here.

## C proposal compatibility decisions still open

C reference: `http-c-production-http-contract@9715887a4757eb22041b55fa0f842e5838359019`,
`docs/HTTP_PRODUCTION_USER_CONTRACT_PROPOSAL.md`, read-only reference, not merged.
Keep its one-POST + GET/events shape and strict user parameters. The proposed
10s default / 30s maximum from authenticated entry and 202 on a readable
accepted timeout are NOT accepted numeric/response changes in this A batch.
Need measured feasibility across validation, SQLite contention, signing and
bounded send, including durable ownership if the response deadline wins. Until
resolved, retain existing errors with Location and no eventual-execution claim;
never copy the test-only five-minute budget as a production SLA.

## Later implementation batches and required evidence

| Batch (proposal only) | Scope | Exit evidence |
| --- | --- | --- |
| A1 | Default-denied private composition shell, startup/capability separation, independent recovery-only boot | Old constructors still cannot send; no signer/transport instantiation; absent/revoked auth and missing signer do not block viable recovery; no startup invocation |
| A2 | Separately scoped custody/identity/clock/authorization adapters, local controlled dependencies only | No-arm not bypassed, measurements/drift fail closed, signer invocation requires fresh durable ownership, no grant or real send |
| A3 | Separately authorized first-transport boundary and C response integration | Exact bytes/one invocation, no hidden retry, marker crash matrix, post-decrypt final control/lease checks, disconnect/duplicate/reopen conservation |
| B integration | Only after reviewed wallet model and schema | Real second generation plus old reorg in every newer phase; no unique-index rollback failure or silent C07 relaxation |
| Combined review | Exact assembled code/evidence heads | Local full/selected race tests with accurate head attribution, adversarial/fault checks, existing recovery dependency proof and prohibited-path checks |
| Real-action gate | Separate explicit environment-specific authorization | Custody/funding/allowlists/caps/monitoring/emergency disable/rollback and operator ownership; no automatic activation after review |

Do not implement these batches in this design turn. Minimum future tests also
include DB/RPC unavailable, stale lease takeover, stop/revoke/TTL/deployment
drift at every stage, retained known-unsent/unknown, actual SQLite close/reopen,
audit/outbox failure rollback, HTTP response loss and no raw/secret leakage.
Use controlled RPC only until separately authorized. Count fixture signing/fake
send separately from prohibited real/mainnet sends; recovery/duplicate/read
must add zero signing/send/nonce/permit actions.

No aggregate M4/W4 PASS, formal closeout, schema change, wallet-model acceptance,
new chain/protocol, real authorization, production broadcaster, mainnet send,
live enable or release readiness follows. Current production broadcast remains
NOT_CONNECTED, authorization NOT_GRANTED, live=false, release_ready=false.
