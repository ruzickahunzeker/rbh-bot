# User HTTP trading — HTTP-A

Base: `main@7b278c4ffd48fe75c8f20d25185f0743222684b8`.
Branch: `http-user-trade-contract`.
Status ceiling: `HTTP_A_READY_FOR_REVIEW`; no independent approval or closeout.

This is the first slice of the agreed direct-order API: callers do not need a
dry-run, quote or prepare round trip. **This slice cannot execute orders.**
Valid new POSTs are durably recorded as `REJECTED / SUBMISSION_DISABLED` and
return 503, never 202. No queue or later activation is created. A rejection
has `request_id` and `operation_id: null`; it is not an economic operation.

## Boundaries

Only Robinhood chain 4663 is recognized. The future execution allowlist is
Pons v2 Curve, but protocol/contract resolution, token metadata verification,
balance/sell-percentage resolution, quote/minimum-output enforcement and fee
execution binding are **NOT_CONNECTED**. A syntactically valid token in a
rejection is not evidence that it is supported or tradable.

The new package has no signer, execution kernel, SubmissionService,
RawBroadcaster, RPC, nonce allocation, permit or worker dependency. The existing
trade-service prepare-execution endpoint can sign; this slice neither exposes
it through /v1 nor claims the entire service has zero signing capability.

Existing C04/C05/C07/C08 and W1/W2/W3/W4-B statuses and W4-C pending-review
status are unchanged. Production broadcaster remains NOT_CONNECTED;
controlled-canary authorization remains NOT_GRANTED; live and release_ready
remain false. No W4-D/W5, BSC, Base, Solana or historical migration changes.

## Local configuration and authentication

Without `RBH_TRADE_API_CONFIG_FILE`, /v1 is not registered. This is NOT an
execution-enable setting. When set, /v1 is mounted on the existing trade-service
Unix-domain HTTP socket, never a new TCP listener. No unauthenticated endpoint
or internal prepare/replay/send proxy is added.

The path must be a private regular file (0600 or 0400, no symlink), injected
outside Git. Configuration example (placeholder, not a usable credential):

```json
{
  "principal_id": "tg-service",
  "wallet_id": "wallet-01",
  "wallet_address": "0x1000000000000000000000000000000000000001",
  "token": "<dedicated-random-api-token-at-least-32-bytes>"
}
```

The wallet ID/address must match the enabled durable chain-4663 wallet. The
credential must be distinct from business IPC authentication and is compared
by constant-time digest comparison. Only its digest is retained by the handler;
it is never returned or written to the ledger/audit. One immutable deployment-
configured caller/wallet scope is supported. Rotating credentials requires
restart; keep principal identity stable to retain retry/query continuity.

This is a local contract, not public TLS, multi-tenant auth or Internet exposure.
Public routing/TLS/rate limiting and additional caller scopes require a separate
slice. Do not proxy the complete UDS namespace to a public HTTP listener.

## POST /v1/trades

Headers:

```http
Authorization: Bearer <dedicated credential>
Idempotency-Key: <caller-owned order identity>
Content-Type: application/json
```

User-facing buy example (sample fees, not recommended fees):

```json
{
  "chain": "robinhood",
  "wallet_id": "wallet-01",
  "token": "0x2000000000000000000000000000000000000002",
  "side": "buy",
  "amount": "0.01",
  "slippage_percent": "3",
  "fee": { "max_gwei": "1", "tip_gwei": "0.1" },
  "ttl_seconds": 30
}
```

Sell replaces side with `sell` and uses either token quantity
`"amount": "1500.5"` or holding percentage `"sell_percent": "50"`, never both.
Buy amount is native coin quantity and excludes gas. Sell amount is token
quantity, not the native proceeds. Percentage is (0,100] at 0.01% precision.
The future execution slice must resolve and bind a percentage's exact units
once; this rejection-only slice does not fetch or invent a balance.

All monetary/percentage fields are decimal strings. No signs, whitespace,
leading-zero integer aliases, floats, exponents, truncation or rounding.
Trailing fractional zeroes normalize to equivalent identity. Buy/native limits
allow at most 18 decimal places; Gwei at most 9; percentages at most 2.
Token decimal precision must be independently verified before future execution.
`HumanUnits` rejects excess precision and uint256 overflow; it does not obtain
token metadata. Protocol-specific quote assets must be verified later, not
assumed for an arbitrary route.

Slippage is required, may be zero, and must not exceed the API policy limit.
Zero slippage still requires minimum-output protection in the future execution
slice. 100% slippage is rejected. Token is an address, never a symbol. Caller
cannot select protocol/router/recipient/nonce/raw bytes/authorization/policy.

### Gas parameters

The current Robinhood execution uses EIP-1559 DynamicFeeTx.

- `max_gwei` is the maximum total per-gas price, not total transaction cost.
- `tip_gwei` is included in max_gwei, not added on top; zero tip is valid.
- Tip requires an explicit max and may not exceed it. If omitted, later
  execution must choose a server-controlled tip within the user's max.
