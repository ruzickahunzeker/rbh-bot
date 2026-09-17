package feed

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/0xfnzero/rbh-parser-sdk/feed/sequencer"
	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
)

var ErrSequencerRunnerUnavailable = errors.New("sequencer runner unavailable")

type SequencerStatus struct {
	State sequencer.ConnectionState
	Cause string
}

type SequencerRunner struct {
	store      *Store
	normalizer *IntentNormalizer

	mu     sync.RWMutex
	status SequencerStatus
}

func NewSequencerRunner(value *parser.Parser, store *Store) (*SequencerRunner, error) {
	if store == nil {
		return nil, ErrSequencerRunnerUnavailable
	}
	return &SequencerRunner{store: store, normalizer: NewPonsCurveIntentNormalizer(value)}, nil
}

func (r *SequencerRunner) Status() SequencerStatus {
	if r == nil {
		return SequencerStatus{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// Ready is deliberately stricter than connection health. A live socket is not
// ready for economic consumers after a detected gap/reorg/verification failure
// until explicit recovery clears the durable degraded marker.
func (r *SequencerRunner) Ready(ctx context.Context) bool {
	if r == nil || r.store == nil || ctx == nil {
		return false
	}
	status := r.Status()
	if status.State != sequencer.ConnectionLive {
		return false
	}
	progress, err := r.store.Progress(ctx)
	return err == nil && !progress.Degraded
}

func (r *SequencerRunner) Config(ctx context.Context) (sequencer.Config, error) {
	if r == nil || r.store == nil || r.normalizer == nil || ctx == nil {
		return sequencer.Config{}, ErrSequencerRunnerUnavailable
	}
	resume, err := r.store.ResumeSequence(ctx)
	if err != nil {
		return sequencer.Config{}, err
	}
	config := sequencer.Config{
		InitialSequence: resume,
		Filter:          r.normalizer.Potential,
		AllowGaps:       false,
		AllowReorgs:     false,
		OnGap: func(callbackCtx context.Context, gap sequencer.SequenceGap) error {
			return r.store.MarkDegraded(callbackCtx, fmt.Sprintf("sequencer_gap expected=%d received=%d", gap.Expected, gap.Received))
		},
		OnReorg: func(callbackCtx context.Context, reorg sequencer.Reorg) error {
			_, err := r.store.RetractSequencerSequence(callbackCtx, reorg)
			return err
		},
		OnVerificationFailure: func(callbackCtx context.Context, failure sequencer.VerificationFailure) error {
			return r.store.MarkDegraded(callbackCtx, fmt.Sprintf("sequencer_verification_failure sequence=%d signer=%s", failure.SequenceNumber, failure.RecoveredSigner.Hex()))
		},
		OnStatus: r.recordStatus,
		// Do not set OnSequence. The SDK invokes it before the transaction handler,
		// so it is observation telemetry only and must never become our durable
		// business checkpoint. CommitSequencer advances progress after handling.
	}
	return config, nil
}

func (r *SequencerRunner) Run(ctx context.Context) error {
	config, err := r.Config(ctx)
	if err != nil {
		return err
	}
	client, err := sequencer.New(config)
	if err != nil {
		return fmt.Errorf("create sequencer client: %w", err)
	}
	if err := client.Run(ctx, r.Handle); err != nil {
		return fmt.Errorf("run sequencer client: %w", err)
	}
	return nil
}

func (r *SequencerRunner) Handle(ctx context.Context, received sequencer.FeedTransaction) error {
	if r == nil || r.store == nil || r.normalizer == nil || ctx == nil || received.Transaction == nil || received.ReceivedAt.IsZero() {
		return ErrSequencerRunnerUnavailable
	}
	observations, err := r.normalizer.Normalize(received.Transaction, received.Sender, SourceSequencer, received.SequenceNumber, received.ReceivedAt)
	if err != nil {
		return err
	}
	if _, err := r.store.CommitSequencer(ctx, observations, received.SequenceNumber); err != nil {
		return err
	}
	return nil
}

func (r *SequencerRunner) recordStatus(_ context.Context, status sequencer.ConnectionStatus) {
	if r == nil {
		return
	}
	value := SequencerStatus{State: status.State}
	if status.Cause != nil {
		value.Cause = status.Cause.Error()
	}
	r.mu.Lock()
	r.status = value
	r.mu.Unlock()
}
