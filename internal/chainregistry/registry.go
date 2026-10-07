// Package chainregistry describes installed runtime identity, not admission,
// authorization or health. Returned values cannot mutate the built-in registry.
package chainregistry

import (
	"errors"
	"fmt"
)

const (
	RobinhoodID uint64 = 4663
	BSCID       uint64 = 56
	BaseID      uint64 = 8453
)

type RuntimeStatus string

const (
	LegacyRobinhood RuntimeStatus = "LEGACY_ROBINHOOD"
	NotImplemented  RuntimeStatus = "NOT_IMPLEMENTED"
)

type Definition struct {
	ID      uint64        `json:"chain_id"`
	Key     string        `json:"chain"`
	Runtime RuntimeStatus `json:"runtime"`
}

var (
	ErrUnknownChain       = errors.New("unknown EVM chain")
	ErrRuntimeUnavailable = errors.New("EVM chain runtime not implemented")
)

// Lookup has no caller-extensible registration path, shared maps or enable flags.
func Lookup(id uint64) (Definition, error) {
	switch id {
	case RobinhoodID:
		return Definition{ID: id, Key: "robinhood", Runtime: LegacyRobinhood}, nil
	case BSCID:
		return Definition{ID: id, Key: "bsc", Runtime: NotImplemented}, nil
	case BaseID:
		return Definition{ID: id, Key: "base", Runtime: NotImplemented}, nil
	default:
		return Definition{}, ErrUnknownChain
	}
}

// List returns independent value copies. Presence in this list grants no gate,
// protocol, wallet or broadcast permission.
func List() []Definition {
	chains := make([]Definition, 0, 3)
	for _, id := range []uint64{RobinhoodID, BSCID, BaseID} {
		definition, _ := Lookup(id)
		chains = append(chains, definition)
	}
	return chains
}

// RequireRuntime is the pre-resource startup boundary. E1 may start only the
// existing Robinhood runtime; BSC/Base must never reach that runtime by fallback.
func RequireRuntime(id uint64) (Definition, error) {
	definition, err := Lookup(id)
	if err != nil {
		return Definition{}, err
	}
	if definition.Runtime != LegacyRobinhood {
		return Definition{}, fmt.Errorf("%w: chain %d", ErrRuntimeUnavailable, id)
	}
	return definition, nil
}
