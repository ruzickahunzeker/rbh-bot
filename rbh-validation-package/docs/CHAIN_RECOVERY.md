# 03｜最终性、Gap、Reorg 与恢复
**状态：设计契约。目标 RPC 的 finality、pending 和服务端 replay 语义尚未实测。**

## 三层状态

| 视图 | 内容 | 回滚 |
|---|---|---|
| finalized_base | 有明确最终性依据的恢复锚点 | 异常则停机核查 |
| canonical_working | 当前 canonical 链上的池状态、成交和仓位 | 按区块可撤销 |
| speculative_overlay | intent 推测及预热信息 | 可丢弃、重建 |

执行结果 success/revert/unknown、inclusion absent/included/orphaned、
finality 级别分别记录。N 个确认只叫 confirmed_by_policy，不能直接命名为链级 finalized。
safe/finalized 方法返回值不等于其语义验证通过；映射需要目标节点及链的独立证据。
无法验证时保存 unknown。

源 intent 不创造真实仓位。Bot 成功且 canonical 的 Receipt 创造可回滚成交仓位，
满足验证后的最终性门槛才标 settled。孤块使用 append-only reversal，不删除审计历史。
单节点暂时返回 null 时，先检查节点同步、区块头、其他读源，不立即冲销。

## Feed 与应用进度

SDK 的 OnSequence 先于业务 handler，过旧 backlog 不交给实时交易 handler。[S03]
当前适合作为 live intent source，不能未经验证作为完整历史存储。

应用持久化已经收到的合格事件，使用自己的 outbox offset 恢复；
链状态经 canonical 区块/Receipt 补齐。未保存且不可恢复的旧 intent 明确标缺失，
不声称 exactly-once，不根据迟到事件补追交易。

一个 sequence 可能包含多个交易，因此保留 batch/transaction index 与稳定 action path。
同 sequence 不同消息/区块 hash 作为替换线索；不是直接创建第二笔经济订单。
有限缓存的 reorg window 不能作为最终性证明。

服务端历史保留窗口、续传 inclusive/exclusive 语义、重连是否补齐必须单独验证。
窗口失效则记录不可恢复区间、重建状态，回到合格 live head 后仅处理新信号。

## Reorg 协议

发现父哈希不连续或 canonical hash 冲突：
暂停相关执行 → 增加 epoch → 失效 quote/route → 查共同祖先
→ 撤销区块派生状态 → 重放新分支 → 发布一致快照 → 对账订单与预算。

深度超出 undo log 时回退到可信快照补链，期间不可 live。
旧 epoch 的 overlay 作废；本方已广播交易仍需跟踪，源 intent 消失不能抹除真实仓位。

## Intent 模式

策略显式选择 intent 或 included_receipt；首个 live 验证候选默认后者，并要求源成功。
intent 模式另有单笔/单钱包/全局 speculative exposure 与信号年龄限制。
源 revert、Bot 成功时保留实际持仓，停止继续加仓并告警；自动市价清仓不是隐含恢复动作。

Feed degraded 默认暂停；仅当策略明确允许且 Receipt 通道独立健康时才降级，
不能静默切模式，也不能让两种来源产生重复经济动作。

## 就绪与时间

分别报告 feed_ready、registry_ready、quote_ready、wallet_ready、execution_ready、
source_mode_ready。连接恢复不等于可以交易。
时间依赖费率要绑定经验证的执行区块时间条件，不以本机时钟代替链上条件。
本包 RPC 脚本不执行 simulation，也不自动判断 finality，真实 Pons dry-run 仍为阻塞项。
