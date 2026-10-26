# A1 — recovery-only startup implementation

Branch: `http-a1-recovery-only-startup`.
Base: `1181bd1b4bb63190637a6a22bdc3d8cd01a6f599`, from local common code
`80ad4e474c3016f8686dd9d9ca096302ae2cf10a`; neither is a new formal main.
Code: `43c6dc9136d1ac4d70de2576fe2aadcbdbbc8397`.
Final readiness-test delta: `156615c992fc5cd76cb952d91a60d42a909d4512`.
Author ceiling: `A1_DISABLED_COMPOSITION_READY_FOR_REVIEW`.
Implementation independent review: NOT_PERFORMED. No A1/whole-service PASS.

The user continued after the scoped independent A/B/C design PASS and A1
recommendation. This batch implements ONLY the recovery-only candidate scope;
it does not accept a wallet reuse model, C's new timeout/error contract or a
real-action grant. The design review is not implementation approval.

## Selection and compatibility

`RBH_TRADE_STARTUP_PROFILE` is `LEGACY` by default. The existing default
configuration/registration behavior remains in that profile. Explicit
`RECOVERY_ONLY` is accepted only for trade-service, with production mode
DISABLED and live false. Direct Config callers with an empty profile retain
legacy behavior; unknown/noncanonical profiles are rejected.

Recovery-only rejects nonempty user API config, read-query/intake/validation/
admission flags, a validation-policy selection, execution key, dry-run wallet
ID or dry-run address. It does not silently ignore incompatible capabilities.
Existing config still requires the internal IPC credential for the service;
its presence grants no user API or transaction authority.

It loads one configured artifact key/version and the configured read-only RPC.
No keyring, rotation or historical key migration is introduced. An unknown
artifact version or wrong key fails closed when the worker authenticates that
item, with existing durable failure audit/outbox when the store is writable.
No promise that arbitrary legacy versions can be decrypted is made. The
recovery adapter implements the existing ArtifactCipher interface but rejects
Encrypt; it does not falsely claim that interface is structurally decrypt-only.

No new environment values, real keys, RPC targets or credentials are provisioned
by this batch. Tests use temporary files, ephemeral fixture signing and local
controlled JSON-RPC under network-disabled containers.

## Production graph and lifecycle

Run still loads config and owns process signal handling; private runConfigured
is the SAME startup path with a caller context for deterministic tests. It
validates the profile before creating runtime files. Recovery-only startup
does not register wallets, parse execution keys, construct signer/legacy
execution kernel/RPC backend, register `/v1/`, or register legacy dry-run/
prepare endpoints. It retains existing local UDS health/ready/metrics routes.
It is not a new externally reachable HTTP trading service.

```text
Run -> runConfigured -> validated RECOVERY_ONLY branch
  -> SQLite migrate/open
  -> rejecting-Encrypt historical cipher + ReadOnlyRecoveryRPC
  -> existing Pons resolver / RecoveryService / ProductionRecoveryQuerier
  -> existing DISABLED ProductionComposition
     -> durable RECOVERY lease -> ControlledRecoveryWorker
        -> original durable submission/receipt/canonical/reorg owners
```

No MANUAL executor, signer, SubmissionService, transport/broadcaster, send
permit issuer/consumer or nonce writer is passed into this graph. The existing
shared trade Store has mutation capabilities; the boundary is its existing
recovery call paths, not a claim of a read-only database.

The worker lifetime begins immediately after Start, before listener creation.
A small cancellation/join wrapper coordinates the existing lifecycle; it is
not a second recovery state machine. Every return after successful Start joins
the worker BEFORE deferred RPC/DB close, including listener failure. If Start
created a worker/lease but fails its startup audit, a cancelled Run terminates
that lifecycle without beginning a scan. The original failure is returned.
No lease row is deleted/reset/released on error or shutdown: renewal stops,
and takeover follows existing expiry plus a strictly higher epoch. Restart
before expiry can therefore be rejected safely; immediate takeover is NOT
promised.

