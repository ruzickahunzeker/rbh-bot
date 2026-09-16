# C03｜Historical Fixture Status

Status: **C03-PONS PARTIAL / C03-LONG NOT STARTED**.

Read-only topic-scoped discovery found successful candidates for Pons launch, curve buy, curve
sell, graduation and two graduated-v4 swap directions. The graduation candidate contains
PoolManager `Initialize`, Pons `PoolRegistered` and a PoolManager `Swap` for the same pool ID in
one receipt. Candidate identities and discovery provenance are in `evidence/c03-discovery.json`.

RPC capture saved the raw transaction object, complete receipt/logs, canonical block header and
transaction index for all six candidates under `fixtures/historical/candidates/`. The capture
report is `evidence/c03-rpc-report.json`.

These files are deliberately labeled `RPC_CAPTURE_NOT_GOLDEN_EXPECTATION` and
`CANDIDATE_NOT_GOLDEN`. They do not yet contain independently reviewed parser expectations or a
transaction-prestate anchor. The two v4 swaps must be classified using PoolKey currency order and
signed deltas before naming them buy/sell.

## Promotion gates for C03-PONS PASS

- Replay every candidate through the locked parser SDK.
- Record independently reviewed normalized expected events.
- Confirm the graduation pool key and pool ID relationship.
- Classify both v4 swap directions.
- Add a reverted or deliberately invalid historical case.
- For quote fixtures, record transaction-prestate evidence, including earlier transactions in the
  same block where relevant.
- Complete C02 contract classification for every emitter used by the fixture.

Long discovery found no candidate in the scanned ranges; a full-history query timed out. This is
not evidence that Long has no history. `C03-LONG` remains `NOT_STARTED` and does not block the
Pons Feed slice.

