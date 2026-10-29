# A/B/C shared contract v1 — review candidate

Common local code baseline: `80ad4e474c3016f8686dd9d9ca096302ae2cf10a`.
This is not formal main or a release baseline. Status:
`ABC_SHARED_CONTRACT_READY_FOR_REVIEW`, not jointly accepted or independently
approved. This page fixes the baseline-preserving coordination rules for the
three design deliverables; proposals that change an invariant need a separate
decision. No runtime permission follows from this document.

## Authority and identity

- Start from immutable durable MANUAL request/source, scoped to authenticated
  principal, wallet ID AND normalized address, chain 4663 / Pons v2 Curve.
  Trace the exact execution binding -> nonce claim -> signed artifact -> sole
  first-submission binding -> submission -> receipt/effect. Never pick an
  arbitrary latest attempt or trust caller-supplied security fields.
- Operation -> ExecutionStep -> TransactionAttempt, existing submissions,
  receipt observations and position effects remain transaction-result owners.
  Commands, gate snapshots, permits, audits and lifecycle events are witnesses
  or authorization overlays, not another financial ledger or retry queue.
- Identity is not current permission. New economic actions must independently
  revalidate durable authorization/epoch, gates, policy, deployment, lease,
  risk ownership, runtime identity and immutable application expiry. Stop/read
  failure denies new action. HTTP status, a hash and historical ADMITTED grant
  nothing. C04/C05/C07/C08 are not bypassed or reopened by this design.

## HTTP and continuation

- One authenticated POST; original request ID, idempotency key and Location
  locate history. GET order/by-key/events are observations, not execution or
  reconciliation triggers. Existing separate token/balance query routes are
  not changed by that order-history rule. No public listener expansion.
- Top-level intake/progress, submission, receipt and canonical/reorg facts stay
  distinct. HTTP 202 is durable acceptance, not send permission, chain success
  or a promise of eventual execution. Absolute expires_at never extends.
- Only the unique first durable admission winner's uninterrupted internal
  continuation may initiate the first sequence. Intake and admission winners
  can differ; the composite public duplicate flag is not ownership. Production
  must use an internal typed handoff, never parse a public JSON response as a
  ticket. Duplicate/restart cannot recreate a lost continuation.
- No automatic scheduling/resume is introduced. Committed-but-unstarted work
  remains queryable, possibly requiring later operator design. Disconnect or
  wait expiry neither refunds claims nor authorizes retry. Production response
  deadline/error mapping is still a C/A proposal, not the harness's test budget.

## Sending and recovery

- First send uses one FIRST_BROADCAST permit and original exact artifact.
  send_intent_at is the durable ambiguity boundary, not proof of propagation.
  After marker commit, inability to durably prove no invocation is unknown,
  including a crash before RPC. Keep lane/reservation frozen, no automatic resend.
- Known-unsent requires durable exclusion of invocation or an explicitly
  trusted, durably recorded deterministic non-propagation result. Ordinary RPC
  errors, timeout and cancellation do not meet that test. Retained known-unsent
  is not automatic release/retry permission.
- UNKNOWN_REPLAY execution remains out of scope. A future authorized replay
  needs query tx/receipt/nonce, a fresh distinct one-shot replay permit and exact
  original raw bytes. No rebuild/resign/replacement/fee bump/new nonce to repair
  an existing operation. A genuine later order is a separate B model decision.
- Recovery owns its independent RECOVERY lease, verified artifact decryption,
  read-only queries and fenced reconciliation. It receives no signer,
  SubmissionService, transport/broadcaster, nonce allocator or permit issuer.
  Existing legacy cipher interface includes Encrypt; do not falsely call that
  interface structurally decrypt-only. Historical recovery ignores admission
  stop/revoke/TTL/deployment permission, while still requiring its own integrity,
  DB/RPC/lease health. Admission/send/recovery readiness remain independent.

## Ownership and gates

| Line | Owns next design | Does not own |
| --- | --- | --- |
| A/mainline | Private typed composition, config/startup/lifecycle, custody/transport boundaries, shared adapter and integration | B accounting decisions or unilateral public contract changes |
| B | Wallet-generation/risk/nonce/reorg model, exact invariant delta, later reviewed additive schema and durable reader/recovery changes | HTTP handler, sender/custody or startup |
| C | Public request/response/error/wait contract, OpenAPI proposal and presentation/contract tests | Execution permission, recovery mutations or competing result reader |

Gate 1: individually review all designs and reconcile shared decisions before
implementation batches. B must resolve compatibility with C07 single-active
risk semantics; current reuse rejection remains. Existing 001–045 are immutable;
old wallet-design branch 5329f28 is reference-only, not an integration input.

Gate 2: separately scoped implementation/review. Default-disabled composition
may precede reusable-wallet implementation, but component construction, send
reachability and real-action authorization are separate checkpoints. No claim
of repeatable service before B passes. No production send, real grant, mainnet
transaction, live, release readiness, push/PR/merge or formal closeout here.
