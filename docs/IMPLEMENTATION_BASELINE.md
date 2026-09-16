# Implementation Baseline

Status: frozen for initial implementation.

## Delivery layers

| Layer | Scope |
|---|---|
| V0.1 Core | Pons Curve, Pons v4, Long, Feed, Copy, Nonce, Recovery, Reorg, Wallet, Risk |
| V0.1 Product | Telegram, wallet UX, manual trading, watch wallets, strategy settings, operation/position views, notifications |
| V0.2 | Limit orders, price/market-cap triggers, additional automation and protocols |

LP has architecture reservations only. It has no milestone, write API, builder or execution path.

## Work tracks

Track A is limited to closing Phase 0 blockers C01-C08. New research must be attached to a
specific Slice admission decision. Track B starts with the production skeleton and proceeds
through Feed, Bot Strategy and Pons dry-run.

Phase 0 gates are split by protocol path in [PHASE0_ADMISSION.md](PHASE0_ADMISSION.md). In
particular, PR-002 depends on the Pons Curve C02/C03 gates, not on Pons v4 quote replay or Long.

Phase 0 is budgeted at 5-10 engineering days (3-5 only when every prerequisite is already
available). A 72-hour soak is elapsed observation time and restarts after a qualifying failure.

## Ownership

- feed-service exclusively writes feed and protocol/chain state.
- bot-service exclusively writes watches, strategies and signals.
- trade-service exclusively writes wallets, reservations, operations, nonces, signed artifacts,
  submissions and the real position ledger.
- No service opens another service's SQLite database.

## Safety invariants

- Live is disabled until the relevant Slice admission gates pass.
- A wallet has at most one unresolved execution step in V0.1.
- A signed artifact is durably persisted before broadcast.
- An ambiguous broadcast freezes the wallet lane and reconciles the same artifact.
- Receipt/canonical chain evidence, not intent, determines real positions.
- Unknown contracts, hooks, routes, fees or stale state fail closed.
- Product clients call bot-service/trade-service APIs and never build, sign or send directly.

## Admission stress test

The 10,000 concurrency target means concurrent admission requests, not 10,000 broadcasts.
Acceptance requires zero duplicate economic executions, nonce collisions, reservation
overcommit and unexplained accepted requests.
