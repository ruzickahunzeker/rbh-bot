# MANUAL M2 — reference integrity hardening, local only

Base: `46270e8195eb76758bf978e36bb28d40b4835d06`.
Branch: `http-m2-binding-integrity-hardening`.
The independent review of that base concluded FAIL. This is a narrowly scoped
fix, not M2 closeout, execution permission or M3 implementation.

Historical checkpoint: fresh independent review of the committed 022 fix at
`b47239dd07dbf62c7ac4af7c0acd1c6737e0608e` subsequently concluded **FAIL**.
UPDATE guards protected OLD/NEW references but missed a different unique-key
conflict victim implicitly deleted by UPDATE OR REPLACE with recursive triggers
disabled. The reviewer reproduced reservation operation-key and lane wallet/
address-key victim loss without removing guards. Ordinary tests passing did not
close this residual. Statements below describe the earlier 022 implementation
and author checkpoint, not a current assertion of complete REPLACE protection.
See `HTTP_MANUAL_UPDATE_CONFLICT_HARDENING.md` for the additive 023 repair and
new fresh-review-pending checkpoint. Historical JSON evidence is unchanged.

## Reviewed defects

Migration 021 checked financial reservation and wallet lane writes primarily
by the supplied operation ID. Separate foreign keys proved that a legacy
operation and a MANUAL execution step existed, not that they belonged together.
A legacy operation could therefore INSERT/UPDATE a second financial reservation
or a nonceful lane referring to the bound MANUAL step. Neither removal of
guards nor disabled foreign keys was required. Both survived SQLite reopen.
The positive-only complete view and duplicate verifier accepted that pollution.

The independent reviewer ran M2 ordinary/race, eight selected M1/C3-B/C3-C race
tests and W4-C SQLite reopen race successfully; four isolated rejection probes
failed because illegal writes succeeded. Those green tests did not close the
two production schema defects. Prior implementation evidence is historical and
is retained unchanged, including its full-repository race timeouts.

## Minimal change

Additive migration **022** changes no landed SQL in 001–021. It adds guards
against financial reservations referring to a bound operation or any of its
steps, including its original validation step. Checks cover both old and new
references on UPDATE and conflict victims of INSERT OR REPLACE. Status is not
a loophole: a released financial record is still a prohibited second ledger.

Lane guards consider referenced step ownership, operation, wallet and address.
The sole permitted bound lane is the exact operation/execution-step/wallet/
address, state reserved, NULL nonce and freeze reason, and original checked_at.
Initial claim of an unrelated idle lane via 021's UPSERT remains valid. Mutation
or replacement of existing bound/alias references is rejected. Deletion cannot
silently clear prior pollution. Unrelated legacy lifecycle remains unaffected.

A BEFORE INSERT binding guard denies preexisting legacy references to the
operation or either step; it cannot adopt an already-polluted validation step.
All existing binding/step/lane/audit/alert atomic effects remain owned by 021.

`manual_execution_binding_integrity` wraps the original positive complete view
with negative checks for extra financial and invalid lane references. The
disconnected writer verifies this stricter view on both first binding and
historical duplicate. Missing view/DB errors fail closed. A polluted duplicate
returns zero result and verified `EXECUTION_BINDING_INCOMPLETE`, without repair,
extra audit/usage, refreshed expiry, or execution/send permission.

022 does not rewrite, release or delete persisted pollution on upgrade. It
installs protection and detection; explicit resolution is outside this slice.
SQLite foreign keys alone and privileged SQL assertions of time still are not
authorization. No signing/send boundary becomes reachable.

## Permanent tests and limitations

- `TestHTTPM2HardeningFinancialReferenceGuards`: execution/validation step
  alias INSERT, OR IGNORE, OR REPLACE, UPDATE and UPDATE OR REPLACE; wrong
  operation; unchanged durable snapshot and valid duplicate after real reopen.
- `TestHTTPM2HardeningLaneReferenceGuards`: both step aliases, NULL/non-NULL
  nonce, wrong/null operation, wrong wallet/address, canonical-lane REPLACE;
  unchanged snapshot, real reopen and history-only duplicate.
- `TestHTTPM2Hardening021PollutionUpgradeRejectsWithoutRepair`: four original
  INSERT/UPDATE reproductions against the disposable 021 boundary, followed by
  actual close/reopen and 022 migration. The old positive view remains 1; the
  strict view becomes 0; original binding is unchanged, duplicate and M1 input
  deny, and no cleanup/repair writes occur. UPDATE/DELETE/REPLACE cannot hide
  the retained illegal references.
- `TestHTTPM2HardeningPrebindingValidationReferenceDeniesAtomicClaim`: legacy
  validation-step references deny writer and direct SQL binding with no effects.
- `TestHTTPM2HardeningLegacyCompatibilityAndIdleClaim`: independent legacy
  reservation/lane updates and REPLACE remain valid, initial idle claim and
  duplicate do not change original TTL or budgets.
- `TestHTTPM2HardeningMissingIntegrityViewFailsClosed`: missing integrity schema
  causes operational unavailability and zero result for first bind/duplicate,
  with full transaction rollback and no repair.
- `TestManualBindingReferenceUpgradeFrom021PreservesLegacy`: constructs actual
  embedded schema through 021, seeds inert legacy submitted/frozen metadata,
  closes/reopens and migrates twice; status/nonce/freeze/budget are preserved,
  the 022 journal is exactly once and existing closed guards remain present.

The pollution fixture removes only disposable 022 objects/journal to reproduce
the landed 021 boundary; it does not remove 001–021 guards. The older corruption
test now explicitly drops the additional delete guard only in its disposable
fixture to simulate damage. Neither is permission to mutate production schema.
Legacy test nonce values are inert metadata, not nonce allocation or signing.
Reopen is not OS power-loss proof. Structural/harness zero economic paths are
not production-wide telemetry. No mainnet, remote GitHub or credentials used.

## Historical 022 author checkpoint (superseded by fresh review FAIL)

Current author validation and limitations are recorded separately in
`rbh-validation-package/evidence/http-manual-binding-integrity-hardening.json`.
Full ordinary testing, all 23 M2 tests and both storage upgrades in three
consecutive race runs (tradeapi 477.145s / storage 9.674s), build/vet/staticcheck
and offline reference/Curve validation passed. Packages are run serially to
avoid overlapping heavy race batches. Full-repository race is not rerun or
claimed PASS for this hardening; the original timeout limitation remains.
Eight exact M1/C3-B/C3-C compatibility race tests passed (56.424s), including the
public late cancellation/deadline regression. Selected W4-C SQLite reopen race
also passed (4.646s); neither selection is full-repository race or service health.
The initial M2 evidence is not relabeled PASS. Fresh independent review must
evaluate 022 and the stricter verifier, not merely reuse the former failed review.

```ini
HTTP_C3_B = PASS
HTTP_C3_C = PASS
M1 = PASS
M2 = READY_FOR_REVIEW
M2 previous independent review = FAIL
M2 fresh independent review = NOT_PERFORMED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Forbidden/unchanged: HTTP/config/startup wiring, M3–M5, signer, SubmissionService,
RawBroadcaster, SendRawTransaction, RPC/mainnet, new nonce/attempt/artifact,
rebuild/resign/replacement/fee bump, second financial charge, real grant,
no-arm/no-permit/no-PASS removal, W4-D/W5, other chains and live/release readiness.
