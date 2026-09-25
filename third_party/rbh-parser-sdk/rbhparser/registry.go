package rbhparser

import (
	"bytes"
	"fmt"
	"sort"
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

// Registry stores immutable registrations. A single lock makes multi-table
// launch commits visible atomically while keeping lookups allocation-free.
type Registry struct {
	mu          sync.RWMutex
	pools       map[common.Hash]PoolRegistration
	curves      map[common.Address]CurveRegistration
	pending     map[common.Hash]PendingPoolRegistration
	tokens      map[common.Address]TokenRegistration
	venues      map[common.Address]TokenRegistration
	tokenVenues map[common.Address]TokenRegistration
	parent      *Registry
}

// RegistrySnapshot is a point-in-time, deterministic copy of all discovery
// state needed to resume parsing after a restart or reorg rollback.
type RegistrySnapshot struct {
	Pools        []PoolRegistration        `json:"pools"`
	Curves       []CurveRegistration       `json:"curves"`
	PendingPools []PendingPoolRegistration `json:"pending_pools"`
	Tokens       []TokenRegistration       `json:"tokens,omitempty"`
	Venues       []TokenRegistration       `json:"venues,omitempty"`
}

func NewRegistry() *Registry {
	return &Registry{
		pools:       make(map[common.Hash]PoolRegistration),
		curves:      make(map[common.Address]CurveRegistration),
		pending:     make(map[common.Hash]PendingPoolRegistration),
		tokens:      make(map[common.Address]TokenRegistration),
		venues:      make(map[common.Address]TokenRegistration),
		tokenVenues: make(map[common.Address]TokenRegistration),
	}
}

// NewOverlayRegistry returns an isolated registry whose misses fall through to
// parent. Discarding it cannot mutate confirmed discovery state.
func NewOverlayRegistry(parent *Registry) *Registry {
	overlay := NewRegistry()
	overlay.parent = parent
	return overlay
}

func NewRegistryFromSnapshot(snapshot RegistrySnapshot) (*Registry, error) {
	registry := NewRegistry()
	for _, registration := range snapshot.Tokens {
		if err := registry.RegisterToken(registration); err != nil {
			return nil, err
		}
	}
	for _, registration := range snapshot.Venues {
		if err := registry.RegisterVenue(registration); err != nil {
			return nil, err
		}
	}
	for _, registration := range snapshot.Curves {
		if err := registry.RegisterCurve(registration); err != nil {
			return nil, err
		}
	}
	for _, registration := range snapshot.PendingPools {
		if err := registry.RegisterPendingPool(registration); err != nil {
			return nil, err
		}
	}
	for _, registration := range snapshot.Pools {
		if err := registry.RegisterPool(registration); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *Registry) LookupToken(token common.Address) (TokenRegistration, bool) {
	if r == nil {
		return TokenRegistration{}, false
	}
	r.mu.RLock()
	value, ok := r.tokens[token]
	r.mu.RUnlock()
	if ok {
		return value, true
	}
	if r.parent != nil {
		return r.parent.LookupToken(token)
	}
	return TokenRegistration{}, false
}

func (r *Registry) LookupVenue(venue common.Address) (TokenRegistration, bool) {
	if r == nil || venue == (common.Address{}) {
		return TokenRegistration{}, false
	}
	r.mu.RLock()
	registration, ok := r.venues[venue]
	r.mu.RUnlock()
	if ok {
		return registration, true
	}
	if r.parent != nil {
		return r.parent.LookupVenue(venue)
	}
	return TokenRegistration{}, false
}

func (r *Registry) LookupTokenVenue(token common.Address) (TokenRegistration, bool) {
	if r == nil || token == (common.Address{}) {
		return TokenRegistration{}, false
	}
	r.mu.RLock()
	registration, ok := r.tokenVenues[token]
	r.mu.RUnlock()
	if ok {
		return registration, true
	}
	if r.parent != nil {
		return r.parent.LookupTokenVenue(token)
	}
	return TokenRegistration{}, false
}

func (r *Registry) LookupPool(id common.Hash) (PoolRegistration, bool) {
	if r == nil {
		return PoolRegistration{}, false
	}
	r.mu.RLock()
	value, ok := r.pools[id]
	r.mu.RUnlock()
	if ok {
		return value, true
	}
	if r.parent != nil {
		return r.parent.LookupPool(id)
	}
	return PoolRegistration{}, false
}

func (r *Registry) LookupCurve(curve common.Address) (CurveRegistration, bool) {
	if r == nil {
		return CurveRegistration{}, false
	}
	r.mu.RLock()
	value, ok := r.curves[curve]
	r.mu.RUnlock()
	if ok {
		return value, true
	}
	if r.parent != nil {
		return r.parent.LookupCurve(curve)
	}
	return CurveRegistration{}, false
}

func (r *Registry) LookupPendingPool(id common.Hash) (PendingPoolRegistration, bool) {
	if r == nil {
		return PendingPoolRegistration{}, false
	}
	r.mu.RLock()
	if _, ok := r.pools[id]; ok {
		r.mu.RUnlock()
		return PendingPoolRegistration{}, false
	}
	value, ok := r.pending[id]
	r.mu.RUnlock()
	if ok {
		return value, true
	}
	if r.parent != nil {
		return r.parent.LookupPendingPool(id)
	}
	return PendingPoolRegistration{}, false
}

func (r *Registry) RegisterPool(registration PoolRegistration) error {
	if r == nil || !registration.Protocol.Valid() || registration.Token == (common.Address{}) || registration.Token == registration.Quote {
		return ErrInvalidRegistration
	}
	id, err := PoolID(registration.PoolKey)
	if err != nil {
		return err
	}
	if id != registration.PoolID {
		return ErrPoolIDMismatch
	}
	if registration.Token != registration.PoolKey.Currency0 && registration.Token != registration.PoolKey.Currency1 {
		return ErrInvalidRegistration
	}
	if registration.Quote != registration.PoolKey.Currency0 && registration.Quote != registration.PoolKey.Currency1 {
		return ErrInvalidRegistration
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureMapsLocked()
	if prior, ok := r.pools[registration.PoolID]; ok && prior != registration {
		return fmt.Errorf("pool %s: %w", registration.PoolID, ErrConflictingPool)
	}
	if pending, ok := r.pending[registration.PoolID]; ok && !pendingMatchesPool(pending, registration) {
		return fmt.Errorf("pool %s: %w", registration.PoolID, ErrConflictingPool)
	}
	if r.parent != nil {
		if prior, ok := r.parent.LookupPool(registration.PoolID); ok && prior != registration {
			return fmt.Errorf("pool %s: %w", registration.PoolID, ErrConflictingPool)
		}
		if pending, ok := r.parent.LookupPendingPool(registration.PoolID); ok && !pendingMatchesPool(pending, registration) {
			return fmt.Errorf("pool %s: %w", registration.PoolID, ErrConflictingPool)
		}
	}
	r.pools[registration.PoolID] = registration
	delete(r.pending, registration.PoolID)
	return nil
}

func (r *Registry) RegisterCurve(registration CurveRegistration) error {
	if r == nil || registration.Curve == (common.Address{}) || !registration.Protocol.Valid() || registration.Token == (common.Address{}) || registration.Token == registration.Quote {
		return ErrInvalidRegistration
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureMapsLocked()
	if prior, ok := r.curves[registration.Curve]; ok && prior != registration {
		return fmt.Errorf("curve %s: %w", registration.Curve, ErrConflictingCurve)
	}
	if r.parent != nil {
		if prior, ok := r.parent.LookupCurve(registration.Curve); ok && prior != registration {
			return fmt.Errorf("curve %s: %w", registration.Curve, ErrConflictingCurve)
		}
	}
	r.curves[registration.Curve] = registration
	return nil
}

func (r *Registry) RegisterPendingPool(registration PendingPoolRegistration) error {
	if r == nil || registration.PoolID == (common.Hash{}) || !registration.Protocol.Valid() || registration.Token == (common.Address{}) || registration.Token == registration.Quote {
		return ErrInvalidRegistration
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureMapsLocked()
	if pool, ok := r.pools[registration.PoolID]; ok {
		if !pendingMatchesPool(registration, pool) {
			return fmt.Errorf("pool %s: %w", registration.PoolID, ErrConflictingPool)
		}
		return nil
	}
	if prior, ok := r.pending[registration.PoolID]; ok && prior != registration {
		return fmt.Errorf("pending pool %s: %w", registration.PoolID, ErrConflictingPool)
	}
	if r.parent != nil {
		if pool, ok := r.parent.LookupPool(registration.PoolID); ok {
			if !pendingMatchesPool(registration, pool) {
				return fmt.Errorf("pool %s: %w", registration.PoolID, ErrConflictingPool)
			}
			return nil
		}
		if prior, ok := r.parent.LookupPendingPool(registration.PoolID); ok && prior != registration {
			return fmt.Errorf("pending pool %s: %w", registration.PoolID, ErrConflictingPool)
		}
	}
	r.pending[registration.PoolID] = registration
	return nil
}

func (r *Registry) RegisterToken(registration TokenRegistration) error {
	if r == nil || registration.Token == (common.Address{}) || !registration.Protocol.Valid() || registration.Token == registration.Quote || registration.Token == registration.Venue {
		return ErrInvalidRegistration
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureMapsLocked()
	prior, ok := r.tokens[registration.Token]
	if !ok && r.parent != nil {
		prior, ok = r.parent.LookupToken(registration.Token)
	}
	if ok {
		merged, err := mergeTokenRegistration(prior, registration)
		if err != nil {
			return err
		}
		registration = merged
	}
	// Flap's bonding Portal is shared by every token and is therefore not a
	// venue identity. Its unique V2 pair is registered at graduation instead.
	if registration.Venue != (common.Address{}) && registration.Protocol != ProtocolFlapTax && registration.Protocol != ProtocolFlapStocks {
		if err := r.validateVenueLocked(registration); err != nil {
			return err
		}
	}
	r.tokens[registration.Token] = registration
	if registration.Venue != (common.Address{}) && registration.Protocol != ProtocolFlapTax && registration.Protocol != ProtocolFlapStocks {
		r.venues[registration.Venue] = registration
		r.tokenVenues[registration.Token] = registration
	}
	return nil
}

// RegisterVenue associates a unique non-V4 venue with a token. It is separate
// from RegisterToken because some launchpads use a shared bonding router before
// migrating each token to its own pool.
func (r *Registry) RegisterVenue(registration TokenRegistration) error {
	if r == nil || registration.Token == (common.Address{}) || registration.Venue == (common.Address{}) ||
		!registration.Protocol.Valid() || registration.Token == registration.Quote || registration.Token == registration.Venue {
		return ErrInvalidRegistration
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureMapsLocked()
	token, ok := r.tokens[registration.Token]
	if !ok && r.parent != nil {
		token, ok = r.parent.LookupToken(registration.Token)
	}
	if !ok || token.Protocol != registration.Protocol || token.Quote != registration.Quote {
		return ErrInvalidRegistration
	}
	return r.registerVenueLocked(registration)
}

func (r *Registry) registerVenueLocked(registration TokenRegistration) error {
	if err := r.validateVenueLocked(registration); err != nil {
		return err
	}
	r.venues[registration.Venue] = registration
	r.tokenVenues[registration.Token] = registration
	return nil
}

func (r *Registry) validateVenueLocked(registration TokenRegistration) error {
	if prior, ok := r.venues[registration.Venue]; ok && prior != registration {
		return fmt.Errorf("venue %s: %w", registration.Venue, ErrConflictingToken)
	}
	if r.parent != nil {
		if prior, ok := r.parent.LookupVenue(registration.Venue); ok && prior != registration {
			return fmt.Errorf("venue %s: %w", registration.Venue, ErrConflictingToken)
		}
	}
	if prior, ok := r.tokenVenues[registration.Token]; ok && prior != registration {
		return fmt.Errorf("token venue %s: %w", registration.Token, ErrConflictingToken)
	}
	if r.parent != nil {
		if prior, ok := r.parent.LookupTokenVenue(registration.Token); ok && prior != registration {
			return fmt.Errorf("token venue %s: %w", registration.Token, ErrConflictingToken)
		}
	}
	return nil
}

func mergeTokenRegistration(a, b TokenRegistration) (TokenRegistration, error) {
	if a.Token != b.Token || a.Protocol != b.Protocol ||
		(a.Quote != (common.Address{}) && b.Quote != (common.Address{}) && a.Quote != b.Quote) ||
		(a.Venue != (common.Address{}) && b.Venue != (common.Address{}) && a.Venue != b.Venue) {
		return TokenRegistration{}, fmt.Errorf("token %s: %w", b.Token, ErrConflictingToken)
	}
	if a.Quote == (common.Address{}) {
		a.Quote = b.Quote
	}
	if a.Venue == (common.Address{}) {
		a.Venue = b.Venue
	}
	return a, nil
}

func pendingMatchesPool(pending PendingPoolRegistration, pool PoolRegistration) bool {
	return pending.Protocol == pool.Protocol && pending.Token == pool.Token && (pending.Quote == (common.Address{}) || pending.Quote == pool.Quote)
}

func (r *Registry) ensureMapsLocked() {
	if r.pools == nil {
		r.pools = make(map[common.Hash]PoolRegistration)
	}
	if r.curves == nil {
		r.curves = make(map[common.Address]CurveRegistration)
	}
	if r.pending == nil {
		r.pending = make(map[common.Hash]PendingPoolRegistration)
	}
	if r.tokens == nil {
		r.tokens = make(map[common.Address]TokenRegistration)
	}
	if r.venues == nil {
		r.venues = make(map[common.Address]TokenRegistration)
	}
	if r.tokenVenues == nil {
		r.tokenVenues = make(map[common.Address]TokenRegistration)
	}
}

func (r *Registry) fork() *Registry {
	overlay := NewRegistry()
	overlay.parent = r
	return overlay
}

// commit validates a private overlay completely before publishing any entry.
func (r *Registry) commit(overlay *Registry) error {
	var pools []PoolRegistration
	var curves []CurveRegistration
	var pending []PendingPoolRegistration
	var tokens []TokenRegistration
	var venues []TokenRegistration
	overlay.mu.RLock()
	for _, registration := range overlay.pools {
		pools = append(pools, registration)
	}
	for _, registration := range overlay.curves {
		curves = append(curves, registration)
	}
	for _, registration := range overlay.pending {
		pending = append(pending, registration)
	}
	for _, registration := range overlay.tokens {
		tokens = append(tokens, registration)
	}
	for _, registration := range overlay.venues {
		venues = append(venues, registration)
	}
	overlay.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureMapsLocked()
	for _, registration := range pools {
		if prior, ok := r.pools[registration.PoolID]; ok && prior != registration {
			return fmt.Errorf("pool %s: %w", registration.PoolID, ErrConflictingPool)
		}
		if prior, ok := r.pending[registration.PoolID]; ok && !pendingMatchesPool(prior, registration) {
			return fmt.Errorf("pool %s: %w", registration.PoolID, ErrConflictingPool)
		}
	}
	for _, registration := range curves {
		if prior, ok := r.curves[registration.Curve]; ok && prior != registration {
			return fmt.Errorf("curve %s: %w", registration.Curve, ErrConflictingCurve)
		}
	}
	for _, registration := range pending {
		if pool, ok := r.pools[registration.PoolID]; ok && !pendingMatchesPool(registration, pool) {
			return fmt.Errorf("pool %s: %w", registration.PoolID, ErrConflictingPool)
		}
		if prior, ok := r.pending[registration.PoolID]; ok && prior != registration {
			return fmt.Errorf("pending pool %s: %w", registration.PoolID, ErrConflictingPool)
		}
	}
	mergedTokens := make(map[common.Address]TokenRegistration, len(tokens))
	for _, registration := range tokens {
		if prior, ok := r.tokens[registration.Token]; ok {
			var err error
			registration, err = mergeTokenRegistration(prior, registration)
			if err != nil {
				return err
			}
		}
		mergedTokens[registration.Token] = registration
	}
	for _, registration := range venues {
		token, ok := mergedTokens[registration.Token]
		if !ok {
			token, ok = r.tokens[registration.Token]
		}
		if !ok || token.Protocol != registration.Protocol || token.Quote != registration.Quote {
			return ErrInvalidRegistration
		}
		if prior, ok := r.venues[registration.Venue]; ok && prior != registration {
			return fmt.Errorf("venue %s: %w", registration.Venue, ErrConflictingToken)
		}
		if prior, ok := r.tokenVenues[registration.Token]; ok && prior != registration {
			return fmt.Errorf("token venue %s: %w", registration.Token, ErrConflictingToken)
		}
	}
	for _, registration := range curves {
		r.curves[registration.Curve] = registration
	}
	for _, registration := range pools {
		r.pools[registration.PoolID] = registration
		delete(r.pending, registration.PoolID)
	}
	for _, registration := range pending {
		if _, ok := r.pools[registration.PoolID]; !ok {
			r.pending[registration.PoolID] = registration
		}
	}
	for _, registration := range mergedTokens {
		r.tokens[registration.Token] = registration
	}
	for _, registration := range venues {
		r.venues[registration.Venue] = registration
		r.tokenVenues[registration.Token] = registration
	}
	return nil
}

func (r *Registry) Tokens() []TokenRegistration {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]TokenRegistration, 0, len(r.tokens))
	for _, registration := range r.tokens {
		out = append(out, registration)
	}
	r.mu.RUnlock()
	return out
}

func (r *Registry) Pools() []PoolRegistration {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]PoolRegistration, 0, len(r.pools))
	for _, registration := range r.pools {
		out = append(out, registration)
	}
	r.mu.RUnlock()
	return out
}

func (r *Registry) Curves() []CurveRegistration {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]CurveRegistration, 0, len(r.curves))
	for _, registration := range r.curves {
		out = append(out, registration)
	}
	r.mu.RUnlock()
	return out
}

func (r *Registry) PendingPools() []PendingPoolRegistration {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]PendingPoolRegistration, 0, len(r.pending))
	for _, registration := range r.pending {
		if _, registered := r.pools[registration.PoolID]; !registered {
			out = append(out, registration)
		}
	}
	r.mu.RUnlock()
	return out
}

