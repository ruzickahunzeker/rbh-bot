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
// and callback toast notifications.
type Reply struct {
	Text         string
	Buttons      [][]InlineButton
	EditOriginal bool
	AlertText    string
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
		BuyPresetsCSV:     "0.05,0.1,0.5,1",
		BuySlippageBps:    500,
		SellSlippageBps:   1000,
		GasTier:           "fast",
		AntiMEV:           true,
		ConfirmBeforeLive: true,
	}
	var antiMEV, confirm int
	err := r.readDB.QueryRowContext(ctx,
		`SELECT buy_presets_csv, buy_slippage_bps, sell_slippage_bps, gas_tier, anti_mev, confirm_before_live
		 FROM bloom_user_settings WHERE chat_id = ?`,
		chatID,
	).Scan(&st.BuyPresetsCSV, &st.BuySlippageBps, &st.SellSlippageBps, &st.GasTier, &antiMEV, &confirm)
	if err == nil {
		st.AntiMEV = antiMEV == 1
		st.ConfirmBeforeLive = confirm == 1
	}
	return st
}

func (r *Router) saveBloomSettings(ctx context.Context, chatID int64, st bloomSettings) error {
	antiInt, confInt := 0, 0
	if st.AntiMEV {
		antiInt = 1
	}
	if st.ConfirmBeforeLive {
		confInt = 1
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO bloom_user_settings(chat_id, buy_presets_csv, buy_slippage_bps, sell_slippage_bps, gas_tier, anti_mev, confirm_before_live, updated_at)
		 VALUES(?,?,?,?,?,?,?,?)
		 ON CONFLICT(chat_id) DO UPDATE SET
		   buy_presets_csv=excluded.buy_presets_csv,
		   buy_slippage_bps=excluded.buy_slippage_bps,
		   sell_slippage_bps=excluded.sell_slippage_bps,
		   gas_tier=excluded.gas_tier,
		   anti_mev=excluded.anti_mev,
		   confirm_before_live=excluded.confirm_before_live,
		   updated_at=excluded.updated_at`,
		chatID, st.BuyPresetsCSV, st.BuySlippageBps, st.SellSlippageBps, st.GasTier, antiInt, confInt,
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
	// Bloom signature UX: paste any 42-char EVM contract address to pop up the interactive Token Card.
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
	case data == "menu":
		return r.renderMainMenu(ctx, sess, true), nil

	case data == "sw_chain":
		newChain := uint64(4663)
		newWallet := "wallet-rbh-readonly"
		if sess.ChainID == 4663 {
			newChain = 56
			newWallet = "wallet-bsc-readonly"
		}
		if _, err := r.db.ExecContext(ctx,
			`UPDATE telegram_allowlist SET active_chain_id = ?, active_wallet_id = ?, updated_at = ? WHERE chat_id = ?`,
			newChain, newWallet, r.now().UTC().Format(time.RFC3339Nano), chatID,
		); err != nil {
			return Reply{}, err
		}
		sess.ChainID = newChain
		sess.WalletID = newWallet
		rep := r.renderMainMenu(ctx, sess, true)
		rep.AlertText = fmt.Sprintf("Switched to %s (%d)", chainDisplayName(newChain), newChain)
		return rep, nil

	case data == "status":
		txt := "Status backend unavailable."
		if sb, ok := r.backend.(StatusBackend); ok {
			if s, err := sb.CheckStatus(ctx); err == nil {
				txt = s
			} else {
				txt = "Status error: " + err.Error()
			}
		}
		return Reply{
			Text:         txt,
			EditOriginal: true,
			Buttons: [][]InlineButton{
				{{Text: "🔄 Refresh", CallbackData: "status"}, {Text: "⬅️ Menu", CallbackData: "menu"}},
			},
		}, nil

	case data == "wallets":
		wallets, err := r.backend.ListWallets(ctx, sess.ChainID)
		var body string
		if err != nil {
			body = fmt.Sprintf("👛 Wallets (%s %d)\nError: %v", chainDisplayName(sess.ChainID), sess.ChainID, err)
		} else if len(wallets) == 0 {
			body = fmt.Sprintf("👛 Wallets (%s %d)\nNo registered wallets.", chainDisplayName(sess.ChainID), sess.ChainID)
		} else {
			body = fmt.Sprintf("👛 Wallets (%s %d)\nActive: %s\n\n%s", chainDisplayName(sess.ChainID), sess.ChainID, sess.WalletID, strings.Join(wallets, "\n"))
		}
		return Reply{
			Text:         body,
			EditOriginal: true,
			Buttons: [][]InlineButton{
				{{Text: "🔄 Refresh", CallbackData: "wallets"}, {Text: "⬅️ Menu", CallbackData: "menu"}},
			},
		}, nil

	case data == "positions":
		return r.renderPositionsCard(ctx, sess), nil

	case data == "copy":
		return r.renderCopyCard(ctx, sess), nil

	case data == "orders":
		return r.renderOrdersCard(ctx, sess), nil

	case data == "settings":
		return r.renderSettingsCard(ctx, sess, ""), nil

	case strings.HasPrefix(data, "set:"):
		return r.handleSettingToggle(ctx, sess, strings.TrimPrefix(data, "set:")), nil

	case strings.HasPrefix(data, "tc:"):
		// tc:<tokKey>:<buy|sell>
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			return Reply{AlertText: "Invalid token card action"}, nil
		}
		cid, tok, _, err := r.lookupUIToken(ctx, parts[1])
		if err != nil {
			return Reply{AlertText: "Token expired; paste CA again"}, nil
		}
		sess.ChainID = cid
		return r.renderTokenCard(ctx, sess, tok, parts[2], true, ""), nil

	case strings.HasPrefix(data, "sb:"):
		// sb:<tokKey>:<amtNative>
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			return Reply{AlertText: "Invalid buy action"}, nil
		}
		return r.executeShadowBuyButton(ctx, sess, parts[1], parts[2]), nil

	case strings.HasPrefix(data, "ss:"):
		// ss:<tokKey>:<pct>
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			return Reply{AlertText: "Invalid sell action"}, nil
		}
		return r.executeShadowSellButton(ctx, sess, parts[1], parts[2]), nil

	default:
		return Reply{AlertText: "Unknown action"}, nil
	}
}

func (r *Router) renderMainMenu(ctx context.Context, sess chatSession, edit bool) Reply {
	chainName := chainDisplayName(sess.ChainID)
	sym := chainNativeSymbol(sess.ChainID)
	otherChain := "RBH (4663)"
	if sess.ChainID == 4663 {
		otherChain = "BSC (56)"
	}
	var watchedCnt, ordersCnt, shadowFills int
	_ = r.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM watched_wallets WHERE chain_id = ? AND enabled = 1`, sess.ChainID).Scan(&watchedCnt)
	_ = r.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM conditional_orders WHERE chain_id = ? AND state = 'ACTIVE'`, sess.ChainID).Scan(&ordersCnt)
	_ = r.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM paper_shadow_fills WHERE chain_id = ? AND wallet_id = ?`, sess.ChainID, sess.WalletID).Scan(&shadowFills)

	text := strings.Join([]string{
		fmt.Sprintf("🌸 evm-bot (Bloom UI) | %s (%d)", chainName, sess.ChainID),
		"Mode: 🟡 SHADOW / READ-ONLY (live_enabled=false)",
		fmt.Sprintf("Wallet: %s (%s)", sess.WalletID, sym),
		fmt.Sprintf("Watched: %d | Orders: %d | Shadow Fills: %d", watchedCnt, ordersCnt, shadowFills),
		"",
		"💡 直接发送任意 0x... 合约地址，秒出代币交易卡（自动探测内盘/DEX + 貔貅检测 + 一键模拟买卖）。",
	}, "\n")

	return Reply{
		Text:         text,
		EditOriginal: edit,
		Buttons: [][]InlineButton{
			{{Text: "📊 Positions", CallbackData: "positions"}, {Text: "👥 Copy Trade", CallbackData: "copy"}},
			{{Text: "📈 Limit Orders", CallbackData: "orders"}, {Text: "👛 Wallets", CallbackData: "wallets"}},
			{{Text: "⚙️ Settings", CallbackData: "settings"}, {Text: "📡 Status", CallbackData: "status"}},
			{{Text: "🔄 Switch to " + otherChain, CallbackData: "sw_chain"}},
		},
	}
}

