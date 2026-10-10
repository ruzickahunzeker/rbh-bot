# MANUAL M1 — read-only admitted execution input

Implementation base: `708163355be0b05e247d9597ee38951dabe1ed86`.
Local branch: `http-m1-admitted-execution-input-reader`.

This slice exposes an internal, disconnected `ManualAdmittedInputReader`.
It accepts authenticated identity, the original durable request ID and a
constructor-owned authorization/deployment scope. It adds no HTTP route,
startup composition, configuration, migration or execution action.

## Contract and invariants

`Load` uses one SQLite read transaction and returns a detached
`ADMITTED_INPUT_CANDIDATE_NOT_PERMISSION` or zero output plus an error.
`ExecutionAuthorized`, `SendAuthorized` and the embedded contract's permission
flags remain false. A successful read is neither a reservation nor a claim.

The reader verifies the canonical C3-B command and complete admission ledger:
original operation/validation step, caller/wallet/chain, source and command
hashes, immutable policy/allowlist, gate evidence identity, authorization
epoch/environment/build/release/deployment and original reservation binding.
The original validation step and all migration 016–020 protections are retained.

Saved BUY, fixed SELL and percentage SELL quantities, calldata, minimum output,
quote block/hash, gas limit, fee caps (including explicit zero tip) and absolute
expiry are unchanged. Percentage SELL uses C2's saved resolved token units,
not a new balance percentage. No quote, simulation, clock-derived new expiry
or parameter reconstruction occurs.

Emergency stop is the first durable control read. Current authorization must
be active and match the expected scope. Initial candidacy requires the command's
own C3-B reservation `reserved` and operation usage `RESERVED`. Frozen,
committed, released, consumed or already-attempted work is not a new execution
candidate. Other active wallet reservations, economic lanes and unresolved
execution steps fail closed. Current wallet/curve/token allowlists must match
the saved policy and runtime identity.

C3-B has already consumed the lifetime operation/input allowance. M1 verifies
aggregate usage against durable operation rows and caps, without charging again
or rejecting valid full-cap equality. Reads, duplicates, failures and restart
write no usage, audit, reservation, operation, step, attempt, permit or metrics.

Intent and authorization expiry use canonical absolute times: `now < expiry`
is valid; equality is expired. Clock reversal/unverifiability, malformed or
missing expiry and incomplete/corrupt ledgers reject. TTL and authorization
expiry are checked again after reads. Operational errors preserve their cause;
the existing error mapper returns sanitized 503 rather than verified rejection.
M1 itself adds no HTTP transport or availability endpoint.

## Snapshot is not permission

Control/auth/allowlist reads describe the transaction's snapshot, not mutable
state after it. A stop or revoke may commit after this snapshot; M1 does NOT
claim a send/admission TOCTOU fence. Any future economic writer must reload and
revalidate authoritative controls, TTL, wallet state and reservation in its own
atomic boundary. No downstream consumer is wired by this slice.

Candidate denial does not disable historical query or recovery. No new recovery
service or recovery-health claim is added. Legacy copy/canary execution remains
unchanged; MANUAL is not disguised as a legacy dry-run request.

## Validation and review boundary

`manual_admitted_input_test.go` covers exact parameter preservation, scope and
caller isolation, detached results, consumed-budget equality, stop/revoke/drift,
noninitial reservation/usage/attempt, expiry boundaries/final checks, damaged
ledger/allowlist, every-read operational faults, SELECT-only transaction
ownership, file-backed close/reopen and concurrent reads. Whole-table snapshots
and connection-local `total_changes()` verify no durable writes.

Disposable fixtures remove named guards only to inject future authorization or
corruption. This is not a real authorization and does not change migrations.
Dependency AST checks and the restricted change set prove no signer, submission,
broadcaster, nonce allocator, RPC or artifact creation dependency. Zero-economic
row assertions are controlled-harness evidence, not production telemetry. The
existing-attempt denial test intentionally inserts an inert legacy row and
verifies that the reader leaves it untouched; it is not a signing test.

Actual author commands/results and source hashes are recorded separately in
`rbh-validation-package/evidence/http-manual-admitted-input.json`. Independent
M1 review was not performed at the original author checkpoint. The maximum
implementation status is `M1_READY_FOR_REVIEW`, never author PASS or closeout.

### Independent review and commit-cancellation hardening

Independent review of `e0554187ecc5d664a7103f88063f3d526a18a692` concluded
FAIL. After the final clock read, context cancellation can cause `database/sql`
to auto-rollback before `Commit`, returning `sql.ErrTxDone` without retaining
`context.Canceled`. Zero output, sanitized 503 and no durable writes still held;
the defect was lost operational cause, not economic fail-open.

Local `http-m1-commit-cancellation-hardening` changes only the new reader's
Commit-error branch: if ErrTxDone and a context cause coexist, retain both with
`errors.Join`. Other DB/Commit errors remain unchanged. The public regression
uses a single-connection pool and competing SELECT as an auto-rollback barrier,
with real cancellation and a real deadline. It asserts both causes, zero output,
503, query-only operation, unchanged whole-table snapshot and usable fresh reads.
Applying this regression to the old source through a temporary overlay fails
for both causes. No production hooks or semantics beyond error identity change.

Historical author evidence remains unchanged; additive
`http-manual-admitted-input-cancellation-hardening.json` records repair evidence.
Fresh independent review is required; this repair does not authorize execution,
send, M2 or formal closeout.

## Later stages — not implemented

1. M2: additive binding to existing Operation → ExecutionStep →
   TransactionAttempt, preserving the original validation/admission source.
2. M3: separately authorized controlled signing/nonce harness.
3. M4: separately authorized exact-artifact submission/replay harness.
4. M5: MANUAL receipt/reorg/status projection through existing recovery.

No M2–M5, W4-D/W5, BSC/Flap/Base/Solana, production send, real grants, mainnet,
GitHub operation, live or release enablement is included.

```ini
HTTP_C3_B = PASS
HTTP_C3_C = PASS
M1 = READY_FOR_REVIEW
independent_review = PENDING_FRESH_REVIEW
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```
