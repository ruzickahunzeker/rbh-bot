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

// InlineButton represents one Telegram InlineKeyboardButton.
type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// Reply is a rich Telegram response supporting Inline Keyboards, in-place message editing,
// callback toast notifications, optional HTML parse mode, and auto-delete for sensitive secrets.
type Reply struct {
	Text              string
	Buttons           [][]InlineButton
	EditOriginal      bool
	AlertText         string
	ParseMode         string
	AutoDeleteSec     int
	DeleteUserMessage bool
}

// ProbeQuoteBackend is optionally implemented by Backend for Paste-CA auto-routing and one-tap Shadow trades.
type ProbeQuoteBackend interface {
	ProbeTokenQuote(ctx context.Context, chainID uint64, walletID string, token common.Address, side string, amountRaw *big.Int) (protocol string, poolPhase string, expectedOut *big.Int, quoteBlock uint64, err error)
}

type bloomSettings struct {
	BuyPresetsCSV     string
	BuySlippageBps    uint64
	SellSlippageBps   uint64
	GasTier           string
	AntiMEV           bool
	ConfirmBeforeLive bool
	AutoTPSL          bool
}

type chatSession struct {
	ChatID   int64
	Role     string
	ChainID  uint64
	WalletID string
}

func (r *Router) loadSession(ctx context.Context, chatID int64) (chatSession, error) {
	var s chatSession
	s.ChatID = chatID
	var enabled int
	err := r.readDB.QueryRowContext(ctx,
		`SELECT role, active_chain_id, active_wallet_id, enabled FROM telegram_allowlist WHERE chat_id = ?`,
		chatID,
	).Scan(&s.Role, &s.ChainID, &s.WalletID, &enabled)
	if errors.Is(err, sql.ErrNoRows) || enabled != 1 {
		return chatSession{}, ErrUnauthorizedChat
	}
	if err != nil {
		return chatSession{}, err
	}
	return s, nil
}

func (r *Router) loadBloomSettings(ctx context.Context, chatID int64) bloomSettings {
	st := bloomSettings{
		BuyPresetsCSV:     "0.05,0.1,0.25,0.5,1",
		BuySlippageBps:    500,
		SellSlippageBps:   1000,
		GasTier:           "fast",
		AntiMEV:           true,
		ConfirmBeforeLive: true,
	}
	var antiMEV, confirm, autoTPSL int
	err := r.readDB.QueryRowContext(ctx,
		`SELECT buy_presets_csv, buy_slippage_bps, sell_slippage_bps, gas_tier, anti_mev, confirm_before_live, auto_tpsl
		 FROM bloom_user_settings WHERE chat_id = ?`,
		chatID,
	).Scan(&st.BuyPresetsCSV, &st.BuySlippageBps, &st.SellSlippageBps, &st.GasTier, &antiMEV, &confirm, &autoTPSL)
	if err == nil {
		st.AntiMEV = antiMEV == 1
		st.ConfirmBeforeLive = confirm == 1
		st.AutoTPSL = autoTPSL == 1
	}
	return st
}

