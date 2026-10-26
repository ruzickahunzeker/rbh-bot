package tradeapi

import (
	"context"
	"sync/atomic"
)

// Ownership only, NOT permission. No serializable/exported state, durable
// ticket, execution consumer or constructor accepting a public request ID.
// All copies share state; copying this value cannot reset its one-shot guard.
type manualAdmissionContinuation struct {
	state *manualAdmissionContinuationState
}

type manualAdmissionContinuationIdentity struct {
	identity                                                 Identity
	request, operation, reservation, contractHash, expiresAt string
	scope                                                    manualAdmissionScope
}

type manualAdmissionContinuationState struct {
	identity manualAdmissionContinuationIdentity
	origin   context.Context
	spent    atomic.Bool
}

func (c *manualAdmissionContinuation) discard() {
	if c != nil && c.state != nil {
		c.state.spent.Store(true)
	}
}

// take only claims this original stack's ownership once. It does not execute
// anything or replace any downstream durable gate. A cancellation/failure
// burns ownership; a fresh caller context cannot override the origin's cancel.
func (c *manualAdmissionContinuation) take(ctx context.Context) (manualAdmissionContinuationIdentity, error) {
	var zero manualAdmissionContinuationIdentity
	if c == nil || c.state == nil || !c.state.spent.CompareAndSwap(false, true) {
		return zero, ErrConflict
	}
	if ctx == nil || c.state.origin == nil {
		return zero, ErrInvalid
	}
	if err := c.state.origin.Err(); err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return c.state.identity, nil
}
