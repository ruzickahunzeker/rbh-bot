# HTTP API P7 — production composition scope freeze

Status: `P7_SCOPE_FROZEN`

P7 composes the already reviewed HTTP intake/query, controlled submission
worker, and recovery lifecycle. It defines process startup and readiness
boundaries; it does not grant canary authorization or enable production send.

## Composition modes

The composition must expose explicit modes:

```text
QUERY_ONLY
CONTROLLED_LOCAL
CONTROLLED_CANARY (authorization required; unreachable by default)
```

`QUERY_ONLY` may start the authenticated read-only HTTP surface with chain
query dependencies and a durable caller/wallet scope. It must not require the
signer, artifact encryption key, SubmissionService, or broadcaster merely to
answer token, balance, allowance, fee, and capabilities queries. Existing
legacy execution startup may retain its own prerequisites when that mode is
selected.

`CONTROLLED_LOCAL` composes the P3/P4/P5/P6 harnesses and durable leases for
tests. It has no production broadcaster and cannot enable live.

`CONTROLLED_CANARY` is schema/config gated by immutable deployment identity,
allowlists, policy version, C04/C05/C07/C08 attestations, risk budgets, and an
explicit authorization. A missing or contradictory value fails closed.

## Independent readiness

The process reports three separate readiness values:

```text
canary_admission_ready
submission_send_ready
recovery_ready
```

Admission and submission readiness stay false unless their own durable gates,
leases, controls, and authorization are valid. Recovery readiness reflects DB,
RPC, worker lease, and reconciliation health and is allowed to remain true
while stop, revoke, TTL, or admission gates block new economic actions.

## Lifecycle and dependencies

Startup must initialize durable DB/migrations, authenticate the local UDS
server, then compose only the dependencies required by the selected mode.
Shutdown must stop HTTP intake, drain submission/recovery workers, preserve
unfinished durable work, and close RPC/DB resources after drain. Restart must
rediscover durable work.

Submission and recovery use independent leases. Recovery has no signer,
SubmissionService, broadcaster, send permit, or nonce dependency. P5
send-intent and ambiguity semantics remain unchanged.

## P7 acceptance

- query-only startup does not require signer or broadcaster configuration;
- controlled-local composition starts only with explicit local harness config;
- default startup keeps all economic readiness false;
- no HTTP route bypasses admission, signing, submission, or recovery gates;
- readiness and metrics expose the three dimensions independently;
- worker lease loss and RPC/DB failure fail closed with durable evidence;
- shutdown/drain and restart preserve durable work;
- production dependency graph has zero real broadcaster/mainnet reachability;
- existing P0–P6 review receipts and checksums remain unchanged;
- no BSC/Flap/Base/Solana, strategy expansion, or unrestricted live path.

## Status ceiling

```ini
P0_P6 = PASS
P7 = READY_FOR_REVIEW
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```
