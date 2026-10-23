# HTTP-C3-B/B3 — exact schema math and immutable parameter prerequisite

Local base: d75fc90d83c8252f64680d7b5f7af682eee2669e.
Branch: http-c3b-atomic-manual-admission.
Scope: ATOMIC_ADMISSION_PREREQUISITES_ONLY.
Independent review NOT_PERFORMED. HTTP_C3_B remains NOT_READY.

This checkpoint follows migration 019's typed accounting foundation. It does
NOT implement an economic admission command/writer or complete B3/B4. No
migration is added or modified and no B1 isolation guard is removed. The next
additive migration remains unallocated until its implementation base is checked.

## Selected exact-arithmetic option

The previously optional deterministic SQLite primitive design is now selected
and implemented in internal/storage/manual_math.go. It has no database access,
wall clock, network, authorization, financial mutation or signing capability.
Four functions are registered once at package initialization before connection
creation; registration also applies to new pool connections and close/reopen.
Registration failure is fatal; there is no permissive fallback.

| Function | Contract |
|---|---|
| manual_u256_add(a,b) | Canonical decimal TEXT, checked uint256 sum; overflow errors |
| manual_u256_cmp(a,b) | Canonical decimal TEXT; exact -1/0/+1, never REAL/CAST |
| manual_u256_sum_json(array) | Array of canonical decimal strings; [] is zero; invalid element or overflow rejects the whole result |
| manual_time_cmp(a,b) | Canonical UTC RFC3339Nano absolute times; exact -1/0/+1, including fractional/whole-second boundaries |

Uint256 input rejects NULL, INTEGER, REAL, BLOB, signs, whitespace, leading zero,
fraction/exponent and out-of-range values. SQL aggregate input must come from
verified TEXT amounts. JSON numeric values and NULL elements never get skipped
or rounded. SQLite TEXT-column affinity can coerce literals before any function
sees them: these functions are NOT proof of a caller's original numeric type.
The future command must validate the immutable canonical C1/C2 source and JSON
types rather than trust a coerced scalar. Cryptographic source/policy hashes
remain independently verified in Go, not by these arithmetic functions.

These are scalar primitives, NOT unit-aware aggregation. The future command
must independently bind/filter the exact declared chain/asset/decimals/kind
before summing authorization inputs. It must not combine SELL token input with
native gas or sum different input assets. The existing Go manualrisk checks
remain mandatory and are cross-checked with 1,000 seeded full-width pairs.

An external client without registered functions cannot write a schema that
depends on them. A separate executable importing only SQLite proves the
missing-function error leaves zero inserted probe rows. Existing migration
001..019 does not yet reference these functions; existing ledger writes gain
NO new authority or numeric enforcement merely from their registration.

Arithmetic CHECK/trigger failure aborts its statement, not automatically every
prior statement in its caller transaction. Probe tests demonstrate both the
single-statement atomic failure and required whole-owner rollback explicitly.
Probe tables are disposable math fixtures, not another financial ledger or
evidence of complete financial admission coupling.

## Parameter/source separation, without new permission

The private verifyManualContractParametersTx reloads scoped immutable C1/C2
source, canonical request/API policy/resolution/result hashes, original policy
and gate identity, exact Operation/ExecutionStep identities and quote/expiry/
calldata/gas parameters. Fixed-amount SELL uses the original exact ratio check;
it is not converted to percentage or re-resolved.

It does not check current TTL, lane, control, authorization or economic
lifecycle, does no writes and returns PARAMETER_CONTRACT_NOT_ADMITTED with
admission_authorized=false and send_authorized=false. A future historical
duplicate loader must verify a COMPLETE immutable admission command plus
reservation, operation usage and aggregate conservation BEFORE using it. This
helper does not itself implement that duplicate loader or economic dedupe.

The public C3-A reader still owns one read transaction and requires initial
validation lifecycle, exact successful Operation/ExecutionStep statuses, entry
and final TTL checks, wallet/contract/token allowlist and non-busy lane. It uses
the shared verifier without relaxing these gates. Equivalent wallet address
case/prefix keeps the prior identity comparison semantics. Corrupt source or
scoped identity rejects without returning a partial contract.

Tests with synthetic admitted/submitted/unknown/manual-resolution statuses
prove immutable history can be checked without making public C3-A admissible.
The synthetic frozen lane remains frozen; verifier writes/backend calls are
zero. These statuses are test fixtures, NOT implemented admission transitions.
MANUAL-specific lifecycle labels remain excluded from legacy copy-oriented
Store.Recoverable; generic admitted/executing must never be used for MANUAL.
Direct-SQL lifecycle coupling is still pending, not claimed by this checkpoint.

## Validation and remaining implementation

Exact commands, final-source hashes, test mapping and historical failed rounds
are recorded in evidence/http-manual-atomic-prerequisites.json. Connection
registration tests use TWO SEPARATE files/pools with 32 callers and forced
replacement, followed by actual close/reopen. They are not shared-budget
contention or 10,000 economic admission evidence.

Final-source author validation passed: full ordinary tests (trade 83.098s,
tradeapi 17.507s), three focused race runs (storage 6.906s, tradeapi 41.315s),
full repository race (trade 629.319s, tradeapi 273.290s), build/vet/staticcheck,
formatting/diff, 39 offline model tests, five expected-curve events and the
130-entry validation manifest. Earlier
fixture-failing rounds and the six-minute broad focused-race timeout are NOT
PASS and do not substitute for these final-source runs. Author validation is
not independent review or proof of complete atomic financial admission.

Next work inside the accepted B3/B4 scope, without new confirmation per step:

1. One immutable admission command overlay on existing ledgers; schema-complete
   reservation/unit/authorization usage/aggregate/decision/attempt/lifecycle/
   audit/outbox coupling. Reject standalone ledger halves and identity drift.
2. Nonzero lifetime conservation, complete historical binding verification,
   exact durable dedupe/conflict and MANUAL-only lifecycle transition guards.
   Replace ONLY budget isolation once their complete replacements are proven;
   no-arm/permit/PASS-snapshot guards remain until separately scoped wiring.
3. Package-private disconnected writer owning BEGIN IMMEDIATE, stop-first and
   final absolute TTL/auth/control rechecks. Return success only after COMMIT.
4. Actual financial fault-window and two-handle mutable-control commit-barrier
   tests, file-backed restart, 10,000 mixed admission conservation, regression
   and independent review. Do not substitute math/metadata tests for this proof.

```ini
B3 = ACCOUNTING_MATH_PARAMETER_PREREQUISITES_IMPLEMENTED_ATOMIC_ADMISSION_PENDING
B4 = NOT_STARTED
HTTP_C3_B = NOT_READY
C3_C = NOT_STARTED
independent_review = NOT_PERFORMED
deadline_capability = APPLICATION_TTL_ONLY
contract_deadline = false
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

No GitHub, HTTP route/options/startup wiring, signer, submission/send, nonce,
permit, real-chain RPC, actual authorization grants, live, W4-D/W5 or additional
chain/protocol scope. Existing closed gates are not reclosed or self-approved;
shared changes require fresh independent review.