func (r *Router) saveBloomSettings(ctx context.Context, chatID int64, st bloomSettings) error {
	antiInt, confInt, autoInt := 0, 0, 0
	if st.AntiMEV {
		antiInt = 1
	}
	if st.ConfirmBeforeLive {
		confInt = 1
	}
	if st.AutoTPSL {
		autoInt = 1
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO bloom_user_settings(chat_id, buy_presets_csv, buy_slippage_bps, sell_slippage_bps, gas_tier, anti_mev, confirm_before_live, auto_tpsl, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(chat_id) DO UPDATE SET
		   buy_presets_csv=excluded.buy_presets_csv,
		   buy_slippage_bps=excluded.buy_slippage_bps,
		   sell_slippage_bps=excluded.sell_slippage_bps,
		   gas_tier=excluded.gas_tier,
		   anti_mev=excluded.anti_mev,
		   confirm_before_live=excluded.confirm_before_live,
		   auto_tpsl=excluded.auto_tpsl,
		   updated_at=excluded.updated_at`,
		chatID, st.BuyPresetsCSV, st.BuySlippageBps, st.SellSlippageBps, st.GasTier, antiInt, confInt, autoInt,
		r.now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (r *Router) registerUIToken(ctx context.Context, chainID uint64, token common.Address, protocolID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", chainID, strings.ToLower(token.Hex()))))
	key := hex.EncodeToString(sum[:4]) // 8 hex chars
	_, _ = r.db.ExecContext(ctx,
		`INSERT INTO bloom_ui_tokens(token_key, chain_id, token_address, protocol_id, updated_at)
		 VALUES(?,?,?,?,?)
		 ON CONFLICT(token_key) DO UPDATE SET protocol_id=excluded.protocol_id, updated_at=excluded.updated_at`,
		key, chainID, strings.ToLower(token.Hex()), protocolID, r.now().UTC().Format(time.RFC3339Nano),
	)
	return key
}

func (r *Router) lookupUIToken(ctx context.Context, key string) (uint64, common.Address, string, error) {
	var cid uint64
	var addrHex, proto string
	err := r.readDB.QueryRowContext(ctx,
		`SELECT chain_id, token_address, protocol_id FROM bloom_ui_tokens WHERE token_key = ?`,
		key,
	).Scan(&cid, &addrHex, &proto)
	if err != nil {
		return 0, common.Address{}, "", err
	}
	return cid, common.HexToAddress(addrHex), proto, nil
}

// HandleMessage routes Telegram text messages:
//   - /start or /menu -> Bloom EVM Main Dashboard with Inline Keyboard
//   - bare 0x... EVM contract address -> Bloom Instant Token Card (auto-route + honeypot + quick buy/sell)
//   - all other /commands -> backwards-compatible HandleCommand
func (r *Router) HandleMessage(ctx context.Context, chatID int64, text string) (Reply, error) {
	sess, err := r.loadSession(ctx, chatID)
	if err != nil {
		return Reply{}, err
	}
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	if lower == "/start" || lower == "/menu" {
		return r.renderMainMenu(ctx, sess, false), nil
	}
	if lower == "/report" {
		rep := r.renderDailyReportCard(ctx, sess)
		rep.EditOriginal = false
		return rep, nil
	}
	// Check if user is answering an interactive prompt (e.g. custom buy amount, add watch wallet, new sniper).
	if !strings.HasPrefix(trimmed, "/") {
		if action, targetKey, ok := r.consumePendingInput(ctx, chatID); ok {
			if rep, handled := r.handlePendingInputReply(ctx, sess, action, targetKey, trimmed); handled {
				return rep, nil
			}
		}
	}
	// Signature UX: paste any 42-char EVM contract address to pop up the interactive Token Card.
	if len(trimmed) == 42 && common.IsHexAddress(trimmed) {
		return r.renderTokenCard(ctx, sess, common.HexToAddress(trimmed), "buy", false, ""), nil
	}
	plain, err := r.HandleCommand(ctx, chatID, trimmed)
	if err != nil {
		return Reply{}, err
	}
	return Reply{Text: plain}, nil
}

// HandleCallback authenticates chatID and dispatches Inline Keyboard button callbacks.
func (r *Router) HandleCallback(ctx context.Context, chatID int64, data string) (Reply, error) {
	sess, err := r.loadSession(ctx, chatID)
	if err != nil {
		return Reply{}, err
	}
	switch {
	case data == "noop":
		return Reply{AlertText: fmt.Sprintf("当前链：%s (%d)", chainDisplayName(sess.ChainID), sess.ChainID)}, nil

	case data == "menu":
		return r.renderMainMenu(ctx, sess, true), nil

	case data == "sw_chain", data == "sw_c:next", data == "sw_c:prev":
		delta := 1
		if data == "sw_c:prev" {
			delta = -1
		}
		newChain := r.nextChain(sess.ChainID, delta)
		if _, err := r.db.ExecContext(ctx,
			`UPDATE telegram_allowlist SET active_chain_id = ?, updated_at = ? WHERE chat_id = ?`,
			newChain, r.now().UTC().Format(time.RFC3339Nano), chatID,
		); err != nil {
			return Reply{}, err
		}
		sess.ChainID = newChain
		rep := r.renderMainMenu(ctx, sess, true)
		rep.AlertText = fmt.Sprintf("已切换至 %s (%d)（保留钱包 %s）", chainDisplayName(newChain), newChain, sess.WalletID)
		return rep, nil

	case data == "status":
		txt := "状态服务暂不可用。"
		if sb, ok := r.backend.(StatusBackend); ok {
			if s, err := sb.CheckStatus(ctx); err == nil {
				txt = s
			} else {
				txt = "状态查询失败: " + err.Error()
			}
		}
		return Reply{
			Text:         txt,
			EditOriginal: true,
			Buttons: [][]InlineButton{
				{{Text: "🔄 刷新", CallbackData: "status"}, {Text: "⬅️ 返回主菜单", CallbackData: "menu"}},
			},
		}, nil

	case data == "wallets":
		return r.renderWalletManagerCard(ctx, sess, ""), nil

	case strings.HasPrefix(data, "wm:"):
		return r.handleWalletManagerCallback(ctx, sess, data), nil

	case strings.HasPrefix(data, "br:"):
		return r.handleBridgeCallback(ctx, sess, data), nil

	case strings.HasPrefix(data, "cp:"):
		return r.handleCopyProfileCallback(ctx, sess, data), nil

	case strings.HasPrefix(data, "sp:"):
		return r.handleSniperDetailCallback(ctx, sess, data), nil

	case strings.HasPrefix(data, "sw_w:"):
		newWalletID := strings.TrimPrefix(data, "sw_w:")
		if newWalletID == "" {
			return Reply{AlertText: "无效钱包 ID"}, nil
		}
		if _, ok := r.resolveAuthorizedWallet(ctx, chatID, sess.ChainID, newWalletID); !ok {
			return Reply{AlertText: "无权切换到该钱包或钱包不存在"}, nil
		}
		_ = r.setActiveWalletID(ctx, chatID, newWalletID)
		sess.WalletID = newWalletID
		rep := r.renderMainMenu(ctx, sess, true)
		rep.AlertText = "已切换活跃钱包为 " + newWalletID
		return rep, nil

	case data == "report":
		return r.renderDailyReportCard(ctx, sess), nil

	case strings.HasPrefix(data, "in:"):
		// Interactive input prompts: in:buy:<tokKey>, in:sell:<tokKey>, in:watch, in:snipe, in:presets
		parts := strings.Split(data, ":")
		if len(parts) < 2 {
			return Reply{AlertText: "无效输入请求"}, nil
		}
		action := parts[1]
		targetKey := ""
		if len(parts) >= 3 {
			targetKey = parts[2]
		}
		r.setPendingInput(ctx, chatID, action, targetKey)
		var prompt string
		switch action {
		case "buy":
			prompt = fmt.Sprintf("✏️ 请直接回复你要买入的 %s 数量（例如：0.25）：", chainNativeSymbol(sess.ChainID))
		case "sell":
			prompt = "✏️ 请直接回复你要卖出的百分比（1~100，例如：35 或 50%）："
		case "watch":
			prompt = "➕ 请直接回复要监控的聪明钱钱包地址（0x...）："
		case "snipe":
			prompt = fmt.Sprintf("🎯 请直接回复狙击参数：<代币合约> <买入%s> [launch|migration]\n示例：0x55d398326f99059fF775485246999027B3197955 0.05 migration", chainNativeSymbol(sess.ChainID))
		case "presets":
			prompt = "✏️ 请直接回复 1~4 个逗号分隔的快捷买入金额（例如：0.02,0.05,0.1,0.5）："
		default:
			return Reply{AlertText: "未知输入类型"}, nil
		}
		return Reply{Text: prompt, AlertText: "请在输入框直接回复"}, nil

	case data == "positions":
		return r.renderPositionsCard(ctx, sess), nil

	case data == "copy":
		return r.renderCopyCard(ctx, sess), nil

	case data == "orders":
		return r.renderOrdersCard(ctx, sess), nil

	case data == "sniper":
		return r.renderSniperCard(ctx, sess), nil

	case strings.HasPrefix(data, "sn:del:"):
		taskID := strings.TrimPrefix(data, "sn:del:")
		_, _ = r.db.ExecContext(ctx,
			`UPDATE sniper_tasks SET state = 'CANCELLED', updated_at = ? WHERE task_id = ? AND chain_id = ? AND state = 'ARMED'`,
			r.now().UTC().Format(time.RFC3339Nano), taskID, sess.ChainID,
		)
		if syncer, ok := r.backend.(interface{ SyncWatchlist(context.Context, uint64) error }); ok {
			_ = syncer.SyncWatchlist(ctx, sess.ChainID)
		}
		rep := r.renderSniperCard(ctx, sess)
		rep.AlertText = "已撤防狙击任务 " + taskID
		return rep, nil

	case data == "settings":
		return r.renderSettingsCard(ctx, sess, ""), nil

	case strings.HasPrefix(data, "set:"):
		return r.handleSettingToggle(ctx, sess, strings.TrimPrefix(data, "set:")), nil

	case strings.HasPrefix(data, "cw:del:"):
		addrHex := strings.TrimPrefix(data, "cw:del:")
		if wb, ok := r.backend.(WatchlistManagerBackend); ok && common.IsHexAddress(addrHex) {
			_ = wb.RemoveWatchedWallet(ctx, sess.ChainID, common.HexToAddress(addrHex))
		}
		rep := r.renderCopyCard(ctx, sess)
		rep.AlertText = "已移除监控地址"
		return rep, nil

	case strings.HasPrefix(data, "co:del:"):
		orderID := strings.TrimPrefix(data, "co:del:")
		_, _ = r.db.ExecContext(ctx,
			`UPDATE conditional_orders SET state = 'CANCELLED', updated_at = ? WHERE order_id = ? AND chain_id = ? AND state = 'ACTIVE'`,
			r.now().UTC().Format(time.RFC3339Nano), orderID, sess.ChainID,
		)
		rep := r.renderOrdersCard(ctx, sess)
		rep.AlertText = "已撤销条件单 " + orderID
		return rep, nil

	case data == "co:delall":
		_, _ = r.db.ExecContext(ctx,
			`UPDATE conditional_orders SET state = 'CANCELLED', updated_at = ? WHERE chain_id = ? AND state = 'ACTIVE'`,
			r.now().UTC().Format(time.RFC3339Nano), sess.ChainID,
		)
		rep := r.renderOrdersCard(ctx, sess)
		rep.AlertText = "已撤销当前链全部活跃条件单"
		return rep, nil

	case strings.HasPrefix(data, "lo:"):
		return r.executeRelativeLimitOrder(ctx, sess, data), nil

	case strings.HasPrefix(data, "tc_c:"):
		// tc_c:<tokKey>:<mode>:<prev|next> -> switch chain and re-probe same token CA!
		parts := strings.Split(data, ":")
		if len(parts) != 4 {
			return Reply{AlertText: "无效切链参数"}, nil
		}
		_, tok, _, err := r.lookupUIToken(ctx, parts[1])
		if err != nil {
			return Reply{AlertText: "代币缓存已过期，请重新发送合约地址"}, nil
		}
		delta := 1
		if parts[3] == "prev" {
			delta = -1
		}
		newChain := r.nextChain(sess.ChainID, delta)
		_, _ = r.db.ExecContext(ctx,
			`UPDATE telegram_allowlist SET active_chain_id = ?, updated_at = ? WHERE chat_id = ?`,
			newChain, r.now().UTC().Format(time.RFC3339Nano), chatID,
		)
		sess.ChainID = newChain
		rep := r.renderTokenCard(ctx, sess, tok, parts[2], true, "")
		rep.AlertText = fmt.Sprintf("已切换至 %s (%d) 并重新探测合约（保留钱包 %s）", chainDisplayName(newChain), newChain, sess.WalletID)
		return rep, nil

	case strings.HasPrefix(data, "tc_g:"):
		// tc_g:<tokKey>:<mode> -> cycle gas tier directly inside Token Card
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			return Reply{AlertText: "无效 Gas 参数"}, nil
		}
		cid, tok, _, err := r.lookupUIToken(ctx, parts[1])
		if err != nil {
			return Reply{AlertText: "代币缓存已过期"}, nil
		}
		sess.ChainID = cid
		st := r.loadBloomSettings(ctx, sess.ChatID)
		switch st.GasTier {
		case "standard":
			st.GasTier = "fast"
		case "fast":
			st.GasTier = "turbo"
		default:
			st.GasTier = "standard"
		}
		_ = r.saveBloomSettings(ctx, sess.ChatID, st)
		rep := r.renderTokenCard(ctx, sess, tok, parts[2], true, "")
		rep.AlertText = "Gas 档位已切换为：" + gasTierCN(st.GasTier)
		return rep, nil

	case strings.HasPrefix(data, "tc_w:"):
		// tc_w:<tokKey>:<mode> -> cycle wallet directly inside Token Card
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			return Reply{AlertText: "无效钱包参数"}, nil
		}
		return r.cycleTokenCardWallet(ctx, sess, parts[1], parts[2]), nil

	case strings.HasPrefix(data, "tc:"):
		// tc:<tokKey>:<buy|sell|limit>
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			return Reply{AlertText: "无效的代币卡片操作"}, nil
		}
		cid, tok, _, err := r.lookupUIToken(ctx, parts[1])
		if err != nil {
			return Reply{AlertText: "代币缓存已过期，请重新发送合约地址"}, nil
		}
		sess.ChainID = cid
		return r.renderTokenCard(ctx, sess, tok, parts[2], true, ""), nil

	case strings.HasPrefix(data, "sb:"):
		// sb:<tokKey>:<amtNative>
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			return Reply{AlertText: "无效的买入操作"}, nil
		}
		return r.executeShadowBuyButton(ctx, sess, parts[1], parts[2]), nil

	case strings.HasPrefix(data, "ss:"):
		// ss:<tokKey>:<pct>
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			return Reply{AlertText: "无效的卖出操作"}, nil
		}
		return r.executeShadowSellButton(ctx, sess, parts[1], parts[2]), nil

	default:
		return Reply{AlertText: "未知操作"}, nil
	}
}

func (r *Router) renderMainMenu(ctx context.Context, sess chatSession, edit bool) Reply {
	chainName := chainDisplayName(sess.ChainID)
	sym := chainNativeSymbol(sess.ChainID)
	var watchedCnt, ordersCnt, shadowFills int
	_ = r.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM watched_wallets WHERE chain_id = ? AND enabled = 1`, sess.ChainID).Scan(&watchedCnt)
	_ = r.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM conditional_orders WHERE chain_id = ? AND state = 'ACTIVE'`, sess.ChainID).Scan(&ordersCnt)
	_ = r.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM paper_shadow_fills WHERE chain_id = ? AND wallet_id = ?`, sess.ChainID, sess.WalletID).Scan(&shadowFills)

	text := strings.Join([]string{
		fmt.Sprintf("⚡ evm-bot 交易终端 | %s (%d)", chainName, sess.ChainID),
		"当前模式：🟡 模拟盘 / 只读模式 (live_enabled=false)",
		fmt.Sprintf("活跃钱包：%s (%s)", sess.WalletID, sym),
		fmt.Sprintf("监控地址：%d | 条件单：%d | 模拟成交：%d", watchedCnt, ordersCnt, shadowFills),
		"",
		"💡 直接发送任意 0x... 合约地址，秒出代币交易卡（自动探测内盘/DEX + 貔貅检测 + 一键模拟买卖）。",
	}, "\n")

	return Reply{
		Text:         text,
		EditOriginal: edit,
		Buttons: [][]InlineButton{
			chainTopSwitcherRow(sess.ChainID, "sw_c:prev", "sw_c:next"),
			{{Text: "📊 持仓与盈亏", CallbackData: "positions"}, {Text: "🎯 狙击任务", CallbackData: "sniper"}},
			{{Text: "👥 聪明钱跟单", CallbackData: "copy"}, {Text: "📈 限价/止盈止损", CallbackData: "orders"}},
			{{Text: "👛 钱包管理", CallbackData: "wallets"}, {Text: "🌉 跨链桥", CallbackData: "br:home"}},
			{{Text: "⚙️ 交易设置", CallbackData: "settings"}, {Text: "📡 系统状态", CallbackData: "status"}},
			{{Text: "📑 今日复盘", CallbackData: "report"}},
		},
	}
}

