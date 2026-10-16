# MANUAL M4-A — disconnected read-only artifact handoff

Branch: `http-m4a-artifact-handoff`.
Base: `1b6b528fea77b57f94f8bd1b964a425e17192ff5`.
Scope: `HTTP_MANUAL_SUBMISSION_SCOPE.md` and the accepted conversation freeze.

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4-A = READY_FOR_REVIEW
checkpoint = M4_A_READY_FOR_REVIEW
independent_M4_A_review = NOT_PERFORMED
M4 = INCOMPLETE_NOT_PASS
M4-B = NOT_STARTED
M5 = NOT_STARTED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## Contract, not submission wiring

New package-private `manualSubmissionArtifactReader` has only Store and a
structural KeyVersion/Decrypt dependency. No public constructor, HTTP/config/
startup caller, clock, signer, observer, encryptor, permit or transport is added.
It never returns raw/ciphertext/encryption nonce or marker-owner material.
To reuse unchanged M3 Open/history, a private adapter supplies an Encrypt stub
that always fails; it owns no underlying Encrypt capability and is never called
by this read path. Tests enforce no encryption/write/economic call sites.

Input is original scoped identity/request and operator-owned expected scope,
not a format tag, raw bytes, contract hash, quote, nonce or expiry override.
One read transaction checks exact durable MANUAL ownership and encoded legacy
canary/dry-run collision witnesses. Missing, dual-owned or unsupported legacy
requests fail closed before crypto. No format detection by trying decryptions.

The existing M3 signed-phase checker validates original command/binding/nonce/
accounting/audit/alert/envelope and cryptographic bytes on that SAME snapshot.
The new result includes detached Facts, hashes/key version/completed_at and
explicit source/format/phase, SnapshotOnly=true/FreshGateRequired=true. All
execution/sign/send/replay permissions remain false. MANUAL full-Facts AAD is
mandatory; no legacy fallback, key fallback, re-encryption or artifact repair.

## Historical and phase boundary

Only complete SIGNED_PREBROADCAST is supported. A claimed unsigned attempt or
marker-only signing ambiguity cannot become a handoff. Any retained submission/
receipt/effect phase not accepted by original 036 fails closed. This slice does
not add those phase contracts or weaken initial-only readers. Later slices must
add exact immutable-evidence plus phase-aware verification; a hash alone is not
enough. Existing old signed history and M3 implementation remain unchanged.

This is historical identity inspection, not admission or a TTL gate. Stop/
revoke and valid frozen risk/usage do not destroy exact history; returned expiry
is original and no clock is consulted. A WAL snapshot can remain coherent while
a second writer changes durable controls/source. The result is still not fresh
permission; the next read sees the changed source and rejects. Future action
must reload authoritative gates under its own reviewed writer boundary.

Cancellation around crypto/read COMMIT returns zero metadata and preserves
operational cause; cipher panic/error yields fixed sanitized failure. A trusted
synchronous decryptor is awaited; no forced interruption/secure erasure of
Go/library copies is claimed. The M3 cipher clears owned plaintext buffers.

## Tests and evidence

Nine permanent tests cover three-side exact repeated read/real reopen after
stop/revoke/freeze with unchanged whole-DB snapshots; unsigned/marker denial;
principal/wallet/auth epoch/policy/environment/build/release/deployment mismatch
before decrypt; both AAD directions, tamper/unknown-key no fallback; legacy
source aliases/downstream pollution; cancellation/deadline/panic/typed-nil/
closed-DB rejection; real WAL snapshot race and whole cmd/internal source
reachability/metadata-only contract. Positive setup invokes one offline signer;
the new reader never signs/observes/encrypts/sends or writes.

Initial focused runs failed only because new fixture code expected existing
MANUAL no-submission/no-legacy-source guards to permit pollution. Those guards
correctly rejected it. Final fixtures first prove ordinary SQL rejection and
snapshot preservation, then seed retained corruption in disposable DBs, restoring
all original guards/FK before the tested read. Product code was not relaxed.
Failure details and exact final commands/results/hashes belong in additive
`evidence/http-manual-m4a-artifact-handoff.json`, not silently relabeled PASS.

Final-source local author validation:

- Focused nine M4-A tests: PASS, 8.045s.
- `go test -p 1 ./... -count=1 -timeout=20m -json`: PASS; API296.503s,
  trade155.380s, storage10.791s, app0.176s; includes all nine new tests.
- Selected single race run across artifact/storage/API: PASS, 6/6/13
  top-level tests, including all nine M4-A tests; API248.237s.
- `go build ./...`, `go vet ./...`, `staticcheck ./...`: PASS.
- Offline reference model39 and expected Curve fixtures5: PASS; not chain proof.
- gofmt, whitespace, preservation and validation-package checks are required
  before the local commit. The additive evidence records exact commands and
  temporary log hashes; temporary logs are not shipped in the repository.

The full ordinary run used controlled local loopback/UDS fixtures. No real key,
endpoint, grant or transaction was used. Positive setup includes an ephemeral
offline signature; zero-action counts describe the new reader, not all legacy
tests. No full-repository race, independent approval, remote CI or power-loss
claim is made. A real close/reopen is not a power-loss test.

No migration, prior product/test/evidence, production startup/config/HTTP,
legacy loader/AAD, existing submission/recovery/gate or accounting implementation
is modified. No runtime action, grant or readiness change follows. Maximum
M4_A_READY_FOR_REVIEW only; independent M4-A review is required before M4-B.
No whole-M4 PASS, M4-B/C/D, M5 or production approval is inferred.
