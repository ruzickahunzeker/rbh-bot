# MANUAL M4-B2.1 formal closeout — disconnected DB evidence only

Reviewed implementation: `eec705473517b591d5502f556685ee2ea9318f58`.
Independent review receipt: `1b5d30e5a54848f2aae11d6dc2c418e16cc54914`.
Closeout branch: `http-m4b21-gate-closeout`, created from the receipt with
ahead/behind 0/0 and a clean worktree. Local-only, not remote main or a PR.
The user's continuation after the independent PASS is limited here to this
docs/evidence/checksum closeout, not B2.2 or new runtime capabilities.

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4-A = PASS
M4-B1 = PASS
M4-B2.1 independent review = PASS
M4-B2.1 = PASS
checkpoint = M4_B21_PASS
M4-B2.1 formal closeout = COMPLETE_LOCAL_ONLY
M4-B2 = INCOMPLETE_NOT_READY_NOT_PASS
M4-B = INCOMPLETE_NOT_READY_NOT_PASS
M4 = INCOMPLETE_NOT_PASS
M4-B2.2 = NOT_STARTED
M4-B2.3 = NOT_STARTED
M4-B3 = NOT_STARTED
M4-B4 = NOT_STARTED
M4-C = NOT_STARTED
M4-D = NOT_STARTED
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

## Exactly what is closed

Only additive038's disconnected immutable DB-input observation contract:
original complete MANUAL foundation/Facts/contract/artifact metadata plus exact
durable controls, authorization identity/epoch/deployment, policy/allowlists,
original reserved accounting/conserved usage and independent SUBMISSION lease.
Stage FIRST_BROADCAST_DB_EVIDENCE, status DB_EVIDENCE_NOT_PERMISSION. There is
no writer/loader, public endpoint, runtime wiring, permission or new action.

All execution/signing/send/replay and trusted-clock/running-deployment/RPC-
runtime/cryptographic-artifact proof flags remain false. checked_at is canonical
absolute relational metadata: source timestamps <= checked_at < original TTL,
auth expiry and lease expiry; equality rejects. It is not trustworthy current
wall time and SQL can backdate inside those bounds. Observing a lease row is
not proof of this process's ownership; a DB build/hash is not running-binary
or RPC runtime verification. Historical evidence is never a fresh gate/permit.

Nothing changes original economic identity, expiry, nonce, usage or accounting.
No no-arm/no-permit/no-PASS, submission/receipt/effect/release guard is opened.
No legacy admission source is manufactured. Scope stays Robinhood4663 / Pons
v2 Curve / native quote, BUY/fixed SELL/already-resolved percentage SELL,
APPLICATION_TTL_ONLY and contract_deadline=false.

## Blocker resolution and independent approval

The original head `e23be87b012b0b1ed391d93b5c7e7363b098d527` was independently
BLOCKED (P2). Its witness FAIL could retain parent+scope, then later exact SQL
could complete audit/alert, close scope and COMMIT. Passing original tests did
not close that issue. Original report/evidence and failure history remain
unchanged; this closeout does not relabel that head PASS.

The reviewed correction stages scope and audit/alert BEFORE parent insertion.
Witness FAIL retains no parent. Later SQL may complete provisional witnesses,
but cannot adopt the existing scope into a parent, cannot close it without the
parent and cannot COMMIT under FK_ON. Rollback is required. A successful fresh
INSERT after rollback can produce evidence; exact parent/foundation/witnesses
are checked before scope close and original full completeness after it. This
preserves the accepted same-successful-INSERT guarantee for committed evidence,
not a silently weakened same-transaction completion claim.

Separately authorized reviewer `m4b21_independent_review` approved the exact
reviewed head. It had author context and the initial finding history; this was
not blind review. The verbatim permanent report is `HTTP_MANUAL_M4_B21_REVIEW.md`,
SHA256 `cc51b739b1bc772f465fc537e3566a5b5957de95402bc7b6f352f3119b66c4d7`.

- Independent focused ordinary storage3/API11 PASS0.555s/63.898s, including all
  16 audit/alert x four conflict-mode x with/without control-drift fault cases.
- Two fresh adversarial overlay groups race PASS52.348s: exact scope/witness
  preseed cannot be adopted via UPSERT or committed, and a second-row witness
  FAIL prevents partial multirow COMMIT. These are reviewer probes, not new
  permanent tests or all11/full-repository independent race.
- Independent integrity checks PASS: reviewed175 manifest entries, source4,
  author log10, mapped tests12, exact6-path hardening diff and old-file retention.
  Receipt preserved the report verbatim and added entry176 with no code change.

Author full ordinary/build/vet/staticcheck/offline PASS is separately attributed,
not an independent full-repository rerun. Author combined all11 race TIMEOUT20m
(exit1) remains NOT PASS: storage3/original10 top-level PASS, new test started10
subcases but emitted no new PASS events before timeout. Separate full new16-case
race PASS340.040s. No all11-single-successful-run or full-repository race, remote
CI, mainnet or hardware power-loss proof is inferred.

## Accepted limits are not changed by closeout

The guarantee is for intact FK_ON schema and the tested witness fault modes,
not arbitrary hostile root AFTER triggers, guard removal, FK_OFF or corrupted
schema. Explicit transaction errors still require rollback; provisional rows
or table presence are not complete evidence. Earlier037 accepted limitations
remain unchanged. Synthetic ARMED/offline signatures are not intact-production
arming or real grants. No existing experimental038 database upgrade/repair is
claimed; no user runtime database is opened by this slice.

The permanent report and additive evidence retain exact commands, hashes,
attribution and qualifications. Temporary raw logs/probes in /tmp are not shipped
runtime data or permanent regressions and can later disappear. This docs-only
closeout verifies their currently available hashes and reviewed snapshots; it
does not rerun runtime suites or invent new test results.

## Preservation and next boundary

Only this document, current delivery plan, additive joint-review evidence and
SHA256SUMS change. All code/tests/schema/migrations001–038, independent report,
prior evidence and implementation docs remain byte-identical to the receipt
and reviewed implementation where applicable. All176 prior manifest lines are
preserved plus one additive closeout entry177. Old READY/NOT_PERFORMED/BLOCKED
and source/document hashes retain their historical snapshot meanings.

B21 PASS is only this DB-evidence subitem. Whole B2/B/M4 remain incomplete.
Next candidate is separately scoped/authorized B2.2 design, not immediate
implementation or a permission exception. No signing/decrypting, PASS snapshot,
permit, submission/replay/send, nonce allocation, rebuild/resign/replacement/
fee bump, refund/release/new budget/expiry, production or HTTP wiring, real key/
RPC/authorization/mainnet/live/release/remote operation or later slice follows.