func (r *Router) renderTokenCard(ctx context.Context, sess chatSession, token common.Address, mode string, edit bool, banner string) Reply {
	if mode != "sell" && mode != "limit" {
		mode = "buy"
	}
	st := r.loadBloomSettings(ctx, sess.ChatID)
	sym := chainNativeSymbol(sess.ChainID)
	probeAmt := parseNativeToWei("0.01")

	tokSym, tokDec := r.tokenMeta(ctx, sess.ChainID, token)
	protoLabel := "未匹配路由"
	quoteLine := "报价: 暂不可用"
	detectedProto := ""
	if pb, ok := r.backend.(ProbeQuoteBackend); ok {
		proto, phase, out, blk, err := pb.ProbeTokenQuote(ctx, sess.ChainID, sess.WalletID, token, "buy", probeAmt)
		if err == nil {
			detectedProto = proto
			protoLabel = fmt.Sprintf("%s (%s) @ 区块 %d", proto, phase, blk)
			quoteLine = fmt.Sprintf("报价: 0.01 %s ≈ %s %s", sym, formatUnits(out, tokDec), tokSym)
		} else {
			quoteLine = fmt.Sprintf("报价失败: %v", err)
		}
	}
	tokKey := r.registerUIToken(ctx, sess.ChainID, token, detectedProto)

	hpLine := "貔貅检测: 未启用"
	if sess.ChainID == 56 {
		if hb, ok := r.backend.(HoneypotBackend); ok {
			if hp, err := hb.CheckHoneypot(ctx, sess.ChainID, token); err == nil {
				// Keep first 2 lines compact for the card
				hpParts := strings.Split(hp, "\n")
				if len(hpParts) >= 2 {
					hpLine = hpParts[1]
				} else {
					hpLine = hp
				}
			}
		}
	}

	shadowBal, shadowCost := r.shadowTokenPosition(ctx, sess.ChainID, sess.WalletID, token)
	lines := []string{
		fmt.Sprintf("🪙 代币交易卡 (%s) | %s (%d)", tokSym, chainDisplayName(sess.ChainID), sess.ChainID),
		fmt.Sprintf("合约: %s", strings.ToLower(token.Hex())),
		fmt.Sprintf("路由: %s", protoLabel),
		quoteLine,
		hpLine,
		fmt.Sprintf("模拟持仓: %s %s (raw=%s | 成本=%s %s)", formatUnits(shadowBal, tokDec), tokSym, shadowBal.String(), formatWeiToNative(shadowCost), sym),
	}
	if banner != "" {
		lines = append(lines, "", banner)
	}

	buyModeBtn := "买入"
	sellModeBtn := "卖出"
	limitModeBtn := "限价"
	switch mode {
	case "buy":
		buyModeBtn = "🟢 买入"
	case "sell":
		sellModeBtn = "🔴 卖出"
	case "limit":
		limitModeBtn = "📈 限价"
	}

	var rows [][]InlineButton
	// Row 1: Top Multi-Chain Left/Right Switcher Bar
	rows = append(rows, chainTopSwitcherRow(sess.ChainID, "tc_c:"+tokKey+":"+mode+":prev", "tc_c:"+tokKey+":"+mode+":next"))
	// Row 2: Buy / Sell / Limit 3-mode switcher
	rows = append(rows, []InlineButton{
		{Text: buyModeBtn, CallbackData: "tc:" + tokKey + ":buy"},
		{Text: sellModeBtn, CallbackData: "tc:" + tokKey + ":sell"},
		{Text: limitModeBtn, CallbackData: "tc:" + tokKey + ":limit"},
	})

	switch mode {
	case "buy":
		presets := strings.Split(st.BuyPresetsCSV, ",")
		var buyBtns []InlineButton
		for _, p := range presets {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			buyBtns = append(buyBtns, InlineButton{Text: fmt.Sprintf("🟡 %s %s", p, sym), CallbackData: "sb:" + tokKey + ":" + p})
			if len(buyBtns) == 5 {
				break
			}
		}
		buyBtns = append(buyBtns, InlineButton{Text: "✏️ 自定义", CallbackData: "in:buy:" + tokKey})
		for i := 0; i < len(buyBtns); i += 3 {
			end := i + 3
			if end > len(buyBtns) {
				end = len(buyBtns)
			}
			rows = append(rows, buyBtns[i:end])
		}
	case "sell":
		rows = append(rows,
			[]InlineButton{
				{Text: "🔴 卖 10%", CallbackData: "ss:" + tokKey + ":10"},
				{Text: "🔴 卖 25%", CallbackData: "ss:" + tokKey + ":25"},
				{Text: "🔴 卖 50%", CallbackData: "ss:" + tokKey + ":50"},
			},
			[]InlineButton{
				{Text: "🔴 卖 75%", CallbackData: "ss:" + tokKey + ":75"},
				{Text: "🔴 卖 100%", CallbackData: "ss:" + tokKey + ":100"},
				{Text: "✏️ 自定义%", CallbackData: "in:sell:" + tokKey},
			},
		)
	case "limit":
		rows = append(rows,
			[]InlineButton{
				{Text: "📉 跌10%买", CallbackData: "lo:buy:" + tokKey + ":-10"},
				{Text: "📉 跌20%买", CallbackData: "lo:buy:" + tokKey + ":-20"},
				{Text: "📉 跌30%买", CallbackData: "lo:buy:" + tokKey + ":-30"},
			},
			[]InlineButton{
				{Text: "📈 涨50%卖50%", CallbackData: "lo:tp:" + tokKey + ":50:50"},
				{Text: "📈 涨100%卖50%", CallbackData: "lo:tp:" + tokKey + ":100:50"},
				{Text: "📈 涨200%全卖", CallbackData: "lo:tp:" + tokKey + ":200:100"},
			},
			[]InlineButton{
				{Text: "🛑 跌20%止损", CallbackData: "lo:sl:" + tokKey + ":-20:100"},
				{Text: "🛑 跌30%止损", CallbackData: "lo:sl:" + tokKey + ":-30:100"},
				{Text: "🛑 跌50%止损", CallbackData: "lo:sl:" + tokKey + ":-50:100"},
			},
		)
	}

	slipBps := st.BuySlippageBps
	if mode == "sell" {
		slipBps = st.SellSlippageBps
	}
	shortW := sess.WalletID
	if len(shortW) > 10 {
		shortW = shortW[:10]
	}
	rows = append(rows,
		[]InlineButton{
			{Text: "👛 " + shortW, CallbackData: "tc_w:" + tokKey + ":" + mode},
			{Text: "⛽ " + gasTierCN(st.GasTier), CallbackData: "tc_g:" + tokKey + ":" + mode},
			{Text: fmt.Sprintf("滑点 %.0f%%", float64(slipBps)/100.0), CallbackData: "settings"},
		},
		[]InlineButton{
			{Text: "🔄 刷新", CallbackData: "tc:" + tokKey + ":" + mode},
			{Text: "🏠 主页", CallbackData: "menu"},
		},
	)

	return Reply{
		Text:         strings.Join(lines, "\n"),
		Buttons:      rows,
		EditOriginal: edit,
	}
}

