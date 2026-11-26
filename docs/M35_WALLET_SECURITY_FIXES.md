# M35 全局钱包修复与发布说明

本次修复基于 `ea14f52`，日期 2026-11-26（Asia/Shanghai）。

## 行为与配置

HTTP 身份来自已验证的 Bearer 凭证；钱包授权从该身份的私有 API 配置读取。请求不能自报 owner。没有配置动态钱包授权时，不会自动获得 keystore 目录中的钱包；`owner_chat_id=0` 共享 keystore 默认拒绝。

在每个需要查询全局钱包的 trade-service 的 API 配置 JSON 中增加 `wallet_access`。以下是结构示例，地址和用户 ID 必须替换为服务器已核实的值：

```json
{
  "principal_id": "tg-bot-bsc",
  "wallet_id": "wallet-bsc-readonly",
  "wallet_address": "0x0000000000000000000000000000000000000001",
  "token": "REPLACE_WITH_EXISTING_PRIVATE_API_CREDENTIAL",
  "wallet_access": {
    "allowed_owner_chat_ids": [1001],
    "allow_shared_keystores": false,
    "primary_can_sign": false,
    "readonly_wallets": [
      {
        "wallet_id": "wallet-rbh-readonly",
        "address": "0x0000000000000000000000000000000000000002"
      }
    ]
  }
}
```

- `allowed_owner_chat_ids` 是该 API 身份可以访问的 keystore 所有者集合。数组复制后固定，不接受客户端覆盖。
- `allow_shared_keystores` 只显式放行本地 owner=0 的 keystore。TG 对共享 keystore 的使用另需服务器设置 `EVM_SHARED_KEYSTORE_CHAT_IDS=1001,1002`，缺省不放行。
- `readonly_wallets` 是该身份的可信跨链只读映射。在不同链配置相同 ID/address 即可跨链查询。相同 ID 对应不同地址、或与同名本地 keystore 冲突时拒绝挂载服务；只读映射不会升级成签名钱包。
- `primary_can_sign` 缺省为 false。只有已有外部签名能力的固定主钱包才显式设为 true；它不是私钥、运行授权或发交易开关。只读钱包的授权提交和交易受理返回 `403 READ_ONLY_WALLET`。
- 一个 bot 凭证若被授予多个用户，HTTP 授权范围就是该 bot 服务的范围。用户之间仍由 TG 层校验；需要 HTTP 用户隔离时，必须分别部署/配置不同 API 身份和凭证。
- 全局授权不会扩展原有 `/v1/trades` 的主钱包受理范围，也不会启用 live、签名或广播。

## 授权记录查询

```text
POST /v1/approvals
GET  /v1/approvals/{request_id}
GET  /v1/approvals/by-idempotency-key?wallet_id=<id>&idempotency_key=<key>
```

按 request_id 加载唯一记录后校验钱包权限；无权读取返回 404。幂等键查询先校验钱包权限，再按 `(wallet_id, idempotency_key)` 查询。省略 wallet_id 兼容为 API 配置的主钱包，不会任意选择其他钱包。同一个幂等键可以在不同钱包使用。

## 删除与中断恢复

bot 迁移 `014_wallet_lifecycle_and_labels.sql` 添加 `ACTIVE / DELETING / DELETED` 状态，以及四类任务表的 INSERT/UPDATE 守卫。已管理钱包的非 ACTIVE 状态阻止新建任务、重启策略和派发；历史未知 ID 保留原有兼容行为。删除后保留 ID 墓碑，历史引用不会被新钱包复用。

删除流程在目录进程锁内运行：

1. 在同一 SQLite 写事务中将目标置为 DELETING，检查全链条件单、狙击、跟单和 submission_jobs。任一查询失败或发现非终态任务都回滚并阻断。
2. 提交事务前写入并同步 `.deleted-<wallet_id>` 标记。交易服务的钱包解析、私钥加载和恢复导入都识别该标记。
3. 提交后删除对应 keystore 和恢复备份，同步目录，再将状态置为 DELETED，清除所有用户对该钱包的当前选择。
4. 启动/目录同步时继续完成已提交的 DELETING 删除；如果只有标记而 DB 已证明仍为 ACTIVE，则清理回滚标记。读取失败时不猜测状态、不开放使用。

文件系统和 SQLite 不构成一个原子事务；持久化标记、保留的 DB 状态与幂等恢复用来覆盖中断窗口。新钱包 ID 使用随机数，不依赖时钟截断值。

## 私钥重新导入与显式恢复

普通导入发现同地址钱包时先验权，再试用当前口令解密。成功则幂等返回，不重写文件。认证失败只能说明口令或密文/AAD 不匹配，不能确定是口令变化。

需要恢复时，TG 显示“恢复钱包加密”按钮，用户明确进入恢复操作并重新发送私钥。系统不保存第一次输入的明文。恢复必须同时满足可信用户归属授权、有效私钥与目标公开地址匹配；保留原 wallet_id、owner 和创建时间，生成新的 v2 密文。共享钱包需显式放行。格式损坏、权限/IO 错误不会被当作口令变化自动覆盖。

落盘在进程锁内采用同目录临时文件（0600）、文件同步、旧密文硬链接备份、原子 Rename、目录同步。Rename 之前失败时旧文件不变；Rename 之后目录同步失败时返回 `ErrKeystoreCommitUncertain`，保留旧密文恢复备份，不宣称旧路径未发生改变。下一次成功认证的导入会确认当前文件并完成备份清理。不要在未确认当前文件可解密前手动清理恢复备份。

## 钱包标签

钱包详情新增“✏️ 修改钱包标签”。标签单独存入 `evm_wallet_labels(chat_id, wallet_id)`，最多 32 个 Unicode 字符；回复 `-` 清除，拒绝换行和控制字符。标签不改变钱包 ID、地址、加密文件名或 AAD；共享钱包的标签也按用户隔离。

## 验证

2026-11-26 实际结果：上述钱包专项及兼容合同的 `-race` 回归通过；`internal/telegram`、`internal/app`、`internal/storage`、`internal/bot` 完整包测试通过；全仓测试编译检查通过；bot-service、trade-service、feed-service、operator 四个二进制构建通过。没有将针对性 tradeapi 测试表述为其全部历史测试通过。

专项测试覆盖：A/B 身份隔离、共享默认拒绝、只读跨链查询和禁止受理、地址冲突、授权 POST/GET 与同键多钱包、非终态任务阻断、数据库异常、跨 SQLite 连接的旧检查结果与入队竞争、重启删除恢复、无权重加密、认证失败歧义、替换前/后的文件故障、旧版兼容、并发恢复及标签隔离。

```bash
go test -race ./internal/trade ./internal/telegram ./internal/tradeapi \
  -run 'TestWalletSecurity|TestWalletRecovery|TestEncryptedWallet|TestGlobalKeystore|TestGlobalEVMWallet|TestLocalV2Dynamic|TestStep15Local|TestBSCMultiChainWalletTradeIntakeAndAuditUpdates|TestUserHTTPTradeHasNoEconomicDependencies|TestUserHTTPTradePrivateConfiguration' \
  -count=1 -timeout=180s
go test ./internal/telegram ./internal/app ./internal/storage ./internal/bot -count=1 -timeout=180s
go test ./... -run '^$'
```

发布时先备份现有私有配置、keystore 与 bot.db，再核实每个 API 身份的授权集合和只读映射。所有服务仍按现有只读/模拟盘模式运行；本次代码修改不修改服务器口令、真实钱包或 live 开关。迁移是新增版本，不修改已经部署的 013 迁移校验和。
