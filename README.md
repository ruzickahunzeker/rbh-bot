# RBH Bot

Robinhood Chain 低延迟交易与 Copy Trading 系统。生产依赖锁定的
`rbh-parser-sdk` 与 `rbh-trade-sdk`，产品工程参考 `solana-bot`，但执行、nonce、
Receipt、reorg 和恢复按 EVM 模型实现。

当前状态：**PR-001 Bootstrap / live disabled**。

## 服务

- `feed-service`：Feed、Receipt、协议 Registry 和链状态的唯一写入者。
- `bot-service`：watched wallet、策略、信号和产品交互的唯一写入者。
- `trade-service`：钱包、余额预占、nonce、签名制品和执行账本的唯一写入者。

服务间使用 HTTP over Unix Domain Socket。每个服务独占自己的 SQLite 数据库，
禁止跨服务直接打开其他服务的数据库。

## 本地启动

需要 Go 1.26.6。复制 `.env.example` 后分别运行：

```bash
go run ./cmd/feed-service
go run ./cmd/bot-service
go run ./cmd/trade-service
```

默认 socket 与数据库位于 `./data/`。当前骨架仅开放 `/health`、`/ready` 和
`/metrics`；交易、签名和广播尚未启用。

## 实施基线

- `V0.1 Core`：Pons Curve、Pons v4、Long、Feed、Copy、Nonce、Recovery、Reorg、Wallet、Risk。
- `V0.1 Product`：Telegram、钱包管理、手动交易、策略配置、订单/仓位查询和通知。
- `V0.2`：限价单、价格/市值触发与更多自动化。
- LP 仅保留模型扩展点，执行能力固定关闭。

Phase 0 证据与运行手册见 [`rbh-validation-package`](rbh-validation-package/README.md)。

