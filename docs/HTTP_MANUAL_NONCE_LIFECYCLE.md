# MANUAL M3-B2 — controlled local nonce lifecycle

Latest claimed-state checkpoint: `http-m3b2-claimed-state-hardening`, based
on reviewed `46e371c36e73e598d2aa1ffb3ffd5d88cce1e790`. Fresh independent
review passed the byte/reference/adoption fixes but concluded FAIL for retained
claimed risk released/committed or usage RELEASED/CONSUMED being accepted as
valid history. Additive 031 changes the B2 public completeness gate only;
001–030, original writer and shared M2/C3 accounting/history code remain intact.
Maximum remains B2 READY_FOR_REVIEW, pending a new independent decision.

Previous reference/byte checkpoint: `http-m3b2-reference-byte-hardening`, based
exactly on reviewed `152e5083abd0bf7457bf9824a487aeba3adce844`. The fresh
independent reviewer concluded FAIL for own-claim NUL-suffixed evidence,
untrusted parent references omitted by raw joins, and UPDATE rename adoption
through NEW.id. The standing user authorization covers this narrow B2 repair
and subsequent independent review, not signing/send or B3. Additive 030 only;
001–029, original writer and historical evidence remain immutable. Maximum
implementation checkpoint remains M3_B2_READY_FOR_REVIEW, never author PASS.

Previous residual checkpoint: `http-m3b2-nonce-residual-hardening`, based exactly
on reviewed `ad15d05054b4077ac0a2df3ad5bae3997ac983f5`. That fresh independent
review concluded FAIL: BLOB wallet relevance, retained orphan parent INSERT
repair, and globally-scoped idempotency-key victim protection. This authorized
slice adds 029 and local tests/evidence only. Old 001–028, original nonce writer,
M1/M2/B1 and historical evidence remain unchanged. The maximum checkpoint is
M3_B2_READY_FOR_REVIEW, pending a separately authorized fresh independent review;
there is no author PASS, closeout or B3 authorization.

Previous local hardening checkpoint: `http-m3b2-nonce-hardening`, based on
`11e594fcd41479e001cb900791bdf35e9e4e2bc8`. Independent review of that base
concluded FAIL (nonce storage-class and retained orphan alias exclusions).
The original author record below is historical, not a fresh review result.
The authorized hardening adds 028 only; migrations 001–027 and the original
author evidence remain byte-identical. Maximum checkpoint remains
M3_B2_READY_FOR_REVIEW / PENDING_FRESH_INDEPENDENT_REVIEW, not PASS or B3.

Branch: `http-m3b2-nonce-lifecycle`.
Base: `09176c1143ad62ebb40cb0a58ad6ac00f68ec874` (M3-B1 local closeout).

