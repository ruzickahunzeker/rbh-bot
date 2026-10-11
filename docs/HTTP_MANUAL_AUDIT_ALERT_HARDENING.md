# MANUAL M2 — audit/alert identity hardening, local only

Branch: `http-m2-audit-alert-hardening`.
Exact base: `3be7a805e693f7973d9224aaf7fd812003a7e911`.
The independent alert/audit static subreview of that base concluded FAIL;
the interrupted broader M2 review remained BLOCKED. That reviewer did not
complete a full-M2 verdict or runtime reproduction. This batch is not closeout.

## Residual and minimal implementation

Alerts have unique id, unique audit_id and implicit rowid. Migration 021 guarded
same-id INSERT replacement and target-row changes, not different conflict
victims. With recursion OFF, a conflict could remove the canonical alert without
running its DELETE guard. Foreign keys point to audit, not alert. Once the audit
is unreferenced, rowid replacement can remove it as well. Reconstructed history
with changed attempt_id/extra JSON could satisfy the old positive view.

Migration **024** leaves landed 001–023 SQL unchanged. Seven new M2-only guards
protect alert INSERT/UPDATE victims over id/audit_id/rowid, full alert identity,
retained alert deletion, audit INSERT victims over id/rowid and exact inserted
audit identity and a final canonical-alert effect check. Retained alternate-ID
alerts and binding-event audit aliases
are protected without repair. Rejection aborts the entire statement, regardless
of recursive trigger settings and regardless of audit child references.

Normal alert state/delivered_at updates and unrelated legacy writes remain
allowed. Exact audit verification checks every original field, attempt_id NULL,
and byte-exact JSON from the original json_object expression. Extra keys,
reordered JSON and whitespace are rejected, not normalized.
Extra stored MANUAL_EXECUTION_BOUND aliases on the same operation also make
history invalid. The last alert insertion verifies exact effects inside the
original statement, so a preexisting alias rejects Go/direct-SQL first binding
atomically without depending on ordering between AFTER-binding triggers.
Alias detection uses NULL-safe identity comparison because SQLite's ordinary
TEXT PRIMARY KEY can retain NULL predecessor IDs; that upgrade case is tested.
These are M2-specific guards, not global re-certification of every shared
audit/alert owner. The static reviewer noted a similar 020 pattern; closed C3
implementation/guards remain unchanged and are not silently broadened here.

024 adds `manual_execution_binding_audit_integrity` and transactionally
recreates the existing `manual_execution_binding_integrity` view to wrap it,
retaining all 022 negative reservation/lane checks. This is a new migration
file, not a claim that no existing view definition changes. The production Go
writer/API remain unchanged and query the same fail-closed view name.

Upgrade never cleans/releases/reconstructs retained rows. Invalid duplicates
return zero-result EXECUTION_BINDING_INCOMPLETE with no repair, renewed TTL or
second charge. Missing exact schema is operational unavailability; first-bind
effects roll back. Migration failure rolls back guards/views/journal together.

## Permanent tests

| Test | Demonstrated boundary |
|---|---|
| TestHTTPM2AuditAlertUniqueConflictGuardsAndRestart | Nine key/rowid cases, FK ON, recursion OFF/ON, unchanged row-inclusive snapshot and historical-only duplicate after reopen |
| TestHTTPM2AuditAlert023NegativeControlAndRetainedUpgrade | Six real predecessor alert mutations retained across 024 upgrade without repair |
| TestHTTPM2AuditExactHistoryUpgradeRejectsWithoutRepair | 023 composed history with changed attempt/extra JSON/whitespace/reordered JSON/audit alias accepted before upgrade, retained but rejected after |
| TestHTTPM2AuditOrphanedHistoryAndRetainedAlertProtected | Audit guard works without child FK; retained alternate-ID alert cannot be deleted/replaced/identity-mutated |
| TestHTTPM2AuditAlertDeliveryAndLegacyCompatibility | PENDING→DELIVERED and unrelated legacy writes work in OFF/ON modes; duplicate consumes nothing |
| TestHTTPM2AuditIdentityFieldsAndAliasesRejected | Alert identity edits and alternate M2 audit aliases reject |
| TestHTTPM2AuditTwoHandleConflictNoConsumption | Eight writes on two handles all reject with the guard, not lock errors; restart preserves history |
| TestHTTPM2AuditAlertMultirowAtomicRollback | Protected conflict rolls back earlier delivery effects in a mixed statement |
| TestHTTPM2AuditNegativeRowidConservativeInsertion | Exact audit at rowid -1 readable; implicit-rowid INSERT conservatively rejects; explicit nonconflicting rowid works |
| TestHTTPM2AuditMissingExactViewFailsClosed | First bind and duplicate fail operationally with no effects if exact schema unavailable |
| TestHTTPM2AuditMigrationRollbackAndRetry | Conflicting schema aborts entire migration; retry/idempotency succeed |
| TestHTTPM2AuditPrebindingAliasRejectsAtomicClaim | Preexisting alias rejects Go/direct-SQL binding with no partial effects, adoption or repair |
| TestManualBindingAuditUpgradeFrom023PreservesLegacy | Embedded 023 predecessor, submitted/frozen legacy nonce metadata, reopen and journal/closed guards preserved |

Predecessor helpers remove only disposable 024 objects/journal and restore the
actual 022 view before removing earlier hardening objects as required. Existing
corruption tests drop the additional deletion guard only in their disposable
fixture. These are not production repair instructions. Test nonce values are
inert metadata, not allocation or real artifacts.

## Limits and review gate

SQLite BEFORE INSERT reports unspecified rowid as -1. A protected explicit -1
audit/alert can conservatively deny an unrelated implicit-rowid INSERT; explicit
nonconflicting rowid works. The audit case is tested, alert shares the same
sentinel constraint. This is not universal legacy compatibility; the previous
lane limitation remains unchanged.

Exact fields cannot retrospectively prove that identical bytes were never
reconstructed before upgrade. There is no cryptographic provenance claim.
024 prevents the documented future victim-loss paths and rejects observably
altered retained payloads. File reopen is not OS power-loss proof. Harness zero
economic paths are not production telemetry. Historical full-race timeouts
remain explicit; focused race is not relabeled full-repository PASS.

Actual commands/results/hashes are in the new additive
`rbh-validation-package/evidence/http-manual-audit-alert-hardening.json`.
Historical evidence remains unchanged.

Final-source author validation: full ordinary `go test -p 1 ./...` passed;
all 42 M2 tests and four binding upgrades passed focused race
(tradeapi 482.100s, storage 7.549s). Three critical audit tests plus the
023-to-024 upgrade passed three repeated race runs (103.475s / 6.233s).
Eight selected M1/C3 compatibility tests passed race (72.269s), and the
selected W4C SQLite reopen race passed (17.401s). Build, vet, staticcheck,
offline model/curve checks and the 142-entry manifest passed. These are author
checks, not independent approval or full-repository race evidence.

```ini
M1 = PASS
M2 = READY_FOR_REVIEW
M2 preceding full independent review = BLOCKED
M2 preceding alert/audit static subreview = FAIL
M2 fresh independent review = NOT_PERFORMED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Next: fresh independent review at the fixed commit, then separate closeout only
if PASS. No author PASS, M3–M5, HTTP/config/startup wiring, signer/submission/
broadcaster/raw send/RPC/mainnet, new nonce/attempt/artifact/permit/grant, second
charge, automatic history repair, rebuild/resign/replacement/fee bump, W4-D/W5,
other chains, GitHub push/PR/merge or live/release changes.
