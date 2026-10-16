# MANUAL M3 formal closeout — controlled local only

Reviewed cumulative B1/B2/B3/B4 head:
`eed70549bb8c4f02fb1fc62e09d7a151a985fcf4`.
Original M3 scope base: `7f75de8fda7f4d8d8bbfe5ff8a284b73ec667304`.
Closeout branch: `http-m3-signing-closeout`, created from that exact reviewed
head with ahead/behind `0/0` and a clean worktree.

```ini
M1 = PASS
M2 = PASS
M3-A = SCOPE_FROZEN
M3-B1 = PASS
M3-B2 = PASS
M3-B3 = PASS
M3-B4 = ACCEPTED_IN_CUMULATIVE_M3_CLOSEOUT
M3 cumulative independent review = PASS
M3 = PASS
M3 formal closeout = COMPLETE_LOCAL_ONLY
M4 = NOT_STARTED
M5 = NOT_STARTED
C04 = PASS
C05 = PASS
C07 = PASS
C08 = PASS
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## What PASS covers

Only the frozen controlled-local signing/nonce harness: B1 detached read-only
bound envelope, B2 one original nonce/attempt claim, B3 first-owner-only local
signing ambiguity/fenced exact encrypted artifact outcome, and B4 cumulative
compatibility/fault/concurrency/reopen/dependency evidence. Robinhood 4663 /
Pons v2 Curve / native quote, BUY/fixed SELL/already-resolved percentage SELL.
APPLICATION_TTL_ONLY; contract_deadline=false.

Operation -> ExecutionStep -> TransactionAttempt remains authoritative. Original
request/amount/minimum/calldata/fee including zero tip, nonce, policy/quote block/
hash, absolute expiry, authorization/deployment, reservation and consumed lifetime
usage remain exact. There is no second ledger, charge/refund, new nonce/expiry/
epoch window, requote or percentage re-resolution. All candidate/history results
have false execution/sign/send permissions. PASS is not production authorization.

The durable signing marker is an ambiguity boundary, not proof the signer ran
and not a resume token. Post-marker failures retain the original claim; no
automatic resign/rebuild/replacement/fee bump/release. Trusted synchronous crypto
is fenced through exact verified outcome COMMIT. Valid committed metadata after
later expiry/clock change is identity history, not fresh permission.

## Fresh cumulative independent review

Separate reviewer `m3_cumulative_independent_review` received a scoped handoff
including historical reviews, author results and parent crosschecks without
forked author conversation. This is not a blind review. It inspected cumulative
027–036, original deny domains, pure/storage helpers, B1/B2/B3/B4 and relevant
M1/M2/C3 verifiers, then independently executed the following on the exact clean
head. Prior stage PASS records were not inherited as whole-M3 approval.

- Ordinary: six artifact / three nonce / 39 storage / 112 API tests PASS
  (0.034s / 0.004s / 6.006s / 173.402s). API includes all 111 M3 tests
  (16 B1 / 39 B2 / 50 B3 / 6 B4) plus the HTTP no-economic-dependency guard.
- Two fresh probes/four subcases PASS (1.885s): normal-SQL attempt-free BLOB
  operation-wallet work added after B2 rejects B3 at initial/second fence;
  stop/revoke on a second connection cannot commit during synchronous Encrypt.
- Selected single race: artifact 6 / nonce 3 / storage 17 / API 15 PASS
  (1.208s / 1.025s / 38.995s / 300.569s). API includes 13 permanent tests and
  two fresh probes. One signature, one explicit SQLite busy zero-result reject,
  zero unexplained results. Actual COMMIT failure (27.70s) and four process-exit
  windows (49.23s) preserve original identity/accounting and no second signature.
- 168 checksums, 11 current source hashes, 13 mapping groups/30 named tests
  and author log attribution PASS. 39 offline model tests/five Curve fixtures
  PASS (model 2.592s), not real-chain evidence.

Two original probe runs failed because reviewer helpers incorrectly required
global zero attempts/reservations in fixtures intentionally containing a B2 claim
and unrelated released legacy reservation. Logs remain sealed. The final probes
check the precise legacy witness and original owner/accounting; no product,
permanent test, schema guard or FK setting was changed or weakened.

B2 valid-COMMIT metadata may return if a nil-returning test callback cancels
after COMMIT. Its frozen contract covers pre-COMMIT cancellation and callback
errors, not atomic cancellation after success. B3 has an additional explicit
post-COMMIT context check. Review accepted this distinction without a finding;
neither false-permission result grants a fresh action or absolute return race
guarantee. This closeout does not change either implementation or contract.

Exact commands and sealed report/results/probe/log hashes are recorded in
`rbh-validation-package/evidence/http-m3-joint-review.json`. Temporary artifacts
are under `/tmp/rbh-m3-cumulative-independent-CKzJsm`, not committed permanent
tests or logs; availability after cleanup/restart is not guaranteed.

## Limits, preservation and next boundary

No independent full-repository ordinary/race, full-M3/repeated race, build/vet/
staticcheck, remote CI or real-chain test is claimed. Author whole-repository
validation and historical stage reviews remain separately attributed. No unrelated
closed gate or legacy implementation is recertified by this scope-limited PASS.
Normal FK-ON/intact reviewed schema and trusted bounded synchronous signer/
cipher/query/clock are assumed; no forced preemption, Go/library secure erasure,
hostile schema rewriting or power-loss/hardware/key-availability proof is claimed.
Positive offline fixtures sign once; legacy fake transports may send locally.
Zero production reachability is not a claim that all test sign/send counts are zero.

This closeout changes only this document, current delivery-plan metadata, additive
joint-review evidence and checksum. Product, tests, 001–036, old evidence and
scope/lifecycle/acceptance docs remain byte-identical to the reviewed head. Their
pending/FAIL/PASS records and source hashes keep their historical snapshot meaning.
No runtime tests are rerun by this docs-only slice; verification checks hashes,
report attribution, historical preservation, manifest and exact diff boundaries.

No HTTP/config/startup signing/query/key composition, production signer, real
RPC/key/grant/mainnet, submission/replay/raw send/broadcaster/permit, new nonce,
rebuild/resign/replacement/fee bump/refund/release, live/release change, other-chain
work or remote GitHub operation occurs. M4/M5/W4-D/W5 are not entered.

Next candidate is separately authorized read-only M4 scope freeze. It must define
any future MANUAL submission boundary before implementation; this closeout does
not authorize that implementation, production composition or real execution.
