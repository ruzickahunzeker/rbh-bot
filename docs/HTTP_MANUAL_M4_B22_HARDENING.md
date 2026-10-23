# MANUAL M4-B2.2 — running-image and clock-evidence hardening

Branch: `http-m4b22-runtime-hardening`.
Base/reviewed original head: `04112980feee6f69ec936f5c9eab72b8dbb03425`.
This continuation implements only the two reported independent-review blockers.
It does not inherit approval of the changed head or enter B2.3.

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4-A = PASS
M4-B1 = PASS
M4-B2.1 = PASS
M4-B2.2 = READY_FOR_REVIEW
checkpoint = M4_B22_READY_FOR_REVIEW
checkpoint_maximum = M4_B22_READY_FOR_REVIEW
original_independent_review = BLOCKED
fresh_hardening_independent_review = NOT_PERFORMED
M4-B2 = INCOMPLETE_NOT_READY_NOT_PASS
M4-B = INCOMPLETE_NOT_READY_NOT_PASS
M4 = INCOMPLETE_NOT_PASS
M4-B2.3 = NOT_STARTED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## Original review, not author approval

The explicitly authorized independent reviewer concluded BLOCKED at the base:

1. `manual_runtime_identity.go` opened the pathname from `os.Executable()`.
   After rename/replacement, a process can still run the old image while that
   pathname contains different bytes with the same clean VCS revision. A manifest
   matching the replacement could incorrectly pass. This violates actual-running
   binary identity, not merely evidence formatting.
2. The original evidence mapped provider/session drift to a test that never
   changed those fields; it also did not reverse monotonic ticks. Production
   clock checks existed, but those accepted invariants lacked direct evidence.

Independent focused ordinary tests passed storage0.172s/chainquery0.096s/
API41.790s; manifest179,15 source/doc hashes, author-log hashes and declared
ordinary18/selected-race13 mappings matched. That did not close these blockers.
The review found no other substantive issue in039, exact source/artifact binding,
final writer fencing or fixed read-only RPC. This is a receipt of its reported
findings, not a fresh independent review of the hardening.

## Minimal production correction

The existing measured adapter keeps its clean build-metadata and exact immutable
manifest comparison, but calls private `manualRunningImageHash`. Linux opens
`/proc/self/exe` directly, keeping a handle to the actual executing inode even
when its old pathname is renamed/replaced/unlinked. No `os.Executable` pathname
fallback, request/DB identity echo or working-tree revision is used.

Unverifiable/unsupported platforms, unavailable proc handle, invalid/nonregular/
empty/oversized image, failed/short/oversized read and cancellation reject with
no hash/candidate. Reads remain bounded512MiB. Non-Linux platforms fail closed;
portability requires a separately reviewed equivalent running-image primitive,
not a pathname fallback. No production constructor/manifest loader/attestation
or startup wiring is added, and this is not remote/hardware attestation.

## Direct regression evidence

