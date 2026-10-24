# MANUAL M4-B2.4 consume hardening

Base: `295a779932a9c66535927796c62a6a60e457e6ed` (PR #37 merge).
Branch: `m4b24-consume-hardening-043`.
Previous hardening head: `86583f07069ab6218cf961b4c4d6dd80bcb17973`.

```ini
M4-B2.3 = PASS
M4-B2.4 = HARDENING_IN_VALIDATION
independent_review = PENDING_FRESH_REVIEW
formal_closeout = NOT_STARTED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
consume = PRIVATE_CONTRACT_ONLY
send = disabled
replay = disabled
live = false
release_ready = false
```

The scope remains a private, durable metadata transition for one already-issued
MANUAL FIRST_BROADCAST permit. It has no production caller or raw-byte handoff.
It does not create a submission, send intent, receipt, position effect, nonce,
signed artifact, financial reservation or new authorization.

## Corrections

The original migration 042 left migration 040's consumption deny installed,
and the initial consumer queried the legacy C07 admission decision instead of
the MANUAL admission/binding/accounting source. The existing hardening branch
corrected both through additive 043 and a MANUAL source query. Migration files
001–042 are retained unchanged.

This continuation reproduced a further gap: disabling the wallet after permit
issuance still allowed consumption at head 86583f0. The consumer now checks
the complete current MANUAL signed-phase source in the same SQLite transaction
as consumption. The shared 043 source view binds the original issuance and
artifact, wallet/allowlists, policy/gate evidence, typed reservation/usage,
authorization, SUBMISSION owner/epoch and durable clock floor. Absolute permit,
operation, authorization and lease expiry use parsed time; equality is expired.
Stop is checked before permit identity and DISABLED mode.

Direct SQL must open an exact transient consume scope. Both scope opening and
the final permit update check the current source. The final check executes in
a nested INSERT trigger because a historical artifact view's `old` audit alias
collides with SQLite's OLD pseudo-row in an UPDATE trigger. A transient check
table provides the INSERT context without changing any historical view or
migration. Retained scopes/checks reference an empty deferred-FK target and
cannot commit; successful checks close within their statement.

The consume audit and alert are written before the scope is closed. Scope
closure requires the exact consumed permit and complete audit/alert identities.
API failures roll back all metadata. A direct SQL RAISE(FAIL) witness failure
cannot commit a partial consume while its deferred scope remains open.

## Acceptance mapping

The B2.4 evidence JSON maps each claim to named tests. Coverage includes:

- BUY, fixed SELL and already-resolved percentage SELL metadata consumption;
- one durable consume/audit, duplicate rejection and file close/reopen;
- 16 consumers using two independent SQLite handles, exactly one winner;
- identity/chain/policy/deployment mismatch, disabled wallet, stop/revoke,
  frozen reservation, expiry and current SUBMISSION owner/epoch denial;
- retained-corruption rejection for artifact, gate, policy, allowlist and
  released/consumed accounting; every original guard is restored before use;
- after-recheck control/lease COMMIT barriers: the stale reader cannot become
  a successful writer and the original permit stays ISSUED;
- audit/alert ABORT, FAIL and IGNORE injection, plus three API fault windows;
- direct SQL no-scope/orphan/expiry/control-change/timestamp rejection;
- atomic 042→043 upgrade rollback on schema conflict and idempotent retry;
- unchanged economic snapshots, replay guard and static no-production-caller /
  no-signer/no-transport dependency proof.

Fixtures use synthetic local authorization and artifacts; the existing fixture
temporarily bypasses no-arm only to seed that state, then restores the exact
original guard before the action under test. This is no production authorization
grant. Corruption cases are explicitly fault injection, not legal transitions
through an intact schema. Fault hooks simulate transaction failures, not an
OS-process-kill/power-loss experiment. Production dependency exclusion is a
structural proof; fixture setup signing is not counted as production activity.

## Validation and handoff

Author validation is complete at `M4_B24_READY_FOR_REVIEW`. Results, exact
commands and source hashes are recorded in
`evidence/http-manual-m4b24-consume-hardening.json`.
CI now runs a separate B2.4 race step in addition to the unchanged B2.3/upgrade
step. The first combined 15-minute local race run timed out while preparing an
identity-test fixture; it is retained as TIMEOUT, not PASS. Identity-only
rejections now reuse one fixture and assert an unchanged complete DB after each
case; state-mutating cases remain isolated. No case or assertion was removed.
Local tests are not remote CI or independent approval. Highest possible handoff
is `M4_B24_READY_FOR_REVIEW`; M4-B/M4 remain incomplete. M4-C/D/M5 and production
wiring are not entered. Separate independent review precedes formal closeout.
