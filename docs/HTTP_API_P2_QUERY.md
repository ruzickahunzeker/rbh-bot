# HTTP API P2 — Pons v2 Curve read-only query layer

Status: `P2_READY_FOR_REVIEW`

This slice verifies the existing read-only chain/profile adapter used by the
HTTP API. The adapter and HTTP read handlers do not grant execution permission
and do not depend on signer, SubmissionService, RawBroadcaster,
`SendRawTransaction`, or nonce allocation. The current trade-service process
still shares a legacy execution composition and therefore requires its
execution configuration before mounting `/v1`; separating that startup
composition is a later production-composition slice.

The supported profile is Robinhood chain 4663 with native-quote Pons v2 Curve.
The controlled RPC client pins observations to one block/header and verifies
chain identity, block fields, token metadata, factory/curve runtime hashes,
token decimals, native/token balance, allowance, and EIP-1559 fee references.
Contradictory or incomplete RPC observations fail closed. Fee responses are
reference data and never alter a user's fee cap or authorize a transaction.

The HTTP read surface is:

```text
GET /v1/tokens/{address}?chain=robinhood
GET /v1/wallets/{wallet_id}/balances?chain=robinhood[&token=...]
GET /v1/fees?chain=robinhood
```

Wallet lane and frozen reservation state are rechecked after RPC observation.
An ambiguous or frozen wallet therefore exposes zero sellable/unreserved token
reference and never becomes a new economic action. The query layer does not
require active authorization and remains available while admission is stopped
once the process composition is running. The current startup prerequisite is
recorded explicitly and is not evidence of query authorization.

Validation evidence on controlled fixtures:

```text
go test ./internal/chainquery -run '^TestHTTPB' -count=1 -timeout=15m       PASS
go test -race ./internal/chainquery ./internal/tradeapi \
  -run '^TestHTTPB' -count=1 -timeout=15m                                  PASS
```

The race run covered both chainquery and HTTP read handlers. The remaining
review gate is independent verification of source identity, block pinning,
fail-closed behavior, and the disconnected dependency graph.

The required safety state remains:

```ini
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```
