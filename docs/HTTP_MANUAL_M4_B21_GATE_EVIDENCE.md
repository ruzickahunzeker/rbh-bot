# MANUAL M4-B2.1 — disconnected DB gate-input evidence

Branch: `http-m4b21-gate-evidence`.
Base: `59358daaf6ac686bbf01eb032f3a7f877468098a` (local B1 closeout).
This refines only the first B2 contract step under the existing M4 freeze.

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4-A = PASS
M4-B1 = PASS
M4-B2.1 = READY_FOR_REVIEW
checkpoint = M4_B21_READY_FOR_REVIEW
independent_M4_B21_review = NOT_PERFORMED
M4-B2 = INCOMPLETE_NOT_READY_NOT_PASS
M4-B = INCOMPLETE_NOT_READY_NOT_PASS
M4 = INCOMPLETE_NOT_PASS
M4-B2.2 = NOT_STARTED
M4-B2.3 = NOT_STARTED
M4-B3 = NOT_STARTED
M4-B4 = NOT_STARTED
M4-C = NOT_STARTED
M4-D = NOT_STARTED
M5 = NOT_STARTED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## What this contract actually proves

Additive038 records the exact DB input projection at a statement's SQLite
snapshot, bound to original complete MANUAL foundation/Facts/contract/artifact,
active authorization identity/epoch/build/release/deployment, controls including
updated_at, policy/allowlists/runtime baseline, original reserved risk/usage,
conserved aggregate usage and independent SUBMISSION lease holder/epoch/expiry.
No legacy `canary_admission_sources` is manufactured. Nothing charges/refunds
usage, allocates nonce, extends TTL, changes original artifact or creates another
economic state machine. Stopped/disabled/missing control is checked first.

Table `manual_submission_gate_evidence(id,operation_id,source_json,source_hash,
checked_at)` is immutable. ID is deterministic from operation/source hash/exact
checked_at; duplicate insertion rejects, including IGNORE/REPLACE. A different
exact observation may create another metadata row, not another expiry window,
operation, permit or charge. Its stage is FIRST_BROADCAST_DB_EVIDENCE and status
DB_EVIDENCE_NOT_PERMISSION. All four action flags AND trusted-clock, running-
deployment, RPC-runtime and cryptographic-artifact verification flags are false.
FreshGateRequired remains true. No Go writer/reader, API/config/startup caller,
key/clock/network/transport interface or production capability is added.

Direct SQL supplied checked_at must be canonical UTC absolute time, at or after
foundation/artifact/control/auth/policy/allowlist/lease source stamps and before
original operation TTL, authorization expiry and lease expiry. Equality at any
expiry rejects; timestamps are parsed, not lexicographically compared. This is
relational DB validation, NOT an assertion that checked_at is trustworthy current
wall time. SQL can capture/backdate metadata without a process clock proof.
Nor does observing a lease row prove this process owns it, reading build_sha
prove the running binary matches, or observing runtime_code_hash prove RPC
runtime identity. Future B2.2/B3 writer must independently authenticate exact
artifact, verify operator-owned running scope/lease, trusted current time with
sticky reversal detection, read-only runtime evidence and fresh controls under
its final fence. No caller-supplied Boolean can stand in for those checks.

Records remain historical after stop/revoke/freeze/lease deletion/restart.
History does not rejoin mutable eligibility; new INSERT does. A fresh record
still is not a gate decision. No canary runtime PASS snapshot or permit is
created, and no prior no-arm/no-permit/no-PASS/downstream guard is opened.
UNKNOWN_REPLAY positive evidence/permit remains unavailable until a separately
reviewed unknown-phase contract and query-first tx/receipt/nonce evidence exist.
This slice does not implement immediate-pre-send or a send/outcome transaction.

## Atomic witnesses and phase boundary

One exact INFO audit and alert are created by the same INSERT statement. A
transient scope is checked and deleted before success; namespace/identity/
rowid/IGNORE/REPLACE/victim/adoption protections reject witness preseed, repair
or replacement. Legitimate alert delivery updates are allowed, not identity
changes. New evidence audit details explicitly require TEXT even in history;
the earlier037 same-byte BLOB historical limitation is not rewritten or fixed.

038 uses an insert-prohibited empty FK target. Any retained scope has an
unsatisfied deferred FK even if its parent evidence row exists. Under an
explicit transaction, an injected arbitrary witness RAISE(FAIL) may leave
provisional evidence/scope after statement failure, but COMMIT fails with FK787;
ROLLBACK clears all of it. Autocommit fails without durable residue. Tests do
not label a provisional row a successful rollback or valid record. Writers must
still roll back errors. No general hostile-schema or FK-OFF claim is made.

