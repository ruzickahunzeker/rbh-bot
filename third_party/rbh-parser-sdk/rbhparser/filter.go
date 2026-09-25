package rbhparser

import "github.com/ethereum/go-ethereum/common"

type ProtocolSet uint64
type IntentKindSet uint32
type EventKindSet uint32
type AddressSet map[common.Address]struct{}
type PoolSet map[common.Hash]struct{}

func Protocols(protocols ...Protocol) ProtocolSet {
	var set ProtocolSet
	for _, protocol := range protocols {
		if protocol.Valid() {
			set |= 1 << protocol
		}
	}
	return set
}

func IntentKinds(kinds ...IntentKind) IntentKindSet {
	var set IntentKindSet
	for _, kind := range kinds {
		if kind > IntentUnknown && kind <= IntentFeePolicy {
			set |= 1 << kind
		}
	}
	return set
}

func EventKinds(kinds ...EventKind) EventKindSet {
	var set EventKindSet
	for _, kind := range kinds {
		if kind > EventUnknown && kind <= EventVenueState {
			set |= 1 << kind
		}
	}
	return set
}

func Addresses(addresses ...common.Address) AddressSet {
	set := make(AddressSet, len(addresses))
	for _, address := range addresses {
		set[address] = struct{}{}
	}
	return set
}

func PoolIDs(ids ...common.Hash) PoolSet {
	set := make(PoolSet, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

// IntentFilter follows include-only then exclude semantics. Its zero value is
// unrestricted. An empty set passed to an include-only constructor matches
// nothing. Treat map fields as immutable after the filter is published.
type IntentFilter struct {
	IncludeProtocols ProtocolSet
	ExcludeProtocols ProtocolSet
	IncludeKinds     IntentKindSet
	ExcludeKinds     IntentKindSet
	Senders          AddressSet
	Contracts        AddressSet
	Tokens           AddressSet
	Pools            PoolSet
	Predicate        func(TransactionIntent) bool
	includeProtocols bool
	includeKinds     bool
}

func IncludeOnlyIntents(kinds ...IntentKind) IntentFilter {
	return IntentFilter{IncludeKinds: IntentKinds(kinds...), includeKinds: true}
}

func ExcludeIntents(kinds ...IntentKind) IntentFilter {
	return IntentFilter{ExcludeKinds: IntentKinds(kinds...)}
}

func IncludeOnlyIntentProtocols(protocols ...Protocol) IntentFilter {
	return IntentFilter{IncludeProtocols: Protocols(protocols...), includeProtocols: true}
}

func ExcludeIntentProtocols(protocols ...Protocol) IntentFilter {
	return IntentFilter{ExcludeProtocols: Protocols(protocols...)}
}

// IncludeOnlyProtocols is retained as a concise alias for intent filters.
func IncludeOnlyProtocols(protocols ...Protocol) IntentFilter {
	return IncludeOnlyIntentProtocols(protocols...)
}

func (f IntentFilter) Match(intent TransactionIntent) bool {
	if !f.matchClassification(intent.Protocol, intent.Kind) {
		return false
	}
	if len(f.Senders) > 0 {
		if _, ok := f.Senders[intent.Sender]; !ok {
			return false
		}
	}
	if len(f.Contracts) > 0 {
		if _, ok := f.Contracts[intent.Contract]; !ok {
			return false
		}
	}
	if len(f.Tokens) > 0 {
		if _, ok := f.Tokens[intent.Token]; !ok {
			return false
		}
	}
	if len(f.Pools) > 0 {
		if _, ok := f.Pools[intent.PoolID]; !ok {
			return false
		}
	}
	return f.Predicate == nil || f.Predicate(intent)
}

func (f IntentFilter) matchClassification(protocol Protocol, kind IntentKind) bool {
	if !protocol.Valid() || kind <= IntentUnknown || kind > IntentFeePolicy {
		return false
	}
	protocolBit := ProtocolSet(1) << protocol
	kindBit := IntentKindSet(1) << kind
	if (f.includeProtocols || f.IncludeProtocols != 0) && f.IncludeProtocols&protocolBit == 0 {
		return false
	}
	if f.ExcludeProtocols&protocolBit != 0 {
		return false
	}
	if (f.includeKinds || f.IncludeKinds != 0) && f.IncludeKinds&kindBit == 0 {
		return false
	}
	return f.ExcludeKinds&kindBit == 0
}

func (f IntentFilter) mayMatchV4() bool {
	for protocol := ProtocolPonsV2; protocol <= ProtocolVirtuals; protocol++ {
		if f.matchClassification(protocol, IntentBuy) || f.matchClassification(protocol, IntentSell) {
			return true
		}
	}
	return false
}

func (f IntentFilter) matchContract(contract common.Address) bool {
	if len(f.Contracts) == 0 {
		return true
	}
	_, ok := f.Contracts[contract]
	return ok
}

type EventFilter struct {
	IncludeProtocols ProtocolSet
	ExcludeProtocols ProtocolSet
	IncludeKinds     EventKindSet
	ExcludeKinds     EventKindSet
	Predicate        func(Event) bool
	includeProtocols bool
	includeKinds     bool
}

func IncludeOnlyEventProtocols(protocols ...Protocol) EventFilter {
	return EventFilter{IncludeProtocols: Protocols(protocols...), includeProtocols: true}
}

func ExcludeEventProtocols(protocols ...Protocol) EventFilter {
	return EventFilter{ExcludeProtocols: Protocols(protocols...)}
}

func IncludeOnlyEvents(kinds ...EventKind) EventFilter {
	return EventFilter{IncludeKinds: EventKinds(kinds...), includeKinds: true}
}

func ExcludeEvents(kinds ...EventKind) EventFilter {
	return EventFilter{ExcludeKinds: EventKinds(kinds...)}
}

func (f EventFilter) Match(event Event) bool {
	if !f.matchClassification(event.Protocol, event.Kind) {
		return false
	}
	return f.Predicate == nil || f.Predicate(event)
}

func (f EventFilter) matchClassification(protocol Protocol, kind EventKind) bool {
	if kind <= EventUnknown || kind > EventVenueState || (protocol != ProtocolUnknown && !protocol.Valid()) {
		return false
	}
	kindBit := EventKindSet(1) << kind
	if protocol == ProtocolUnknown {
		if f.includeProtocols || f.IncludeProtocols != 0 || f.ExcludeProtocols != 0 {
			return false
		}
	} else {
		protocolBit := ProtocolSet(1) << protocol
		if (f.includeProtocols || f.IncludeProtocols != 0) && f.IncludeProtocols&protocolBit == 0 {
			return false
		}
		if f.ExcludeProtocols&protocolBit != 0 {
			return false
		}
	}
	if (f.includeKinds || f.IncludeKinds != 0) && f.IncludeKinds&kindBit == 0 {
		return false
	}
	if f.ExcludeKinds&kindBit != 0 {
		return false
	}
	return true
}
