# Trade API: disabled submission intake and status

Implementation base: `main@7b278c4ffd48fe75c8f20d25185f0743222684b8`.
This slice adds an authenticated API contract, not transaction submission capability.
The implementation is pending independent review; it does not close W4-C or enter W4-D/W5.

## Transport and authentication

Use the existing trade-service HTTP-over-Unix-domain-socket transport
(`RBH_SOCKET_DIR/trade-service.sock`, default `./data/run/trade-service.sock`).
There is no new TCP listener, public gateway or unauthenticated business endpoint.
The existing `RBH_INTERNAL_AUTH_SECRET` (at least 32 bytes) authenticates the request.
The API shares this internal trust domain; it is not a multi-tenant/user authorization API.
Its wallet scope is the server-configured `RBH_DRY_RUN_WALLET_ID`, not a caller override.

Use `internal/ipc.NewUnixClient` and `Authenticator.Sign` from another service in this
repository. Sign every retry with a fresh authentication nonce while retaining the
same business idempotency key. Required headers are `X-RBH-Timestamp`, `X-RBH-Nonce`,
`X-RBH-Body-SHA256` and `X-RBH-Signature`. The signature is the hex HMAC-SHA256 of
`METHOD\nREQUEST_URI\nTIMESTAMP\nNONCE\nBODY_SHA256`; request URI includes the query.
Timestamp is Unix seconds; the existing verification window is 30 seconds.
The existing authentication nonce replay cache is process-local, not durable across
restart. Business idempotency is durable in SQLite; neither mechanism enables sends.
Never expose the shared secret to browser clients or commit it to source control.

All new responses carry `Cache-Control: no-store` and the IPC request ID header.
The existing dry-run and prepare-execution endpoints are unchanged. In particular,
prepare-execution can sign; this slice does not claim that the whole service has no signer.

## Endpoints

| Method and path | Behavior |
| --- | --- |
| `POST /internal/trade/operations/{operation_id}/submission-requests` | Validate existing durable identity and record an immutable disabled rejection; never queue or send. |
| `GET /internal/trade/operations/{operation_id}` | Redacted snapshot of existing operation, steps, submissions and receipt observations. |
| `GET /internal/trade/operations/{operation_id}/events?after=0` | Incremental API rejection audit metadata only; not a full recovery/runtime event feed. |

Identifiers are 1–128 ASCII characters from `A-Z a-z 0-9 . _ : -`.
POST accepts exactly these case-sensitive JSON members (no duplicates or extra objects):

```json
{
  "wallet_id": "wallet-1",
  "idempotency_key": "api-request-1",
  "policy_version": 1,
  "expires_at": "2026-10-07T12:00:00Z"
}
```

The example expiry is illustrative: copy `policy_version` and canonical absolute
`expires_at` verbatim from the existing durable operation, never calculate a new TTL.
The operation must belong to the configured wallet and chain 4663, have
`deadline_capability=APPLICATION_TTL_ONLY`, and have one successful Pons v2 Curve
dry-run result. This API cannot create an intent, policy, quote, execution or source event.
Caller-provided raw transactions, calldata, nonce, gate claims, permits or authorization
are rejected. No signing, replay or recovery trigger endpoint is introduced.

A valid initial request returns **HTTP 503**, not 202:

```json
{
  "id": "<durable-request-id>",
  "operation_id": "<existing-operation-id>",
  "wallet_id": "wallet-1",
  "idempotency_key": "api-request-1",
  "policy_version": 1,
  "expires_at": "2026-10-07T12:00:00Z",
  "outcome": "REJECTED",
  "reason_code": "SUBMISSION_DISABLED",
  "created_at": "<server-time>",
  "duplicate": false,
  "send_authorized": false,
  "submission_queued": false
}
```

`SUBMISSION_DISABLED` is a terminal business rejection, not an instruction to poll for
automatic activation. Clients must not create new idempotency keys to retry a rejection.
The immutable ledger cannot be updated to queued/accepted later, even if controls change.

