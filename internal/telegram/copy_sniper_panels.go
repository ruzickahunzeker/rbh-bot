package telegram

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type copyWalletProfile struct {
	ProfileKey         string
	ChainID            uint64
	Address            string
	Label              string
	Enabled            bool
	ExecMode           string // "shadow" | "live"
	CopyBuy            bool
	CopySell           bool
	PoolFilter         string // "all" | "launchpad" | "dex" | "pons"
	SizingMode         string // "fixed" | "ratio"
	BuyAmountNative    string
	BuyRatioPct        int
	SellRatioPct       int
	MinMcapUSD         int64
	MaxMcapUSD         int64
	MinWalletBuyNative string
	MaxWalletBuyNative string
	GasTier            string
	AntiMEV            bool
	SlippageBps        int
	MaxDelaySec        int
	WalletID           string
}

func makeCopyProfileKey(chainID uint64, addrHex string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("cp:%d:%s", chainID, strings.ToLower(addrHex))))
	return hex.EncodeToString(sum[:4])
}

func (r *Router) ensureCopyProfile(ctx context.Context, chainID uint64, addrHex, defaultWallet string) copyWalletProfile {
	addrHex = strings.ToLower(addrHex)
	key := makeCopyProfileKey(chainID, addrHex)
	nowStr := r.now().UTC().Format(time.RFC3339Nano)
	_, _ = r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO copy_wallet_profiles(profile_key, chain_id, address, wallet_id, updated_at)
		 VALUES(?,?,?,?,?)`,
		key, chainID, addrHex, defaultWallet, nowStr,
	)
	p, err := r.loadCopyProfile(ctx, key)
	if err != nil {
		return copyWalletProfile{
			ProfileKey: key, ChainID: chainID, Address: addrHex, Enabled: true, ExecMode: "shadow",
			CopyBuy: true, CopySell: true, PoolFilter: "all", SizingMode: "fixed",
			BuyAmountNative: "0.1", BuyRatioPct: 20, SellRatioPct: 100,
			MinWalletBuyNative: "0", MaxWalletBuyNative: "0", GasTier: "fast",
			AntiMEV: true, SlippageBps: 1500, MaxDelaySec: 60, WalletID: defaultWallet,
		}
	}
	return p
}

func (r *Router) loadCopyProfile(ctx context.Context, key string) (copyWalletProfile, error) {
	var p copyWalletProfile
	var en, cb, cs, mev int
	err := r.readDB.QueryRowContext(ctx,
		`SELECT profile_key, chain_id, address, label, enabled, exec_mode, copy_buy, copy_sell,
		        pool_filter, sizing_mode, buy_amount_native, buy_ratio_pct, sell_ratio_pct,
		        min_mcap_usd, max_mcap_usd, min_wallet_buy_native, max_wallet_buy_native,
		        gas_tier, anti_mev, slippage_bps, max_delay_sec, wallet_id
		 FROM copy_wallet_profiles WHERE profile_key = ?`,
		key,
	).Scan(
		&p.ProfileKey, &p.ChainID, &p.Address, &p.Label, &en, &p.ExecMode, &cb, &cs,
		&p.PoolFilter, &p.SizingMode, &p.BuyAmountNative, &p.BuyRatioPct, &p.SellRatioPct,
		&p.MinMcapUSD, &p.MaxMcapUSD, &p.MinWalletBuyNative, &p.MaxWalletBuyNative,
		&p.GasTier, &mev, &p.SlippageBps, &p.MaxDelaySec, &p.WalletID,
	)
	if err != nil {
		return copyWalletProfile{}, err
	}
	p.Enabled = en == 1
	p.CopyBuy = cb == 1
	p.CopySell = cs == 1
	p.AntiMEV = mev == 1
	return p, nil
}

func (r *Router) saveCopyProfile(ctx context.Context, p copyWalletProfile) error {
	b2i := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE copy_wallet_profiles
		 SET label=?, enabled=?, exec_mode=?, copy_buy=?, copy_sell=?, pool_filter=?, sizing_mode=?,
		     buy_amount_native=?, buy_ratio_pct=?, sell_ratio_pct=?, min_mcap_usd=?, max_mcap_usd=?,
		     min_wallet_buy_native=?, max_wallet_buy_native=?, gas_tier=?, anti_mev=?, slippage_bps=?,
		     max_delay_sec=?, wallet_id=?, updated_at=?
		 WHERE profile_key = ?`,
		p.Label, b2i(p.Enabled), p.ExecMode, b2i(p.CopyBuy), b2i(p.CopySell), p.PoolFilter, p.SizingMode,
		p.BuyAmountNative, p.BuyRatioPct, p.SellRatioPct, p.MinMcapUSD, p.MaxMcapUSD,
		p.MinWalletBuyNative, p.MaxWalletBuyNative, p.GasTier, b2i(p.AntiMEV), p.SlippageBps,
		p.MaxDelaySec, p.WalletID, r.now().UTC().Format(time.RFC3339Nano), p.ProfileKey,
	)
	return err
}