```ini
M1 = PASS
M2 = PASS
M3-B1 = PASS
M3-B2 = M3_B2_READY_FOR_REVIEW
independent_review = NOT_PERFORMED
M3-B3 = NOT_STARTED
M3-B4 = NOT_STARTED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

This is a disconnected controlled/local harness, not production nonce RPC,
signing or whole-M3 PASS. Old migrations 001–026 and existing M1/M2/B1 readers,
writers and completeness views are unchanged. Only additive 027 replaces
seven specifically named initial-phase deny guards with exact one-way claim
exceptions, retaining their original deny domains and pollution protections.
No HTTP/config/startup adapter or real grant is introduced.

## Data model and exact phases

`manual_nonce_claims` is an immutable coordination command, not another state
machine or financial ledger. It references the original operation, existing
index-1 execution step, one deterministic attempt ID, wallet/canonical address,
TEXT nonce, original risk reservation/contract hash and canonical nonce
observation JSON/hash/checked_at. Authorization epoch/deployment/policy/quote/
expiry remain bound through the immutable M2 binding and original command.
The sole risk reservation and consumed lifetime budget are not copied or charged
again. A wallet/address + nonce is unique and cannot be reused by another claim.

| Phase | Existing lifecycle | Allowed B2 action |
|---|---|---|
| Bound | step `manual_execution_bound`, reserved lane with NULL nonce, no attempt | Fresh checks + one local query, then atomic claim |
| Claimed, signing excluded | same step `nonce_reserved`, same reserved lane with exact nonce, one attempt `prepared` with exact unsigned fields and NULL artifact/hash/encryption | Verified historical duplicate only; no new query/nonce or signing permission |
| Corrupt/incomplete | effects or immutable witnesses disagree | Zero-output rejection; no adoption, repair, release or refund |

Operation and validation step remain admission facts, so existing legacy
recoverable-operation scans do not pick up MANUAL claimed work. Attempt status
`prepared` reuses the existing lifecycle, with exact original chain/sender/to/
value/calldata/gas/fee/tip/policy/quote/expiry and no signature/raw artifact.
No invocation/signing ambiguity phase is implemented here; that is B3 work.

One INSERT owns attempt insertion, exact step/lane transition, append-only audit
and alert. Its final phase-aware completeness check aborts the entire statement
if a constituent was skipped even under outer OR IGNORE. Direct SQL receives
the same effects/audit or rollback. Immutable command and attempt/step/lane/
audit/alert conflict victims, including explicit/implicit rowid boundaries and
recursive_triggers OFF, are protected. Delivery state/timestamp of the alert
may still advance without changing its immutable identity.

The new `manual_nonce_claim_complete` view verifies immutable M2 facts and
original exact audit plus current claimed effects and negative alias/pollution
checks. It does NOT redefine the old initial-only view. M1/B1 and the M2 binding
writer reject claimed work rather than misreading it as a nonce-free phase.
Claimed risk/usage may freeze, never release/consume/thaw through this slice;
historical claim verification does not convert that frozen history into action.
Prepared attempts cannot acquire submission, receipt or position-effect rows.

## Query evidence and authoritative writer

The private writer accepts identity/request and immutable operator scope, not
a nonce or transaction override. Its private observer boundary has only one
local query method and no signer, send, nonce allocation or production adapter.
Initial owned read snapshot verifies current emergency stop first and B1's
full contract/controls/auth/TTL/gates/accounting/exclusive lane. Exact existing
claims are read as false-permission history, without querying a new nonce.

A first claim obtains a bounded (two-second context) controlled-local observation
outside the SQLite writer. Facts given to the observer originate from the
verified durable contract, never caller-supplied wallet/contract/runtime fields.
Observation binds operation/request/binding, chain/wallet/address, contract
runtime identity, canonical block/hash, equal confirmed and pending nonce and
canonical observed_at. A newer canonical block may be used; at the quote block
its hash must match exactly. Pending/confirmed disagreement, unavailable or
contradictory evidence rejects. No unsigned data or percentage re-resolution.

The writer then owns BEGIN IMMEDIATE with verified connection controls, reloads
current controls/auth/policy/gates/TTL/reservation/lane/complete ledger on that
same writer, and revalidates exact immutable input and observation. Competing
stop/revoke cannot commit before the protected claim outcome commits. Final
checks repeat control/auth/gates/TTL/time before commit. SQL derives facts from
durable joins and uses a pure deterministic scalar for observation SHA256,
exact canonical JSON/identity, absolute times and nonce range. A SQL client
without that scalar fails closed; it is not a network or permission capability.

A per-invocation clock tracker checks every read/query/writer timestamp,
including an intermediate reversal that later recovers; no shared mutable
clock state is added. Immediately before COMMIT it rechecks application/auth
expiry and observation age after all DB verification work. Crossing any of
those boundaries rolls back the provisional claim rather than publishing it.

Nonce zero and values above signed-int64 are valid canonical TEXT. Noncanonical
and uint64 overflow values reject; max uint64 also rejects because its successor
cannot be represented. No global nonce counter is advanced and no alternative
nonce is manufactured on conflict. Gas limit must remain positive SQL INTEGER
precision; an unrepresentable durable gas limit rejects, never coerces or changes.
Addresses are normalized only for query identity; original envelope strings
and fee/tip values are preserved exactly.

Observation time must not precede binding, exceed checked_at, expire or be more
than two seconds old. Equality to application/auth expiry and clock reversal
reject. Restart/duplicate cannot refresh original TTL, query evidence or epoch.
This query is evidence, not a guarantee the chain nonce stays unchanged later:
B3 still requires separately reviewed fresh controls/chain/runtime/nonce gates
immediately before signing the original claim, never a second claim or resign.

## Faults, concurrency and restart

Read cancellation retains operational context and commit cause; query timeout/
failure creates no claim. Before read/query/writer/insert/commit failures return
zero output with no durable claim effects. Provisional writes roll back as one
unit, including audit/alert. Response loss after a completed commit returns zero
output but restart rediscovers that exact durable claim: no second query, nonce,
attempt, reservation or budget use. TTL expiry/revoke alone do not release it.

Twenty-four contenders on two independent SQLite handles yield one first claim
and exact historical duplicates. Barrier tests show control updates commit
before the writer and reject it, or are SQLITE_BUSY inside the commit window
and can commit only afterward. Old-owner signing/lease semantics are not invented
in B2: no signer/worker/permit exists. File reopen, statement/transaction rollback
and failed migration rollback are tested, not OS power loss or external signing.

The 026-to-027 upgrade preserves legacy submitted/frozen recovery facts and
allocates nothing. A late trigger collision rolls back guard replacements,
tables and migration journal; retry and repeated migration are idempotent.

## Evidence and stop boundary

`evidence/http-manual-nonce-lifecycle.json` maps permanent B2/pure/scalar/upgrade
tests, source hashes, actual author validation commands, intermediate failures
and limitations. All historical author/reviewer evidence is preserved. There
is no independent review or full-repository race PASS implied by author checks.
Existing compiled package SDK/ethclient dependencies remain preexisting; the
new writer has no production backend, signer/cipher/submission/send capability.
Prohibited-path proof is local/static evidence, not production telemetry.

Only controlled fake fixture authorizations/nonce observations are used; no
real key, endpoint, mainnet call, authorization grant, live or release change.
Corruption fixtures explicitly remove guards/FK only in disposable test DBs.
No rebuild/resign/replacement/fee bump/new nonce on retry, new expiry window,
second risk/budget owner, permission/arming/send permit, other chain, B3/B4,
M4/M5/W4-D/W5, remote fetch/push/PR/merge or automatic PASS/closeout.

Stop at M3_B2_READY_FOR_REVIEW for separately authorized independent read-only
B2 review. Do not reuse the previous B1 reviewer permission or begin B3.

## Subsequent B2 alias-integrity hardening

The independent reviewer reproduced two holes at `11e594f`: normal SQL can
store a legacy nonce as BLOB bytes `23`, which Go reads as `"23"` but SQLite
compares unequal to TEXT `"23"`; an inner join can omit a retained attempt
whose step is missing even when its direct wallet/address and nonce conflict.
Neither result granted execution/signing/send permission. Nevertheless both
violate exact nonce conflict exclusion and corrupt-history zero-output denial.

028 adds a pure `manual_nonce_value_valid` scalar that accepts canonical uint64
TEXT only (including zero and above-int64 values, excluding successor overflow).
It never casts, normalizes, repairs or reallocates malformed history. The
query-only alias view preserves attempts with LEFT JOINs; direct wallet/address
identity is independent of a successful step/operation join, and step/operation
wallet identities remain covered. Relevant malformed nonce or unverifiable
ownership rejects; canonical nonconflicting legacy history and unrelated
wallet history are not globally blocked.

The original 027 completeness predicates are preserved in a read-only base
view. The public `manual_nonce_claim_complete` now additionally excludes all
relevant invalid/conflicting aliases. New claim guards enforce that check for
direct SQL before any claim effects, including OR IGNORE/REPLACE. Scoped guards
protect retained aliases and their ownership/conflict victims against mutation,
deletion, adoption and REPLACE with recursion OFF/ON. They exempt only the
original claim-owned attempt when protecting external ownership metadata;
027 still owns and checks the exact legal step/lane transition. No new state,
financial owner, permission, accounting charge, TTL or nonce window is created.

Permanent tests cover the original normal-SQL BLOB case; malformed/noncanonical
relevant values; orphan first claim and historical duplicate; direct SQL;
actual file reopen; unchanged durable snapshots; conflict-victim/no-repair
behavior; canonical/unrelated legacy compatibility; scalar replacement pool
connections; and late-failure atomic 027-to-028 upgrade without data repair.
Explicit corruption fixtures restore the production guards/FK before testing
denial. The original owner-only corruption fixture drops the new insertion
guard as well, solely to keep its corruption injection explicit, not to weaken
production policy.

Validation/results and source hashes are additive in
`evidence/http-manual-nonce-alias-hardening.json`. Original author hashes are
historical snapshots at `11e594f`, not current-source claims. Replaying earlier
reviewer probes during hardening is author regression evidence, not a fresh
independent approval. No closeout, production nonce adapter, HTTP/startup,
signer/cipher/submission/send, real grant/RPC/mainnet/live, B3/B4/M4/M5 or remote
operation is authorized by this hardening.

## Subsequent B2 identity / missing-parent / uniqueness hardening

029 changes only the alias view and narrowly named guards, preserving the
original 027 completeness base and the public 028 completeness wrapper. Wallet
identity in attempt, step and operation and case-insensitive sender address are
compared by a pure storage-class-aware scalar. It reports TEXT equality or a
matching BLOB as distinct values; a BLOB match is an untrusted denial witness,
never canonical input. Irrelevant BLOB identities and canonical nonconflicting
legacy history remain outside the blocked domain. No history is rewritten.

Scalar inputs use `hex(value)` plus `typeof(value)` so embedded NUL cannot be
silently truncated by SQLite's UDF TEXT conversion. Stored nonce validation
also checks the complete encoded bytes and original TEXT storage class; a
canonical prefix followed by NUL is malformed, not another valid nonce. The
original scalars remain for historical 027/028 objects; 029's aliases and
incoming mutation guards use the complete-byte validator. The pure functions
have no RPC, clock, allocation, database-write or authorization capability.

Missing-step and missing-operation guards preserve retained claimed history
before INSERT/UPDATE can supply or rename a missing parent. INSERT/IGNORE/
REPLACE and UPDATE/REPLACE are checked with recursion OFF/ON. Additional NEW
identity guards prevent incoming step/operation metadata from adopting attempts
into a claimed wallet's blocked domain. Claim-owned exact legal transitions
remain governed by 027; no second state machine or accounting owner is added.

Operation conflict protection uses ID, rowid or the actual composite unique
key `(chain_id,wallet_id,idempotency_key)`, never a globally unique key claim.
Same key on an unrelated wallet remains legal; replacing the real conflict
victim remains forbidden. The existing Robinhood-only chain constraint is not
relaxed, and this slice does not claim another-chain support.

Permanent tests map each residual to first-claim SQL/duplicate denial, immutable
snapshots, zero query on rejected history, restored guards around explicit
retained-corruption fixtures, actual file reopen, no parent repair, mutation
denial, compatibility and true conflict victims. Separate storage tests cover
replacement connections, whole-byte/NUL comparisons and a real 028-to-029 late
failure rolling back replaced views/guards/journal, followed by idempotent retry
with unchanged BLOB/orphan legacy rows. No migration repairs or allocates work.

`evidence/http-manual-nonce-residual-hardening.json` is additive author evidence.
Previous independent FAILs remain recorded; author regression is not fresh
independent approval. No production nonce adapter, HTTP/config/startup, signer,
cipher, artifact, permit, submission, send, grant, mainnet, live, B3/B4/M4/M5,
remote operation or automatic closeout follows from these checks.

## Subsequent B2 complete-byte claim and reference hardening

030 adds a pure ten-argument encoded observation validator. Each of the five
original evidence fields/facts is supplied with complete hex bytes and its
SQLite storage class; all must be TEXT and pass the original canonical
manualnonce verifier. BEFORE INSERT checks apply to the claim's own nonce,
JSON, hash and checked_at for INSERT/IGNORE/REPLACE. The original 028 public
completeness predicates are preserved in a base view; the public view adds the
same full-byte own-evidence check. Claim-owned attempts are never exempt from
their own evidence validation. Old scalars stay unchanged for old migrations.

Negative alias/parent guard joins compare complete encoded reference bytes to
identify potentially relevant ownership that raw TEXT-versus-BLOB equality
misses. This is denial-only correlation, not a trusted parent resolution:
non-TEXT step/operation references and parent IDs in a relevant alias make it
unsafe. Nothing is cast, repaired, adopted or reallocated. The existing positive
execution/admission/effect joins remain unchanged. Unrelated wallets' retained
BLOB references remain outside the blocked domain.

Parent INSERT and UPDATE guards inspect incoming references through NEW.id,
while old-owner and conflict-victim guards continue to protect OLD.id/rowid.
The same encoded correlation protects retained parent update/delete/replace
victims. The exact claim-owned legal step transition remains allowed; no second
lifecycle, reservation, budget charge, expiry/epoch window or permission exists.

Permanent tests cover all four own evidence NUL suffixes, public completeness
denial for explicitly retained 029-shaped history, first/direct-SQL and retained
BLOB parent conflicts, restored guards/FK, update/delete/victims with recursion
OFF/ON, projected parent rename, actual file reopen and unrelated-reference
compatibility. Storage tests check full-byte TEXT/BLOB/NULL/NUL facts and
replacement connections plus real 029-to-030 late-failure rollback/idempotent
retry without history repair. Reviewer-only probes are retained as historical
independent FAIL evidence; any author replay is not another independent review.

See additive `evidence/http-manual-nonce-reference-bytes-hardening.json` for
source hashes, exact validation scope, intermediate failures and limits. Stop at
B2 READY_FOR_REVIEW until a new independent reviewer passes the fixed commit.
No signing/cipher/production adapter/startup/permit/submission/send, real
RPC/key/grant/mainnet/live, B3/B4/M4/M5/W4-D/W5 or remote action follows.

## Subsequent B2 claimed accounting-state hardening

031 preserves the exact 030 whole-byte public predicates as a base view and
requires the original same-operation/wallet risk reservation to be reserved or
frozen, and original same-operation/authorization usage to be RESERVED or FROZEN.
No accounting state is changed by the migration. No shared historical checker
is tightened for M2/C3, no budget is refunded/recharged and no owner is added.
All four valid risk/usage combinations remain readable historical metadata with
false execution/sign/send permissions and zero new nonce query. The existing
freeze-only mutation guards stay unchanged; normal SQL still cannot release,
consume or thaw claimed work. Corrupt retained terminal states are additionally
rejected by completeness, even after exact guards are restored or files reopen.

Permanent tests cover four normal denied updates, explicit single-state-guard
retained corruption followed by restoration, zero-output/no-query/no-write
history denial, no implicit repair and actual file reopen. Positive tests keep
the original nonce/artifact-free attempt and all accounting snapshots unchanged
through every valid reserved/frozen combination and reopen. A real 030-to-031
test forces migration-journal failure after view replacement, proving both views
and journal roll back; retry is idempotent and preserves legacy BLOB/orphan data.
Old upgrade introspection now checks the layered alias, byte and accounting
predicates, not only the public view's immediate SQL text.

See additive `evidence/http-manual-nonce-claimed-state-hardening.json`. Author
regression does not grant independent PASS. The original reviewed commit and
retained-corruption finding remain recorded. No signer/send, new nonce/expiry,
financial transition, real grant/RPC/mainnet/live, B3 or remote action is added.
