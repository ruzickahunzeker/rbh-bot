package telegram

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// renderWalletManagerCard renders M24 Full Wallet Management:
// lists both UDS-registered wallets and local AES-256-GCM encrypted keystores as clickable buttons,
// plus Create New Wallet and Import Private Key actions.
func (r *Router) renderWalletManagerCard(ctx context.Context, sess chatSession, alert string) Reply {
	dir := keystoreDirPath()
	ksList, _ := tradecore.ListEncryptedWallets(dir)
	udsWallets, _ := r.backend.ListWallets(ctx, sess.ChainID)

	lines := []string{
		fmt.Sprintf("👛 钱包管理 | %s (%d)", chainDisplayName(sess.ChainID), sess.ChainID),
		fmt.Sprintf("当前活跃钱包：%s", sess.WalletID),
		"",
	}
	var btns [][]InlineButton
	seen := make(map[string]bool)

	for _, w := range ksList {
		if w.ChainID != sess.ChainID {
			continue
		}
		seen[w.WalletID] = true
		star := ""
		if w.WalletID == sess.WalletID {
			star = "⭐ "
		}
		short := w.Address.Hex()
		short = short[:6] + "..." + short[len(short)-4:]
		lines = append(lines, fmt.Sprintf("• %s%s (%s) [加密Keystore]", star, w.WalletID, short))
		if len("wm:open:"+w.WalletID) <= 64 {
			btns = append(btns, []InlineButton{{Text: fmt.Sprintf("🔑 %s%s (%s)", star, w.WalletID, short), CallbackData: "wm:open:" + w.WalletID}})
		}
	}
	for _, wStr := range udsWallets {
		f := strings.Fields(wStr)
		if len(f) == 0 || seen[f[0]] {
			continue
		}
		id := f[0]
		star := ""
		if id == sess.WalletID {
			star = "⭐ "
		}
		lines = append(lines, fmt.Sprintf("• %s%s [只读注册]", star, wStr))
		if len("wm:open:"+id) <= 64 {
			btns = append(btns, []InlineButton{{Text: fmt.Sprintf("👛 %s%s", star, id), CallbackData: "wm:open:" + id}})
		}
	}
	if len(lines) <= 3 {
		lines = append(lines, "当前链暂无钱包，点击下方「创建新钱包」或「导入私钥」。")
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
	dir := keystoreDirPath()
	ksPath := filepath.Join(dir, walletID+".json")
	isKeystore := false
	addrStr := "只读注册钱包"
	if ksList, err := tradecore.ListEncryptedWallets(dir); err == nil {
		for _, w := range ksList {
			if w.WalletID == walletID {
				isKeystore = true
				addrStr = w.Address.Hex()
				break
			}
		}
	}
	star := "否"
	if walletID == sess.WalletID {
		star = "是 ⭐"
	}
	text := strings.Join([]string{
		fmt.Sprintf("👛 钱包详情 | %s (%d)", chainDisplayName(sess.ChainID), sess.ChainID),
		"钱包 ID：" + walletID,
		"地址：" + addrStr,
		"当前活跃：" + star,
		fmt.Sprintf("加密存储：%v (%s)", isKeystore, ksPath),
	}, "\n")

	var btns [][]InlineButton
	btns = append(btns, []InlineButton{{Text: "⭐ 设为活跃钱包", CallbackData: "wm:act:" + walletID}})
	if isKeystore {
		btns = append(btns,
			[]InlineButton{
				{Text: "🔑 导出私钥 (30秒焚毁)", CallbackData: "wm:exp1:" + walletID},
				{Text: "🗑️ 删除钱包", CallbackData: "wm:del1:" + walletID},
			},
		)
	}
	btns = append(btns, []InlineButton{{Text: "« 返回钱包列表", CallbackData: "wallets"}, {Text: "🏠 主页", CallbackData: "menu"}})
	return Reply{Text: text, EditOriginal: true, AlertText: alert, Buttons: btns}
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
		_, _ = r.db.ExecContext(ctx,
			`UPDATE telegram_allowlist SET active_wallet_id = ?, updated_at = ? WHERE chat_id = ?`,
			wid, r.now().UTC().Format(time.RFC3339Nano), sess.ChatID,
		)
		sess.WalletID = wid
		return r.renderWalletDetailCard(ctx, sess, wid, "已设为当前活跃钱包")
	case "create":
		pass := keystorePassphrase()
		if len(pass) < 8 {
			return Reply{
				Text:      "⚠️ 服务端尚未配置 EVM_WALLET_PASSPHRASE（>=8位），无法在 TG 内直接加密创建钱包。\n请先在 data/env/bot.env 中配置 EVM_WALLET_PASSPHRASE 后重启，或使用 CLI `operator wallet create`。",
				AlertText: "未配置加密口令",
			}
		}
		newID := fmt.Sprintf("w-%s-%d", strings.ToLower(chainDisplayName(sess.ChainID)), r.now().UTC().Unix()%100000)
		sum, err := tradecore.CreateEncryptedWallet(dir, newID, sess.ChainID, pass)
		if err != nil {
			return Reply{AlertText: "创建失败: " + err.Error()}
		}
		return r.renderWalletManagerCard(ctx, sess, fmt.Sprintf("已创建 %s (%s)", sum.WalletID, sum.Address.Hex()[:10]))
	case "import":
		if len(keystorePassphrase()) < 8 {
			return Reply{
				Text:      "⚠️ 服务端尚未配置 EVM_WALLET_PASSPHRASE（>=8位），暂不允许导入私钥。\n请先在 data/env/bot.env 中配置 EVM_WALLET_PASSPHRASE。",
				AlertText: "未配置加密口令",
			}
		}
		r.setPendingInput(ctx, sess.ChatID, "wm_import", fmt.Sprintf("%d", sess.ChainID))
		return Reply{Text: "📥 请直接回复要导入的 0x 私钥（系统将立即 AES-256-GCM 加密落盘，请发送后手动删除原消息）：", AlertText: "请回复私钥"}
	case "exp1":
		return Reply{
			Text:         fmt.Sprintf("⚠️ 安全确认：确定要导出钱包 %s 的明文私钥吗？\n私钥将以隐藏遮罩发送，并在 30 秒后自动阅后即焚删除。", wid),
			EditOriginal: true,
			Buttons: [][]InlineButton{
				{{Text: "✅ 确认导出私钥", CallbackData: "wm:exp2:" + wid}, {Text: "❌ 取消", CallbackData: "wm:open:" + wid}},
			},
		}
	case "exp2":
		pass := keystorePassphrase()
		if len(pass) < 8 {
			return Reply{AlertText: "服务端未配置 EVM_WALLET_PASSPHRASE，无法解密"}
		}
		priv, sum, err := tradecore.LoadEncryptedWallet(filepath.Join(dir, wid+".json"), pass)
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
		return Reply{
			Text:         fmt.Sprintf("⚠️ 危险操作：确定要永久删除加密钱包 %s 吗？\n删除前请确保已备份私钥且钱包无余额！", wid),
			EditOriginal: true,
			Buttons: [][]InlineButton{
				{{Text: "🗑️ 确认永久删除", CallbackData: "wm:del2:" + wid}, {Text: "❌ 取消", CallbackData: "wm:open:" + wid}},
			},
		}
	case "del2":
		ksPath := filepath.Join(dir, wid+".json")
		if err := os.Remove(ksPath); err != nil {
			return Reply{AlertText: "删除失败: " + err.Error()}
		}
		return r.renderWalletManagerCard(ctx, sess, "已删除钱包 "+wid)
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
	case "wm_import":
		pass := keystorePassphrase()
		if len(pass) < 8 {
			return Reply{Text: "❌ 服务端未配置 EVM_WALLET_PASSPHRASE"}, true
		}
		wid := fmt.Sprintf("imp-%s-%d", strings.ToLower(chainDisplayName(sess.ChainID)), r.now().UTC().Unix()%100000)
		sum, err := tradecore.ImportEncryptedWallet(keystoreDirPath(), wid, sess.ChainID, input, pass)
		if err != nil {
			return Reply{Text: "❌ 私钥导入失败: " + err.Error()}, true
		}
		rep := r.renderWalletManagerCard(ctx, sess, "私钥已加密导入")
		rep.EditOriginal = false
		rep.Text = fmt.Sprintf("✅ 已安全导入并 AES-256-GCM 加密保存钱包 %s (%s)\n⚠️ 请立即手动删除你刚才发送的私钥消息！\n\n%s", sum.WalletID, sum.Address.Hex(), rep.Text)
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