func poolFilterCN(f string) string {
	switch f {
	case "launchpad":
		return "仅内盘 (Four.meme/Flap)"
	case "dex":
		return "仅外盘 (Pancake V2/V3)"
	case "pons":
		return "仅 Pons (RBH)"
	default:
		return "全部"
	}
}

func (r *Router) renderCopyProfileCard(ctx context.Context, sess chatSession, key, alert string) Reply {
	p, err := r.loadCopyProfile(ctx, key)
	if err != nil {
		return Reply{AlertText: "未找到该跟单策略"}
	}
	sym := chainNativeSymbol(p.ChainID)
	enLabel := "🟢 已启用"
	if !p.Enabled {
		enLabel = "⏸️ 已暂停"
	}
	modeLabel := "切 Live"
	modeText := "🟡 模拟盘"
	if p.ExecMode == "live" {
		modeLabel = "切 模拟"
		modeText = "🟢 实盘 (受全局闸门保护)"
	}
	fixedMark, ratioMark := "⚪", "⚪"
	amtRowBtn := fmt.Sprintf("买入金额: %s %s", p.BuyAmountNative, sym)
	amtCB := "cp:in_amt:" + key
	if p.SizingMode == "fixed" {
		fixedMark = "✅"
	} else {
		ratioMark = "✅"
		amtRowBtn = fmt.Sprintf("跟买比例: %d%%", p.BuyRatioPct)
		amtCB = "cp:in_ratio:" + key
	}
	mcapText := "不限"
	if p.MinMcapUSD > 0 || p.MaxMcapUSD > 0 {
		mcapText = fmt.Sprintf("$%d~$%d", p.MinMcapUSD, p.MaxMcapUSD)
	}
	rangeText := "不限"
	if p.MinWalletBuyNative != "0" || p.MaxWalletBuyNative != "0" {
		rangeText = fmt.Sprintf("%s~%s %s", p.MinWalletBuyNative, p.MaxWalletBuyNative, sym)
	}
	wLabel := p.WalletID
	if wLabel == "" {
		wLabel = sess.WalletID
	}
	if len(wLabel) > 12 {
		wLabel = wLabel[:12]
	}

	title := p.Address
	if p.Label != "" {
		title = fmt.Sprintf("%s (%s)", p.Label, p.Address)
	}
	text := strings.Join([]string{
		fmt.Sprintf("👥 跟单策略配置 | %s (%d)", chainDisplayName(p.ChainID), p.ChainID),
		"地址：" + title,
		fmt.Sprintf("状态：%s | 模式：%s", enLabel, modeText),
		fmt.Sprintf("市值区间：%s | 钱包买入区间：%s", mcapText, rangeText),
	}, "\n")

	return Reply{
		Text:         text,
		EditOriginal: true,
		AlertText:    alert,
		Buttons: [][]InlineButton{
			{{Text: enLabel, CallbackData: "cp:en:" + key}, {Text: modeLabel, CallbackData: "cp:mode:" + key}},
			{{Text: "跟买 " + boolOnOff(p.CopyBuy), CallbackData: "cp:cb:" + key}, {Text: "跟卖 " + boolOnOff(p.CopySell), CallbackData: "cp:cs:" + key}},
			{{Text: "Pool筛选: " + poolFilterCN(p.PoolFilter), CallbackData: "cp:pool:" + key}},
			{{Text: fixedMark + " 固定" + sym, CallbackData: "cp:sz_f:" + key}, {Text: ratioMark + " 跟买比例", CallbackData: "cp:sz_r:" + key}},
			{{Text: amtRowBtn, CallbackData: amtCB}},
			{{Text: fmt.Sprintf("卖出比例: %d%%", p.SellRatioPct), CallbackData: "cp:sellp:" + key}},
			{{Text: "最小/最大市值", CallbackData: "cp:in_mcap:" + key}, {Text: "钱包买入区间", CallbackData: "cp:in_rng:" + key}},
			{{Text: "⛽ Gas: " + gasTierCN(p.GasTier), CallbackData: "cp:gas:" + key}, {Text: "🛡️ MEV: " + boolOnOff(p.AntiMEV), CallbackData: "cp:mev:" + key}},
			{{Text: fmt.Sprintf("滑点: %.0f%%", float64(p.SlippageBps)/100.0), CallbackData: "cp:slip:" + key}, {Text: fmt.Sprintf("最大延迟: %ds", p.MaxDelaySec), CallbackData: "cp:delay:" + key}},
			{{Text: "👛 钱包: " + wLabel, CallbackData: "cp:wallet:" + key}, {Text: "🔄 刷新", CallbackData: "cp:open:" + key}},
			{{Text: "📋 复制地址", CallbackData: "cp:copyaddr:" + key}, {Text: "🗑️ 删除跟踪", CallbackData: "cp:del:" + key}},
			{{Text: "« 返回跟单", CallbackData: "copy"}, {Text: "🏠 主页", CallbackData: "menu"}},
		},
	}
}

