# EVM Bot evolution: E0 scope freeze

Status: `SCOPE_FROZEN`; implementation slices require independent review, not author PASS.
Implementation base: `main@7b278c4ffd48fe75c8f20d25185f0743222684b8`.
The separately pushed `trade-api-submission-intake` branch is not landed in this base.
E1 does not incorporate or claim that pending API implementation.

## Product and execution ownership

Evolve this repository into **evm-bot**, containing Robinhood first, BSC second,
and optional Base later. Solana is explicitly out of scope. One repository and API
contract do not mean one shared nonce pool, database or runtime authorization.

Target flow:

```text
TG Bot / Copy Trading
  -> authenticated evm-bot API (no signer/broadcaster)
     -> explicit chain-scoped execution owner
        -> protocol adapter -> existing execution model -> submission/recovery
```

The eventual API authenticates caller and wallet permissions, persists request identity
and routes only to a configured chain worker. Each worker is permanently bound to one
environment/chain and owns its execution DB, nonce lanes, reservations and leases.
Each chain wallet has one execution owner for both manual trades and copy trading;
Node/Go processes must not independently allocate nonce or send for the same chain wallet.
The current service is not yet that generic gateway.

Shared execution concepts remain `Operation -> ExecutionStep -> TransactionAttempt`,
followed by submission observations and canonical effects. Do not create protocol-specific
parallel execution state machines. Share audited interfaces incrementally, not a bulk rewrite.

## Frozen invariants

- Routing chain ID is a selector, not a caller security attestation. Server registry,
  wallet permission, durable policy and runtime authorization must agree.
- Bind environment, chain, wallet, protocol/deployment identity, policy version,
  quote block/hash, immutable expiry and exact artifact. Unknown/unready chains or protocols
  fail closed; no chain/protocol fallback.
- Idempotency identity includes chain/wallet scope. An existing request cannot be changed
  to another chain/protocol/expiry to create a second action. The future gateway needs a
  durable caller-key binding before dispatch, not just per-chain deduplication.
- Keep per-chain SQLite execution stores with immutable DB identity and a single write owner.
  The current Robinhood schema and migration journal remain untouched. A future chain
  uses separately reviewed schema identity constraints, not arbitrary chain rows in RBH DB.
- Absolute `expires_at` is created once. Duplicate/retry/restart cannot extend it.
  Contract deadline capability is protocol-specific and verified, never inferred from EVM.
- Reuse the conservative send-intent ambiguity boundary. `broadcast_unknown` does not mean
  send proven; it means absence of send cannot be durably proven. Preserve freeze and
  query first; no automatic second economic action.
- No automatic rebuild/resign/replacement/fee bump/new nonce. Future explicit replay uses
  fresh authorization/permit and identical raw bytes only, never a first-send permit.
- Keep admission/send/recovery readiness independent per chain. Stop/revoke/TTL prevents
  new signing/sending, not existing receipt/canonical/reorg recovery.
- Recovery never obtains signer, broadcaster, submission or nonce-allocation dependency.
- Static chain registry is not durable gate attestation, health, readiness or authorization.
- Existing C04/C05/C07/C08 and W1/W2/W3/W4-B conclusions retain their reviewed RBH scope.
  They are not inherited by BSC/Base or by an extracted common engine without fresh evidence.
- W4-C remains pending independent review at this base. EVM evolution does not close it,
  authorize W4-D/W5, connect broadcast or grant live/canary authorization.

## API and intent evolution (not E1 implementation)

Future API capabilities/quotes/operations/status/events/submission requests must expose
honest per-chain/per-protocol maturity. Manual and copy intent sources are different
validated models; manual callers may not invent feed events to pass current copy-only checks.
Integers are canonical decimal strings; no uploaded private keys, nonce, arbitrary calldata,
RPC endpoints or self-reported gate PASS. The initial new API remains disabled intake/query
until execution wiring is separately admitted. Public HTTP requires its own reviewed
authentication/TLS/authorization boundary; existing internal UDS/HMAC is not public user auth.

## Slices and approval gates

| Slice | Scope | Maximum result |
| --- | --- | --- |
| E0 | This scope/ownership/compatibility freeze | `SCOPE_FROZEN` |
| E1 | Product naming, compatible configuration, static chain registry, startup alias | `E1_READY_FOR_REVIEW` |
| E2 | Authenticated generic API contract/intake/query/chain routing | disabled, independently reviewed |
| E3 | RBH compatibility adapter and incremental shared interfaces | old DB/artifact/recovery compatibility proven |
| E4 | BSC Four/Pancake V2 quote/build/validate/simulation | controlled local harness only |
| F0/F1 | Official Flap materials, lifecycle, quote/build/simulation/receipt adapter | `FLAP_READY_FOR_REVIEW` |
| E5 | BSC durable execution/submission/recovery lifecycle | default disabled; independent review |
| Later | Base adapters and chain-specific admission | disabled until separately approved |

Flap research may proceed independently of API infrastructure. It requires official
deployment/ABI/runtime identity, proxy identity where applicable, fee/quote asset and
curve graduation rules, buy/sell/allowance semantics, simulation/revert and receipt/reorg
evidence. Neither Four calldata nor a different chain's Flap adapter proves BSC support.
Pancake V3/V4/Infinity, LP, token launch, cross-chain execution and unrestricted live are not
implied by the initial BSC scope or by SDK methods existing.

## E1 exact compatibility boundary

E1 retains repository directory, GitHub remote, Go module/import path, DB/socket filenames,
service IDs, migrations, RPC semantics, existing intent/lease state machines and all signed
artifact identity/AAD. Existing `rbh:4663:...` AAD must remain readable after product renaming.
Add `EVM_*` service-config aliases and a `cmd/evm-bot` alias for the existing RBH trade-service,
not a multi-chain worker. Only chain 4663 can start. BSC 56/Base 8453 are `NOT_IMPLEMENTED`.
Do not start the old/new aliases concurrently; they denote the same execution owner.

Keep `RBH_*` and `ROBINHOOD_RPC_URL` compatibility. If both forms are configured, their raw
values must be identical; reject conflicts without printing values/secrets. New defaulted
settings reject explicitly empty values; legacy empty defaulted settings retain their old
default behavior. Optional secret/identity fields may be empty at config loading, but existing
startup validation still requires them. RPC selection remains configuration-only; an alias
does not prove RPC chain identity. Existing backend identity checks are unchanged.

Directory/module/remote/deployment renaming is a later, separately reviewed compatibility
slice after API landing and E1 review. No automatic data movement, key re-encryption,
authorization transfer or historical evidence rewriting is allowed.

## Required evidence

E1 proves alias equivalence, no secret exposure on conflicts, rejection of every chain
without an installed runtime, explicit empty/default handling, disabled mode/live, immutable
registry returned values, and unchanged service/storage identity. Subsequent slices add
cross-chain request/DB/artifact confusion tests, manual/copy source enforcement, nonce/risk
conservation, stop/revoke/TTL gates, startup/drain/restart/fencing/crash, ambiguous exact-artifact
query/replay rules and canonical/reorg exactly-once evidence. Every slice stops at its review
gate; implementation success is not admission PASS or permission for real transactions.

Preserve `production_broadcast=NOT_CONNECTED`, `controlled_canary_authorization=NOT_GRANTED`,
`live=false`, `release_ready=false`. No mainnet send or real canary is authorized.
