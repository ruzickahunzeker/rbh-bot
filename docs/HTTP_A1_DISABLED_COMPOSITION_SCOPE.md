# Proposed A1 — recovery-only startup, MANUAL actions unreachable

Status: `A1_SCOPE_READY_FOR_ACCEPTANCE`, not accepted or implemented.
Code source: local `80ad4e474c3016f8686dd9d9ca096302ae2cf10a`; A design
`3d527a970bf736142d892a4dfa4d8e39414e9161`. No implementation branch is created
by this scope. Gate 1 review/acceptance remains required before production edits.

## Why this is the first bounded implementation candidate

Current app.Run requires execution private key, configured wallet/from and
artifact keys, then constructs a legacy prepare-execution kernel before the
independent recovery worker. The worker itself needs only durable history,
decryption, read-only RPC, resolver and RECOVERY lease. A1 separates those
startup capabilities without connecting the private MANUAL executor to HTTP.
It is useful lifecycle work, not a completed production trading endpoint.

## Proposed exact boundary

Add an explicitly selected **recovery-only startup profile**, alongside the
existing legacy profile. Profile/config names are not reserved or implemented
here. Preserve the current default profile and its existing opt-in/default-off
user API configuration; do not silently remove a legacy authenticated endpoint
or make an existing deployment unexpectedly instantiate another capability.
Canary production mode remains DISABLED, live=false. Recovery-only is not
CONTROLLED_CANARY mode, new action authority or a flag that can enable send.

In the explicit recovery-only profile:

1. Open/migrate the existing SQLite store; require supported schema and durable
   wallet/artifact provenance. Do not invent/register missing wallet history
   from a request or rewrite it to fit startup config.
2. Load only historical artifact decryption/key versions and read-only RPC;
   validate chain/network health without real signing or sends. The legacy
   cipher's Encrypt method is not called. Wrong/missing keys fail closed for
   recovery; absence of a signer secret alone is not a recovery failure.
3. Construct existing RecoveryService/ProductionRecoveryQuerier/resolver and
   ProductionComposition; acquire existing independent RECOVERY lease and
   rediscover submitted/unknown durable work. No new queue or state machine.
4. Keep the existing health/metrics policy on the local UDS, but register NO
   user `/v1/` API or legacy dry-run/prepare handlers in this first recovery-only
   profile. Reject a nonempty user API config file or enabled user API flags in
   this profile; do not silently ignore incompatible configuration. The legacy
   profile retains the existing authenticated user API unchanged. Merely using
   default API Options would not make a read-only adapter: even a rejected POST
   can write an intake rejection. A separate future authenticated GET-only
   adapter needs its own contract; A1 creates no new public listener/endpoints.
5. Maintain independent recovery readiness, false during drain/terminal failure;
   admission/send remain false. The legacy global readiness requirement
   pons_curve_dry_run_configured must not falsely gate a recovery-only profile;
   define required health gates per profile without weakening the legacy one.
6. Reuse existing shutdown/drain ordering and audited lease failure behavior;
   keep DB/RPC alive until dependents stop. Unfinished work remains durable and
   rediscoverable. Stop/revoke/expiry does not automatically release unknown.

Not instantiating a signer for MANUAL is insufficient if the recovery-only
process still constructs the legacy signer/kernel first. Test the actual
profile startup path, not only the worker constructor. A1 must also preserve
legacy behavior tests so capability separation is not an unrelated API removal.

## Allowed change surface after explicit acceptance

- `internal/config/config.go` and tests: finite startup-profile validation,
  unchanged existing default, explicit incompatible-flag rejection. No new
  authorization/mode permissiveness, secret output or live toggle.
- `internal/app/run.go`, `user_trade_api.go` and tests: separate recovery-only
  graph from optional legacy execution/signing construction, profile-aware
  registration/readiness, unchanged authenticated local transport.
- Small private app composition helpers and focused lifecycle tests if needed.
  Reuse existing trade recovery/lifecycle code; change it only for a concrete
  necessary A1 defect, not as an opportunity to redesign worker semantics.
- Documentation/evidence/checksum for exact code and validation heads. Pin
  fixture setup versus incremental action counters honestly.

No references to the private MANUAL executor/transport from new production
files, no typed execution handoff implementation, no new permit issuance,
signing/nonce/admission writer, new HTTP wait or error mapping, production send
adapter, authorization protocol, migration or wallet-reuse changes. Existing
lexical/private-dependency/no-arm tests must keep passing unchanged; do not
relax them merely to call a disabled wrapper a production composer.

## Required acceptance before A1 review

| Scenario | Required proof |
| --- | --- |
| Default legacy configuration | Existing profile/default-off API behavior and rejection tests preserved |
| Recovery-only, no execution key/no authorization | Actual application graph boots on controlled local DB/RPC with signer/kernel constructors never called |
| Incompatible intake/admission/prepare configuration | Startup rejects, no new action capability instantiated |
| Recovery dependency inspection | Worker/service/querier contain no signer, SubmissionService, MANUAL executor, broadcaster, permit issuer or nonce allocator |
| Submitted and unknown after SQLite reopen | Actual startup rediscovery, query/reconcile and retained exact identities; no memory-only fixture queue |
| Stop/revoke/TTL/deployment permission unavailable | Existing historical work recovers if its own integrity/DB/RPC/lease are valid; admission/send remain false |
| Bad key/DB/RPC/lease/unverifiable observation | Not ready plus existing durable audit/alert where store is writable; no fabricated success if audit storage itself fails |
| Drain/restart/lease takeover | No lost durable work, no stale-owner mutation, recovery stays false during drain; unknown remains frozen |
| No action capabilities | No signer instantiation/calls, legacy prepare route, send/permit/nonce writer calls or mainnet sends in the recovery-only target path |

Use actual file-backed SQLite and controlled local JSON-RPC. Reuse already
reviewed canonical/reorg/idempotency tests; add only evidence specific to the
application-profile boundary. No new receipt-result owner or automatic replay.
Recovery-only profile support must not be presented as production POST execution
or continuous trading completion.

Highest future author status: `A1_DISABLED_COMPOSITION_READY_FOR_REVIEW`, not
PASS, joint acceptance or whole-service readiness. Production broadcast remains
NOT_CONNECTED, authorization NOT_GRANTED, live=false, release_ready=false.
