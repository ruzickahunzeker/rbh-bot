# C03｜Historical Fixture Status

Status: **C03-PONS-CURVE PASS / C03-PONS-V4 PARTIAL / C03-LONG NOT STARTED**.

Read-only topic-scoped discovery found successful candidates for Pons launch, curve buy, curve
sell, graduation and two graduated-v4 swap directions. The graduation candidate contains
PoolManager `Initialize`, Pons `PoolRegistered` and a PoolManager `Swap` for the same pool ID in
one receipt. Candidate identities and discovery provenance are in `evidence/c03-discovery.json`.

RPC capture saved the raw transaction object, complete receipt/logs, canonical block header and
transaction index for the six successful semantic candidates, one supporting curve-launch context
and one historical revert under
`fixtures/historical/candidates/`. The capture report is `evidence/c03-rpc-report.json`.

All seven receipts now replay successfully through locked parser SDK v0.4.0 when processed in
block/transaction order. The supporting launch seeds the registry required to attribute the later
curve buy and sell. Observed normalized output is in `evidence/parser-replay-observed.json`; it is
explicitly not an independently authored golden expectation.

Pons Curve launch/buy/sell expectations are now independently authored under
`fixtures/historical/expected/`. `scripts/verify_curve_expected.py` derives the same fields
directly from raw topics and 256-bit ABI words without importing parser-sdk. A Go CI test then
replays the raw receipts through the locked parser and compares its normalized projection with
those expectations. This also prevents JSON tooling from silently rounding large token amounts.

PoolKey plus signed-delta classification currently observes:

- `0xd6e22e...45ff7` as **sell**: the registered token is currency1 and its pool delta is positive.
- `0x7e9002...ce35` as **buy**: the registered token is currency1 and its pool delta is negative.

Raw captures remain labeled `RPC_CAPTURE_NOT_GOLDEN_EXPECTATION`; expectations are separate files.
Curve expectations are independently verified, while v4 fixtures still lack independent expected
events and transaction-prestate anchors. The v4 direction labels above remain observed
classifications until independently reviewed.

## Remaining promotion gates for C03-PONS-V4 PASS

- Record independently reviewed normalized expected events.
- Confirm the graduation pool key and pool ID relationship.
- For quote fixtures, record transaction-prestate evidence, including earlier transactions in the
  same block where relevant.
- Complete C02 contract classification for every emitter used by the fixture.

The search is closed by a real historical negative, not by the stop-loss. Across 60,001
non-overlapping blocks it examined 591 Pons-targeted transactions with zero block or receipt read
errors and found one reverted Pons Factory transaction:
`0xa056fd548d5251ba0ccbbf005932d1ee665eca0dc5fdcb6d2e79451b32954e4e` at block 64339048,
transaction index 5. The canonical capture contains the raw transaction, failed receipt and block
header; its independent expectation is `fixtures/historical/expected/pons-factory-revert.json`.
Aggregated coverage is frozen in `evidence/c03-revert-search-final.json`. No right-outer scan is
required.

The synthetic safety package remains supplementary evidence and is not the reason for PASS:

- `fixtures/synthetic/pons-reverted-receipt.json` is explicitly marked
  `SYNTHETIC_LOCAL_SAFETY_TEST_NOT_HISTORICAL`.
- `internal/feed.ReceiptGate` persists the failed receipt as an audit fact but returns zero
  economic events and `copy_eligible=false` before invoking parser-sdk.
- The deterministic test attaches real launch logs to a locally failed receipt and proves the
  parser registry is not mutated; the later curve-buy receipt consequently remains unattributed.
- A control test confirms the unchanged successful launch receipt still produces two eligible
  normalized events.

Long discovery found no candidate in the scanned ranges; a full-history query timed out. This is
not evidence that Long has no history. `C03-LONG` remains `NOT_STARTED` and does not block the
Pons Feed slice.