Normal shutdown uses existing HTTP shutdown plus recovery Wait, and joins the
worker even when HTTP shutdown fails. In-flight read-only calls/scans drain
under existing timeouts/lease fences. Unknown work remains frozen. A stopped
or failed worker cannot revive readiness through this wrapper.

## Readiness and historical independence

Recovery-only requires config/DB/migration/socket/live-disabled/recovery health
gates, NOT the legacy dry-run-configured gate. The legacy profile retains that
gate. Existing recovery metrics remain independent: admission and send are
false, recovery reflects its worker/DB/RPC/RECOVERY-lease lifecycle. Startup
health is not proof every stored artifact will authenticate; a failed item
terminates the loop and readiness becomes false. No hard real-time promise
beyond existing readiness polling is added.

Emergency stop, missing/revoked current authorization, old application TTL or
deployment permission do not become new historical recovery prerequisites.
Recovery still authenticates original source/artifact and validates its own
observations, DB and lease. SUBMISSION lease semantics, C04/C05/C07/C08, MANUAL
no-arm guard, marker ambiguity and retained-wallet exclusion are unchanged.

## Acceptance and honest coverage boundaries

New config tests cover finite profile/default compatibility, incompatible flags
and service/profile mismatch. New app tests exercise actual runConfigured:

- A file-backed database with synthetic imported LEGACY submitted and unknown
  artifacts is CLOSED after fixture setup and REOPENED by the application.
  No authorization exists, control is stopped and original TTL is expired.
  Both items are rediscovered; query methods are observed; original artifact
  bytes/nonce/provenance remain unchanged; unknown lane/reservation stay frozen.
  A second application instance reopens the database under epoch 2 after a
  controlled test-only expiry. No lease deletion/production expiry rewrite.
- Existing metrics show admission/send 0 and recovery 1; target action routes
  are absent. A separate lifecycle test holds an RPC read behind a barrier,
  observes readiness false during drain and after terminal RPC failure, and
  proves resources are not closed before that read is released.
- Wrong key, unknown version, runtime RPC failure, synthetic lease takeover
  and contradictory reverted-receipt identity terminate with audit/outbox and
  unchanged ambiguity freeze/no effects. A forced startup-audit insert failure
  after lease acquisition returns promptly without listener/worker leakage.
- A non-socket listener target is preserved; failed startup stops renewal and
  leaves the lease to expire normally. An inaccessible DB prevents RPC/listen.
  Invalid capability config is rejected before creating runtime directories.
- AST guards allow only loadRecoveryCipher in the recovery-only branch, then
  exclude its legacy else and inspect shared startup/helper for forbidden
  execution symbols. These are narrow lexical capability checks plus runtime
  no-key boot, not an arbitrary whole-program reachability proof.

The app fixture signs TWO ephemeral legacy artifacts during setup. Do NOT
describe that whole test as signer_calls=0. The target recovery-only path
constructs no signer and makes no signing/send/nonce/permit actions; controlled
RPC counts eth_sendRawTransaction=0. Nothing is sent to mainnet. The app
rediscovery test is not new MANUAL source/grant acceptance. Existing MANUAL
query/freeze/canonical/reorg/lease/wallet-rejection tests are reused separately,
without modifying their fixtures, implementation or migration 045.

Exact final-vs-earlier command attribution, timings, test mapping and retained
limitations are in `evidence/http-a1-recovery-only-startup.json`. Full repository
ordinary/race and remote CI are not inferred from focused tests. Source build,
vet/staticcheck and formatting are tracked separately from runtime acceptance.

## Handoff

Only app/config and their new tests changed. No schema, trade/tradeapi behavior,
public request/OpenAPI, CI, B/C branch or existing test is modified. No push,
PR, merge, formal closeout or next-stage implementation is performed.

Next gate: independent implementation review of the exact A1 code/test heads.
After that, separately decide production composition/custody/grant/transport
scope and C's response contract. This milestone neither connects production
POST execution nor solves continuous same-wallet trading.

Production broadcast=NOT_CONNECTED; authorization=NOT_GRANTED;
live=false; release_ready=false.
