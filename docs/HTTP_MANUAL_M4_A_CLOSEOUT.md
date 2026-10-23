# MANUAL M4-A formal closeout — private read-only metadata only

Reviewed implementation: `32a5fd6d0855b99a2151bedbc87c3adfe2f75594`.
Implementation base: `1b6b528fea77b57f94f8bd1b964a425e17192ff5`.
Closeout branch: `http-m4a-artifact-closeout`, created from the exact reviewed
head with ahead/behind 0/0 and a clean worktree. These are local commits, not
claims about remote main or merged PRs.

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4 scope = FROZEN
M4-A independent review = PASS
M4-A = PASS
M4-A formal closeout = COMPLETE_LOCAL_ONLY
M4 = INCOMPLETE_NOT_PASS
M4-B = NOT_STARTED
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

## Accepted contract

PASS covers only the private SELECT-only source-aware MANUAL artifact metadata
reader. Exact durable MANUAL ownership selects the format; full-byte legacy
source collisions and unsupported phases fail closed before decrypt. Original
M3 command/binding/nonce/accounting/audit/phase and full-Facts AAD verification
are reused unchanged on one read snapshot. No legacy AAD/key fallback, repair,
new nonce/expiry/epoch/budget, new state machine or financial action is added.

Only complete SIGNED_PREBROADCAST is supported. The result contains detached
Facts and authenticated hashes/key/completion metadata, not raw transaction,
ciphertext, encryption nonce, marker owner or permit. SnapshotOnly and
FreshGateRequired are true; execution/signing/send/replay permissions are false.
Historical identity after stop/revoke/freeze is not current TTL/auth permission.
Concurrent changes may make a coherent read snapshot stale. Future actions must
perform their own fresh reviewed gates; this PASS does not authorize them.

Scope remains Robinhood4663 / Pons v2 Curve / native quote, BUY/fixed SELL/
already-resolved percentage SELL. APPLICATION_TTL_ONLY; contract_deadline=false.
Original amount/minimum/calldata/fee including zero tip/nonce/policy/quote block
and hash/absolute expiry/authorization/deployment remain exact. No signer,
observer, encryptor or transport capability is granted to the reader. The
required Encrypt adapter stub always fails and is not invoked by the path.

## Independent review and attribution

Separately authorized reviewer `http_m4a_independent_review` inspected the exact
clean head. It inherited the author conversation and handoff, so this is not
claimed to be a blind review. It made no repository changes and found no
actionable blocker within M4-A.

- All nine M4-A ordinary tests PASS, 8.568s.
- All nine M4-A focused race tests PASS, 194.272s.
- Two reviewer-only overlay tests / seven cases PASS, 3.162s: nonce 0 / 2^63 /
  2^64-2, detached Facts and false permissions, retained BLOB source operation,
  missing artifact, receipt and position phase rejection. Disposable corruption
  fixtures restore original guards/FK before the tested read.
- Reviewed manifest170, all169 prior lines, seven source/document hashes, nine
  named test mappings and preservation of prior files/001–036 PASS.
- Author full ordinary and selected-race JSONL hashes and their nine M4-A PASS
  events were crosschecked; they are not relabeled independent full-suite runs.

Exact commands, report/probe/overlay hashes and limits are recorded in additive
`rbh-validation-package/evidence/http-m4a-joint-review.json`. The independent
report is `/tmp/rbh-m4a-independent-GciIhK/review.md`, SHA256
`7d018b588dc3f7d6d84396a3acbd3d1429e7b5ca00361b92c1a20aab6c5f7d44`.
Temporary report/probes are not committed permanent tests and may disappear
after cleanup/restart; raw reviewer test logs are not separately archived in
that directory. This closeout records their attributed results, not new runs.

## Limits and preservation

No independent full-repository ordinary/race, build/vet/staticcheck, remote CI,
power-loss or real-chain claim is made. Author checks remain attributed to the
unchanged author evidence. Trusted synchronous crypto and intact reviewed
FK-ON schema are assumed; no forced interruption, Go/library secure erasure,
hostile-schema security or hardware durability proof is claimed. Positive
fixtures sign offline; zero-action claims apply to the reader, not all setup.

Only this document, current delivery plan, additive joint-review evidence and
checksum change. Product, tests, schema/migrations and all previous evidence/
scope/implementation docs remain byte-identical to the reviewed commit. Their
READY/NOT_PERFORMED records and hashes remain historical snapshots; the delivery
plan hash in author evidence refers to the reviewed head, not the closeout head.
This docs-only slice validates attribution, hashes, preservation, manifest and
diff boundaries, not another runtime test run.

No HTTP/config/startup caller, key/RPC/signing/submission/replay/send wiring,
permit, broadcaster, real authorization, mainnet, live or release change is
made. No rebuild/resign/replacement/fee bump/new nonce/release or second economic
action follows. No M4-B/C/D/M5, W4-D/W5 or other chain/protocol is entered.

Next candidate is separately authorized M4-B detailed additive contract design
and implementation under the existing freeze. M4-A PASS alone does not open
no-arm/no-permit/no-PASS guards or authorize any later migration/runtime action.
