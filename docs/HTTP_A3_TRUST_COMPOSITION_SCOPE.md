# A3 — trust contracts and disconnected dependency composition

Design branch: `http-a3-trust-composition-design`.
Base: local A2 closeout `747b0d311334304060ae5b74e6c65609b8c83686`.
Status: `A3_TRUST_COMPOSITION_SCOPE_READY_FOR_REVIEW`.
Implementation: NOT_STARTED. Independent A3 scope review: NOT_PERFORMED.
This executed batch is source inspection, design and baseline boundary checks;
it does not accept its own proposed contracts or implement later batches.
A1/A2 remain scoped PASS, not aggregate production HTTP service PASS.

## Deliverable and explicit non-deliverable

The next proposed implementation builds a private, disconnected dependency
bundle for the existing MANUAL signing and runtime-verification owners, with
controlled local trust providers only. It adds no fresh-admission consumer,
HTTP action route, startup registration, transport, permit or grant. A3 should
not be described as production signing merely because the existing signer
interface accepts a test implementation.

The production trust gap is explicit: an authenticated operator manifest loader,
real signer custody selection and a verifiable bounded production time source
are NOT currently implemented. A checked config digest is integrity, not proof
of operator authority; time.Now plus a configured error is not trusted UTC.
Those adapters require a concrete trust/operations decision before activation.
No local fixture or new constructor is allowed to stand in for that decision.

## Inspected facts that constrain implementation

| Source | Existing behavior | Consequence |
| --- | --- | --- |
| manual_admission_continuation.go / http.go | Private one-shot fresh-COMMIT witness; POST discards it; no production take caller | A3 must not consume it or make historical IDs executable |
| manual_local_signing.go | Private writer accepts manualsign.Signer, Cipher, nonce observer and scalar now(); durable signing-intent marker then final writer fence and synchronous bounded SignExact | Reuse its sole marker/artifact owner; do not insert an alternate signing coordinator |
| manual_runtime_identity.go / manual_runtime_image.go | Clean embedded VCS revision plus SHA256 of /proc/self/exe checked against an expected immutable process manifest | Never use working-tree HEAD, executable pathname, request build fields or copied durable identity as measurement |
| manual_runtime_inputs.go | Runtime profile binds scope, binary/digest, network checkpoint/profile, provider, SUBMISSION holder/epoch and query/freshness/error limits | All expected fields remain immutable for an invocation/process profile; lease takeover cannot silently rewrite expected identity |
| manual_runtime_clock.go | Bounded wall/error/ticks/provider/session; durable floor/latch; no production source | Preserve lower/upper semantics, sticky failure and existing audit owner; do not invent a second clock ledger |
| manual_runtime_verifier.go | Fresh read/RPC preparation followed by same-writer final check; signed/claimed phase projections; returns metadata, not authority | It cannot serve as a pre-sign gate: its input requires an already signed artifact |
| manual_nonce_claim.go | Private observer accepts exact durable facts; claim uses original nonce evidence and retained wallet exclusions | No generic legacy RPCBackend substitution or nonce allocation fallback |
| manual_submission_artifact.go / manualartifact | Separate decryptor interface; full Facts/AAD/exact-artifact verification; read adapter rejects Encrypt | Recovery/verifier must not receive the signer or full signing bundle |
| manual_submission_dependency_test.go | Executor/private exact-byte transport must remain in one owner file; zero product transport implementation | A3 requires NO exception to this executor/transport guard |
| manual_local_signing_test.go / manual_runtime_verifier_test.go | Separate lexical owner restrictions on signing writer and verifier | Only exact new private dependency-bundle references may be considered after scope acceptance; retain deny coverage |
| app/user_trade_api.go / A1 recovery-only startup | Existing user API options stop at admission; RECOVERY_ONLY has no user-action routes or signer requirement | A3 leaves app/config/startup, public Options and A1 entirely unchanged |

## Proposed contract/data model (not yet implementation)

No SQL schema or new durable economic state is proposed. Reuse Operation ->
ExecutionStep -> TransactionAttempt and all existing command/audit owners.
The following are private composition values, never HTTP JSON or grant rows:

1. **Expected deployment:** original manualAdmissionScope plus measured binary
   hash, expected manifest digest, fixed chain4663/PONS_V2_CURVE network profile
   and immutable checkpoint. Principal/wallet/address bind to the original
   durable source; signer.Address must match that wallet, not a request override.
