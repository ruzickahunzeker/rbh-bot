# LP 预留边界
**只预留，不规划。** 不增加 LP 开发排期、里程碑、写 API、builder、资金测试或自动再平衡。

| 保留点 | 本包边界 |
|---|---|
| Operation → Step → Attempt | 不把经济操作等同于单笔交易；当前验证范围仍是 swap |
| liquidity.* 名字空间 | 文档保留；参考模型硬性返回 LP_EXECUTION_NOT_IMPLEMENTED |
| 多资产预算项 | asset_ref + max_debit_raw；同币聚合；swap 与 gas 已有用途 |
| Pool 与 Position 分离 | feed 拥有协议池状态，trade 拥有钱包实际权益 |
| opaque PositionRef | chain_id + manager + schema + opaque_id；不只用 pool_id，不绑死 NFT tokenId |

见 contracts/lp-position-ref.schema.json。schema 存在不代表 position reader 或交易 API 已实现。

能力状态固定为：
```json
{
  "observe_pool_liquidity": "unknown",
  "observe_wallet_position": "unknown",
  "execute": "not_implemented"
}
```

ModifyLiquidity 的事件 sender 不能未经 manager/receipt 归属验证就当用户 owner。[S06]
池 liquidity、钱包 token 余额、LP 权益、某时点 token0/token1 估值和未收费用分别建模；
估值必须带 block/state version，不把它当固定本金。

预算扩展点区分 max-debit 与 min-credit，避免将注资和撤资安全边界混为一谈；
本包不实现 LP preview。观察能力也不自动设为 supported。

离线测试覆盖 LP 写动作硬性拒绝、环境变量不能打开、观察能力不被误报，以及多资产预算原子性。
