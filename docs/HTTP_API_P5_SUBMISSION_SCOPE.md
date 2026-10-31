# HTTP API P5 — submission worker scope freeze

Status: `P5_SCOPE_FROZEN`

P5 is the next separately reviewed slice after the P0–P4 fresh review PASS. It
defines the submission worker boundary only. It does not authorize production
broadcast, grant controlled-canary authorization, or enable live.

## Existing state owner

P5 continues to use:

```text
Operation -> ExecutionStep -> TransactionAttempt
          -> transaction_submissions
          -> receipt/canonical/reorg recovery
```

No parallel submission state machine may be introduced. The worker adds lease,
permit, fence, audit, and metrics coordination around the existing durable
attempt state.

## Submission stages

```text
ADMITTED
  -> EXECUTION_ADMITTED
  -> SIGNED_EXACT_ARTIFACT
  -> FIRST_BROADCAST_PERMIT_ISSUED
  -> SEND_INTENT_MARKED
  -> BROADCAST_INVOCATION
  -> SUBMITTED | BROADCAST_UNKNOWN | FAILED_KNOWN_UNSENT
```

The durable `send_intent_at` marker is the ambiguity boundary. It means that
restart can no longer prove broadcaster invocation did not occur. It does not
prove that a transaction was broadcast. Any restart after the marker enters
`BROADCAST_UNKNOWN`, freezes the wallet lane and reservation, and forbids an
automatic second economic action.

`FAILED_KNOWN_UNSENT` is allowed only when broadcaster invocation is durably
excluded or a trusted deterministic rejection proves non-propagation.

## One-shot permits

P5 keeps two different permit kinds:

```text
FIRST_BROADCAST
UNKNOWN_REPLAY
```

They are one-shot and cannot be exchanged. A first-send permit cannot be used
for replay. Unknown replay must first query transaction, receipt, and nonce
evidence, then issue a fresh replay permit. Replay uses the original exact raw
artifact hash and bytes. P5 cannot rebuild, resign, replace, fee-bump, or
allocate a new nonce.

## Final send fence

Immediately before any broadcaster invocation, one SQLite transaction must
recheck:

- emergency stop, with highest priority;
- authorization ID and epoch;
- deployment/build identity;
- chain, wallet, protocol, contract, token, and policy identity;
- immutable artifact hash and raw-byte hash;
- absolute application TTL;
- active wallet lane and risk reservation;
- lease owner and monotonic lease epoch;
- permit kind, status, and expiry.

The final recheck and permit consume commit together. A stop, revoke, expiry,
lease loss, or control drift between earlier gates must leave the permit
unconsumed and produce durable audit/alert evidence.

## Worker and transport boundaries

The P5 worker owns an independent `SUBMISSION` lease. `RECOVERY` remains a
separate lease and can continue query/reconciliation while submission is
stopped. Worker authentication must bind environment, deployment identity,
build SHA/release ID, wallet, and policy version.

The implementation slice may use a controlled local broadcaster harness for
fault and barrier evidence. Production broadcaster wiring, mainnet RPC, and
real `SendRawTransaction` reachability are outside this scope.

## Acceptance matrix

P5 must prove:

- one active submission owner per environment;
- stale owner cannot commit send or outcome mutation after lease loss;
- lease takeover requires higher epoch and preserves durable intent;
- first permit and replay permit are distinct one-shot rows;
- duplicate issuance/consume does not extend expiry or spend twice;
- immediate stop/revoke/TTL/lease races reject before consume;
- pre-marker crash is `known_unsent` only when invocation is durably excluded;
- post-marker pre-RPC crash is `broadcast_unknown` and remains frozen;
- in-RPC and post-RPC pre-outcome crash remain `broadcast_unknown`;
- known-unsent never enters manual resolution automatically;
- unknown replay is query-first and exact-artifact-only;
- no automatic resend, rebuild, resign, replacement, fee bump, or new nonce;
- audit, alert, permit, lease, and outcome writes are durable and idempotent;
- restart rediscovery loses no submission work;
- signer, SubmissionService, SendRawTransaction, and mainnet counts stay zero
  in the production graph until a later authorization.

## Status ceiling

```ini
P5 = READY_FOR_REVIEW
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```

P5 implementation must stop at its independent review gate. P6 recovery
composition and any real production broadcaster wiring remain separately
authorized stages.
