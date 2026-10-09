# HTTP-C3-B/B1 — pure measurement and isolated durable unit metadata

Implementation base: `0b26a9952593564ab09af8081d5dfc818b5968cb`.
Branch: `http-c3b-b1-unit-schema`.
Ceiling: HTTP_C3_B1_READY_FOR_REVIEW. Independent review NOT_PERFORMED.
Local delivery only; no GitHub push/PR/merge or remote-main claim.

## Accepted boundary and implemented scope

The user accepted a single input asset per MANUAL authorization and an additive
unit/accounting compatibility scope. B1 adds the pure `internal/manualrisk`
package, additive migration 018, controlled metadata and legacy API tests.
Old migrations are untouched; 014 remains on its separate unlanded branch.

No runtime configuration, production composition, HTTP route, operator policy
registration/authorization API or transaction consumer is added. The metadata
tables are empty on a fresh migration. Tests populate them using isolated dummy
SQL fixtures; production credentials, existing databases and real chains are
not accessed. No production Go execution/store implementation is changed.

## Measurement contract

Unit identity is `(chain_id, asset, decimals, accounting_kind)`. Only Robinhood
4663 is allowed. Native must declare 18 decimals; token must be a canonical
lowercase nonzero EVM address with explicit decimals. A missing unit is rejected.

TRADE_INPUT, NATIVE_GAS, NATIVE_VALUE and NATIVE_OUTLAY are distinct. Add rejects
different assets, decimals, chain or kind. Outlay is the explicit native-value
plus native-gas operation; SELL token input cannot be added to gas. Gas derives
from canonical uint64 gas_limit * positive uint256 fee_cap. Tip may be zero but
cannot exceed fee_cap; it is not added on top or increased.

Canonical integer quantities are uint256 decimal text; no signs, exponent,
float, aliases, rounding, truncation or overflow. Caps accept equality and
reject +1 unit. Fixed SELL uses exact arbitrary-precision cross multiplication
on uint256 input values; its temporary products need not fit uint256, and no
percentage is invented. Retained native balance is checked with overflow-safe
outlay+minimum arithmetic.

Budget.Check is a PURE check of one proposed operation against a single declared
input unit, operation count and total input cap. It does not create/consume a
durable reservation or budget, handle dedupe, or grant rights. Input gas is
excluded; unlike token/native/kind/decimals under one budget reject.

## Immutable policy metadata

manual_risk_policies binds a MANUAL policy ID/hash to the original C2 C07 policy
version/hash. JSON includes sorted unique asset limits (per-operation/wallet/
global active input), native gas/outlay/retained limits, exact sell bps and
wallet/global count windows. It does not reinterpret historical C07 scalars.

Go Policy.Canonical/ParsePolicy verifies exact normalized JSON, SHA256 digest,
source identity, supported units and consistent positive cap hierarchy. Unknown
fields, duplicates/order aliases and mismatched source/hash fail closed. Lookup
requires exact unit; no inferred decimals, asset conversion or valuation.

SQL verifies source FK/hash identity, object/array shape and required fields,
sorted unique unit identity, bounded/canonical decimal cap text and hierarchy.
Cryptographic SHA256 verification is intentionally NOT claimed by SQLite:
there is no configured hash function. B2/B3 consumers MUST call ParsePolicy
with independently loaded original policy identity before use. A direct SQL
shape-valid false digest can be recorded as metadata, but the Go loader rejects
it, and B1 isolation prevents it from becoming economic authorization. This
boundary has an explicit test; metadata alone is never permission.

UPDATE/DELETE/REPLACE reject, independent of recursive delete-trigger settings.
Policy insertion, runtime audit and alert outbox are one SQLite statement/
transaction. Forced audit/outbox failure rolls back the policy row.

## Authorization input-unit declaration

manual_authorization_units is one-to-one metadata on an EXISTING authorization,
not a second state machine/financial ledger. Its tuple binds authorization
ID/epoch, MANUAL policy ID/hash, chain/input asset/decimals/kind and MANUAL source.

Declaration is allowed only on an unused PENDING authorization with matching
original policy, zero durable count/total and no operation history, send permit
or PASS gate snapshot. created_at equals its original authorization creation
stamp. ARMED/REVOKED identities cannot acquire the declaration; old usage is not
reset/reclassified. Input unit must be explicitly listed in the MANUAL policy.
Native declarations are for native BUY input, token declarations for that exact
token's SELL input. B2 will bind this to the actual saved source side/quantity;
B1 does not yet load an order or decide admission.

Declaration is immutable; concurrent duplicate SQL INSERTs on two SQLite handles
have exactly one accepted row and one audit/outbox pair. Source/declaration/
audit/outbox insertion is atomic, and file-backed close/reopen preserves it.

## Intentional B1 isolation guards

For DECLARED MANUAL authorizations only, migration 018 prohibits:

- transition to ARMED and replacing its authorization identity;
- insertion of authorization operation usage or changing/replacing/deleting
  cumulative count/input usage (including moving the row to a legacy ID);
- PASS economic gate snapshots;
- FIRST_BROADCAST/UNKNOWN_REPLAY send permits.

The old runtime API cannot arm or consume one of these partially implemented
identities. A controlled corruption test removes ONLY the arming guard in an
isolated test DB; legacy budget consumption still fails its INSERT and rolls
back without usage or audit. This is not a real authorization/grant action.

Undeclared legacy authorizations follow their prior path. Regression exercises
the existing runtime/canary/HTTP code. These opt-in schema guards are an explicit
shared compatibility impact to review, not a rewrite of historical PASS evidence.

Guard removal/replacement belongs to a later additive migration with the COMPLETE
atomic MANUAL admission/source/accounting path and its review, never merely a
configuration toggle. B1 READY_FOR_REVIEW is not permission to remove them.

## Tests/evidence and non-claims

Evidence mapping is in evidence/http-manual-risk-unit-schema.json. Coverage:
pure arithmetic boundaries, policy/hash/lookup validation, direct-SQL unit/
source/structure/range mutation rejection, immutability, audit/outbox faults,
legacy API isolation, two-handle concurrent declarations, migration from 017
with an existing legacy ARMED identity and actual SQLite close/reopen.

No C3-B admission decision, reservation, usage consumption, operation/step state
transition, nonce, artifact, permit or HTTP composition is implemented. There
is no claim of 10,000 economic admission conservation/commit-window atomicity;
those remain B3/B4. Pure numeric checks are not present-chain balance/freshness
oracles. The new package has only whitelisted pure standard-library imports.
Whole-repository tests can exercise existing fake signing/submission fixtures;
zero-call claims refer to the NEW production B1 dependency graph only.

B1 implementation is local commit a09628a4bf4a9f7d352a84c9a865c2738042eb20.
The subsequently completed full repository race run passed (trade 623.476s,
tradeapi 141.760s). The run started with B1 sources before B2 edits and the B1
code remained unchanged; this is not final B2 validation, independent review
or a formal PASS.

```ini
HTTP_C3_A = PASS_LOCAL_CLOSEOUT
HTTP_C3_B1 = READY_FOR_REVIEW
HTTP_C3_B = NOT_READY
B2 = NOT_STARTED
B3 = NOT_STARTED
B4 = NOT_STARTED
C3_C = NOT_STARTED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Next authorized implementation packages remain B2/B3/B4 under the accepted
design. Existing historical C04/C05/C07/C08/W1/W2/W3 records are unchanged.
No author self-PASS, real authorization, mainnet, live, W4-D/W5 or remote actions.
