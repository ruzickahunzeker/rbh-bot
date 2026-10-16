# MANUAL M4-B2.2 — non-authorizing runtime verifier design

Inspected local base: `094053090d6dbe85b9469da58a54d1de94431b14`.
Docs-only branch: `http-m4b22-gate-design`; no implementation branch is created.
This is a proposed detailed boundary for acceptance, not implementation approval,
an independently approved design, production time/deployment attestation or a
change to any closed milestone. The user's continuation is applied to design only.

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4-A = PASS
M4-B1 = PASS
M4-B2.1 = PASS
M4-B2.2 scope = PROPOSED_FOR_ACCEPTANCE
M4-B2.2 implementation = NOT_STARTED
M4-B2.2 independent review = NOT_PERFORMED
checkpoint = M4_B22_DESIGN_READY_FOR_ACCEPTANCE
M4-B2 = INCOMPLETE_NOT_READY_NOT_PASS
M4-B = INCOMPLETE_NOT_READY_NOT_PASS
M4 = INCOMPLETE_NOT_PASS
M4-B2.3 = NOT_STARTED
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

## 1. Goal, not another permission system

Proposed B2.2 is a disconnected, package-private MANUAL runtime verifier with
controlled local harnesses. It authenticates an existing signed artifact and
checks actual process-owned scope/lease, bounded trusted-time observations,
read-only runtime identity and current DB inputs. Its output is private,
transient metadata: `VERIFIED_RUNTIME_CANDIDATE_NOT_PERMISSION`.

It neither issues nor consumes a permit, creates a canary PASS snapshot, changes
economic phase, releases funds/lanes nor authorizes execution/signing/send/replay.
All four action flags remain false and FreshGateRequired remains true. B2.1's
persisted false proof/action flags must not be rewritten to true. New SQL JSON
or a stored `verified=true` is never proof of real clock/binary/RPC/crypto work.

Use existing Operation -> ExecutionStep -> TransactionAttempt and the original
MANUAL command/typed accounting. No fake `canary_admission_sources`, second
reservation/ledger, new nonce/TTL/budget or authorization epoch. The only
candidate phase is complete SIGNED_PREBROADCAST with no submission history.
Unknown/submitted/corrupt/ambiguous downstream history rejects this verifier;
it must not be repaired or processed using a permissive signed-phase reader.

## 2. Inspected gaps and reusable components

| Current component | What can be reused | What is still missing |
|---|---|---|
| `manualSubmissionArtifactReader.load` / `loadManualSignedArtifactHistory` | original source/owner, MANUAL full-Facts AAD, authenticated artifact hashes, metadata-only result | reader is historical/snapshot-only, not a current gate; outer reader owns a different read transaction |
| `manualartifact.Open` / `Verify` | canonical bytes, signature/sender, chain4663, exact nonce/calldata/value/gas/fee including zero tip, full-Facts AAD | authenticates artifact only; no TTL/control/lease/RPC checks |
| `manualAdmissionScope`, control/auth/policy helpers | expected original identity, source/policy/attestation and conserved usage validation | scope is configuration, not measured running identity; some admission helpers calculate another admission budget and MUST NOT be called to re-charge |
| `manualSigningClock` | per-invocation sticky reversal pattern | no durable high-water/fault latch across restart or independent wall-time quality bound |
| additive038 | exact DB projection, original expiry bounds, immutable history and hardened audit/alert statement | no trusted current time/process ownership/binary/RPC/crypto proof and no product caller |
| `chainquery.Client` | fixed read-only method allowlist, strict envelope parsing, no user endpoint or generic RPC | no exact narrow MANUAL submission-runtime observation API today; do not rerun quote/simulation to fabricate a new contract |
| existing worker leases | SUBMISSION role/environment/holder/epoch/absolute expiry | row existence alone is not this process's ownership; compare an operator-owned lease reference in the final transaction |
| legacy orchestrator | boundary lessons, independent final recheck requirement | joins legacy source and manages permits/PASS snapshots; MUST NOT be composed for B2.2 |

No existing function is renamed into a proof by documentation. Existing M3/M4-A,
legacy gates/lease APIs and migrations001–038 stay unchanged. Prefer new private
or query-interface helpers over widening any reviewed public/phase contract.

## 3. Trust ownership and proposed dependency boundary

The verifier receives only authenticated caller identity + original request ID.
Wallet/operation/step/attempt/nonce/contract/runtime baseline/policy/quote/expiry
are loaded from original complete immutable MANUAL ownership, never HTTP or a
caller-supplied security struct. Expected scope, worker identity, time provider,
read-only RPC profile and decrypt-only primitive belong to the private harness
composition, not method arguments or exported JSON.

