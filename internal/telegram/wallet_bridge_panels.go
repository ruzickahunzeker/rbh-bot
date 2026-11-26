package telegram

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	tradecore "github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

func keystoreDirPath() string {
	if v := strings.TrimSpace(os.Getenv("EVM_KEYSTORE_DIR")); v != "" {
		return v
	}
	return "/root/evm-bot/data/keystore"
}

func keystorePassphrase() string {
	return os.Getenv("EVM_WALLET_PASSPHRASE")
}

// renderWalletManagerCard renders M35 Global EVM Wallet Management:
// lists all EVM wallets authorized for this user (both chain-agnostic/legacy Keystore wallets and readonly wallets),
// clearly distinguishing signable vs readonly wallets. Switching chains preserves the active wallet.
func (r *Router) renderWalletManagerCard(ctx context.Context, sess chatSession, alert string) Reply {
	wallets := r.syncAndListAuthorizedWallets(ctx, sess.ChatID, sess.ChainID)

	lines := []string{
		fmt.Sprintf("👛 全局 EVM 钱包管理 | 当前链：%s (%d)", chainDisplayName(sess.ChainID), sess.ChainID),
		fmt.Sprintf("当前活跃钱包：%s（切链自动保留，全 EVM 链通用）", sess.WalletID),
		"",
	}
	var btns [][]InlineButton

	for _, w := range wallets {
		star := ""
		if w.WalletID == sess.WalletID {
			star = "⭐ "
		}
		short := "无地址"
		if w.Address != (common.Address{}) {
			full := w.Address.Hex()
			short = full[:6] + "..." + full[len(full)-4:]
		}
		typeBadge := "[只读钱包]"
		icon := "👁️"
		if w.CanSign {
			typeBadge = "[可签名 Keystore]"
			icon = "🔑"
		}
		name := w.WalletID
		if w.Label != "" {
			name = w.Label + " · " + w.WalletID
		}
		lines = append(lines, fmt.Sprintf("• %s%s (%s) %s", star, name, short, typeBadge))
		if len("wm:open:"+w.WalletID) <= 64 {
			btns = append(btns, []InlineButton{{Text: fmt.Sprintf("%s %s%s (%s)", icon, star, name, short), CallbackData: "wm:open:" + w.WalletID}})
		}
	}
	if len(wallets) == 0 {
		lines = append(lines, "暂无可用 EVM 钱包，点击下方「创建新钱包」或「导入私钥」。")
	}

	btns = append(btns,
		[]InlineButton{
			{Text: "➕ 创建新钱包", CallbackData: "wm:create"},
			{Text: "📥 导入私钥", CallbackData: "wm:import"},
		},
		[]InlineButton{
			{Text: "🔄 刷新", CallbackData: "wallets"},
			{Text: "🏠 主页", CallbackData: "menu"},
		},
	)
	return Reply{
		Text:         strings.Join(lines, "\n"),
		EditOriginal: true,
		AlertText:    alert,
		Buttons:      btns,
	}
}

func (r *Router) renderWalletDetailCard(ctx context.Context, sess chatSession, walletID, alert string) Reply {
	w, ok := r.resolveAuthorizedWallet(ctx, sess.ChatID, sess.ChainID, walletID)
	if !ok {
		return Reply{AlertText: "无权查看该钱包或钱包不存在"}
	}
	addrStr := "未绑定地址"
	if w.Address != (common.Address{}) {
		addrStr = w.Address.Hex()
	}
	star := "否"
	if walletID == sess.WalletID {
		star = "是 ⭐"
	}
	typeDesc := "只读钱包（仅查询/模拟，不可签名）"
	if w.CanSign {
		typeDesc = "可签名 Keystore（全 EVM 链通用，按链隔离资产与 Nonce）"
	}
	lines := []string{
		fmt.Sprintf("👛 钱包详情 | 当前链：%s (%d)", chainDisplayName(sess.ChainID), sess.ChainID),
		"钱包 ID：" + w.WalletID,
		"标签：" + w.Label,
		"地址：" + addrStr,
		"类型：" + typeDesc,
		"当前活跃：" + star,
	}
	if w.KeystorePath != "" {
		lines = append(lines, "加密文件："+w.KeystorePath)
	}

	var btns [][]InlineButton
	btns = append(btns, []InlineButton{{Text: "⭐ 设为活跃钱包", CallbackData: "wm:act:" + w.WalletID}, {Text: "✏️ 修改钱包标签", CallbackData: "wm:label:" + w.WalletID}})
	if w.CanSign {
		btns = append(btns,
			[]InlineButton{
				{Text: "🔑 导出私钥 (30秒焚毁)", CallbackData: "wm:exp1:" + w.WalletID},
				{Text: "🗑️ 删除钱包", CallbackData: "wm:del1:" + w.WalletID},
			},
		)
	}
	btns = append(btns, []InlineButton{{Text: "« 返回钱包列表", CallbackData: "wallets"}, {Text: "🏠 主页", CallbackData: "menu"}})
	return Reply{Text: strings.Join(lines, "\n"), EditOriginal: true, AlertText: alert, Buttons: btns}
}

