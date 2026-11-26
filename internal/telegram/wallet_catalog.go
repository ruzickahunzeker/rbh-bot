package telegram

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	tradecore "github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

// GlobalWallet represents a chain-agnostic EVM wallet visible to an authorized Telegram chat.
type GlobalWallet struct {
	WalletID      string
	Address       common.Address
	WalletType    string // "keystore" | "readonly"
	KeystorePath  string
	OwnerChatID   int64 // 0 = shared operator wallet; >0 = owned by specific chat_id
	LegacyChainID uint64
	CanSign       bool
	Label         string
}

// syncAndListAuthorizedWallets has no filesystem authorization fallback: DB failure
// denies access, and reconciliation never resurrects a DELETING/DELETED identity.
func (r *Router) syncAndListAuthorizedWallets(ctx context.Context, chatID int64, activeChainID uint64) []GlobalWallet {
	if r.db == nil || r.readDB == nil {
		return nil
	}
	if err := r.recoverWalletDeletions(ctx); err != nil {
		return nil
	}
	ksList, err := tradecore.ListEncryptedWallets(keystoreDirPath())
	if err != nil {
		return nil
	}
	ksByID := map[string]tradecore.KeystoreSummary{}
	addresses := map[common.Address]bool{}
	for _, w := range ksList {
		ksByID[w.WalletID] = w
		addresses[w.Address] = true
		if err := r.registerGlobalWalletRow(ctx, GlobalWallet{WalletID: w.WalletID, Address: w.Address, WalletType: "keystore", KeystorePath: w.FilePath, OwnerChatID: w.OwnerChatID, LegacyChainID: w.ChainID}); err != nil {
			return nil
		}
	}
	chainIDs := []uint64{activeChainID}
	if r.backend != nil {
		if configured, ok := r.backend.(interface{ ConfiguredChainIDs() []uint64 }); ok {
			chainIDs = configured.ConfiguredChainIDs()
		}
		for _, cid := range chainIDs {
			wallets, err := r.backend.ListWallets(ctx, cid)
			if err != nil {
				continue
			}
			for _, line := range wallets {
				id, addr := parseListedWalletLine(line)
				if !tradecore.ValidWalletID(id) {
					continue
				}
				if _, exists := ksByID[id]; exists {
					continue
				}
				if addresses[addr] {
					continue
				}
				if addr == (common.Address{}) {
					continue
				}
				if err = r.registerGlobalWalletRow(ctx, GlobalWallet{WalletID: id, Address: addr, WalletType: "readonly", LegacyChainID: cid}); err != nil {
					return nil
				}
				addresses[addr] = true
			}
		}
	}
	rows, err := r.readDB.QueryContext(ctx, `SELECT w.wallet_id,w.address,w.wallet_type,w.keystore_path,w.owner_chat_id,w.legacy_chain_id,COALESCE(l.label,'')
 FROM evm_wallets w LEFT JOIN evm_wallet_labels l ON l.wallet_id=w.wallet_id AND l.chat_id=?
 WHERE w.state='ACTIVE' AND (w.owner_chat_id=? OR (w.owner_chat_id=0 AND (w.wallet_type='readonly' OR ?)))`, chatID, chatID, sharedKeystoreAllowed(chatID))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []GlobalWallet
	for rows.Next() {
		var w GlobalWallet
		var addr string
		if err = rows.Scan(&w.WalletID, &addr, &w.WalletType, &w.KeystorePath, &w.OwnerChatID, &w.LegacyChainID, &w.Label); err != nil {
			return nil
		}
		w.Address = common.HexToAddress(addr)
		if tradecore.CheckWalletAvailable(keystoreDirPath(), w.WalletID) != nil {
			continue
		}
		if w.WalletType == "keystore" {
			summary, exists := ksByID[w.WalletID]
			if !exists || summary.Address != w.Address || summary.OwnerChatID != w.OwnerChatID {
				continue
			}
			w.CanSign = true
		}
		out = append(out, w)
	}
	if rows.Err() != nil {
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WalletType != out[j].WalletType {
			return out[i].WalletType == "keystore"
		}
		return out[i].WalletID < out[j].WalletID
	})
	return out
}

func parseListedWalletLine(line string) (string, common.Address) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return "", common.Address{}
	}
	id := fields[0]
	var addr common.Address
	for _, f := range fields[1:] {
		clean := strings.Trim(f, "()[],")
		if common.IsHexAddress(clean) {
			addr = common.HexToAddress(clean)
			break
		}
	}
	return id, addr
}

func (r *Router) resolveAuthorizedWallet(ctx context.Context, chatID int64, activeChainID uint64, walletID string) (GlobalWallet, bool) {
	for _, w := range r.syncAndListAuthorizedWallets(ctx, chatID, activeChainID) {
		if w.WalletID == walletID {
			return w, true
		}
	}
	return GlobalWallet{}, false
}

func (r *Router) countAuthorizedKeystoreWallets(ctx context.Context, chatID int64, activeChainID uint64) int {
	n := 0
	for _, w := range r.syncAndListAuthorizedWallets(ctx, chatID, activeChainID) {
		if w.WalletType == "keystore" {
			n++
		}
	}
	return n
}

func (r *Router) registerGlobalWalletRow(ctx context.Context, w GlobalWallet) error {
	if r.db == nil || !tradecore.ValidWalletID(w.WalletID) {
		return fmt.Errorf("wallet catalog unavailable")
	}
	now := r.now().UTC().Format(time.RFC3339Nano)
	result, err := r.db.ExecContext(ctx, `INSERT INTO evm_wallets(wallet_id,address,wallet_type,keystore_path,owner_chat_id,legacy_chain_id,created_at,updated_at)
 VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(wallet_id) DO UPDATE SET updated_at=excluded.updated_at
 WHERE evm_wallets.state='ACTIVE' AND evm_wallets.address=excluded.address AND evm_wallets.owner_chat_id=excluded.owner_chat_id
 AND evm_wallets.wallet_type=excluded.wallet_type AND evm_wallets.keystore_path=excluded.keystore_path`,
		w.WalletID, strings.ToLower(w.Address.Hex()), w.WalletType, w.KeystorePath, w.OwnerChatID, w.LegacyChainID, now, now)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("wallet catalog identity conflict or wallet unavailable")
	}
	return nil
}

func (r *Router) setActiveWalletID(ctx context.Context, chatID int64, walletID string) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE telegram_allowlist SET active_wallet_id = ?, updated_at = ? WHERE chat_id = ?`,
		walletID, r.now().UTC().Format(time.RFC3339Nano), chatID,
	)
	return err
}