func (r *Router) executeShadowBuyButton(ctx context.Context, sess chatSession, tokKey, amtNativeStr string) Reply {
	cid, tok, _, err := r.lookupUIToken(ctx, tokKey)
	if err != nil {
		return Reply{AlertText: "Token expired; paste CA again"}
	}
	sess.ChainID = cid
	amtWei := parseNativeToWei(amtNativeStr)
	if amtWei == nil || amtWei.Sign() <= 0 {
		return Reply{AlertText: "Invalid buy amount"}
	}
	pb, ok := r.backend.(ProbeQuoteBackend)
	if !ok {
		return Reply{AlertText: "Quote backend unavailable"}
	}
	proto, _, outRaw, _, err := pb.ProbeTokenQuote(ctx, sess.ChainID, sess.WalletID, tok, "buy", amtWei)
	if err != nil {
		rep := r.renderTokenCard(ctx, sess, tok, "buy", true, "❌ Shadow Buy failed: "+err.Error())
		rep.AlertText = "Quote failed"
		return rep
	}
	spot := ComputeScaledSpotPrice(amtWei, outRaw)
	nowUTC := r.now().UTC()
	fillID := fmt.Sprintf("ui-buy-%d-%d", sess.ChainID, nowUTC.UnixNano())
	_, _ = r.db.ExecContext(ctx,
		`INSERT INTO paper_shadow_fills(fill_id, chain_id, wallet_id, token, side, amount_in_raw, simulated_out_raw, spot_price_wei, source_ref, created_at)
		 VALUES(?,?,?,?,'buy',?,?,?,?,?)`,
		fillID, sess.ChainID, sess.WalletID, strings.ToLower(tok.Hex()),
		amtWei.String(), outRaw.String(), spot.String(), "tg-ui:"+proto, nowUTC.Format(time.RFC3339Nano),
	)
	banner := fmt.Sprintf("✅ [模拟买入] %s %s -> +%s raw (%s)", amtNativeStr, chainNativeSymbol(sess.ChainID), outRaw.String(), proto)
	if st := r.loadBloomSettings(ctx, sess.ChatID); st.AutoTPSL {
		tpPrice := new(big.Int).Mul(spot, big.NewInt(2)) // +100%
		slPrice := new(big.Int).Mul(spot, big.NewInt(70))
		slPrice.Div(slPrice, big.NewInt(100)) // -30%
		if slPrice.Sign() <= 0 {
			slPrice = big.NewInt(1)
		}
		expStr := nowUTC.Add(24 * time.Hour).Format(time.RFC3339Nano)
		nowStr := nowUTC.Format(time.RFC3339Nano)
		tpID := fmt.Sprintf("atp-%d-%d", sess.ChainID, nowUTC.UnixNano())
		slID := fmt.Sprintf("asl-%d-%d", sess.ChainID, nowUTC.UnixNano()+1)
		_, _ = r.db.ExecContext(ctx,
			`INSERT INTO conditional_orders(order_id, chain_id, wallet_id, token, order_kind, trigger_price_wei, amount_raw, sell_bps, idempotency_key, state, expires_at, created_at, updated_at)
			 VALUES(?,?,?,?,?,'take_profit',?,'0',5000,?,'ACTIVE',?,?,?)`,
			tpID, sess.ChainID, sess.WalletID, strings.ToLower(tok.Hex()), tpPrice.String(), "auto-"+tpID, expStr, nowStr, nowStr,
		)
		_, _ = r.db.ExecContext(ctx,
			`INSERT INTO conditional_orders(order_id, chain_id, wallet_id, token, order_kind, trigger_price_wei, amount_raw, sell_bps, idempotency_key, state, expires_at, created_at, updated_at)
			 VALUES(?,?,?,?,?,'stop_loss',?,'0',10000,?,'ACTIVE',?,?,?)`,
			slID, sess.ChainID, sess.WalletID, strings.ToLower(tok.Hex()), slPrice.String(), "auto-"+slID, expStr, nowStr, nowStr,
		)
		banner += "\n🎯 已自动挂单：止盈(+100%/卖50%) & 止损(-30%/卖100%)"
	}
	rep := r.renderTokenCard(ctx, sess, tok, "buy", true, banner)
	rep.AlertText = "模拟买入成功！"
	return rep
}

