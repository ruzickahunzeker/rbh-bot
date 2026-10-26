# Next HTTP execution batch — proposal, not runtime authority

Historical proposal note: the user subsequently accepted implementation of the
controlled local first-order harness. That accepted test-only scope is tracked
in `HTTP_LOCAL_POST_EXECUTION.md`; wallet reuse and production signing/transport
below remain proposals, not permission. This document preserves the planning
baseline and is not evidence that production execution was connected.

Planning source: production/schema `4a54c7c5785e75835a53f108056ebc0a3d42fa25`,
test-only `02495884b4399ecb51e3f3577e59e4cffd085a22`, coordinator branch
`http-abc-local-integration`. Current validation and scoped review belong to
`HTTP_ABC_RECOVERY_INTEGRATION.md`, not to the proposed work below.

No code, migration, production constructor, transport, authorization grant or
remote operation is authorized or implemented by this document. In particular,
`API.Options` cannot enable execution, and `manualFirstTransport` remains a
private fake-harness capability, not an adapter to a production broadcaster.

## Delivery goal and current gap

The intended client workflow is one authenticated POST with user-facing amount,
fee in gwei and optional limits, followed by GET/events for pending results.
There is no required client dry-run/prepare/sign round trip. Internal validation,
source binding, current authorization, limits and multi-stage checks remain.

Today POST does intake/validation/admission only. A can consume an existing exact
signed artifact once in a private local harness. B can recover/reconcile its
durable history; C can present that history. Their separate completion does not
mean a POST invokes A, nor does a tx hash imply propagation or canonical success.

Two gaps need different treatment:

| Work | Can preserve the existing wallet restriction? | Delivery meaning |
| --- | --- | --- |
| Authenticated local first-order POST composition | Yes | Controlled integration test only, one retained MANUAL generation per wallet |
| General repeatable trading on the same wallet | No | Needs a separately reviewed accounting/reorg contract before implementation |
| Production signing/transport/startup | Not sufficient | Needs its own dependency, custody, environment and real-action authority |

No percentage of production completion is inferred from these local milestones.

## Proposed work packages

1. **Contract and dependency freeze.** Keep public request fields unchanged.
   Client input cannot supply nonce, raw bytes, authorization, deployment,
   policy/contract/runtime identity or send permit. Define a private application
   composition boundary that reuses admission, binding, local artifact creation,
   verifier/issuer and the sole A executor. Recovery receives only the existing
   query/decryption capabilities, never the execution composition or transport.
   Resolve which existing owner discovers an admitted-but-not-started request;
   do not add an HTTP-only economic state machine or an implicit retry queue.
2. **Local first-order composition.** Only after its scope is accepted, build an
   in-process authenticated HTTP harness using controlled keys, time, RPC/query
   fixtures and synchronous fake transport. Keep production constructors/options
   disconnected. Obtain every identity from the original durable source and
   reload current gates at each action boundary. Preserve the existing
   `MANUAL_WALLET_REUSE_UNSUPPORTED` rejection, C07 uniqueness and retained claims.
   This proves integration for a first order; it is not a usable repeat-trading
   service or permission to send on-chain.
3. **Wallet reuse design, independently developable.** Enumerate old canonical,
   reverted, reorged, ambiguous and known-unsent-retained generations versus a
   new admission. Define authoritative risk ownership, wallet lane and nonce
   ownership, retained exposure, manual intervention and reorg accounting before
   selecting a model. Keep the one-active-risk constraint unless separately
   authorized to revise it. No parallel financial ledger, implicit refund,
   automatic cancellation of another order or deletion of history is a fix.
   Choose a finite-finality assumption only if explicitly accepted; do not
   silently stop supporting old receipt reorgs just to make reuse work.
4. **Combined fault/response acceptance and independent review.** Test the full
   controlled workflow with B/C and the real local resolver. Record exact
   reviewed code head and separate test/evidence deltas. Existing A or B/C PASS
   does not automatically approve the new composition. No formal closeout or
   production activation follows from an author test result.