```text
private controlled-local verifier
  -> SQLite query/metadata writer on one owned connection
  -> authenticated time + actual process identity + expected SUBMISSION owner
  -> MANUAL decrypt-only adapter -> unchanged Open/Verify
  -> narrow read-only chain/header/code observer
  -> transient candidate + optional unchanged038 DB observation + audit

NO signer/key loader/encryptor/SubmissionService/broadcaster/permit/nonce allocator
NO HTTP/config/startup registration or production constructor
```

No plaintext raw transaction, ciphertext, encryption nonce, private key, provider
credential or opaque permission token escapes the result or audit/logs. Reuse
the existing decrypt-only adapter whose Encrypt path always fails. Synchronous
decrypt/verify must recheck context afterwards; best-effort owned-buffer wiping
is not a guarantee about all Go runtime copies. No real key/decrypt service is
loaded by tests; fixture signatures/decrypt calls are separately counted and
are not misreported as zero fixture activity.

## 4. Time contract and durable metadata proposal

Time provider is an operator-owned trusted dependency, not a Boolean in a SQL
row. A sample contains canonical UTC wall time, a finite nonnegative maximum
error bound, a process-local monotonic sample and provider/session identity.
Missing/unverifiable quality, overflow/out-of-range time or an unavailable
provider rejects. Plain `time.Now`/RPC block timestamp by itself is not labelled
externally attested UTC. Production provider approval/wiring is out of scope;
only explicit deterministic trusted providers exist in the controlled harness.

Let L = wall - error and U = wall + error. Require every original relevant
source timestamp <= L, and U < original expires_at, auth expiry and SUBMISSION
lease expiry. Equality at any upper expiry fails. Zero-error fixtures reduce
exactly to `now < expires_at`; canonical nanosecond parsing, not text ordering.
Observation freshness is bounded by an immutable local verification profile
(query timeout/max observation age/max error), not a new operation TTL. Missing
profile fails closed; no invented production defaults or client parameters.

Keep the original Go monotonic sample for duration/reversal tests before
serializing UTC. A backwards wall/monotonic sample latches invalid for the
whole invocation even if a later sample recovers. Across restart, compare wall
sample to a durable high-water floor and honor an already latched fault.
Do not compare a new lower uncertainty bound to the previous upper bound:
normal overlapping uncertainty intervals are not themselves wall reversal.

Proposed additive039 (NOT created or reserved by this design):

```text
manual_submission_clock_state
  chain_id + environment       primary clock domain, stable over deploy/restart
  generation                   monotonic metadata generation, not auth epoch
  high_water_at                canonical parsed nondecreasing wall sample
  fault_latched                monotonic 0 -> 1, no automatic reset
  fault_reason / fault_at       exact durable fault identity when latched
  updated_at                   canonical metadata timestamp
```

This table is clock metadata only, not a transaction/action state machine.
Generation overflow rejects; no DELETE/REPLACE/encoded alias/backward update or
fault clearing. State changes and exact runtime audit/alert are one transaction,
with schema-enforced direct-SQL transition evidence and fault tests. Missing,
corrupt or uninitialized clock state rejects candidate generation. Private
controlled-harness initialization must itself validate a trusted sample and
produce atomic metadata/audit; production bootstrap/reset is not implemented.
Restart/deployment change cannot recreate the clock domain or clear its latch.

The table cannot prove that a SQL-supplied timestamp came from a real clock;
the live private verifier still takes/revalidates its own samples. No separate
table of reusable `runtime_verified=true` credentials is proposed. Existing038
remains immutable DB observation with all proof/action flags false. Latched
clock state may only reduce eligibility; it never grants permission.

## 5. Running identity, owner and RPC checks

Actual running build identity must come from a measured binary/build manifest,
not copied from authorization, mutable working-tree HEAD or the request. Missing
revision/dirty-or-unverifiable build fails closed; release/deployment/environment
come from immutable authenticated composition. Compare them against original
Facts, binding and active auth/epoch. Changing deployment never edits the old
authorization to fit. A config string equal to build_sha alone is not proof.
Trusted controlled identity fixtures are not production remote attestation.

Load exact SUBMISSION lease by environment and compare holder/epoch to the
process-owned expected reference. Canonical expiry, upper-time expiry check,
unique encoded identity, exact prior-row/generation comparisons and final
transaction fencing are mandatory. B2.2 does not acquire/renew/take over a
lease; harness setup supplies an existing owner. RECOVERY lease is unrelated.

Narrow proposed chain observer uses only fixed chain/header/code methods.
Its deployment-owned endpoint/profile cannot be selected by HTTP. It must:

- verify chain4663 and the configured environment/network profile;
- obtain contract address + expected runtime hash from original MANUAL contract
  and immutable allowlist, not address/hash parameters supplied by the caller;
- confirm original quote block number/hash is still canonical, without requote;
- obtain a current observation block number/hash, read exact nonempty code at
  that anchor (not older than the original quote), hash it locally and match
  the durable authorized baseline;
