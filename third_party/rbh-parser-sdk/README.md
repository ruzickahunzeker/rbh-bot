<div align="center">
  <h1>RBH Parser SDK for Go</h1>
  <h3><em>Low-latency Robinhood Chain event, receipt, and calldata parsing</em></h3>
</div>

<p align="center">
  <strong>Typed parsing for Pons V2, Long, o1, Pools.trade, PAIR, Bags V2, letscash.fun, Flap, Varo, Virtuals, and Uniswap v4.</strong>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/0xfnzero/rbh-parser-sdk"><img src="https://pkg.go.dev/badge/github.com/0xfnzero/rbh-parser-sdk.svg" alt="Go Reference"></a>
  <a href="https://github.com/0xfnzero/rbh-parser-sdk/releases/latest"><img src="https://img.shields.io/github/v/release/0xfnzero/rbh-parser-sdk" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

<p align="center">
  <a href="./README_CN.md">中文</a> |
  <a href="./README.md">English</a> |
  <a href="https://fnzero.dev/">Website</a> |
  <a href="https://t.me/fnzero_group">Telegram</a> |
  <a href="https://discord.gg/vuazbGkqQE">Discord</a>
</p>

The `rbhparser` package parses application-supplied data without hidden RPC calls.
Optional `feed/sequencer` and `feed/blockrazor` packages provide official signed
Sequencer Feed and regional BlockRazor Direct Feed clients for low-latency intents.

## SDKs

