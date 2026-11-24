package telegram

import (
	"context"
	"errors"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type bloomStubBackend struct {
	stubBackend
}

func (b *bloomStubBackend) ProbeTokenQuote(_ context.Context, _ uint64, _ string, _ common.Address, side string, amountRaw *big.Int) (string, string, *big.Int, uint64, error) {
	if side == "buy" {
		// 1e17 wei (0.1 BNB) -> 1,000,000 raw tokens
		out := new(big.Int).Div(amountRaw, big.NewInt(1e11))
		if out.Sign() <= 0 {
			out = big.NewInt(1)
		}
		return "pancake-v2", "DEX", out, 50_000_000, nil
	}
	// Sell: 1,000,000 raw tokens -> 1.2e17 wei (+20% PnL!)
	out := new(big.Int).Mul(amountRaw, big.NewInt(120_000_000_000))
	return "pancake-v2", "DEX", out, 50_000_001, nil
}

func (b *bloomStubBackend) CheckHoneypot(_ context.Context, _ uint64, _ common.Address) (string, error) {
	return "Honeypot Check\nverdict=SAFE safe=true", nil
}

func TestBloomUIFlowStartPasteCAShadowBuySellAndSettings(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.BotOwner, filepath.Join(t.TempDir(), "bot-bloom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 25, 0, 10, 0, 0, time.UTC)
	router, err := NewRouter(db, &bloomStubBackend{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := router.AllowChat(ctx, 7131128175, "op", "operator", 56, "wallet-bsc-readonly"); err != nil {
		t.Fatal(err)
	}

	// Unauthorized callback must fail closed.
	if _, err := router.HandleCallback(ctx, 999999, "menu"); !errors.Is(err, ErrUnauthorizedChat) {
		t.Fatalf("expected ErrUnauthorizedChat, got %v", err)
	}

	// 1. /start renders Chinese Main Dashboard with Inline Keyboard and zero "Bloom" branding.
	rep, err := router.HandleMessage(ctx, 7131128175, "/start")
	if err != nil || !strings.Contains(rep.Text, "交易终端") || strings.Contains(strings.ToLower(rep.Text), "bloom") || len(rep.Buttons) < 4 {
		t.Fatalf("/start failed: %+v err=%v", rep, err)
	}

	// 2. Switch chain button toggles BSC (56) -> RBH (4663) -> BSC (56).
	rep, err = router.HandleCallback(ctx, 7131128175, "sw_chain")
	if err != nil || !strings.Contains(rep.Text, "RBH (4663)") || !rep.EditOriginal {
		t.Fatalf("sw_chain to RBH failed: %+v err=%v", rep, err)
	}
	rep, err = router.HandleCallback(ctx, 7131128175, "sw_chain")
	if err != nil || !strings.Contains(rep.Text, "BSC (56)") {
		t.Fatalf("sw_chain back to BSC failed: %+v err=%v", rep, err)
	}

	// 3. Paste bare CA -> Instant Token Card with Buy presets.
	ca := "0x55d398326f99059fF775485246999027B3197955"
	card, err := router.HandleMessage(ctx, 7131128175, ca)
	if err != nil || !strings.Contains(card.Text, "代币交易卡") || !strings.Contains(card.Text, "pancake-v2") {
		t.Fatalf("paste CA failed: %+v err=%v", card, err)
	}
	// Extract the 0.1 BNB buy button callback data (sb:<tokKey>:0.1)
	var buy01CB, sellModeCB string
	for _, row := range card.Buttons {
		for _, btn := range row {
			if strings.HasSuffix(btn.CallbackData, ":0.1") {
				buy01CB = btn.CallbackData
			}
			if strings.HasSuffix(btn.CallbackData, ":sell") {
				sellModeCB = btn.CallbackData
			}
		}
	}
	if buy01CB == "" || sellModeCB == "" {
		t.Fatalf("missing buy/sell buttons on token card: %+v", card.Buttons)
	}

	// 4. One-tap Shadow Buy 0.1 BNB -> fills 1,000,000 raw tokens.
	buyRep, err := router.HandleCallback(ctx, 7131128175, buy01CB)
	if err != nil || !strings.Contains(buyRep.Text, "[模拟买入]") || !strings.Contains(buyRep.Text, "raw=1000000") {
		t.Fatalf("shadow buy button failed: %+v err=%v", buyRep, err)
	}

	// 5. Positions card shows +20.0% PnL.
	posRep, err := router.HandleCallback(ctx, 7131128175, "positions")
	if err != nil || !strings.Contains(posRep.Text, "+20.0%") {
		t.Fatalf("positions PnL card failed: %+v err=%v", posRep, err)
	}

	// 6. Switch to Sell Mode & tap Sell 50%.
	sellCard, err := router.HandleCallback(ctx, 7131128175, sellModeCB)
	if err != nil || !strings.Contains(sellCard.Text, "代币交易卡") {
		t.Fatalf("sell mode switch failed: %+v err=%v", sellCard, err)
	}
	var sell50CB string
	for _, row := range sellCard.Buttons {
		for _, btn := range row {
			if strings.HasSuffix(btn.CallbackData, ":50") {
				sell50CB = btn.CallbackData
			}
		}
	}
	now = now.Add(time.Second)
	sellRep, err := router.HandleCallback(ctx, 7131128175, sell50CB)
	if err != nil || !strings.Contains(sellRep.Text, "[模拟卖出 50%]") || !strings.Contains(sellRep.Text, "raw=500000") {
		t.Fatalf("shadow sell 50%% failed: %+v err=%v", sellRep, err)
	}

	// 7. Settings toggles persist cleanly.
	setRep, err := router.HandleCallback(ctx, 7131128175, "set:slip_buy")
	if err != nil || !strings.Contains(setRep.Text, "买入滑点：10.0%") || strings.Contains(strings.ToLower(setRep.Text), "bloom") {
		t.Fatalf("set:slip_buy toggle failed: %+v err=%v", setRep, err)
	}
	setRep, err = router.HandleCallback(ctx, 7131128175, "set:confirm")
	if err != nil || !strings.Contains(setRep.Text, "极速直发") {
		t.Fatalf("set:confirm toggle failed: %+v err=%v", setRep, err)
	}

	// 8. Interactive input (in:presets) & Daily Report (/report) & Wallet switcher (sw_w:).
	if promptRep, err := router.HandleCallback(ctx, 7131128175, "in:presets"); err != nil || !strings.Contains(promptRep.Text, "快捷买入金额") {
		t.Fatalf("in:presets prompt failed: %+v err=%v", promptRep, err)
	}
	if appliedRep, err := router.HandleMessage(ctx, 7131128175, "0.02,0.05,0.2,0.5"); err != nil || !strings.Contains(appliedRep.Text, "0.02,0.05,0.2,0.5") {
		t.Fatalf("in:presets reply failed: %+v err=%v", appliedRep, err)
	}
	if reportRep, err := router.HandleMessage(ctx, 7131128175, "/report"); err != nil || !strings.Contains(reportRep.Text, "今日交易复盘") {
		t.Fatalf("/report failed: %+v err=%v", reportRep, err)
	}
	if swRep, err := router.HandleCallback(ctx, 7131128175, "sw_w:wallet-bsc-2"); err != nil || !strings.Contains(swRep.Text, "wallet-bsc-2") {
		t.Fatalf("sw_w failed: %+v err=%v", swRep, err)
	}
}

func TestM20ToM25FullPanels(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.BotOwner, filepath.Join(t.TempDir(), "bot-m25.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 25, 7, 45, 0, 0, time.UTC)
	router, err := NewRouter(db, &bloomStubBackend{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := router.AllowChat(ctx, 7131128175, "op", "operator", 56, "wallet-bsc-readonly"); err != nil {
		t.Fatal(err)
	}

	// 1. Top L/R chain bar + 5 buy presets + Limit Mode in Token Card
	ca := "0x55d398326f99059fF775485246999027B3197955"
	card, err := router.HandleMessage(ctx, 7131128175, ca)
	if err != nil || len(card.Buttons[0]) != 3 || card.Buttons[0][0].Text != "◀️" || card.Buttons[0][2].Text != "▶️" {
		t.Fatalf("expected top L/R chain switcher row, got %+v err=%v", card.Buttons, err)
	}
	tokKey := strings.Split(card.Buttons[1][0].CallbackData, ":")[1]
	limCard, err := router.HandleCallback(ctx, 7131128175, "tc:"+tokKey+":limit")
	if err != nil || !strings.Contains(limCard.Text, "代币交易卡") {
		t.Fatalf("limit mode failed: %+v err=%v", limCard, err)
	}
	loRep, err := router.HandleCallback(ctx, 7131128175, "lo:buy:"+tokKey+":-10")
	if err != nil || !strings.Contains(loRep.Text, "已挂单") {
		t.Fatalf("relative limit buy failed: %+v err=%v", loRep, err)
	}
	if gasRep, err := router.HandleCallback(ctx, 7131128175, "tc_g:"+tokKey+":buy"); err != nil || !strings.Contains(gasRep.AlertText, "极速") {
		t.Fatalf("in-card gas toggle failed: %+v err=%v", gasRep, err)
	}

	// 2. Copy Profile Detail Card (M22)
	prof := router.ensureCopyProfile(ctx, 56, "0x000000000000000000000000000000000000cafe", "wallet-bsc-readonly")
	cpRep, err := router.HandleCallback(ctx, 7131128175, "cp:open:"+prof.ProfileKey)
	if err != nil || !strings.Contains(cpRep.Text, "跟单策略配置") {
		t.Fatalf("copy profile card failed: %+v err=%v", cpRep, err)
	}
	if cpRep, err = router.HandleCallback(ctx, 7131128175, "cp:pool:"+prof.ProfileKey); err != nil || !strings.Contains(cpRep.AlertText, "内盘") {
		t.Fatalf("copy pool filter toggle failed: %+v err=%v", cpRep, err)
	}

	// 3. Sniper Detail Card (M23)
	if _, err := router.HandleCommand(ctx, 7131128175, "/snipe "+ca+" 50000000000000000 migration"); err != nil {
		t.Fatalf("/snipe failed: %v", err)
	}
	var tid string
	_ = router.readDB.QueryRowContext(ctx, `SELECT task_id FROM sniper_tasks LIMIT 1`).Scan(&tid)
	spRep, err := router.HandleCallback(ctx, 7131128175, "sp:open:"+tid)
	if err != nil || !strings.Contains(spRep.Text, "狙击任务详情") {
		t.Fatalf("sniper detail card failed: %+v err=%v", spRep, err)
	}
	if spRep, err = router.HandleCallback(ctx, 7131128175, "sp:tpsl:"+tid); err != nil || !strings.Contains(spRep.AlertText, "已更新") {
		t.Fatalf("sniper tpsl toggle failed: %+v err=%v", spRep, err)
	}

	// 4. Bridge powered by Houdini (M25)
	brRep, err := router.HandleCallback(ctx, 7131128175, "br:home")
	if err != nil || !strings.Contains(brRep.Text, "Bridge powered by Houdini") {
		t.Fatalf("bridge home failed: %+v err=%v", brRep, err)
	}
	if _, err := router.HandleCallback(ctx, 7131128175, "br:in:BNB:ETH:1"); err != nil {
		t.Fatalf("bridge input prompt failed: %v", err)
	}
	qRep, err := router.HandleMessage(ctx, 7131128175, "0.5 0x1111111111111111111111111111111111111111")
	if err != nil || !strings.Contains(qRep.Text, "Bridge powered by Houdini") || !strings.Contains(qRep.Text, "0.5 BNB") {
		t.Fatalf("bridge quote failed: %+v err=%v", qRep, err)
	}
}
