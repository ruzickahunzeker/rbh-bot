# HTTP-C3-B/B2 — transaction-local MANUAL input verification

Base: a09628a4bf4a9f7d352a84c9a865c2738042eb20 (local B1 checkpoint).
Branch: http-c3b-b2-transaction-inputs. Ceiling: HTTP_C3_B2_READY_FOR_REVIEW.
Independent review NOT_PERFORMED; atomic economic admission NOT_IMPLEMENTED.

## Boundary

This package adds disconnected, package-private SELECT-only helpers. No new
HTTP route/option/listener, startup worker, operator authorization API, signing,
submission, nonce, permit or broadcaster dependency. No migration is changed
or added. Migration 018 isolation stays intact in production.

The existing C3-A reader now delegates all source/contract checks to
loadManualExecutionContractTx. Its public Load still owns one read transaction
and exposes one detached parameter contract. Its two TTL checks and original
hash/parameter/allowlist/lifecycle checks are preserved. No C3-A historical
review is silently repurposed to approve new B2 behavior.

loadManualAdmissionInputsTx receives the caller's sql.Conn and scoped identity,
durable request ID and future operator-owned deployment/auth scope. It never
obtains another connection, begins/commits/rolls back the caller's transaction,
calls public Load, validates through RPC, or writes economic/durable state.
The future B3 caller MUST own BEGIN IMMEDIATE on this connection. B2 is not an
exported transaction API, does not prove transaction ownership from a bare
connection, and cannot substitute for B3's authoritative atomic commit gate.

## Verification order and output

1. Durable control/stop on the SAME connection. Missing/unreadable control
   rejects; known stop outranks even invalid secondary identities. Mode must
   be CONTROLLED_CANARY; effective/update timestamps canonical and not future.
2. Scoped original C1/C2 source and complete C3-A contract, TTL, policy hash,
   durable C04/C05/C08 identity, wallet/contract/token allowlists and lane checks.
3. Control policy equals saved policy; typed MANUAL policy source/hash equals
   the saved original C07 policy. ParsePolicy verifies SHA256 and complete
   canonical JSON, addressing B1's intentionally explicit SQL hash limitation.
4. Exact saved input unit/cap; gas_limit * fee_cap with zero tip preserved;
   native value+gas outlay cap and retained native snapshot balance; fixed or
   percentage SELL checked by exact saved amount/balance cross multiplication.
5. Durable attestation-set identity, then existing authorization and immutable
   unit declaration. No caller-provided security/transaction fields.
6. Final absolute TTL/auth expiry checks; final clock must be nonzero and must
   not precede the input-loader entry timestamp. This is not an external clock
   synchronization/attestation service.

Scope binds authorization ID/epoch, MANUAL policy ID, environment, immutable
build SHA, release and deployment. Authorizations must be ARMED, correct mode,
chain, wallet and original policy; canonical authorized/created/updated/expiry
times must be valid. Missing/revoked/expired/drifted identities reject.

For NEW MANUAL identities only, gate_evidence_set_hash is SHA256 of canonical
JSON for the ordered C04/C05/C08 array of Go records with fields Gate, Status,
EvidenceHash, AttestedAt. Each status/hash is checked against the frozen durable
gate baseline and each stamp must be canonical/non-future. Existing legacy
authorization/hash semantics are untouched. An arbitrary 64-character operator
or caller digest is not enough for the new MANUAL loader. No real grant is
issued by computing this checksum.

Unit declaration must match authorization epoch, MANUAL policy ID/hash, source
MANUAL, chain/asset/decimals/TRADE_INPUT, and original authorization creation
stamp. Policy must explicitly list that exact unit. BUY native and SELL token
inputs cannot share an authorization. Missing legacy declarations are rejected,
not inferred from amount/direction.

