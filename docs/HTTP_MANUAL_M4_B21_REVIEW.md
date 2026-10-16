# M4-B2.1 hardening — final independent read-only review

M4-B2.1 independent review conclusion = PASS

Exact reviewed head: eec705473517b591d5502f556685ee2ea9318f58
Branch: http-m4b21-gate-hardening
Hardening base: e23be87b012b0b1ed391d93b5c7e7363b098d527
Original B21 base: 59358daaf6ac686bbf01eb032f3a7f877468098a
Project: /root/http/rbh-bot

This is independent approval of only the disconnected B21 DB-input-evidence
contract and its narrow hardening. It is NOT a formal milestone closeout, whole
B2/B/M4 PASS, permission grant or approval to enter B2.2. Reviewer had author
context and initial finding history; this is not a blind review. Repository and
Git remained untouched. No remote operation, key/grant or real-chain call occurred.

## Original blocker resolution

Initial report remains unchanged:
/tmp/rbh-m4b21-independent-cYr1ZQ/review.md
SHA256 14e0cba434b62821bf702a800537dcf71f6577ee87d3da12143be99b046f67db

That report BLOCKED the original head because an injected witness FAIL retained
the parent row and scope, allowing later exact audit/alert insertion, scope close
and COMMIT. The new038 code fixes that provenance gap rather than relabeling the
old result or silently accepting same-transaction repair:

- Before parent insertion, source guard validates exact current durable source,
  rejects existing parent/scope/audit/alert, then stages scope with exact time and
  witnesses. Audit/alert guards bind the scope's original operation/attempt/auth.
- Audit/alert FAIL now leaves parent=0. Manual completion of provisional witnesses
  is possible and honestly disclosed, but parent insertion cannot adopt existing
  scope/witnesses; scope closure requires the missing parent.
- Deferred parent/empty-target FKs reject COMMIT of the incomplete scope, including
  after manual witness completion. ROLLBACK restores the state, after which a fresh
  INSERT can succeed normally.
- AFTER actual parent insertion closes exact scope and checks original full
  foundation completeness. Original001–037 and downstream guard domains remain
  unchanged. No runtime writer or other capability was introduced.

The permanent new regression independently passed all16 audit/alert x four conflict
modes x with/without post-failure control-drift scenarios. There is no DDL after
the injected fault until rollback. No remaining blocker was found in this scope.

## Independent final-head execution

Offline cached environment:
PATH=/tmp/rbh-go-toolchain/bin:$PATH
GOCACHE=/tmp/rbh-go-build-cache GOMODCACHE=/tmp/rbh-go-mod-cache GOPROXY=off

1. All11 B21 focused ordinary plus storage3:

   go test -p 1 ./internal/storage ./internal/tradeapi \
     -run '^(TestManualGateEvidence|TestManualSubmissionFoundation|TestOpenMigrateAndValidateEveryOwner$|TestHTTPM4B21)' \
     -count=1 -v -timeout=5m

   PASS, storage0.555s/API63.898s; new16-subcase test18.46s. Log focused.log.
   This includes original source/time/drift/identity/concurrency/history/no-runtime
   guards and old foundation/owner migration compatibility tests.

2. Two fresh adversarial reviewer overlay groups under race on this exact head:

   go test -race -overlay /tmp/rbh-m4b21-preflight-o77rKI/overlay.json \
     ./internal/tradeapi -run '^TestIndependentB21Hardening' \
     -count=1 -v -timeout=3m

   PASS52.348s (26.69s/24.44s), log probes-race.log. Probe source/overlay are outside
   the repository. First group manually seeds exact scope plus provisional witnesses,
   then tries parent UPSERT DO NOTHING/DO UPDATE, scope deletion and COMMIT: all reject.
   Second group uses a multirow evidence INSERT: first row reaches complete, second
   alert FAIL leaves parent1/scope1 provisional; COMMIT rejects and ROLLBACK succeeds.
   Preflight ordinary execution of those same groups had PASS2.914s at identical
   source/test hashes. No whole-repository or all11 independent race claim follows.

