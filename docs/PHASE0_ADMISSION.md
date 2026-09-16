# Phase 0 Slice Admission

Phase 0 is evaluated per execution path. A partial Long or Pons v4 result must not block the
receipt-only Pons Curve Feed slice.

| Gate | Current status | Required evidence |
|---|---|---|
| C01 SDK | `PASS_OFFLINE_ONLY` | Locked SDK toolchain and offline checks |
| C02-PONS-CURVE | `PASS` | Factory/curve emitter identity classified; runtime-hash allowlist fails closed |
| C02-PONS-V4 | `PARTIAL` | Hook/router/PoolManager identity and classification, independent RPC replay |
| C02-LONG | `NOT_STARTED` | Long execution-path identity and classification |
| C03-PONS-CURVE | `PARTIAL` | Independent launch/buy/sell expectations plus one historical reverted transaction |
| C03-PONS-V4 | `PARTIAL` | Independent graduation/buy/sell expectations and quote pre-state evidence |
| C03-LONG | `NOT_STARTED` | Long launch/Initialize/buy/sell evidence |

Historical negative discovery has a fixed stop-loss: admission review begins after at least
250,000 relevant blocks or 1,000 Pons-targeted transactions with no observed failed receipt. A
synthetic/local revert can supplement the review but can never be relabeled as historical data.

## PR-002 Feed admission

Required:

- PR-001A merged.
- C01 `PASS_OFFLINE_ONLY`.
- C02-PONS-CURVE `PASS`.
- C03-PONS-CURVE `PASS`.

PR-002 is limited to Sequencer input, Pons intent parsing, durable observation, outbox and
restart/replay behavior. It does not quote, sign or broadcast and therefore does not depend on
Pons v4 quote pre-state, live execution policy or Long fixtures.

## PR-004 Pons dry-run admission

Adds the applicable C02-PONS-V4 classification, C03 quote/pre-state evidence and C05 admission
policy. No lower-slice PASS implies approval for a higher-risk slice.
