// Package registry holds the immutable per-instance ChainProfile, ProtocolAdapter,
// and trusted protocol spender registry. Unknown chains, protocols, or spenders fail closed.
package registry

import (
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

var (
	ErrUnknownChain    = errors.New("unknown or mismatched chain")
	ErrUnknownProtocol = errors.New("unknown protocol for chain")
	ErrUnknownSpender  = errors.New("protocol has no registered trusted spender")
)

type TrustedSpender struct {
	Address         common.Address
	ExpectedCodeHash common.Hash
}

type Registry struct {
	chain    evm.ChainProfile
	adapters map[string]ports.ProtocolAdapter
	spenders map[string]TrustedSpender
}

func New(profile evm.ChainProfile, adapters []ports.ProtocolAdapter, spenders map[string]TrustedSpender) (*Registry, error) {
	if profile.ChainID == 0 || profile.ChainSlug == "" {
		return nil, errors.New("invalid chain profile")
	}
	r := &Registry{
		chain:    profile,
		adapters: make(map[string]ports.ProtocolAdapter, len(adapters)),
		spenders: make(map[string]TrustedSpender, len(spenders)),
	}
	for _, a := range adapters {
		if a == nil {
			continue
		}
		d := a.Describe()
		if d.ChainID != profile.ChainID {
			return nil, fmt.Errorf("adapter %s chain_id=%d does not match profile chain_id=%d", d.ProtocolID, d.ChainID, profile.ChainID)
		}
		if _, exists := r.adapters[d.ProtocolID]; exists {
			return nil, fmt.Errorf("duplicate protocol adapter %q", d.ProtocolID)
		}
		r.adapters[d.ProtocolID] = a
	}
	for proto, sp := range spenders {
		if sp.Address == (common.Address{}) {
			return nil, fmt.Errorf("zero spender address for protocol %q", proto)
		}
		r.spenders[proto] = sp
	}
	return r, nil
}

func (r *Registry) Chain() evm.ChainProfile { return r.chain }

func (r *Registry) Adapter(protocolID string) (ports.ProtocolAdapter, error) {
	if r == nil {
		return nil, ErrUnknownProtocol
	}
	a, ok := r.adapters[protocolID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProtocol, protocolID)
	}
	return a, nil
}

func (r *Registry) Spender(protocolID string) (TrustedSpender, error) {
	if r == nil {
		return TrustedSpender{}, ErrUnknownSpender
	}
	sp, ok := r.spenders[protocolID]
	if !ok {
		return TrustedSpender{}, fmt.Errorf("%w: %s", ErrUnknownSpender, protocolID)
	}
	return sp, nil
}