func (r *Router) executeShadowSellButton(ctx context.Context, sess chatSession, tokKey, pctStr string) Reply {
	cid, tok, _, err := r.lookupUIToken(ctx, tokKey)
	if err != nil {
		return Reply{AlertText: "Token expired; paste CA again"}
	}
	sess.ChainID = cid
	pct, err := strconv.ParseUint(pctStr, 10, 64)
	if err != nil || pct == 0 || pct > 100 {
		return Reply{AlertText: "Invalid sell percentage"}
	}
	bal, _ := r.shadowTokenPosition(ctx, sess.ChainID, sess.WalletID, tok)
	if bal.Sign() <= 0 {
		rep := r.renderTokenCard(ctx, sess, tok, "sell", true, "⚠️ Zero Shadow token balance to sell.")
		rep.AlertText = "Zero balance"
		return rep
	}
	sellAmt := new(big.Int).Mul(bal, new(big.Int).SetUint64(pct))
	sellAmt.Div(sellAmt, big.NewInt(100))
	if sellAmt.Sign() <= 0 {
		return Reply{AlertText: "Sell amount rounds to zero"}
	}
	pb, ok := r.backend.(ProbeQuoteBackend)
	if !ok {
		return Reply{AlertText: "Quote backend unavailable"}
	}
	proto, _, outWei, _, err := pb.ProbeTokenQuote(ctx, sess.ChainID, sess.WalletID, tok, "sell", sellAmt)
	if err != nil {
		rep := r.renderTokenCard(ctx, sess, tok, "sell", true, "❌ Shadow Sell failed: "+err.Error())
		rep.AlertText = "Sell quote failed"
		return rep
	}
	spot := ComputeScaledSpotPrice(outWei, sellAmt)
	nowUTC := r.now().UTC()
	fillID := fmt.Sprintf("ui-sell-%d-%d", sess.ChainID, nowUTC.UnixNano())
	_, _ = r.db.ExecContext(ctx,
		`INSERT INTO paper_shadow_fills(fill_id, chain_id, wallet_id, token, side, amount_in_raw, simulated_out_raw, spot_price_wei, source_ref, created_at)
		 VALUES(?,?,?,?,'sell',?,?,?,?,?)`,
		fillID, sess.ChainID, sess.WalletID, strings.ToLower(tok.Hex()),
		sellAmt.String(), outWei.String(), spot.String(), "tg-ui:"+proto, nowUTC.Format(time.RFC3339Nano),
	)
	banner := fmt.Sprintf("✅ [模拟卖出 %d%%] -%s raw -> +%s %s (%s)", pct, sellAmt.String(), formatWeiToNative(outWei), chainNativeSymbol(sess.ChainID), proto)
	rep := r.renderTokenCard(ctx, sess, tok, "sell", true, banner)
	rep.AlertText = "模拟卖出成功！"
	return rep
}

