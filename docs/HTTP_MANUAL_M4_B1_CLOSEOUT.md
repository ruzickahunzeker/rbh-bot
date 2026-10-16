# MANUAL M4-B1 formal closeout — disconnected structural foundation only

Reviewed implementation: `6fa755be25047868e51daddd66672943a91e478b`.
Implementation base: `4855d982aca882b9ca2b1eccd965efe725ce2679`.
Closeout branch: `http-m4b1-submission-closeout`, created from the exact reviewed
head with ahead/behind 0/0 and a clean worktree. Local-only, not remote main.

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4-A = PASS
M4-B1 independent review = PASS
M4-B1 = PASS
M4-B1 formal closeout = COMPLETE_LOCAL_ONLY
M4-B = INCOMPLETE_NOT_READY_NOT_PASS
M4 = INCOMPLETE_NOT_PASS
M4-B2 = NOT_STARTED
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

## Accepted scope

Only additive037's immutable structural history is closed: exact original
MANUAL ownership, signed-prebroadcast Facts/contract/artifact hashes, typed risk
reservation and original operation usage, with one audit and alert created by
the same INSERT statement. It is not another execution state machine, budget,
reservation, current gate, permit or cryptographic artifact authentication.
All execution/signing/send/replay flags remain false. Historical metadata may
exist after stop/revoke/expiry/freeze without changing original lifetime usage,
expiry, nonce, authorization epoch or ownership. M4-A authentication remains
required; structurally complete tampered ciphertext is not valid signed bytes.

No old schema object is replaced by037. Original001–036, product Go and prior
evidence are preserved. Only two prior test metadata expectations advanced for
037; no economic behavior assertion was removed. no-arm/no-permit/no-PASS,
submission/receipt/effect/lane/risk/usage release guards remain closed. There is
no B1 runtime writer/loader or HTTP/config/startup/transport caller.

Scope remains Robinhood4663 / Pons v2 Curve / native quote, BUY/fixed SELL/
already-resolved percentage SELL. APPLICATION_TTL_ONLY; contract_deadline=false.

## Fresh independent review

Separately authorized reviewer `http_m4b1_independent_review` reviewed the exact
clean head. It inherited author context/handoff and parent check directions;
this is not a blind review. No repo writes or independent failures occurred.

- Focused ordinary storage2/API13 top-level tests PASS, 0.287s/21.024s.
- Selected single race artifact6/storage4/API17 PASS, 1.213s/8.083s/498.878s;
  includes all13 B1 tests, upgrade and selected existing compatibility guards.
- Four reviewer-only overlay groups PASS, 4.629s: created_at minus1ns/equality/
  plus1ns, OR FAIL input, multirow duplicate rollback, UPSERT rejection, retained
  BLOB alert rejection and the two explicitly classified observations below.
- Manifest172, prior171 lines, nine source/document hashes, 14 mappings and
  eight author log hashes verified; all14 mapped author final ordinary/race
  PASS events verified. Author historical FAIL records remain separately
  attributed; author whole-repository validation is not an independent rerun.

Exact commands/results and six temporary artifact hashes are in additive
`evidence/http-m4b1-joint-review.json`. Independent report SHA256:
`426dbdef5bc7fce41944f160e32e547b06905b356de172ee6bcc638f4efbbab3`.
Temporary logs/report/probes under `/tmp/rbh-m4b1-independent-Sxdlqk` are not
permanent regression tests or shipped runtime data; cleanup can remove them.

## Explicitly accepted limits, not stronger guarantees

1. 037 preclose compares audit.details_json by full bytes without an additional
   typeof predicate. If guards are deliberately removed/restored to seed the
   same JSON bytes as BLOB, complete=1. Initial audit insertion requires TEXT
   and intact audit UPDATE is rejected. Review accepts this byte-equivalent
   historical payload after out-of-model corruption; it is not an intact-schema
   alias/repair/ownership or permission bypass. Do not claim every retained
   SQL-storage-class corruption is rejected.
2. A newly installed arbitrary witness trigger using RAISE(FAIL) can leave
   foundation=1, transient scope=1, complete=0 after the statement error for
   audit or alert faults. This is not a successful rollback test. Existing
   reviewed triggers use ABORT, not that injected FAIL. Reviewed ABORT/IGNORE,
   input OR FAIL/multirow, and deferred COMMIT rollback tests pass. No intact
   schema path to the arbitrary FAIL was found. Atomicity claims are restricted
   to the reviewed schema/fault modes, not hostile/future trigger rewrites.
   A future writer must roll back transaction errors and reject incomplete
   retained rows; foundation-table presence is never permission.

These observations were not hidden or fixed by closeout. Review PASS applies
only within the intact reviewed FK-ON schema, not hostile schema rewriting.
No independent full-repository ordinary/race, build/vet/staticcheck, remote CI,
real chain or power-loss proof is claimed. Synthetic offline setup temporarily
removes no-arm to seed an ARMED fixture; B1 restores its exact original018 SQL
before tested actions. This is not intact-production arming or a real grant.
Offline fixture signatures are not zero; zero economic capability describes
the foundation, not every setup or legacy test.

## Preservation and next boundary

Closeout modifies only this document, current delivery plan, additive evidence
and checksum. All product/test/schema/migration/old evidence/scope/implementation
documents remain byte-identical to reviewed HEAD. Old READY/NOT_PERFORMED and
hashes keep their historical meaning; the old delivery-plan hash refers to the
reviewed snapshot. This docs-only slice verifies report attribution, log/source
hashes, preservation, manifest173 and exact diff; it does not rerun runtime tests.

No production signer/submission/replay/raw send/broadcaster/mainnet, grant,
live/release change, new nonce/expiry/budget, rebuild/resign/replacement/fee bump,
refund/release, remote operation or later slice follows. Next candidate is
separately authorized M4-B2 detailed gate/permit contract design. B1 PASS does
not open no-arm/no-permit/no-PASS or authorize B2 implementation.