func (r *Router) renderTokenCard(ctx context.Context, sess chatSession, token common.Address, mode string, edit bool, banner string) Reply {
	if mode != "sell" {
		mode = "buy"
	}
	st := r.loadBloomSettings(ctx, sess.ChatID)
	sym := chainNativeSymbol(sess.ChainID)
	probeAmt := parseNativeToWei("0.01")

	protoLabel := "unrouted"
	quoteLine := "Quote: unavailable"
	detectedProto := ""
	if pb, ok := r.backend.(ProbeQuoteBackend); ok {
		proto, phase, out, blk, err := pb.ProbeTokenQuote(ctx, sess.ChainID, sess.WalletID, token, "buy", probeAmt)
		if err == nil {
			detectedProto = proto
			protoLabel = fmt.Sprintf("%s (%s) @ blk %d", proto, phase, blk)
			quoteLine = fmt.Sprintf("0.01 %s -> %s raw", sym, out.String())
		} else {
			quoteLine = fmt.Sprintf("Quote: %v", err)
		}
	}
	tokKey := r.registerUIToken(ctx, sess.ChainID, token, detectedProto)

	hpLine := "Honeypot: n/a"
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
		fmt.Sprintf("🪙 Token Card | %s (%d)", chainDisplayName(sess.ChainID), sess.ChainID),
		fmt.Sprintf("CA: %s", strings.ToLower(token.Hex())),
		fmt.Sprintf("Route: %s", protoLabel),
		quoteLine,
		hpLine,
		fmt.Sprintf("Shadow Bal: %s raw (cost=%s %s)", shadowBal.String(), formatWeiToNative(shadowCost), sym),
	}
	if banner != "" {
		lines = append(lines, "", banner)
	}

	buyModeBtn := "Buy Mode"
	sellModeBtn := "Sell Mode"
	if mode == "buy" {
		buyModeBtn = "🟢 Buy Mode"
	} else {
		sellModeBtn = "🔴 Sell Mode"
	}

	var rows [][]InlineButton
	rows = append(rows, []InlineButton{
		{Text: buyModeBtn, CallbackData: "tc:" + tokKey + ":buy"},
		{Text: sellModeBtn, CallbackData: "tc:" + tokKey + ":sell"},
		{Text: "🔄 Refresh", CallbackData: "tc:" + tokKey + ":" + mode},
	})

	if mode == "buy" {
		presets := strings.Split(st.BuyPresetsCSV, ",")
		var row1, row2 []InlineButton
		for i, p := range presets {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			btn := InlineButton{Text: fmt.Sprintf("🟡 Buy %s %s", p, sym), CallbackData: "sb:" + tokKey + ":" + p}
			if i < 2 {
				row1 = append(row1, btn)
			} else {
				row2 = append(row2, btn)
			}
		}
		if len(row1) > 0 {
			rows = append(rows, row1)
		}
		if len(row2) > 0 {
			rows = append(rows, row2)
		}
	} else {
		rows = append(rows,
			[]InlineButton{
				{Text: "🔴 Sell 25%", CallbackData: "ss:" + tokKey + ":25"},
				{Text: "🔴 Sell 50%", CallbackData: "ss:" + tokKey + ":50"},
			},
			[]InlineButton{
				{Text: "🔴 Sell 75%", CallbackData: "ss:" + tokKey + ":75"},
				{Text: "🔴 Sell 100%", CallbackData: "ss:" + tokKey + ":100"},
			},
		)
	}

	mevLabel := "🛡️ Anti-MEV: ON"
	if !st.AntiMEV && sess.ChainID != 56 {
		mevLabel = "MEV: OFF"
	}
	slipBps := st.BuySlippageBps
	if mode == "sell" {
		slipBps = st.SellSlippageBps
	}
	rows = append(rows, []InlineButton{
		{Text: mevLabel, CallbackData: "settings"},
		{Text: fmt.Sprintf("Slip: %.1f%%", float64(slipBps)/100.0), CallbackData: "settings"},
		{Text: "⬅️ Menu", CallbackData: "menu"},
	})

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
	spot := new(big.Int).Mul(amtWei, big.NewInt(1_000_000_000))
	spot.Div(spot, outRaw)
	if spot.Sign() <= 0 {
		spot = big.NewInt(1)
	}
	nowUTC := r.now().UTC()
	fillID := fmt.Sprintf("ui-buy-%d-%d", sess.ChainID, nowUTC.UnixNano())
	_, _ = r.db.ExecContext(ctx,
		`INSERT INTO paper_shadow_fills(fill_id, chain_id, wallet_id, token, side, amount_in_raw, simulated_out_raw, spot_price_wei, source_ref, created_at)
		 VALUES(?,?,?,?,'buy',?,?,?,?,?)`,
		fillID, sess.ChainID, sess.WalletID, strings.ToLower(tok.Hex()),
		amtWei.String(), outRaw.String(), spot.String(), "bloom-ui:"+proto, nowUTC.Format(time.RFC3339Nano),
	)
	banner := fmt.Sprintf("✅ [SHADOW BUY] %s %s -> +%s raw via %s", amtNativeStr, chainNativeSymbol(sess.ChainID), outRaw.String(), proto)
	rep := r.renderTokenCard(ctx, sess, tok, "buy", true, banner)
	rep.AlertText = "Shadow Buy filled!"
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
	spot := new(big.Int).Mul(outWei, big.NewInt(1_000_000_000))
	spot.Div(spot, sellAmt)
	if spot.Sign() <= 0 {
		spot = big.NewInt(1)
	}
	nowUTC := r.now().UTC()
	fillID := fmt.Sprintf("ui-sell-%d-%d", sess.ChainID, nowUTC.UnixNano())
	_, _ = r.db.ExecContext(ctx,
		`INSERT INTO paper_shadow_fills(fill_id, chain_id, wallet_id, token, side, amount_in_raw, simulated_out_raw, spot_price_wei, source_ref, created_at)
		 VALUES(?,?,?,?,'sell',?,?,?,?,?)`,
		fillID, sess.ChainID, sess.WalletID, strings.ToLower(tok.Hex()),
		sellAmt.String(), outWei.String(), spot.String(), "bloom-ui:"+proto, nowUTC.Format(time.RFC3339Nano),
	)
	banner := fmt.Sprintf("✅ [SHADOW SELL %d%%] -%s raw -> +%s %s via %s", pct, sellAmt.String(), formatWeiToNative(outWei), chainNativeSymbol(sess.ChainID), proto)
	rep := r.renderTokenCard(ctx, sess, tok, "sell", true, banner)
	rep.AlertText = "Shadow Sell filled!"
	return rep
}

