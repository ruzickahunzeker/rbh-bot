# MANUAL M3-B3 — controlled offline signing implementation

Local branch: `http-m3b3-controlled-local-signing`.
Base: `6c706f411cb3bac95c4425f7e8c33af72381e929` (local B2 closeout).
The user's subsequent continuing-execution instruction authorizes this B3
implementation only, not production signing, HTTP wiring or real sending.

## Ordered work and current checkpoint

Current foundation checkpoint: independent read-only review **PASS** at
`ff073f02713c4f03b7cdd0c98ff63095b697d729`, limited to B3-A/B1. All prior owner,
retained-alias and missing-witness findings are closed within the reviewed
FK-ON / intact-trigger boundary. Independent focused tests and additional
temporary SQL probes passed. The author's complete affected-package ordinary
run also finished PASS (storage 9.908s / tradeapi 210.368s); this later result
is recorded additively, not by rewriting the frozen implementation evidence.
See `evidence/http-manual-m3b3-foundation-review.json` for exact commands,
review/probe hashes, qualifications and retained prior FAILs.

This is a foundation checkpoint, NOT whole-B3 closeout or permission to sign.
B3-B2 exact artifact phase, B3-C fenced local orchestration and B3-D cumulative
acceptance remain unimplemented. Existing attempt/step/lane and signing/send
guards are unchanged. The next implementation must use the next additive
migration, keep original facts/accounting and introduce no HTTP/startup/send.

The tested threat limit is explicit: normal SQL cannot reopen or independently
commit the creation scope. If an administrator deliberately disables its source
guard and forges matching creation authority, direct SQL can rebuild/close
effects; no protection against that hostile schema modification is claimed.
Historical reads reject residual scope without clearing it. The SQL scope is
never signing authority; later orchestration still requires fresh mutable gates,
an in-memory first-call owner and the final writer fence.

Historical review/fix checkpoints follow; their FAIL/pending statements describe
their original snapshots, not the current foundation conclusion.

Fresh foundation review of `13fe6b76d4b6cd868704d1229bd99c07f4748d98`
closed 034's owner/alias findings but still concluded B3-B1 FAIL: absent
audit/alert witnesses could be reinserted for retained markers. Additive 035
restricts creation to a transient same-statement SQL scope. A BEFORE marker
trigger opens it; exact original audit/alert effects run; verified closure
removes it before success. The deferred FK prevents standalone scope commit,
and an existing marker forbids reopening. Preseeded scopes cannot be adopted.
Missing historical witnesses remain missing, never repaired/backfilled.
All scopes must be empty after success; residual scope makes history incomplete.
This is statement coordination only, not a new execution state machine, durable
financial owner or reusable invocation/send permit. 001–034 are unchanged;
only the marker's own effect trigger is replaced in 035 with its original
payloads and explicit closure. Fresh review remains required.

Foundation independent review of `bee4513d2514146e849b5bc5034fc75d96b77eaf`
concluded B3-A scope-limited PASS, B3-B1 FAIL (combined FAIL). It reproduced
retained BLOB audit/outbox witness repair/adoption and a total-64-byte interior
NUL owner accepted by SQLite GLOB. That historical FAIL is not overwritten.
Additive 034 addresses only these foundation residuals, with encoded denial-only
witness/victim/OLD+NEW protection, full-byte owner checks and extra-alert
completeness exclusion. 001–033 remain byte-identical. Legitimate outbox delivery
updates remain supported. No phase/signing/send guard is opened. Fixes require
fresh foundation review; this is not whole-B3 completion or approval.

B3-A is implemented and author-validated locally. The new package has no signer;
only test files use existing local signer/cipher primitives with ephemeral keys.
The SELECT-only claimed-facts helper is disconnected and is not a fresh gate.
B3-B1 now adds additive 033's marker/audit foundation and read-only ambiguity
history. No code inserts markers outside controlled SQL tests, opens signing
transitions or invokes a signer. This is not B3 completion or independent
approval; B3-B2/C/D below remain required.

1. B3-A: exact claimed-transaction projection, strict signed-byte verification
   and versioned MANUAL encrypted artifact primitives. No durable transitions
   or orchestrator signer invocation in this first sub-batch.
