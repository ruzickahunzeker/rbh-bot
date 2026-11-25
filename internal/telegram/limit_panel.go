package telegram

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// supportedChains is the ordered multi-chain ring for the top [ ◀️ ] [ 🌐 Chain ] [ ▶️ ] bar.
// Adding new chains later (e.g. Base 8453, ETH 1, ARB 42161) only requires appending here.
var supportedChains = []uint64{56, 4663}

func nextChainInRing(current uint64, delta int) uint64 {
	idx := 0
	for i, cid := range supportedChains {
		if cid == current {
			idx = i
			break
		}
	}
	n := len(supportedChains)
	nextIdx := (idx + delta + n) % n
	return supportedChains[nextIdx]
}

func defaultWalletForChain(chainID uint64) string {
	switch chainID {
	case 1:
		return "wallet-eth-readonly"
	case 56:
		return "wallet-bsc-readonly"
	case 999:
		return "wallet-hype-readonly"
	case 5042:
		return "wallet-arc-readonly"
	case 8453:
		return "wallet-base-readonly"
	case 99901:
		return "wallet-ely-readonly"
	default:
		return "wallet-rbh-readonly"
	}
}

func chainTopSwitcherRow(chainID uint64, prevCB, nextCB string) []InlineButton {
	return []InlineButton{
		{Text: "◀️", CallbackData: prevCB},
		{Text: fmt.Sprintf("🌐 %s (%d)", chainDisplayName(chainID), chainID), CallbackData: "noop"},
		{Text: "▶️", CallbackData: nextCB},
	}
}