func (r *Router) handleWalletManagerCallback(ctx context.Context, sess chatSession, data string) Reply {
	parts := strings.Split(data, ":")
	if len(parts) < 2 {
		return Reply{AlertText: "无效钱包操作"}
	}
	act := parts[1]
	wid := ""
	if len(parts) >= 3 {
		wid = parts[2]
	}
	dir := keystoreDirPath()

	switch act {
	case "open":
		return r.renderWalletDetailCard(ctx, sess, wid, "")
	case "act":
		if _, ok := r.resolveAuthorizedWallet(ctx, sess.ChatID, sess.ChainID, wid); !ok {
			return Reply{AlertText: "无权切换到该钱包或钱包不存在"}
		}
		_ = r.setActiveWalletID(ctx, sess.ChatID, wid)
		sess.WalletID = wid
		return r.renderWalletDetailCard(ctx, sess, wid, "已设为当前活跃钱包（跨链保留）")
	case "label":
		if _, ok := r.resolveAuthorizedWallet(ctx, sess.ChatID, sess.ChainID, wid); !ok {
			return Reply{AlertText: "无权修改该钱包标签"}
		}
		r.setPendingInput(ctx, sess.ChatID, "wm_label", wid)
		return Reply{Text: "✏️ 请回复钱包标签（最多 32 个字符），回复 - 清除标签。标签仅对你显示。"}
	case "recover":
		if len(keystorePassphrase()) < 8 {
			return Reply{AlertText: "未配置加密口令"}
		}
		r.setPendingInput(ctx, sess.ChatID, "wm_recover", "evm")
		return Reply{Text: "🔐 恢复加密：请重新发送目标钱包的私钥。系统将验证地址和归属，保留原钱包 ID，以当前服务器口令重新加密；此操作可能替换损坏的密文。明文消息将自动删除。"}
	case "create":
		pass := keystorePassphrase()
		if len(pass) < 8 {
			return Reply{
				Text:      "⚠️ 服务端尚未配置 EVM_WALLET_PASSPHRASE（>=8位），无法在 TG 内直接加密创建钱包。\n请先在 data/env/bot.env 中配置 EVM_WALLET_PASSPHRASE 后重启，或使用 CLI `operator wallet create`。",
				AlertText: "未配置加密口令",
			}
		}
		existingKeystores := r.countAuthorizedKeystoreWallets(ctx, sess.ChatID, sess.ChainID)
		newID, err := tradecore.NewGlobalWalletID("w-evm-")
		if err != nil {
			return Reply{AlertText: "生成钱包 ID 失败"}
		}
		sum, err := tradecore.CreateGlobalEncryptedWallet(dir, newID, sess.ChatID, pass)
		if err != nil {
			return Reply{AlertText: "创建失败: " + err.Error()}
		}
		if err := r.registerGlobalWalletRow(ctx, GlobalWallet{
			WalletID:     sum.WalletID,
			Address:      sum.Address,
			WalletType:   "keystore",
			KeystorePath: sum.FilePath,
			OwnerChatID:  sess.ChatID,
			CanSign:      true,
		}); err != nil {
			return Reply{AlertText: "钱包已加密保存，目录登记失败，请刷新重试"}
		}
		alertMsg := fmt.Sprintf("已创建全链钱包 %s (%s)", sum.WalletID, sum.Address.Hex()[:10])
		if existingKeystores == 0 {
			_ = r.setActiveWalletID(ctx, sess.ChatID, sum.WalletID)
			sess.WalletID = sum.WalletID
			alertMsg += "，已自动设为活跃钱包"
		}
		return r.renderWalletManagerCard(ctx, sess, alertMsg)
	case "import":
		if len(keystorePassphrase()) < 8 {
			return Reply{
				Text:      "⚠️ 服务端尚未配置 EVM_WALLET_PASSPHRASE（>=8位），暂不允许导入私钥。\n请先在 data/env/bot.env 中配置 EVM_WALLET_PASSPHRASE。",
				AlertText: "未配置加密口令",
			}
		}
		r.setPendingInput(ctx, sess.ChatID, "wm_import", "evm")
		return Reply{Text: "📥 请直接回复要导入的 0x 私钥（系统将自动去重、AES-256-GCM 跨链通用加密落盘，并自动删除你的明文消息）：", AlertText: "请回复私钥"}
	case "exp1":
		w, ok := r.resolveAuthorizedWallet(ctx, sess.ChatID, sess.ChainID, wid)
		if !ok || !w.CanSign {
			return Reply{AlertText: "无权导出该钱包或该钱包为只读钱包"}
		}
		return Reply{
			Text:         fmt.Sprintf("⚠️ 安全确认：确定要导出钱包 %s 的明文私钥吗？\n私钥将以隐藏遮罩发送，并在 30 秒后自动阅后即焚删除。", wid),
			EditOriginal: true,
			Buttons: [][]InlineButton{
				{{Text: "✅ 确认导出私钥", CallbackData: "wm:exp2:" + wid}, {Text: "❌ 取消", CallbackData: "wm:open:" + wid}},
			},
		}
	case "exp2":
		w, ok := r.resolveAuthorizedWallet(ctx, sess.ChatID, sess.ChainID, wid)
		if !ok || !w.CanSign {
			return Reply{AlertText: "无权导出该钱包或该钱包为只读钱包"}
		}
		pass := keystorePassphrase()
		if len(pass) < 8 {
			return Reply{AlertText: "服务端未配置 EVM_WALLET_PASSPHRASE，无法解密"}
		}
		ksPath := w.KeystorePath
		if ksPath == "" {
			ksPath = filepath.Join(dir, wid+".json")
		}
		priv, sum, err := tradecore.LoadEncryptedWallet(ksPath, pass)
		if err != nil {
			return Reply{AlertText: "解密失败: " + err.Error()}
		}
		privBytes := crypto.FromECDSA(priv)
		hexKey := "0x" + hex.EncodeToString(privBytes)
		clear(privBytes)
		return Reply{
			Text:          fmt.Sprintf("🔑 钱包 <b>%s</b> (%s) 私钥（30 秒后自动焚毁）：\n<tg-spoiler>%s</tg-spoiler>", sum.WalletID, sum.Address.Hex(), hexKey),
			ParseMode:     "HTML",
			AutoDeleteSec: 30,
			AlertText:     "私钥已发送，30秒后自动删除",
		}
	case "del1":
		w, ok := r.resolveAuthorizedWallet(ctx, sess.ChatID, sess.ChainID, wid)
		if !ok || !w.CanSign {
			return Reply{AlertText: "无权删除该钱包或该钱包为只读钱包"}
		}
		if blockers, checkErr := r.walletDeleteBlockers(ctx, wid); checkErr != nil {
			return Reply{AlertText: "暂不能删除：无法完整检查任务状态"}
		} else if len(blockers) > 0 {
			return Reply{
				Text:         fmt.Sprintf("⛔ 无法删除钱包 %s：检测到全链仍有活跃引用：\n• %s\n请先取消挂单、关闭跟单/狙击策略并等待未完成交易结束后再删除。", wid, strings.Join(blockers, "\n• ")),
				EditOriginal: true,
				AlertText:    "存在活跃挂单或策略，禁止删除",
				Buttons: [][]InlineButton{
					{{Text: "« 返回钱包详情", CallbackData: "wm:open:" + wid}, {Text: "🏠 主页", CallbackData: "menu"}},
				},
			}
		}
		return Reply{
			Text:         fmt.Sprintf("⚠️ 危险操作：确定要永久删除加密钱包 %s 吗？\n删除前请确保已备份私钥且全链无余额！", wid),
			EditOriginal: true,
			Buttons: [][]InlineButton{
				{{Text: "🗑️ 确认永久删除", CallbackData: "wm:del2:" + wid}, {Text: "❌ 取消", CallbackData: "wm:open:" + wid}},
			},
		}
	case "del2":
		w, ok := r.resolveAuthorizedWallet(ctx, sess.ChatID, sess.ChainID, wid)
		if !ok || !w.CanSign {
			return Reply{AlertText: "无权删除该钱包或该钱包为只读钱包"}
		}
		if err := r.deleteGlobalWallet(ctx, sess.ChatID, wid); err != nil {
			return Reply{AlertText: "删除未完成：" + err.Error()}
		}

		if sess.WalletID == wid {
			remaining := r.syncAndListAuthorizedWallets(ctx, sess.ChatID, sess.ChainID)
			nextID := ""
			if len(remaining) > 0 {
				nextID = remaining[0].WalletID
			}
			_ = r.setActiveWalletID(ctx, sess.ChatID, nextID)
			sess.WalletID = nextID
		}
		return r.renderWalletManagerCard(ctx, sess, "已安全删除钱包 "+wid)
	}
	return Reply{AlertText: "未知钱包操作"}
}

