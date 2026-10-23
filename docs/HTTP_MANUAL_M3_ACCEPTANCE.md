# MANUAL M3-B4 — cumulative controlled-local acceptance

Branch: `http-m3b4-cumulative-acceptance`.
Base: `df5060e3bb0d5d3748404a2d109db2616c8555cf`, the local B3 formal closeout.
Original M3 scope: `HTTP_MANUAL_SIGNING_SCOPE.md`.

```ini
M1 = PASS
M2 = PASS
M3-A = SCOPE_FROZEN
M3-B1 = PASS
M3-B2 = PASS
M3-B3 = PASS
M3-B4 = AUTHOR_ACCEPTANCE_COMPLETE_PENDING_INDEPENDENT_REVIEW
M3 = READY_FOR_REVIEW
checkpoint = M3_READY_FOR_REVIEW
M3 overall = READY_FOR_REVIEW_NOT_PASS
independent_cumulative_M3_review = NOT_PERFORMED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## Scope and change contract

This final implementation batch is tests/docs/additive evidence/checksum only.
No product file, migration 001–036, prior regression, historical evidence,
HTTP/config/startup composition, gate or accounting contract is changed.
The separately reviewed B1/B2/B3 checkpoints remain scoped approvals, not a
substitute for independent review of cumulative M3 after this new batch.

Robinhood 4663 / Pons v2 Curve / native quote only, BUY/fixed SELL/already-
resolved percentage SELL. APPLICATION_TTL_ONLY; contract_deadline=false.
The original Operation -> ExecutionStep -> TransactionAttempt, one reservation,
consumed lifetime allowance, exact amount/minimum/calldata/fee including zero
tip, nonce, policy/quote block/hash, authorization/deployment and absolute expiry
remain authoritative. No second ledger, state machine, grant or renewed window.

## New cumulative tests

`internal/tradeapi/manual_m3_acceptance_test.go` adds:

- One real SQLite-file chain from original intake/admission through M1, M2,
  B1, B2 and B3 for BUY/fixed SELL/resolved percentage SELL. Actual close/reopen
  after binding, nonce claim and signed artifact preserves every durable row.
  Exact original contract, fee/zero-tip, expiry and consumed accounting survive;
  one nonce claim and one local signature only, no requote/resimulation.
- Old M1/B1 readers and M2/B2 writers cannot treat later-phase work as initial
  permission. A signed artifact stays out of legacy dry-run discovery and
  controlled submission recovery discovery. Fresh B3 history composition has
  no observer, signer or clock and cannot resume invocation.
- Existing HTTP GET and duplicate POST inspect a previously signed local
  fixture after stop/revoke/expiry and risk/usage freeze. They expose admission
  history only, no raw/cipher/owner material or economic permission. Original
  expiry/counters/full DB snapshot stay unchanged, no new validation or signing.
  This characterizes existing API history, not a newly wired signing endpoint.
- Three probes from the cumulative B3 review are promoted with source SHA256
  provenance into author-maintained permanent regressions: reading the retained
  owner's value on a second real DB handle cannot resume that invocation; a
  correct observation returned after query deadline is rejected without marker
  or signature; cancellation in the final invocation callback calls no signer.
  Promotion is not another independent approval.

`internal/tradeapi/manual_m3_dependencies_test.go` adds a whole cmd/internal
product-source AST/import scan. Private nonce/signing writers may be referenced
only in their owner files; only the private signing writer may import manualsign.
No production startup imports the M3 crypto/nonce packages. The new local graph
cannot import networking, RPC, trade/config/app composition or hold named
submission/broadcaster/permit/allocation capabilities. Existing transitive SDK
dependencies are not claimed absent; this is a reviewed source-graph guard,
not arbitrary malicious-programming protection or runtime telemetry.

## Cumulative matrix and attribution

Existing M1/M2/B1/B2/B3 regressions stay byte-identical and supply the detailed
fault/expiry/cancellation/corruption/SQL/upgrade/concurrency matrices. B4 reruns
them together with new integration tests and the existing legacy execution,
submission, recovery, C04/C05/C07/C08 and production-default-off regressions.
The latter remain controlled local fixtures; test broadcasts/signatures are
not real endpoints or production actions. Do not sum zero production path
claims into the false statement that all positive test signers/sends are zero.

Selected single-run race emphasizes B4 plus B2 concurrent claims/control fence,
B3 one-signer/control fence, actual COMMIT failure and four process exits.
Selected race is not full-repository/full-M3 race. Exact commands, source
hashes, actual results and intermediate failures belong in additive
`evidence/http-manual-m3-cumulative-acceptance.json`; author and independent
results must remain distinct. Original author B3 full cumulative race and all
older stage reviews are historical evidence, not fresh B4 results.

Final-source author validation completed:

- `go test -p 1 ./... -count=1 -timeout=20m -json` PASS. Storage 10.471s,
  trade 154.248s, API 285.864s; includes all 111 M3 API top-level tests
  (B1 16 / B2 39 / B3 50 / B4 6), plus existing whole-repository regressions.
- Selected cumulative single race PASS: pure artifact 6 / nonce 3 / storage
  17 / API 13 tests, 1.323s / 1.024s / 38.028s / 256.659s. All six new B4
  tests pass. C concurrency retained one signature, one explicit SQLITE_BUSY
  zero-result rejection and zero unexplained outcomes. Stop/revoke fence,
  B2 concurrent claims/control boundary, actual COMMIT failure and all four
  process-exit windows pass. No timeout or product behavior was relaxed.
- Whole-repository `go build ./...`, `go vet ./...`, staticcheck, gofmt and
  diff checks PASS; 39 offline model tests and five Curve fixtures PASS.

The initial formatting command could not find gofmt on the default PATH;
explicit cached-toolchain formatting succeeded before the full runs. Some
diagnostic reads guessed nonexistent old helper filenames and were corrected
using repository search. No compilation/test assertion failure occurred in
this B4 batch. Existing full-suite loopback/UDS fixtures required approved local
test permissions; no real-network trading authorization was requested or used.

Author raw JSON test logs and offline report live temporarily under
`/tmp/rbh-m3b4-author-8KGGc1`, with hashes recorded in the additive evidence.
They are not committed logs and are not guaranteed after cleanup/restart.
No remote CI, full-repository/full-M3 race, real-chain or independent cumulative
M3 approval is claimed. Evidence integrity checks add one manifest entry while
preserving all previous entries and historical evidence.

## Safety and remaining gate

The marker is a signing ambiguity boundary, not proof signing happened and
not a resume token. Failures after marker commit retain original identity and
freeze, never automatic resign/rebuild/replacement/fee bump/new nonce/release.
Post-valid-COMMIT metadata is not current time-validated action permission.
Trusted bounded synchronous local signer/cipher/query/clock and reviewed FK-ON
schema are assumed. Cancellation awaits late trusted crypto but rejects its
output; it cannot forcibly terminate untrusted code. Owned-buffer clearing is
not secure erasure of Go/library copies. Reopen/process exit is not power loss.

No production query/key/signer adapter, HTTP execution endpoint, real RPC,
SubmissionService/ReplayUnknown/raw-send/broadcaster/permit, mainnet/canary
authorization, live/release or other-chain change is allowed. M4/M5/W4-D/W5
remain outside this batch. No GitHub fetch/push/PR/merge is performed.

Maximum result is M3_READY_FOR_REVIEW. Stop there for separately authorized
fresh cumulative independent M3 review; do not inherit whole-M3 PASS from B3,
create closeout automatically or infer permission for production signing/send.