2. B3-B: additive 033+ exact invocation/artifact commands and existing
   step/attempt/lane transitions, phase-aware completeness, atomic audit/alert
   and victim/no-repair guards. Preserve 001–032 and closed send guards.
3. B3-C: disconnected controlled-local orchestration, fresh query evidence and
   authoritative writer fences before invocation and artifact commit.
4. B3-D: complete fault/reopen/concurrency/compatibility evidence, then stop at
   M3_B3_READY_FOR_REVIEW. Independent approval and formal closeout are separate;
   no author PASS or automatic B4/M4/M5 follows.

```ini
M1 = PASS
M2 = PASS
M3-B1 = PASS
M3-B2 = PASS
M3-B3 = IN_PROGRESS
M3-B3-A = FOUNDATION_SCOPE_INDEPENDENT_PASS_AT_FF073F
M3-B3-B1 = REVIEWED_DURABLE_MARKER_FOUNDATION_NOT_SIGNING_READY
foundation_review = PASS_AT_FF073F_SCOPE_LIMITED_NOT_WHOLE_B3
M3-B4 = NOT_STARTED
M3 overall = INCOMPLETE_NOT_PASS
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## Identity and ownership

Robinhood 4663 / Pons v2 Curve native quote only, BUY/fixed SELL/resolved
percentage SELL. All facts originate from original immutable command/binding
and B2 claim, not HTTP or a caller's claimed security fields. Preserve exact
chain/sender/to/value/data/gas/fee/tip, claimed nonce, policy version, quote
block/hash, absolute expiry, contract hash, original reservation, authorization
ID/epoch, policy/gate hashes and build/release/deployment/environment.
APPLICATION_TTL_ONLY; contract_deadline=false. No new quote, percentage
resolution, fee default, financial owner, budget consumption or TTL/epoch window.

Keep Operation -> ExecutionStep -> TransactionAttempt. Claim remains original
unsigned prepared attempt until the explicitly reviewed B3-B transitions exist.
No new financial ledger or second recovery/submission state machine.

## Durable marker foundation (B3-B1 / 033)

`manual_signing_intents` is append-only coordination metadata on the original
B2 claim, not a second execution state machine, financial reservation or permit.
One operation and one 64-hex owner identity can be recorded once. Facts JSON
is derived in SQL from the durable claim/binding/command in the exact Go
canonical order; full-byte scalar validation rejects malformed/type/NUL drift.
The source guard requires original complete prepared claim, original reserved
risk/usage, source-derived wallet/contract/token identity, active bound auth,
control/policy/durable gate identities, conserved already-consumed budget and
fresh same-nonce query evidence. Checked time is evidence, NOT the DB wall clock
or signing permission; a future writer must independently recheck current time
and all mutable gates after marker commit, immediately before any signer call.

Every INSERT/IGNORE/REPLACE either creates exactly one marker with its exact
durable audit and WARNING outbox event, or aborts the whole statement. Dropped
or ignored audit/alert writes cannot produce marker-only success. UPDATE,
DELETE, implicit rowid/REPLACE conflict victims and retained byte aliases cannot
reset/adopt the marker. Future consumers must treat any incomplete/unverifiable
marker as deny/frozen, not as permission to start anew.

This foundation intentionally retains the original step `nonce_reserved`,
attempt `prepared` and exclusive lane `reserved`; all existing signed-artifact,
lane-release, financial-release and send guards remain closed. The immutable
marker is an additional reason no new invocation is allowed. "Frozen" here
means no new action/release, not a newly introduced textual lane phase. A
later additive exact phase contract is still required for signing/artifact
effects; no blanket exception is introduced in 033. History returns only
SIGNING_OUTCOME_UNKNOWN_NOT_PERMISSION with all three permission flags false,
even after stop/revoke/expiry or restart; it never says a signer actually ran.

Tests use direct SQL in disposable fixtures, including explicit restored
guard/CHECK/FK corruption injection, real file reopen and 12 simultaneous
contenders across two DB handles. Pure scalar registration survives pool
replacement. Real 032→033 late journal failure rolls back all schema/journal
changes, retries idempotently and preserves legacy BLOB/orphan rows. The prior
artifact tests now use an external test package solely to avoid a test-only
import cycle through existing trade -> storage -> manualartifact dependencies;
no production signer dependency or implementation was moved or changed.

034 also denies retained same-byte BLOB audit/outbox replacement, rowid victims,
duplicate TEXT witness insertion, OLD identity changes and projected NEW
adoption. Extra relevant alert aliases make history incomplete; removing them
is forbidden. Owner validation checks both byte and TEXT length plus lowercase
ASCII hex, so leading/interior/trailing NULs cannot hide inside 64 bytes.
Retained corruption remains untouched/unreadable after actual file reopen.
Fault tests explicitly disable original table triggers/FK only for disposable
seeding and restore every exact trigger definition/FK before the tested action.
The owner-history negative control satisfies the old 033 predicate including
its matching audit payload, isolating the new full-byte rejection. No DB
wall-clock/signing-permission or hostile-schema-modification guarantee follows.

## Invocation ambiguity and writer fencing

A durable invocation marker means: after its commit, restart can no longer
prove signer invocation did not occur. It is NOT proof of completed signing
and is NOT signing permission. A second BEGIN IMMEDIATE must reload current
emergency stop first, authorization/epoch/deployment, immutable identities,
policy/allowlists/durable gates, exact claim, initial reserved risk/usage,
conserved allowance, wallet exclusivity, trustworthy clock/TTL and bounded
fresh controlled-local chain/runtime/nonce evidence immediately before signing.
Stop/revoke/ownership changes must not commit inside the protected invocation
window. No observer capable of sending or allocating nonce may enter this graph.

Marker commit followed by crash, failed fence, lost response or absent artifact
is conservative SIGNING ambiguity: retain original claim/lane/accounting, no
automatic signer retry, no resign/rebuild/replacement/fee bump/new nonce. Do NOT
call it broadcast_unknown: there is no broadcaster. Recovery never manufactures
an artifact or reuses a stale candidate as permission. A trusted synchronous
local signer must honor its bounded context; do not release a writer fence and
leave an unbounded orphan signer goroutine running.

After signing, exact verification and encryption are required. Expiry/clock
reversal after invocation, cancellation, verifier/cipher failure or artifact
commit failure returns zero usable artifact and preserves ambiguity. Complete
encrypted history may later be inspected after expiry/revoke, never treated as
fresh execution/sign/send permission. A historical read must decrypt and verify
against the original complete durable identity, not just ciphertext/tx hashes.

## MANUAL artifact format v1 (B3-A)

`manualartifact.Facts` is a canonical, bounded projection candidate, not proof
that DB gates passed. Its canonical JSON SHA256 binds all immutable fields.
Only EIP-1559 transactions with empty access lists and exact unsigned fields
are accepted; raw transaction bytes must round-trip canonically. No alternate
transaction type, sender/key, chain, nonce, fee, contract creation or data.

Reuse existing AES-GCM primitives through their narrow structural interface.
Keep the legacy `rbh:4663:<operation>:<step>:<attempt>` AAD prefix, with explicit
`:manual:v1:<facts SHA256>` suffix to bind policy/quote/expiry/authorization and
deployment as well. This is a NEW MANUAL AAD format, not a silent change to
legacy artifacts or a claim that legacy submission can consume it. Any future
consumer must explicitly verify MANUAL v1; legacy signer/cipher/submission code
is unchanged. Key version is explicit, non-empty and checked; no key fallback.
Only ciphertext/encryption nonce, metadata/hash and false permission flags may
leave the primitive boundary; plaintext raw bytes are not returned by Open.

Temporary plaintext and decrypted buffers are cleared on return where owned;
Go/runtime or cryptographic-library copies are not claimed securely erased.
Cipher output is round-trip decrypted and exact-verified before success. Every
failure returns a zero result with fixed sanitized errors, never private key,
plaintext, ciphertext or injected error details in messages. Pure validation
does not know current time and cannot certify TTL validity or authorize signing.

## Acceptance and stop boundaries

B3-A must test exact original fields, nonce zero/above-int64, zero/nonzero tip,
all signed-field substitutions, wrong sender/type/access list, malformed or
noncanonical metadata/raw bytes, wrong AAD/key/ciphertext/nonce and cipher
failure. Test-only ephemeral keys and existing local cipher primitives only.
HTTP/config/startup and 001–032 remain unchanged; no new transaction in SQLite
or signer dependency is allowed in HTTP's source/call path.

B3-B/C/D additionally require direct SQL atomicity, original guard preservation,
all fault windows, two-handle one-invocation concurrency, commit-window stop/
revoke fences, duplicate/restart no re-invocation and same artifact identity,
post-invocation TTL/revoke ambiguity, exact signing call counts and no send.
Every remaining unchecked requirement must remain explicit, never relabeled
complete merely because B3-A primitives or author tests pass.

No production keys, production query adapter, real chain RPC, signer service,
HTTP/config/startup wiring, SubmissionService, ReplayUnknown, RawBroadcaster,
SendRawTransaction, send permits, real authorization grant, unrestricted live,
mainnet/live/release change, BSC/Flap/Base/Solana, Long/LP/strategy expansion,
B4/M4/M5/W4-D/W5 or remote fetch/push/PR/merge action is authorized here.

## Remaining batches — implementation checklist, not completed evidence

### B3-B2: exact artifact phase contract

- Next additive migration, never edits to 001–035. Immutable command binds the
  existing marker owner/facts, ciphertext/key/nonce/hash metadata and completion
  time to the ONE existing attempt. No second financial owner or transaction.
- Same-statement command effect updates only that prepared attempt to signed,
  that nonce_reserved step to signed and that exclusive reserved lane to signed,
  with exact audit/alert. Original operation/validation/admission facts remain.
- Replace only named guard definitions by retaining their complete old deny
  predicate and adding an exact one-way command-owned row/column/rowid exception.
  No generic "marker exists" or "status signed" exception; no unrelated victims.
- New phase-aware completeness verifies original immutable B2/M2 facts AND
  all exact new effects. Any phase projection used for equality must first
  verify the actual signed row against the full command and cannot normalize
  malformed storage or make old initial-only views accept arbitrary signed rows.
- Marker-without-complete-artifact remains ambiguity. Original B2 history APIs
  must never convert a signing/signed phase to a fresh claim or sign permission.
  Missing/corrupt artifacts reject without repair, re-encryption or resign.
- Schema proves metadata/effect/audit completeness; it cannot decrypt ciphertext
  without a key. Every usable artifact history path must ALSO decrypt and verify
  exact bytes against original facts. No schema-complete => crypto-valid claim.

### B3-C: disconnected fenced local orchestration

- A first invocation requires original B2 claim; never allocate a nonce here.
  Read candidate facts, query a bounded controlled-local observer outside the
  writer, and verify same original nonce/current chain/runtime/canonical evidence.
- BEGIN IMMEDIATE reloads emergency stop first, scope/auth/epoch/deployment,
  policy/allowlists/attestations, original complete claim/accounting/usage caps,
  wallet exclusivity and canonical non-reversing current clock/absolute expiry.
  Commit a single marker with a fresh per-call owner only after final rechecks.
- After marker commit, acquire a new writer fence and repeat all authoritative
  mutable and evidence-age checks. Only this uninterrupted first invocation's
  in-memory ownership may continue. Reading an owner ID from history is NOT
  permission to resume; any subsequent call/restart is history/ambiguity only.
- Check matching signer address, final time/auth/application TTL and bounded
  observer freshness immediately before synchronous local signer invocation.
  Hold the writer fence through the bounded trusted call/outcome; no orphan
  signer goroutine, background retry or plaintext fallback.
- Exact raw verification/encryption, current time/expiry/evidence checks and
  atomic command/effect commit must succeed before returning metadata. Failure
  after marker never erases ambiguity or automatically signs a second time.
- Complete artifact history skips observer/signer and remains false-permission
  after TTL/revoke/stop. Its exact encrypted bytes/identity are unchanged.
  No HTTP/config/startup adapter or submission/send dependency is added.

### B3-D: cumulative acceptance evidence

Test exact BUY/fixed SELL/resolved percentage SELL with ephemeral matching keys,
one invocation under competing callers, wrong signer zero calls, gate denial
zero calls, control-writer barriers, every marker/sign/encryption/commit fault,
response loss and actual file close/reopen. Prove no retry/restart resign or new
nonce, no leaked usable artifact on failure, original accounting conserved,
all direct-SQL/IGNORE/REPLACE/victim/retained-phase protections and no send.
Then run proportionate full ordinary/race/compatibility/lint/validation checks,
record real commands/results/limits and stop at M3_B3_READY_FOR_REVIEW. Stage-
specific independent review is still required; prior B2 PASS is not B3 approval.