func (r *Router) renderPositionsCard(ctx context.Context, sess chatSession) Reply {
	rows, err := r.readDB.QueryContext(ctx,
		`SELECT DISTINCT token FROM paper_shadow_fills WHERE chain_id = ? AND wallet_id = ? ORDER BY created_at DESC LIMIT 10`,
		sess.ChainID, sess.WalletID,
	)
	sym := chainNativeSymbol(sess.ChainID)
	lines := []string{fmt.Sprintf("📊 Shadow Positions | %s (%d)", chainDisplayName(sess.ChainID), sess.ChainID)}
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
					pnlStr = fmt.Sprintf(" | Val: %s %s (%+.1f%%)", formatWeiToNative(valWei), sym, pctVal*100)
				}
			}
			shortTok := tokHex
			if len(shortTok) > 12 {
				shortTok = shortTok[:6] + "..." + shortTok[len(shortTok)-4:]
			}
			lines = append(lines, fmt.Sprintf("%d. %s | Bal: %s | Cost: %s %s%s", count, shortTok, bal.String(), formatWeiToNative(cost), sym, pnlStr))
			tokKey := r.registerUIToken(ctx, sess.ChainID, tok, "")
			buttons = append(buttons, []InlineButton{{Text: fmt.Sprintf("🪙 Open %s", shortTok), CallbackData: "tc:" + tokKey + ":sell"}})
		}
	}
	if count == 0 {
		lines = append(lines, "No open Shadow positions yet. Paste any token CA to simulate a quick buy!")
	}
	buttons = append(buttons, []InlineButton{{Text: "🔄 Refresh", CallbackData: "positions"}, {Text: "⬅️ Menu", CallbackData: "menu"}})
	return Reply{Text: strings.Join(lines, "\n"), Buttons: buttons, EditOriginal: true}
}

