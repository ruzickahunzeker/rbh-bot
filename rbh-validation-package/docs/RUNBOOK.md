# 复现与交接

## 离线参考模型

```bash
cd rbh-validation-package
python3 scripts/verify_offline.py
```

仅依赖 Python 3.10+ 标准库与 SQLite。测试使用临时文件、本地 FakeChain 和无效的模型字节，
不联网、不读取私钥、不签名、不广播。退出码 0=模型通过，1=失败。
默认结果写 runs/，evidence/ 是本次交付记录。测试耗时不是交易延迟指标。

## 固定 SDK

准备 go1.26.6、Git、网络；使用无生产凭据的一次性环境。上游测试会执行上游/依赖代码。

```bash
python3 scripts/verify_sdk.py --allow-network
```

脚本禁止自动下载/替换工具链，按 commit 拉取并验证 HEAD 和 go.mod/go.sum blob，
执行 build/test/race/vet，再跑组合模块 smoke，保存解析后的 go.mod/go.sum。
现有 .work 若不是干净的锁定提交则拒绝，不会 reset 或覆盖。下载中断后的目录需人工检查再处理。
退出码 2=阻塞，不是测试失败；上游 skip 单独统计，不算在线通过。
本脚本不做主网 dry-run、长时间 feed 或历史报价验证。

## 只读 RPC

通过受控环境注入 ROBINHOOD_RPC_URL，不要把带密钥 URL 写入日志、示例或仓库。

```bash
python3 scripts/rpc_probe.py --allow-readonly-network
```

只允许 chainId、区块、交易和 Receipt 读取；chainId 不匹配停止。
safe/finalized/pending 的返回值仅作观察，语义仍需验证。传输错误不说明链不支持。

在 historical/manifest 中填入真实、经核验的交易哈希，再采集：

```bash
python3 scripts/rpc_probe.py --allow-readonly-network \
  --fixture-manifest fixtures/historical/manifest.json
```

空清单不会制造样本；SYNTHETIC 输入禁止用于历史采集。采集文件不是 golden。
仍需交易前状态、独立预期字段、真实 SDK 解析及 simulation。
脚本未实现 eth_call、estimateGas、合约代码验证或 feed soak。

## 校验和交接

```bash
python3 scripts/check_package.py
```

验证 SHA256SUMS、JSON 可解析与本包测试报告的自洽性。
离线测试重跑写 runs/，不覆盖原证据；修改交付文件后校验和将故意失败。
真实服务开发继续采用 Go；Python 仅是这份可执行规格和验证工具，不是另起一套生产交易栈。

本次未修改远程仓库、未部署服务。C01–C08 未解除前保持实盘禁用。
