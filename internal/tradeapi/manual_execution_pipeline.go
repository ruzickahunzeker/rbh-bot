package tradeapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// ManualExecutionPipeline wires the formal Step 1A single-stack execution chain:
// binding -> nonce_claim -> local_signing -> submission_foundation -> runtime_capture
// -> first_permit_issue -> submission_executor.
//
// Once C3-C durable admission commits, execution detaches from upstream HTTP client
// cancellation (context.WithoutCancel) and is bounded by min(execBudget, expiresAt)
// plus process graceful drain. Restart or duplicate POST never recreates a lost continuation.
type ManualExecutionPipeline struct {
	store      *Store
	scope      manualAdmissionScope
	binding    *manualExecutionBindingWriter
	nonce      *manualNonceClaimWriter
	signing    *manualLocalSigningWriter
	foundation *manualSubmissionFoundationWriter
	verifier   *manualRuntimeVerifier
	issuer     *manualFirstBroadcastIssuer
	executor   *manualSubmissionExecutor
	execBudget time.Duration
	drainCtx   context.Context
	now        func() time.Time

	inFlightMu   sync.Mutex
	inFlightOp   map[string]chan struct{}
	inFlightReq  map[string]chan struct{}
	hookAfterAdm func() // test-only optional hook fired right after C3-C commit
}

type ManualExecutionPipelineConfig struct {
	Store      *Store
	Scope      manualAdmissionScope
	Binding    *manualExecutionBindingWriter
	Nonce      *manualNonceClaimWriter
	Signing    *manualLocalSigningWriter
	Verifier   *manualRuntimeVerifier
	Executor   *manualSubmissionExecutor
	ExecBudget time.Duration
	DrainCtx   context.Context
	Now        func() time.Time
}

