package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// TokenMetadataBackend is optionally implemented by Backend to format ERC-20 symbol & decimals.
type TokenMetadataBackend interface {
	ReadTokenMetadata(ctx context.Context, chainID uint64, token common.Address) (symbol string, decimals uint8)
}

func (r *Router) tokenMeta(ctx context.Context, chainID uint64, token common.Address) (string, uint8) {
	if mb, ok := r.backend.(TokenMetadataBackend); ok {
		sym, dec := mb.ReadTokenMetadata(ctx, chainID, token)
		if sym != "" {
			return sym, dec
		}
	}
	return "TOKEN", 18
}

func formatUnits(raw *big.Int, decimals uint8) string {
	if raw == nil || raw.Sign() <= 0 {
		return "0"
	}
	if decimals == 0 {
		return raw.String()
	}
	f := new(big.Float).SetPrec(128).SetInt(raw)
	scale := new(big.Float).SetPrec(128).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	f.Quo(f, scale)
	v, _ := f.Float64()
	if v >= 1000 {
		return fmt.Sprintf("%.2f", v)
	}
	if v >= 1 {
		return fmt.Sprintf("%.4f", v)
	}
	return fmt.Sprintf("%.6f", v)
}

func (r *Router) setPendingInput(ctx context.Context, chatID int64, action, targetKey string) {
	nowUTC := r.now().UTC()
	exp := nowUTC.Add(5 * time.Minute)
	_, _ = r.db.ExecContext(ctx,
		`INSERT INTO tg_pending_inputs(chat_id, action, target_key, expires_at, updated_at)
		 VALUES(?,?,?,?,?)
		 ON CONFLICT(chat_id) DO UPDATE SET action=excluded.action, target_key=excluded.target_key, expires_at=excluded.expires_at, updated_at=excluded.updated_at`,
		chatID, action, targetKey, exp.Format(time.RFC3339Nano), nowUTC.Format(time.RFC3339Nano),
	)
}

func (r *Router) consumePendingInput(ctx context.Context, chatID int64) (action, targetKey string, ok bool) {
	var expStr string
	err := r.readDB.QueryRowContext(ctx,
		`SELECT action, target_key, expires_at FROM tg_pending_inputs WHERE chat_id = ?`,
		chatID,
	).Scan(&action, &targetKey, &expStr)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return "", "", false
	}
	_, _ = r.db.ExecContext(ctx, `DELETE FROM tg_pending_inputs WHERE chat_id = ?`, chatID)
	exp, err := time.Parse(time.RFC3339Nano, expStr)
	if err != nil || !exp.After(r.now().UTC()) {
		return "", "", false
	}
	return action, targetKey, true
}