func (r *Router) handleCopyProfileCallback(ctx context.Context, sess chatSession, data string) Reply {
	parts := strings.Split(data, ":")
	if len(parts) < 3 {
		return Reply{AlertText: "无效跟单操作"}
	}
	act, key := parts[1], parts[2]
	if act == "open" {
		return r.renderCopyProfileCard(ctx, sess, key, "")
	}
	p, err := r.loadCopyProfile(ctx, key)
	if err != nil {
		return Reply{AlertText: "跟单配置不存在"}
	}
	switch act {
	case "en":
		p.Enabled = !p.Enabled
		_ = r.saveCopyProfile(ctx, p)
		enInt := 0
		if p.Enabled {
			enInt = 1
		}
		_, _ = r.db.ExecContext(ctx, `UPDATE watched_wallets SET enabled = ? WHERE chain_id = ? AND address = ?`, enInt, p.ChainID, p.Address)
		if syncer, ok := r.backend.(interface{ SyncWatchlist(context.Context, uint64) error }); ok {
			_ = syncer.SyncWatchlist(ctx, p.ChainID)
		}
		return r.renderCopyProfileCard(ctx, sess, key, "状态已切换为："+boolOnOff(p.Enabled))
	case "mode":
		if p.ExecMode == "shadow" {
			p.ExecMode = "live"
		} else {
			p.ExecMode = "shadow"
		}
		_ = r.saveCopyProfile(ctx, p)
		return r.renderCopyProfileCard(ctx, sess, key, "执行模式已切换为："+p.ExecMode)
	case "cb":
		p.CopyBuy = !p.CopyBuy
		_ = r.saveCopyProfile(ctx, p)
		return r.renderCopyProfileCard(ctx, sess, key, "跟买："+boolOnOff(p.CopyBuy))
	case "cs":
		p.CopySell = !p.CopySell
		_ = r.saveCopyProfile(ctx, p)
		return r.renderCopyProfileCard(ctx, sess, key, "跟卖："+boolOnOff(p.CopySell))
	case "pool":
		switch p.PoolFilter {
		case "all":
			p.PoolFilter = "launchpad"
		case "launchpad":
			p.PoolFilter = "dex"
		case "dex":
			p.PoolFilter = "pons"
		default:
			p.PoolFilter = "all"
		}
		_ = r.saveCopyProfile(ctx, p)
		return r.renderCopyProfileCard(ctx, sess, key, "Pool筛选："+poolFilterCN(p.PoolFilter))
	case "sz_f":
		p.SizingMode = "fixed"
		_ = r.saveCopyProfile(ctx, p)
		return r.renderCopyProfileCard(ctx, sess, key, "已切换为固定金额买入")
	case "sz_r":
		p.SizingMode = "ratio"
		_ = r.saveCopyProfile(ctx, p)
		return r.renderCopyProfileCard(ctx, sess, key, "已切换为跟买比例模式")
	case "sellp":
		switch p.SellRatioPct {
		case 25:
			p.SellRatioPct = 50
		case 50:
			p.SellRatioPct = 75
		case 75:
			p.SellRatioPct = 100
		default:
			p.SellRatioPct = 25
		}
		_ = r.saveCopyProfile(ctx, p)
		return r.renderCopyProfileCard(ctx, sess, key, fmt.Sprintf("卖出比例：%d%%", p.SellRatioPct))
	case "gas":
		switch p.GasTier {
		case "standard":
			p.GasTier = "fast"
		case "fast":
			p.GasTier = "turbo"
		default:
			p.GasTier = "standard"
		}
		_ = r.saveCopyProfile(ctx, p)
		return r.renderCopyProfileCard(ctx, sess, key, "Gas 档位："+gasTierCN(p.GasTier))
	case "mev":
		if p.ChainID == 56 {
			p.AntiMEV = true
			return r.renderCopyProfileCard(ctx, sess, key, "BSC 强制开启 MEV 防夹保护")
		}
		p.AntiMEV = !p.AntiMEV
		_ = r.saveCopyProfile(ctx, p)
		return r.renderCopyProfileCard(ctx, sess, key, "MEV 防夹："+boolOnOff(p.AntiMEV))
	case "slip":
		switch p.SlippageBps {
		case 500:
			p.SlippageBps = 1000
		case 1000:
			p.SlippageBps = 1500
		case 1500:
			p.SlippageBps = 2500
		default:
			p.SlippageBps = 500
		}
		_ = r.saveCopyProfile(ctx, p)
		return r.renderCopyProfileCard(ctx, sess, key, fmt.Sprintf("滑点已设为 %.0f%%", float64(p.SlippageBps)/100.0))
	case "delay":
		switch p.MaxDelaySec {
		case 15:
			p.MaxDelaySec = 30
		case 30:
			p.MaxDelaySec = 60
		case 60:
			p.MaxDelaySec = 120
		default:
			p.MaxDelaySec = 15
		}
		_ = r.saveCopyProfile(ctx, p)
		return r.renderCopyProfileCard(ctx, sess, key, fmt.Sprintf("最大延迟：%ds", p.MaxDelaySec))
	case "wallet":
		wallets := r.syncAndListAuthorizedWallets(ctx, sess.ChatID, p.ChainID)
		if len(wallets) > 0 {
			next := wallets[0].WalletID
			for i, w := range wallets {
				if w.WalletID == p.WalletID {
					next = wallets[(i+1)%len(wallets)].WalletID
					break
				}
			}
			p.WalletID = next
			_ = r.saveCopyProfile(ctx, p)
		}
		return r.renderCopyProfileCard(ctx, sess, key, "执行钱包："+p.WalletID)
	case "copyaddr":
		return Reply{Text: p.Address, AlertText: "已发送地址便于长按复制"}
	case "del":
		_, _ = r.db.ExecContext(ctx, `DELETE FROM copy_wallet_profiles WHERE profile_key = ?`, key)
		if wb, ok := r.backend.(WatchlistManagerBackend); ok {
			_ = wb.RemoveWatchedWallet(ctx, p.ChainID, common.HexToAddress(p.Address))
		}
		rep := r.renderCopyCard(ctx, sess)
		rep.AlertText = "已删除跟踪地址"
		return rep
	case "in_amt":
		r.setPendingInput(ctx, sess.ChatID, "cp_amt", key)
		return Reply{Text: fmt.Sprintf("✏️ 请回复跟买固定金额（%s，例如 0.2）：", chainNativeSymbol(p.ChainID)), AlertText: "请输入金额"}
	case "in_ratio":
		r.setPendingInput(ctx, sess.ChatID, "cp_ratio", key)
		return Reply{Text: "✏️ 请回复跟买比例百分比（1~100，例如 25）：", AlertText: "请输入比例"}
	case "in_mcap":
		r.setPendingInput(ctx, sess.ChatID, "cp_mcap", key)
		return Reply{Text: "✏️ 请回复 最小市值 最大市值（单位 USD，0 表示不限，例如：10000 500000）：", AlertText: "请输入市值区间"}
	case "in_rng":
		r.setPendingInput(ctx, sess.ChatID, "cp_rng", key)
		return Reply{Text: fmt.Sprintf("✏️ 请回复聪明钱买入区间（%s，0 表示不限，例如：0.05 5）：", chainNativeSymbol(p.ChainID)), AlertText: "请输入买入区间"}
	}
	return Reply{AlertText: "未知跟单操作"}
}