func (r *Router) renderCopyCard(ctx context.Context, sess chatSession) Reply {
	lines := []string{fmt.Sprintf("👥 Copy Trade Watchlist | %s (%d)", chainDisplayName(sess.ChainID), sess.ChainID)}
	if wb, ok := r.backend.(WatchlistManagerBackend); ok {
		list, err := wb.ListWatchedWallets(ctx, sess.ChainID)
		if err != nil {
			lines = append(lines, "Error: "+err.Error())
		} else if len(list) == 0 {
			lines = append(lines, "No watched wallets yet.\nUse: /watch <0xAddress>")
		} else {
			for i, addr := range list {
				lines = append(lines, fmt.Sprintf("%d. 🟢 %s", i+1, addr))
			}
			lines = append(lines, "", "Add: /watch <0x...> | Remove: /unwatch <0x...>")
		}
	}
	return Reply{
		Text:         strings.Join(lines, "\n"),
		EditOriginal: true,
		Buttons: [][]InlineButton{
			{{Text: "🔄 Refresh", CallbackData: "copy"}, {Text: "⬅️ Menu", CallbackData: "menu"}},
		},
	}
}

func (r *Router) renderOrdersCard(ctx context.Context, sess chatSession) Reply {
	lines := []string{fmt.Sprintf("📈 Limit / TP / SL Orders | %s (%d)", chainDisplayName(sess.ChainID), sess.ChainID)}
	rows, err := r.readDB.QueryContext(ctx,
		`SELECT order_id, order_kind, token, trigger_price_wei, sell_bps, state
		 FROM conditional_orders WHERE chain_id = ? AND state IN ('ACTIVE', 'TRIGGERED')
		 ORDER BY created_at DESC LIMIT 10`,
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
				lines = append(lines, fmt.Sprintf("%d. [%s] %s %s trig=%s sell=%d%% (%s)", cnt, st, kind, tok[:10]+"...", trig, bps/100, oid))
			}
		}
	}
	if cnt == 0 {
		lines = append(lines, "No active orders.\nCreate: /tp <token> <price_wei> [pct] or /sl <token> <price_wei> [pct]")
	}
	return Reply{
		Text:         strings.Join(lines, "\n"),
		EditOriginal: true,
		Buttons: [][]InlineButton{
			{{Text: "🔄 Refresh", CallbackData: "orders"}, {Text: "⬅️ Menu", CallbackData: "menu"}},
		},
	}
}