// ============================================================================
// M25: Bridge powered by Houdini
// ============================================================================

func (r *Router) renderBridgeCard(ctx context.Context, sess chatSession, anonymous bool, alert string) Reply {
	modeLabel := "标准路由"
	nextAnon := "1"
	if anonymous {
		modeLabel = "🔒 隐私跨链 (Private)"
		nextAnon = "0"
	}
	text := strings.Join([]string{
		"🌉 跨链桥 · Bridge powered by Houdini",
		fmt.Sprintf("当前链：%s (%d) | 隐私模式：%s", chainDisplayName(sess.ChainID), sess.ChainID, modeLabel),
		"",
		"选择常用跨链通道后输入金额，即可实时向 Houdini 询价并生成专属跨链订单：",
	}, "\n")
	anonTag := "0"
	if anonymous {
		anonTag = "1"
	}
	return Reply{
		Text:         text,
		EditOriginal: true,
		AlertText:    alert,
		Buttons: [][]InlineButton{
			{{Text: "切换模式: " + modeLabel, CallbackData: "br:anon:" + nextAnon}},
			{{Text: "BNB (BSC) → ETH", CallbackData: "br:in:BNB:ETH:" + anonTag}, {Text: "ETH → BNB (BSC)", CallbackData: "br:in:ETH:BNB:" + anonTag}},
			{{Text: "BNB (BSC) → SOL", CallbackData: "br:in:BNB:SOL:" + anonTag}, {Text: "USDT (BSC) → ETH", CallbackData: "br:in:USDTBSC:ETH:" + anonTag}},
			{{Text: "📋 历史跨链订单", CallbackData: "br:orders"}, {Text: "🏠 主页", CallbackData: "menu"}},
		},
	}
}

