# RBH Bot 验证包
**报告日期：2026-09-16｜交付：设计基线 + 离线契约验证｜实盘：禁止**

范围维持 **Pons V2 + Long**，Uniswap v4 仅用于已验证的共享执行路径；不含 Bags V2。
**LP 只预留、不规划**：保留操作层次、PositionRef、多资产预算及能力边界，
不新增 LP 里程碑、写 API、builder 或资金执行。liquidity.* 在参考模型中硬性拒绝。

本包不是完整 Bot，也不是已完成主网验收的发行版。

## 实际结果

| 项目 | 本次结果 | 证据及边界 |
|---|---|---|
| SDK 版本 | 两个不可变 commit 与模块文件 blob 已记录 | [versions.lock.json](versions.lock.json) |
| 离线参考模型 | **39 项通过，0 失败、0 跳过** | [JSON 逐项结果](evidence/offline-results.json)、[测试日志](evidence/offline-results.log) |
| 崩溃模型 | 3 个边界 × 5 次真实子进程退出通过 | SQLite + 本地 FakeChain，不是链上广播证明 |
| SDK 编译/测试 | **PASS_OFFLINE_ONLY** | [SDK 报告](evidence/sdk-report.json)：固定 Go 1.26.6 下 23 个检查通过，792 pass、0 fail、10 upstream skip |
| RPC 只读检查 | **OBSERVED / 语义未验证** | [C03 RPC 报告](evidence/c03-rpc-report.json)：chain 4663、7 个 canonical captures；finality/pending 语义仍未验收 |
| Pons 历史 fixtures | **PARTIAL** | 6 个语义候选 + 1 个 launch context 已完成 parser replay；仍非 golden，缺 revert、pre-state 与独立复核 |
| 主网 canary / soak / 性能 SLO | **未执行** | 不构成实盘上线批准 |

运行证据使用机器实际 UTC 时间，报告日期采用本次交付日期；分别保存。
模型测试耗时不是交易延迟指标。

## 四份核心文档

1. [状态所有权与通信](docs/STATE_AND_TRANSPORT.md)：三个进程、单写者、传输、原子发布、重放与背压。
2. [操作、nonce 与提交恢复](docs/EXECUTION_RECOVERY.md)：Operation/Step/Attempt、预算、签名持久化屏障、unknown 恢复。
3. [最终性、Gap、Reorg](docs/CHAIN_RECOVERY.md)：三层状态、可回滚账本、恢复与 intent 风险。
4. [SDK 兼容性报告](docs/SDK_COMPATIBILITY.md)：固定源、实际结果、历史样本标准与 C01–C08 阻塞项。

补充：[LP 预留](docs/LP_RESERVATION.md)、[验收矩阵](docs/ACCEPTANCE.md)、
[运行与交接](docs/RUNBOOK.md)、[来源与证据边界](docs/SOURCES.md)。

## 目录

```
docs/          设计契约、兼容性报告、LP 预留、验收与交接
contracts/     候选数据契约和 PositionRef schema
verification/  Python + SQLite 可执行参考模型及 39 项测试
scripts/       离线检查、固定 SDK 检查、只读 RPC/样本采集、包校验
spike/         待正确工具链执行的 Go SDK smoke 输入
fixtures/      合成身份样本 + 尚未晋升 golden 的真实历史候选
evidence/      本次实际运行 JSON 和日志
versions.lock.json
SHA256SUMS
```

Python 仅用于验证工具和可执行规格，拟议生产服务仍采用 Go。
参考模型不含真实签名器、加密实现或 RPC；其假字节不会构成有效 EVM 交易。
SDK runner 执行上游测试前需要显式联网授权和正确工具链；组合 Go smoke 已在固定工具链执行并留存解析后的 go.mod/go.sum。

## 本地复现

```bash
cd rbh-validation-package
python3 scripts/check_package.py
python3 scripts/verify_offline.py
```

默认重跑写入 runs/，不覆盖 evidence/。
离线模型仅需 Python 3.10+ 标准库与 SQLite，不读取钱包，不签名或广播。
正确工具链与网络准备好后，可按 RUNBOOK 执行固定 SDK 和只读 RPC 检查。

## 当前准入结论

**release_ready=false。** 可以评审设计、复现模型、继续补齐兼容性验证；
不能把“39 项通过”表述为“Pons/Long 主网交易通过”。
真实 Pons dry-run、golden fixture 晋升和生产服务集成仍未交付，已在报告列为阻塞。

本次未创建或修改远程仓库，未部署服务，未导入钱包，未签名或发送真实交易。
