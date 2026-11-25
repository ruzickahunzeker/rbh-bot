# M34 V4 / Aerodrome 修复与验证

日期：2026-11-25（Asia/Shanghai）。修复基于提交 `8d10d23`。

## 修复内容

- ETH / BSC / Base 保留官方 5 字段 `ExactInputSingleParams`；RBH 使用带 `minHopPriceX36` 的 6 字段布局，价格下限显式设为 0，由 `amountOutMinimum` 执行滑点保护。
- V4 声明 `PERMIT2` 授权模型。询价预检在同一区块读取两层 allowance，并检查 Permit2 expiration；执行器按缺失项串行确认 ERC-20 和 Permit2 授权，授权有效期覆盖交换 deadline。确认后再次读取实际额度，防止回执成功但授权未生效时继续卖出。
- WETH/WBNB 池买入先包装原生币，再使用路由器余额结算；卖出先提取到路由器，再解包给显式 recipient。未使用的买入资金也退回 recipient。
- V4 原生币净额仅采用匹配 PoolKey / PoolManager / router 的 Swap 事件；包装币净额支持 Deposit/Withdrawal 及 RBH WETH 的 mint/burn Transfer，避免双重计数。
- Aerodrome 补齐 Deposit 支出，并过滤非 router / wallet 的包装和解包事件。

## 自动化回归

以下针对性测试通过，覆盖 ABI 字节偏移、与固定版本 RBH SDK 的编码差分、四链原生池及包装池的买卖构单、净额方向、无关日志隔离、两层额度不足及过期、授权失败阻断、授权回执成功但状态未改变时阻断：

```bash
go test ./internal/protocols/uniswap/v4 ./internal/protocols/aerodrome ./internal/protocols/permit2 ./internal/trade ./internal/tradeapi \
  -run 'TestV4|TestUniswapV4|TestAerodrome|TestPermit2|TestQuotePreflightPermit2' -count=1
go test ./... -run '^$'
```

完整相关测试集中，protocols、trade、feed、app、bot、telegram 通过；tradeapi 的完整测试未通过，包含结构边界检查失败、SQLite 并发锁错误，并在 10 分钟达到默认超时。其中报告失败的五组测试已在 `git archive HEAD` 导出的原始 `8d10d23` 副本上逐项复现（`TestA2PrivateUncachedNoExecutionConsumerBoundary`、`TestHTTPC3B3AtomicCanceledCommitAndConnectionControls`、`TestHTTPM4B24ControlCommitAfterRecheckRejectsConsume`、`TestHTTPM3B3CLocalCompositionDisconnected`、`TestHTTPM3B4ProductionGraphRemainsDisconnected`）。它们与上述针对性回归分开记录，不能宣称全量测试通过。

## 主网只读模拟

调用公开 PublicNode RPC 的 `eth_call` 和 `eth_simulateV1`，固定区块并通过 state override 为临时钱包提供模拟余额。未签名、未广播交易。每次投入 `1,000,000,000,000,000` wei；V4 顺序执行买入、ERC-20 授权 Permit2、Permit2 授权 Universal Router、卖出。Aerodrome 使用直接 ERC-20 授权，三个步骤。表中每组调用均成功，买卖效果解码均返回 `EvidenceComplete=true`。

| 链 / 协议 | 固定区块 | 报价池 | 买入代币 raw | 卖出回收原生币 wei |
| --- | ---: | --- | ---: | ---: |
| BSC V4 | 135611204 | 原生 BNB，fee=100 / tickSpacing=1 | 543173321267516348 | 999750037139230 |
| BSC V4 | 135610587 | WBNB，fee=500 / tickSpacing=10 | 537617393299075391 | 998756320128332 |
| RBH V4 | 122856850 | 原生 ETH，fee=100 / tickSpacing=1 | 1656477 | 999749216958532 |
| RBH V4 | 122856110 | WETH，fee=3000 / tickSpacing=60 | 1649324 | 993014275468154 |
| Base V4 | 61997030 | WETH，fee=500 / tickSpacing=10 | 1656314 | 998750020423103 |
| Ethereum V4 | 26490544 | WETH，fee=500 / tickSpacing=10 | 1658210 | 998749885701511 |
| Base Aerodrome | 61998416 | WETH / USDC volatile | 1656142 | 994008847469108 |

代币：BSC USDT `0x55d398326f99059fF775485246999027B3197955`；RBH USDG `0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168`；Base USDC `0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913`；Ethereum USDC `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48`。

这些模拟证明表列合约、PoolKey 和金额下的交易路径可执行；未验证全部 hooks、代币和流动性条件，也未执行真实交易或部署。
