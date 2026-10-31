# HTTP EVM trading API replan

Status: DESIGN_ONLY

This document defines the next HTTP trading contract. It does not enable live
execution, grant canary authorization, connect a production broadcaster, or
change the existing Operation -> ExecutionStep -> TransactionAttempt state
owner.

## User contract

The user submits one order. Internal quote, validation, admission, signing,
submission, and recovery stages are server-owned. There is no client-facing
dry-run or prepare prerequisite.

`POST /v1/trades` requires `Authorization: Bearer ...` and an
`Idempotency-Key`. The request contains `chain`, `wallet_id`, `token`, `side`,
`amount` or `sell_percent`, `slippage_percent`, and an optional `fee` object.
The first supported profile is `robinhood` chain 4663 with Pons v2 Curve and a
native quote asset.

The optional fee object is:

```json
{"max_gwei":"3","tip_gwei":"0.2","max_total_native":"0.0005"}
```

`max_gwei` is the maximum total per-gas price and includes `tip_gwei`.
`max_total_native` is an optional total gas budget. Omitted values remain
unset; the server applies bounded policy defaults and never treats omission as
unlimited. The server owns gas limit, nonce, route, recipient, and raw bytes.
Other optional user controls are `ttl_seconds` and `min_receive`. Each is
validated and bound to the immutable request snapshot.

The same idempotency key with the same canonical request returns the original
record. A changed request with the same key returns `409`. A lost response is
recovered by querying the original key; it never creates a second operation.

## Response and status semantics

`202` means that a durable request or admitted operation was recorded. It does
not mean signed, broadcast, or confirmed. The response contains
`request_id`, `operation_id` when an operation exists, and a read-only progress
projection.

The user-visible projection is:

```text
RECEIVED -> VALIDATING -> ADMITTED -> EXECUTING -> SIGNED -> BROADCASTING
         -> SUBMITTED -> CONFIRMED | REVERTED
```

Terminal or exceptional states are `FAILED_KNOWN_UNSENT`,
`BROADCAST_UNKNOWN`, and `MANUAL_RESOLUTION`. `FAILED_KNOWN_UNSENT` requires
durable proof that broadcaster invocation did not occur or a trusted
deterministic non-propagating rejection. `BROADCAST_UNKNOWN` means the system
cannot durably prove non-invocation; its wallet lane and reservation stay
frozen. TTL, stop, authorization expiry, shutdown, and restart never release
that freeze or trigger an automatic resend.

An unknown replay must query transaction, receipt, and nonce evidence first,
then obtain a fresh `UNKNOWN_REPLAY` permit. It uses the original exact raw
artifact and cannot rebuild, resign, replace, fee-bump, or allocate a nonce.

## Internal gate sequence

The existing durable state owner remains:

```text
Operation -> ExecutionStep -> TransactionAttempt
```

Each economic action passes independent gates for execution admission,
pre-sign, first broadcast, immediate pre-send, and unknown replay. Gate
snapshots bind C04/C05/C07/C08 attestations, deployment identity, chain,
wallet, protocol source, policy version, absolute expiry, and artifact hash.
The immediate pre-send gate rereads emergency stop, authorization epoch, TTL,
reservation, and policy in the same transaction that consumes the permit.

Readiness is three-dimensional and independent:

```text
canary_admission_ready
submission_send_ready
recovery_ready
```

Recovery may query receipts, canonical headers, and reorg state while admission
or sending is stopped. Recovery never depends on signer, SubmissionService,
raw send, permit issuance, or nonce allocation.

## Protocol extension boundary

The HTTP layer uses a chain/profile adapter registry:

```text
ChainProfile -> ProtocolAdapter -> token resolver / quote / call builder /
                               receipt decoder
```

Adding Base or another EVM protocol adds a separately reviewed adapter,
allowlist, runtime identity, fixtures, and evidence. It does not add a second
HTTP state machine or bypass shared gates. Solana is outside this API.

## Delivery gates

1. **P0 contract freeze:** OpenAPI, error codes, fee precision, idempotency,
   status mapping, and `broadcast_unknown` semantics.
2. **P1 intake:** authenticated transport, durable request, idempotency, audit,
   caller/wallet scope, and rate/body limits.
3. **P2 profile adapter:** Pons v2 Curve source, token, balance, allowance,
   quote, fee reference, and runtime identity checks.
4. **P3 admission:** atomic C04/C05/C07/C08 attestations, risk reservation,
   immutable policy/TTL, and emergency-stop priority.
5. **P4 execution:** exact artifact, pre-sign gate, controlled signer lease,
   and artifact evidence.
6. **P5 submission:** independent submission lease, first-broadcast permit,
   immediate pre-send recheck, and conservative ambiguity boundary.
7. **P6 recovery:** durable startup rediscovery, receipt/canonical/reorg
   reconciliation, lease fencing, and safe drain.
8. **P7 composition:** production lifecycle wiring with all three readiness
   dimensions independently reported.
9. **P8 canary:** explicit allowlists, budgets, deployment identity, and
   authorization only after every prior independent review and closeout.

The P2 adapter is read-only and signer-free. The existing trade-service startup
still shares execution configuration with the HTTP registration point; a
future composition slice must split query-only startup before claiming that a
production query process can run without signer configuration.

Every gate requires focused ordinary tests, race/fault/restart evidence, and
independent review. A stage cannot imply authorization for a later stage.

## Fixed safety state during design and early implementation

```ini
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```

No stage in this document authorizes mainnet sends, unrestricted live, Pons v4,
Long, LP, Solana, strategy expansion, or a public unauthenticated HTTP bypass.