3. Independently checked final committed6-path diff, source4/document hashes,
   author10 log hashes,12 named mappings against full ordinary PASS events,
   manifest174 prior lines preserved plus one additive entry, original BLOCKED
   report hash unchanged. check_package.py PASS175. git diff --check PASS.
   Old001–037/product Go/old tests/old evidence outside the exact new B21 test
   path are not changed by the hardening. Last HEAD/status check remained exact
   eec705473517b591d5502f556685ee2ea9318f58 and clean.

## Author validation attribution, including timeout

Author full ordinary PASS and build/vet/staticcheck/offline results were verified
by log hashes and referenced mappings, not independently rerun across the repository.
Full ordinary app0.249s/storage21.654s/trade242.713s/API540.760s.

The author's attempted combined all11 B21 race command TIMEOUT20m, exit1. This
is not PASS. JSONL confirms storage3 PASS39.177s, original10 B21 top-level PASS,
new test started10 subcases with zero new-subcase/top-level PASS events. A separate
new16-subcase race command PASS340.040s (test338.930s), confirmed all16 subcase and
top-level PASS events. Source hashes are the same. Docs/evidence explicitly preserve
the timeout and do not claim one successful all11/full-repository race. The timeout
does not alone establish a correctness failure; isolated coverage and final focused
independent runs complete successfully. No full-repository race PASS is inferred.

Original author B21 evidence/doc hashes refer to their historical original snapshot,
not current038/test. The new additive evidence and latest plan clearly supersede
those initial claims without rewriting old evidence or the initial BLOCKED report.

## Remaining accepted limits and boundaries

- B21 is durable relational DB evidence, not a current clock/binary/RPC/crypto proof.
  checked_at can be backdated within source bounds; lease metadata is not process
  ownership. All action/proof flags stay false, fresh gates still required.
- Historical metadata is not new action authorization after stop/revoke/TTL/lease
  changes. No permit/PASS snapshot/sign/send/nonce/accounting path is added.
- Intact FK_ON schema and the specifically tested audit/alert fault modes only;
  no hostile arbitrary new root AFTER triggers, dropped guards, FK_OFF, power-loss
  safety or existing experimental038 runtime-database repair is claimed.
- A future writer must still rollback explicit transaction errors. It must not
  treat provisional witnesses or table presence as valid complete evidence.
- Prior037 accepted limitations remain unchanged. Synthetic ARMED/offline signature
  fixtures are not production arming/grants. No other chain/protocol is entered.

No additional blocking or nonblocking finding beyond those explicit existing
limits was identified. Independent PASS may be recorded as a separate review receipt;
the maximum delivery checkpoint is still M4_B21_READY_FOR_REVIEW until separately
authorized formal closeout. Whole B2/B/M4 incomplete; B2.2 not entered.

production_broadcast=NOT_CONNECTED
controlled_canary_authorization=NOT_GRANTED
live=false
release_ready=false

## Temporary artifact hashes

- focused.log:
  3a396df9260e405ce05e6d30f33083b39300808aa09fe23e28bf63d26c811426
- probes-race.log:
  8c8f1370ac549c8eddc49cffbc7b25a4fe308948b8c1dc61c1ea3301648ec455
- /tmp/rbh-m4b21-preflight-o77rKI/probe_test.go:
  4aac04beccd40f33728fc4674520e024fa0a21329917dbbc3e26cf1dbe7729be
- /tmp/rbh-m4b21-preflight-o77rKI/overlay.json:
  2eaa087651779a6f66d7207ba01f2ce4cb16648d4b11cedf2b762b2ba6c48f09

Temporary files are local review artifacts, not shipped production evidence or
permanent regressions; the new16-case regression is in the reviewed repository.