| SDK | Module |
|-----|--------|
| Parser | [`github.com/0xfnzero/rbh-parser-sdk`](https://github.com/0xfnzero/rbh-parser-sdk) |
| Trade | [`github.com/0xfnzero/rbh-trade-sdk`](https://github.com/0xfnzero/rbh-trade-sdk) |

## Supported protocols

| Protocol | Launch events | Curve trades | Uniswap v4 pools and swaps | Transaction intents |
|----------|---------------|--------------|----------------------------|---------------------|
| Pons V2 | Yes | Yes | Yes | Yes |
| Long | Yes | No | Yes | Yes |
| o1 | Yes | No | Yes | Yes |
| Pools.trade | Yes | No | Yes | Yes |
| PAIR | Yes | No | Yes | Yes |
| Bags V2 | Yes | Yes | Yes | Yes |
| letscash.fun | Yes | No | Yes | Yes |
| Flap Tax / Stocks | Yes | No | No | Yes |
| Varo | Yes | No | No | Yes |
| Virtuals | Yes | No | No | Yes |

Flap also registers graduated V2 pairs and tracks `Sync` reserves; Varo
registers v3 pools, while Virtuals tracks its bonding venue and external
liquidity. `SupportedProtocols()` distinguishes these capabilities individually.

Use `SupportedProtocols()` for machine-readable capabilities and
`ParseProtocol()` for configuration values.

## Guarantees

- Routes launch events by `(emitter, topic0)`, not by topic alone.
- Validates ABI words, signed integers, address padding, dynamic offsets, and
  bounded payload sizes.
- Reconstructs and verifies Uniswap v4 `PoolKey` and pool IDs.
- Associates launch, initialization, liquidity, swap, and curve events through
  a concurrency-safe local registry.
- Completes new curve and pool registrations before decoding the first curve or
  v4 trade in the same receipt.
- Reports refunded Bags buys with consumed input in `CurveTrade.AmountIn`, while
  `CurveTrade.Bags` preserves submitted quote, refund, fee split, and post-trade
  virtual reserves.
- Applies receipt registry updates transactionally: a malformed receipt cannot
  partially mutate parser state.
- Supports snapshot and restore of known curve and pool registrations.
- Tracks fee-policy updates and graduated venue registrations without treating
  `ModifyLiquidity` as fee evidence.
- Supports overlay registries for speculative Feed discovery without mutating
  finalized state.
- Verifies official Feed signatures, sequence continuity, gaps, and reorgs in
  the optional `feed/sequencer` package; Feed intents are not receipt fills.
- Registers an exact canonical Bags hook/WETH/dynamic-fee/tick-spacing
  `Initialize` without requiring an earlier observed `TokenCreated`; the hook
  only permits its registered bonding curve to initialize the pool.
- Performs no hidden RPC or network calls.

## Installation

```bash
go get github.com/0xfnzero/rbh-parser-sdk@v0.4.0
```

`v0.4.0` contains the expanded protocol and Feed support; earlier tags do not.

## Parse a receipt

```go
package main

import (
    "fmt"

    sdk "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
    gethtypes "github.com/ethereum/go-ethereum/core/types"
)

func parse(receipt *gethtypes.Receipt) error {
    parser := sdk.New()
    events, err := parser.ParseReceipt(receipt)
    if err != nil {
        return err
    }
    for _, event := range events {
        fmt.Printf("protocol=%s kind=%s data=%#v\n", event.Protocol, event.Kind, event.Data)
    }
    return nil
}
```

`ParseReceipt` is preferred when a transaction can launch and initialize a pool
before its first swap. The parser stages all registry changes and commits them
only after the complete receipt succeeds.

## Parse a log stream

Keep one parser per ordered stream so its registry accumulates launches and
pools:

```go
parser := rbhparser.New()

event, ok, err := parser.ParseLog(log)
if err != nil {
    // Reject or quarantine malformed protocol data.
}
if ok {
    // Process the normalized event.
}
```

For lifecycle association, supply complete receipts for launch and migration
transactions. A bare Uniswap v4 `Initialize` event may not contain enough
information to distinguish launchpad ownership.

## Parse transaction calldata

Applications may pass any decoded EVM transaction directly to the intent
parser. The SDK does not connect to or depend on its source:

```go
if parser.IsPotentialIntent(tx) {
    intents, err := parser.ParseTransactionIntents(tx, sender)
    if err != nil {
        return err
    }
    _ = intents
}
```

`IsPotentialIntentWithFilter` performs a cheap contract and selector check
before full ABI decoding. Transaction intents describe submitted calldata, not
confirmed execution or fill amounts.

For low-latency input, use the optional signed Feed client with the same parser:

```go
feed, err := sequencer.New(sequencer.Config{
    Filter: func(tx *gethtypes.Transaction) bool {
        return parser.IsPotentialIntentWithFilter(tx, intentFilter)
    },
    OnSequence: func(_ context.Context, sequence uint64) error {
        return persistSequence(sequence)
    },
})
if err != nil { return err }
err = feed.Run(ctx, func(_ context.Context, item sequencer.FeedTransaction) error {
    _, err := parser.VisitTransactionIntentsFiltered(
        item.Transaction, item.Sender, intentFilter, handleIntent,
    )
    return err
})
```

See [`examples/direct_feed`](./examples/direct_feed). Persist Feed sequence
numbers, not EVM block numbers; confirm outcomes from receipts. Feed signatures
are verified before filtering. The hot path needs no receipt or RPC lookup.

## Filters

```go
filter := rbhparser.EventFilter{
    IncludeProtocols: rbhparser.Protocols(
        rbhparser.ProtocolPonsV2,
        rbhparser.ProtocolBagsV2,
    ),
    IncludeKinds: rbhparser.EventKinds(
        rbhparser.EventCurveBuy,
        rbhparser.EventSwap,
    ),
}

events, err := parser.ParseReceiptFiltered(receipt, filter)
```

Excluded output does not prevent internal registry updates. This keeps later
pool and curve attribution correct when launch notifications are filtered out.

## Restore known registrations

```go
registry := rbhparser.NewRegistry()
if err := registry.Restore(snapshot); err != nil {
    return err
}
parser := rbhparser.NewWithRegistry(rbhparser.DefaultAddressBook(), registry)
```

Persist only finalized state in production and apply speculative state to an
overlay registry owned by the application.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
```

Live deployment and receipt verification is opt-in. Offline tests cover the
expanded protocol set; live tests depend on the availability of representative
mainnet transactions and deployments.

```bash
ROBINHOOD_RPC_URL=https://rpc.mainnet.chain.robinhood.com go test ./rbhparser -run TestRobinhood -count=1 -v
```

## License

MIT. See [LICENSE](LICENSE).
