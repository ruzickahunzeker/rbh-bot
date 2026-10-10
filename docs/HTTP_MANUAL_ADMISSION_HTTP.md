# HTTP C3-C — default-off MANUAL HTTP admission

Implementation base: `e33c0bc32280c6599b818148913c6d55b4b71cfe` (local C3-B
formal closeout). Branch: `http-c3c-default-off-admission`.
Ceiling: HTTP_C3_C_READY_FOR_REVIEW, not independent PASS or all-C3 PASS.
Independent review NOT_PERFORMED. Delivery remains local only; no GitHub.

## Frozen scope and unchanged user contract

Robinhood 4663, native-quote Pons v2 Curve only. Reuse authenticated
`POST /v1/trades` on the existing Unix-domain listener and the original ordinary
user parameters, exact Gwei fees, optional user floors/caps UNSET, original
absolute TTL and idempotency key. No client-facing dry-run/prepare prerequisite,
additional admission/replay/send route, public listener or internal IPC proxy.

```text
authenticated ordinary user request
  -> C1 immutable intent / Operation / ExecutionStep
  -> C2 saved internal resolution and validation
  -> C3-B one-command atomic reservation / usage / decision / audit
  -> admission observation, NOT execution/signing/send permission
```

This adds a transport composition and read projection, not a second financial
ledger or state machine. C3-B writer, migrations 001–020, shared trade kernel,
W4-B ambiguity/recovery semantics and existing closed-gate records are unchanged.
No-arm, no-permit and no-PASS-snapshot guards remain in the production schema.
Actual MANUAL authorization arming still requires a separately reviewed scope;
this slice cannot make a real positive grant. Positive tests remove no-arm only
in disposable dummy fixtures; a dedicated test restores the equivalent guard
and proves HTTP does not remove it or grant authorization.

## Default-off operator configuration

`RBH_TRADE_API_MANUAL_ADMISSION` defaults to false. Opt-in composition requires
the existing private API config, MANUAL intake and internal validation enabled,
an explicit durable validation policy version and live disabled. Incomplete or
malformed configuration fails before registering `/v1/`.

The existing private regular config file (no symlink, private permissions,
dedicated API credential distinct from internal IPC) may contain this
deployment-owned expected scope ONLY when admission is explicitly enabled:

```json
{
  "manual_admission": {
    "authorization_id": "operator-owned-authorization-reference",
    "authorization_epoch": 1,
    "manual_policy_id": "operator-owned-manual-risk-policy",
    "environment": "operator-owned-environment",
    "build_sha": "1111111111111111111111111111111111111111",
    "release_id": "operator-owned-release",
    "deployment_id": "operator-owned-deployment"
  }
}
```

This is an illustrative config fragment, NOT a usable authorization, current
binary attestation or instruction to enable it. The scope is copied at handler
construction. Every new C3-B admission verifies it against durable authorization
identity/epoch/unit/policy/gate evidence and current controls on the owned
writer transaction. The file does not create or arm rows, declare units on a
historical authorization, increase caps or reset usage. Constructor performs
no RPC or authorization grant; expected-reference existence/activity is enforced
by admission, not advertised as startup readiness. Scope fields remain forbidden
in user bodies/query/header overrides. Duplicate/case-alias/unknown/null/missing
config keys, fractional/string/overflow epoch and invalid identifiers reject.
The config parser rejects a scope block when admission is disabled, rather than
silently accepting an inconsistent enable/reference pair. To restart with a
disabled handler, turn off the flag AND remove that block (retain the same
principal/wallet credential scope). Historical GET/duplicate data is unaffected.
Emergency stop/revoke are durable controls and do not require config removal
or disable the already registered query endpoints.

Expected deployment identity is supplied by trusted operator composition;
binary measurement/deployment authorization and live release approval are not
new capabilities proven by this slice. Config identity is never accepted from
the untrusted trade request.

## Ordering, duplicates and partial durability

With admission enabled, an advisory stop-first control read prevents known
stop/unavailable controls from proceeding to validation RPC. This read is not
permission and is NOT a replacement for C3-B's same-writer final gates. A
stop/revoke after validation is independently rejected by atomic admission.

Before running C2, a scoped read snapshot checks for a complete original
admission. Complete history goes directly to C3-B dedupe, not to C2's
initial-only lifecycle validator. If another caller admits during C2, only a
complete canonical history can resolve the race; no retry/requote/new expiry
is generated. Concurrent validation may invoke read-only validation backends
more than once before the immutable winner commits; no claim of exactly-one
RPC invocation is made. Durable result, reservation and budget consumption
remain exactly once.

Intake, validation and admission are separate durable transactions. Admission
rollback does not erase previously committed intent/validation. A later
projection failure or lost HTTP response after admission COMMIT likewise does
not imply rollback. Keep `Location` and reuse the ORIGINAL idempotency key to
query or retry. Do not automatically mint a new key or expiry.

Historically rejected keys remain rejected. Same key with changed normalized
parameters/TTL conflicts. Already committed admission under an enabled handler
still observes C3-B stop/scope checks and returns history only; revoke/expiry
does not renew permission. Disabled/validation-only composition can return
existing historical admission without calling the writer or validation RPC.

