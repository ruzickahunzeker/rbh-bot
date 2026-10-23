# HTTP-C3-B/B3 prerequisite — typed reservation accounting foundation

Local base: dbea860acbb5bff4fc5cb625f7d1b75b813b439c.
Branch: http-c3b-b3-accounting-foundation.
Scope: B3_ACCOUNTING_FOUNDATION_ONLY; not complete B3/B4 or economic admission.
Independent review NOT_PERFORMED. HTTP_C3_B remains NOT_READY.

## Implemented boundary

Additive migration 019 overlays canary_risk_reservations with immutable
manual_reservation_units. No previous migration changes. The parent reservation
remains the only owner of amount, gas, wallet, state and quote; the overlay
contains unit/source/policy identity only, never another financial balance.
Fresh migration creates no metadata/reservations/authorizations/decisions.

The package-private SELECT-only loadManualExposureTx reads from its caller's
same sql.Conn. A future admission writer MUST own BEGIN IMMEDIATE. This bare
connection helper cannot prove transaction ownership; it has no production or
HTTP caller, and returning exposure is not permission or a decision. This
batch deliberately leaves all B1 no-arm/budget/permit/PASS-snapshot guards intact.

## Schema source binding and audit

One metadata row references one existing reservation and one original MANUAL
operation. INSERT binds parent wallet/token/original policy/input/gas/quote
block+hash/result hash/creation stamp to the immutable C1/C2 source, resolution
and validation. MANUAL policy source version/hash and input-unit declaration
must match; BUY is native18, SELL is the exact resolved token and decimals.
Absolute expiry and request fingerprint are preserved, never recomputed.

Metadata UPDATE/DELETE/REPLACE rejects. Bound parent identity UPDATE/DELETE/
REPLACE also rejects, including amount, gas, source, quote and creation time.
Existing parent state/updated_at transitions remain owned by existing lifecycle
code; this migration adds no release/refund/termination semantics.

Metadata INSERT, MANUAL_RESERVATION_UNIT_BOUND runtime audit and INFO alert
outbox are one SQL statement. Forced audit/outbox failure aborts that statement.
If a caller inserted the parent earlier in the transaction, it MUST roll back
the entire transaction on error: statement ABORT alone does not undo earlier
statements. Tests explicitly prove owner rollback leaves neither parent nor
metadata/audit/outbox. This is not schema-complete economic admission coupling;
there is not yet an independently usable atomic admission writer/command.

SQLite verifies source identity/JSON fields, not SHA256 cryptography. The Go
loader independently verifies canonical C1 request/API policy, C2 resolution/
original policy/result hashes, saved validation/calldata/gas and MANUAL policy
canonical SHA256 before returning any measured exposure. SQL metadata alone
cannot grant authorization, signing or execution rights.

## Authoritative read semantics

Read parent rows on the same snapshot. Relevant rows are reserved/frozen
regardless of age/TTL, or any state created in the inclusive rolling window
[now - WindowSeconds, now]. Window counts include released/committed history
and count wallet/global operations across assets and original policies; release
does not refund those counts. This conservative rolling window is not the
legacy truncated bucket projection. No existing projection is rebuilt/reset.

All relevant rows require their exact typed metadata and valid saved source.
Unreadable/untyped/invalid/future/noncanonical data rejects the entire result;
no partial exposure is returned. Irrelevant older released/committed rows are
not active exposure. Untyped legacy buckets whose interval may overlap the
window reject; the original policy window duration is checked, not just text
ordering/start time. Unreadable/malformed bucket identity also fails closed.

Active input sums use exact (chain,asset,decimals,TRADE_INPUT) keys with checked
uint256. Current policy must explicitly support every active unit. Native
outlay is original native call value + derived gas_limit * fee_cap; SELL token
input never becomes wei or adds to gas. Wallet/global native outlay has its own
unit. Pure cap equality/+1, retained balance and overflow checks are exercised,
but cap enforcement in an atomic admission transaction remains future work.

