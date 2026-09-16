# C03｜Historical Fixture Status

Status: **C03-PONS PARTIAL / C03-LONG NOT STARTED**.

Read-only topic-scoped discovery found successful candidates for Pons launch, curve buy, curve
sell, graduation and two graduated-v4 swap directions. The graduation candidate contains
PoolManager `Initialize`, Pons `PoolRegistered` and a PoolManager `Swap` for the same pool ID in
one receipt. Candidate identities and discovery provenance are in `evidence/c03-discovery.json`.

RPC capture saved the raw transaction object, complete receipt/logs, canonical block header and
transaction index for the six semantic candidates plus one supporting curve-launch context under
`fixtures/historical/candidates/`. The capture report is `evidence/c03-rpc-report.json`.

All seven receipts now replay successfully through locked parser SDK v0.4.0 when processed in
block/transaction order. The supporting launch seeds the registry required to attribute the later
curve buy and sell. Observed normalized output is in `evidence/parser-replay-observed.json`; it is
explicitly not an independently authored golden expectation.

PoolKey plus signed-delta classification currently observes:

- `0xd6e22e...45ff7` as **sell**: the registered token is currency1 and its pool delta is positive.
- `0x7e9002...ce35` as **buy**: the registered token is currency1 and its pool delta is negative.

These files are deliberately labeled `RPC_CAPTURE_NOT_GOLDEN_EXPECTATION` and
`CANDIDATE_NOT_GOLDEN`. They do not yet contain independently reviewed parser expectations or a
transaction-prestate anchor. The v4 direction labels above remain observed classifications until
independently reviewed.

## Promotion gates for C03-PONS PASS

- Record independently reviewed normalized expected events.
- Confirm the graduation pool key and pool ID relationship.
- Add a reverted or deliberately invalid historical case.
- For quote fixtures, record transaction-prestate evidence, including earlier transactions in the
  same block where relevant.
- Complete C02 contract classification for every emitter used by the fixture.

Long discovery found no candidate in the scanned ranges; a full-history query timed out. This is
not evidence that Long has no history. `C03-LONG` remains `NOT_STARTED` and does not block the
Pons Feed slice.
