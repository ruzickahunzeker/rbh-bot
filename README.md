# EVM Bot

从 RBH Bot 演进的 EVM 交易与 Copy Trading 项目。当前安装的运行时仍只支持
Robinhood Chain（4663）；BSC（56）和 Base（8453）仅登记为 `NOT_IMPLEMENTED`，
不能启动或交易。不增加 Solana。

E0 scope 已冻结；E1 命名、配置兼容与 chain registry 为 `READY_FOR_REVIEW`，
尚不是 independent PASS。完整边界见 [EVM_BOT_SCOPE.md](docs/EVM_BOT_SCOPE.md)。

## 当前能力与安全边界

- Robinhood 生产依赖仍为锁定的 `rbh-parser-sdk` / `rbh-trade-sdk`。
- 已有 Pons v2 Curve dry-run、prepare-execution 和受控 recovery lifecycle。
  prepare-execution 可以创建签名制品；不能把 live=false 解读为 signer 不存在。
- Production broadcast 为 `NOT_CONNECTED`，controlled-canary authorization 为
  `NOT_GRANTED`，live/release readiness 保持 false。
- W4-C implementation 已 landed，但仍等待独立 review；E1 不关闭该 gate，
  不进入 W4-D/W5，不开放其他协议或链。
- 统一多链 HTTP gateway、BSC SDK/Flap/Base adapters 尚未实现。
  `trade-api-submission-intake` 是独立未合入分支，不包含在本 slice 的 main 基线。

## 服务

- `feed-service`：现有 Robinhood Feed、Receipt、Registry 与 observation/outbox owner。
- `bot-service`：现有 watched wallet、策略、信号与 intent owner。
- `trade-service`：现有 Robinhood wallet、reservation、nonce、artifact 与执行账本 owner。
- `evm-bot`：同一个 trade-service 的命名别名，不是另一个 owner 或多链 worker。

服务间使用 HTTP over Unix Domain Socket。每个服务独占自己的 SQLite 数据库，
禁止跨服务直接打开其他服务的数据库。不允许同时启动新旧别名竞争同一套数据。
未来 TG Bot 与 Copy Trading 通过统一 API 进入相应链的唯一 execution owner，
不能独立分配 nonce 或广播。

## 本地启动

需要 Go 1.26.6。按 [EVM_CONFIG.md](docs/EVM_CONFIG.md) 注入配置后：

```bash
go run ./cmd/feed-service
go run ./cmd/bot-service
go run ./cmd/evm-bot
```

旧 `go run ./cmd/trade-service` 仍有效，二选一运行。新入口不接受 CLI 参数，
`--chain`/`--live` 不会被静默忽略。不要为测试命名变更启动真实钱包/RPC；
测试使用配置加载、preflight 和本地 harness，不发送交易。

默认 DB 为 `./data/feed.db`、`bot.db`、`trade.db`；socket 位于 `./data/run/`，
service ID、DB 和 socket 文件名均不改变。`EVM_*` 为新配置名称，旧 `RBH_*` /
`ROBINHOOD_RPC_URL` 继续兼容。同一字段的新旧值同时存在时必须字节级相同，否则
拒绝启动，错误不打印配置值。链选择只允许 4663 runtime，不能回退替代 BSC/Base。

内部 IPC 基础包提供 Unix socket client、HMAC body 签名、时间窗、request nonce
防重放、request ID 和结构化错误。业务端点接入前必须配置独立认证 secret。

## 演进与兼容

当前目录、Go module/import path、GitHub remote 和历史 evidence 包仍叫 `rbh-bot`。
实体目录/仓库/部署重命名留给后续独立兼容 slice；不自动迁移数据库、重加密制品
或转移 authorization。产品名称变更不扩大已有 review PASS 的执行范围。

后续顺序是统一认证 API → RBH 兼容适配 → BSC 只读 adapters → 单独 Flap review →
BSC durable execution/recovery；Base 后续独立 admission。LP、Pons v4、Long、
Flap 和新链执行都不能因 SDK 方法或 registry 条目存在而自动开放。

Phase 0 证据与运行手册见 [`rbh-validation-package`](rbh-validation-package/README.md)。
