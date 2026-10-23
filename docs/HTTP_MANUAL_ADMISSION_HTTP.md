# HTTP C3-C — default-off MANUAL HTTP admission

## Formal closeout — current status

HTTP_C3_C = PASS; formal closeout COMPLETE_LOCAL_ONLY on
`http-c3c-admission-closeout`, based exactly on reviewed head
`500735151be1ca9fec59ac4bf7c258bbb88cd025`. Fresh independent review by the
separate `http_c3c_independent_review` agent concluded PASS with no blockers.
The initial review began without forked author conversation; the same reviewer
then received the scoped hardening handoff and independently re-reviewed it.
This is not a claim of a blind hardening review. The previous cancellation
blocker is closed: operational faults preserve their cause and return sanitized
503 API_UNAVAILABLE; verified invalid data still rejects, without changing
reservation/usage/TTL/gate semantics.

`http-c3c-joint-review.json` records the reviewed head, independent commands,
results and limitations. Original author implementation/hardening evidence is
preserved unchanged at its historical READY_FOR_REVIEW/PENDING checkpoints.
This closeout changes docs/evidence/checksum only. No GitHub fetch/push/PR/merge
or remote-main claim is made. Existing runtime capabilities remain static stage
descriptions; this document does not turn them into health or send permission.

PASS covers default-off HTTP admission and historical observation only, not C3
as a whole or MANUAL execution/signing/submission adaptation. No real grant,
signing/send/nonce/permit, live, deployment attestation or recovery health is
approved. Production broadcast NOT_CONNECTED; authorization NOT_GRANTED;
live=false; release_ready=false. Next work requires separately scoped MANUAL
execution/signing/submission design; it is not started by this closeout.

## Historical implementation and review checkpoints

Implementation base: `e33c0bc32280c6599b818148913c6d55b4b71cfe` (local C3-B
formal closeout). Branch: `http-c3c-default-off-admission`.
Ceiling: HTTP_C3_C_READY_FOR_REVIEW, not independent PASS or all-C3 PASS.
Original author checkpoint: independent review NOT_PERFORMED. Delivery remains
local only; no GitHub.

The paragraph above describes the original author checkpoint. Independent
review of `e3b1c504ed4d9bc17468e6c03b17d74c60a581a5` subsequently concluded
FAIL: late cancellation/DB failure in historical verification was incorrectly
reported as HTTP 422 MANUAL_ADMISSION_REJECTED rather than sanitized HTTP 503
API_UNAVAILABLE. No economic safety failure was identified. The hardening on
`http-c3c-operational-error-hardening`, based on that exact head, preserves
operational causes through canonical/policy/gate/usage/exposure verification.
Verified absent or invalid input remains a typed rejection. This necessarily
touches shared MANUAL verifier helpers, including the validation-policy gate
read; it does not change C3-B schema, ledger, risk, TTL, gate or atomicity rules.
Original author evidence and previous closed-gate records remain historical
and unchanged; current hardening evidence is
`http-manual-admission-error-hardening.json`. This historical checkpoint required
fresh independent review before closeout; its later PASS is recorded above and
in the separate joint-review evidence, not retroactively written into author
evidence.

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
ledger or state machine. Hardening only corrects shared MANUAL verifier error
propagation; C3-B writer atomicity/ledger/risk rules, migrations 001–020, shared
trade kernel, W4-B ambiguity/recovery semantics and existing closed-gate records
are unchanged.
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
HTTP_C3_C = PASS
independent_review = PASS
formal_closeout = COMPLETE_LOCAL_ONLY
reviewed_head = 500735151be1ca9fec59ac4bf7c258bbb88cd025
manual_HTTP_admission_default = false
manual_execution_signing_submission_adaptation = NOT_STARTED
deadline_capability = APPLICATION_TTL_ONLY
contract_deadline = false
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Formal closeout does not authorize downstream MANUAL execution or operator
grant, mainnet, W4-D/W5, BSC/Flap/Base/Solana or public API expansion.

Independent review must evaluate default-off production registration and config
failure before route registration; request/identity/auth-scope separation;
duplicate race handoff without revalidation of admitted lifecycle; stop/revoke
after C2 versus C3-B final writer controls; history projection completeness and
absence of new permission; partial prior durability and response-loss recovery;
unchanged schema/legacy queues; exact test/evidence mappings and prohibited-path
proof scope. Author test success does not preapprove any of these checks.

## Operational-error hardening acceptance

The new test-only query wrapper faults each actual historical command and
authorization-usage read after loading a successfully committed fixture command.
Cancellation, expired context deadline, a closed DB handle and SQL read failure
must preserve the operational cause and map through the actual HTTP error writer
to sanitized 503 API_UNAVAILABLE. Earlier reads at each later fault point have
already succeeded. Admission/reservation/usage counts and totals remain intact;
history afterwards retains the original contract and expiry. These are
verifier-to-error-mapper integration tests, not end-to-end timed HTTP cancellation
or mainnet availability evidence. Additional control/authorization/exposure
checks and verified-rejection regression tests retain fail-closed behavior.

Historical hardening checkpoint was READY_FOR_REVIEW pending fresh independent
review; it is now reviewed PASS at the exact head stated above. No new
route, production checkpoint, schema/migration, signer/submission/send/nonce,
authorization grant, public listener or downstream execution scope is added.
Production broadcast remains NOT_CONNECTED; authorization NOT_GRANTED;
live=false; release_ready=false. The original acceptance commands above remain
historical author results, not results for the hardening head.

Final hardening author validation: full ordinary suite PASS (trade 122.126s,
tradeapi 83.840s); C3-C/baseline negative-path focused race PASS (tradeapi
249.065s), plus all five new hardening tests with three race runs PASS
(129.352s). Build/vet/staticcheck/gofmt/diff, six source hashes, five named test
mappings and 134-entry manifest checks PASS. Offline reference model 39 tests
and Curve 5 fixture events PASS. Original reviewer overlay rerun by the author
PASS (1.045s), including the former cancellation failure; this author rerun is
not fresh independent approval. Full repository race was not run for this slice.

Fresh reviewer validation passed: original overlay including the formerly
failing cancellation probe (0.718s); C3-C/app/config plus selected C3-B negative
paths (10.373s/0.213s/0.017s); hardening race (18.614s); C2/C3-B input
compatibility (5.551s); exposure foundation (1.670s); focused vet/diff/source
hashes/test mappings and all 134 reviewed manifest entries. These are selected
independent tests, not independent full-repository tests/race. Fault probes are
verifier-to-error-mapper and local transaction checkpoints, not timed end-to-end
HTTP cancellation or real-chain availability. Formal closeout adds only its new
review evidence/checksum entry; the resulting package has 135 entries.
