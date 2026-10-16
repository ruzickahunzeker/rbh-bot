# MANUAL M4-B1 — disconnected durable submission foundation

Branch: `http-m4b1-submission-foundation`.
Base: `4855d982aca882b9ca2b1eccd965efe725ce2679` (local M4-A closeout).
M4 scope: `HTTP_MANUAL_SUBMISSION_SCOPE.md`; B1 refines only its durable metadata
foundation. This is not production submission or authority to open later guards.

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4-A = PASS
M4-B1 = READY_FOR_REVIEW
checkpoint = M4_B1_READY_FOR_REVIEW
independent_M4_B1_review = NOT_PERFORMED
M4-B = INCOMPLETE_NOT_READY_NOT_PASS
M4-B2 = NOT_STARTED
M4-B3 = NOT_STARTED
M4-B4 = NOT_STARTED
M4-C = NOT_STARTED
M4-D = NOT_STARTED
M5 = NOT_STARTED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## Additive 037 model

`manual_submission_foundations` stores only operation_id, exact binding_json,
binding_hash and canonical created_at. Its expected projection comes from the
existing complete signed-prebroadcast phase, original admission/execution/nonce,
MANUAL source, risk reservation/typed unit and operation-usage rows. Full-byte
ownership/collision checks reject TEXT/BLOB legacy dry-run/canary ownership.

The JSON binds original full Facts, facts/envelope/raw/tx hashes, key version,
artifact completion, full original contract/hash, reservation identity/input/gas/
unit and original operation lifetime usage. This includes original request/
principal/wallet/chain/policy/quote hash/nonce/fee/expiry/authorization epoch and
deployment. The fixed source/format/status and all four false action flags are
part of the exact byte projection. Caller changes or a new self-computed hash
cannot substitute another identity, amount, time window or accounting unit.

This is historical structural metadata, not a new execution state machine,
reservation, budget, permit or admission decision. Only the existing
Operation -> ExecutionStep -> TransactionAttempt remains authoritative. No
current-clock/TTL/auth check is claimed: canonical created_at is no earlier than
the original artifact completion, but may be after original expiry. Revocation,
stop and valid frozen accounting do not erase history or grant fresh permission.
No new expiry/nonce/epoch/aggregate-usage window, charge/refund/release follows.

SQL completeness cannot authenticate ciphertext. A permanent negative test
intentionally records a structurally complete tampered envelope and foundation;
unchanged M4-A decrypt/authenticate rejects it. A future action requires both
cryptographic verification and fresh authoritative gates, not this row or hash.
No Go foundation writer/loader or HTTP/config/startup caller is added by B1.

## Witness atomicity and denial domains

Exactly one INFO audit and alert are generated inside the foundation INSERT.
A transient deferred-FK scope allows witness creation only in that statement;
it must be empty on success. Missing/ignored/failed witness effects roll back
foundation/audit/alert/scope together under ordinary, IGNORE and REPLACE. The
scope cannot be adopted, reopened after a retained foundation, or committed as
an orphan. Missing witnesses cannot be recreated as independent SQL writes.

Foundation identity and history are immutable. Guards deny TEXT/BLOB aliases,
rowid/conflict victims, duplicate/replacement, witness namespace preseed and
unrelated alert UPDATE adoption. Existing global audit append-only protection
remains in force. Outbox delivery state/timestamp updates remain allowed while
identity, severity, original creation and referenced audit cannot change.

No old table/view/trigger is dropped or altered. 001–036 remain byte-identical.
Two existing test metadata expectations are synchronized with additive037:
the migration count and latest-schema checkpoint. Their economic dependency
and closed-guard assertions remain intact; no old behavior assertion is removed.
no-arm/no-permit/no-PASS, submission/receipt/position and claimed-risk/usage
release guards remain closed. Existing M4-A and M3 signed history continue to
work; the new row is not a phase exception. Downstream phase completeness and
fresh gate/permit/outcome contracts remain separate B2/B3 work.

## Tests and author evidence

Thirteen new API tests cover three sides and real reopen, 33 changed projection
paths with freshly recomputed hashes across three INSERT modes, malformed and
BLOB metadata/time, marker-only/source collision/unsupported submission denial,
four witness fault modes across three INSERT modes, conflict victims/delivery,
retained missing/BLOB history with exact witness recreation/adoption denied,
namespace preseed/orphan scope, eight contestants on two SQLite handles with
exactly one foundation, expired/revoked/stopped historical metadata, explicit
no-arm/no-permit/no-PASS denial, actual deferred-FK COMMIT failure/rollback and
cryptographic-permission separation. Upgrade-from-036 checks late journal
failure atomicity, idempotence, retained corruption and old guard/view identity.

Reused offline fixtures deliberately seed a synthetic ARMED authorization by
temporarily removing no-arm; the new B1 wrapper restores the exact original 018
trigger before every tested action. These fixtures do not prove an intact
production schema can arm MANUAL or that any real grant exists. Positive setup
signs locally with ephemeral keys. The foundation path has no signer, decryptor,
transport, permit or nonce dependency; no zero-signature claim about all setup.
Corruption seeds restore original guards/FK before tested reads/writes.

Initial 10-test API run PASS17.982s preceded stronger tests and is not final
source evidence. Two later runs failed because new fixture assumptions were
wrong: inherited setup had dropped no-arm; a retained BLOB foundation made a
JOIN-based recreation probe select zero rows. The wrapper restores the exact
guard and the probe now attempts the exact original witness independently of
the corrupt row. No old implementation or assertion was weakened. A new alert
UPDATE-into-namespace guard closes a B1-only self-check finding. Failed logs
are retained and attributed in additive author evidence.

The first full-repository run exposed the old hardcoded latest-schema 036
expectation (actual037). Only that metadata expectation is advanced to037;
its original no-signing/no-runtime/closed-guard checks remain unchanged. The
original failed full run is retained rather than labeled PASS. The checkpoint
regression is rerun, followed by final-source full validation.

Completed local author checks on final B1 source:

- Focused storage2/API13 tests PASS (0.280s / 19.905s); this preceded only the
  old schema-checkpoint metadata sync, not changes to B1 migration/new tests.
- Corrected old checkpoint regression PASS, 0.182s.
- Final `go test -p 1 ./... -count=1 -timeout=20m -json` PASS: storage10.720s,
  trade156.863s, API321.406s, app0.172s. Includes all13 B1 and all9 M4-A tests.
- `go build ./...`, `go vet ./...`, `staticcheck ./...` PASS.
- Offline reference models39 (2.977s) and Curve fixtures5 PASS, not chain proof.
- Selected single race PASS: artifact6/storage4/API17 top-level tests, including
  all13 B1 tests, original guard compatibility and M4-A snapshot/dependency
  checks (1.203s / 8.186s / 498.535s). No whole-repository race is claimed.

Final checks also verify gofmt/whitespace, all old files except the two exact
test-metadata replacements and current plan/manifest, original171 manifest
lines plus one new evidence entry, source/document/log hashes and named test
mappings against final ordinary/race PASS events. Package checker covers172.
Author logs stay temporary; they are not committed or remote-CI evidence.

Final commands/results and hashes belong in
`evidence/http-manual-m4b1-submission-foundation.json`. Independent B1/M4-B
review has NOT been performed. Maximum checkpoint M4_B1_READY_FOR_REVIEW, not
whole-M4-B readiness/PASS or whole-M4 readiness/PASS. No B2/B3/B4/C/D/M5,
production wiring, real key/RPC/grant/mainnet/live or remote operation follows.
