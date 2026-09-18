# 验收与证据分级

PASS_REFERENCE_MODEL 只代表本地模型；SOURCE_REVIEWED 是源码核查；
BLOCKED 是输入/环境受阻；NOT_RUN 是没有执行。真实集成通过需要独立 PASS_INTEGRATION，
本次没有这一等级。

## 已执行参考测试

完整 39 个测试名称、结果与时间在 evidence/offline-results.json。

| 测试编号 | 覆盖 |
|---|---|
| 01–08 | 整数/地址/链身份/非零保护/拒绝外部执行参数/无 live 模式 |
| 09–11 | LP 写动作禁用、观察 unknown、环境变量不能绕过 |
| 12–16 | 逻辑动作身份、quote epoch/block/TTL、未知路由拒绝 |
| 17–23 | SQLite WAL+FULL、幂等、重启、聚合资金预占 |
| 24–31 | nonce lane、持久制品屏障、unknown 恢复、同制品重播、kill |
| 32–34 | SQLite busy、并发重复接单、真实 SQLite backup/integrity_check |
| 35–38 | event/outbox/cursor 原子、内存发布恢复、合成账务回滚 |
| 39 | 3 边界 × 5 次真实子进程 os._exit，链仍是 FakeChain |

进程退出不是断电测试；FakeChain 的唯一约束不是 EVM 证明。
模型只实现简化单链、单执行步骤和单资产回滚账，不是生产多步执行器或完整 LP 账本。
未测试真实 SDK Registry staging、IPC、认证、密钥加密、真实 RPC 或性能 SLO。

## 尚未达到的真实集成门槛

| 项目 | 候选要求 | 本次 |
|---|---|---|
| fixtures | 被声明支持的路径与独立期望全部通过 | 未执行 |
| 崩溃恢复 | 接单/签名/广播各边界无重复经济订单 | 仅模型 |
| admission 并发 | 至少 10,000 请求；accepted + deduped + queued + rejected = total | 未执行 |
| 故障 | RPC timeout、真实 reorg、disk full、旧备份后链上补账 | 未执行 |
| feed soak | 连续 72 小时，无未解释 gap/永久积压 | 未执行 |
| canary | 用户明确批准预算、gas、次数与停止条件 | 未授权 |
| 安全 | IPC auth、认证重放、密钥轮换、日志脱敏、kill | 生产实现未完成 |

前次讨论的性能候选值不是实测：应用接收到持久接单 p50/p95/p99=10/30/80ms，
含 quote/simulation 至广播开始=150/400/800ms。需在指定硬件、存储、RPC、负载和预热条件下
测量后再冻结，不得为达标去掉 simulation 或先发送后落库。
阻断、过期、排队与超时事件必须统计，不能只挑成功样本。

10,000 admission concurrency 还必须证明 duplicate economic executions、nonce collisions、
reservation overcommit 与 unexplained accepted requests 全部为 0；它不要求同时广播 10,000
笔交易。

**release_ready=false。SDK_COMPATIBILITY 的 C01–C08 未解除前不批准实盘。**
