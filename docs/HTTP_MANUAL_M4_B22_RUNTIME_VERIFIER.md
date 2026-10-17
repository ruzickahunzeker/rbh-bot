# MANUAL M4-B2.2 — disconnected runtime verifier implementation

Branch: `http-m4b22-runtime-verifier`.
Base: `c5c67ce74450d00600700b14f747e29397d987b2` (local detailed design).
User explicitly selected option2: accept B2.2 scope and authorize its additive039
and non-authorizing verifier implementation. No broader runtime authority follows.

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4-A = PASS
M4-B1 = PASS
M4-B2.1 = PASS
M4-B2.2 scope = ACCEPTED
M4-B2.2 = READY_FOR_REVIEW
checkpoint = M4_B22_READY_FOR_REVIEW
independent_M4_B22_review = NOT_PERFORMED
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

## Implemented capability and data ownership

The package-private `manualRuntimeVerifier.capture` authenticates original
MANUAL signed-prebroadcast history and captures current read-only runtime/DB
inputs. Only trusted controlled-local fixtures compose it. No public constructor,
HTTP/config/startup/economic-worker caller or real time/key/RPC provider is wired.
The output is transient `VERIFIED_RUNTIME_CANDIDATE_NOT_PERMISSION` metadata:
SnapshotOnly/FreshGateRequired true and all action flags false. No raw/ciphertext/
encryption-nonce buffer escapes. Original038 records remain unchanged immutable
DB_EVIDENCE_NOT_PERMISSION with all proof/action flags false; SQL cannot attest
that external verification ran. There is no reusable persisted proof credential.

Identity and original request are the only capture arguments. Original complete
foundation/contract/Facts plus control/auth/policy/allowlists/reserved typed usage,
conserved aggregate totals and SUBMISSION lease are reloaded using original038
views, not fabricated legacy sources or another admission budget charge. Exact
operation/request/step/attempt/reservation/wallet/auth epoch/environment/build/
release/deployment/original quote/expiry/nonce/fee including zero tip stay bound.
No signing/re-resolution/requote/new nonce/TTL/authorization/usage/refund occurs.

## Additive039: append-only clock ledger, not another economic machine

The proposed logical clock state is implemented as append-only
`manual_gate_clock_events` plus `manual_gate_clock_state`, the latest complete
generation per chain4663/environment. This representation preserves history and
avoids a mutable state row advancing ahead of its audit. There is no mutable
second transaction/reservation/authorization state machine.

Fields: deterministic id, chain_id, environment, generation, high_water_at,
fault_latched, fault_reason/fault_at, updated_at. Init generation1 is healthy;
healthy transitions advance exactly one generation with nondecreasing canonical
high-water. A fault preserves the prior high-water, latches1 with exact reason/
timestamp, and permits no future transition/reset/reinit/delete/replacement.
Fault timestamp may precede the floor when clock reversal is the reason.
Corrupt latest generation never falls back to an older healthy row. This table
is not a trusted clock proof: runtime samples still come from the owned provider.

Direct SQL uses the same transition guard and automatic exact audit/alert as the
private writer. Scope + witnesses are staged BEFORE actual event insertion,
following the B21 hardening. Initial audit insertion binds exact scope details;
event insertion rejects any existing scope/witness identity. The scope has
deferred event + insert-prohibited-empty-target FKs. Injected FAIL may retain
provisional scope/witnesses; even manually completing them cannot create/adopt
the absent event, close the scope or COMMIT. Rollback is mandatory. ABORT and
postclose-detected missing IGNORE witnesses restore the statement. Audit/alert
namespaces, byte aliases, rowid victims, duplicate/REPLACE/adoption/history repair
and retained alert identity are protected; legitimate alert delivery may update.

Private explicit bootstrap validates its provider samples and creates initial
metadata/audit, never reinitializes existing raw/incomplete history. No production
bootstrap/reset mechanism or runtime DB migration command was invoked here.
Upgrade tests build038, fail039's journal to prove whole-schema rollback, retry
and compare exact SQL of every prior object. Migration001–038 are byte-identical.

