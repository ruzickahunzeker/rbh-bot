# E1 configuration compatibility and static chain identity

Status: `E1_READY_FOR_REVIEW`, pending independent review.
Base: `main@7b278c4ffd48fe75c8f20d25185f0743222684b8`.

## Configuration aliases

| New name | Legacy name | Default / purpose |
| --- | --- | --- |
| `EVM_DATA_DIR` | `RBH_DATA_DIR` | `./data`; unchanged DB filenames |
| `EVM_SOCKET_DIR` | `RBH_SOCKET_DIR` | `<data-dir>/run`; unchanged socket names |
| `EVM_LOG_LEVEL` | `RBH_LOG_LEVEL` | `INFO` |
| `EVM_CHAIN_ID` | `RBH_CHAIN_ID` | `4663`; only installed runtime |
| `EVM_LIVE_ENABLED` | `RBH_LIVE_ENABLED` | `false`; true/malformed rejected at config loading |
| `EVM_CONTROLLED_CANARY_PRODUCTION_MODE` | `RBH_CONTROLLED_CANARY_PRODUCTION_MODE` | `DISABLED`; trade-service rejects every other mode |
| `EVM_INTERNAL_AUTH_SECRET` | `RBH_INTERNAL_AUTH_SECRET` | no default; existing internal HMAC secret |
| `EVM_RPC_URL` | `ROBINHOOD_RPC_URL` | no default; existing Robinhood RPC configuration |
| `EVM_DRY_RUN_WALLET_ID` | `RBH_DRY_RUN_WALLET_ID` | no default; existing wallet identity |
| `EVM_DRY_RUN_FROM_ADDRESS` | `RBH_DRY_RUN_FROM_ADDRESS` | no default; existing from address |
| `EVM_EXECUTION_PRIVATE_KEY` | `RBH_EXECUTION_PRIVATE_KEY` | no default; inject secret, never commit |
| `EVM_ARTIFACT_ENCRYPTION_KEY` | `RBH_ARTIFACT_ENCRYPTION_KEY` | no default; inject secret, never commit |
| `EVM_ARTIFACT_KEY_VERSION` | `RBH_ARTIFACT_KEY_VERSION` | `v1`; unchanged encrypted artifact version |

Use either vocabulary. If both names are present, raw values must be identical; no silent
precedence, trimming, path normalization or secret output. Conflicts reject even for fields
unused by the service. Do not combine new config with inherited legacy values unless identical.
No automatic conversion/deletion of legacy environment settings occurs.

Explicitly empty new settings with defaults are errors. Absent new settings retain legacy
behavior: empty legacy defaulted values use the old defaults. Optional secret/address/RPC
fields can be empty at Load, but existing app startup still validates required fields before
serving business routes. Config must load before app.Run creates its data directory, opens
SQLite or dials RPC. Aliases do not prove RPC identity or authorize wallet use; existing backend
and durable gate checks remain necessary and unchanged.

`.env.example` uses new names and no automatic RPC endpoint. It is a template, not a request
to run a real wallet or contact mainnet. The probe/classification commands under `cmd/` remain
legacy RBH tools reading their documented `ROBINHOOD_RPC_URL` or existing flags. E1 aliases
apply to config.Load-based feed/bot/trade services, not historical tools.

## Installed runtime versus static registry

| Chain | ID | Registry runtime | Can config start it in E1? |
| --- | --- | --- | --- |
| Robinhood | 4663 | `LEGACY_ROBINHOOD` | yes, existing config/guards still required |
| BSC | 56 | `NOT_IMPLEMENTED` | no |
| Base | 8453 | `NOT_IMPLEMENTED` | no |

Unknown chains reject. Registry returns fresh value copies; callers cannot register,
enable or override runtime. A listed chain is not admission, recovery health, authorization,
durable gate PASS or send permission. No registry HTTP endpoint exists in E1.

There is no BSC SDK import, BSC/Base RPC, new protocol adapter or fallback. All three
existing service entrypoints reject BSC/Base selection before resource startup. Future
per-chain databases/worker binding require subsequent independently reviewed implementation.

## Naming alias and rollout

`go run ./cmd/evm-bot` calls exactly `app.Run(config.TradeService)`, with the same readiness,
signer preparation and query-only recovery as the legacy entry. It does not create a generic
gateway or multi-chain process. CLI arguments reject before startup; do not pass `--chain=56`
and assume a BSC worker exists.

Choose the old or new name, never both as competing owners. E1 does not add a process lock;
the existing single-owner deployment prerequisite still applies. Do not use production
handoff as an E1 test. Before any separately approved operational rename, inventory immutable
deployments/auth, unresolved artifacts, DB location and recovery drain. Deployment identity
changes invalidate old authorization rather than transferring it.

Rollback restores the previous build and legacy config without data conversion. Drain
existing recovery before a separately authorized process switch; preserve DB/key version and
exact artifact identity. This slice performs no deployment restart, drain, authorization
grant/revoke or live action. DB/socket/service names, migrations, artifact AAD,
W4-B ambiguity semantics and historical attestations are unchanged.

## Acceptance evidence

- `TestEVMConfigLegacyAndNewAliasesAreEquivalent`: each service; new/old/equal dual config.
- `TestEVMConfigConflictingAliasesFailClosedWithoutValues`: every alias pair, no values in errors.
- `TestEVMConfigNoNewDefaultsChangeStorageOrServiceIdentity`: unchanged DB/socket/service/key defaults.
- `TestEVMConfigUnavailableChainCannotSelectLegacyRuntime`: selectors reject BSC/Base/unknown/malformed.
- `TestEVMConfigExplicitEmptyAndLegacyDefaults`: explicit-empty new config versus legacy fallback.
- `TestEVMConfigCannotEnableLiveOrCanary`: live and trade mode fail closed.
- `TestRegistryRuntimeIsNotMultiChainEnablement`, `TestRegistryReturnedValuesCannotInstallOrEnableRuntime`,
  `TestRegistryConcurrentLookupsRemainIsolated`: static metadata cannot mutate/enable runtime.
- `TestEVMUnavailableChainRejectedBeforeDataOrRPCStartup`: actual app.Run preflight, no data creation.
- `TestEVMEntryRejectsIgnoredChainOrLiveArguments`, `TestEVMEntryAliasesOnlyExistingTradeService`:
  no CLI bypass, one existing composition.

These prove E1 config/registry/naming, not production multi-chain execution. Existing
prepare-execution CAN sign; do not claim global zero signing for a naming alias. E1 adds no
signer/submission/send implementation and does not invoke those capabilities in its tests.
Broadcast remains `NOT_CONNECTED`, authorization `NOT_GRANTED`, live/release readiness false,
W4-C review pending, W4-D/W5 not entered.
