package bot

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/feed"
)

func TestRPCBackfillEventTimeDoesNotTriggerOldCopyOrSniper(t *testing.T) {
	store, closeDB := testStore(t)
	defer closeDB()
	now := time.Now().UTC()
	source := &sliceOutboxSource{}
	for i, side := range []string{"buy", "launch", "migration"} {
		data, _ := json.Marshal(feed.NormalizedSwapPayload{ChainID: 4663, BlockNumber: 10, BlockHash: common.HexToHash("0xabc").Hex(), TxHash: common.HexToHash("0x1234").Hex(), Trader: common.HexToAddress("0xcafe").Hex(), Token: common.HexToAddress("0xbeef").Hex(), PoolVersion: "pons-v2-curve", Side: side, Stage: "included", TokenDeltaRaw: "10", EventTime: now.Add(-time.Hour)})
		source.items = append(source.items, feed.OutboxItem{Offset: int64(i + 1), ObservationID: side, Source: feed.SourceRPC, PayloadJSON: data})
	}
	rt, err := NewMultiChainRuntime(store, []FeedStreamConfig{{ChainID: 4663, FeedInstanceID: "feed-rbh", Source: source, PrincipalID: "p", StrategyID: "s", WalletID: "w", MaxSignalAge: time.Minute, FixedAmountRaw: big.NewInt(1)}}, nil, nil, nil, 100, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewSniperEngine(store, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []SniperTriggerKind{SniperLaunch, SniperMigration} {
		if _, err := engine.ArmTask(context.Background(), SniperTask{TaskID: string(kind), ChainID: 4663, WalletID: "w", Token: common.HexToAddress("0xbeef"), TriggerKind: kind, AmountInWei: big.NewInt(1)}, now); err != nil {
			t.Fatal(err)
		}
	}
	rt.SetSniperEngine(engine)
	n, err := rt.RunOnce(context.Background())
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	var jobs int
	if err := store.db.QueryRow(`SELECT count(*) FROM submission_jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("old signals created jobs: %d %v", jobs, err)
	}
	var disposition string
	if err := store.db.QueryRow(`SELECT disposition FROM copy_signal_decisions`).Scan(&disposition); err != nil || disposition != "EXPIRED_SKIPPED" {
		t.Fatal(disposition, err)
	}
	cursor, err := store.LoadFeedCursor(context.Background(), 4663, "feed-rbh")
	if err != nil || cursor != 3 {
		t.Fatal(cursor, err)
	}
	var armed int
	if err := store.db.QueryRow(`SELECT count(*) FROM sniper_tasks WHERE state='ARMED'`).Scan(&armed); err != nil || armed != 2 {
		t.Fatal("old launch consumed sniper task", armed, err)
	}
}
