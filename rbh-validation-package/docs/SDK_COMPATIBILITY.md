# 04｜SDK 兼容性报告
**结论：NOT READY FOR LIVE。C01 离线 SDK 兼容性、C02/C03 Pons Curve、C04 replay/finality/pending、C06 Pons Curve dry-run 与 C08 execution safety 已通过；C05 仍未关闭，live=false。**

## 版本基线

| 项目 | 锁定 |
|---|---|
| Parser | upstream base `72cf9cee394bbd2566fca1d03202b05981eb51b4` + repository-local patch `30e4a1414664f7e8f501675368891f48b1a6fd35` |
| Trade | a894cf496b862144ea00910e73afe553073a858b |
| Parser go.mod | Go 1.25.0；toolchain go1.26.6 |
| Trade go.mod | Go 1.26.0；toolchain go1.26.6 |
| 共同 go-ethereum requirement | v1.17.5 |
| 本包验证工具链 | go1.26.6，不自动下载或静默替换 |

依据为本次会话 GitHub 连接器取得的固定提交和 go.mod。[S01][S02]
完整值与 go.mod/go.sum blob 在 versions.lock.json。
组合 smoke 的依赖图已在固定 Go 1.26.6 工具链下解析并保存；
锁两个顶层提交和解析依赖图仍不等于完成所有依赖供应链审计。

## 本次结果

| 检查 | 状态 | 证据 |
|---|---|---|
| 离线参考模型 | 39 项通过，0 失败、0 跳过 | evidence/offline-results.json |
| SDK build/test/race/vet | PASS_OFFLINE_ONLY：792 pass、0 fail、10 upstream skip | evidence/sdk-report.json |
| 组合 SDK smoke | PASS：依赖解析及 Parser/Feed、Pons ABI、Long fail-closed、共享符号测试 | spike/sdk_smoke_test.go、evidence/resolved-spike-go.mod |
| RPC eth_chainId 只读检查 | 以实际报告为准；本次未取得有效响应 | evidence/rpc-report.json |
| safe/finalized/pending 观测与恢复 | C04 PASS | 24 点 tags/canonical 对照、inclusive resume、retention gap 与 crash recovery；不作超出证据的链级语义声明 |
| Pons Curve 真实历史 fixtures | PASS | 独立 launch/buy/sell golden、真实 reverted Factory tx 与 ReceiptGate 证据 |
| Pons 真实 SDK → eth_call dry-run | C06 PASS | route→quote→unsigned build→Curve Buy/Sell simulation 与 fail-closed 证据 |
| 主网 canary / soak / 延迟 | 未执行 | 未使用资金或签名 |

固定工具链验证于 2026-09-16 使用 `golang:1.26.6-bookworm` 执行；
两个仓库均校验锁定 commit 和 go.mod/go.sum blob，依次运行
`go mod download`、`go mod verify`、`go build ./...`、`go test ./...`、
`go test -race ./...` 与 `go vet ./...`，再运行组合 smoke。
上游 skip 不计作在线通过，C01 通过也不解除 C02–C08。
Pons Curve execution safety 的签名、exact-artifact submission、controlled JSON-RPC recovery
与 canonical/reorg reconciliation 已完成 C08 验收；这些不是 mainnet/live evidence，且不授权实盘。

C04 transport compatibility slice 将 patched Parser SDK 快照固定在
`third_party/rbh-parser-sdk`，通过 module `replace` 保证 CI 可复现。compression handshake
本地测试与 production `SequencerRunner` 公共 Feed 只读连接均通过；接入缺陷修复与后续
replay/finality/pending evidence 已通过独立 joint review。该结论不构成 mainnet live 授权。

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
| C01 | **PASS_OFFLINE_ONLY** | 固定源 build/test/race/vet、组合 smoke、解析 go.mod/go.sum 已留证；不含链上验证 |
| C02-PONS-CURVE | **PASS** | Factory/Curve emitter identity、runtime hash 与 fail-closed allowlist 已留证 |
| C03-PONS-CURVE | **PASS** | 独立 launch/buy/sell golden、真实失败交易及 hard gate 已留证 |
| C04 | **PASS** | compression compatibility、24 点 RPC tags/canonical 对照、inclusive resume、duplicate boundary、retention gap fail-closed、reorg/verification/checkpoint recovery joint review |
| C05 | Pons deadline 残余风险未闭合 | 明确能力及实盘准入政策 |
| C06-PONS-CURVE | **PASS** | Pons v2 Curve Buy/Sell SDK build、`eth_call`、revert、幂等与恢复证据 |
| C07 | Long 支持组合未实证 | Hook/Router/fee/时间/PoolKey 正反向样本 |
| C08 | **PASS** | PR-005 pre-broadcast、PR-006 submission/recovery 与 C08 evidence-hardening 联合证据通过 review |

这些是验收条件，不是排期；本包不为 LP 增加任务或里程碑。

## 历史 fixtures 规则

采集保存 chain_id、真实交易哈希、原始 tx/Receipt、block hash/父块、交易位置、
采集时间、SDK/schema 版本与独立期望。
采集脚本结果标 RPC_CAPTURE_NOT_GOLDEN_EXPECTATION；仍须审核，不能直接变成 golden。

历史报价比对使用相同交易前状态、sender/value、calldata、时间与 fee 配置。
同区块有前序交易时需要顺序回放或等价状态证明；不能用区块结束状态冒充交易前状态。
确定性整数计算要求一致；容差必须具体到 adapter 和最小单位并说明理由。

rpc_probe 仅采集区块/交易/Receipt，不执行 eth_call、estimateGas、合约校验或 feed soak。