Source admission verifies original foundation completeness. Preclose validates
only the exact foundation bytes and this statement's witnesses; after scope
deletion, complete rejoins original complete foundation and is checked again
before INSERT succeeds. A deep old signed-phase view returned no row from the
initial recursive close oracle despite becoming complete after interruption;
temporary probes isolated that context-dependent observation. The correction
separates preclose from postclose verification, without altering old001–037 or
accepting incomplete final source. Final-source positive/history and negative
tests cover both. No broader SQLite engine diagnosis is claimed.

## Compatibility, evidence and limits

Migration001–037 and all old product Go/evidence remain byte-identical. Three
old test metadata adjustments: migration count36->37 (014 absent), latest-schema
37->38, and037 upgrade's comparison excludes new manual_gate_* objects. New038
upgrade independently enumerates every prior sqlite_master object and requires
its exact original SQL after migration; old guards are not exempted from that
check. Migration failure at the journal rolls back every038 object; repeat
migration creates no evidence and does not repair retained corruption.

Ten API tests cover exact BUY/fixed/percentage SELL and actual reopen, false
permissions/proofs,20 rehashed source overrides across four INSERT modes,
encoded types/canonical time/reversal, operation/auth/whole/fractional lease
expiry ±1ns/equality,18 durable drift cases and RECOVERY lease independence,
two-handle stale-source stop/revoke/takeover rejection, ABORT/IGNORE/FAIL audit/
alert modes, uncommittable provisional scopes, four concurrent contenders,
duplicate/rowid/victim protections, retained missing/BLOB witness no repair,
no-arm/no-permit/no-PASS/no-runtime source guards, namespace/orphan scope and
fresh observation without economic/TTL change. Two upgrade tests plus owner
migration validation are included in focused regression.

The first nine-test focused run failed28.330s because the initial close oracle
rejoined deep old completeness while the new transient scope was still open.
An attempted view materialization barrier alone still failed1.775s. Disposable
/tmp overlay probes preserve the raw context-dependent observations. After the
phase separation, three-side check PASS2.098s; full nine tests PASS28.628s.
Final ten-test focused validation PASS29.490s; storage3 PASS0.500s. Earlier logs
are not relabeled final source validation. Exact later full/race/build/manifest
results and log hashes belong in additive B2.1 evidence.

Final-source full ordinary validation PASS:
`go test -p 1 ./... -count=1 -timeout=20m -json` (storage11.121s,
trade165.594s, API359.751s, app0.201s), including all10 B2.1,13 B1 and9 M4-A
API tests. Build/vet/staticcheck PASS. Offline reference39 (2.703s) and expected
Curve fixtures5 PASS, not real-chain evidence. Selected single race PASS:
storage3/API8 top-level tests (13.007s/194.382s), six B2.1 plus two source-
dependency compatibility tests. It is not all B2.1 tests or full repository race.
Existing loopback/UDS fixtures required approved local-test
network access only, no real key/RPC endpoint/grant/chain transaction.

Final integrity checks cover gofmt/whitespace, all173 prior manifest entries plus
one additive evidence entry (package174), source/document/log hashes,11 named
test mappings against final ordinary PASS events and six selected race events,
original001–037/old product/evidence preservation, and exactly three legacy test
metadata/comparison adjustments. Temporary logs are not shipped. No independent
review has been performed; this is author validation only.

Reused positive fixtures seed synthetic ARMED grants and offline signatures;
the exact018 no-arm is restored before B2.1 actions. This is not intact-
production arming or real authorization. Retained corruption tests restore
guards/FK before tested actions. Zero economic capability applies to the new DB
evidence path, not every offline/legacy setup. Real close/reopen and explicit
COMMIT failure are not hardware power-loss proof. No independent review,
whole-repository race, remote CI or real-chain evidence is claimed.

Highest checkpoint M4_B21_READY_FOR_REVIEW only, not whole B2/B/M4 readiness or
PASS. No B2.2/B2.3/B3/B4/C/D/M5/W4-D/W5 or other chain/protocol is entered.
No production wiring, real key/RPC/grant/mainnet/send/live/release change or
remote GitHub operation follows. Scope remains Robinhood4663/Pons v2 Curve/
native quote, APPLICATION_TTL_ONLY and contract_deadline=false.
