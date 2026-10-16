# M3-B3-B2 — exact encrypted artifact phase, local only

Branch: `http-m3b3-controlled-local-signing`.
Batch base: `0a8978f7f533e59f1fd69555aab5ea8dcfdc92b6`, the local foundation
review checkpoint. The foundation reviewed at `ff073f` passed only B3-A/B1.
It is not independent approval of this new phase contract.

Current residual checkpoint: independent phase review of `9e089dabc6f626981a0529b78d8eca885eea56e0`
concluded **FAIL**, limited to a P2 late-cancellation zero-result defect in the
SELECT-only history helper. Cancellation during successful authenticated
decryption returned nonzero metadata; no raw/ciphertext exposure or permission
bypass was observed, and no SQL integrity finding remained. The local minimal
repair checks context immediately before and after crypto, adds permanent
first-key/decrypt/final-key cancellation tests, and leaves 001–036, guards and
all economic paths unchanged. Fresh scoped review is pending. Original phase
evidence and its source hashes below remain historical at the original head;
the additive cancellation-hardening evidence records this review and repair.

```ini
M3-B3 = IN_PROGRESS_NOT_READY_FOR_REVIEW_NOT_PASS
M3-B3-B2 = CANCELLATION_REPAIR_PENDING_FRESH_INDEPENDENT_PHASE_REVIEW
M3-B3-C = NOT_IMPLEMENTED
M3-B3-D = NOT_COMPLETE
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## Structural contract, not signing orchestration

Additive 036 preserves 001–035. One immutable
`manual_signed_artifact_commands` row binds the original operation, original
marker owner, canonical Facts/hash, canonical encrypted envelope, same-nonce
observation/hash and checked time. It is coordination metadata on the ONE
existing attempt, not another transaction state machine, financial owner or
authorization-budget ledger. No production function inserts this command.
Only controlled disposable SQL fixtures exercise it in this batch.

One successful INSERT atomically performs:

1. original prepared attempt → signed, adding only exact key/cipher/nonce/hash
   metadata and the completion timestamp;
2. original nonce_reserved step → signed, with only its updated timestamp;
3. original exclusive reserved lane → signed, with only its updated timestamp;
4. exact `MANUAL_ARTIFACT_STORED` audit and INFO alert;
5. closure of its transient effect scope, followed by full completeness checks.

All quote/calldata/fee/zero-tip/nonce/policy/expiry/authorization/deployment facts,
original operation/validation step, risk reservation and consumed lifetime usage
remain unchanged. No new nonce, expiry, epoch, percentage resolution or recharge.
Source gates additionally require the original complete claim/marker, reserved
risk/RESERVED usage, active bound controls/authorization, exact source identities,
allowlists, durable gates and fresh same-nonce controlled query evidence.
SQL checked_at is evidence, NOT a trusted DB wall clock or signing permission.
The pending B3-C writer must still perform authoritative fresh time/control
checks and enforce its uninterrupted first-call owner and final writer fence.

Nine specifically named guards retain their complete old deny predicates and
error bodies. Their sole new exception matches every old column, every new
column, physical rowid and command-owned one-way transition inside the effect
scope. The attempt must first be exactly prepared; step transition additionally
checks the exact signed attempt; lane transition checks the exact signed step.
No generic marker/status exception or unrelated conflict-victim relaxation.
All other attempt/step/lane/source/no-release/no-permit/no-send guards remain.

## Integrity and historical reading

Old M2/B2 initial-only views remain unchanged and reject signed work. New
phase-specific views duplicate the original immutable claim/admission/old-audit
conditions, verify the exact signed rows against the entire command and retain
the full-byte nonce observations, retained-alias exclusions and claimed
accounting-phase checks. They do not normalize signed state into old prepared
state. Query materialization boundaries avoid SQLite's 64-table flattened-join
limit without removing predicates.

Marker identity/033–035 audit conditions are independently checked in the signed
phase. Extra artifact commands/audit/outbox aliases, missing witnesses, corrupt
effects or residual scopes make history incomplete, not repairable.
The transient scope uses a deferred FK: it cannot commit without its command,
cannot reopen for a retained command and cannot be preseeded/adopted. Witnesses
are initial-statement effects only. Retained BLOB aliases and rowid/id/audit_id
victims are protected; alert state/delivered_at alone remain mutable.

The new pure metadata decoder/UDF checks full canonical bytes, version, original
Facts hash, key identity, hex/size bounds, hashes and original completion-before-
expiry semantics. This cannot decrypt ciphertext and does NOT certify crypto
validity. A test intentionally persists shape-complete corrupt ciphertext and
proves the history API returns zero result after authenticated decryption fails.

`loadManualSignedArtifactHistory` is package-private and SELECT-only. It verifies
request ownership, original command/binding/observation/accounting, full signed
phase completeness, then decrypts and exact-verifies original bytes through the
existing MANUAL v1 primitive. It returns metadata only, never plaintext/ciphertext
or execution/signing/send permission. Historical reads after stop/revoke/frozen
accounting/reopen remain identity inspection, not a refreshed expiry or action.
Wrong keys, cancellation, wrong identity/scope and corrupt history return zero.
No constructor, HTTP/config/startup, observer or signer is connected.

## Test and review boundary

Final source full ordinary Go tests PASS; storage 10.662s, trade 152.387s,
tradeapi 243.664s. The focused cumulative ordinary suite, phase single race
(API 550.138s), extra nonce-boundary race (32.501s), build/vet/staticcheck and
offline checks also PASS. The first phase race predates only the test-only
nonce-case addition; separate race and final full ordinary cover that addition.
No full-repository race, CI or independent phase approval is claimed. Exact
commands/source hashes/intermediate failures are retained in additive
`evidence/http-manual-m3b3-artifact-phase.json`. Whole B3 is not READY or PASS.

Permanent tests cover BUY, fixed SELL and resolved percentage SELL with a generated
matching wallet set BEFORE immutable intake facts/allowlists exist. Existing
test helpers keep their old default behavior; no immutable allowlist is changed.
Only test files use generated ephemeral keys and direct local cryptographic
fixtures/existing AES-GCM; product code has no signer invocation dependency.

Tests cover source denials, boundary expiry, full-column mutation, conflict
victims, all seven attempt/step/lane/audit/alert/scope fault windows with
ABORT/IGNORE and outer INSERT/IGNORE/REPLACE, two real SQLite handles, real file
reopen, retained corruption and no repair, same artifact/nonce and unchanged
original accounting. Upgrade tests cover actual 035→036 late journal rollback
and idempotent retry, with legacy BLOB data unchanged. Test-only corruption
injection restores exact triggers/FK before tested reads/writes/reopen.

The threat boundary remains normal FK-ON/reviewed-trigger execution, not arbitrary
hostile schema changes or forged effect-scope authority after disabling guards.
SQLite reopen is not power-loss proof. Test results and limitations belong in
the additive phase evidence; author checks are not independent approval.

No B3-C orchestration, new invocation/marker writer API, production keys/query
adapter, real RPC/grant, SubmissionService, ReplayUnknown, broadcaster/raw-send,
send permit, financial release/refund, rebuild/resign/replacement/fee bump/new
nonce, HTTP/startup wiring, live/release change, remote GitHub operation or
B4/M4/M5/W4-D/W5 follows. Whole B3 remains incomplete and not PASS.