The historical loader does not invoke the initial-only C3-A reader, check
current TTL/authorization/control or discard ambiguity on stop. Expired frozen
rows remain measured. This is historical accounting, not a recovery service
health check or new permission. New admission must still run all B2 control/
source/allowlist/auth/TTL gates and final commit checks.

## Shared legacy compatibility (explicit review impact)

Legacy Store.AdmitControlledCanary rejects a MANUAL source before interpreting
it as a legacy admission source OR returning generic duplicate success. Exact
duplicate rejection remains REJECTED, with a new rejected attempt and no second
decision/reservation. REJECTED decisions/attempts/audit remain recordable;
schema prohibits ADMITTED/DEDUPED/QUEUED decisions/attempts and SQL upgrading/
replacing a MANUAL rejection. Direct SQL cannot attach MANUAL source to legacy
source table.

Once ANY typed reservation metadata exists, the legacy raw-scalar path rejects
TYPED_ACCOUNTING_REQUIRES_MANUAL_PATH. Schema rejects new non-MANUAL legacy
reservations and legacy usage INSERT/UPDATE too. This is deliberately a GLOBAL
and PERSISTENT history guard, even after typed rows are released/old; there is
no silent automatic switch back, relabelling or deletion of history. Legacy-only
databases without typed metadata retain their prior path. Emergency stop keeps
its earlier highest priority, and deterministic rejections remain auditable.

This extension requires fresh shared-compatibility review. Historical C07/W1/W2
PASS does not preapprove new guards. Query/receipt/canonical/reorg recovery and
W4-B send-intent ambiguity semantics are unchanged; no recovery dependency is
added by this foundation. Future typed admission must also reject relevant
legacy history and prove both modes cannot bypass conservation.

## Evidence and remaining work

Test mapping and exact author-validation results live in
rbh-validation-package/evidence/http-manual-accounting-foundation.json.
Direct SQL binding/parent mutation tests, forced audit/outbox rollback, 24-way
metadata insert contest on two handles, actual SQLite close/reopen, same-writer
uncommitted visibility, mixed BUY/SELL buckets, window counts/expiry/freeze,
corrupt/untyped/unsupported-unit rejection and dependency guards are covered.
These are METADATA tests, NOT 24 economic admissions or 10,000 conservation.
Disposable tests insert parent/metadata fixtures directly; no authorization is
created or armed in this new foundation suite. Corruption cases explicitly drop
only narrowly named guards in disposable databases. Existing regression tests
may use fake signer/send fixtures; zero new dependency claims concern the new
production foundation graph, not all test fixture calls in the repository.

Final-source author validation passed: full ordinary tests (trade 177.194s,
tradeapi 31.259s), three focused race runs (198.303s), full repository race
(trade 621.191s, tradeapi 249.226s), build/vet/staticcheck, formatting/diff,
39 offline tests, five expected-curve events and 129-entry validation manifest.
The prior full race run was intentionally terminated with exit 143 after the
duplicate guard changed; it is not a PASS or final-source evidence. No race
report was observed in that superseded run. The final full race is separate.
These are author results, not independent review or an admission PASS.

Next work order remains HTTP_MANUAL_ATOMIC_ADMISSION_IMPLEMENTATION.md:
complete source/lifecycle/dedupe compatibility, immutable full admission binding,
schema-complete shared financial coupling, nonzero authorization conservation,
single-transaction writer and final gates, fault/commit barriers, and 10,000
mixed admission proof. B2 still accepts unused authorization history only.
No guard is removed or economic API/HTTP composition exposed here.

```ini
HTTP_C3_A = PASS_LOCAL_CLOSEOUT
HTTP_C3_B1 = READY_FOR_REVIEW
HTTP_C3_B2 = READY_FOR_REVIEW
B3 = ACCOUNTING_FOUNDATION_IMPLEMENTED_ATOMIC_ADMISSION_NOT_IMPLEMENTED
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

Local only: no GitHub, signing, submission/send, nonce allocation, permits,
real-chain RPC, operator grants, live, W4-D/W5 or new chain/protocol scope.