## Observations and errors

Top-level C1 `status=RECORDED`/`reason_code=VALIDATION_NOT_CONNECTED` remain
historical intake fields, not current transaction outcome. Optional `validation`
is the saved C2 result. Optional `admission` exposes only:

- Operation/reservation ID, current reservation state, original contract hash,
  expires_at and admitted_at.
- First successfully committed POST: ADMITTED_NOT_SIGNED_NOT_SUBMITTED.
- Duplicate and every GET: HISTORICAL_ADMISSION_NOT_PERMISSION.
- execution_authorized=false and send_authorized=false, always.

Top-level duplicate describes intake identity; an earlier intake retry can be
the first successful admission. Judge the admission observation, not duplicate,
to distinguish that case. All submission/send/live flags remain false.

GET by request/key uses authenticated caller/wallet scope. Admission projection
verifies complete identity, canonical source and exact lifetime allowance in a
single SQLite read snapshot, without querying RPC, using a new clock or requiring
active controls/authorization. Stop/revoke/expiry/disabled composition preserve
history and frozen exposure. Corrupt/partial history rejects; never silently
omit admission, refund allowance or repair ledgers. The overall response combines
immutable intake/validation with a reservation-state observation; it is not a
cross-stage permission snapshot. Mutable history is observational only.

Known control stop/unavailability returns 503 MANUAL_ADMISSION_UNAVAILABLE;
other verified admission rejection returns 422 MANUAL_ADMISSION_REJECTED.
Existing authentication/wallet/conflict/TTL/unverifiable-RPC mappings remain.
DB/commit/projection faults return sanitized 503 API_UNAVAILABLE, with no SQL,
private scope, credential or internal exception text exposed. GET availability
does not advertise recovery service health or authorization readiness.

Capabilities reports default-off composition and static stage, never active
authorization/health/PASS. `/events` retains MANUAL_ORDER_INTAKE_ONLY and its
existing cursor contract; no admission/recovery stream or per-transport-request
durable dedupe/rejection telemetry is claimed. Existing admission audit/outbox
remain durable in C3-B's transaction; expanding event delivery is a later scope.

## Author acceptance and next gate

`http-manual-admission-http.json` records actual final-source commands/results,
hashes and named test mappings. Tests cover single fresh POST (BUY/fixed and
percentage SELL/zero tip), defaults/config/immutable scope, authentication and
security injection, rejected-key permanence, stop/revoke/expiry/drift, canceled
context/closed DB, audit/outbox failure with partial earlier durability, actual
file close/reopen, historical query under disabled/stop/revoked/expired mode,
lost response after commit, two-handle HTTP duplicate contention and a real
two-validation barrier handing off to one admission.

Structural import/call checks and empty nonce/artifact/submission/permit tables
prove prohibited economic paths in THIS new graph/harness, not invented
whole-repository zero signing counts. Legacy tests may use fake economic APIs;
no real keys, grants, external-chain RPC or transactions are used.

Final-source author validation completed:

- Full repository ordinary tests PASS (trade 201.542s, tradeapi 144.227s).
  Initial socket-restricted sandbox execution failed on local UDS/loopback
  fixtures; the permitted local-fixture rerun passed. No real chain was used.
- C3-C verbose ordinary rerun PASS, 15 API tests, tradeapi 4.519s.
- Three-run C3-C/app/config focused race PASS (tradeapi 374.745s).
- Broader C1/C2/C3 compatibility/schema race PASS (tradeapi 800.949s, storage
  7.988s), including C3-B's 10,000 mixed-request conservation test.
- Local UDS/controlled-RPC transport race PASS (tradeapi 12.557s).
- Build/vet/staticcheck/gofmt/diff checks PASS; 18 named test mappings and 11
  source hashes match; package manifest PASS, 133 entries.
- YAML/local-reference check PASS: 9 paths, 53 references. Full OpenAPI schema
  validator was unavailable and no complete schema-validation claim is made.
- Offline model 39 tests and expected Curve 5 fixture events PASS; outputs
  stayed in a temporary directory, not overwritten historical evidence.

The broader race above is NOT all-repository race, and none of these runs is an
independent review. Source remained fixed throughout final validation; only
docs/evidence/checksum were subsequently completed.

```ini
HTTP_C3_B = PASS
HTTP_C3_C = READY_FOR_REVIEW
independent_review = NOT_PERFORMED
manual_HTTP_admission_default = false
manual_execution_signing_submission_adaptation = NOT_STARTED
deadline_capability = APPLICATION_TTL_ONLY
contract_deadline = false
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Stop at independent review. No self-PASS/closeout, downstream MANUAL execution
or operator grant, mainnet, W4-D/W5, BSC/Flap/Base/Solana or public API expansion.

Independent review must evaluate default-off production registration and config
failure before route registration; request/identity/auth-scope separation;
duplicate race handoff without revalidation of admitted lifecycle; stop/revoke
after C2 versus C3-B final writer controls; history projection completeness and
absence of new permission; partial prior durability and response-loss recovery;
unchanged schema/legacy queues; exact test/evidence mappings and prohibited-path
proof scope. Author test success does not preapprove any of these checks.