// Sniper Task Deep Detail Card (M23)
func (r *Router) renderSniperDetailCard(ctx context.Context, sess chatSession, taskID, alert string) Reply {
	var tok, kind, amtStr, state, gasTier string
	var cid uint64
	var slipBps, antiMEV, hpCheck, maxTax, maxDelay, autoTPSL int
	err := r.readDB.QueryRowContext(ctx,
		`SELECT chain_id, token, trigger_kind, amount_in_wei, max_slippage_bps, anti_mev, honeypot_check, state, gas_tier, max_tax_bps, max_delay_blocks, auto_tpsl
		 FROM sniper_tasks WHERE task_id = ?`,
		taskID,
	).Scan(&cid, &tok, &kind, &amtStr, &slipBps, &antiMEV, &hpCheck, &state, &gasTier, &maxTax, &maxDelay, &autoTPSL)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return Reply{AlertText: "狙击任务不存在"}
	}
	amtWei, _ := new(big.Int).SetString(amtStr, 10)
	sym := chainNativeSymbol(cid)
	kindCN := "内盘毕业 (Migration)"
	if kind == "launch" {
		kindCN = "开盘狙击 (PairCreated)"
	}
	text := strings.Join([]string{
		fmt.Sprintf("🎯 狙击任务详情 | %s (%d)", chainDisplayName(cid), cid),
		"任务 ID：" + taskID,
		"目标合约：" + tok,
		fmt.Sprintf("状态：%s | 触发：%s", state, kindCN),
		fmt.Sprintf("投入：%s %s | 滑点：%.0f%% | 税率上限：%.0f%%", formatWeiToNative(amtWei), sym, float64(slipBps)/100.0, float64(maxTax)/100.0),
	}, "\n")
	return Reply{
		Text:         text,
		EditOriginal: true,
		AlertText:    alert,
		Buttons: [][]InlineButton{
			{{Text: "触发: " + kindCN, CallbackData: "sp:kind:" + taskID}, {Text: fmt.Sprintf("滑点: %.0f%%", float64(slipBps)/100.0), CallbackData: "sp:slip:" + taskID}},
			{{Text: "⛽ Gas: " + gasTierCN(gasTier), CallbackData: "sp:gas:" + taskID}, {Text: fmt.Sprintf("税率上限: %.0f%%", float64(maxTax)/100.0), CallbackData: "sp:tax:" + taskID}},
			{{Text: fmt.Sprintf("最大延迟: %d区块", maxDelay), CallbackData: "sp:delay:" + taskID}, {Text: "🎯 自动止盈止损: " + boolOnOff(autoTPSL == 1), CallbackData: "sp:tpsl:" + taskID}},
			{{Text: "🛡️ 防貔貅: " + boolOnOff(hpCheck == 1), CallbackData: "sp:hp:" + taskID}, {Text: "❌ 撤防删除", CallbackData: "sn:del:" + taskID}},
			{{Text: "« 返回狙击", CallbackData: "sniper"}, {Text: "🏠 主页", CallbackData: "menu"}},
		},
	}
}

