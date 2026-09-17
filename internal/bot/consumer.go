package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/feed"
)

var ErrConsumerUnavailable = errors.New("bot consumer unavailable")

type OutboxSource interface {
	ReadOutboxAfter(context.Context, int64, int) ([]feed.OutboxItem, error)
}

type Consumer struct {
	store     *Store
	source    OutboxSource
	batchSize int
	now       func() time.Time
}

func NewConsumer(store *Store, source OutboxSource, batchSize int) (*Consumer, error) {
	if store == nil || source == nil {
		return nil, ErrConsumerUnavailable
	}
	if batchSize <= 0 || batchSize > 1000 {
		batchSize = 100
	}
	return &Consumer{store: store, source: source, batchSize: batchSize, now: time.Now}, nil
}

// RunOnce commits each event before reading the next. A crash after fetch but
// before commit leaves bot_progress unchanged, so restart replays that event.
func (c *Consumer) RunOnce(ctx context.Context) (int, error) {
	if c == nil || c.store == nil || c.source == nil || ctx == nil {
		return 0, ErrConsumerUnavailable
	}
	offset, err := c.store.DurableEventOffset(ctx)
	if err != nil {
		return 0, err
	}
	items, err := c.source.ReadOutboxAfter(ctx, offset, c.batchSize)
	if err != nil {
		return 0, fmt.Errorf("read feed outbox: %w", err)
	}
	processed := 0
	for _, item := range items {
		if _, err := c.store.ProcessFeedEvent(ctx, item, c.now().UTC()); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (c *Consumer) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := c.RunOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