- use block-hash pinning when supported, or before/after header fencing for the
  exact numbered block; reject inconsistent/reorged/malformed/partial evidence;
- reject unavailable/contradictory configured nodes, response identity errors,
  timeout, cancellation and data outside bounded local freshness limits.

No automatic endpoint fallback, majority selection or client override repairs
contradictions. Chain ID alone does not prove environment. The observed anchor
is separate from the original quote identity: neither is overwritten. This
proves code at the observation anchor, not that external chain state is atomic
with SQLite COMMIT or cannot change afterwards. A future immediate-pre-send
gate must make its own fresh verification; no contract-deadline protection is
claimed. B2.2 uses only controlled local JSON-RPC fixtures, no real-chain calls.

## 6. Verification sequence and final transaction

1. Read stop/control first. Missing/unreadable/stopped controls reject before
   external observer/decrypt work, irrespective of another invalid input.
2. In one initial read snapshot load original source/complete foundation/Facts,
   owner, exact artifact metadata, control/auth/policy/attestations, reservation/
   usage and expected lease. Verify process/time identity; no budget charge.
3. Release the read transaction. Do only bounded read-only RPC/identity work;
   do not hold a SQLite writer across network I/O. Sample time before/after
   external and cryptographic work, reject expiry/reversal/cancellation.
4. Acquire one connection and BEGIN IMMEDIATE. Read stop first again; reload
   original source/complete phase, auth state/epoch, policy/allowlists/gates,
   original active reserved risk/RESERVED usage and conserved lifetime totals,
   exact artifact envelope and expected SUBMISSION lease on this transaction.
   Compare full identities to pre-RPC inputs; drift rejects, no repair/retry
   with a fabricated newer contract/window. Reauthenticate exact bytes using
   a query-interface history helper on the same snapshot, including current
   decryptor key-version consistency; no M4-A
   outer reader opening another transaction or legacy AAD fallback.
5. Take final trusted time, reload/recheck clock floor/latch/generation on the
   same connection, validate RPC anchor/freshness/context and final expiry.
   Persist monotonic clock metadata + exact audit/alert and, if requested by
   this private capture call, an unchanged038 DB observation at final wall time.
   Never mix an old snapshot source_json with a new checked_at to dodge drift.
6. COMMIT; only then return detached non-authorizing metadata. On any SQL/error/
   cancellation/fault, no candidate is returned and explicit errors rollback.
   Check context after synchronous primitives and commit; a cancelled response
   must not expose a successful candidate even if metadata commit became durable.

If detecting a reversal while the main capture transaction must roll back,
persist the monotonic fault latch + critical audit in a separate bounded
metadata-only transaction AFTER rollback, rechecking the domain row. Never nest
transactions on the same connection or let rollback erase the only fault latch.
If durable latch/audit cannot be committed, mark the component locally unhealthy
and return failure; other healthy components still use their own trusted clock,
and DB unavailability is not replaced by a cached PASS. No write is claimed
durable while storage is unavailable. Fault-latch/reset semantics must be tested
under crash/restart, not asserted by a log message alone.

This final fence linearizes a DB observation, NOT sending. A later stop/revoke/
expiry/takeover after this COMMIT invalidates any future action; B2.3 must rerun
the final verification inside its own final gate/permit transaction. Proposed
private separation is: bounded observation preparation outside a writer, a
transaction-local final-check helper that never BEGINs/COMMITs and never grants
permission, and a B2.2 capture wrapper that owns its observation-only transaction.
Future B2.3 must use fresh preparation + that final-check helper on B2.3's own
writer; it MUST NOT nest the capture wrapper or reuse a committed candidate.
Detached candidate,
recorded hash or a duplicate result is never reusable authority.

## 7. Candidate model, duplicate and failure behavior

Transient result: original operation/request/step/attempt/owner/foundation/Facts/
artifact hashes + original expiry; DB observation ID/hash if newly recorded;
process identity digest; clock domain/generation/sample bounds; exact lease ref;
RPC chain/profile/quote and observation anchor/code hashes; completed_at;
SnapshotOnly=true, FreshGateRequired=true, all action flags=false.
Do not export or deserialize this result as a capability. Persistent038 proof
flags remain false; historical review PASS is never a runtime Boolean input.

An exact existing038 record may be returned as historical metadata only after
identity/completeness validation, not as a successful fresh runtime verification.
This is a separate history read, not a duplicate fast path in the verifier.
If a fresh candidate is requested, rerun the entire verifier; observing it may
create another metadata record, not an operation/expiry/permit/budget window.
Different original expiry/identity is a conflict. Restart discards transient
candidate and observations, reuses durable floor/latch/original binding, and
does fresh checks without resetting TTL or lifetime accounting.

