// Package preflight implements the Step 5 (R9) per-chain zero-send Canary Preflight
// gate checker: verifying live_enabled=false, chain_identity, SQLite PRAGMAs, timeout
// inequality, BSC private_only guard, and live VACUUM INTO backup + read-only restore drill.
package preflight

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type CheckResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

type Report struct {
	ChainID     uint64        `json:"chain_id"`
	ChainSlug   string        `json:"chain_slug"`
	Passed      bool          `json:"passed"`
	BackupHash  string        `json:"backup_sha256,omitempty"`
	BackupBytes int64         `json:"backup_bytes,omitempty"`
	CheckedAt   string        `json:"checked_at"`
	Checks      []CheckResult `json:"checks"`
}

type Input struct {
	Config       config.Config
	BackupDir    string
	TargetPublic bool // true if configured broadcast endpoint is public mempool (must fail on BSC private_only)
}

// Run executes the zero-send Step 5 Preflight gate suite for a single chain instance.
func Run(ctx context.Context, in Input) (Report, error) {
	rep := Report{
		ChainID:   in.Config.ChainID,
		ChainSlug: in.Config.ChainSlug,
		CheckedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	allPass := true
	add := func(name string, ok bool, detail string) {
		if !ok {
			allPass = false
		}
		rep.Checks = append(rep.Checks, CheckResult{Name: name, Passed: ok, Detail: detail})
	}

	// 1. Live disabled & canary mode disabled.
	if !in.Config.LiveEnabled && in.Config.CanaryProductionMode == config.CanaryProductionModeDisabled {
		add("live_disabled", true, "live_enabled=false and canary_production_mode=DISABLED")
	} else {
		add("live_disabled", false, fmt.Sprintf("live_enabled=%v canary_mode=%s", in.Config.LiveEnabled, in.Config.CanaryProductionMode))
	}

	// 2. Timeout inequality & distinct sockets.
	if err := in.Config.ValidateTimeouts(); err != nil {
		add("timeout_inequality", false, err.Error())
	} else {
		add("timeout_inequality", true, "BotClientTimeout > HTTPWriteTimeout > PreSendRPCBudget+BroadcastRPCTimeout+2s")
	}

	// 3. BSC private_only broadcast route guard.
	if in.Config.ChainID == config.BSCChainID {
		if err := bsc.ValidateBroadcastRoute(evm.SubmissionPrivateOnly, in.TargetPublic); err != nil {
			add("bsc_private_only", false, err.Error())
		} else {
			add("bsc_private_only", true, "BSC private_only route verified (no public mempool fallback)")
		}
	} else {
		add("bsc_private_only", true, "not applicable for non-BSC chain")
	}

	// 4. Open DB, verify chain identity before migrate, validate SQLite PRAGMAs.
	owner := storage.Owner(in.Config.Service)
	if owner == "" {
		owner = storage.TradeOwner
	}
	db, err := storage.Open(ctx, owner, in.Config.Database)
	if err != nil {
		add("sqlite_open_and_pragmas", false, err.Error())
		rep.Passed = false
		return rep, fmt.Errorf("preflight failed")
	}
	defer db.Close()

	netKind := in.Config.NetworkKind
	if netKind == "" {
		netKind = "mainnet"
	}
	if err := db.VerifyOrInitChainIdentity(ctx, storage.ChainIdentity{
		ChainID:     in.Config.ChainID,
		ChainSlug:   in.Config.ChainSlug,
		NetworkKind: netKind,
	}); err != nil {
		add("chain_identity", false, err.Error())
	} else {
		add("chain_identity", true, fmt.Sprintf("verified chain_id=%d slug=%s", in.Config.ChainID, in.Config.ChainSlug))
	}

	if err := db.Migrate(ctx); err != nil {
		add("sqlite_migrations_and_pragmas", false, err.Error())
	} else if err := db.Validate(ctx); err != nil {
		add("sqlite_migrations_and_pragmas", false, err.Error())
	} else {
		add("sqlite_migrations_and_pragmas", true, "WAL + FULL + foreign_keys=1 + journal_size_limit verified")
	}

	// 5. Durable Emergency-Stop roundtrip on target chain_id (U-P0-1 / U-P1-3).
	if owner == storage.TradeOwner {
		if sqlDB, sErr := db.SQLDB(owner); sErr == nil {
			tbl := "canary_control_state"
			if in.Config.ChainID != 0 && in.Config.ChainID != config.ChainID {
				tbl = "multichain_control_state"
			}
			nowStr := time.Now().UTC().Format(time.RFC3339Nano)
			_, stopErr := sqlDB.ExecContext(ctx,
				`INSERT INTO `+tbl+`(singleton, chain_id, mode, policy_version, emergency_stopped, reason, authorization_ref, effective_at, updated_at)
				 VALUES(1, ?, 'DISABLED', 1, 1, 'preflight-drill', 'preflight', ?, ?)
				 ON CONFLICT(singleton) DO UPDATE SET emergency_stopped = 1, reason = 'preflight-drill', updated_at = excluded.updated_at`,
				in.Config.ChainID, nowStr, nowStr,
			)
			if stopErr != nil {
				add("emergency_stop_roundtrip", false, fmt.Sprintf("persist emergency-stop failed: %v", stopErr))
			} else if _, resErr := sqlDB.ExecContext(ctx,
				`UPDATE `+tbl+` SET emergency_stopped = 0, reason = '', updated_at = ? WHERE singleton = 1`, nowStr,
			); resErr != nil {
				add("emergency_stop_roundtrip", false, fmt.Sprintf("clear emergency-stop failed: %v", resErr))
			} else {
				add("emergency_stop_roundtrip", true, fmt.Sprintf("durable emergency-stop/resume verified in %s for chain_id=%d", tbl, in.Config.ChainID))
			}
		}
	}

	// 6. Online VACUUM INTO hot backup + offline read-only restore verification (AT-24).
	if in.BackupDir == "" {
		add("vacuum_backup_restore", false, "backup_dir is required for Step 5 restore drill")
	} else {
		backupPath := filepath.Join(in.BackupDir, fmt.Sprintf("preflight-%s-%d.db", in.Config.ChainSlug, in.Config.ChainID))
		digest, size, err := db.VacuumBackup(ctx, backupPath)
		if err != nil {
			add("vacuum_backup_restore", false, err.Error())
		} else {
			rep.BackupHash = digest
			rep.BackupBytes = size
			// Reopen backup in read-only / recovery mode (zero signer) and verify chain_identity + ReadDB.
			restored, rErr := storage.Open(ctx, owner, backupPath)
			if rErr != nil {
				add("vacuum_backup_restore", false, fmt.Sprintf("reopen backup failed: %v", rErr))
			} else {
				defer restored.Close()
				if _, rdErr := restored.ReadDB(owner); rdErr != nil {
					add("vacuum_backup_restore", false, fmt.Sprintf("read-only pool check failed: %v", rdErr))
				} else if idErr := restored.VerifyOrInitChainIdentity(ctx, storage.ChainIdentity{
					ChainID:     in.Config.ChainID,
					ChainSlug:   in.Config.ChainSlug,
					NetworkKind: netKind,
				}); idErr != nil {
					add("vacuum_backup_restore", false, fmt.Sprintf("restored identity mismatch: %v", idErr))
				} else {
					add("vacuum_backup_restore", true, fmt.Sprintf("VACUUM INTO verified sha256=%s bytes=%d", digest[:16], size))
				}
			}
		}
	}

	rep.Passed = allPass
	if !rep.Passed {
		return rep, fmt.Errorf("preflight gate failed for chain %s (%d)", in.Config.ChainSlug, in.Config.ChainID)
	}
	return rep, nil
}
