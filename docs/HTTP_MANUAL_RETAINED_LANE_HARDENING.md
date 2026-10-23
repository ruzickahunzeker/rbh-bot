# MANUAL M2 — retained wallet/address lane hardening

Local branch: `http-m2-retained-lane-hardening`.
Exact base: `c59e13e342dfe1fb2d9ad986ac817629aee7ef05`.
Minimal M2 residual only; no M3 or permission closeout.

## Review and reachable negative control

The complete independent M2 review at the base concluded **FAIL**. Its sole
remaining blocker was a static retained-lane finding, not reviewer runtime
reproduction. 022 DELETE/OLD-update relevance omitted wallet and normalized
address even though the strict integrity view considers both relevant.

The permanent author tests now reproduce the reachable sequence: seed a legacy
case-variant address lane on another wallet **before** first binding on actual
021; first binding succeeds under its positive-only view; upgrade retains the
lane and rejects history under the strict view. With only 026 removed, ordinary
DELETE or address retarget in 025 hides that pollution. Both recursive-trigger
settings accept the mutation, and committed negative controls demonstrate the
production binding writer returning a historical duplicate after actual reopen.
That result is still not execution/send permission or a proven economic action.
The withdrawn post-binding alias insertion sequence is not used.

## Minimal repair

Additive **026** supplies BEFORE DELETE and BEFORE UPDATE guards using the same
OLD relevance as the strict view: operation, validation/execution step, owned
step, wallet and normalized address. The sole update exception is the existing
exact idle-to-bound claim: matching bound wallet/address, OLD empty operation,
step, nonce and freeze, NEW exact reserved binding identity, no nonce/freeze,
and matching checked_at. An unrelated legacy lane remains mutable/deletable.

No migration 001–025, production writer, view, accounting owner, TTL semantics,
HTTP/config/startup wiring or closed C3 implementation changes. Upgrade does
not repair history or recover evidence of a mutation already committed under
025. Retained polluted bindings remain unavailable; valid historical duplicates
keep expiry/budget unchanged and both permission flags false.

## Permanent evidence

| Test | Direct evidence |
|---|---|
| TestHTTPM2RetainedLane025NegativeControl | Actual 021 pre-binding alias, unchanged upgrade history; 025 DELETE/UPDATE/UPDATE OR REPLACE hide pollution inside rolled-back transactions, OFF/ON and FK ON |
| TestHTTPM2RetainedLane025CommittedRepairNegativeControl | 025 committed DELETE/retarget makes the actual writer accept historical duplicates; file reopen retains this negative-control history |
| TestHTTPM2RetainedLaneMutationDenialAndReopen | 026 rejects DELETE, retarget, UPDATE OR REPLACE and no-op; full snapshot and row-inclusive history unchanged; file reopen and zero-result duplicate rejection |
| TestHTTPM2RetainedLaneTwoHandleDenial | Twelve competing delete/retarget attempts on two SQLite handles, OFF/ON, verified guard denials, no consumption and invalid duplicate after reopen |
| TestHTTPM2RetainedLaneMixedStatementRollback | Unrelated row before retained alias in rowid order; rejected multirow UPDATE/DELETE rolls back the statement |
| TestHTTPM2RetainedLaneIdleClaimAndLegacyCompatibility | Exact idle UPSERT remains accepted; unrelated legacy update/delete and valid historical duplicate remain usable, OFF/ON |
| TestHTTPM2RetainedLaneMigrationRollbackAndRetry | Failure at second new trigger rolls back first trigger, journal and data; retry twice installs protection without repair |
| TestManualBindingRetainedLaneUpgradeFrom025PreservesLegacy | Actual embedded 025, legacy submitted/frozen metadata, additive journals and closed guards retained |

Disposable predecessor helpers remove newer migration objects in descending
order; within each negative-control boundary all landed integrity guards and
foreign keys remain present. Only the existing fake local authorization fixture
removes no-arm; no real authorization is granted. Seeded legacy reservation and
nonce metadata are inert history, not allocation. Duplicate tests assert no
target financial reservation, no attempts/submissions/permits/gate snapshots,
full-table snapshot preservation and M1 refusal to read bound history.

The old intentional-corruption fixture explicitly removes the additional delete
guard only to test missing-history rejection. It is not the reachable negative
control. Source freeze and final author validation are recorded in additive
`evidence/http-manual-retained-lane-hardening.json`; prior evidence/checksums are
historical and unchanged. Focused race does not imply full-repository race PASS.

Final author validation: full ordinary Go suite PASS (trade 123.900s,
tradeapi 128.462s, storage 11.695s); all 55 M2 tests and six binding upgrades
focused race PASS (519.101s / 11.361s). Seven new tests and the new upgrade
passed three-run race (269.350s / 8.625s); they were unchanged across the old
test-only corruption-fixture correction and rerun in final cumulative race.
W4C reopen compatibility race PASS (11.845s); full build/vet/staticcheck,
offline 39-model/5-event checks, six source hashes, nine test mappings and
144-entry manifest PASS. Initial test failures and the interrupted earlier
full ordinary run remain explicit in evidence, not relabeled PASS. Existing
025 rowid=-1 fail-closed availability limits still apply. M1/C3 tests ran in
the full ordinary suite; no separate M1/C3 race recertification is claimed.

## Boundary

File close/reopen, SQLite statement rollback and migration rollback are tested;
this is not OS crash/power-loss proof. No production telemetry, recovery health
or complete recertification of other owners is claimed. Source-level disconnected
writer guards and empty local economic tables do not prove real live execution.

```ini
M1 = PASS
M2 = READY_FOR_REVIEW
M2 latest completed independent review = FAIL (base c59e13e)
M2 fresh post-026 independent review = PENDING
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

No author PASS/closeout, GitHub operations, signer, SubmissionService,
RawBroadcaster, SendRawTransaction, chain RPC/mainnet, new nonce/artifact/attempt/
permit, extra admission charge, rebuild/resign/replacement/fee bump, automatic
history repair, M3–M5, W4-D/W5 or other-chain work.