The first-order composition can be reviewed while the reusable-wallet design
is pending, but repeat-order acceptance cannot pass until its model is reviewed.
These are dependency work packages, not instructions to create branches now.

## Proposed synchronous response semantics

- Keep `request_id`, `Location` and the original idempotency key as durable
  recovery handles. Same-key/different-payload remains conflict. Same-key/exact
  retry returns verified existing history, never another economic invocation.
- The bounded HTTP wait is not application TTL and cannot extend `expires_at`.
  Do not wait for canonical confirmation as a condition of returning from POST.
  A future composer may wait within a bounded budget for its first submission
  outcome; unresolved accepted work remains queryable, not a promise to send.
- Existing HTTP 202 means durable intake/admission, not executed success. Any
  additional response code/schema contract must be explicitly reviewed rather
  than changing this meaning. Preserve historical top-level intake status;
  `execution` and events carry submission/receipt/canonical/reorg facts.
- Before durable acceptance, cancellation cannot create untracked work. After a
  committed claim, cancellation/response loss cannot undo that claim. Reopen
  existing durable work and classify it under the established intent semantics;
  do not reinvoke A from a retry or recreate an invocation ticket after restart.
- Before the durable intent marker, known-unsent requires durable exclusion of
  any surviving invocation. After that marker, an unprovable outcome is
  `broadcast_unknown`, including a crash before RPC. Keep lane/risk frozen.
  Trusted deterministic non-propagation is distinct from timeout/contradiction.
- No automatic UNKNOWN_REPLAY, replacement, resign, rebuild, fee bump, fresh
  nonce or TTL reset. Query/receipt/canonical/reorg recovery remains independent
  of current admission/send readiness, stop, authorization or TTL expiry.

## Proposed combined acceptance matrix

- POST first BUY, fixed SELL and durably resolved percentage SELL: one original
  request/operation/step/attempt/artifact, one local signature and at most one
  fake first submission; persisted fee/amount/TTL/source remain identical.
- Two handles/concurrent identical POSTs, response loss and exact retries:
  one lifetime budget consumption and no extra artifact/nonce/permit/send.
- Same-key changed payload, foreign scope, injected security fields, invalid
  source, stop/revoke/expiry/deployment/clock/lease drift: fail closed without a
  second economic action. Check each irreversible boundary, not just HTTP entry.
- Claim/audit failure, pre-intent crash, post-intent/pre-call crash, in-call
  timeout, post-call/pre-outcome crash and cancel/reopen: conserve original
  identity, risk/lane and budget; ambiguity never becomes retry permission.
- Fake success, deterministic rejection and contradictory result remain
  distinct; submitted success/revert, block mismatch, reorg and reapply reuse
  B's verified source and exactly-once economic effects, not an HTTP result owner.
- GET/idempotency/events after restart/stop/revoke return verified history with
  stable cursor pagination and no raw bytes/keys/ciphertext/permits. Corrupt
  provenance fails closed; reads cannot trigger RPC or reconciliation.
- Second same-wallet/address admission remains rejected in the first-order
  harness, including after settlement, while old reorg rollback still works.
  Do not report this as repeated-order trading acceptance.
- Separate counters for local fixture setup versus incremental duplicate/read/
  recovery actions. Local fixture signing/fake submission are not zero; extra
  duplicate/recovery actions and every real/mainnet send must be zero.

## Stop conditions and later production decision

Stop for a materially new accounting assumption, altered existing gate, new
schema contract, production signer/custody/transport, replay execution, public
listener or environment-specific grant. A general instruction to continue is
not a real-canary authorization. Deployment credentials, wallet allowlist,
funding, caps, emergency disable, operator ownership and final independent
production readiness remain separate decisions. No BSC/Base/Solana/protocol
expansion is part of this plan.

Production broadcast=NOT_CONNECTED; authorization=NOT_GRANTED; live=false;
release_ready=false. No push, PR, merge or real-chain transaction is performed.
