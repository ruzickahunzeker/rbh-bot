# MANUAL M4-B2.1 — interrupted witness hardening

Local branch: `http-m4b21-gate-hardening`.
Hardening base: `e23be87b012b0b1ed391d93b5c7e7363b098d527`.
Original B21 base: `59358daaf6ac686bbf01eb032f3a7f877468098a`.
This is only correction of the unpublished disconnected B21 contract, not B2.2.

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4-A = PASS
M4-B1 = PASS
M4-B2.1 = READY_FOR_REVIEW
checkpoint = M4_B21_READY_FOR_REVIEW
original_M4_B21_independent_review = BLOCKED
hardening_independent_review = PENDING_FRESH_REVIEW
M4-B2 = INCOMPLETE_NOT_READY_NOT_PASS
M4-B = INCOMPLETE_NOT_READY_NOT_PASS
M4 = INCOMPLETE_NOT_PASS
M4-B2.2 = NOT_ENTERED
M4-B2.3 = NOT_ENTERED
M4-B3 = NOT_ENTERED
M4-B4 = NOT_ENTERED
M4-C = NOT_ENTERED
M4-D = NOT_ENTERED
M5 = NOT_ENTERED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## Independent finding, not a test-green closeout

The separately authorized reviewer found one P2 blocker at the exact original
head. Ordinary storage3/API10 passed, but a fresh SQL probe demonstrated:

```text
BEGIN IMMEDIATE
  evidence INSERT -> injected witness RAISE(FAIL)
  parent=1, scope=1, complete=0
  clear once-only fault using unrelated ordinary audit INSERT (no DDL)
  later exact audit INSERT; later exact alert INSERT; DELETE scope
COMMIT succeeds, complete=1
```

The same sequence succeeded after stop/revoke/risk-and-usage freeze/lease
deletion. This did not grant permission or change economic state, and historical
DB observation was never a fresh gate. It did refute the original same-INSERT /
no-adoption assertion under its explicitly included FAIL fault model. Merely
checking that an immediate COMMIT failed was insufficient. The old author
evidence remains a historical snapshot, not final-source or independent PASS.

Independent report: `/tmp/rbh-m4b21-independent-cYr1ZQ/review.md`, SHA256
`14e0cba434b62821bf702a800537dcf71f6577ee87d3da12143be99b046f67db`.
Independent full B21 race was intentionally interrupted after the confirmed
blocker; it was not a PASS. The initial reviewer probe timeout was a connection-
pool mistake after its successful COMMIT, not a product failure. Exact details
and artifacts remain attributed to that original report.

## Narrow correction, not weaker acceptance semantics

Only this unpublished additive038 and its B21 tests are corrected. Original
001–037, all product Go and every previous evidence file remain byte-identical.
This is not a new runtime writer or a second authorization/financial model.
No old no-arm/no-permit/no-PASS/submission/receipt/effect/release guard opens.

The INSERT now performs:

```text
BEFORE parent INSERT:
  exact fresh DB-source/time/identity validation
  reject existing parent, scope, audit or alert for this identity
  insert transient scope including exact checked_at
  insert exact audit and alert using scope-owned bindings
actual parent INSERT
AFTER parent INSERT:
  require exact parent/foundation/witness bindings to close scope
  require original full foundation + no retained scope for complete
```

An injected audit or alert FAIL can now retain provisional scope/witnesses, but
**not a parent evidence row**. Later statements can complete provisional
witnesses; that alone is not success and is not claimed impossible. They cannot
insert the exact parent using any tested conflict mode because its source guard
rejects adoption of the existing scope/witnesses. The scope cannot be deleted
without the parent. Deferred parent/empty-target FKs therefore still reject
COMMIT even after the attempted repair. Only ROLLBACK clears the failed scope;
a new clean statement can then succeed. This preserves same-successful-INSERT
provenance for committed evidence rather than silently accepting a same-
transaction repair guarantee.