func (r *Router) handlePendingInputReply(ctx context.Context, sess chatSession, action, targetKey, input string) (Reply, bool) {
	input = strings.TrimSpace(input)
	if input == "" || strings.HasPrefix(input, "/") {
		return Reply{}, false
	}
	switch action {
	case "buy":
		if amt := parseNativeToWei(input); amt == nil || amt.Sign() <= 0 {
			return Reply{Text: "❌ 金额格式无效，请输入正数（例如 0.25）。"}, true
		}
		rep := r.executeShadowBuyButton(ctx, sess, targetKey, input)
		rep.EditOriginal = false
		return rep, true

	case "sell":
		pctStr := strings.TrimSuffix(input, "%")
		pct, err := strconv.ParseUint(strings.TrimSpace(pctStr), 10, 64)
		if err != nil || pct == 0 || pct > 100 {
			return Reply{Text: "❌ 卖出比例无效，请输入 1~100 的整数（例如 35 或 50%）。"}, true
		}
		rep := r.executeShadowSellButton(ctx, sess, targetKey, strconv.FormatUint(pct, 10))
		rep.EditOriginal = false
		return rep, true

	case "watch":
		if !common.IsHexAddress(input) {
			return Reply{Text: "❌ 地址格式无效，请发送正确的 0x 钱包地址。"}, true
		}
		if err := r.backend.AddWatchedWallet(ctx, sess.ChainID, common.HexToAddress(input)); err != nil {
			return Reply{Text: "❌ 添加监控失败: " + err.Error()}, true
		}
		rep := r.renderCopyCard(ctx, sess)
		rep.EditOriginal = false
		return rep, true

	case "snipe":
		// Expected: "<token_ca> <amount_native> [launch|migration]"
		fields := strings.Fields(input)
		if len(fields) < 2 || !common.IsHexAddress(fields[0]) {
			return Reply{Text: "❌ 格式无效，请发送：<代币合约> <金额> [launch|migration]\n示例：0x55d3...7955 0.05 migration"}, true
		}
		amtWei := parseNativeToWei(fields[1])
		if amtWei == nil || amtWei.Sign() <= 0 {
			return Reply{Text: "❌ 狙击金额无效，请输入正数（如 0.05）。"}, true
		}
		kind := "migration"
		if len(fields) >= 3 && strings.ToLower(fields[2]) == "launch" {
			kind = "launch"
		}
		txt, err := r.HandleCommand(ctx, sess.ChatID, fmt.Sprintf("/snipe %s %s %s", fields[0], amtWei.String(), kind))
		if err != nil {
			return Reply{Text: "❌ 创建狙击失败: " + err.Error()}, true
		}
		rep := r.renderSniperCard(ctx, sess)
		rep.EditOriginal = false
		rep.Text = txt + "\n\n" + rep.Text
		return rep, true

	case "presets":
		parts := strings.Split(input, ",")
		var clean []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if parseNativeToWei(p) != nil {
				clean = append(clean, p)
			}
		}
		if len(clean) == 0 || len(clean) > 5 {
			return Reply{Text: "❌ 请输入 1~5 个逗号分隔的正数金额，例如：0.05,0.1,0.25,0.5,1"}, true
		}
		st := r.loadBloomSettings(ctx, sess.ChatID)
		st.BuyPresetsCSV = strings.Join(clean, ",")
		_ = r.saveBloomSettings(ctx, sess.ChatID, st)
		rep := r.renderSettingsCard(ctx, sess, "快捷买入预设已更新")
		rep.EditOriginal = false
		return rep, true
	}
	return Reply{}, false
}

// renderDailyReportCard summarizes today's Shadow fills, realized/unrealized volume, and active orders (M18).
func (r *Router) renderDailyReportCard(ctx context.Context, sess chatSession) Reply {
	dayPrefix := r.now().UTC().Format("2006-01-02")
	sym := chainNativeSymbol(sess.ChainID)
	var buys, sells int
	buyWei := big.NewInt(0)
	sellWei := big.NewInt(0)

	rows, err := r.readDB.QueryContext(ctx,
		`SELECT side, amount_in_raw, simulated_out_raw FROM paper_shadow_fills
		 WHERE chain_id = ? AND wallet_id = ? AND created_at LIKE ?`,
		sess.ChainID, sess.WalletID, dayPrefix+"%",
	)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var side, inStr, outStr string
			if rows.Scan(&side, &inStr, &outStr) != nil {
				continue
			}
			inVal, _ := new(big.Int).SetString(inStr, 10)
			outVal, _ := new(big.Int).SetString(outStr, 10)
			if side == "buy" && inVal != nil {
				buys++
				buyWei.Add(buyWei, inVal)
			} else if side == "sell" && outVal != nil {
				sells++
				sellWei.Add(sellWei, outVal)
			}
		}
	}
	netWei := new(big.Int).Sub(sellWei, buyWei)
	sign := "+"
	absNet := new(big.Int).Set(netWei)
	if netWei.Sign() < 0 {
		sign = "-"
		absNet.Neg(absNet)
	}
	text := strings.Join([]string{
		fmt.Sprintf("📑 今日交易复盘 (%s) | %s (%d)", dayPrefix, chainDisplayName(sess.ChainID), sess.ChainID),
		fmt.Sprintf("钱包：%s", sess.WalletID),
		fmt.Sprintf("买入笔数：%d（投入 %s %s）", buys, formatWeiToNative(buyWei), sym),
		fmt.Sprintf("卖出笔数：%d（回收 %s %s）", sells, formatWeiToNative(sellWei), sym),
		fmt.Sprintf("当日净现金流：%s%s %s", sign, formatWeiToNative(absNet), sym),
	}, "\n")
	return Reply{
		Text:         text,
		EditOriginal: true,
		Buttons: [][]InlineButton{
			{{Text: "🔄 刷新", CallbackData: "report"}, {Text: "📊 持仓与盈亏", CallbackData: "positions"}},
			{{Text: "⬅️ 返回主菜单", CallbackData: "menu"}},
		},
	}
}
