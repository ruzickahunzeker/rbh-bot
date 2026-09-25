# Phase 0 Slice Admission

Phase 0 is evaluated per execution path. A partial Long or Pons v4 result must not block the
receipt-only Pons Curve Feed slice.

| Gate | Current status | Required evidence |
|---|---|---|
| C01 SDK | `PASS_OFFLINE_ONLY` | Locked SDK toolchain and offline checks |
| C02-PONS-CURVE | `PASS` | Factory/curve emitter identity classified; runtime-hash allowlist fails closed |
| C02-PONS-V4 | `PARTIAL` | Hook/router/PoolManager identity and classification, independent RPC replay |
| C02-LONG | `NOT_STARTED` | Long execution-path identity and classification |
| C03-PONS-CURVE | `PASS` | Independent launch/buy/sell expectations plus historical reverted Pons Factory transaction and hard gate |
| C03-PONS-V4 | `PARTIAL` | Independent graduation/buy/sell expectations and quote pre-state evidence |
| C03-LONG | `NOT_STARTED` | Long launch/Initialize/buy/sell evidence |

`C03-PONS-CURVE PASS` is supported by independently authored launch, curve-buy and curve-sell
expectations; the canonical historical Pons Factory revert
`0xa056fd548d5251ba0ccbbf005932d1ee665eca0dc5fdcb6d2e79451b32954e4e`;
and a durable-audit ReceiptGate test proving zero economic events, zero copy eligibility and zero
parser-registry mutation for the failed receipt. The search covered 60,001 non-overlapping blocks
and 591 targeted transactions with zero RPC read failures. The stop-loss was not used to close the
gate.

Historical negative discovery has a fixed stop-loss: admission review begins after at least
250,000 relevant blocks or 1,000 Pons-targeted transactions with no observed failed receipt. A
synthetic/local revert can supplement the review but can never be relabeled as historical data.

The deterministic substitute package must prove all of: failed receipt audit persistence, an
explicit pre-parser success gate, zero economic events, zero copy eligibility and zero parser
registry mutation. Its existence does not itself authorize
`PASS_WITH_HISTORICAL_NEGATIVE_NOT_OBSERVED`; that status requires the stop-loss threshold and an
explicit admission review decision.

## PR-002 Feed admission

Required:

- PR-001A merged.
- C01 `PASS_OFFLINE_ONLY`.
- C02-PONS-CURVE `PASS`.
- C03-PONS-CURVE `PASS`.

PR-002 is limited to Sequencer input, Pons intent parsing, durable observation, outbox and
restart/replay behavior. It does not quote, sign or broadcast and therefore does not depend on
Pons v4 quote pre-state, live execution policy or Long fixtures.

## PR-004 Pons v2 Curve dry-run / C06 admission

Scope is limited to Pons v2 Curve Buy/Sell.

Required:

- C02-PONS-CURVE `PASS`.
- C03-PONS-CURVE `PASS`.
- C05 Curve admission policy.
- Pinned trade SDK.
- Deterministic route and execution parameters.
- Unsigned transaction build.
- `eth_call` simulation.
- Durable operation, execution step and dry-run result.
- Restart, replay and failure-path evidence.

C02-PONS-V4, C03-PONS-V4 and Long are not admission dependencies for PR-004 and remain deferred.
No lower-slice PASS implies approval for a higher-risk slice.

`C06 PASS` proves deterministic Pons v2 Curve dry-run only. It is not execution admission and does
not authorize nonce allocation, signing or broadcast.

## PR-005 Execution Kernel / Pre-broadcast admission

PR-005 starts from the existing economic operation identity and reruns execution admission. It
must verify strategy/version binding, wallet lane, balance and reservation, route/state freshness,
fee policy, pending nonce state, chain ID and execution wallet.

Required:

- At most one unresolved execution step per wallet.
- Durable balance/gas reservation and nonce reservation with RPC reconciliation.
- Deterministic transaction-attempt identity and final gas/fee parameters.
- Pons v2 Curve Buy/Sell signer abstraction and artifact integrity verification.
- Encrypted raw signed transaction persistence with `key_version`; private keys never persist.
- Atomic durable agreement between nonce, attempt, raw transaction and transaction hash.
- Duplicate, concurrent admission, restart and real process crash-window evidence.
- Zero reachable broadcast calls.

PR-005 stops after the encrypted signed artifact is durably committed. `PR-005 PASS` does not
enable broadcast. Submission, receipt tracking, positions, Pons v4, Long and live execution are
out of scope.

## PR-006 Submission / Recovery admission

PR-006 may start only from a PR-005 durable signed artifact. It loads, decrypts and verifies the
existing artifact, broadcasts the exact same raw bytes, persists submission state, reconciles
unknown outcomes and applies only canonical receipt effects.

PR-006 owns no nonce allocation, transaction rebuild, normal signing, fee recalculation or
replacement signing. Stale policy may block replay but may not create another artifact. Canonical
reorg handling must roll back position effects and must not double-apply them if the receipt later
becomes canonical again. `PR-006 PASS` does not enable unrestricted live execution.

## C08 Execution Safety admission

C08 status: **PASS**.

C08 requires the joint evidence of PR-005 pre-broadcast safety and PR-006 submission/canonical
recovery. Its 10,000-request test is admission concurrency, not simultaneous broadcast:

```text
accepted + deduped + queued + rejected = total requests
duplicate economic executions = 0
nonce collisions = 0
reservation overcommit = 0
unexplained accepted requests = 0
```

Evidence must cover every nonce/sign/commit/submission crash window, ambiguous broadcast wallet
freeze and exact-artifact replay, canonical success/revert outcomes, reorg rollback and replay
without duplicate position effects.

`C08 PASS` closes the execution-safety gate and means eligible for a controlled Pons Curve live
canary review only; it does not enable unrestricted live execution. Live remains disabled by
default and requires a separate explicit authorization. C04 and C05 remain independent
live-admission blockers.
