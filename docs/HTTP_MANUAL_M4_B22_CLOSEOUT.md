# MANUAL M4-B2.2 formal closeout — private non-authorizing runtime verifier

Reviewed implementation: `3def1aa185d2c193f066952954c8394db0b7ad00`.
Original blocked implementation: `04112980feee6f69ec936f5c9eab72b8dbb03425`.
Accepted design checkpoint: `c5c67ce74450d00600700b14f747e29397d987b2`.
Closeout branch: `http-m4b22-verifier-closeout`, created from the exact reviewed
head, ahead/behind0/0, clean worktree. Local only, not a remote-main/PR closeout.
The user explicitly authorized only docs/evidence formal closeout after the
fresh independent PASS. No product/runtime authority follows.

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4-A = PASS
M4-B1 = PASS
M4-B2.1 = PASS
M4-B2.2 independent review = PASS
M4-B2.2 = PASS
checkpoint = M4_B22_PASS
M4-B2.2 formal closeout = COMPLETE_LOCAL_ONLY
M4-B2 = INCOMPLETE_NOT_READY_NOT_PASS
M4-B = INCOMPLETE_NOT_READY_NOT_PASS
M4 = INCOMPLETE_NOT_PASS
M4-B2.3 = NOT_STARTED
M4-B3 = NOT_STARTED
M4-B4 = NOT_STARTED
M4-C = NOT_STARTED
M4-D = NOT_STARTED
M5 = NOT_STARTED
C04 = PASS
C05 = PASS
C07 = PASS
C08 = PASS
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## Exactly what is closed

Only the disconnected, package-private MANUAL runtime verifier/capture and
additive039 clock metadata. It loads original complete signed-prebroadcast
ownership/Facts/contract/artifact and durable controls/auth epoch/policy/
allowlists/active reserved accounting/conserved usage/SUBMISSION lease, compares
expected process identity and bounded trusted time, authenticates the exact
artifact through the unchanged decrypt-only history verifier, observes fixed
read-only chain/header/hash-pinned code, and rechecks inside its metadata writer.
No writer is held across RPC. Clock floor/latch/event and exact audit/alert are
durable, append-only and schema guarded; metadata capture is atomic.

The result remains private, transient VERIFIED_RUNTIME_CANDIDATE_NOT_PERMISSION.
SnapshotOnly/FreshGateRequired remain true, execution/signing/send/replay flags
remain false. Original038 remains immutable DB_EVIDENCE_NOT_PERMISSION with
false external-proof/action flags. DB rows/JSON/history/review PASS do not
become runtime permission. No new operation/expiry/nonce/budget/authorization
window, permit/PASS snapshot, lane release, economic state or public caller.

Scope remains Robinhood4663 / Pons v2 Curve / native quote; BUY/fixed SELL/
already-resolved percentage SELL, APPLICATION_TTL_ONLY, contract_deadline=false.
Submitted/ambiguous recovery and W4-B's send-intent semantics remain unchanged.

## Independent approval and original blockers

The original implementation was BLOCKED on actual-running-image measurement and
unsupported provider/session/monotonic-drift evidence. Those findings are not
retroactively relabelled PASS. The hardening at the exact reviewed head fixes
measurement with a pinned `/proc/self/exe` handle on Linux (no replaceable-path
fallback), and adds direct sticky/latch/restart tests for all three drift types
both before RPC and within the final writer. The reviewer independently
reproduced the old adapter's incorrect acceptance with the same-clean-revision
deleted-pathname-reuse negative control and verified the new adapter rejects.

Explicitly authorized reviewer `m4b22_hardening_fresh_review` approved that head.
The reviewer received author context and prior findings; this was not blind
review. Its report is preserved verbatim in `HTTP_MANUAL_M4_B22_REVIEW.md`.

- Independent focused ordinary PASS:23 top-level tests = all21 B22 mappings
  plus2 compatibility tests; storage0.173s/RPC0.126s/API66.681s.
- Independent new-hardening race3 PASS111.209s; not all B22/repository race.
- Exact old-code negative control EXPECTED_FAIL0.964s, specifically
  `accepted reused deleted-image pathname manifest`, not a new-code failure.
- Independent vet/gofmt/whitespace and180 manifest entries PASS. Six source/doc
  hashes,25 author-log hashes and author focused23/race3/selected6/ordinary601
  mappings/counts/timings matched; prior179 manifest entries preserved.

Author full ordinary/API498.303s and selected6-race287.078s used final product
source but preceded only final image-fixture strengthening; they are not
mislabelled final-test-source or independent results. Author final focused all21
plus2 PASS98.388s and new3 race PASS165.609s cover final test source. Initial
fixture audit-count error, local socket denial and weaker negative control
remain recorded separately. Closeout reruns integrity checks only, not runtime
tests, and invents no new runtime PASS, remote CI or chain outcome.

## Accepted limits

Pinned running-image measurement is Linux-only; unsupported/unavailable paths
fail closed. Trusted fixture manifests/time are not production attestation,
credential distribution or deployment approval. The image probe compiles exact
production source with minimal domain stubs; its child is compiled ordinarily,
not with race instrumentation. The parent test is race-instrumented. Procfs and
intact schema/trusted composition are assumptions, not hostile-root resilience.

Clock fault detection and its later latch commit have a process-crash window;
no precommit durable latch is claimed. Postcommit response cancellation does
not undo committed metadata. Read-only chain observations are not DB/chain
atomicity or quorum. Fixture signatures/decrypt/RPC are not zero activity;
new economic path non-reachability is structural proof, not telemetry.

No production time/manifest/key/RPC composition, HTTP/startup/economic caller,
hardware/power-loss test, remote CI or real chain result is implied. Temporary
review/author logs remain in /tmp, may later disappear, and are not shipped.
This closeout records their current hashes and permanent verbatim conclusions.

## Preservation and next boundary

Only this closeout, verbatim review receipt, current delivery plan, additive
joint-review evidence and SHA256SUMS change. All product code/tests/schema/
migrations001–039/prior evidence/author design and implementation documents
remain byte-identical to the reviewed head. All180 manifest lines remain plus
one new evidence entry181. Old READY/BLOCKED/NOT_PERFORMED and hashes are
historical checkpoints, not silently updated facts.

Future B2.3 needs separate scope/authorization: freshly prepare observations,
run final checks on its own writer, never nest capture or reuse a committed
candidate as authority. This is a next boundary only, not an implementation
plan approval. No B2.3/B3/B4/C/D/M5/W4-D/W5, extra chain/protocol, signer/
submission/broadcast wiring, actual grant, send, live/release or remote action
is performed or authorized by this closeout.