func newManualExecutionPipeline(cfg ManualExecutionPipelineConfig) (*ManualExecutionPipeline, error) {
	if cfg.Store == nil || cfg.Binding == nil || cfg.Nonce == nil || cfg.Signing == nil || cfg.Verifier == nil || cfg.Executor == nil {
		return nil, ErrInvalid
	}
	foundation, err := newManualSubmissionFoundationWriter(cfg.Store)
	if err != nil {
		return nil, err
	}
	budget := cfg.ExecBudget
	if budget <= 0 || budget > 5*time.Minute {
		budget = 30 * time.Second
	}
	drain := cfg.DrainCtx
	if drain == nil {
		drain = context.Background()
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &ManualExecutionPipeline{
		store:       cfg.Store,
		scope:       cfg.Scope,
		binding:     cfg.Binding,
		nonce:       cfg.Nonce,
		signing:     cfg.Signing,
		foundation:  foundation,
		verifier:    cfg.Verifier,
		issuer:      &manualFirstBroadcastIssuer{verifier: cfg.Verifier},
		executor:    cfg.Executor,
		execBudget:  budget,
		drainCtx:    drain,
		now:         nowFn,
		inFlightOp:  make(map[string]chan struct{}),
		inFlightReq: make(map[string]chan struct{}),
	}, nil
}

// IsOperationInFlight reports whether an active in-memory continuation is currently
// running for operationID (used by abort-pre-sign to prevent racing a live goroutine).
func (p *ManualExecutionPipeline) IsOperationInFlight(operationID string) bool {
	if p == nil || operationID == "" {
		return false
	}
	p.inFlightMu.Lock()
	defer p.inFlightMu.Unlock()
	_, ok := p.inFlightOp[operationID]
	return ok
}

// WaitRequestInFlight waits for an active in-memory continuation for requestID to finish
// up to ctx.Done(). Returns true if an active continuation was observed.
func (p *ManualExecutionPipeline) WaitRequestInFlight(ctx context.Context, requestID string) (wasActive bool, finished bool) {
	if p == nil || requestID == "" {
		return false, true
	}
	p.inFlightMu.Lock()
	ch, ok := p.inFlightReq[requestID]
	p.inFlightMu.Unlock()
	if !ok {
		return false, true
	}
	select {
	case <-ch:
		return true, true
	case <-ctx.Done():
		return true, false
	}
}

func (p *ManualExecutionPipeline) registerInFlight(operationID, requestID string) func() {
	ch := make(chan struct{})
	p.inFlightMu.Lock()
	p.inFlightOp[operationID] = ch
	p.inFlightReq[requestID] = ch
	p.inFlightMu.Unlock()
	return func() {
		p.inFlightMu.Lock()
		delete(p.inFlightOp, operationID)
		delete(p.inFlightReq, requestID)
		p.inFlightMu.Unlock()
		close(ch)
	}
}

// executeWinningContinuation claims the unique one-shot continuation and advances it
// synchronously on a detached server context immune to client HTTP disconnect.
func (p *ManualExecutionPipeline) executeWinningContinuation(reqCtx context.Context, continuation *manualAdmissionContinuation) error {
	if p == nil || continuation == nil {
		return ErrConflict
	}
	// Claim one-shot ownership before detaching context.
	owned, err := continuation.take(reqCtx)
	if err != nil {
		return err
	}
	// Detach from client socket cancellation so an upstream timeout after C3-C
	// commit cannot abort signing/broadcast mid-flight.
	// Use relative remaining timeout so deterministic test clocks and real wall clocks behave identically.
	baseCtx := context.WithoutCancel(reqCtx)
	timeout := p.execBudget
	if exp, parseErr := time.Parse(time.RFC3339Nano, owned.expiresAt); parseErr == nil {
		if rem := exp.Sub(p.now().UTC()); rem < timeout {
			timeout = rem
		}
	}
	if timeout <= 0 {
		return context.DeadlineExceeded
	}
	execCtx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()

	// Cancel execCtx if process graceful drain fires.
	stopDrain := context.AfterFunc(p.drainCtx, cancel)
	defer stopDrain()

	done := p.registerInFlight(owned.operation, owned.request)
	defer done()

	if p.hookAfterAdm != nil {
		p.hookAfterAdm()
	}

	b, err := p.binding.bind(execCtx, owned.identity, owned.request, p.scope)
	if err != nil {
		return err
	}
	if b.Duplicate {
		return ErrConflict
	}
	n, err := p.nonce.claim(execCtx, owned.identity, owned.request, p.scope)
	if err != nil {
		return err
	}
	if n.Duplicate {
		return ErrConflict
	}
	signed, err := p.signing.sign(execCtx, owned.identity, owned.request, p.scope)
	if err != nil {
		return err
	}
	if signed.Duplicate || signed.Status != "LOCAL_ARTIFACT_STORED_NOT_PERMISSION" {
		return ErrConflict
	}
	if err := p.foundation.write(execCtx, signed.OperationID); err != nil {
		return err
	}
	if _, err := p.verifier.capture(execCtx, owned.identity, owned.request); err != nil {
		return err
	}
	permit, err := p.issuer.issue(execCtx, owned.identity, owned.request, signed.Artifact.AttemptID, signed.Artifact.RawSHA256, signed.Artifact.TxHash)
	if err != nil {
		return err
	}
	if _, err := p.executor.execute(execCtx, owned.identity, owned.request, permit.ID); err != nil {
		return err
	}
	return nil
}

// attachExecutionPipeline connects a formal ManualExecutionPipeline to an API instance.
// Kept private/explicit so default API construction remains intake/admission-only.
func (h *API) attachExecutionPipeline(p *ManualExecutionPipeline) error {
	if h == nil || p == nil {
		return errors.New("invalid execution pipeline attachment")
	}
	h.executionPipeline = p
	return nil
}

// handleWinningContinuation is called inside submit() only when C3-C admission succeeded
// and produced a non-nil one-shot continuation, or when a duplicate request arrives while
// an active continuation is still running in memory.
func (h *API) handleContinuationOrInFlight(r *http.Request, record *Record, continuation *manualAdmissionContinuation) error {
	if h.executionPipeline == nil {
		return nil
	}
	if continuation != nil {
		// U-P0-2: C3-C durable admission has ALREADY committed in SQLite. Even if a downstream
		// continuation step times out or hits an RPC error, reload the durable Record and return
		// nil so HTTP responds 202 Accepted with true durable state rather than misleading 503.
		_ = h.executionPipeline.executeWinningContinuation(r.Context(), continuation)
		if current, err := h.store.Get(context.WithoutCancel(r.Context()), h.identity, record.RequestID, false); err == nil {
			current.Duplicate = record.Duplicate
			*record = current
		}
		return nil
	}
	// Duplicate POST for the same Idempotency-Key while original winning stack is still active.
	if wasActive, finished := h.executionPipeline.WaitRequestInFlight(r.Context(), record.RequestID); wasActive {
		if finished {
			if current, err := h.store.Get(r.Context(), h.identity, record.RequestID, false); err == nil {
				current.Duplicate = true
				*record = current
			}
		} else {
			record.ContinuationActive = true
		}
	}
	return nil
}
