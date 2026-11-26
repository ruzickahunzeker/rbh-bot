package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tradecore "github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

func sharedKeystoreAllowed(chatID int64) bool {
	for _, value := range strings.Split(os.Getenv("EVM_SHARED_KEYSTORE_CHAT_IDS"), ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err == nil && id != 0 && id == chatID {
			return true
		}
	}
	return false
}

type walletBlockerReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func walletBlockers(ctx context.Context, reader walletBlockerReader, walletID string) ([]string, error) {
	if reader == nil || walletID == "" {
		return nil, errors.New("wallet deletion checks unavailable")
	}
	queries := []struct{ sql, label string }{
		{`SELECT COUNT(*) FROM conditional_orders WHERE wallet_id=? AND state NOT IN ('EXECUTED','CANCELLED','EXPIRED')`, "全链活跃挂单/止盈止损"},
		{`SELECT COUNT(*) FROM sniper_tasks WHERE wallet_id=? AND state NOT IN ('EXECUTED','CANCELLED')`, "全链活跃狙击任务"},
		{`SELECT COUNT(*) FROM copy_wallet_profiles WHERE wallet_id=? AND enabled=1`, "全链启用中的跟单策略"},
		{`SELECT COUNT(*) FROM submission_jobs WHERE wallet_id=? AND state NOT IN ('COMPLETED','EXPIRED_SKIPPED','FAILED_TERMINAL')`, "全链未完成交易任务"},
	}
	var blockers []string
	for _, q := range queries {
		var count int
		if err := reader.QueryRowContext(ctx, q.sql, walletID).Scan(&count); err != nil {
			return nil, fmt.Errorf("wallet deletion check failed: %w", err)
		}
		if count > 0 {
			blockers = append(blockers, fmt.Sprintf("%s %d 项", q.label, count))
		}
	}
	return blockers, nil
}
func (r *Router) walletDeleteBlockers(ctx context.Context, walletID string) ([]string, error) {
	if r.readDB == nil {
		return nil, errors.New("wallet deletion database unavailable")
	}
	return walletBlockers(ctx, r.readDB, walletID)
}

func (r *Router) deleteGlobalWallet(ctx context.Context, chatID int64, walletID string) error {
	if r.db == nil {
		return errors.New("wallet catalog unavailable")
	}
	dir := keystoreDirPath()
	return tradecore.WithWalletDirectoryLock(dir, func() error {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		now := r.now().UTC().Format(time.RFC3339Nano)
		result, err := tx.ExecContext(ctx, `UPDATE evm_wallets SET state='DELETING',updated_at=? WHERE wallet_id=? AND state='ACTIVE' AND wallet_type='keystore'
   AND (owner_chat_id=? OR (owner_chat_id=0 AND ?))`, now, walletID, chatID, sharedKeystoreAllowed(chatID))
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("wallet unavailable or unauthorized")
		}
		blockers, err := walletBlockers(ctx, tx, walletID)
		if err != nil {
			return err
		}
		if len(blockers) > 0 {
			return fmt.Errorf("禁止删除：%s", strings.Join(blockers, "；"))
		}
		// The marker precedes DB commit; a failed commit leaves a conservative marker
		// which restart reconciliation can remove after proving the DB row is still ACTIVE.
		if err = tradecore.MarkWalletDeletingLocked(dir, walletID); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return r.finishWalletDeletionLocked(ctx, walletID)
	})
}
func (r *Router) finishWalletDeletionLocked(ctx context.Context, walletID string) error {
	if err := tradecore.MarkWalletDeletingLocked(keystoreDirPath(), walletID); err != nil {
		return err
	}
	if err := tradecore.RemoveWalletFilesLocked(keystoreDirPath(), walletID); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := r.now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE evm_wallets SET state='DELETED',updated_at=? WHERE wallet_id=? AND state='DELETING'`, now, walletID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE telegram_allowlist SET active_wallet_id='',updated_at=? WHERE active_wallet_id=?`, now, walletID); err != nil {
		return err
	}
	return tx.Commit()
}

// Recover before exposing the catalog. A committed DELETING row has already passed
// all blocker checks; DB triggers prevent new work while deletion is retried.
func (r *Router) recoverWalletDeletions(ctx context.Context) error {
	if r.db == nil {
		return errors.New("wallet catalog unavailable")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT wallet_id FROM evm_wallets WHERE state='DELETING'`)
	if err != nil {
		return err
	}
	var pending []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	markers, err := filepath.Glob(filepath.Join(keystoreDirPath(), ".deleted-*"))
	if err != nil {
		return err
	}
	if len(pending) == 0 && len(markers) == 0 {
		return nil
	}
	return tradecore.WithWalletDirectoryLock(keystoreDirPath(), func() error {
		for _, id := range pending {
			var state string
			if err := r.db.QueryRowContext(ctx, `SELECT state FROM evm_wallets WHERE wallet_id=?`, id).Scan(&state); err != nil {
				return err
			}
			if state == "DELETING" {
				if err := r.finishWalletDeletionLocked(ctx, id); err != nil {
					return err
				}
			}
		}
		for _, p := range markers {
			id := strings.TrimPrefix(filepath.Base(p), ".deleted-")
			if !tradecore.ValidWalletID(id) {
				return errors.New("invalid wallet deletion marker")
			}
			var state string
			if err := r.db.QueryRowContext(ctx, `SELECT state FROM evm_wallets WHERE wallet_id=?`, id).Scan(&state); err != nil {
				return err
			}
			if state == "ACTIVE" {
				if err := tradecore.ClearWalletDeletingLocked(keystoreDirPath(), id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (r *Router) defaultActiveWallet(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	if !tradecore.ValidWalletID(id) {
		return "", errors.New("invalid default wallet ID")
	}
	var state string
	err := r.readDB.QueryRowContext(ctx, `SELECT state FROM evm_wallets WHERE wallet_id=?`, id).Scan(&state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil && state != "ACTIVE" {
		return "", nil
	}
	if err = tradecore.CheckWalletAvailable(keystoreDirPath(), id); errors.Is(err, tradecore.ErrWalletDeleting) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return id, nil
}
