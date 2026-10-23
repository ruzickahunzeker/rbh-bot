# MANUAL M2 — binding INSERT conflict-victim hardening

Local branch: `http-m2-binding-rowid-hardening`.
Exact base: `5010258ab7ad2ef889f376bb280809f2087d809b`.
This is minimal residual implementation, not closeout or M3.

## Preceding review

Full independent review at the base was interrupted by reviewer tooling and
concluded BLOCKED_REVIEW_INCOMPLETE, not PASS or a completed full-M2 FAIL.
Before interruption it reproduced a schema defect: foreign_keys ON and
recursive_triggers OFF allowed another wallet's valid binding INSERT OR REPLACE
to reuse the first binding's rowid. The old binding disappeared; the new strict
view remained valid. Actual reopen left the old duplicate unavailable. The ON
control rejected deletion. No M2 integrity guards were removed; the existing
disposable authorization fixture removes only its no-arm guard, never a real
grant. Reviewer focused race PASS (424.537s / 7.650s) did not close this defect.

## Minimal fix

Additive migration **025** adds one BEFORE INSERT guard to
`manual_execution_bindings`. It checks operation_id, request_id,
validation_step_id, execution_step_id and implicit rowid conflict victims.
RAISE(ABORT) prevents victim loss independently of DELETE-trigger recursion,
rolling back the whole statement. No existing table/view is rebuilt.
Production Go writer, migrations 001–024, lifecycle/accounting owners and
closed permission guards remain unchanged.

Upgrade installs protection only: no lost-binding reconstruction, orphan
cleanup, risk release or renewed TTL. Damaged history stays unavailable; valid
duplicates remain historical-only with both permission flags false. Normal
nonconflicting first bindings retain the existing atomic effects and do not
consume another admission allowance.

## Permanent acceptance

| Test | Direct evidence |
|---|---|
| TestHTTPM2BindingRowidConflictRetainsHistory | Two admitted wallets, FK ON, OFF/ON denial, unchanged row-inclusive/full-table snapshot, ordinary first bind, actual reopen and both historical duplicates |
| TestHTTPM2BindingRowid024NegativeControlAndUpgrade | Real 024 guards reproduce OFF victim loss and ON rejection; 025 upgrade preserves damaged metadata without repair; valid duplicate remains historical-only |
| TestHTTPM2BindingRowidTwoHandleConflictNoConsumption | Eight competing inserts across two SQLite handles deny with guard errors, unchanged snapshot, reopen and later ordinary bind |
| TestHTTPM2BindingRowidMixedStatementRollback | Later conflict rolls back earlier binding/step/lane/audit/alert effects in a multirow statement |
| TestHTTPM2BindingRowidNegativeSentinelExplicitInsert | Protected -1 rowid conservatively denies implicit insert without effects; explicit nonconflicting positive rowid works |
| TestHTTPM2BindingRowidMigrationRollbackAndRetry | Conflicting trigger name aborts migration without journal/data effect; retry twice preserves history |
| TestManualBindingRowidUpgradeFrom024PreservesLegacy | Actual embedded 024 predecessor, legacy submitted/frozen metadata, journal and closed guard preservation |

Older disposable predecessor helpers first remove only 025's trigger/journal,
then their previously documented objects. Negative controls do not disable
foreign keys or remove 021–024 integrity guards. Fixture authorization and
nonce metadata are inert test setup, not real grants or nonce allocation.

## Limits and review gate

SQLite's unspecified NEW.rowid is -1. A binding explicitly stored at -1 may
conservatively deny another implicit INSERT. That fail-closed availability
limitation is tested, not universal compatibility. Explicit nonconflicting
positive rowid works in the harness; no new production API/repair is added.

This fixes binding INSERT victims only, not all shared audit owners/closed C3
or every execution_steps rowid/FK path. Uncompleted reviewer investigations
are not relabeled closed. Existing history/provenance and crash-vs-power-loss
limitations remain. Zero economic paths are structural/local evidence, not
production telemetry or recovery health.

Final-source commands/hashes/results are in additive
`rbh-validation-package/evidence/http-manual-binding-rowid-hardening.json`.
Historical evidence including full-repository race timeouts is unchanged.
Focused race is not full-repository PASS or independent approval.

Author validation: final full ordinary Go tests PASS (trade 144.857s,
tradeapi 122.847s, storage 9.369s); all 48 M2 tests plus five upgrades final
focused race PASS (638.830s / 9.338s). Six new tests and the new upgrade
passed three-run race (190.104s / 11.598s) before the old test-only journal
assertion fix; their sources/helpers were unchanged and all were rerun in
final M2 race. Eight selected M1/C3 race tests PASS (127.630s); selected W4C
reopen race PASS (18.312s). Build/vet/staticcheck and offline 39-model/5-event
checks PASS; manifest has 143 entries. The initial socket/test-journal failures
and interrupted pre-final runs remain recorded, not relabeled PASS.

```ini
M1 = PASS
M2 = READY_FOR_REVIEW
M2 preceding independent review = BLOCKED_REVIEW_INCOMPLETE
M2 fresh independent review = NOT_PERFORMED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Stop at the local commit for fresh independent review. No author PASS/closeout,
GitHub operations, HTTP/config/startup wiring, signer/submission/broadcaster/
chain RPC/mainnet, real grants, new nonce/attempt/artifact/permit, extra charge,
rebuild/resign/replacement/fee bump, automatic repair, M3–M5, W4-D/W5 or other
chains.