## Idempotency, TTL and audit

The idempotency key is unique within the wallet. Operation, wallet, policy and expiry
are bound to a request fingerprint; the rejection also binds the durable operation
fingerprint. A changed operation/policy/expiry for an existing key returns 409.
New keys cannot bypass the operation's immutable policy or expiry.

Initial admission requires `now < expires_at`; equality is expired. Missing,
non-canonical or unverifiable expiry fails closed. An exact duplicate retrieves the
original 503 rejection after expiry/restart without minting a new expiry, audit event,
alert or economic action. Retrieval still requires authenticated wallet scope.
Status/events remain readable after TTL expiry and do not change reservations or lanes.

Additive migration 014 creates only the immutable `REJECTED/SUBMISSION_DISABLED` ledger.
A schema trigger writes `TRADE_API_SUBMISSION_REJECTED` into `canary_runtime_audit`
and an INFO alert-outbox row in the same transaction. Either insertion failure rolls
back the whole request. Outbox insertion is not proof that an external alert was delivered.
UPDATE/DELETE are prohibited; previous migrations and execution state machines are unchanged.
Concurrent SQLite writer contention can return 503: retry with the same key and fresh
HMAC nonce to retrieve the committed rejection. There is no in-memory request queue.

## Read views, bounds and errors

GET is scoped to the configured wallet; missing/out-of-scope operations return 404.
Status exposes operation identity, TTL capability, step IDs/statuses, submission
IDs/attempt IDs/tx hashes/states/sequences, and receipt block/status/canonical metadata.
It never exposes raw/encrypted artifacts, calldata, private keys or arbitrary audit
`details_json`. Each status collection is capped at 100; larger operations return
503 rather than silently truncate financial history. `submission_enabled=false` and
`contract_deadline=false` are capabilities, not computed send readiness.

Events return `events` and `next_cursor`. Each event contains `cursor`, `id`,
`event_type`, `reason_code`, `created_at`. Cursor is a durable global AUTOINCREMENT
sequence filtered by operation; gaps are normal. Fetch successive pages of at most
100 with `after=next_cursor` until an empty page; persist the cursor across restart.
Only canonical unsigned decimal values in `0..2^63-1` are accepted; no leading zeros,
encoded/duplicate/unknown query members. POST/status accept no query parameters.

Invalid bodies/IDs: 400. Authentication failures (including the existing auth body
limit of 1 MiB): 401. POST wallet scope mismatch: 403. Missing operation: 404.
Identity conflict or a newly requested expired operation: 409. Store failures: 503
with sanitized `API_UNAVAILABLE`; a durable disabled rejection also uses 503 but
has the distinct record body shown above. No 503 response authorizes background work.

## Safety and follow-up boundary

The new handler depends only on Store, fixed wallet, clock and HTTP router. Its path
does not call ExecutionKernel, signer, SubmissionService, broadcaster, permits or nonce
allocation. Existing submitted/broadcast_unknown records remain queryable without
rebuild/resign/replacement/fee bump/new nonce. Ambiguous reservations remain frozen.

Production broadcast remains `NOT_CONNECTED`, controlled-canary authorization
`NOT_GRANTED`, `live=false`, `release_ready=false`; admission/send readiness stay false.
Existing recovery readiness remains independently determined by W4-C DB/RPC/lease health.
W4-C independent review is still pending; no PASS or closeout is implied by this slice.

Suggested follow-up order: independent review of this API contract; controlled caller
integration with these disabled responses; only separately authorized/reviewed work
may define future send admission. No enabling switch or latent send queue is included.
Pons v4, Long, LP, unrestricted live, public API authentication and SDK upgrades are out of scope.

Focused acceptance tests are `TestTradeAPI*` in `internal/trade/api_intake_test.go`;
machine-readable mappings are in the validation package's
`evidence/trade-api-submission-intake.json`.
