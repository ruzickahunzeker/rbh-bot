# Step 0A 基线收拢与分支 SHA 对照记录

- **日期**：2026-11-24
- **仓库位置**：`/root/evm-bot`
- **集成工作分支**：`evm-bot/step0-baseline`

| 来源分支 | HEAD SHA | 相对差异与处理结论 |
|---|---|---|
| `origin/main` | `1ef6784` | **选定代码主干基线**：含 PR #43（A4-B2 atomic arming closeout）、`001..050` 全量 SQLite 迁移及 A1–A4 引擎代码 |
| `origin/http-canary-activation-template` | `c71cbd9`（父提交 `ab84f46`） | 含 32 个独有提交，其中 31 个为 P5–P9 验收/计划文档，仅 1 个代码提交 `bdf9149`（`feat: separate HTTP query-only composition`）；其只读查询能力已整合为 `TradeProfileQueryAndRecovery` 与 A1 `RECOVERY_ONLY` 共同演进 |
| `evm-bot/step0-baseline` | 见 `git log` | 在 `origin/main@1ef6784` 上落地 `EVM_BOT_PLAN_V3.md` 及 Step 0A / Step 0B 全部工程加固项 |