func (r *Router) handleBridgeCallback(ctx context.Context, sess chatSession, data string) Reply {
	parts := strings.Split(data, ":")
	if len(parts) < 2 {
		return Reply{AlertText: "无效跨链操作"}
	}
	switch parts[1] {
	case "home":
		return r.renderBridgeCard(ctx, sess, false, "")
	case "anon":
		anon := len(parts) >= 3 && parts[2] == "1"
		return r.renderBridgeCard(ctx, sess, anon, "已切换跨链隐私模式")
	case "in":
		// br:in:<from>:<to>:<anon>
		if len(parts) < 5 {
			return Reply{AlertText: "无效跨链路由"}
		}
		targetKey := parts[2] + ":" + parts[3] + ":" + parts[4]
		r.setPendingInput(ctx, sess.ChatID, "br_quote", targetKey)
		return Reply{
			Text:      fmt.Sprintf("🌉 Bridge powered by Houdini\n通道：%s → %s\n请回复：<发送数量> <接收地址>\n示例：0.5 0xYourReceiveAddress", parts[2], parts[3]),
			AlertText: "请回复数量与接收地址",
		}
	case "orders":
		rows, err := r.readDB.QueryContext(ctx,
			`SELECT order_id, from_symbol, to_symbol, amount_str, expected_out, status
			 FROM bridge_orders WHERE chat_id = ? ORDER BY created_at DESC LIMIT 8`,
			sess.ChatID,
		)
		lines := []string{"📋 历史跨链订单 (Bridge powered by Houdini)"}
		cnt := 0
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var oid, fromS, toS, amt, expOut, st string
				if rows.Scan(&oid, &fromS, &toS, &amt, &expOut, &st) == nil {
					cnt++
					lines = append(lines, fmt.Sprintf("%d. [%s] %s %s → ~%s %s (%s)", cnt, st, amt, fromS, expOut, toS, oid))
				}
			}
		}
		if cnt == 0 {
			lines = append(lines, "暂无跨链记录。")
		}
		return Reply{
			Text:         strings.Join(lines, "\n"),
			EditOriginal: true,
			Buttons: [][]InlineButton{
				{{Text: "« 返回跨链桥", CallbackData: "br:home"}, {Text: "🏠 主页", CallbackData: "menu"}},
			},
		}
	}
	return Reply{AlertText: "未知跨链操作"}
}

