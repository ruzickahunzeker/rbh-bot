package feed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

var ErrReceiptProcessorUnavailable = errors.New("receipt processor unavailable")

type ReceiptProcessor struct {
	gate  *ReceiptGate
	store *Store
}

func NewReceiptProcessor(value *parser.Parser, store *Store) (*ReceiptProcessor, error) {
	if store == nil {
		return nil, ErrReceiptProcessorUnavailable
	}
	return &ReceiptProcessor{
		gate:  NewReceiptGateWithAuditSink(value, store),
		store: store,
	}, nil
}

// Process persists the receipt audit before parser invocation, then atomically
// commits every normalized economic event with its outbox row. Confirmed
// receipts are RPC provenance in PR-002; execution is deliberately out of scope.
func (p *ReceiptProcessor) Process(ctx context.Context, receipt *gethtypes.Receipt, observedAt time.Time) (CommitResult, error) {
	if p == nil || p.gate == nil || p.store == nil || ctx == nil || observedAt.IsZero() {
		return CommitResult{}, ErrReceiptProcessorUnavailable
	}
	result, err := p.gate.Process(receipt)
	if err != nil {
		return CommitResult{}, err
	}
	observations := make([]Observation, 0, len(result.EconomicEvents))
	for _, event := range result.EconomicEvents {
		payload, err := json.Marshal(event)
		if err != nil {
			return CommitResult{}, fmt.Errorf("marshal receipt event: %w", err)
		}
		observation := Observation{
			ChainID:          ChainID,
			Source:           SourceRPC,
			SourceSequence:   result.BlockNumber,
			TransactionHash:  result.TransactionHash,
			StableActionPath: fmt.Sprintf("receipt/log/%06d/%s/%s", event.Log.Index, event.Protocol.String(), event.Kind.String()),
			PayloadJSON:      payload,
			ObservedAt:       observedAt,
		}
		if err := observation.Validate(); err != nil {
			return CommitResult{}, err
		}
		observations = append(observations, observation)
	}
	return p.store.CommitObservations(ctx, observations)
}
