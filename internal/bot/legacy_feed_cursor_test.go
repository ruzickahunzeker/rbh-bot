package bot

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/feed"
	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestLegacyFeedAdoptionVerifiesSourceAndPreservesRows(t *testing.T) {
	ctx := context.Background()
	store, closeDB := testStore(t)
	defer closeDB()
	source := &sliceOutboxSource{}
	for i := int64(1); i <= 3; i++ {
		item := feed.OutboxItem{Offset: i, ObservationID: common.BigToHash(big.NewInt(i)).Hex(), TransactionHash: common.HexToHash("0x1234"), PayloadJSON: json.RawMessage(`{"chain_id":56}`)}
		source.items = append(source.items, item)
		if _, err := store.ProcessFeedEvent(ctx, item, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	bad := &sliceOutboxSource{items: append([]feed.OutboxItem(nil), source.items...)}
	bad.items[1].ObservationID = "wrong"
	if err := store.AdoptLegacyFeedCursor(ctx, 56, "feed-bsc", bad); err == nil {
		t.Fatal("wrong source adopted")
	}
	if cp, _ := store.LoadFeedCursor(ctx, 56, "feed-bsc"); cp != 0 {
		t.Fatal("failed adoption committed")
	}
	if err := store.AdoptLegacyFeedCursor(ctx, 56, "feed-bsc", source); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptLegacyFeedCursor(ctx, 56, "feed-bsc", source); err != nil {
		t.Fatal("adoption not idempotent", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM signal_inbox WHERE chain_id=56 AND feed_instance_id='feed-bsc'`).Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	cp, err := store.LoadFeedCursor(ctx, 56, "feed-bsc")
	if err != nil || cp != 3 {
		t.Fatal(cp, err)
	}
}

// Explicit offline DB copy + authenticated read-only live feed sources.
func TestLegacyFeedProductionCopy(t *testing.T) {
	path := os.Getenv("LEGACY_FEED_COPY_DB")
	if path == "" {
		t.Skip("explicit offline bot DB required")
	}
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.BotOwner, path)
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
	auth, err := ipc.NewAuthenticator([]byte(os.Getenv("LEGACY_FEED_AUTH")), 30*time.Second)
	if err != nil {
		t.Fatal("invalid private test auth")
	}
	bsc, err := NewHTTPOutboxSource(os.Getenv("LEGACY_FEED_BSC_SOCKET"), auth)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptLegacyFeedCursor(ctx, 56, "feed-bsc", bsc); err != nil {
		t.Fatal(err)
	}
	cp, err := store.LoadFeedCursor(ctx, 56, "feed-bsc")
	if err != nil || cp == 0 {
		t.Fatal(cp, err)
	}
	rbh, err := NewHTTPOutboxSource(os.Getenv("LEGACY_FEED_RBH_SOCKET"), auth)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewMultiChainRuntime(store, []FeedStreamConfig{{ChainID: 4663, FeedInstanceID: "feed-rbh", Source: rbh, PrincipalID: "p", StrategyID: "s", WalletID: "w"}}, nil, nil, nil, 1000, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	count, err := runtime.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := store.LoadFeedCursor(ctx, 4663, "feed-rbh")
	if err != nil || cursor == 0 {
		t.Fatal(cursor, err)
	}
	t.Logf("verified legacy BSC offset=%d; consumed RBH observations=%d cursor=%d", cp, count, cursor)
}
