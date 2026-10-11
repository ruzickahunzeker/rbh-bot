# MANUAL M2 — unique conflict-victim hardening, local only

Branch: `http-m2-update-replace-hardening`.
Exact implementation base: `b47239dd07dbf62c7ac4af7c0acd1c6737e0608e`.
Fresh independent review of that base concluded FAIL. This batch repairs only
the residual metadata integrity boundary; it is not M2 closeout or M3.

Subsequent checkpoint: the full independent review at committed head
`3be7a805e693f7973d9224aaf7fd812003a7e911` was interrupted and remained BLOCKED.
A separate read-only alert/audit static subreview concluded FAIL: alert
audit_id/rowid conflict victims and composed audit replacement were not covered.
That reviewer did not execute reproductions or conclude a full-M2 verdict.
See `HTTP_MANUAL_AUDIT_ALERT_HARDENING.md` for the 024 repair and new pending
fresh-review checkpoint. This document and its JSON are historical 023 evidence.

## Residual and repair boundary

With `recursive_triggers=OFF`, SQLite UPDATE OR REPLACE can implicitly delete a
different row that conflicts with the target's new unique keys. Checking only
OLD/NEW target references and protecting ordinary DELETE is insufficient. A
clean legacy target could delete retained bound-step pollution, turn the strict
integrity view from 0 to 1 and make a historical duplicate appear complete.

Additive migration **023** checks other conflicting rows before UPDATE:

- Financial reservation: operation_id, step_id and implicit rowid.
- Wallet lane: wallet_id, the exact/BINARY unique address and implicit rowid.
- Relevance comes from the bound operation/owned steps, plus bound wallet/
  normalized address for lanes; the updated target row is excluded.

Explicit rowid INSERT OR REPLACE has the same victim-loss mechanism, so two
additional BEFORE INSERT guards cover declared and implicit unique keys. Four
new guards reject with RAISE(ABORT), independently of recursive trigger mode,
and roll back the entire statement. The existing strict verifier is unchanged.

The narrow existing idle-lane UPSERT remains valid only for the exact binding
identity, reserved state, NULL nonce/freeze and original checked_at. Bound or
contaminated non-idle lanes cannot use this exception. Unrelated legacy
self-updates and ordinary unrelated conflict replacements remain valid.

SQLite BEFORE INSERT reports an unspecified NEW.rowid as -1. If a protected
lane has explicitly stored rowid -1, an unrelated insertion without an explicit
rowid may be conservatively rejected. The permanent test proves exact idle
claim still succeeds and explicit nonconflicting positive-rowid insertion works.
This is a documented fail-closed compatibility limitation, not an assertion
that every unusual legacy rowid insertion remains available.

Migrations 001–022, the production Go writer, closed no-arm/no-permit/no-PASS
guards and previous evidence are unchanged. Upgrade installs guards but never
deletes, releases, rewrites or auto-repairs retained pollution. A contaminated
historical duplicate remains zero-result EXECUTION_BINDING_INCOMPLETE; M1
candidate loading rejects it. No new TTL or second budget consumption occurs.

## Permanent evidence matrix

| Test | Directly demonstrated |
|---|---|
| TestHTTPM2ResidualUpdateConflictVictimsRetainedAcrossRestart | Seven operation/wallet/address/UPDATE-rowid/INSERT-rowid variants reject with recursion OFF/ON, unchanged full snapshot and close/reopen; duplicate and M1 remain denied |
| TestHTTPM2Residual022NegativeControlAnd023Upgrade | All seven negative controls succeed on disposable 022 and hide pollution; rollback restores it; real reopen/023 upgrade preserves it and rejects the same writes |
| TestHTTPM2ResidualCanonicalLaneConflictVictim | Exact bound lane survives wallet/address/rowid conflicting UPDATE and explicit rowid INSERT |
| TestHTTPM2ResidualLegacySelfUpdateAndReplaceCompatibility | Exact idle UPSERT, unrelated self-updates and unrelated legacy REPLACE remain valid; duplicate is historical-only and consumes nothing |
| TestHTTPM2ResidualSecondHandleConflictRejectsWithoutConsumption | Eight competing writes across two independent SQLite handles all fail with the guard error, not merely lock errors; snapshots/budgets remain unchanged after reopen |
| TestHTTPM2ResidualMultirowUpdateAbortsAllEffects | A mixed multirow UPDATE aborts atomically, including earlier unrelated effects |
| TestHTTPM2ResidualIdleLaneWithExplicitNegativeRowidClaim | Idle claim at rowid -1 works; ambiguous unspecified rowid fails closed; explicit unrelated positive rowid works |
| TestManualBindingReferenceUpgradeFrom022PreservesLegacy | Actual embedded predecessor schema, legacy submitted/frozen metadata, real close/reopen, idempotent migration journal and retained closed guards |

The 021 upgrade fixture now removes disposable 023 before disposable 022.
Negative controls never remove real predecessor guards, disable foreign keys
or become production repair instructions. Fixture nonce values are inert
metadata, not nonce allocation. File close/reopen is not OS power-loss proof.
Harness/schema zero-path evidence is not production-wide telemetry.

## Validation and handoff

Actual commands, final-source results/hashes and limitations are recorded in
`rbh-validation-package/evidence/http-manual-update-conflict-hardening.json`.
Full ordinary tests passed (trade 111.551s / tradeapi 74.041s / storage 8.748s).
All 30 M2 tests and three upgrades passed a single focused race run (tradeapi
376.341s / storage 7.576s); that run started before a test-only unused assignment
cleanup. Final-source seven residual tests and the 022 upgrade passed three
consecutive race runs (tradeapi 235.317s / storage 6.044s). Final-source focused
ordinary, build/vet/staticcheck and offline 39-model/5-Curve-event checks passed.
Eight selected M1/C3-B/C3-C compatibility race tests passed (57.453s), including
the public late cancellation/deadline regression. Selected W4-C SQLite reopen
race passed (7.266s). The additive validation package has 141 checksum entries;
five source hashes and nine test mappings were checked. Previous evidence is
unchanged. These selections are not full-repository race or recovery health.
Initial full ordinary validation exposed a test-helper int/int64 compile error;
it was corrected and final-source tests rerun. An initial broader lane guard
blocked valid idle UPSERT; the narrow identity-checked exception and regression
tests correct that overreach without weakening bound-lane protection.

Full-repository race is not claimed PASS. The original M2 full-race timeouts
remain in historical evidence; focused race is not a substitute for independent
review. No GitHub fetch/push/PR/merge or real-chain network activity occurred.

```ini
M1 = PASS
M2 = READY_FOR_REVIEW
M2 latest independent review = FAIL_AT_B47239DD
M2 residual fresh independent review = NOT_PERFORMED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Next: fresh independent review at the new fixed commit, then separate local
closeout only if review PASS. Stop here; no author PASS and no M3–M5, HTTP/config/
startup wiring, signer/SubmissionService/broadcaster/SendRawTransaction/RPC,
new nonce/attempt/artifact/permit/grant, second financial ledger, rebuild/resign/
replacement/fee bump, W4-D/W5, other chains or live/release changes.

Fresh review must rerun the three original conflict-victim reproductions on
the fixed schema with foreign keys ON and recursive triggers OFF, inspect the
additional rowid variants and idle exception, and check that no predecessor
migration/production writer/closed gate changed. It must explicitly assess the
documented rowid -1 fail-closed availability limitation and outstanding full-
repository race limitation rather than infer acceptance from author test PASS.