The scope checked_at must be canonical TEXT, and its ID binds operation,
source_hash and exact checked_at. Audit creation uses original operation/
attempt/authorization/epoch and scope time/hash; alert creation uses the same
scope identity/time. Once a parent exists, initial-witness insertion is denied.
Historical complete still validates full original foundation after scope
closure without rejoining mutable current controls/lease.

## Regression and qualification

`TestHTTPM4B21InterruptedWitnessCannotAdoptOrCommit` promotes the independent
failure into permanent negative regression: both audit/alert faults, each of
INSERT/OR IGNORE/OR REPLACE/OR FAIL, with/without subsequent stop/revoke/freeze/
lease deletion (16 subcases). There is no DDL after the failed statement until
after rollback. It proves parent=0/scope=1, tries exact provisional witness
completion, rejects later parent adoption in all four conflict modes and scope
closure, complete=0, rejects COMMIT with FK787, then verifies complete rollback
and a successful fresh INSERT. Existing fault tests now explicitly require no
parent on FAIL. Orphan/adoption tests supply exact scope checked_at rather than
passing merely because a new NOT NULL field is missing.

All 11 B21 API tests and relevant storage/legacy regressions must be rerun on
the correction. Final commands, results, source/log hashes and exact limitations
belong in additive hardening evidence, not rewritten old author evidence.
Fresh independent review of a committed final head is required. Neither the
initial BLOCKED review nor an author test PASS closes this checkpoint.

Final-source full ordinary regression PASS: `go test -p 1 ./... -count=1
-timeout=20m -json` (app0.249s/storage21.654s/trade242.713s/API540.760s).
Focused storage3/API11 PASS0.460s/80.735s, including all16 new fault subcases;
build/vet/staticcheck PASS with empty logs; expected Curve5 and offline39 PASS.
Only existing controlled loopback/UDS fixtures needed local test network
permission, not real-chain access. The first four-test run PASS8.107s preceded
expansion from4 to16 subcases; it is not final test-source validation.

Attempted all-B21 race hit its20m timeout (API1200.250s, exit1), not PASS.
Storage3 completed PASS39.177s and10 original B21 top-level tests emitted PASS.
The new test had started10 subcases when timeout fired; no completed new
top-level/subcase PASS events were emitted, so none are inferred from progress
messages. The original JSONL/timeout stack is preserved. A separate focused
race command reran the entire new16-subcase test with its own10m timeout:
PASS, API340.040s/test338.930s, all16 subcase PASS events. Exact commands/log
hashes are recorded in additive hardening evidence. Do not relabel the
timed-out command, claim all11 in one successful race run, or claim full-
repository race. This author validation is not independent approval.

The guarantee is for intact FK_ON schema and the tested witness-fault modes.
It is not an assertion that arbitrary new hostile root AFTER triggers, dropped
guards, FK_OFF, hostile schema rewrites or hardware power loss are safe. Explicit
transaction errors still require writer rollback. Earlier037 limitations are
unchanged. An already-created experimental038 database is not silently upgraded
or repaired by editing this unpublished migration; tests use fresh databases
and upgrades from037. No user runtime database was opened or modified here.

DB observation is still NOT permission: trusted-current-clock, actual running-
deployment, RPC runtime identity and cryptographic artifact verification flags
remain false. checked_at can be backdated within relational source bounds;
lease metadata is not process ownership. Future B2.2/B3 checks are still needed.
All execution/signing/send/replay flags remain false. No UNKNOWN_REPLAY positive
contract, PASS snapshot, permit, reader/writer/HTTP/config/startup caller, key,
real RPC, financial charge/refund/release, nonce or expiry window is added.

Robinhood4663/Pons v2 Curve/native quote only; APPLICATION_TTL_ONLY,
contract_deadline=false. No B2.2/B2.3/B3/B4/C/D/M5/W4-D/W5, remote GitHub
operation, mainnet send, real authorization, live or release change follows.
After author validation the maximum remains M4_B21_READY_FOR_REVIEW. Any later
independent conclusion is a separately attributed record, not self-PASS.
