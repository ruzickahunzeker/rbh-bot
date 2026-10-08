# HTTP-B — read-only user queries

Local branch: `http-readonly-query-layer`.
Parent: HTTP-A commit `b100b72e4a468cb85a96d2ebc2cfdf83866f9688`.
Underlying main baseline: `7b278c4ffd48fe75c8f20d25185f0743222684b8`.
Status ceiling: `HTTP_B_READY_FOR_REVIEW`, not independent PASS.

Historical HTTP-B slice record. See HTTP_MANUAL_ORDER_PERSISTENCE.md for C1's
opt-in record-only POST overlay. Query semantics remain read-only references.
HTTP_MANUAL_PARAMETER_VALIDATION.md documents the separate C2 internal validator;
HTTP-B Client itself still cannot estimate gas, simulate trades or send.

Per user instruction this slice stays local: no GitHub push, PR, merge or new
remote repository. HTTP-A is inherited as a local dependency, not claimed to
have landed main or passed independent review.

## Configuration and production composition

The API still requires the private scoped configuration documented in
USER_HTTP_TRADING.md, and remains local HTTP over the trade-service UDS.
`RBH_TRADE_API_READ_QUERIES=false` by default. Setting it true only attaches the
sealed read-only Client using the deployment-owned ROBINHOOD_RPC_URL. It does
not create an API without RBH_TRADE_API_CONFIG_FILE and cannot enable sending.
Client construction performs zero network calls. Configured query support is
not an RPC-health/readiness claim; an actual failed query returns an error.

The existing broad RPCBackend is deliberately NOT reused: it exposes sending.
New internal/chainquery exposes only Token, Balances and Fees; no ethclient,
trade SDK, signer, SubmissionService, RawBroadcaster, send permit, worker or
nonce allocation is accessible. Its private HTTP JSON-RPC boundary separately
allowlists these exact methods:

```text
eth_chainId
eth_getBlockByNumber
eth_getCode
eth_call
eth_getBalance
eth_maxPriorityFeePerGas
```

Caller HTTP parameters cannot supply RPC URL/method, factory, curve, runtime
hash, arbitrary call data, block or wallet address. Bearer credentials are never
forwarded to RPC. Redirects, RPC credentials in URL userinfo and oversized
responses are rejected. Endpoint errors are redacted.

## Snapshot and identity proof

Each logical query verifies chain 4663, captures latest header, checks the
returned block hash equals the computed full header hash, and binds code/call/
balance reads to EIP-1898 blockHash + requireCanonical=true. Before returning,
it re-reads chain ID and the numbered header; chain drift/reorg/wrong block or
hash fails closed. Providers lacking hash-pinned support cannot silently fall
back to latest or number-only reads. No partial successful results are returned.

This proves consistent observations from the configured provider, not multi-
provider consensus, consensus finality or a durable authorization attestation.
Latest snapshot may change after the response; execution must independently
revalidate. Fee priority suggestions are inherently not block-pinned and are
explicitly marked PROVIDER_ESTIMATE_NOT_BLOCK_PINNED / reference_only=true.

Factory address/hash and canonical curve runtime hash are immutable production
constructor constants copied from configs/mainnet/pons-curve-feed.lock.json.
A permanent test requires exact agreement with its C02_PONS_CURVE_PASS,
direct_implementation, trusted_for_feed entries. That feed identity baseline
is used ONLY to classify read-only routes, not to inherit execution permission.

Token queries require:

1. factory runtime hash matches the pinned baseline;
2. getLaunchedToken has exact static tuple length/canonical address, integer and
   boolean encodings, matching token, nonzero curve, exists=true, phase=0;
3. pair token is the zero address (native quote only);
4. curve runtime hash matches the locked hash;
5. curve.token/factory/pairToken agree with the factory record;
6. curve.graduated=false and isNativeQuote=true;
7. token code exists, decimals is a canonical uint8, and metadata is bounded,
   valid UTF-8 with no control characters or malformed ABI padding/offsets.

