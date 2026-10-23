# HTTP-C1 — MANUAL order persistence

Historical C1 record. HTTP_MANUAL_PARAMETER_VALIDATION.md documents C2's
opt-in internal validation overlay; it does not authorize execution or sending.

Local branch: `http-manual-order-persistence`.
Parent HTTP-B: `45139064f58712a0bf7804492446d5413006ad27`.
Status ceiling: `HTTP_C1_READY_FOR_REVIEW`; no independent PASS claimed.
Local only: no GitHub push, PR, merge or remote repository change.

## Delivery boundary

This is order intake, not live HTTP trading. It adds no simulation, execution
kernel, signer, broadcaster, authorization, permit, reservation or nonce call.
It does not implement HTTP-C2/C3 or W4-D/W5, and does not change closed gates or
W4-B send-intent semantics. Existing prepare-execution can sign; the new API
does not expose or call it. No real-chain endpoint is used in tests.

`RBH_TRADE_API_MANUAL_INTAKE=false` by default. Without the private scoped
`RBH_TRADE_API_CONFIG_FILE`, enabling intake fails startup. This independent
toggle never enables RPC queries, economic execution, live or readiness.
The API remains on the existing authenticated local Unix-domain HTTP socket.
No TCP listener, public routing or internal-endpoint proxy is added.

## POST and duplicate semantics

With intake disabled, a genuinely new valid request retains HTTP-A's durable
`503 REJECTED / SUBMISSION_DISABLED` outcome. With intake explicitly enabled,
a genuinely new valid request returns:

```json
{
  "status": "RECORDED",
  "reason_code": "VALIDATION_NOT_CONNECTED",
  "operation_id": "manual_op_req_<stable identity>",
  "send_authorized": false,
  "submission_queued": false,
  "deadline_capability": "APPLICATION_TTL_ONLY",
  "contract_deadline": false
}
```

HTTP status is 202. RECORDED means durable intent only, not accepted risk,
execution admission, queued submission, successful simulation or trade success.
There is no worker consuming these validation-pending orders in C1. Unknown
tokens are still syntax-only requests, not supported/tradable-token attestations.
GET status/by-idempotency-key returns the original intake/rejection snapshot;
it is not a transaction-status endpoint yet. Capabilities reports
MANUAL_RECORD_ONLY when enabled, with submission/live still disabled.

Both modes share `(principal_id, Idempotency-Key)` lookup and SQLite
`BEGIN IMMEDIATE` serialization. Historical rejected requests remain rejected
even after enabling intake. Disabling intake does not change an already recorded
request: its exact duplicate still returns the original 202 record. Changed
trading fields yield 409 in either mode. Key reuse across callers remains scoped.
Changing omitted TTL to explicit TTL is a conflict, even if numerically equal to
the old default. Normalized equivalent decimal text retains the same identity.

Only new records consult the clock, wallet source and policy defaults. Absolute
expires_at is created once, persisted and returned unchanged after restart,
expiry, policy changes or duplicates. Expiry does not activate, delete or
automatically retry an order; C2/C3 must fail closed before economic action.

## Source overlay, not a second state machine

Additive migration 016 leaves 001..013 and 015 unchanged; 014 remains reserved
on its separate unmerged branch. The transaction writes:

```text
Operation(status=manual_validation_pending)
  -> ExecutionStep(kind=manual_trade_validation, status=validation_pending)
  -> immutable manual_http_order_sources
  -> existing canary_runtime_audit + canary_alert_outbox
```

The source table contains no mutable lifecycle state. It binds request identity,
principal, idempotency key, operation/step, durable wallet ID/address, chain,
MANUAL origin, normalized request/fingerprint, API policy JSON/hash/version,
created_at and absolute expires_at. SQL source/wallet/operation binding,
cross-ledger key exclusion and immutable UPDATE/DELETE/REPLACE guards are tested.
Audit/outbox insertion is triggered by SQL, in the same transaction; failure of
any of the five writes rolls back the whole intake. No credentials,
private key or signed artifact is persisted.

The internal Operation key is `manual:<request_id>`, namespaced separately from
copy-source keys. No strategy, watched-wallet, source-event, observation or
source-transaction identity is fabricated. Operation/step execution policy_version
is NULL: API policy is not C07 execution policy or authorization. Quote
block/hash, decimals, resolved sell amount, gas limit/budget and contract runtime
identity are deliberately absent, not invented or inherited from HTTP-B.

## User parameters preserved, not yet executed

- Buy amount: human native units excluding gas.
- Sell amount: human token units; mutually exclusive with sell_percent.
- sell_percent remains intent; C2 must resolve durable exact units once.
- Slippage and Gwei are normalized exact decimal strings, no floats/rounding.
- max_gwei includes tip; no fee bump/replacement is introduced.
- min_receive and fee.max_total_native stay UNSET when omitted.
- User cannot supply source, policy version, expiry, router, recipient, nonce
  or arbitrary calldata. Existing strict JSON and scoped authentication remain.

API queries remain read-only references. A pending intent is not a risk
reservation or a wallet-lane lock. Future C2/C3 must revalidate and reserve
atomically using existing controls; recording does not authorize spending.
Emergency stop/absent authorization do not block storing non-economic intent,
and cannot be interpreted as permission to execute it.

Events are request-scoped intake audit only:
MANUAL_ORDER_INTAKE_ONLY / MANUAL_HTTP_ORDER_RECORDED. Existing rejected requests
retain API_REJECTION_ONLY events. This is not receipt/canonical/reorg health.

## Acceptance evidence

Tests map to contract/query, zero economic dependencies, legacy rejection and
toggle behavior, actual SQLite close/reopen after TTL expiry, changed-parameter
conflicts, SQL immutability/REPLACE, audit/outbox fault rollback, and 24 concurrent
requests across independent handles (including enabled/disabled races).
Production local composition/authentication and default/invalid config are tested.

Final local validation:

- `go test ./... -count=1 -timeout=15m`: PASS.
- `go test -race ./internal/tradeapi ./internal/storage ./internal/app ./internal/config ./internal/chainquery -count=3 -timeout=5m`: PASS.
- `go build ./...`, `go vet ./...`, `staticcheck ./...`: PASS.
- OpenAPI local references, offline reference model and curve fixtures: PASS.

Full repository race was not rerun for C1; no GitHub CI or independent review
approval is claimed. All RPC/UDS test fixtures are local and controlled.

```ini
HTTP_C1 = READY_FOR_REVIEW
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Next: review C1, then C2 durable parameter resolution/internal validation.
No autonomous PASS, closeout, signer wiring or live/send authorization.
