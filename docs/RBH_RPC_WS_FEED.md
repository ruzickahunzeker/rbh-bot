# RBH standard RPC WebSocket feed

RBH feed can use an authorized standard JSON-RPC provider through
`RBH_FEED_SOURCE=rpc_ws`, `RBH_RPC_URL` and `RBH_WS_URL`. This restores included
block observations; it does not supply Nitro sequencer preconfirmations.
See `deploy/env/rbh.env.example` for configuration names and defaults.

## Environment migration

RBH instance-local settings use `RBH_*`. Legacy `EVM_*` aliases remain supported;
different nonempty values for aliases fail startup with variable names only.
`ROBINHOOD_RPC_URL` remains a legacy RPC alias. Cross-chain bot routing and global
wallet settings (`EVM_WALLET_PASSPHRASE`, `EVM_KEYSTORE_DIR`) retain their names.
The running feed/trade instances read `feed-rbh.env` / `trade-rbh.env`; changing
only `rbh.env` is insufficient. Load each instance's environment without inherited
chain-local variables from a different service. Never print or commit private envs.

The feed source is explicit. Merely setting a WS URL does not switch an existing
source. BSC can opt in using `EVM_FEED_SOURCE=rpc_ws`; its default remains RPC
polling. RPC polling on RBH is rejected because the legacy polling runner reuses
the sequencer cursor. Sequencer mode is restricted to chain 4663.

## Checkpoints, gaps and readiness

Migration 007 adds `feed_rpc_checkpoints` and `feed_rpc_source_audit`. Nitro
`feed_progress.observed_sequence` remains independent; it cannot be subtracted
from an EVM block height to measure missing blocks.

On the first start only, `START_MODE=latest` records a baseline at head minus one
and starts with the observed head block. Earlier history is explicitly excluded
and the baseline is audited. `START_MODE=block` plus `START_BLOCK` can select a
positive start inside the bounded catchup window. Restart always resumes the
persisted RPC checkpoint; changing start mode does not reset it.

WS `newHeads` notifications wake a single HTTP collector. The collector validates
HTTP and WS chain identity, parent linkage, receipt count and transaction order,
receipt/log block identities, and the canonical hash after fetching receipts.
An empty block advances; null or incomplete receipts never advance. Whole-block
events, outbox, registry snapshot, receipt audits and checkpoint commit atomically.
Parser registry state is restored if collection or persistence fails.

The default maximum catchup is 200 blocks. Larger gaps and detected reorgs durably
halt and audit the feed; they are never skipped automatically. These halts require
an explicit, reviewed recovery: stop the feed, back up its DB, inspect canonical
hashes and pending economic actions, select a recovery range, and record the
operator decision before changing checkpoints/degraded state. Do not clear the
degraded flag just to make readiness green. Already broadcast economic actions
cannot be undone by replaying blocks.

RPC requests default to at least 50 ms apart, with five-second request deadlines.
WS has transport ping/pong plus a ten-second header inactivity deadline. Failed
connections resubscribe with exponential backoff; HTTP 429 preserves bounded
Retry-After metadata. Notification buffers are bounded; missing notifications are
recovered from canonical HTTP blocks. Ready requires a live recent WS stream,
recent successful processing, at most eight blocks of processing lag, recent
block time, and no durable degraded flag. `/health` remains process liveness.

## Events and consumers

RBH receipts use the existing SDK for verified Pons launches, curve buys/sells,
registered V4 swaps and pool migration events. Unknown Pons curves are resolved
against the canonical factory at the block being processed before registration.
Unattributed V4 pools are not guessed. Generic V2/V3 signals additionally verify
pool token membership and reject ambiguous multi-asset deltas. Existing other-chain
receipt adapters remain available; a recognized topic alone is not proof of full
protocol coverage.

Normalized signals carry block hash and chain event time. Bot freshness checks
use event time, not replay/consumption time. Expired copy signals create no job;
expired launch/migration signals do not consume armed sniper tasks. Existing
sequencer intent payloads remain observation-only. Observation IDs include source;
economic copy dedupe remains scoped by principal, strategy, chain and trigger ID.

## Validation

Validated config aliases/conflicts, real local WS subscription and reconnection,
independent cursor/resume, bounded gaps, same-height and parent-hash reorgs,
null/incomplete receipts, transaction rollback and replay, historical Pons receipt
to durable normalized outbox, HTTP 429 metadata, and stale copy/sniper suppression.

The config/feed/bot/app/storage/telegram package suites and whole-repository test
compilation passed. Targeted new behavior passed with `-race`. An explicitly
enabled read-only PublicNode integration subscribed to actual `newHeads` and
advanced 87 blocks in 25 seconds using a temporary DB, with readiness observed.
Tests do not send Telegram messages or submit trades. Production acceptance is
recorded separately in the private release backup directory.

## Legacy bot cursor adoption

When shadow multi-chain runtime is enabled it takes exclusive ownership of feed
consumption; the old single-chain consumer is disabled. Before takeover, every
legacy inbox observation up to bot_progress must match the authenticated primary
feed's offset and observation ID. Only then are legacy rows assigned the actual
chain and the multi-chain checkpoint seeded in a single transaction. Missing or
conflicting history fails startup; it is not guessed from the migration's RBH
default. A private copy of production bot.db verified all 58,885 historical BSC
rows and then consumed all 226 RBH sequencer observations without economic replay.
Feed outbox now carries original observation timestamps as a compatibility fallback
for old included payloads without event_time, preventing stale history becoming
fresh merely because a consumer restarts. Already managed cursors make adoption
idempotent on later restarts.
