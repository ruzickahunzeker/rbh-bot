# 来源与证据边界

## S01｜Parser go.mod

[Parser go.mod](https://github.com/0xfnzero/rbh-parser-sdk/blob/72cf9cee394bbd2566fca1d03202b05981eb51b4/go.mod)

支持：Go/module/toolchain declarations。

边界：GitHub connector contents supplied in this conversation; not a compile test。

## S02｜Trade go.mod

[Trade go.mod](https://github.com/0xfnzero/rbh-trade-sdk/blob/a894cf496b862144ea00910e73afe553073a858b/go.mod)

支持：Go/module/toolchain declarations。

边界：GitHub connector contents supplied in this conversation; not a compile test。

## S03｜Sequencer client.go

[Sequencer client.go](https://github.com/0xfnzero/rbh-parser-sdk/blob/72cf9cee394bbd2566fca1d03202b05981eb51b4/feed/sequencer/client.go)

支持：OnSequence before handler; historical backlog filtering。

边界：Pinned source excerpt reviewed, server replay not tested。

## S04｜Pons calldata.go

[Pons calldata.go](https://github.com/0xfnzero/rbh-trade-sdk/blob/a894cf496b862144ea00910e73afe553073a858b/adapters/pons/calldata.go)

支持：PackBuy/PackSell have no deadline argument; application minOut guard needed。

边界：Pinned source excerpt reviewed, deployed ABI not independently checked。

## S05｜Long quote implementation

[Long quote implementation](https://github.com/0xfnzero/rbh-trade-sdk/blob/a894cf496b862144ea00910e73afe553073a858b/rbhtrade/long_quote.go)

支持：Specific hook and fee preconditions, not universal Long support。

边界：Source reviewed; no quote executed。

## S06｜Parser liquidity tests

[Parser liquidity tests](https://github.com/0xfnzero/rbh-parser-sdk/blob/72cf9cee394bbd2566fca1d03202b05981eb51b4/rbhparser/liquidity_test.go)

支持：ModifyLiquidity position-poke test exists。

边界：Search excerpt reviewed, not exhaustive LP API audit or user ownership verification。

## S08｜SQLite PRAGMA synchronous

[SQLite PRAGMA synchronous](https://www.sqlite.org/pragma.html#pragma_synchronous)

支持：WAL synchronous FULL/NORMAL durability distinction。

边界：Official documentation read; hardware durability not tested。

以上源码依据不等于编译或链上验证。实际执行结果保存在 evidence/，完整版本值在 versions.lock.json。
