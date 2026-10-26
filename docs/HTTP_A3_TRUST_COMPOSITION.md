# A3 — private trust composition and bounded pre-sign gates

Branch: `http-a3-trust-composition`.
Base/accepted scope: `37926899a0aca4c56e4aa8aef5a893d438c25335`.
Initial implementation: `7f34a901dd3fab64353b7c8fa821974d40241750`.
Final code and tests: `d9ca5639f04bb45a8bc64fa62b4c0c130a5f7123`.
Author ceiling: `A3_TRUST_COMPOSITION_READY_FOR_REVIEW`, not A3 PASS.
Independent implementation review: NOT_PERFORMED. No formal closeout.

The user accepted the independently scope-reviewed original A3 contract plus
clock transaction addendum at the base above. The scope review was PASS with
zero blockers; it is not code review approval. Historical design/evidence and
A1/A2 closeout records remain unchanged. New evidence carries current status.

## Implemented A3-A/B/C boundary

The new private dependency bundle copies expected wallet/principal and runtime
profile values and holds trusted synchronous local interfaces. Construction
rejects missing/typed-nil dependencies, invalid profile, signer-wallet mismatch
and inconsistent cipher/decryptor versions. It does not bootstrap a clock,
write a row, call observers, sign or grant authority. The health flag is shared
through a pointer across copies; mutable expected profile copies are detached.
The underlying interface implementations remain trusted primitives: this is
not deep-copying arbitrary service implementations or authenticating an operator.

Only the private bundle creates the new trusted-local signing composition.
The original signing writer is still the sole owner of intent, final writer,
one synchronous SignExact call and artifact outcome. Its old scalar-clock
controlled fixtures remain compatible; the new bundle always selects bounded
trust and never falls back to scalar now(). Neither path is newly connected
to HTTP, A2 ownership consumption, startup or a production sender.

The pre-sign helper reuses original durable Facts/claims, scope, gate identity,
policy, allowlists, authorization and SUBMISSION lease. It does not ask the
already-signed-artifact verifier to authorize pre-sign. Runtime and nonce
observations are read-only controlled dependencies outside the writer, with
network/profile/checkpoint/quote/code identities revalidated under the final
writer. No RPC is called while holding that writer. Evidence freshness checks
both bounded wall age and per-invocation monotonic elapsed time.

Each trust read checks stop first, shared health, stable key version and measured
process identity, then samples bounded time AFTER image measurement. Both bounds
are enforced: lower for source/control/claim/observation causality, upper strictly
before operation/auth/lease expiry and the nonce freshness deadline. Provider,
session, ticks, uncertainty, latest durable floor/latch and source identities
are checked afresh. Original signing checks are retained as additional gates.

The final artifact-phase check uses the original phase-aware historical verifier
and exact original Facts hash, not an initial-phase source after it has become
signed. It samples time again AFTER that authentication/decryption. A controlled
decryptor advancing time to expiry proves a pre-crypto timestamp cannot pass
the final artifact boundary. No TTL extension, sleep, resign or new nonce.

## Clock transactions, failure and restart

The initial read does not advance the floor. The original intent transaction
adds one healthy migration039 event with its original exact witnesses. The
original artifact transaction adds a second healthy event after its final
checks. In each case economic command, clock event, audit and outbox commit
together or roll back. Migration039 and all prior schema are byte-identical.
The phase gap re-reads latest complete generation; another legitimate healthy
metadata writer can advance it without creating a second signing opportunity.

On clock failure, the invocation and shared action bundle fail closed.
Cleanup order is original phase rollback -> connection release -> separate
existing latch transaction. A checkpoint proves the one-connection pool can
be reacquired and no fault exists BEFORE post-release latch persistence.
Concurrent persistence classifies a verified complete latest fault without
duplicating an event or inventing a successful write. Missing/corrupt history,
DB/audit/cleanup errors are surfaced, never described as a durable latch.
Cleanup release-failure acceptance uses a controlled injected checkpoint error,
not a claim a real driver Close failure was induced. A separate test closes
the actual DB after release, then verifies no durable fault appeared on reopen.

Cancellation, policy stop/revoke/expiry and signer/cipher rejection alone are
not clock corruption. Historical observation remains action-free even if the
bundle is unhealthy. The independent recovery graph receives neither this
bundle nor signer; the verifier receives only original narrow dependencies.

Actual child-process exit77 covers marker committed, fault detected before
latch, and latch committed. Only committed intent/floor/fault survive reopen.
No crash-atomic/global pre-COMMIT fault claim is made. The test child uses a
synthetic identity and a signer that panics if invoked; it reconstructs fixture
parameters only and is NOT a production manifest loader or custody adapter.
These are software process exits, not power-loss/storage durability proof.

