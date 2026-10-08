# HTTP-C2 — immutable MANUAL parameter validation

Local branch: `http-manual-parameter-validation`.
Parent C1: `7a8443bb65ea0e4255d314e01fe8b4b9db871137`.
Status ceiling: `HTTP_C2_READY_FOR_REVIEW`. Author validation is not independent
review approval. No GitHub push, PR, merge or remote change is part of delivery.

## User contract and safety boundary

The caller still uses one authenticated `POST /v1/trades`, not a separate
dry-run/quote/prepare workflow. Internally, successful validation requires
quote arithmetic, gas estimation and simulation. This slice does NOT execute
the order: no C07 admission decision/reservation, runtime authorization, nonce,
signed artifact, permit, submission or send is created.

Only Robinhood chain 4663, native-quote Pons v2 Curve is supported. No BSC,
Flap, Base, Solana, LP, Long, strategy expansion, HTTP-C3 or W4-D/W5 is added.
Existing closed gate implementations and W4-B ambiguity semantics are unchanged.
The existing prepare-execution endpoint can sign; zero-call assertions apply
to the new user API/validator dependency graph, not the entire trade-service.

Production broadcaster stays NOT_CONNECTED, authorization NOT_GRANTED, live
and release_ready false. Validation cannot cancel a propagated transaction
and provides APPLICATION_TTL_ONLY, never a contract deadline.

## Opt-in composition

```ini
RBH_TRADE_API_VALIDATE_ORDERS=false
RBH_TRADE_API_VALIDATION_POLICY_VERSION=0
```

Enabling internal validation requires scoped private API configuration,
MANUAL intake enabled, and an explicit EXISTING immutable C07 policy version
with exact durable C04/C05/C08 evidence identities. The slice neither creates
nor arms policy/authorization. Constructor performs no network I/O. The
deployment-owned RPC endpoint is fixed; callers cannot provide URL, contract,
runtime hash, recipient, raw call, policy version, nonce or expiry.

Composition remains on the existing authenticated local UDS, with no new
listener or public proxy. Read validation may run while emergency-stopped or
without authorization: that does not bypass any economic gate or grant sending.

## Durable flow, not a second execution state machine

Additive migration 017 adds immutable observation overlays and SQL-triggered
audit/outbox effects. Previous migrations are untouched (014 remains reserved).

```text
immutable MANUAL source / original absolute expires_at
  -> Operation: manual_validation_pending
     ExecutionStep: validation_pending
  -> manual_trade_resolutions (identity, units, balance, block/hash, policy)
     Operation: manual_parameters_resolved
     ExecutionStep: parameters_resolved
  -> manual_trade_validations (quote, calldata, fee/gas, simulation result)
     Operation: manual_validation_succeeded
     ExecutionStep: validation_succeeded
```

Each observation insert, corresponding operation/step update, audit and alert
outbox insert is one SQLite transaction; injected failures roll it back.
UPDATE/DELETE/REPLACE of observations are rejected. Bound policy and quote
block/hash cannot change. These validation states are not candidates for the
legacy execution-preparation allowlist; success is not queued execution.

First resolution persists BEFORE quote/simulation. Exact decimal conversion
never rounds: buy uses native units, sell uses token decimals verified at the
snapshot. Sell percentage is floor(balance * bps / 10000), bounded by allowance
and positive units. Failure after resolution leaves it durable. Restart/retry
reuses the saved amount AND snapshot, never recalculating the percentage from
new balances. Concurrent read attempts may be redundant; exactly one durable
snapshot/result wins. No new economic action or reservation occurs.

Completion rereads chain, numbered header, token/runtime identity and balances
at the SAME persisted block/hash; contradiction, reorg or unavailable history
fails closed. Code/call/balance/estimate reads require EIP-1898 blockHash plus
requireCanonical; there is no latest/number-only fallback. A provider without
hash-pinned estimateGas support cannot validate. This is consistency against
one configured provider, not multi-provider consensus/finality.