B2 accepts only unused declared identities: operation_count=0, total_input=0,
no authorization operation history, and a pure single-operation budget check.
Nonzero history is UNVERIFIABLE for this incomplete helper, not reset/refunded.
B3 must replace this incomplete check with immutable operation binding,
conservation, dedupe and lifetime consumption inside its single transaction.

The result retains PARAMETER_CONTRACT_NOT_ADMITTED, admission_authorized=false,
send_authorized=false. Policy/control/auth inputs and CheckedAt are diagnostic
transaction-local values, not a gate snapshot, reusable permit or capability.
No returned object can safely authorize an action after commit/stop/revoke.

## Tests and evidence

The command/result mapping is evidence/http-manual-transaction-inputs.json.
An initial 4-minute combined race run timed out during SQLite migration with
multiple heavy suites concurrent; it is not recorded as PASS or a race finding.
Final B2 three-run focused race and full affected-package race passed. The
longer combined three-run rerun passed (244.004s), as did the final ordinary
whole-repository run (trade 112.425s, tradeapi 20.685s). Build, vet, staticcheck,
gofmt/diff, 39 offline reference tests and five expected-curve events passed.
This is author validation, not independent review or a final B2 full-repository
race claim; the earlier B1 full-repository race is separate evidence.

- Same explicit writer connection with DB MaxOpenConns=1: no nested connection
  deadlock; caller COMMIT still succeeds; total_changes unchanged by loader.
- BUY/fixed SELL/percentage SELL, exact calldata/quote/expiry/hash/fees/units;
  zero tip unchanged; backend Resolve/Complete counters remain one from C2.
- Stop highest priority; missing/disabled/malformed/future control fail closed.
- Authorization epoch/deployment/environment/policy drift, missing declaration,
  wrong unit and nonzero/corrupt budget reject without writes.
- A shape-valid false-hash policy inserted WITHOUT disabling SQL guards is
  rejected by the Go loader; metadata never becomes trusted permission.
- TTL/auth expiry -1ns / equality / +1ns, late expiry and backward/zero clock.
- Typed input/gas cap +1, combined native outlay, retained balance and exact
  fixed SELL ratio; exact limits accepted without scalar cross-token summing.
- Uncommitted stop is visible inside caller transaction; loader leaves caller
  rollback responsibility intact.
- Actual file-backed SQLite close/reopen and migration rediscover identical
  contract/amount/quote/hash/expiry/unit/auth budget with no RPC.
- AST/dependency guards forbid writes/nested transaction/economic calls. The
  new helper has no production caller; tests use local dummy identities only.

The positive fixture deliberately drops ONLY the B1 no-arm guard in an isolated
test database, then transitions a dummy identity. This is not a production
operator grant; budget/permit/PASS guards remain. Corruption tests explicitly
drop narrowly targeted guards/FKs only in their disposable local databases.

## Remaining B3/B4 requirements and non-claims

The next concrete work order is HTTP_MANUAL_ATOMIC_ADMISSION_IMPLEMENTATION.md.

This is not risk admission, nor proof of complete risk/authorization accounting.
Active exposures/count windows/legacy-unit compatibility, nonzero usage rows,
idempotent duplicate handling, lifecycle transitions, reservations/decisions,
budget consumption, audit/outbox and final commit serialization remain B3/B4.
No 10,000 economic admission or crash-window atomicity claim is made from B2.
Snapshot balances are not current-chain oracles. Recovery health is not claimed.

```ini
HTTP_C3_A = PASS_LOCAL_CLOSEOUT
HTTP_C3_B1 = READY_FOR_REVIEW
HTTP_C3_B2 = READY_FOR_REVIEW
HTTP_C3_B = NOT_READY
B3 = NOT_STARTED
B4 = NOT_STARTED
C3_C = NOT_STARTED
deadline_capability = APPLICATION_TTL_ONLY
contract_deadline = false
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Local only. No GitHub push/PR/merge, real-chain RPC, signing/sending, authorization
grant, live, W4-D/W5 or chain/protocol expansion. Author validation is not PASS.