// Snapshot copies all registry tables under one read lock. The returned
// slices are sorted so JSON snapshots and checksums remain reproducible.
func (r *Registry) Snapshot() RegistrySnapshot {
	if r == nil {
		return RegistrySnapshot{}
	}
	r.mu.RLock()
	snapshot := RegistrySnapshot{
		Pools:        make([]PoolRegistration, 0, len(r.pools)),
		Curves:       make([]CurveRegistration, 0, len(r.curves)),
		PendingPools: make([]PendingPoolRegistration, 0, len(r.pending)),
		Tokens:       make([]TokenRegistration, 0, len(r.tokens)),
		Venues:       make([]TokenRegistration, 0, len(r.venues)),
	}
	for _, registration := range r.pools {
		snapshot.Pools = append(snapshot.Pools, registration)
	}
	for _, registration := range r.curves {
		snapshot.Curves = append(snapshot.Curves, registration)
	}
	for _, registration := range r.pending {
		if _, registered := r.pools[registration.PoolID]; !registered {
			snapshot.PendingPools = append(snapshot.PendingPools, registration)
		}
	}
	for _, registration := range r.tokens {
		snapshot.Tokens = append(snapshot.Tokens, registration)
	}
	for _, registration := range r.venues {
		snapshot.Venues = append(snapshot.Venues, registration)
	}
	r.mu.RUnlock()
	sort.Slice(snapshot.Pools, func(i, j int) bool {
		return bytes.Compare(snapshot.Pools[i].PoolID[:], snapshot.Pools[j].PoolID[:]) < 0
	})
	sort.Slice(snapshot.Curves, func(i, j int) bool {
		return bytes.Compare(snapshot.Curves[i].Curve[:], snapshot.Curves[j].Curve[:]) < 0
	})
	sort.Slice(snapshot.PendingPools, func(i, j int) bool {
		return bytes.Compare(snapshot.PendingPools[i].PoolID[:], snapshot.PendingPools[j].PoolID[:]) < 0
	})
	sort.Slice(snapshot.Tokens, func(i, j int) bool {
		return bytes.Compare(snapshot.Tokens[i].Token[:], snapshot.Tokens[j].Token[:]) < 0
	})
	sort.Slice(snapshot.Venues, func(i, j int) bool {
		return bytes.Compare(snapshot.Venues[i].Venue[:], snapshot.Venues[j].Venue[:]) < 0
	})
	return snapshot
}

// Restore validates the complete snapshot before replacing live state. It is
// intended for startup hydration and fail-closed reorg recovery.
func (r *Registry) Restore(snapshot RegistrySnapshot) error {
	if r == nil {
		return ErrInvalidRegistration
	}
	candidate, err := NewRegistryFromSnapshot(snapshot)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.parent != nil {
		return ErrInvalidRegistration
	}
	r.pools = candidate.pools
	r.curves = candidate.curves
	r.pending = candidate.pending
	r.tokens = candidate.tokens
	r.venues = candidate.venues
	r.tokenVenues = candidate.tokenVenues
	return nil
}