## Clock, process and observer trust

Time samples include canonical UTC wall, bounded nonnegative maximum error,
monotonic ticks and stable provider/session identity. Source timestamps must be
<= wall-error; wall+error must be strictly before original operation/auth/lease
expiry. Equality, fractional boundaries, malformed/out-of-range uncertainty,
backward wall/ticks and mid-invocation provider/session changes reject. Reversal
sticks for the invocation; durable floor/latch survives actual close/reopen.
Cancellation alone is not diagnosed as clock corruption. Provider panic/error
fails closed; failed latch persistence also marks this verifier unhealthy.

Latching occurs in a bounded metadata-only transaction AFTER failed capture has
rolled back/released its connection, with one exact critical audit/alert. Storage
failure never claims a durable latch or cached PASS. Detection and the subsequent
durable write are not atomic across a process/power failure: a crash before latch
commit cannot be claimed to have persisted it. Existing committed floor/latch
survives restart; every new invocation still takes fresh bounded trusted samples.
There is no hardware clock/remote time attestation or production time provider.
Deterministic harness providers are explicitly trusted fixtures, not plain
time.Now disguised as externally authenticated UTC.

The private measured-binary adapter reads clean VCS build metadata and hashes the
actual executable against an operator-owned immutable manifest, with bounded
file read and context checks. Missing/dirty revision or binary/manifest mismatch
rejects; it does not read working-tree HEAD or copy a DB revision. Release/env/
deployment/worker/network/time-provider identity belongs to that trusted manifest.
The verifier compares measured process output to its immutable expected profile
before observation and in the final writer (again after crypto). Tests use a
clearly separate trusted fake for positive measurements and exercise the real
adapter's reject path. No positive production attestation/build deployment or
authenticated manifest loader is claimed or installed.

SUBMISSION role/environment/holder/epoch must match the expected process owner;
lease existence alone is insufficient. Lease expiry uses parsed bounded time.
B2.2 never acquires/renews/takes over a lease. RECOVERY ownership is independent.

`chainquery.SubmissionRuntimeObserver` is a separate fixed read-only adapter.
Its operator profile includes an immutable network checkpoint, profile ID and
environment to avoid chain-ID-only environment claims. It validates chain4663,
checkpoint and original canonical quote, obtains a latest anchor not older than
quote/checkpoint, reads code pinned to that block hash with requireCanonical,
hashes code locally against original durable baseline, then rechecks numbered
headers/chain. Existing strict RPC ID/duplicate/error/null/timeout parsing is
reused. No quote/eth_call/simulation/nonce/send methods are used. Controlled
loopback tests cover identity/reorg/malformed/unavailable observations. No real
RPC, fallback provider, quorum protocol or external-chain atomicity is claimed.

## Snapshot, external work and final fence

Stop/control is checked first, before owned provider/decrypt/observer work.
Initial complete inputs and original artifact authentication share one read
snapshot. That transaction is released before bounded RPC work. Final
`BEGIN IMMEDIATE` re-reads stop/process/auth/epoch/policy/attestations/allowlists/
accounting/lease/time and original complete artifact on the same connection.
Full source JSON/hash and initial clock generation must still match; stale
preparation or concurrent clock-generation drift rejects without automatic retry.

The transaction-local finalCheck never owns BEGIN/COMMIT, calls RPC or grants
permission. It reauthenticates full-Facts MANUAL AAD using the unchanged history
verifier and decrypt-only adapter; no legacy fallback/Encrypt or signer exists.
Time and observed-anchor freshness are rechecked after synchronous crypto.
Then capture records next healthy clock generation and an exact unchanged038
observation in the same metadata-only transaction. An exact existing038 row can
be referenced only after the whole fresh verification; it is not a cache hit
permission fast path. Error/cancellation yields zero candidate; all explicit
errors rollback. Cancellation after durable COMMIT suppresses the response, not
a false assertion that metadata was rolled back.

