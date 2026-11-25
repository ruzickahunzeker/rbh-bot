package telegram

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestGlobalEVMWalletSelectionPersistencePermissionsAndDeletion(t *testing.T) {
	ctx := context.Background()
	ksDir := t.TempDir()
	t.Setenv("EVM_KEYSTORE_DIR", ksDir)
	t.Setenv("EVM_WALLET_PASSPHRASE", "TestPassphrase#2026")

	db, err := storage.Open(ctx, storage.BotOwner, filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC)
	clk := func() time.Time {
		now = now.Add(time.Second)
		return now
	}
	router, err := NewRouter(db, &stubBackend{}, clk)
	if err != nil {
		t.Fatal(err)
	}

	const userA int64 = 1001
	const userB int64 = 2002
	if err := router.AllowChat(ctx, userA, "alice", "operator", 56, "wallet-bsc-readonly"); err != nil {
		t.Fatal(err)
	}
	if err := router.AllowChat(ctx, userB, "bob", "operator", 56, "wallet-bsc-readonly"); err != nil {
		t.Fatal(err)
	}

	// 1. User A creates first global EVM wallet -> chain-agnostic ID & auto-activated.
	rep, err := router.HandleCallback(ctx, userA, "wm:create")
	if err != nil {
		t.Fatalf("create first wallet: %v", err)
	}
	sessA, err := router.loadSession(ctx, userA)
	if err != nil {
		t.Fatal(err)
	}
	firstID := sessA.WalletID
	if !strings.HasPrefix(firstID, "w-evm-") {
		t.Fatalf("expected chain-agnostic wallet ID w-evm-*, got %q (alert=%q)", firstID, rep.AlertText)
	}

	// 2. User A creates second global EVM wallet -> active wallet stays on firstID.
	if _, err := router.HandleCallback(ctx, userA, "wm:create"); err != nil {
		t.Fatalf("create second wallet: %v", err)
	}
	sessA, _ = router.loadSession(ctx, userA)
	if sessA.WalletID != firstID {
		t.Fatalf("subsequent wallet creation must not overwrite active wallet: want %s got %s", firstID, sessA.WalletID)
	}

	// 3. Switch chain BSC -> RBH -> BSC: active_wallet_id is preserved and wallet is visible on every chain!
	if _, err := router.HandleCallback(ctx, userA, "sw_c:next"); err != nil {
		t.Fatalf("switch chain: %v", err)
	}
	sessA, _ = router.loadSession(ctx, userA)
	if sessA.ChainID == 56 || sessA.WalletID != firstID {
		t.Fatalf("chain switch must keep active_wallet_id=%s, got chain=%d wallet=%s", firstID, sessA.ChainID, sessA.WalletID)
	}
	wmRep, err := router.HandleCallback(ctx, userA, "wallets")
	if err != nil || !strings.Contains(wmRep.Text, firstID) {
		t.Fatalf("wallet %s must remain visible after chain switch; text=%s err=%v", firstID, wmRep.Text, err)
	}

	// 4. Simulate bot-service restart calling AllowChat with default chain/wallet -> must preserve userA's selection!
	savedChain := sessA.ChainID
	if err := router.AllowChat(ctx, userA, "alice", "operator", 56, "wallet-bsc-readonly"); err != nil {
		t.Fatal(err)
	}
	sessA, _ = router.loadSession(ctx, userA)
	if sessA.ChainID != savedChain || sessA.WalletID != firstID {
		t.Fatalf("restart AllowChat overwrote user selection: want (%d,%s), got (%d,%s)", savedChain, firstID, sessA.ChainID, sessA.WalletID)
	}

	// 5. Permission isolation: User B cannot view, activate, export, or delete User A's wallet.
	if _, ok := router.resolveAuthorizedWallet(ctx, userB, 56, firstID); ok {
		t.Fatalf("userB must not have access to userA's wallet %s", firstID)
	}
	denyAct, _ := router.HandleCallback(ctx, userB, "wm:act:"+firstID)
	if !strings.Contains(denyAct.AlertText, "无权") {
		t.Fatalf("expected unauthorized alert for userB activate, got %+v", denyAct)
	}
	denyExp, _ := router.HandleCallback(ctx, userB, "wm:exp2:"+firstID)
	if !strings.Contains(denyExp.AlertText, "无权") {
		t.Fatalf("expected unauthorized alert for userB export, got %+v", denyExp)
	}

	// 6. Duplicate private key import does not create a duplicate wallet.
	priv, _ := crypto.GenerateKey()
	privHex := "0x" + hex.EncodeToString(crypto.FromECDSA(priv))
	r1, ok1 := router.handleWalletOrBridgePendingInput(ctx, sessA, "wm_import", "evm", privHex)
	if !ok1 || !strings.Contains(r1.Text, "已安全导入") {
		t.Fatalf("first import failed: %+v", r1)
	}
	beforeCount := len(router.syncAndListAuthorizedWallets(ctx, userA, sessA.ChainID))
	r2, ok2 := router.handleWalletOrBridgePendingInput(ctx, sessA, "wm_import", "evm", privHex)
	afterCount := len(router.syncAndListAuthorizedWallets(ctx, userA, sessA.ChainID))
	if !ok2 || !strings.Contains(r2.Text, "已存在，未重复创建") || beforeCount != afterCount {
		t.Fatalf("duplicate import must not create duplicate wallet: before=%d after=%d text=%s", beforeCount, afterCount, r2.Text)
	}

	// 7. Deletion guard: active sniper task on another chain (chain 56 while user is on savedChain) blocks deletion.
	sqlDB, _ := db.SQLDB(storage.BotOwner)
	_, err = sqlDB.ExecContext(ctx,
		`INSERT INTO sniper_tasks(task_id, chain_id, wallet_id, token, trigger_kind, amount_in_wei, max_slippage_bps, state, created_at, updated_at)
		 VALUES('snipe-1', 56, ?, '0x55d398326f99059ff775485246999027b3197955', 'migration', '1000', 500, 'ARMED', '2026-11-25T00:00:00Z', '2026-11-25T00:00:00Z')`,
		firstID,
	)
	if err != nil {
		t.Fatal(err)
	}
	delBlocked, _ := router.HandleCallback(ctx, userA, "wm:del2:"+firstID)
	if !strings.Contains(delBlocked.AlertText, "禁止删除") {
		t.Fatalf("expected deletion blocked by active sniper task, got %+v", delBlocked)
	}

	// Clear active sniper task -> deletion now succeeds and updates active_wallet_id away from deleted wallet!
	_, _ = sqlDB.ExecContext(ctx, `UPDATE sniper_tasks SET state = 'CANCELLED' WHERE task_id = 'snipe-1'`)
	if _, err := router.HandleCallback(ctx, userA, "wm:del2:"+firstID); err != nil {
		t.Fatalf("delete after clearing blockers: %v", err)
	}
	sessA, _ = router.loadSession(ctx, userA)
	if sessA.WalletID == firstID || sessA.WalletID == "" {
		t.Fatalf("deleting active wallet must switch active_wallet_id to another authorized wallet, got %q", sessA.WalletID)
	}
}