func (r *Router) handleSniperDetailCallback(ctx context.Context, sess chatSession, data string) Reply {
	parts := strings.Split(data, ":")
	if len(parts) < 3 {
		return Reply{AlertText: "无效狙击操作"}
	}
	act, tid := parts[1], parts[2]
	if act == "open" {
		return r.renderSniperDetailCard(ctx, sess, tid, "")
	}
	nowStr := r.now().UTC().Format(time.RFC3339Nano)
	switch act {
	case "kind":
		_, _ = r.db.ExecContext(ctx, `UPDATE sniper_tasks SET trigger_kind = CASE WHEN trigger_kind='launch' THEN 'migration' ELSE 'launch' END, updated_at=? WHERE task_id=?`, nowStr, tid)
	case "slip":
		_, _ = r.db.ExecContext(ctx, `UPDATE sniper_tasks SET max_slippage_bps = CASE max_slippage_bps WHEN 1000 THEN 1500 WHEN 1500 THEN 2500 WHEN 2500 THEN 3500 ELSE 1000 END, updated_at=? WHERE task_id=?`, nowStr, tid)
	case "gas":
		_, _ = r.db.ExecContext(ctx, `UPDATE sniper_tasks SET gas_tier = CASE gas_tier WHEN 'standard' THEN 'fast' WHEN 'fast' THEN 'turbo' ELSE 'standard' END, updated_at=? WHERE task_id=?`, nowStr, tid)
	case "tax":
		_, _ = r.db.ExecContext(ctx, `UPDATE sniper_tasks SET max_tax_bps = CASE max_tax_bps WHEN 500 THEN 1000 WHEN 1000 THEN 2000 ELSE 500 END, updated_at=? WHERE task_id=?`, nowStr, tid)
	case "delay":
		_, _ = r.db.ExecContext(ctx, `UPDATE sniper_tasks SET max_delay_blocks = CASE max_delay_blocks WHEN 1 THEN 3 WHEN 3 THEN 5 ELSE 1 END, updated_at=? WHERE task_id=?`, nowStr, tid)
	case "tpsl":
		_, _ = r.db.ExecContext(ctx, `UPDATE sniper_tasks SET auto_tpsl = 1 - auto_tpsl, updated_at=? WHERE task_id=?`, nowStr, tid)
	case "hp":
		_, _ = r.db.ExecContext(ctx, `UPDATE sniper_tasks SET honeypot_check = 1 - honeypot_check, updated_at=? WHERE task_id=?`, nowStr, tid)
	}
	return r.renderSniperDetailCard(ctx, sess, tid, "狙击参数已更新")
}

