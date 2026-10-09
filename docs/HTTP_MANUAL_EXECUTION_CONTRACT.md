# HTTP-C3-A — MANUAL execution parameter contract

Base: `d89830ae019ebe62b32fdea657289a39831d00fc` (local C2 closeout).
Branch: `http-c3a-manual-execution-contract`.
Reviewed implementation: `a3a5f47b176032f223a9c5db460f309fec06df35`.
Independent review: PASS; formal closeout: COMPLETE_LOCAL_ONLY.
Closeout branch: `http-c3a-contract-closeout`.
Delivery is local only: no GitHub push, PR, merge, deployment or remote change.

## Scope

Reuse the completed RBH execution core, but do not feed MANUAL requests into
legacy copy-trading preparation. That path can resolve sell percentage, fees
and current snapshots again. C3-A instead exposes an internal read-only adapter
for the ORIGINAL durable C1/C2 result. No HTTP route, configuration switch,
production composition, migration or existing execution implementation changes.

`NewManualExecutionReader(store)` has no construction RPC. Its sole method is
`Load(ctx, scopedIdentity, durableRequestID)`. It takes no caller-provided
amount, fee, policy, runtime identity, nonce, expiry, calldata or RPC endpoint.

Supported scope remains Robinhood chain 4663, native-quote Pons v2 Curve only.
No BSC, Flap, Base, Solana, Long, LP, strategy expansion, W4-D or W5.

## Read flow, not a new state machine

```text
immutable manual_http_order_sources
  -> existing Operation / ExecutionStep
  -> immutable manual_trade_resolutions
  -> immutable manual_trade_validations
  -> detached ManualExecutionContract (internal, ephemeral, NOT_ADMITTED)
```

All source, policy, allowlist, lifecycle and validation reads use one SQLite
read transaction. The adapter performs zero SQL writes on success or failure.
Existing lifecycle stays `manual_validation_succeeded / validation_succeeded`.
No TransactionAttempt or new durable execution state is created.

The result binds:

- Request/principal/operation/step/wallet/address and chain identity.
- Original normalized request and fingerprint, API policy version/hash.
- Immutable existing C07 policy version/hash and exact durable C04/C05/C08
  evidence identities (read validation prerequisites, not economic admission).
- Resolution/result hashes; token decimals, protocol/factory/Curve and runtime
  hashes, original quote block number/hash and saved balances.
- Exact input, quoted/minimum output, calldata/to/from/value, gas limit,
  fee/tip caps, gas budget and simulation result from the saved C2 output.
- Original created_at and canonical absolute expires_at;
  APPLICATION_TTL_ONLY, contract_deadline=false.

The adapter independently verifies C2 content hashes, normalized JSON,
arithmetic and calldata consistency; it does not run Resolve/Complete,
simulation or any RPC. Pure expected-calldata verification does NOT rebuild a
transaction, allocate a nonce or create an artifact. No current-chain health,
canonicality, finality or quote freshness is claimed by this offline read.

`binding_hash` is a deterministic SHA256 over canonical contract JSON with
binding_hash empty. It is a checksum, NOT a signature/HMAC/permission. Returned
objects are detached from durable storage; mutating one cannot change the next
load. Future consumers must reload from SQLite, not trust an HTTP-echoed object.

## Parameters and measurement

No new expiry, percentage, fee, quote or calldata is minted on load/retry/restart.
Zero tip remains zero. Optional min_receive and fee.max_total_native stay UNSET
when absent; no synthetic user floor or gas cap is introduced.

Each quantity carries `(chain_id, asset, decimals, integer units)`:

| Measure | Buy | Sell |
|---|---|---|
| Input | native, 18 decimals | verified token and decimals |
| Quoted/minimum output | verified token and decimals | native, 18 decimals |
| Gas budget | native, 18 decimals | native, 18 decimals |
| Transaction value | native buy input | native zero |

Gas budget is gas_limit * gas_fee_cap; it excludes buy input. Do not add raw
quantities of different tokens, or token input to native gas. No valuation,
conversion, shared aggregate ledger or new interpretation of C07's historical
untyped amount limits is introduced.

Both percentage and fixed-amount sells are checked using exact integers:

```text
saved_amount * 10000 <= saved_token_balance * policy.max_sell_bps
```