func (r *Router) handleWalletOrBridgePendingInput(ctx context.Context, sess chatSession, action, targetKey, input string) (Reply, bool) {
	switch action {
	case "wm_label":
		if _, ok := r.resolveAuthorizedWallet(ctx, sess.ChatID, sess.ChainID, targetKey); !ok {
			return Reply{Text: "无权修改该钱包标签"}, true
		}
		label := strings.TrimSpace(input)
		if label == "-" {
			label = ""
		}
		if !utf8.ValidString(label) || utf8.RuneCountInString(label) > 32 {
			return Reply{Text: "标签必须是不超过 32 个字符的文本"}, true
		}
		for _, c := range label {
			if unicode.IsControl(c) {
				return Reply{Text: "标签不能包含换行或控制字符"}, true
			}
		}
		_, err := r.db.ExecContext(ctx, `INSERT INTO evm_wallet_labels(chat_id,wallet_id,label,updated_at) VALUES(?,?,?,?) ON CONFLICT(chat_id,wallet_id) DO UPDATE SET label=excluded.label,updated_at=excluded.updated_at`, sess.ChatID, targetKey, label, r.now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return Reply{Text: "标签保存失败，请重试"}, true
		}
		rep := r.renderWalletDetailCard(ctx, sess, targetKey, "标签已保存")
		rep.EditOriginal = false
		return rep, true
	case "wm_import", "wm_recover":
		pass := keystorePassphrase()
		if len(pass) < 8 {
			return Reply{Text: "❌ 服务端未配置 EVM_WALLET_PASSPHRASE", DeleteUserMessage: true}, true
		}
		existingKeystores := r.countAuthorizedKeystoreWallets(ctx, sess.ChatID, sess.ChainID)
		wid, idErr := tradecore.NewGlobalWalletID("imp-evm-")
		if idErr != nil {
			return Reply{Text: "生成钱包 ID 失败", DeleteUserMessage: true}, true
		}
		sum, duplicate, err := tradecore.ImportGlobalEncryptedWalletWithOptions(keystoreDirPath(), wid, sess.ChatID, input, pass, tradecore.WalletImportOptions{Recover: action == "wm_recover", AllowShared: sharedKeystoreAllowed(sess.ChatID)})
		if err != nil {
			if errors.Is(err, tradecore.ErrKeystoreRecoveryRequired) {
				return Reply{Text: "已有同地址钱包，但当前口令无法验证旧密文。可能是口令不同或文件被修改；未覆盖旧文件。如需用当前口令恢复，请点击下方按钮并重新提供私钥。", DeleteUserMessage: true, Buttons: [][]InlineButton{{{Text: "🔐 恢复钱包加密", CallbackData: "wm:recover"}}}}, true
			}
			return Reply{Text: "❌ 私钥导入失败: " + err.Error(), DeleteUserMessage: true}, true
		}
		if duplicate {
			if sum.Reencrypted {
				rep := r.renderWalletManagerCard(ctx, sess, "已用当前口令恢复钱包加密，原 ID 与归属已保留")
				rep.EditOriginal = false
				rep.DeleteUserMessage = true
				return rep, true
			}
			if sum.OwnerChatID != 0 && sum.OwnerChatID != sess.ChatID {
				return Reply{
					Text:              "⛔ 该私钥对应地址已归属其他用户，拒绝重复导入（已自动删除你的明文私钥消息）。",
					DeleteUserMessage: true,
				}, true
			}
			rep := r.renderWalletManagerCard(ctx, sess, "已识别为现有钱包")
			rep.EditOriginal = false
			rep.DeleteUserMessage = true
			rep.Text = fmt.Sprintf("ℹ️ 该私钥对应地址已存在，未重复创建：%s (%s)（已自动删除你的明文私钥消息）\n\n%s", sum.WalletID, sum.Address.Hex(), rep.Text)
			return rep, true
		}
		if err := r.registerGlobalWalletRow(ctx, GlobalWallet{
			WalletID:     sum.WalletID,
			Address:      sum.Address,
			WalletType:   "keystore",
			KeystorePath: sum.FilePath,
			OwnerChatID:  sess.ChatID,
			CanSign:      true,
		}); err != nil {
			return Reply{Text: "钱包已加密保存，目录登记失败，请刷新重试", DeleteUserMessage: true}, true
		}
		autoNote := ""
		if existingKeystores == 0 {
			_ = r.setActiveWalletID(ctx, sess.ChatID, sum.WalletID)
			sess.WalletID = sum.WalletID
			autoNote = "，并已自动设为当前活跃钱包"
		}
		rep := r.renderWalletManagerCard(ctx, sess, "私钥已加密导入")
		rep.EditOriginal = false
		rep.DeleteUserMessage = true
		rep.Text = fmt.Sprintf("✅ 已安全导入全链钱包 %s (%s)%s（已自动删除你的明文私钥消息）\n\n%s", sum.WalletID, sum.Address.Hex(), autoNote, rep.Text)
		return rep, true

	case "br_quote":
		// targetKey = "<from>:<to>:<anon>"
		tp := strings.Split(targetKey, ":")
		fields := strings.Fields(input)
		if len(tp) < 3 || len(fields) < 2 {
			return Reply{Text: "❌ 格式无效，请回复：<数量> <接收地址>（例如：0.5 0x...）"}, true
		}
		fromSym, toSym, anon := tp[0], tp[1], tp[2] == "1"
		amtStr, recvAddr := fields[0], fields[1]
		expOut, note := fetchHoudiniQuote(ctx, fromSym, toSym, amtStr, anon)

		nowUTC := r.now().UTC()
		oid := fmt.Sprintf("hdn-%d", nowUTC.UnixNano()%1000000000)
		anonInt := 0
		if anon {
			anonInt = 1
		}
		_, _ = r.db.ExecContext(ctx,
			`INSERT INTO bridge_orders(order_id, chat_id, from_symbol, to_symbol, amount_str, receive_address, anonymous, expected_out, status, created_at, updated_at)
			 VALUES(?,?,?,?,?,?,?,?,'QUOTED',?,?)`,
			oid, sess.ChatID, fromSym, toSym, amtStr, recvAddr, anonInt, expOut,
			nowUTC.Format(time.RFC3339Nano), nowUTC.Format(time.RFC3339Nano),
		)
		modeTxt := "标准"
		if anon {
			modeTxt = "隐私 (Private)"
		}
		msg := strings.Join([]string{
			"🌉 跨链询价单 · Bridge powered by Houdini",
			fmt.Sprintf("订单号：%s | 模式：%s", oid, modeTxt),
			fmt.Sprintf("兑换：%s %s → 预计 %s %s", amtStr, fromSym, expOut, toSym),
			fmt.Sprintf("接收地址：%s", recvAddr),
			note,
		}, "\n")
		return Reply{
			Text: msg,
			Buttons: [][]InlineButton{
				{{Text: "📋 历史跨链订单", CallbackData: "br:orders"}, {Text: "🏠 主页", CallbackData: "menu"}},
			},
		}, true
	}
	return Reply{}, false
}