The fence linearizes DB metadata, not external time/chain or sending. Post-COMMIT
stop/revoke/expiry/takeover invalidates future actions. Future B2.3 must freshly
prepare then run its own final-check within its own writer; it cannot nest this
capture transaction or reuse a detached candidate. No state transition to
known-unsent/unknown/submitting is added by rejecting verification. Submitted/
ambiguous recovery is not routed through this signed-only verifier. W4-B's
send-intent ambiguity boundary and frozen ambiguous ownership remain unchanged.

## Tests, intermediate outcomes and final validation attribution

New runtime tests cover three sides, fresh duplicate/real reopen, stop/owner/
source/lease/deployment denial, RPC/control drift, TTL after RPC/crypto, durable
clock reversal/latch/restart, uncertain/fractional auth/lease boundaries,
audit failure/cancellation/decrypt failure, expected-process-vs-DB separation,
writer barrier against competing stop/takeover and structural no-reachability.
Clock tests cover direct SQL mutation/encoding/generation/overflow, ABORT/IGNORE/
FAIL across four INSERT modes with later repair attempts, eight contenders/two
handles, corrupt-latest no fallback and real os.Exit77 before writer, before
COMMIT and after COMMIT. These are controlled software process exits, not power
loss. RPC tests use controlled JSON-RPC only; fixture signing/decrypt/RPC activity
is not falsely reported as zero. New-path signer/encrypt/permit/PASS/submission/
replay/send/nonce/economic actions remain structurally unavailable.

Intermediate author failures are preserved in additive evidence: initial unused
import compile failures; floor reversal initially masked by control timestamp
validation (fixed by floor check before that check, stop still first); incorrect
IGNORE fault-test assumption (actual postclose ABORT, assertion corrected); local
socket sandbox denial; RPC test fault-name mismatch (normal responses until
using fixture's bad_id/duplicate_envelope); initial unused-adapter staticcheck
(real negative tests added, not production wiring); final offline rerun initially
used the repo root instead of validation-package (scripts not found, corrected
directory rerun passed). Initial positive/focused
runs preceding final code/tests are not final-source or independent approval.

Final author validation on stable product/test source:

- `go test -p 1 ./... -count=1 -timeout=25m -json`: PASS; all18 new mapped
  top-level tests and all337 API top-level tests. API511.876s, trade245.021s,
  storage20.815s, chainquery1.487s, app0.252s (JSON package elapsed).
- Focused storage/API ordinary: PASS0.182s/86.923s; storage1, API15 new B22
  plus two existing compatibility tests. Exact filters are in evidence.
- Selected `-race` storage/chainquery/API: PASS11.748s/1.530s/551.451s;
  storage1/RPC2/API10 B22 tests, NOT all15 API or full-repository race.
- Final `go build ./...`, `go vet ./...`, staticcheck: PASS, empty success logs.
- Expected curve fixtures5 and offline reference models39: PASS (no real chain).
- gofmt, whitespace and old-file/mapping preservation checks: PASS. All261
  protected old product/schema/evidence/design files remain byte-identical.

Exact commands, named test mappings, source/log hashes and package checks belong
in additive `evidence/http-manual-m4b22-runtime-verifier.json`; all178 prior
manifest entries are preserved plus this one. Logs remain local temporary files,
not shipped runtime data. Local loopback/UDS permission was used only by controlled
fixtures. No independent B2.2 reviewer has been invoked; B21 permission does not
carry over. This is author verification, not external approval or remote CI.

Only three old test edits are required: migration count37->38/schema38->39 and
the B21 static reference guard's exact private capture-file exception. A new B22
graph test verifies that exception stays private and rejects economic/startup
reachability. Old product Go/001–038/prior evidence/approved scope docs remain
byte-identical. The current delivery plan is a new checkpoint; old hashes/status
remain historical snapshots. No guard opening or closed gate implementation is
modified. Highest final implementation state can only be M4_B22_READY_FOR_REVIEW.

NO B2.3/B3/B4/C/D/M5/W4-D/W5/other chain or protocol, real keys/authorization,
mainnet/provider call, production composition, send permit, live/release or
remote GitHub operation. Local implementation is not production readiness.
