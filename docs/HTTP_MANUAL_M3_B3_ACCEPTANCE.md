# M3-B3-D — cumulative controlled-local acceptance

Branch: `http-m3b3-controlled-local-signing`.
Batch base: `334d6b4c51d1844d233421ae251ecde1e1fc74ec`, the docs/evidence-only
B3-C independent review checkpoint. Product implementation reviewed at
`ce9f46a08b1e1403171ad9f603937f98640508d4` is unchanged by D.

```ini
M3-B3-C independent review = PASS_SCOPE_LIMITED
M3-B3-D = AUTHOR_ACCEPTANCE_COMPLETE_PENDING_INDEPENDENT_REVIEW
M3-B3 = READY_FOR_REVIEW
review_state = PENDING_FRESH_CUMULATIVE_INDEPENDENT_REVIEW
M3-B4 = NOT_STARTED
M3 overall = INCOMPLETE_NOT_PASS
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## Changes and invariant ownership

This batch adds tests/evidence/docs/checksum only. 001–036 and every product
file remain byte-identical to the independently reviewed C head. There is no
new runtime state machine, schema guard exception, production key/query/grant,
HTTP/config/startup entry, submission/broadcaster/permit or new financial owner.
Original quote, amount, resolved SELL percentage, calldata, fee/zero tip, nonce,
policy/quote block/hash, authorization/deployment and absolute expiry remain.
Robinhood 4663 / Pons v2 Curve / native quote only; APPLICATION_TTL_ONLY,
contract_deadline=false. No B4/M4/M5/W4-D/W5 or remote GitHub operation follows.

The independent C review's original nine-test/25-case temporary probe is
promoted into a permanent, author-maintained regression file with D-specific
names and provenance hash. Promotion is NOT a fresh independent verdict. The
retained historical C report/evidence and author failures remain unchanged.

## Permanent acceptance matrix

Existing A/B/C tests continue to cover exact BUY/fixed SELL/resolved percentage
SELL, nonce bounds, signed-field substitution, authenticated encryption, source
gates, canonical time/expiry equality, direct SQL/IGNORE/REPLACE/rowid and
encoded-alias denial, no retained-history repair, atomic marker/artifact effects,
two-handle one-invocation competition and real stop/revoke writer fencing.
Only explicit SQLite code5+zero result is accepted as operational contention;
unknown errors, duplicate signatures or new permission always fail.

New permanent D regressions add:

- crypto KeyVersion/Encrypt/Decrypt panic/cancellation: fixed sanitized error,
  zero output, original ambiguity retained, no retry;
- late synchronous signer returning a valid signature after deadline: await
  its return before releasing the fence, reject output, do not abandon a worker;
- corrupt marker/artifact after actual reopen: deny without observer/signer,
  repair, artifact manufacture or accounting change;
- expiry during final signer-address verification: immediate callback rejects
  before any signer invocation;
- risk/usage freeze, authorization exhaustion and disabled wallet committed
  from a second DB handle between marker and final fence: zero signer calls;
- retained epoch/deployment/usage/durable-gate/allowlist corruption, with exact
  original guards/FK restored before tested action: zero signer calls;
- original lifetime budget exactly equal to both caps: one original operation,
  one signature, no extra budget charge;
- cancellation after either durable commit: zero response followed by correct
  ambiguity/artifact history, never renewed signing ownership;
- accepted valid-before-COMMIT metadata boundary: post-COMMIT clock expiry or
  reversal does not grant permission or alter authenticated artifact identity.

## Actual SQLite COMMIT failure, not a precommit callback

In disposable test DBs, extra test-only tables and an AFTER INSERT trigger
introduce a deferred FK violation into either the marker or artifact transaction.
The real SQLite COMMIT returns extended foreign-key error 787. All original
guards and product code remain intact; no migration is added.

Marker COMMIT failure: zero signer calls and no durable marker/effect; original
nonce claim/accounting remain. A later explicit request may sign the SAME
original nonce once after the test fault is removed. This is durably proven
pre-invocation rollback, not automatic retry or a new nonce/expiry window.

Artifact COMMIT failure: signer called once, marker/audit already durable;
attempt/step/lane/artifact/audit/alert outcome all roll back. Actual close/reopen
followed by another request yields only signing ambiguity, with no observer,
second signature, financial recharge or release.

## Real process-exit windows

A subprocess runs the existing local fixture with an ephemeral private key.
At each selected checkpoint it reports only non-secret fixture metadata and
measured counts to its parent, then calls os.Exit(86) while SQLite is still open.
Deferred rollback/DB close/test cleanup cannot run in that child. Its temporary
DB lives under a parent-owned temporary root; keys are never serialized in
arguments/environment/files. The parent opens the same DB with a fresh
composition holding NO signer, observer or clock.

| Exit point | Signer calls before exit | Rediscovered durable result |
|---|---:|---|
| after marker COMMIT | 0 | signing ambiguity only |
| after sign, before artifact insert | 1 | signing ambiguity only |
| after artifact insert, before COMMIT | 1 | artifact rollback, ambiguity only |
| after artifact COMMIT | 1 | exact verified immutable artifact history |

Each case requires original nonce/accounting, exactly one marker audit, artifact
audit present iff committed, empty transient scopes, no send/submission/receipt/
position/permit rows and three identical historical reads without durable
mutation or permission. This is actual OS process exit/reopen recovery, not
power loss, hardware durability, key availability or production integration.

## Validation and independent gate

Exact author commands/results, final source hashes, intermediate failures and
the test-to-evidence mapping belong to additive
`evidence/http-manual-m3b3-cumulative-acceptance.json`. No full-repository race,
remote CI, real-chain test or production health/telemetry is inferred from a
focused pass. Controlled local/loopback test fixtures are not mainnet endpoints.

Final-source cumulative single-run race PASS: all 6 manualartifact, 7 selected
storage and 51 selected API tests (1.242s / 18.086s / 1860.260s). This includes
the entire local B3 A/B/C/D test surface and HTTP static dependency check, not
the whole repository race suite. Real COMMIT failure cases took 27.49s and all
four process-exit windows 48.74s under race. One C concurrent caller received
explicit SQLite busy/zero result; signer calls remained one and unexplained
outcomes zero. No production behavior or timeout was relaxed to obtain PASS.

Final-source full repository ordinary PASS (`go test -p 1 ./... -count=1
-timeout=20m`): storage 10.625s, trade 160.944s and API 278.960s. The command used
only cached offline dependencies and controlled local loopback/UDS fixtures.
Full build/vet/staticcheck, test mapping/source hash checks, 39 offline model
tests and five Curve fixtures also PASS. No new D-batch intermediate test
failure occurred; prior stage failures remain historical and unmodified.

Completed author acceptance reaches M3_B3_READY_FOR_REVIEW only. A fresh
independent review of the cumulative B3 head, and later separately recorded
formal closeout, are still required for B3 PASS. Prior A/B/C scoped review is
not approval of these added D tests or whole-B3 completion. This batch never
grants signing/send permission, real canary authorization or live readiness.
