package blockrazor

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

// MultiClient races regional feeds and deduplicates by transaction hash. Its
// handler can be called concurrently for distinct transactions.
type MultiClient struct {
	clients        []*Client
	dedupeCapacity int
}

func NewMulti(configs ...Config) (*MultiClient, error) {
	if len(configs) == 0 {
		return nil, ErrInvalidConfig
	}
	clients := make([]*Client, len(configs))
	capacity := 0
	for i, config := range configs {
		client, err := New(config)
		if err != nil {
			return nil, err
		}
		clients[i] = client
		if client.config.DedupeCapacity > capacity {
			capacity = client.config.DedupeCapacity
		}
	}
	return &MultiClient{clients: clients, dedupeCapacity: capacity}, nil
}

func (m *MultiClient) Run(ctx context.Context, handler Handler) error {
	if ctx == nil || m == nil || len(m.clients) == 0 || handler == nil {
		return ErrInvalidConfig
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	dedupe := lockedDeduper{dedupe: newDeduper(m.dedupeCapacity)}
	wrapped := func(ctx context.Context, transaction FeedTransaction) error {
		if !dedupe.add(transaction.Transaction.Hash()) {
			return nil
		}
		if err := handler(ctx, transaction); err != nil {
			return handlerError{err: err}
		}
		return nil
	}
	errs := make(chan error, len(m.clients))
	for _, client := range m.clients {
		go func() { errs <- client.Run(ctx, wrapped) }()
	}
	failures := make([]error, 0, len(m.clients))
	for range m.clients {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			var callback handlerError
			if errors.As(err, &callback) {
				cancel()
				return callback.err
			}
			failures = append(failures, err)
		}
	}
	return fmt.Errorf("all direct-feed regions stopped: %w", errors.Join(failures...))
}

type handlerError struct{ err error }

func (e handlerError) Error() string { return e.err.Error() }
func (e handlerError) Unwrap() error { return e.err }

type lockedDeduper struct {
	mu     sync.Mutex
	dedupe *deduper
}

func (d *lockedDeduper) add(hash common.Hash) bool {
	d.mu.Lock()
	added := d.dedupe.add(hash)
	d.mu.Unlock()
	return added
}
