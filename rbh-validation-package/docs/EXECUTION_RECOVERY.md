# 02｜操作、nonce 与提交不确定性
**状态：工程契约 + 离线参考模型。未实现真实钱包、签名器或交易广播。**

## 操作层次

Operation（经济目的与幂等边界）→ ExecutionStep（有依赖的 approval/swap 步骤）
→ TransactionAttempt（某签名制品的一次发送尝试）。

重播同一 raw tx 不等于重复创建经济订单；same-nonce replacement 是另一签名版本，
仍属于原 step/replacement_group。新 nonce 买入不得伪装成“原单重试”。
LP 只保留 liquidity.* 名字空间、多资产预算和 PositionRef，不实现 LP 工作流。

生产幂等唯一键为 chain_id + execution_wallet_id + idempotency_key。
同键同固定约束返回原操作，不同约束返回 409。quote 版本另记执行计划，
市场变化不能把查询旧订单变成新增订单。

## 金额与资金预占

金额全部为最小单位整数字符串，检查 uint256 范围，不用 float。
地址规范化；混合大小写 checksum 校验另设，参考 address() 不实现 EIP-55。

trade_service 在事务内聚合并预占同钱包所有策略的资金和 gas。
买入 amount_in 是明确输入资产数量。百分比卖出基于该策略可支配仓位，
绑定余额/已预占快照、向下取整，不默认卖完整钱包余额。
精确数量与百分比请求必须互斥。

## 发送前持久化屏障

```
接单落库 → 路由/报价/风控 → 预算与 nonce 绑定
→ 实际 from/value/目标状态的 simulation → 签名
→ 保存签名 hash、加密 raw tx、nonce、计划与来源证据
→ COMMIT 成功 → 写提交尝试 → 才允许广播
```

计划绑定 route_version + state_version + block_hash + epoch 和时间相关 fee 参数。
制品落库失败不能发送。真实 raw tx 必须受控加密存储，记录 `key_version`、nonce、
AAD、创建时间；私钥永不持久化，日志和 evidence 不记录 raw tx、私钥、master key 或认证头。
nonce、attempt、密文、tx hash 与完整执行字段必须在同一明确事务边界内 durable commit。

本包 artifact 只是 MODEL_ONLY 假字节，SHA256 是测试标识，不是 Ethereum tx hash；
**参考模型不包含加密实现，也不产生任何有效链上交易。**

## 恢复状态

| 情况 | 动作 |
|---|---|
| prepared，未形成持久签名制品 | 不广播；先核查制品与外部 nonce 消耗再恢复 |
| signed，发送前崩溃 | 加载、解密并验证既有制品；策略或新鲜度可以阻止重播，但不得 rebuild/resign |
| RPC timeout / 响应丢失 | broadcast_unknown，冻结该钱包新增执行 |
| RPC 返回 hash | 仅记录提交结果，继续查 Receipt |
| 单次返回 null | 不是失败、未提交或永久 dropped 证明 |
| nonce 被未知交易消耗 | 停签并定位消费交易，不能直接分配新 nonce 再买 |
| 成功 canonical Receipt | included，写可回滚成交 |
| revert Receipt | 记录失败与费用，nonce 已消费 |
| 区块不再 canonical | orphaned，追加撤销记录，重新跟踪原制品 |

dropped/replaced 不由简单超时认定。自动加速、取消和 replacement 不开放；
只允许受控同制品重播或人工处置。单钱包一个 allocator，首个 live 验证每钱包最多一个
未解决执行步骤。本包模型的 nonce 仅覆盖 SQLite 有符号整数范围，不是完整生产 nonce 类型。

PR-006 只能加载并验证 PR-005 已持久化的制品。ambiguous recovery 只能查询、对账，或在
策略允许时重播 exact same raw bytes；不得重新构建交易、重新签名、重新计算 fee 或重新分配
nonce。replacement signing 属于未来独立安全设计，不在当前范围内。

## Pons deadline 特例

所读 PackBuy/PackSell 编码输入额、最小输出和 recipient，没有 deadline 参数。[S04]
应用 quote TTL 只能阻止过期的新签名/重播，不能令已传播交易自动过期。
adapter 应区分 contract_deadline、application_ttl_only、unknown。
Pons live 前必须明确处理此残余风险，不假设存在未经验证的 wrapper。

## 授权、对账和停止

approval 单独拥有 step、nonce、发送恢复和 Receipt；不能计入 swap 成交。
前序未知时不继续依赖步骤；授权完成后重新验证余额、价格和额度。
实际到账以可归属的钱包资产变化与 Receipt 为据，区分退款、税费和 gas。

kill switch 停止新的签名/广播/自动重播，保留查询、对账、告警；
不能声称撤销已经广播的交易。

签名/幂等/nonce 日志采用 WAL + FULL；NORMAL 与 FULL 的断电耐久不同。[S08]
本包进程退出测试不是断电测试。恢复旧备份后先停签，核查备份之后的 nonce 和成交，
不能因数据库 integrity_check 成功就立即恢复 live。
