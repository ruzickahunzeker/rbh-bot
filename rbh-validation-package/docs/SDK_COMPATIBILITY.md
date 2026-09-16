# 04｜SDK 兼容性报告
**结论：NOT READY FOR LIVE。源码基线已核查；真实 SDK 与主网链路未完成验证。**

## 版本基线

| 项目 | 锁定 |
|---|---|
| Parser | 72cf9cee394bbd2566fca1d03202b05981eb51b4 |
| Trade | a894cf496b862144ea00910e73afe553073a858b |
| Parser go.mod | Go 1.25.0；toolchain go1.26.6 |
| Trade go.mod | Go 1.26.0；toolchain go1.26.6 |
| 共同 go-ethereum requirement | v1.17.5 |
| 本包验证工具链 | go1.26.6，不自动下载或静默替换 |

依据为本次会话 GitHub 连接器取得的固定提交和 go.mod。[S01][S02]
完整值与 go.mod/go.sum blob 在 versions.lock.json。
组合业务模块的依赖图尚未解析；锁两个顶层提交不等于完成所有依赖供应链审计。

## 本次结果

| 检查 | 状态 | 证据 |
|---|---|---|
| 离线参考模型 | 39 项通过，0 失败、0 跳过 | evidence/offline-results.json |
| SDK build/test/race/vet | BLOCKED：当前 Go 1.23.2 | evidence/sdk-report.json |
| 组合 SDK smoke 输入 | 已提供，未执行 | spike/sdk_smoke_test.go |
| RPC eth_chainId 只读检查 | 以实际报告为准；本次未取得有效响应 | evidence/rpc-report.json |
| safe/finalized/pending 语义 | 未验证 | 不把返回值或 SDK 配置当语义证据 |
| 真实历史 fixtures | 未采集 | historical/manifest 的 transactions 为空 |
| Pons 真实 SDK → eth_call dry-run | 未完成 | 参考模型不是替代品 |
| 主网 canary / soak / 延迟 | 未执行 | 未使用资金或签名 |

环境阻塞不能说成 SDK 失败或链不支持；上游 skip 不计作在线通过。
本包没有完成真实 Pons dry-run 纵向闭环，也没有部署三个生产服务。

## 源码核查发现

**Feed：** OnSequence 先于 transaction handler；旧 backlog 不进入实时 handler。
必须有独立的业务提交进度。[S03]

**Pons：** 所读 PackBuy/PackSell 没有 deadline 参数；该处 minOut 检查允许零，
应用需要额外非零保护，不能宣称所有路径都有链上到期保护。[S04]

**Long：** 指定报价函数检查特定 Hook/动态费率标记，调用方必须提供已验证费率。
这不是所有 Long 池可交易的保证。[S05]

**LP：** ModifyLiquidity 解析测试不等于钱包权益归属或 LP 写接口已验证；
保留 unknown / not_implemented，不开放功能。[S06]

## 未解除的准入阻塞

| 编号 | 缺口 | 所需证据 |
|---|---|---|
| C01 | 工具链/依赖无法完整执行 | 固定源 build/test/race/vet、组合 smoke、解析 go.sum |
| C02 | 链身份/合约/代理升级路径未验证 | chainId、地址、代码 hash、实现/升级依据、区块绑定 |
| C03 | 历史样本缺失 | 原始 tx/Receipt/块、独立期望值、交易前状态 |
| C04 | replay/finality/pending 语义未知 | 可复现实验和异常案例 |
| C05 | Pons deadline 残余风险未闭合 | 明确能力及实盘准入政策 |
| C06 | 真实 Pons dry-run 未完成 | route→quote→build→simulation→virtual order 证据 |
| C07 | Long 支持组合未实证 | Hook/Router/fee/时间/PoolKey 正反向样本 |
| C08 | 真实执行安全与恢复未实现 | 签名持久化、IPC auth、nonce、reorg、备份恢复测试 |

这些是验收条件，不是排期；本包不为 LP 增加任务或里程碑。

## 历史 fixtures 规则

采集保存 chain_id、真实交易哈希、原始 tx/Receipt、block hash/父块、交易位置、
采集时间、SDK/schema 版本与独立期望。
采集脚本结果标 RPC_CAPTURE_NOT_GOLDEN_EXPECTATION；仍须审核，不能直接变成 golden。

历史报价比对使用相同交易前状态、sender/value、calldata、时间与 fee 配置。
同区块有前序交易时需要顺序回放或等价状态证明；不能用区块结束状态冒充交易前状态。
确定性整数计算要求一致；容差必须具体到 adapter 和最小单位并说明理由。

rpc_probe 仅采集区块/交易/Receipt，不执行 eth_call、estimateGas、合约校验或 feed soak。
