package bot

import (
	"context"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestSniperEngineArmTriggerOnceAndShadowFill(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.BotOwner, filepath.Join(t.TempDir(), "bot-sniper.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	shadow, err := NewPaperShadowEngine(store, true)
	if err != nil {
		t.Fatal(err)
	}
	quoter := &stubTickQuoter{spotWei: big.NewInt(100), sellOut: big.NewInt(1e17)}
	var triggered []SniperTask
	eng, err := NewSniperEngine(store, shadow, quoter, func(task SniperTask, _ *big.Int) {
		triggered = append(triggered, task)
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 25, 0, 35, 0, 0, time.UTC)
	token := common.HexToAddress("0x0000000000000000000000000000000000004444")
	if _, err := eng.ArmTask(ctx, SniperTask{
		TaskID:         "snipe-mig-1",
		ChainID:        56,
		WalletID:       "wallet-bsc-readonly",
		Token:          token,
		TriggerKind:    SniperMigration,
		AmountInWei:    big.NewInt(5e16),
		MaxSlippageBps: 1500,
		AntiMEV:        true,
		HoneypotCheck:  true,
	}, now); err != nil {
		t.Fatalf("ArmTask: %v", err)
	}

	// Non-matching trigger kind ("launch") must not fire "migration" task.
	m, err := eng.MatchAndTrigger(ctx, 56, token, SniperLaunch, now.Add(time.Second))
	if err != nil || len(m) != 0 {
		t.Fatalf("expected 0 launch matches, got %d err=%v", len(m), err)
	}

	// Matching "migration" event fires exactly once and executes Shadow fill.
	m, err = eng.MatchAndTrigger(ctx, 56, token, SniperMigration, now.Add(2*time.Second))
	if err != nil || len(m) != 1 || m[0].State != "EXECUTED" || len(triggered) != 1 {
		t.Fatalf("expected 1 EXECUTED migration match, got %+v triggered=%d err=%v", m, len(triggered), err)
	}

	// Second identical event must NOT re-trigger.
	m, err = eng.MatchAndTrigger(ctx, 56, token, SniperMigration, now.Add(3*time.Second))
	if err != nil || len(m) != 0 {
		t.Fatalf("expected 0 duplicate triggers, got %d err=%v", len(m), err)
	}
}