// executeRelativeLimitOrder creates a Limit Buy, Take-Profit, or Stop-Loss order relative to
// the token's live quoted spot price (e.g. -10% dip buy, +100% TP sell 50%, -30% SL sell 100%).
// Callback format:
//   - lo:buy:<tokKey>:<pctDelta>       (e.g. -10 -> 90% of spot, uses first buy preset)
//   - lo:tp:<tokKey>:<pctDelta>:<sellPct>
//   - lo:sl:<tokKey>:<pctDelta>:<sellPct>
func (r *Router) executeRelativeLimitOrder(ctx context.Context, sess chatSession, data string) Reply {
	parts := strings.Split(data, ":")
	if len(parts) < 4 {
		return Reply{AlertText: "无效限价单参数"}
	}
	kindCode := parts[1]
	tokKey := parts[2]
	pctDelta, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return Reply{AlertText: "无效涨跌幅参数"}
	}
	cid, tok, _, err := r.lookupUIToken(ctx, tokKey)
	if err != nil {
		return Reply{AlertText: "代币缓存已过期，请重新发送合约地址"}
	}
	sess.ChainID = cid
	pb, ok := r.backend.(ProbeQuoteBackend)
	if !ok {
		return Reply{AlertText: "报价服务未启用"}
	}
	probeWei := parseNativeToWei("0.01")
	_, _, outRaw, _, err := pb.ProbeTokenQuote(ctx, sess.ChainID, sess.WalletID, tok, "buy", probeWei)
	if err != nil || outRaw == nil || outRaw.Sign() <= 0 {
		rep := r.renderTokenCard(ctx, sess, tok, "limit", true, "❌ 无法获取当前参考价，请确认路由可用。")
		rep.AlertText = "获取参考价失败"
		return rep
	}
	spot := new(big.Int).Mul(probeWei, big.NewInt(1_000_000_000))
	spot.Div(spot, outRaw)
	if spot.Sign() <= 0 {
		spot = big.NewInt(1)
	}
	mult := 100 + pctDelta
	if mult <= 1 {
		mult = 1
	}
	trigPrice := new(big.Int).Mul(spot, big.NewInt(mult))
	trigPrice.Div(trigPrice, big.NewInt(100))
	if trigPrice.Sign() <= 0 {
		trigPrice = big.NewInt(1)
	}

	nowUTC := r.now().UTC()
	expStr := nowUTC.Add(24 * time.Hour).Format(time.RFC3339Nano)
	nowStr := nowUTC.Format(time.RFC3339Nano)

	var (
		orderKind string
		amtStr    = "0"
		sellBps   uint64
		summary   string
	)
	switch kindCode {
	case "buy":
		orderKind = "limit_buy"
		st := r.loadBloomSettings(ctx, sess.ChatID)
		firstPreset := "0.1"
		if p := strings.Split(st.BuyPresetsCSV, ","); len(p) > 0 && strings.TrimSpace(p[0]) != "" {
			firstPreset = strings.TrimSpace(p[0])
		}
		amtWei := parseNativeToWei(firstPreset)
		if amtWei == nil || amtWei.Sign() <= 0 {
			amtWei = parseNativeToWei("0.1")
		}
		amtStr = amtWei.String()
		summary = fmt.Sprintf("限价买入 %s %s（较现价 %+d%%）", firstPreset, chainNativeSymbol(sess.ChainID), pctDelta)
	case "tp", "sl":
		if kindCode == "tp" {
			orderKind = "take_profit"
		} else {
			orderKind = "stop_loss"
		}
		sellPct := uint64(100)
		if len(parts) >= 5 {
			if p, pErr := strconv.ParseUint(parts[4], 10, 64); pErr == nil && p > 0 && p <= 100 {
				sellPct = p
			}
		}
		sellBps = sellPct * 100
		label := "止盈"
		if kindCode == "sl" {
			label = "止损"
		}
		summary = fmt.Sprintf("%s卖出 %d%%（较现价 %+d%%）", label, sellPct, pctDelta)
	default:
		return Reply{AlertText: "不支持的限价单类型"}
	}

	oid := fmt.Sprintf("lo-%s-%d-%d", kindCode, sess.ChainID, nowUTC.UnixNano())
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO conditional_orders(order_id, chain_id, wallet_id, token, order_kind, trigger_price_wei, amount_raw, sell_bps, idempotency_key, state, expires_at, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,'ACTIVE',?,?,?)`,
		oid, sess.ChainID, sess.WalletID, strings.ToLower(tok.Hex()), orderKind,
		trigPrice.String(), amtStr, sellBps, "ui-"+oid, expStr, nowStr, nowStr,
	); err != nil {
		rep := r.renderTokenCard(ctx, sess, tok, "limit", true, "❌ 创建限价单失败: "+err.Error())
		rep.AlertText = "挂单失败"
		return rep
	}

	banner := fmt.Sprintf("✅ 已挂单 %s\n%s | 触发价=%s | 有效期 24h", oid, summary, trigPrice.String())
	rep := r.renderTokenCard(ctx, sess, tok, "limit", true, banner)
	rep.AlertText = "限价单已挂出！"
	return rep
}

func (r *Router) cycleTokenCardWallet(ctx context.Context, sess chatSession, tokKey, mode string) Reply {
	cid, tok, _, err := r.lookupUIToken(ctx, tokKey)
	if err != nil {
		return Reply{AlertText: "代币缓存已过期"}
	}
	sess.ChainID = cid
	wallets, err := r.backend.ListWallets(ctx, sess.ChainID)
	if err != nil || len(wallets) == 0 {
		return Reply{AlertText: "当前链暂无其他钱包可切换"}
	}
	var ids []string
	for _, w := range wallets {
		if f := strings.Fields(w); len(f) > 0 {
			ids = append(ids, f[0])
		}
	}
	if len(ids) == 0 {
		return Reply{AlertText: "无可用钱包"}
	}
	nextID := ids[0]
	for i, id := range ids {
		if id == sess.WalletID {
			nextID = ids[(i+1)%len(ids)]
			break
		}
	}
	_, _ = r.db.ExecContext(ctx,
		`UPDATE telegram_allowlist SET active_wallet_id = ?, updated_at = ? WHERE chat_id = ?`,
		nextID, r.now().UTC().Format(time.RFC3339Nano), sess.ChatID,
	)
	sess.WalletID = nextID
	rep := r.renderTokenCard(ctx, sess, tok, mode, true, "")
	rep.AlertText = "已切换执行钱包：" + nextID
	return rep
}