func fetchHoudiniQuote(ctx context.Context, fromSym, toSym, amtStr string, anonymous bool) (expectedOut string, note string) {
	apiKey := strings.TrimSpace(os.Getenv("HOUDINI_API_KEY"))
	apiSecret := strings.TrimSpace(os.Getenv("HOUDINI_API_SECRET"))
	if apiKey == "" || apiSecret == "" {
		return "待配置 API Key 实时报价", "提示：在 bot.env 配置 HOUDINI_API_KEY 与 HOUDINI_API_SECRET 后即可直连 Houdini 官方接口获取实时报价与充值地址。"
	}
	qURL := fmt.Sprintf("https://api-partner.houdiniswap.com/quote?amount=%s&from=%s&to=%s&anonymous=%t",
		url.QueryEscape(amtStr), url.QueryEscape(fromSym), url.QueryEscape(toSym), anonymous)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, qURL, nil)
	if err != nil {
		return "N/A", "请求构建失败: " + err.Error()
	}
	req.Header.Set("Authorization", apiKey+":"+apiSecret)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return "N/A", "Houdini 接口暂不可达: " + err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusOK {
		return "N/A", fmt.Sprintf("Houdini 返回 HTTP %d", resp.StatusCode)
	}
	var res struct {
		AmountOut any `json:"amountOut"`
		Duration  any `json:"duration"`
	}
	if json.Unmarshal(raw, &res) == nil && res.AmountOut != nil {
		return fmt.Sprintf("%v", res.AmountOut), fmt.Sprintf("预计耗时：%v 分钟 (Powered by Houdini)", res.Duration)
	}
	return "N/A", "已收到 Houdini 响应，请检查币种代码。"
}