name/symbol may use ABI strings or legacy bytes32. They remain contract-supplied
display metadata, not identity. Arbitrary tax/rebasing-token trade support is
not claimed. Graduated, ERC-20-quote and unknown protocols are rejected rather
than rerouted to Pons v4, Long, LP or another chain.

## GET /v1/tokens/{address}?chain=robinhood

Returns address, name, symbol, decimals, protocol, curve/factory identities,
runtime hashes, quote_asset=native and the observed block/hash. Always includes
send_authorized=false. Successful metadata does not reserve anything or make
the token executable. The trade input contract remains unchanged.

## GET /v1/wallets/{wallet_id}/balances?chain=robinhood[&token=address]

The path must match the authenticated scope. The durable wallet ID/address is
verified before RPC and again after RPC. Token queries use the same route/
metadata proof above and read balanceOf(wallet) and allowance(wallet,curve) at
the same pinned block. Human amounts are exact strings, never floats or rounded.

Response includes:

- on-chain native/token balances and exact units;
- token allowance and min(balance,allowance) reference;
- wallet_lane_blocked from one SQLite statement inspecting existing execution
  lanes/steps/reservations and C07 active reservations;
- unreserved_token_balance and sellable_token_amount, both zero if any wallet
  lane/reservation remains unresolved; otherwise sellable is allowance-limited;
- available_native=null, because trade-specific gas budget, minimum retained
  balance and risk policy are not evaluated by this query;
- availability_scope=REFERENCE_ONLY_NOT_EXECUTION_RESERVATION;
- send_authorized=false.

Both accounting layers are blockers, not amounts to subtract twice. This is a
point-in-time read, not admission, an atomic reservation or a guarantee a sale
will pass simulation/risk/TTL checks. Query does not resolve the quantity for a
future percentage-sale intent. That must bind once in the execution slice.

Missing/disabled/drifted wallet or DB failure fails closed. No authorization,
emergency-stop=false or TTL validity is needed for reading existing balances.
Frozen broadcast_unknown remains frozen; no nonce/artifact/reservation is
released or changed. No auto-approval is performed if allowance is insufficient.

## GET /v1/fees?chain=robinhood

Returns verified header base_gwei, node suggested tip_gwei and the reference
max_gwei = 2 × base fee + tip. All conversions are exact decimal strings, and
tip is included in the max, not added again. Malformed/negative/overflow data
or a suggestion exceeding the API fee ceiling is rejected, not silently capped
or auto-bumped. A recommendation may become stale before an order executes.

gas_budget_available=false: without an actual validated trade call, this API
cannot honestly estimate transaction gas or total cost. Execution integration
must later enforce the user's max_gwei, optional max_total_native and server
risk limits against the actual immutable artifact. No estimateGas, signing,
replacement or fee bump occurs in HTTP-B.

## Failure behavior and unchanged gates

Bearer auth is required on every query. Chain must explicitly be robinhood.
Duplicate, unknown, empty or wrong-chain query parameters are rejected before
RPC. Query errors: 503 QUERY_UNAVAILABLE, 502 UNVERIFIABLE_OBSERVATION,
422 UNSUPPORTED_TOKEN_ROUTE or API_POLICY_REJECTED. Capabilities reports
READ_ONLY_REFERENCE only when composed, never authorizes execution or claims
recovery service health. Queries create no audit/alert/intent rows: they work
with query_only SQLite. Existing economic worker audit/alert rules are unchanged.

POST /v1/trades remains the immutable 503 SUBMISSION_DISABLED rejection journal;
it does not call the new query layer, queue a trade, sign or send. HTTP-A's
absolute expiry/idempotency behavior and omitted min_receive/max_total_native
defaults remain unchanged. No migration or execution/recovery implementation
is modified, including W4-B's durable send-intent ambiguity boundary.

```ini
HTTP_B = READY_FOR_REVIEW
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

The next slice is local durable operation/parameter integration through the
existing state machine, subject to independent review. It is NOT permission to
enable broadcast/live, grant authorization, enter W4-D/W5 or use mainnet tests.