2. **Expected owner:** role SUBMISSION, environment, holder and epoch. RECOVERY
   is never an acceptable substitute. Missing/expired/different durable owner
   denies any future action; A3 does not acquire/renew/take over leases. A new
   holder/epoch requires a newly validated expected profile, not copying a DB
   tuple into a previously approved manifest to make a mismatch disappear.
3. **Time provider:** sample(ctx) -> canonical wall, maximum error, monotonic
   ticks and stable provider/session. The immutable profile sets the allowed
   provider, maximumError, queryTimeout and observation maxAge. Numeric limits
   are operator-policy bounds to be accepted, not invented latency guarantees.
4. **Custody capabilities:** an explicitly supplied synchronous local Signer,
   separate write Cipher for signing and decrypt-only view for verification.
   No credential paths, key material, RPC URL or provider trust flags come from
   HTTP. A3 local fixtures use ephemeral keys only; no production loader,
   env-key fallback, key rotation or KMS/remote-signing protocol is selected.
5. **Read-only observers:** exact MANUAL nonce observer and fixed runtime
   observer are separate capabilities. No sender, allocator, provider retry or
   broad RPCBackend may be embedded in the dependency bundle. A future real
   nonce adapter needs its own method/evidence contract; A3 uses controlled
   observations and does not claim to have implemented that adapter.
6. **Private dependency bundle:** owns immutable verified references/config,
   not an operation queue or permission. Construction performs zero signing,
   nonce/permit/economic writes and zero sends. No public factory can accept a
   historical request ID and start work. Invalid/typed-nil dependencies fail
   closed; cancellation, provider panic and identity drift never select fallback.

The bundle has no submitExact/Submit/ReplayUnknown/SendRawTransaction dependency
and no reference to manualSubmissionExecutor or manualFirstTransport. A3 tests
may explicitly exercise the existing signing writer on an already prepared
synthetic source, but never from production HTTP/A2 or startup. Fixture signing
is counted separately from prohibited real signing and send actions.

## Pre-sign time is a real contract, not a scalar conversion

The runtime verifier already uses [wall-error, wall+error]. Signing's now()
callback does not carry uncertainty or provider/session evidence. Returning
only wall, lower or upper from a new source cannot establish BOTH conservative
causality and conservative expiry checks. Do not label such an adapter safe.

Before a future production signer can be called, the signing owner's existing
final writer must enforce all of the following from fresh bounded samples:

- source/control/claim/observation timestamps <= lower bound;
- upper bound strictly before immutable operation/auth expiry and applicable
  observation freshness deadline; equality is expired;
- nondecreasing wall/ticks, unchanged provider/session, allowed uncertainty,
  durable floor and sticky fault disposition;
- original source/reservation/usage, signer wallet, deployment identity and
  relevant owner still match; emergency stop keeps first control priority.

The same requirements apply immediately before actual SignTransaction, after
it returns and before durable artifact outcome COMMIT. No outside transaction
preflight substitutes for the original writer's authoritative recheck. Existing
intent ownership is preserved: failure/crash after its durable marker cannot
reconstruct another signing opportunity, and old TTL is never extended.

This is a proposed, separately reviewed extension to the signing owner's trust
inputs, NOT an instruction to reuse the signed-artifact verifier at pre-sign or
to alter the marker. If it cannot be added without changing existing writer
semantics, stop the implementation and report the exact conflict. A3-A below
can complete while A3-B's exact input extension is reviewed.

## Proposed implementation sequence and exact file ownership

| Slice | Changes after scope acceptance | Exit evidence |
| --- | --- | --- |
| A3-A | New private manual_trust_composition.go and tests: validated immutable dependency bundle; existing identity/profile validation reused; exact owner-reference guard extension only | Invalid/nil/mutable-input rejection; constructor has zero actions; no HTTP/startup/A2 take caller or executor reference |
| A3-B | New private bounded pre-sign trust helper plus narrowly scoped manual_local_signing.go integration and new tests; existing final writer/SignExact/marker/AAD owner unchanged | Lower/upper boundaries; stop/clock/identity drift before invocation gives zero signing; after invocation failure retains ambiguity and never resigns |
| A3-C | New controlled harness tests exercise the bundle with existing signing/verifier and inherited source/artifact owners; no product app registration | Exact wallet/nonce/Facts/AAD binding; fault/reopen/concurrency; separate dependency graphs; no permits/submission/sends |
| A3 review handoff | docs/additive evidence/checksum, exact source/test hashes and ordinary/selected race/static results | Highest A3_TRUST_COMPOSITION_READY_FOR_REVIEW, not PASS or deployable custody/time proof |