Rejecting first-candidate verification does not create a transaction submission,
known-unsent/unknown/manual-resolution outcome or automatically release anything.
No send marker is written. Existing `send_intent_at` conservative ambiguity
semantics remain unchanged; ambiguous work stays frozen and query/reconcile
remains independent of new-candidate stop/auth/time rejection. History/recovery
must not be routed through this signed-only eligibility verifier.

## 8. Required implementation tests/evidence (not yet executed)

| Area | Required proof |
|---|---|
| Exact three sides | BUY/fixed SELL/resolved-percent SELL keep original Facts/raw hash/nonce/fees/minimum/quote/expiry/accounting; no new resolution |
| Ownership | caller address/hash/time/epoch/build/security-Boolean overrides unavailable; cross-owner/source/encoded aliases reject before crypto/RPC |
| Artifact | full-Facts AAD, tamper/key-version drift/sender/signature/fee/zero-tip mismatch fail; decrypt failure has no legacy fallback or output buffers |
| Clock | TTL/auth/lease expiry -1ns/equality/+1ns; malformed/BLOB/NUL/noncanonical; uncertainty straddles expiry; overflow/zero/error/late return; sticky recovered reversal |
| Restart clock | actual file close/reopen retains floor/latch; different build cannot reset domain; concurrent floor generation CAS; initialization/advance/fault audit atomic |
| Measured deployment | missing/dirty/mismatched binary, release/environment/chain/auth epoch fail; copied DB build fields alone insufficient |
| Lease | correct row but wrong process holder/epoch rejects; fractional boundaries; RECOVERY independent; no acquisition/renewal/nonce allocation |
| RPC | chain/profile/wrong curve/code, empty code, quote reorg, observation anchor conflict, mismatched JSON-RPC ID/duplicate fields/unavailable/late node fail; fixed read-only methods only |
| Final fence | barriers for stop/revoke/lease takeover/source drift during RPC and before BEGIN; serialized competing mutation cannot commit inside final writer boundary; post-commit drift grants no future action |
| Audit/crash | audit/alert ABORT/IGNORE/FAIL, journal/COMMIT/cancellation errors, provisional witness completion/UPSERT/multirow attacks; failed main transaction cannot erase required fault latch |
| Duplicate/restart | same binding never recharges or remints TTL/nonce/permit; complete historical record not a fresh candidate; incomplete history not repaired |
| No reachability | signer/encrypt/key-loader/submission/replay/raw send/permit/PASS snapshot/new nonce/financial effects zero for new path; production/startup/HTTP unchanged; fixture decrypt/read-RPC counts reported separately |

Include source/hash mappings, exact commands/results, intermediate failures and
timeouts without relabelling, ordinary relevant regressions and proportionate
race/barrier tests. Follow the B21 failure lesson: immediate failed COMMIT alone
is insufficient; attempt later-statement repair/adoption before asserting safety.
Independent review requires explicit B2.2 reviewer authorization; B21 approval
does not carry over. Maximum implemented checkpoint would be
M4_B22_READY_FOR_REVIEW, never author PASS or whole B2/M4 readiness.

## 9. Proposed implementation order and exact non-scope

After acceptance/implementation authorization, use one new B2.2 branch from the
then-verified local clean checkpoint. Implement/test in this order without
implicitly opening another slice:

1. Private clock/process/lease/query contracts and deterministic local fixtures.
2. Additive039 clock metadata + direct-SQL audit/concurrency/restart faults;
   validate all old schema objects remain exact. If another slice has consumed
   039, stop/reconcile numbering rather than overwrite it.
3. Narrow fixed read-only runtime observer, decrypt-only artifact integration
   and transaction-local MANUAL input projection.
4. Private verifier/capture coordinator + final mutable recheck and context/
   fault-latch behavior; no public/runtime wiring.
5. Full affected ordinary/race/dependency tests, honest additive evidence/hash,
   clean local commit, independent review gate; fix only in-scope blockers.

NO actual039, Go/test changes or implementation in this design slice.
NO permit/PASS/no-arm exceptions, FIRST_BROADCAST/UNKNOWN_REPLAY capability,
query-first replay contract, submission/outcome phase, broadcast/recovery worker,
transport/raw bytes handoff, public HTTP/config/startup composition, new signing,
rebuild/resign/replacement/fee bump/new nonce/expiry/budget/refund/release.
NO changes to001–038, M1/M2/M3/M4-A/B1/B21/C04/C05/C07/C08 reviewed behavior.
NO production key/time-attestor/deployment/RPC endpoint/network credentials,
actual chain calls, authorization grant, live/release/remote GitHub operation.
NO other chains/protocols, B2.3/B3/B4/C/D/M5/W4-D/W5.

This turn validates design traceability and docs/evidence/checksum preservation
only. Proposed tests are NOT test results; design-ready is NOT scope acceptance,
implementation-ready, verified runtime evidence or independent approval.
