# 01｜状态所有权与服务通信
**状态：候选工程契约。真实 Go 服务、IPC、认证及 SDK Registry 适配尚未实现。**

## 数据所有权

维持三个进程；state 是 feed_service 内部模块，不拆第四个服务。

| 进程 | 唯一业务写入权 | 数据库 |
|---|---|---|
| feed_service | 协议 Registry、池状态、区块应用日志、快照、观察记录、事件 outbox | feed.db |
| bot_service | watched wallets、策略、信号决策、inbox、请求 outbox | bot.db |
| trade_service | 钱包、经济订单、预算预占、nonce、签名制品、提交尝试、实际成交账 | trade.db |

其他进程不得直接访问数据库文件。真实可用余额、聚合预算和持仓以 trade_service 为权威，
bot 只拥有展示投影。协议池的流动性与钱包 LP 权益分开，不能用一个 position 表混装。

## 传输协议

单机使用 HTTP over Unix Domain Socket；消息 JSON，事件流 NDJSON。
实时与审计是逻辑通道，不引入两套消息系统。

| 路径 | 确认语义 |
|---|---|
| Feed → Bot：事件流/按 offset 补读 | 事件提交后发布；允许重复投递 |
| Feed → Trade：不可变快照/增量 | epoch、版本连续；缺口后重取完整快照 |
| Bot → Trade：提交经济操作 | 持久接单后返回 operation_id；不是成交确认 |
| Trade → Bot：状态查询/状态流 | 用原 operation_id 恢复，不依赖首次 HTTP 响应 |

接收方 inbox 去重、决策与下一跳 outbox 在本地事务完成后才推进消费 offset。
事件带 schema_version、chain_id、source、source_sequence、durable_offset、tx_hash、
stable_action_path、observed_at、expiry、state_epoch 和 provenance。

每个生产者的应用 offset 有序；不承诺不同来源的全序。队列有界，队满则背压或断开，
按 offset 补读。过期事件只补审计，不默认补下旧订单。服务端保留窗口过期时返回
CURSOR_EXPIRED，暂停策略并重建状态，不静默跳 latest。

候选接口见 contracts/transport.json；本包未实现网络服务器。

## 进度、事务与内存发布

区分 observed_sequence（已观察）、durable_event_offset（应用已持久化）、
applied_block_hash（链状态已应用）。当前 SDK OnSequence 在交易 handler 前调用，
且过旧 backlog 不进入实时 handler，不能把该回调当业务 checkpoint。[S03]

正确顺序是：在隔离视图解析 → DB 事务保存事件/状态变化或恢复依据/outbox/业务进度
→ COMMIT → 发布不可变内存快照。提交失败不得发布新状态；提交后、发布前崩溃可恢复。
内存 Registry 与 SQLite 没有天然的跨介质原子事务，真实 SDK staging 必须单独验证。
本包参考模型只测试 event/outbox/cursor 的 SQLite 事务。

## 去重与快照

观察键可以含 source；经济动作键不能含 source：

```
observation_id = chain_id + source + tx_hash + stable_action_path
logical_action_id = chain_id + strategy_instance_id + execution_wallet_id
                    + source_tx_hash + normalized_action_id
```

provider、重试次数、策略配置版本只做审计字段。多跳和 multicall 先归并业务动作，
不能使用随解析器版本变化的数组索引。Receipt 不得再次触发同一 intent 已执行的动作。

快照绑定 chain_id、SDK/schema、epoch、route_version、state_version、block_hash、
合约验证证据和时间条件。外部策略不能覆盖 Hook、PoolKey、Router。
关键状态失效后重新报价/模拟；签名后不能悄悄按新报价重建独立交易。
不要求每个无关区块推进都令订单永久失败。

## 安全边界

服务身份分离、socket 目录与组权限受限；内部请求认证、body hash、request nonce、
有界时间窗和重放缓存必须在真实服务首次接入时落地。
传输重试更换认证 nonce，但复用业务幂等键。认证去重不是经济动作去重的替代品。

本包未验证 IPC、认证、背压、跨服务崩溃恢复或吞吐量。来源索引见 SOURCES.md。