Future file names are proposals, not files created by this design. An exact
new-bundle exception may be required in the signing/verifier reference guards;
replace neither guard with a blanket allowlist or delete its negative checks.
Add a guard that prevents the bundle escaping to app/cmd/public HTTP/recovery.
A3 makes no change to the executor/transport dependency guard or A2's no-take
guard. Existing marker/TTL/schema/C07/recovery tests remain authoritative.

## Acceptance matrix for the proposed implementation

| Boundary | Required positive and discriminating negative evidence |
| --- | --- |
| Manifest/measurement | Real measured clean running-image positive fixture; copied build config, dirty/missing VCS, wrong binary, pathname replacement, wrong deployment, mutable expected input rejected; test fake identify results are not production proof |
| Owner/network | Wrong role/environment/holder/epoch, chain/profile/checkpoint and unavailable/contradictory RPC reject; no repair/rebinding/default provider |
| Clock before sign | wall/error/tick/provider/session changes, reversal/floor/latch, stale/malformed uncertainty, expiry -1ns/equality/+1ns; no sign on rejection; no plain time.Now trust fallback |
| Writer commit windows | Barriers at final source/time gate and before signer; stop/revoke/takeover cannot commit inside held writer; changes committed before acquisition are rejected |
| Signer/cipher | Wrong wallet/chain/transaction or cipher version/AAD failure denies; one synchronous call only; timeout output rejected without detached goroutine/retry; no raw/key leakage |
| Fault/restart | Before/after intent COMMIT, before/after signing, artifact insert/COMMIT failures, actual SQLite close/reopen; original ambiguity/claim retained, no automatic resign or new nonce |
| Construction/integration | Constructor/cache/duplicate/history/query/recovery add zero signing; A2 ownership remains unconsumed by product code; no permit, submission, transport, broad RPC or startup registration |
| Recovery | Existing A1 independent healthy/failure/drain checks remain valid without signing availability; recovery graph contains no bundle/signer/broadcaster |
| Attribution | Ephemeral signing and controlled RPC counts distinct from zero real-key/mainnet sends; ordinary/selected race exact heads and fault results retained; lexical checks not whole-program proof |

## Continuous execution and final acceptance routing

Once this exact scope is accepted, A3-A -> A3-B -> A3-C is one implementation
batch: inspect -> implement -> focused tests -> fix in-scope failures -> selected
race/regression/static checks -> additive evidence/checksum -> local commits.
Do not ask after each test, fix or local commit; do not start expensive whole
suites for docs-only updates. Track started checks to completion. Independent
review and formal closeout still require their applicable authority; a design
or author test result is not independent PASS.

The later joint local-service acceptance still requires A4 operator authority,
A5 exact-byte transport/default-disabled HTTP action composition and C response
contract integration. Their schema/authority/transport decisions are not accepted
by the user executing this design. B same-wallet reuse remains a separate model;
first-order success is not repeat-trading acceptance. Batch unresolved material
decisions together rather than interrupt for implementation details.

## Decisions retained for the scope/real-deployment gates

- Accept/reject the exact A3-B pre-sign trust-input extension and narrow private
  reference exceptions. No code can be safely described as the final connection
  merely by constructing today's interfaces.
- Select the real signer custody and authenticated manifest source before any
  real-key loader/deployment; this design selects neither a filesystem key,
  environment key, KMS nor external remote-signing protocol.
- Select a real bounded-time provider with documented trust/failure model
  before production activation. A3 controlled providers are not NTP/hardware
  attestation. No host time service or kernel clock configuration is changed.

This batch modifies only this scope document, additive evidence and checksum.
No implementation branch, schema, key, time provider, listener, authorization,
chain call or later action is provisioned. No push/PR/merge or unattended job.
Production broadcast=NOT_CONNECTED; controlled authorization=NOT_GRANTED;
live=false; release_ready=false. A3 scope/implementation are NOT PASS.
