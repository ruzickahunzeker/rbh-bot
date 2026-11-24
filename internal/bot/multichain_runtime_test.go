package bot

import (
	"context"
	"encoding/json"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/feed"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type sliceOutboxSource struct {
	items []feed.OutboxItem
}

func (s *sliceOutboxSource) ReadOutboxAfter(_ context.Context, after int64, _ int) ([]feed.OutboxItem, error) {
	var out []feed.OutboxItem
	for _, it := range s.items {
		if it.Offset > after {
			out = append(out, it)
		}
	}
	return out, nil
}

type stubShadowQuoter struct {
	calls int
}

func (q *stubShadowQuoter) QuoteShadowFill(_ context.Context, _ uint64, _ string, _ common.Address, _ string, _ *big.Int) (*big.Int, *big.Int, error) {
	q.calls++
	return big.NewInt(250_000), big.NewInt(4_000_000_000), nil
}

func TestMultiChainRuntimeShadowCopyAndV2V3SplitDedupe(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.BotOwner, filepath.Join(t.TempDir(), "bot.db"))
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

	trader := "0x000000000000000000000000000000000000cafe"
	token := common.HexToAddress("0x000000000000000000000000000000000000beef")
	p1, _ := json.Marshal(feed.NormalizedSwapPayload{
		ChainID: 56, BlockNumber: 10000, TxHash: "0xaaa1", PoolVersion: "v2",
		Trader: trader, Token: token.Hex(), Side: "buy", TokenDeltaRaw: "950000",
		TriggerID: feed.ComputeTriggerID(56, "0xaaa1", trader, token.Hex(), "buy"), Stage: "included",
	})
	p2, _ := json.Marshal(feed.NormalizedSwapPayload{
		ChainID: 56, BlockNumber: 10000, TxHash: "0xaaa1", PoolVersion: "v3",
		Trader: trader, Token: token.Hex(), Side: "buy", TokenDeltaRaw: "950000",
		TriggerID: feed.ComputeTriggerID(56, "0xaaa1", trader, token.Hex(), "buy"), Stage: "included",
	})

	src := &sliceOutboxSource{
		items: []feed.OutboxItem{
			{Offset: 1, ObservationID: "obs-1", PayloadJSON: p1},
			{Offset: 2, ObservationID: "obs-2", PayloadJSON: p2},
		},
	}
	quoter := &stubShadowQuoter{}
	now := time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC)

	rt, err := NewMultiChainRuntime(store, []FeedStreamConfig{
		{
			ChainID:        56,
			FeedInstanceID: "feed-bsc",
			Source:         src,
			PrincipalID:    "p1",
			StrategyID:     "strat-bsc-1",
			WalletID:       "w-bsc-1",
			FixedAmountRaw: big.NewInt(1_000_000_000_000_000),
			MaxSignalAge:   15 * time.Second,
		},
	}, shadow, quoter, nil, 100, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	processed, err := rt.RunOnce(ctx)
	if err != nil || processed != 2 {
		t.Fatalf("RunOnce failed: processed=%d err=%v", processed, err)
	}
	// Even though 2 outbox items arrived (V2 + V3 split of tx 0xaaa1), trigger_id dedupe
	// must execute ONLY 1 shadow quote and record ONLY 1 shadow fill!
	if quoter.calls != 1 {
		t.Fatalf("expected exactly 1 shadow quote call after V2+V3 split dedupe, got %d", quoter.calls)
	}
	netTok, netNat, err := shadow.ShadowPosition(ctx, 56, "w-bsc-1", token)
	if err != nil || netTok.Cmp(big.NewInt(250_000)) != 0 || netNat.Cmp(big.NewInt(-1_000_000_000_000_000)) != 0 {
		t.Fatalf("unexpected shadow position after read-only live copy: tok=%s nat=%s err=%v", netTok, netNat, err)
	}
}