func (r *Router) renderPositionsCard(ctx context.Context, sess chatSession) Reply {
	rows, err := r.readDB.QueryContext(ctx,
		`SELECT DISTINCT token FROM paper_shadow_fills WHERE chain_id = ? AND wallet_id = ? ORDER BY created_at DESC LIMIT 10`,
		sess.ChainID, sess.WalletID,
	)
	sym := chainNativeSymbol(sess.ChainID)
	lines := []string{fmt.Sprintf("📊 模拟持仓与盈亏 | %s (%d)", chainDisplayName(sess.ChainID), sess.ChainID)}
	var buttons [][]InlineButton
	count := 0
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var tokHex string
			if rows.Scan(&tokHex) != nil {
				continue
			}
			tok := common.HexToAddress(tokHex)
			bal, cost := r.shadowTokenPosition(ctx, sess.ChainID, sess.WalletID, tok)
			if bal.Sign() <= 0 {
				continue
			}
			count++
			pnlStr := ""
			if pb, ok := r.backend.(ProbeQuoteBackend); ok {
				if _, _, valWei, _, qErr := pb.ProbeTokenQuote(ctx, sess.ChainID, sess.WalletID, tok, "sell", bal); qErr == nil && cost.Sign() > 0 {
					diff := new(big.Int).Sub(valWei, cost)
					pct := new(big.Float).Quo(new(big.Float).SetInt(diff), new(big.Float).SetInt(cost))
					pctVal, _ := pct.Float64()
					pnlStr = fmt.Sprintf(" | 现值: %s %s (%+.1f%%)", formatWeiToNative(valWei), sym, pctVal*100)
				}
			}
			shortTok := tokHex
			if len(shortTok) > 12 {
				shortTok = shortTok[:6] + "..." + shortTok[len(shortTok)-4:]
			}
			lines = append(lines, fmt.Sprintf("%d. %s | 余额: %s | 成本: %s %s%s", count, shortTok, bal.String(), formatWeiToNative(cost), sym, pnlStr))
			tokKey := r.registerUIToken(ctx, sess.ChainID, tok, "")
			buttons = append(buttons, []InlineButton{{Text: fmt.Sprintf("🪙 打开 %s", shortTok), CallbackData: "tc:" + tokKey + ":sell"}})
		}
	}
	if count == 0 {
		lines = append(lines, "暂无模拟持仓。直接发送任意代币合约地址即可一键体验模拟买入！")
	}
	buttons = append(buttons, []InlineButton{{Text: "📑 今日复盘", CallbackData: "report"}, {Text: "🔄 刷新", CallbackData: "positions"}, {Text: "⬅️ 返回主菜单", CallbackData: "menu"}})
	return Reply{Text: strings.Join(lines, "\n"), Buttons: buttons, EditOriginal: true}
}

