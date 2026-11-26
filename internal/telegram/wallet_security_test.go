package telegram

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	tradecore "github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

func walletSecurityRouter(t *testing.T) (*Router, *storage.Database, tradecore.KeystoreSummary) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("EVM_KEYSTORE_DIR", dir)
	t.Setenv("EVM_WALLET_PASSPHRASE", "wallet-security-passphrase")
	db, err := storage.Open(ctx, storage.BotOwner, filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := NewRouter(db, &stubBackend{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	w, err := tradecore.CreateGlobalEncryptedWallet(dir, "owned-wallet", 1001, "wallet-security-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.registerGlobalWalletRow(ctx, GlobalWallet{WalletID: w.WalletID, Address: w.Address, WalletType: "keystore", KeystorePath: w.FilePath, OwnerChatID: 1001}); err != nil {
		t.Fatal(err)
	}
	if err = r.AllowChat(ctx, 1001, "alice", "operator", 56, w.WalletID); err != nil {
		t.Fatal(err)
	}
	if err = r.AllowChat(ctx, 2002, "bob", "operator", 4663, "other"); err != nil {
		t.Fatal(err)
	}
	return r, db, w
}
func walletSubmissionDecision(t *testing.T, db *sql.DB, walletID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO copy_signal_decisions(decision_id,principal_id,strategy_id,chain_id,trigger_id,action_kind,wallet_id,token,trade_idempotency_key,disposition,created_at) VALUES('decision','principal','strategy',4663,'trigger','copy_buy',?,'token','idem','QUEUED','2026-11-26T00:00:00Z')`, walletID)
	if err != nil {
		t.Fatal(err)
	}
}
func insertSecurityJob(db *sql.DB, id, state string) error {
	_, err := db.Exec(`INSERT INTO submission_jobs(job_id,decision_id,chain_id,wallet_id,idempotency_key,state,created_at,updated_at) VALUES('job','decision',4663,?,'idem',?,'2026-11-26T00:00:00Z','2026-11-26T00:00:00Z')`, id, state)
	return err
}

func TestWalletSecurityAllNonterminalJobsBlockDeletion(t *testing.T) {
	for _, state := range []string{"PENDING", "DISPATCHING", "SUBMITTED"} {
		t.Run(state, func(t *testing.T) {
			r, _, w := walletSecurityRouter(t)
			walletSubmissionDecision(t, r.db, w.WalletID)
			if err := insertSecurityJob(r.db, w.WalletID, state); err != nil {
				t.Fatal(err)
			}
			rep, err := r.HandleCallback(context.Background(), 1001, "wm:del2:"+w.WalletID)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rep.AlertText, "禁止删除") {
				t.Fatalf("%s deletion alert=%s", state, rep.AlertText)
			}
			if _, err = os.Stat(w.FilePath); err != nil {
				t.Fatal("nonterminal job lost its key")
			}
			var actual string
			r.db.QueryRow(`SELECT state FROM evm_wallets WHERE wallet_id=?`, w.WalletID).Scan(&actual)
			if actual != "ACTIVE" {
				t.Fatal("blocked deletion did not roll back lifecycle")
			}
		})
	}
}

func TestWalletSecurityDeletionDatabaseFailureIsClosed(t *testing.T) {
	r, _, w := walletSecurityRouter(t)
	if _, err := r.db.Exec(`DROP TABLE conditional_orders`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.walletDeleteBlockers(context.Background(), w.WalletID); err == nil {
		t.Fatal("deletion query failure was ignored")
	}
	if err := r.deleteGlobalWallet(context.Background(), 1001, w.WalletID); err == nil {
		t.Fatal("failed deletion checks were bypassed")
	}
	if _, err := os.Stat(w.FilePath); err != nil {
		t.Fatal("database failure deleted the file")
	}
	if err := tradecore.CheckWalletAvailable(keystoreDirPath(), w.WalletID); err != nil {
		t.Fatal("pre-check failure left a deletion marker")
	}
	var state string
	r.db.QueryRow(`SELECT state FROM evm_wallets WHERE wallet_id=?`, w.WalletID).Scan(&state)
	if state != "ACTIVE" {
		t.Fatal("failed check changed lifecycle state")
	}
}

func TestWalletSecurityStaleEnqueueAndReactivationAreBlocked(t *testing.T) {
	r, _, w := walletSecurityRouter(t)
	walletSubmissionDecision(t, r.db, w.WalletID)
	var sequence int
	var databaseName, databasePath string
	if err := r.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &databaseName, &databasePath); err != nil {
		t.Fatal(err)
	}
	second, err := storage.Open(context.Background(), storage.BotOwner, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	otherWriter, err := second.SQLDB(storage.BotOwner)
	if err != nil {
		t.Fatal(err)
	}
	checked := make(chan struct{})
	resume := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		var state string
		err := otherWriter.QueryRow(`SELECT state FROM evm_wallets WHERE wallet_id=?`, w.WalletID).Scan(&state)
		if err != nil || state != "ACTIVE" {
			done <- errors.New("precheck failed")
			close(checked)
			return
		}
		close(checked)
		<-resume
		done <- insertSecurityJob(otherWriter, w.WalletID, "PENDING")
	}()
	<-checked
	if err := r.deleteGlobalWallet(context.Background(), 1001, w.WalletID); err != nil {
		t.Fatal(err)
	}
	close(resume)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "WALLET_NOT_ACTIVE") {
		t.Fatalf("stale enqueue escaped lifecycle boundary: %v", err)
	}
	for _, statement := range []string{
		`INSERT INTO conditional_orders(order_id,chain_id,wallet_id,token,order_kind,trigger_price_wei,idempotency_key,state,expires_at,created_at,updated_at) VALUES('o',56,'owned-wallet','token','limit_buy','1','order-key','ACTIVE','later','now','now')`,
		`INSERT INTO sniper_tasks(task_id,chain_id,wallet_id,token,trigger_kind,amount_in_wei,state,created_at,updated_at) VALUES('s',56,'owned-wallet','token','launch','1','ARMED','now','now')`,
		`INSERT INTO copy_wallet_profiles(profile_key,chain_id,address,wallet_id,enabled,updated_at) VALUES('c',56,'address','owned-wallet',1,'now')`,
	} {
		if _, err := r.db.Exec(statement); err == nil || !strings.Contains(err.Error(), "WALLET_NOT_ACTIVE") {
			t.Fatalf("task write was not blocked: %v", err)
		}
	}
	// Disabling/history writes remain possible, but re-enabling the profile must fail.
	if _, err := r.db.Exec(`INSERT INTO copy_wallet_profiles(profile_key,chain_id,address,wallet_id,enabled,updated_at) VALUES('disabled',56,'address','owned-wallet',0,'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE copy_wallet_profiles SET enabled=1 WHERE profile_key='disabled'`); err == nil {
		t.Fatal("re-enabled strategy on a deleted wallet")
	}
	if _, _, err := tradecore.LoadEncryptedWallet(w.FilePath, "wallet-security-passphrase"); !errors.Is(err, tradecore.ErrWalletDeleting) {
		t.Fatalf("deleted key loader=%v", err)
	}
}

func TestWalletSecurityDeletingTransactionWaitsForExistingEnqueue(t *testing.T) {
	r, _, w := walletSecurityRouter(t)
	walletSubmissionDecision(t, r.db, w.WalletID)
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO submission_jobs(job_id,decision_id,chain_id,wallet_id,idempotency_key,state,created_at,updated_at) VALUES('job','decision',4663,?,'idem','PENDING','now','now')`, w.WalletID); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() { close(started); done <- r.deleteGlobalWallet(context.Background(), 1001, w.WalletID) }()
	<-started
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil || !strings.Contains(err.Error(), "禁止删除") {
		t.Fatalf("deletion ignored a committed enqueue: %v", err)
	}
	if _, err = os.Stat(w.FilePath); err != nil {
		t.Fatal("enqueue winner lost its key")
	}
}

func TestWalletSecurityCrashRecoveryAndNoResurrection(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback-marker", true: "committed-deletion"}[committed], func(t *testing.T) {
			r, db, w := walletSecurityRouter(t)
			ctx := context.Background()
			if err := tradecore.WithWalletDirectoryLock(keystoreDirPath(), func() error {
				if err := tradecore.MarkWalletDeletingLocked(keystoreDirPath(), w.WalletID); err != nil {
					return err
				}
				if committed {
					_, err := r.db.Exec(`UPDATE evm_wallets SET state='DELETING' WHERE wallet_id=?`, w.WalletID)
					return err
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// Constructor is the restart recovery boundary.
			next, err := NewRouter(db, &stubBackend{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if committed {
				if _, err = os.Stat(w.FilePath); !os.IsNotExist(err) {
					t.Fatal("restart did not finish committed deletion")
				}
				if _, ok := next.resolveAuthorizedWallet(ctx, 1001, 56, w.WalletID); ok {
					t.Fatal("catalog resurrected a deleted identity")
				}
				if err := next.AllowChat(ctx, 1001, "alice", "operator", 56, w.WalletID); err != nil {
					t.Fatal(err)
				}
				session, err := next.loadSession(ctx, 1001)
				if err != nil || session.WalletID == w.WalletID {
					t.Fatal("restart retained a deleted active wallet")
				}
				if err := next.registerGlobalWalletRow(ctx, GlobalWallet{WalletID: w.WalletID, Address: w.Address, WalletType: "keystore", KeystorePath: w.FilePath, OwnerChatID: 1001}); err == nil {
					t.Fatal("sync revived deleted identity")
				}
			} else {
				if _, err = os.Stat(w.FilePath); err != nil {
					t.Fatal("failed DB commit lost old file")
				}
				if err = tradecore.CheckWalletAvailable(keystoreDirPath(), w.WalletID); err != nil {
					t.Fatal("rollback marker was not cleared")
				}
			}
		})
	}
}

func TestWalletSecurityLabelsAndExplicitRecoveryUI(t *testing.T) {
	r, _, w := walletSecurityRouter(t)
	ctx := context.Background()
	rep, err := r.HandleCallback(ctx, 1001, "wm:label:"+w.WalletID)
	if err != nil || !strings.Contains(rep.Text, "32") {
		t.Fatal("label prompt missing")
	}
	session, _ := r.loadSession(ctx, 1001)
	changed, handled := r.handleWalletOrBridgePendingInput(ctx, session, "wm_label", w.WalletID, "主钱包 ✨")
	if !handled || !strings.Contains(changed.Text, "主钱包 ✨") {
		t.Fatalf("label change=%+v", changed)
	}
	before, _ := os.ReadFile(w.FilePath)
	bob, _ := r.loadSession(ctx, 2002)
	denied, _ := r.handleWalletOrBridgePendingInput(ctx, bob, "wm_label", w.WalletID, "hijack")
	if !strings.Contains(denied.Text, "无权") {
		t.Fatal("foreign label edit allowed")
	}
	tooLong, _ := r.handleWalletOrBridgePendingInput(ctx, session, "wm_label", w.WalletID, strings.Repeat("字", 33))
	if !strings.Contains(tooLong.Text, "32") {
		t.Fatal("oversized label allowed")
	}
	after, _ := os.ReadFile(w.FilePath)
	if string(before) != string(after) {
		t.Fatal("label edit changed encrypted wallet")
	}
	// Explicit recovery requires a second deliberate input, never retains the first plaintext input.
	key, _, err := tradecore.LoadEncryptedWallet(w.FilePath, "wallet-security-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	private := hex.EncodeToString(crypto.FromECDSA(key))
	t.Setenv("EVM_WALLET_PASSPHRASE", "new-wallet-passphrase")
	failed, _ := r.handleWalletOrBridgePendingInput(ctx, session, "wm_import", "evm", private)
	if !failed.DeleteUserMessage || len(failed.Buttons) == 0 {
		t.Fatal("recovery did not require explicit confirmation")
	}
	restored, _ := r.handleWalletOrBridgePendingInput(ctx, session, "wm_recover", "evm", private)
	if !restored.DeleteUserMessage || !strings.Contains(restored.AlertText, "恢复钱包加密") {
		t.Fatalf("explicit recovery=%+v", restored)
	}
	if _, _, err = tradecore.LoadEncryptedWallet(w.FilePath, "new-wallet-passphrase"); err != nil {
		t.Fatal(err)
	}
	// Random IDs remain distinct even under a fixed application clock.
	r.now = func() time.Time { return time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC) }
	first, _ := r.HandleCallback(ctx, 1001, "wm:create")
	second, _ := r.HandleCallback(ctx, 1001, "wm:create")
	if strings.Contains(first.AlertText, "失败") || strings.Contains(second.AlertText, "失败") {
		t.Fatal("fixed clock caused wallet ID collisions")
	}
	wallets := r.syncAndListAuthorizedWallets(ctx, 1001, 56)
	if len(wallets) != 3 {
		t.Fatalf("expected three distinct wallets, got %d", len(wallets))
	}
}

func TestWalletSecuritySharedLabelsArePerChat(t *testing.T) {
	r, _, _ := walletSecurityRouter(t)
	ctx := context.Background()
	shared := GlobalWallet{WalletID: "watch", Address: common.HexToAddress("0x5555"), WalletType: "readonly"}
	if err := r.registerGlobalWalletRow(ctx, shared); err != nil {
		t.Fatal(err)
	}
	alice, _ := r.loadSession(ctx, 1001)
	bob, _ := r.loadSession(ctx, 2002)
	r.handleWalletOrBridgePendingInput(ctx, alice, "wm_label", "watch", "Alice label")
	r.handleWalletOrBridgePendingInput(ctx, bob, "wm_label", "watch", "Bob label")
	a := r.renderWalletDetailCard(ctx, alice, "watch", "")
	b := r.renderWalletDetailCard(ctx, bob, "watch", "")
	if !strings.Contains(a.Text, "Alice label") || strings.Contains(a.Text, "Bob label") || !strings.Contains(b.Text, "Bob label") || strings.Contains(b.Text, "Alice label") {
		t.Fatal("shared wallet label leaked across users")
	}
}