func (r *Router) renderSettingsCard(ctx context.Context, sess chatSession, alert string) Reply {
	st := r.loadBloomSettings(ctx, sess.ChatID)
	mevStr := "ON (private_only locked on BSC)"
	if !st.AntiMEV && sess.ChainID != 56 {
		mevStr = "OFF"
	}
	confStr := "ON (Confirm Popup)"
	if !st.ConfirmBeforeLive {
		confStr = "OFF (Degen Instant)"
	}
	text := strings.Join([]string{
		fmt.Sprintf("⚙️ Bloom Settings | %s (%d)", chainDisplayName(sess.ChainID), sess.ChainID),
		fmt.Sprintf("Buy Slippage: %.1f%% | Sell Slippage: %.1f%%", float64(st.BuySlippageBps)/100.0, float64(st.SellSlippageBps)/100.0),
		fmt.Sprintf("Gas Tier: %s", strings.ToUpper(st.GasTier)),
		fmt.Sprintf("Anti-MEV: %s", mevStr),
		fmt.Sprintf("Live Confirm: %s", confStr),
		fmt.Sprintf("Buy Presets: %s %s", st.BuyPresetsCSV, chainNativeSymbol(sess.ChainID)),
	}, "\n")
	return Reply{
		Text:         text,
		EditOriginal: true,
		AlertText:    alert,
		Buttons: [][]InlineButton{
			{{Text: fmt.Sprintf("Buy Slip: %.0f%%", float64(st.BuySlippageBps)/100.0), CallbackData: "set:slip_buy"}, {Text: fmt.Sprintf("Sell Slip: %.0f%%", float64(st.SellSlippageBps)/100.0), CallbackData: "set:slip_sell"}},
			{{Text: "⛽ Gas: " + strings.ToUpper(st.GasTier), CallbackData: "set:gas"}, {Text: "🛡️ Anti-MEV", CallbackData: "set:mev"}},
			{{Text: "Confirm Live: " + boolOnOff(st.ConfirmBeforeLive), CallbackData: "set:confirm"}},
			{{Text: "⬅️ Menu", CallbackData: "menu"}},
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
	case 56:
		return "BSC"
	case 8453:
		return "Base"
	default:
		return "RBH"
	}
}

func chainNativeSymbol(chainID uint64) string {
	if chainID == 56 {
		return "BNB"
	}
	return "ETH"
}

func boolOnOff(v bool) string {
	if v {
		return "ON"
	}
	return "OFF"
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