- Omitted fee requests bounded automatic pricing in the future execution slice;
  it does not mean unlimited fees. No RPC fee suggestion is made by HTTP-A.
- gas_limit is server-owned. Maximum gas budget is gas_limit × max_gwei × 10^9
  wei; native balance must also cover input and the retained-balance policy.
- Fee identity will bind to the original artifact. No fee bump, resign or
  replacement on timeout/expiry is permitted.

### Optional user limits — default UNSET

- `fee.max_total_native`: additional total gas budget ceiling, native coin units,
  excludes trade input. Can be supplied alone with automatic per-gas pricing.
- `min_receive`: minimum output in human output-asset units. Future execution
  must use max(user floor, quote/slippage floor).

Omission does not fill either value. null, empty text and zero are rejected.
Server risk limits and quote/slippage protection remain required when omitted.
Their execution enforcement is not claimed by this slice.

### TTL, policy and durable identity

ttl_seconds is optional. Deployment API policy defaults are TTL 30s, maximum
300s, maximum slippage 500 bps and maximum manual fee 100 Gwei. They are local
API limits, NOT recommendations or runtime authorization. Optional api_policy
in the private config replaces the complete Policy object; capabilities exposes
the effective values. No default is secretly chosen for optional user limits.

Only a new durable record consults the server clock/default TTL. Its absolute
expires_at, canonical request, policy JSON/hash/version and wallet source are
immutable. Exact duplicates are retrieved before evaluating changed policy or
clock, including now == expires_at, later expiry and real DB close/reopen.

Policy snapshots are per-request API metadata, not C07 attestations or an
execution policy version. A future execution slice must independently validate
durable C07/runtime authorization and bind the actual immutable execution policy.

Idempotency scope is (principal_id, key), before chain dispatch. Key changes to
chain, wallet, side, token, amount, fee, optional limits, slippage or TTL cannot
create a second economic action. In-scope changed requests conflict. A wallet
outside the credential's scope is denied rather than dispatched. Missing TTL
and explicitly supplied TTL are different request identities, even if values
happen to equal today's default. Caller authentication is always required for
duplicate retrieval. Never rotate keys automatically after uncertain outcomes.

Request + schema-owned runtime audit + alert outbox are one SQLite transaction.
Audit/outbox failure returns API_UNAVAILABLE with no partial record. The ledger
is append-only and schema forbids accepted/authorized/queued records. It cannot
become a send queue after a restart or future live enablement. Rejected keys stay
rejected; a future authorized order requires new explicit user consent/identity.

Migration 015 is additive and independent. 014 is reserved by the unmerged
trade-api-submission-intake branch; that branch and evm-foundation-config-registry
are NOT inherited here. If 014 lands later, resolve migration-count test changes
without renumbering already-landed migrations.

## Queries

```text
GET /v1/capabilities
GET /v1/wallets
GET /v1/trades/{request_id}
GET /v1/trades/by-idempotency-key    (Idempotency-Key header)
GET /v1/trades/{request_id}/events?after=<canonical cursor>
```

All require Bearer auth and return only the configured caller/wallet scope.
Queries return 200 for a stored rejection. Events are explicitly
API_REJECTION_ONLY, not a receipt/canonical/reorg feed or recovery health claim.
AUTOINCREMENT cursor is preserved across SQLite reopen and VACUUM. Capabilities
marks missing validation/query/execution integrations instead of claiming live
readiness. Wallet listing reads the durable ID/address binding; it is not a
balance query. Balances/token metadata/reference fees are not implemented here.

Errors: 400 INVALID_REQUEST, 401 UNAUTHORIZED, 403 WALLET_SCOPE_DENIED or
WALLET_UNAVAILABLE, 404 REQUEST_NOT_FOUND, 409 IDEMPOTENCY_CONFLICT,
413 REQUEST_TOO_LARGE, 415 UNSUPPORTED_MEDIA_TYPE, 422 API_POLICY_REJECTED,
503 API_UNAVAILABLE. A valid rejected order returns 503 SUBMISSION_DISABLED
inside its redacted Record. No error contains raw SQL, secrets or artifact bytes.

## Remaining slices and review gate

1. Independent review of HTTP-A; branch-only READY_FOR_REVIEW, not PASS.
2. Read-only token/balance/fee queries and verified protocol/source resolution.
3. Manual/copy durable operation integration and user parameters through quote,
   simulation, execution, risk accounting and exact signed artifact binding.
4. Authenticated controlled submission only after outstanding wiring reviews
   and explicit runtime authorization; no bypass of any existing gate.
5. Real operation events/client integration, independent review and closeout.

No client-facing dry-run step is introduced. Internal validation/simulation is
not removed. No automatic approval, cancel-propagated promise, cross-protocol
fallback, split orders, unrestricted live or tax/rebasing-token support claim.