func (r *Router) renderCopyCard(ctx context.Context, sess chatSession) Reply {
	lines := []string{fmt.Sprintf("👥 聪明钱跟单列表 | %s (%d)", chainDisplayName(sess.ChainID), sess.ChainID)}
	var buttons [][]InlineButton
	if wb, ok := r.backend.(WatchlistManagerBackend); ok {
		list, err := wb.ListWatchedWallets(ctx, sess.ChainID)
		if err != nil {
			lines = append(lines, "查询失败: "+err.Error())
		} else if len(list) == 0 {
			lines = append(lines, "暂无监控地址。\n添加指令：/watch <0x钱包地址>")
		} else {
			for i, addr := range list {
				short := addr
				if len(short) > 12 {
					short = short[:6] + "..." + short[len(short)-4:]
				}
				prof := r.ensureCopyProfile(ctx, sess.ChainID, addr, sess.WalletID)
				statusDot := "🟢"
				if !prof.Enabled {
					statusDot = "⏸️"
				}
				lines = append(lines, fmt.Sprintf("%d. %s %s | %s %s | 跟卖:%s", i+1, statusDot, short, prof.BuyAmountNative, chainNativeSymbol(sess.ChainID), boolOnOff(prof.CopySell)))
				if i < 6 {
					buttons = append(buttons, []InlineButton{{Text: fmt.Sprintf("⚙️ 配置 #%d (%s)", i+1, short), CallbackData: "cp:open:" + prof.ProfileKey}})
				}
			}
			lines = append(lines, "", "添加新地址：/watch <0x...>")
		}
	}
	buttons = append(buttons,
		[]InlineButton{{Text: "➕ 添加监控地址", CallbackData: "in:watch"}},
		[]InlineButton{{Text: "🔄 刷新", CallbackData: "copy"}, {Text: "⬅️ 返回主菜单", CallbackData: "menu"}},
	)
	return Reply{
		Text:         strings.Join(lines, "\n"),
		EditOriginal: true,
		Buttons:      buttons,
	}
}

func (r *Router) renderOrdersCard(ctx context.Context, sess chatSession) Reply {
	lines := []string{fmt.Sprintf("📈 限价 / 止盈止损订单 | %s (%d)", chainDisplayName(sess.ChainID), sess.ChainID)}
	var buttons [][]InlineButton
	rows, err := r.readDB.QueryContext(ctx,
		`SELECT order_id, order_kind, token, trigger_price_wei, sell_bps, state
		 FROM conditional_orders WHERE chain_id = ? AND state IN ('ACTIVE', 'TRIGGERED')
		 ORDER BY created_at DESC LIMIT 8`,
		sess.ChainID,
	)
	cnt := 0
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var oid, kind, tok, trig, st string
			var bps uint64
			if rows.Scan(&oid, &kind, &tok, &trig, &bps, &st) == nil {
				cnt++
				lines = append(lines, fmt.Sprintf("%d. [%s] %s %s 触发价=%s 卖出=%d%%", cnt, st, kind, tok[:10]+"...", trig, bps/100))
				if st == "ACTIVE" && len("co:del:"+oid) <= 64 {
					buttons = append(buttons, []InlineButton{{Text: fmt.Sprintf("❌ 撤单 #%d (%s)", cnt, kind), CallbackData: "co:del:" + oid}})
				}
			}
		}
	}
	if cnt == 0 {
		lines = append(lines, "暂无活跃条件单。\n提示：在任意代币交易卡点「📈 限价」即可一键按涨跌幅挂单！")
	} else {
		buttons = append(buttons, []InlineButton{{Text: "🗑️ 一键撤销全部", CallbackData: "co:delall"}})
	}
	buttons = append(buttons, []InlineButton{{Text: "🔄 刷新", CallbackData: "orders"}, {Text: "⬅️ 返回主菜单", CallbackData: "menu"}})
	return Reply{
		Text:         strings.Join(lines, "\n"),
		EditOriginal: true,
		Buttons:      buttons,
	}
}

func (r *Router) renderSniperCard(ctx context.Context, sess chatSession) Reply {
	lines := []string{fmt.Sprintf("🎯 狙击任务（开盘 / 内盘毕业）| %s (%d)", chainDisplayName(sess.ChainID), sess.ChainID)}
	var buttons [][]InlineButton
	rows, err := r.readDB.QueryContext(ctx,
		`SELECT task_id, token, trigger_kind, amount_in_wei, state
		 FROM sniper_tasks WHERE chain_id = ? AND state IN ('ARMED', 'TRIGGERED')
		 ORDER BY created_at DESC LIMIT 8`,
		sess.ChainID,
	)
	cnt := 0
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var tid, tok, kind, amt, st string
			if rows.Scan(&tid, &tok, &kind, &amt, &st) == nil {
				cnt++
				short := tok
				if len(short) > 12 {
					short = short[:6] + "..." + short[len(short)-4:]
				}
				amtVal, _ := new(big.Int).SetString(amt, 10)
				lines = append(lines, fmt.Sprintf("%d. [%s] %s %s 金额=%s %s", cnt, st, strings.ToUpper(kind), short, formatWeiToNative(amtVal), chainNativeSymbol(sess.ChainID)))
				if st == "ARMED" && len("sp:open:"+tid) <= 64 {
					buttons = append(buttons, []InlineButton{{Text: fmt.Sprintf("⚙️ 配置 #%d (%s)", cnt, short), CallbackData: "sp:open:" + tid}})
				}
			}
		}
	}
	if cnt == 0 {
		lines = append(lines, "暂无已布防狙击任务。\n创建：/snipe <代币合约> <金额wei> [launch|migration]")
	}
	buttons = append(buttons,
		[]InlineButton{{Text: "➕ 新建狙击任务", CallbackData: "in:snipe"}},
		[]InlineButton{{Text: "🔄 刷新", CallbackData: "sniper"}, {Text: "⬅️ 返回主菜单", CallbackData: "menu"}},
	)
	return Reply{
		Text:         strings.Join(lines, "\n"),
		EditOriginal: true,
		Buttons:      buttons,
	}
}