## Test mapping and honest limits

Sixteen new top-level tests reside in manual_trust_composition_test.go:

| Test suffix after TestA3 | Main evidence |
| --- | --- |
| PrivateBundleConstructionAndCopies | Constructor zero writes/actions, typed nil/invalid/mismatched dependencies, copied profile and shared unhealthy flag |
| BoundedSigningTwoCommitsAndHistoryReopen | One fixture signature, original artifact/nonce, two atomic clock advances; real reopen/history under stop/revoke adds no action |
| BundleVerifierIsDecryptOnlyAndFresh | Existing verifier consumes exact bundle-produced artifact, requires fresh observations twice, adds no signing/economic action |
| TrustGateDenialsAndNanosecondBounds | Stop first, auth/owner/identity/provider/time failures; upper expiry -1ns/equality/+1ns and lower causality |
| ClockFaultRollbackReleaseLatchNoResign | Six pre/post-marker/sign fault windows, pool-reacquisition barrier, durable fault and retained ambiguity, reopen no resign |
| HealthyClockWitnessAndCommitFaultsAtomic | Audit/outbox failures and actual deferred-FK COMMIT failures in BOTH transactions, no half artifact/intent/floor |
| GenerationGapAndPolicyDenialNotClockFault | Concurrent generation advance, stop/revoke/takeover, cancel and controlled pre-final-BEGIN error without false clock fault |
| FailedLatchAndConcurrentAlreadyLatched | Fault audit failure, injected release-cleanup failure, two handles racing one complete fault |
| PositiveUncertaintyAndStickyBundleFailure | Positive nonzero uncertainty succeeds with advancing controlled samples; shared unhealthy bundle denies without sampling |
| PrivateGraphAndRecoveryIsolation | Exact private owner files, no exported registration/consumer/send dependencies or detached work; narrow verifier fields |
| FinalWriterBarrierAndTwoHandleSigning | Actual competing control SQL returns SQLITE_BUSY while final writer held; after COMMIT control write succeeds; duplicate observes history with one signature |
| RuntimeObservationAndIdentityDriftBeforeSign | Wrong/unavailable runtime identities, process drift before/after signer, ticks/session changes |
| ActualProcessClockCrashWindows | Three exit77/reopen windows; committed floor/marker/fault only, no automatic signing |
| SignerCipherFailureRetainsOriginalMarker | Error/panic/wrong nonce/timeout/cipher failure: one original fixture invocation, marker retained, never re-sign |
| DatabaseUnavailableDuringFaultPersistence | Actual DB close after release; unhealthy flag/error retained, no false durable latch on reopen |
| FinalClockSampleAfterArtifactAuthentication | Time crosses expiry during decrypt; artifact/final healthy event roll back, original signature ambiguity retained |

Two existing test guards get only the exact accepted new bundle-file exception;
all their other checks remain, plus a new whole-tree lexical restriction on the
bundle/pre-sign/health symbols. The executor/transport and A2 no-take guards
remain unchanged. Lexical restrictions are not a formal arbitrary call-graph
proof. The constructor, duplicate/history and recovery targets add zero signing;
positive/fault signing tests DO make ephemeral local fixture signatures. The
whole suite must not be advertised as signer_calls=0.

The existing clean running-image replacement positive/negative probe is rerun
unchanged. Positive bundle tests otherwise use explicitly synthetic expected
identity/time/observations. No real manifest authentication, custody selection,
bounded production time source or live nonce/RPC adapter is implemented here.
No new production-key or mainnet action is authorized by local acceptance.

Exact commands/heads/results, selected-race coverage (not all sixteen), earlier
failures and final-code attribution belong in the additive implementation
evidence. All-repository build/vet/staticcheck are distinguished from targeted
runtime tests; no full-repository runtime/race or remote CI claim is inferred.

## Handoff

No migrations001–045, public Options/routes/JSON/OpenAPI, app/config/startup,
trade executor/recovery, A2 consumer, existing C04/C05/C07/C08 implementation,
no-arm, wallet model, CI or B/C worktree changes. No permit/submission/raw-send
path is added. A1/A2 remain PASS. A3 scope PASS is not this implementation PASS.

Next gate is separately authorized independent implementation review of the
exact final head. No self-issued PASS/closeout, A4/A5, push/PR/merge, real grant,
key provisioning, mainnet send, replay/rebuild/replacement/fee bump or live.
Production broadcast=NOT_CONNECTED; authorization=NOT_GRANTED;
live=false; release_ready=false. Production HTTP trading is still unconnected.
