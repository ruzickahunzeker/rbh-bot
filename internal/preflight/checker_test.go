package preflight

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/config"
)

func TestStep5PreflightBSCPassesAndRejectsPublicFallbackOrWrongChain(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trade-bsc.db")

	baseCfg := config.Config{
		Service:              config.TradeService,
		Database:             dbPath,
		Socket:               filepath.Join(dir, "trade-bsc.sock"),
		AdminSocket:          filepath.Join(dir, "trade-bsc-admin.sock"),
		ChainID:              config.BSCChainID,
		ChainSlug:            "bsc",
		NetworkKind:          "mainnet",
		LiveEnabled:          false,
		CanaryProductionMode: config.CanaryProductionModeDisabled,
		PreSendRPCBudget:     3 * time.Second,
		BroadcastRPCTimeout:  5 * time.Second,
		HTTPWriteTimeout:     15 * time.Second,
		BotClientTimeout:     25 * time.Second,
	}

	// 1. Clean BSC private_only config passes all Step 5 checks + VACUUM INTO restore drill.
	rep, err := Run(ctx, Input{Config: baseCfg, BackupDir: t.TempDir(), TargetPublic: false})
	if err != nil || !rep.Passed || rep.BackupHash == "" || rep.BackupBytes <= 0 {
		t.Fatalf("expected clean BSC preflight to pass: rep=%+v err=%v", rep, err)
	}

	// 2. BSC with public mempool target must fail closed.
	if repPub, err := Run(ctx, Input{Config: baseCfg, BackupDir: t.TempDir(), TargetPublic: true}); err == nil || repPub.Passed {
		t.Fatalf("expected BSC public mempool fallback to fail preflight: %+v", repPub)
	}

	// 3. Reusing BSC database with Robinhood chain_id=4663 must fail closed on chain_identity.
	wrongChainCfg := baseCfg
	wrongChainCfg.ChainID = config.ChainID
	wrongChainCfg.ChainSlug = "rbh"
	if repWrong, err := Run(ctx, Input{Config: wrongChainCfg, BackupDir: t.TempDir(), TargetPublic: false}); err == nil || repWrong.Passed {
		t.Fatalf("expected cross-chain DB reuse to fail preflight: %+v", repWrong)
	}
}