Equality is allowed. The adapter never invents SellBPS for a fixed amount,
rounds up the cap or recomputes saved units from a new balance. Existing C07
production rules remain untouched; this does NOT yet establish compatibility
with its admission/reservation APIs.

## Fail-closed boundary and no permission claims

Missing/incomplete/rejected validation, wrong scoped identity, policy/hash/
attestation/allowlist drift, disabled wallet, unresolved wallet lane, conflicting
operation/step/result binding, cancelled context or unavailable DB fails closed.
The reader checks TTL at entry and again before returning its read snapshot.
now >= expires_at, malformed/noncanonical expiry and unverifiable clock reject.
Historical C2 GET/duplicate results remain queryable after expiry, unchanged.

This is NOT an execution admission gate: emergency stop or absent authorization
does not forbid reading the parameter contract. All results explicitly say
`PARAMETER_CONTRACT_NOT_ADMITTED`, admission_authorized=false,
send_authorized=false. A policy/wallet change may commit while a snapshot is
open; the returned old snapshot grants NO rights. The next read observes drift.
Future C3-B must independently enforce highest-priority stop, active durable
authorization with epoch/deployment binding, current wallet/policy/gates and
TTL in the SAME write transaction as admission, reservations, usage and audit.

## Required next stages (not implemented)

C3-B must first freeze unit-aware risk policy and authorization max_total_input
semantics without summing unlike assets, guessing prices, silently reinterpreting
closed policy limits or creating a second/conflicting risk ledger. It must also
resolve zero-tip/fixed-sell compatibility explicitly, without raising tip or
fabricating copy intent. Shared closed-core changes require their own scope and
review. Typed C3-A fields document units; they do NOT solve that ledger design.

Only then: atomic decision/risk reservation/usage/authorization-budget/lifecycle/
audit, duplicate/restart conservation, followed by default-disabled C3-C HTTP
connection. Reservations are not signing/send permission. No C3-B/C3-C work is
included here, and C3 as a whole is NOT READY_FOR_REVIEW.

## Evidence and current status

Tests use isolated file-backed SQLite and the existing test-only C2 backend;
no socket or real-chain calls are needed for C3-A tests. Evidence maps each
claim to named tests in `http-manual-execution-contract.json`. Whole-repository
regression fixtures may exercise legacy local fake signing/submission; zero
economic-call claims are scoped to this NEW adapter, not the entire service.

```ini
HTTP_C3_A = PASS
independent_review = PASS
formal_closeout = COMPLETE_LOCAL_ONLY
C3_B = NOT_STARTED
C3_C = NOT_STARTED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

No signer/SubmissionService/RawBroadcaster/SendRawTransaction/nonce allocator,
permit, risk reservation or authorization write is connected to this adapter.
Existing closed gates and W4-B ambiguity semantics are unchanged. Stop at the
next stage's scope gate; this local C3-A closeout does not enable live or claim
C3-B/C3-C/C3 as a whole PASS.

## Independent review and local closeout

A separate reviewer agent, with no author conversation context forked, reviewed
the exact implementation SHA above and found no C3-A blockers. Start/end HEAD
matched that SHA and the worktree was clean. The reviewer independently ran:

- `go test -race ./internal/tradeapi -run '^(TestHTTPC3A|TestUserHTTPTradeHasNoEconomicDependencies)' -count=1 -timeout=3m`: PASS (31.839s).
- `go test ./internal/tradeapi -run '^TestHTTPC[12]' -count=1 -timeout=3m`: PASS (2.468s).
- `go build ./...`, `go vet ./...`, `staticcheck ./...`: PASS.
- Diff/gofmt checks, 13 evidence-to-test mappings and all SHA256SUMS: PASS.
- Validation package: PASS (125 files at the reviewed baseline).

Go checks used the fixed toolchain/caches with GOPROXY=off and GOTOOLCHAIN=local.
The reviewer did NOT rerun all repository tests/race, contact a real chain,
prove economic admission/budget compatibility or execute HTTP live trading.
These are not implied by its scoped PASS.

Historical implementation evidence `http-manual-execution-contract.json` is
preserved unchanged, including its original READY_FOR_REVIEW/NOT_PERFORMED
labels. Separate `http-c3a-joint-review.json` records formal local review and
closeout. No runtime label, code, migration or configuration is changed by
closeout. No remote main/CI/PR landing is claimed.
