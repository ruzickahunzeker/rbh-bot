package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/feed"
)

// FeedStreamConfig binds one chain's feed outbox stream to a bot copy-trading strategy lane.
type FeedStreamConfig struct {
	ChainID        uint64
	FeedInstanceID string
	Source         OutboxSource
	PrincipalID    string
	StrategyID     string
	WalletID       string
	FixedAmountRaw *big.Int
	MaxSignalAge   time.Duration
}

// ShadowQuoter fetches a real read-only on-chain quote (via POST /v1/quotes) for Paper/Shadow mode
// without requiring a private key or broadcasting any transaction.
type ShadowQuoter interface {
	QuoteShadowFill(ctx context.Context, chainID uint64, walletID string, token common.Address, side string, amountInRaw *big.Int) (expectedOutRaw *big.Int, spotPriceWei *big.Int, err error)
}

// MultiChainRuntime orchestrates multi-chain feed outbox ingestion, net trigger_id deduplication,
// and either zero-private-key Paper/Shadow live-quote execution or serial wallet trade dispatch.
type MultiChainRuntime struct {
	store     *Store
	streams   []FeedStreamConfig
	shadow    *PaperShadowEngine
	quoter    ShadowQuoter
	submitter TradeSubmitter
	batchSize int
	now       func() time.Time
}

func NewMultiChainRuntime(
	store *Store,
	streams []FeedStreamConfig,
	shadow *PaperShadowEngine,
	quoter ShadowQuoter,
	submitter TradeSubmitter,
	batchSize int,
	now func() time.Time,
) (*MultiChainRuntime, error) {
	if store == nil || len(streams) == 0 {
		return nil, ErrConsumerUnavailable
	}
	if batchSize <= 0 || batchSize > 1000 {
		batchSize = 100
	}
	if now == nil {
		now = time.Now
	}
	return &MultiChainRuntime{
		store:     store,
		streams:   streams,
		shadow:    shadow,
		quoter:    quoter,
		submitter: submitter,
		batchSize: batchSize,
		now:       now,
	}, nil
}

// RunOnce polls every configured chain's feed outbox from its independent durable cursor,
// ingests and deduplicates swap signals by net trigger_id, and dispatches queued jobs
// through Paper/Shadow live-quoting (when shadow mode is enabled) or TradeSubmitter.
func (r *MultiChainRuntime) RunOnce(ctx context.Context) (int, error) {
	if r == nil || r.store == nil || ctx == nil {
		return 0, ErrConsumerUnavailable
	}
	totalProcessed := 0
	now := r.now().UTC()

	for _, stream := range r.streams {
		cursor, err := r.store.LoadFeedCursor(ctx, stream.ChainID, stream.FeedInstanceID)
		if err != nil {
			return totalProcessed, err
		}
		items, err := stream.Source.ReadOutboxAfter(ctx, cursor, r.batchSize)
		if err != nil {
			return totalProcessed, fmt.Errorf("read outbox (%d,%s): %w", stream.ChainID, stream.FeedInstanceID, err)
		}
		for _, item := range items {
			var payload feed.NormalizedSwapPayload
			if err := json.Unmarshal(item.PayloadJSON, &payload); err != nil {
				return totalProcessed, fmt.Errorf("decode swap payload offset=%d: %w", item.Offset, err)
			}
			stage := payload.Stage
			if stage == "" {
				stage = "included"
			}
			sig := MultiChainSignal{
				ChainID:         stream.ChainID,
				FeedInstanceID:  stream.FeedInstanceID,
				FeedOffset:      item.Offset,
				ObservationID:   item.ObservationID,
				TxHash:          payload.TxHash,
				TraderAddress:   payload.Trader,
				TokenAddress:    payload.Token,
				Side:            payload.Side,
				Stage:           stage,
				FirstObservedAt: now,
			}
			if _, err := r.store.IngestMultiChainSignal(
				ctx,
				stream.PrincipalID,
				stream.StrategyID,
				stream.WalletID,
				sig,
				stream.MaxSignalAge,
				now,
			); err != nil {
				return totalProcessed, err
			}
			totalProcessed++
		}

		// Dispatch next pending job for this stream's (chain_id, wallet_id) lane.
		if r.shadow != nil && r.shadow.Enabled() && r.quoter != nil {
			if err := r.dispatchShadowLane(ctx, stream, now); err != nil {
				return totalProcessed, err
			}
		} else if r.submitter != nil {
			if _, _, err := r.store.DispatchNextForWallet(ctx, stream.ChainID, stream.WalletID, r.submitter, now); err != nil {
				return totalProcessed, err
			}
		}
	}
	return totalProcessed, nil
}

func (r *MultiChainRuntime) dispatchShadowLane(ctx context.Context, stream FeedStreamConfig, now time.Time) error {
	adapter := &shadowSubmitterAdapter{
		store:     r.store,
		shadow:    r.shadow,
		quoter:    r.quoter,
		amountRaw: stream.FixedAmountRaw,
		now:       now,
	}
	for {
		dispatched, _, err := r.store.DispatchNextForWallet(ctx, stream.ChainID, stream.WalletID, adapter, now)
		if err != nil {
			return err
		}
		if !dispatched {
			return nil
		}
		// In zero-broadcast shadow mode, complete the job immediately so the serial wallet lane
		// advances to the next queued copy-trade job.
		if err := r.store.MarkJobTerminal(ctx, adapter.lastKey, "COMPLETED", "", now); err != nil {
			return err
		}
	}
}

type shadowSubmitterAdapter struct {
	store     *Store
	shadow    *PaperShadowEngine
	quoter    ShadowQuoter
	amountRaw *big.Int
	now       time.Time
	lastKey   string
}

func (a *shadowSubmitterAdapter) SubmitCopyJob(ctx context.Context, chainID uint64, walletID, idempotencyKey string) (string, bool, error) {
	a.lastKey = idempotencyKey
	var tokenHex, actionKind string
	err := a.store.db.QueryRowContext(ctx,
		`SELECT token, action_kind FROM copy_signal_decisions WHERE trade_idempotency_key = ?`,
		idempotencyKey,
	).Scan(&tokenHex, &actionKind)
	if err != nil {
		return "", false, err
	}
	side := "buy"
	if actionKind == "copy_sell" {
		side = "sell"
	}
	amtIn := a.amountRaw
	if amtIn == nil || amtIn.Sign() <= 0 {
		amtIn = big.NewInt(1_000_000_000_000_000) // default 0.001 native unit
	}
	tokenAddr := common.HexToAddress(tokenHex)
	outRaw, spotWei, err := a.quoter.QuoteShadowFill(ctx, chainID, walletID, tokenAddr, side, amtIn)
	if err != nil {
		return "", true, err
	}
	if outRaw == nil || outRaw.Sign() <= 0 {
		return "", false, errors.New("shadow quote returned zero output")
	}
	if spotWei == nil || spotWei.Sign() <= 0 {
		spotWei = big.NewInt(1)
	}
	fillID := "shadow-" + idempotencyKey
	if err := a.shadow.RecordShadowFill(ctx, fillID, chainID, walletID, tokenAddr, side, amtIn, outRaw, spotWei, idempotencyKey, a.now); err != nil {
		return "", false, err
	}
	return fillID, false, nil
}
