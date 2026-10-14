# MANUAL M3-B3 — controlled offline signing implementation

Local branch: `http-m3b3-controlled-local-signing`.
Base: `6c706f411cb3bac95c4425f7e8c33af72381e929` (local B2 closeout).
The user's subsequent continuing-execution instruction authorizes this B3
implementation only, not production signing, HTTP wiring or real sending.

## Ordered work and current checkpoint

B3-A is implemented and author-validated locally. The new package has no signer;
only test files use existing local signer/cipher primitives with ephemeral keys.
The SELECT-only claimed-facts helper is disconnected and is not a fresh gate.
No invocation marker, signing transition or artifact row exists yet. This is
not B3 completion or independent approval; B3-B/C/D below remain required.

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
M3-B3-A = IMPLEMENTED_AUTHOR_VALIDATED_NOT_INDEPENDENTLY_REVIEWED
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
