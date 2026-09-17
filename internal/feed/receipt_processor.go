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
	parser *parser.Parser
	gate   *ReceiptGate
	store  *Store
}

func NewReceiptProcessor(value *parser.Parser, store *Store) (*ReceiptProcessor, error) {
	if store == nil {
		return nil, ErrReceiptProcessorUnavailable
	}
	if value == nil {
		value = parser.New()
	}
	return &ReceiptProcessor{
		parser: value,
		gate:   NewReceiptGateWithAuditSink(value, store),
		store:  store,
	}, nil
}

// Process persists the receipt audit before parser invocation, then atomically
// commits every normalized economic event, its outbox row and the resulting
// parser registry snapshot. If the durable commit fails, the in-memory parser
// registry is restored to its pre-receipt snapshot before returning.
func (p *ReceiptProcessor) Process(ctx context.Context, receipt *gethtypes.Receipt, observedAt time.Time) (CommitResult, error) {
	if p == nil || p.parser == nil || p.gate == nil || p.store == nil || ctx == nil || observedAt.IsZero() {
		return CommitResult{}, ErrReceiptProcessorUnavailable
	}
	before := p.parser.Registry().Snapshot()
	result, err := p.gate.Process(receipt)
	if err != nil {
		return CommitResult{}, err
	}
	observations := make([]Observation, 0, len(result.EconomicEvents))
	for _, event := range result.EconomicEvents {
		payload, err := json.Marshal(event)
		if err != nil {
			if restoreErr := p.parser.Registry().Restore(before); restoreErr != nil {
				return CommitResult{}, fmt.Errorf("marshal receipt event: %v; restore parser registry: %w", err, restoreErr)
			}
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
			if restoreErr := p.parser.Registry().Restore(before); restoreErr != nil {
				return CommitResult{}, fmt.Errorf("validate receipt observation: %v; restore parser registry: %w", err, restoreErr)
			}
			return CommitResult{}, err
		}
		observations = append(observations, observation)
	}
	after := p.parser.Registry().Snapshot()
	commit, err := p.store.CommitReceipt(ctx, observations, after)
	if err == nil {
		return commit, nil
	}
	if restoreErr := p.parser.Registry().Restore(before); restoreErr != nil {
		return CommitResult{}, fmt.Errorf("commit receipt: %v; restore parser registry: %w", err, restoreErr)
	}
	return CommitResult{}, err
}