func (r *Router) renderSettingsCard(ctx context.Context, sess chatSession, alert string) Reply {
	st := r.loadBloomSettings(ctx, sess.ChatID)
	mevStr := "开启（BSC 锁定 private_only 防夹）"
	if !st.AntiMEV && sess.ChainID != 56 {
		mevStr = "关闭"
	}
	confStr := "开启（发单前二次确认）"
	if !st.ConfirmBeforeLive {
		confStr = "关闭（极速直发）"
	}
	text := strings.Join([]string{
		fmt.Sprintf("⚙️ 交易参数设置 | %s (%d)", chainDisplayName(sess.ChainID), sess.ChainID),
		fmt.Sprintf("买入滑点：%.1f%% | 卖出滑点：%.1f%%", float64(st.BuySlippageBps)/100.0, float64(st.SellSlippageBps)/100.0),
		fmt.Sprintf("Gas 档位：%s", strings.ToUpper(st.GasTier)),
		fmt.Sprintf("防夹保护：%s", mevStr),
		fmt.Sprintf("实盘确认：%s", confStr),
		fmt.Sprintf("自动止盈止损 (+100%%/-30%%)：%s", boolOnOff(st.AutoTPSL)),
		fmt.Sprintf("快捷买入预设：%s %s", st.BuyPresetsCSV, chainNativeSymbol(sess.ChainID)),
	}, "\n")
	return Reply{
		Text:         text,
		EditOriginal: true,
		AlertText:    alert,
		Buttons: [][]InlineButton{
			{{Text: fmt.Sprintf("买入滑点: %.0f%%", float64(st.BuySlippageBps)/100.0), CallbackData: "set:slip_buy"}, {Text: fmt.Sprintf("卖出滑点: %.0f%%", float64(st.SellSlippageBps)/100.0), CallbackData: "set:slip_sell"}},
			{{Text: "⛽ Gas: " + strings.ToUpper(st.GasTier), CallbackData: "set:gas"}, {Text: "🛡️ 防夹保护", CallbackData: "set:mev"}},
			{{Text: "实盘确认: " + boolOnOff(st.ConfirmBeforeLive), CallbackData: "set:confirm"}, {Text: "🎯 自动止盈止损: " + boolOnOff(st.AutoTPSL), CallbackData: "set:auto_tpsl"}},
			{{Text: "✏️ 修改快捷买入预设", CallbackData: "in:presets"}, {Text: "⬅️ 返回主菜单", CallbackData: "menu"}},
		},
	}
}

func (r *Router) handleSettingToggle(ctx context.Context, sess chatSession, key string) Reply {
	st := r.loadBloomSettings(ctx, sess.ChatID)
	var alert string
	switch key {
	case "slip_buy":
		switch st.BuySlippageBps {
		case 100:
			st.BuySlippageBps = 500
		case 500:
			st.BuySlippageBps = 1000
		case 1000:
			st.BuySlippageBps = 2000
		default:
			st.BuySlippageBps = 100
		}
		alert = fmt.Sprintf("Buy slippage set to %.0f%%", float64(st.BuySlippageBps)/100.0)
	case "slip_sell":
		switch st.SellSlippageBps {
		case 500:
			st.SellSlippageBps = 1000
		case 1000:
			st.SellSlippageBps = 1500
		case 1500:
			st.SellSlippageBps = 2500
		default:
			st.SellSlippageBps = 500
		}
		alert = fmt.Sprintf("Sell slippage set to %.0f%%", float64(st.SellSlippageBps)/100.0)
	case "gas":
		switch st.GasTier {
		case "standard":
			st.GasTier = "fast"
		case "fast":
			st.GasTier = "turbo"
		default:
			st.GasTier = "standard"
		}
		alert = "Gas tier set to " + strings.ToUpper(st.GasTier)
	case "confirm":
		st.ConfirmBeforeLive = !st.ConfirmBeforeLive
		alert = "Live confirm " + boolOnOff(st.ConfirmBeforeLive)
	case "auto_tpsl":
		st.AutoTPSL = !st.AutoTPSL
		alert = "Auto TP/SL " + boolOnOff(st.AutoTPSL)
	case "mev":
		if sess.ChainID == 56 {
			st.AntiMEV = true
			alert = "BSC Anti-MEV is locked ON (private_only)"
		} else {
			st.AntiMEV = !st.AntiMEV
			alert = "Anti-MEV " + boolOnOff(st.AntiMEV)
		}
	}
	_ = r.saveBloomSettings(ctx, sess.ChatID, st)
	return r.renderSettingsCard(ctx, sess, alert)
}

func (r *Router) shadowTokenPosition(ctx context.Context, chainID uint64, walletID string, token common.Address) (bal *big.Int, costWei *big.Int) {
	bal = big.NewInt(0)
	costWei = big.NewInt(0)
	rows, err := r.readDB.QueryContext(ctx,
		`SELECT side, amount_in_raw, simulated_out_raw FROM paper_shadow_fills
		 WHERE chain_id = ? AND wallet_id = ? AND token = ? ORDER BY created_at ASC, rowid ASC`,
		chainID, walletID, strings.ToLower(token.Hex()),
	)
	if err != nil {
		return bal, costWei
	}
	defer rows.Close()
	for rows.Next() {
		var side, inStr, outStr string
		if rows.Scan(&side, &inStr, &outStr) != nil {
			continue
		}
		inVal, _ := new(big.Int).SetString(inStr, 10)
		outVal, _ := new(big.Int).SetString(outStr, 10)
		if inVal == nil || outVal == nil {
			continue
		}
		if side == "buy" {
			bal.Add(bal, outVal)
			costWei.Add(costWei, inVal)
		} else if side == "sell" {
			if bal.Sign() > 0 && inVal.Cmp(bal) < 0 {
				// Reduce cost proportionally
				rem := new(big.Int).Sub(bal, inVal)
				costWei.Mul(costWei, rem)
				costWei.Div(costWei, bal)
				bal.Set(rem)
			} else {
				bal.SetInt64(0)
				costWei.SetInt64(0)
			}
		}
	}
	return bal, costWei
}

func chainDisplayName(chainID uint64) string {
	switch chainID {
	case 1:
		return "ETH"
	case 56:
		return "BSC"
	case 999:
		return "HYPE"
	case 5042:
		return "ARC"
	case 8453:
		return "Base"
	case 99901:
		return "ELY"
	default:
		return "RBH"
	}
}

func chainNativeSymbol(chainID uint64) string {
	switch chainID {
	case 56:
		return "BNB"
	case 999, 99901:
		return "HYPE"
	case 5042:
		return "USDC"
	default:
		return "ETH"
	}
}

func boolOnOff(v bool) string {
	if v {
		return "开启"
	}
	return "关闭"
}

func gasTierCN(tier string) string {
	switch strings.ToLower(tier) {
	case "standard":
		return "标准"
	case "turbo":
		return "极速"
	default:
		return "快速"
	}
}

func parseNativeToWei(decStr string) *big.Int {
	f, ok := new(big.Float).SetPrec(128).SetString(strings.TrimSpace(decStr))
	if !ok || f.Sign() <= 0 {
		return nil
	}
	scale := new(big.Float).SetPrec(128).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	f.Mul(f, scale)
	out, _ := f.Int(nil)
	return out
}

func formatWeiToNative(wei *big.Int) string {
	if wei == nil || wei.Sign() <= 0 {
		return "0.0000"
	}
	f := new(big.Float).SetPrec(128).SetInt(wei)
	scale := new(big.Float).SetPrec(128).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	f.Quo(f, scale)
	v, _ := f.Float64()
	return fmt.Sprintf("%.4f", v)
}