func (r *Router) handleCopyProfilePendingInput(ctx context.Context, sess chatSession, action, key, input string) (Reply, bool) {
	p, err := r.loadCopyProfile(ctx, key)
	if err != nil {
		return Reply{Text: "❌ 跟单配置不存在。"}, true
	}
	switch action {
	case "cp_amt":
		if parseNativeToWei(input) == nil {
			return Reply{Text: "❌ 请输入正数金额，例如 0.2"}, true
		}
		p.BuyAmountNative = strings.TrimSpace(input)
		p.SizingMode = "fixed"
		_ = r.saveCopyProfile(ctx, p)
		rep := r.renderCopyProfileCard(ctx, sess, key, "跟买金额已更新")
		rep.EditOriginal = false
		return rep, true
	case "cp_ratio":
		v, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(input), "%"))
		if err != nil || v < 1 || v > 100 {
			return Reply{Text: "❌ 请输入 1~100 的比例整数"}, true
		}
		p.BuyRatioPct = v
		p.SizingMode = "ratio"
		_ = r.saveCopyProfile(ctx, p)
		rep := r.renderCopyProfileCard(ctx, sess, key, "跟买比例已更新")
		rep.EditOriginal = false
		return rep, true
	case "cp_mcap":
		f := strings.Fields(input)
		if len(f) != 2 {
			return Reply{Text: "❌ 请输入两个数字：最小市值 最大市值（例如 10000 500000）"}, true
		}
		mn, e1 := strconv.ParseInt(f[0], 10, 64)
		mx, e2 := strconv.ParseInt(f[1], 10, 64)
		if e1 != nil || e2 != nil || mn < 0 || mx < 0 {
			return Reply{Text: "❌ 市值数字无效"}, true
		}
		p.MinMcapUSD, p.MaxMcapUSD = mn, mx
		_ = r.saveCopyProfile(ctx, p)
		rep := r.renderCopyProfileCard(ctx, sess, key, "市值过滤区间已更新")
		rep.EditOriginal = false
		return rep, true
	case "cp_rng":
		f := strings.Fields(input)
		if len(f) != 2 {
			return Reply{Text: "❌ 请输入两个数字：最小买入 最大买入（例如 0.05 5）"}, true
		}
		p.MinWalletBuyNative, p.MaxWalletBuyNative = f[0], f[1]
		_ = r.saveCopyProfile(ctx, p)
		rep := r.renderCopyProfileCard(ctx, sess, key, "钱包买入区间已更新")
		rep.EditOriginal = false
		return rep, true
	}
	return Reply{}, false
}