Completed results are historical immutable observations. Duplicate/GET returns
the saved result without RPC, clock checks or a new expiry, including after
expiry. It must NEVER be interpreted as current execution readiness. Incomplete
duplicates may retry read validation within the original expiry at the original
snapshot. Changing request fields conflicts; old rejected requests stay rejected.

## Parameter and TTL rules

- All input quantities are exact decimal strings; no float, exponent, overflow
  or excess asset precision. Optional min_receive/max_total_native stay UNSET.
- Durable existing wallet, token and Curve runtime allowlists are required;
  policy/attestations/wallet/lane are checked again inside write transactions.
- This slice applies per-operation, slippage/sell, fee/tip, gas and retained
  native-balance caps. Aggregate wallet/token/window risk reservation is NOT
  performed here and remains an execution-admission responsibility.
- Manual max_gwei includes tip and is preserved, never clamped or bumped.
  Omitted fee uses bounded 2*baseFee+tip. Priority suggestions are unpinned
  provider estimates, not authoritative snapshot consensus.
- Gas budget = estimated gas * fee cap. max_total_native caps GAS only; buy
  input is separate. Native balance must cover value + gas + retained balance.
- Minimum output is max(slippage floor, optional user floor), bound into fixed
  Curve calldata. Simulation must return the expected one-word uint256 output.
- Partial-spend/refund buys are rejected: the SDK's ratio-based protection is
  not misrepresented as an absolute min_receive guarantee. No automatic approval.
- Original canonical absolute expires_at is checked before/after quote calls,
  estimate and simulation, plus before durable success. now >= expires_at,
  malformed/noncanonical expiry or unverifiable clock fails closed. Retry/restart
  cannot extend it; no contract deadline is claimed.
  Both observation transactions check again immediately before commit; expiry
  after insert rolls back observation, lifecycle effects, audit and outbox.

## Responses and errors

HTTP 202 still means durable intent, not trade success. The immutable C1 intake
fields `status=RECORDED / reason_code=VALIDATION_NOT_CONNECTED` are historical;
the optional `validation` overlay is the authoritative validation observation:

```json
{
  "status": "VALIDATED_NOT_AUTHORIZED",
  "policy_version": 1,
  "policy_hash": "<immutable policy digest>",
  "expires_at": "<original expiry>",
  "result_hash": "<immutable result digest>",
  "output": {"resolution": {"amount_in": "<integer units>"}},
  "send_authorized": false
}
```

The output example is abbreviated; OpenAPI describes the full observation.
An error after intake may leave durable source/resolution. The response's
Location identifies the record; query/retry uses the SAME idempotency key,
never a new order/window. 422 reports TTL/parameter/policy rejection, 502
unverifiable observation, 503 unavailable validation. Errors are redacted.
Events remain intake-only; resolution/success audits are durable database
records, not exposed as receipt/recovery or validation events by that endpoint.

## Evidence and handoff

Tests cover controlled JSON-RPC quote/simulation, strict parameters/TTL,
concrete validator capability boundary, HTTP/store binding with test-only
backend, actual SQLite close/reopen after failure, concurrent duplicate
conservation, schema guards, fault rollback, policy/source/wallet/lane failures,
and default-off authenticated startup without construction RPC.
RPC adapter tests and HTTP persistence tests are separate boundary tests, not
an end-to-end real-chain or signing integration claim.

Exact commands/results and test mapping are recorded in
`rbh-validation-package/evidence/http-manual-parameter-validation.json`.
Full ordinary Go regression plus affected-package race/build/vet/staticcheck
and offline package verification are required before local handoff.

```ini
HTTP_C2 = READY_FOR_REVIEW
independent_review = NOT_PERFORMED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Stop at the review gate. HTTP-C3 execution/signing wiring requires a separate
authorized slice; no autonomous PASS, send authorization or live enablement.
