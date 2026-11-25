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
}

// syncAndListAuthorizedWallets reconciles on-disk v1/v2 encrypted keystores and configured
// UDS readonly wallets into bot.db evm_wallets, returning all EVM wallets chatID is authorized
// to view/use across every EVM chain (ordered: keystore wallets first, then readonly wallets).
func (r *Router) syncAndListAuthorizedWallets(ctx context.Context, chatID int64, activeChainID uint64) []GlobalWallet {
	dir := keystoreDirPath()
	nowStr := r.now().UTC().Format(time.RFC3339Nano)

	// 1. Sync on-disk keystores (both legacy v1 and chain-agnostic v2) into evm_wallets.
	ksList, _ := tradecore.ListEncryptedWallets(dir)
	ksByID := make(map[string]tradecore.KeystoreSummary, len(ksList))
	for _, w := range ksList {
		ksByID[w.WalletID] = w
		if r.db != nil {
			_, _ = r.db.ExecContext(ctx,
				`INSERT INTO evm_wallets(wallet_id, address, wallet_type, keystore_path, owner_chat_id, legacy_chain_id, created_at, updated_at)
				 VALUES(?, ?, 'keystore', ?, ?, ?, ?, ?)
				 ON CONFLICT(wallet_id) DO UPDATE SET
				   address = excluded.address,
				   wallet_type = 'keystore',
				   keystore_path = excluded.keystore_path,
				   owner_chat_id = CASE WHEN evm_wallets.owner_chat_id != 0 THEN evm_wallets.owner_chat_id ELSE excluded.owner_chat_id END,
				   legacy_chain_id = CASE WHEN evm_wallets.legacy_chain_id != 0 THEN evm_wallets.legacy_chain_id ELSE excluded.legacy_chain_id END,
				   updated_at = excluded.updated_at`,
				w.WalletID, strings.ToLower(w.Address.Hex()), w.FilePath, w.OwnerChatID, w.ChainID, nowStr, nowStr,
			)
		}
	}

	// 2. Collect authorized wallets from DB + on-disk keystores.
	byID := make(map[string]GlobalWallet)
	seenAddr := make(map[string]string) // lower(address) -> wallet_id
	if r.readDB != nil {
		rows, err := r.readDB.QueryContext(ctx,
			`SELECT wallet_id, address, wallet_type, keystore_path, owner_chat_id, legacy_chain_id
			 FROM evm_wallets
			 WHERE owner_chat_id = 0 OR owner_chat_id = ?
			 ORDER BY CASE WHEN wallet_type = 'keystore' THEN 0 ELSE 1 END, wallet_id ASC`,
			chatID,
		)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var gw GlobalWallet
				var addrHex string
				if rows.Scan(&gw.WalletID, &addrHex, &gw.WalletType, &gw.KeystorePath, &gw.OwnerChatID, &gw.LegacyChainID) == nil {
					gw.Address = common.HexToAddress(addrHex)
					gw.CanSign = gw.WalletType == "keystore"
					// If it's a keystore row, ensure the file still exists on disk.
					if gw.WalletType == "keystore" {
						if _, exists := ksByID[gw.WalletID]; !exists {
							continue
						}
					}
					byID[gw.WalletID] = gw
					seenAddr[strings.ToLower(gw.Address.Hex())] = gw.WalletID
				}
			}
		}
	}
	// Fallback when DB table has not been populated yet (e.g. direct unit tests).
	for _, w := range ksList {
		if w.OwnerChatID != 0 && w.OwnerChatID != chatID {
			continue
		}
		if _, exists := byID[w.WalletID]; !exists {
			byID[w.WalletID] = GlobalWallet{
				WalletID:      w.WalletID,
				Address:       w.Address,
				WalletType:    "keystore",
				KeystorePath:  w.FilePath,
				OwnerChatID:   w.OwnerChatID,
				LegacyChainID: w.ChainID,
				CanSign:       true,
			}
			seenAddr[strings.ToLower(w.Address.Hex())] = w.WalletID
		}
	}

	// 3. Merge UDS readonly wallets from the active chain (and configured chains) without duplicating keystores.
	chainIDs := []uint64{activeChainID}
	if r.backend != nil {
		if uds, ok := r.backend.(interface{ ConfiguredChainIDs() []uint64 }); ok {
			chainIDs = uds.ConfiguredChainIDs()
		}
		for _, cid := range chainIDs {
			udsWallets, err := r.backend.ListWallets(ctx, cid)
			if err != nil {
				continue
			}
			for _, wStr := range udsWallets {
				id, addr := parseListedWalletLine(wStr)
				if id == "" {
					continue
				}
				if _, exists := byID[id]; exists {
					continue
				}
				if addr != (common.Address{}) {
					if _, dupAddr := seenAddr[strings.ToLower(addr.Hex())]; dupAddr {
						continue
					}
					seenAddr[strings.ToLower(addr.Hex())] = id
				}
				byID[id] = GlobalWallet{
					WalletID:      id,
					Address:       addr,
					WalletType:    "readonly",
					LegacyChainID: cid,
					CanSign:       false,
				}
			}
		}
	}

	out := make([]GlobalWallet, 0, len(byID))
	for _, w := range byID {
		out = append(out, w)
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

func (r *Router) registerGlobalWalletRow(ctx context.Context, w GlobalWallet) {
	if r.db == nil || w.WalletID == "" {
		return
	}
	nowStr := r.now().UTC().Format(time.RFC3339Nano)
	_, _ = r.db.ExecContext(ctx,
		`INSERT INTO evm_wallets(wallet_id, address, wallet_type, keystore_path, owner_chat_id, legacy_chain_id, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(wallet_id) DO UPDATE SET
		   address = excluded.address,
		   wallet_type = excluded.wallet_type,
		   keystore_path = excluded.keystore_path,
		   owner_chat_id = CASE WHEN evm_wallets.owner_chat_id != 0 THEN evm_wallets.owner_chat_id ELSE excluded.owner_chat_id END,
		   updated_at = excluded.updated_at`,
		w.WalletID, strings.ToLower(w.Address.Hex()), w.WalletType, w.KeystorePath, w.OwnerChatID, w.LegacyChainID, nowStr, nowStr,
	)
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

// walletDeleteBlockers checks all chains in bot.db for active conditional orders, sniper tasks,
// copy-trade profiles, and in-flight submission jobs referencing walletID.
func (r *Router) walletDeleteBlockers(ctx context.Context, walletID string) []string {
	if r.readDB == nil || walletID == "" {
		return nil
	}
	var blockers []string
	var cnt int

	if err := r.readDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM conditional_orders WHERE wallet_id = ? AND state IN ('ACTIVE','TRIGGERED')`,
		walletID,
	).Scan(&cnt); err == nil && cnt > 0 {
		blockers = append(blockers, fmt.Sprintf("全链活跃挂单/止盈止损 %d 笔", cnt))
	}
	if err := r.readDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sniper_tasks WHERE wallet_id = ? AND state IN ('ARMED','TRIGGERED')`,
		walletID,
	).Scan(&cnt); err == nil && cnt > 0 {
		blockers = append(blockers, fmt.Sprintf("全链活跃狙击任务 %d 个", cnt))
	}
	if err := r.readDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM copy_wallet_profiles WHERE wallet_id = ? AND enabled = 1`,
		walletID,
	).Scan(&cnt); err == nil && cnt > 0 {
		blockers = append(blockers, fmt.Sprintf("全链启用中的跟单策略 %d 个", cnt))
	}
	if err := r.readDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM submission_jobs WHERE wallet_id = ? AND state IN ('PENDING','DISPATCHED','IN_FLIGHT')`,
		walletID,
	).Scan(&cnt); err == nil && cnt > 0 {
		blockers = append(blockers, fmt.Sprintf("全链未完成交易任务 %d 笔", cnt))
	}
	return blockers
}