`TestHTTPM4B22RunningImageReplacementSameRevision` compiles the exact production
identity and image-helper source (package name changed only) in a disposable,
clean, offline Git fixture. Minimal domain value/error/hash/JSON stubs replace
unrelated package dependencies; this is not production trade composition.
It builds a real executable with clean VCS metadata, measures its original image,
renames it and writes a same-revision replacement with different bytes. The
replacement-matching manifest must reject while the original manifest must keep
matching the actual running image, including after both pathnames are removed.
After unlink, the fixture reuses the exact pathname still reported by
`os.Executable` (which strips Linux's deleted suffix). This is important because
Linux updates the proc link on rename: replacing only the earlier pathname is
not sufficient proof of an incorrect measurement. The actual reused-pathname
case rejects the replacement manifest and retains the original image match.
Nil/cancelled image measurement must reject. Only temporary fixture files are
renamed/deleted; no real wallet, chain, key, authorization or send is involved.

`TestHTTPM4B22ClockDriftRecoveryIsSticky` uses explicit trusted sample sequences
for provider change, session change and nonnegative backwards monotonic ticks.
Each sequence includes a later recovered sample. After the fault, the invocation
must keep returning zero sample/error without calling the source again.

`TestHTTPM4B22ClockDriftDurableLatchAndRestart` runs the same three variants both
before RPC and in the final writer after RPC/crypto. Assert zero candidate, zero
038 observation, unchanged economic/permit/artifact/accounting snapshots, exact
one new fault event/audit/alert, terminal fault generation2 and CLOCK_REVERSAL.
An actual SQLite close/reopen with healthy recovered samples still rejects and
does not add another fault event or economic action. Read-only fixture RPC counts
are explicitly0 before RPC and1 in the final-writer case, never falsely zeroed.

The old provider/session fixture and all original tests remain unchanged. The
original implementation evidence and its hashes/status are historical snapshots;
the provider/session mapping in that evidence is specifically superseded by these
named tests, not retrospectively claimed to have run in the original validation.

## Validation and attribution

Final author validation is recorded only after completion in additive
`evidence/http-manual-m4b22-runtime-hardening.json`. It contains exact commands,
results, source/log hashes, new test mappings and limits. Original179 manifest
entries remain byte-identical; the new evidence is the only appended entry.

Completed author results (Go JSON package elapsed):

- Full ordinary repository PASS, API498.303s/trade216.316s/storage18.079s;
  final product source, but before the image-fixture strengthening described
  below. This is not a full-repository final-test-source claim.
- Final focused storage/RPC/API PASS0.481s/0.128s/98.388s; all21 B22 mappings
  (original18 + new3), plus two compatibility tests. Final product/test source.
- Final new-hardening race3 PASS165.609s; six-case durable drift, sticky clock
  and strengthened running-image regression. NOT whole B22/repository race.
- Earlier selected race6 PASS287.078s includes writer-barrier/dependency/
  measured-adapter compatibility, before only the image-fixture strengthening.
- Build, final vet/staticcheck, expected curve5/offline39, gofmt/whitespace,
  prior-file/hash/mapping preservation and180-entry package manifest PASS.

Focused RPC initially failed only because local listen was sandbox-denied;
separate permissioned loopback rerun PASS0.195s and final whole focused rerun
PASS above. The initial batch's API95.480s/storage0.177s success does not turn
its overall exit1 into PASS. Logs are temporary author data, not shipped.

An initial hardening test failed because its assertion counted all fixture audit
rows as if they were only clock rows. It was corrected to require exactly one
additional durable audit/alert relative to the captured baseline, not by changing
production code or weakening the one-event/one-audit/one-alert requirement.
Early focused success before the final-writer cases was not final-source proof.
The first old-code negative control failed only when the original image name
was removed, not by demonstrating incorrect acceptance. The strengthened control
uses the actual deleted-image pathname; the original adapter then fails the
regression with `accepted reused deleted-image pathname manifest`, while the
fixed adapter passes. These negative controls are intentionally failing tests
of old code, not failures or independent approval of the new implementation.
The full ordinary batch and initial six-test race group preceded only this
image-fixture strengthening; their product source is final but test source is
not. Fresh focused all-B22 and new-hardening race reruns use final test source.
Patch-context failures changed no files and are not test outcomes.

No new hardening-independent reviewer has been invoked. Fresh independent review
is required at the final committed hardening head before closeout; author testing
cannot mark B22 PASS. Whole B2/B/M4 remain incomplete. No full-repository race,
remote CI, production deployment or real-chain result is claimed without execution.

## Preserved boundaries

Only the measured-identity helper changes production behavior. All migrations
001–039, original clock/verifier/observer/artifact/accounting implementations,
old tests and prior evidence remain unchanged. The delivery plan adds a current
checkpoint without rewriting historical statements. Current hashes in this
evidence supersede old source hashes only for files actually changed here.

Signer/encryptor/permit/PASS snapshot/SubmissionService/replay/broadcaster/send/
nonce allocation/new economic accounting remain structurally unreachable from
the new path. This is a dependency proof, not zero fixture signing/decrypt/RPC
activity or production telemetry. Authorization remains NOT_GRANTED; no mainnet,
live, release readiness, extra chain/protocol, B2.3/B3/B4/C/D/M5/W4-D/W5 or remote
GitHub action is authorized or performed.
