package rbhparser

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

func TestEventFilterProtocolAndKindCombinations(t *testing.T) {
	protocols := []Protocol{ProtocolPonsV2, ProtocolLong, ProtocolO1, ProtocolPoolsTrade, ProtocolPAIR, ProtocolBagsV2, ProtocolLetsCash, ProtocolFlapTax, ProtocolFlapStocks, ProtocolVaro, ProtocolVirtuals}
	kinds := []EventKind{EventLaunch, EventPoolInitialized, EventSwap, EventCurveBuy, EventCurveSell, EventFeePolicy, EventVenueSwap}
	for _, protocol := range protocols {
		for _, kind := range kinds {
			event := Event{Protocol: protocol, Kind: kind}
			filter := EventFilter{IncludeProtocols: Protocols(protocol), IncludeKinds: EventKinds(kind)}
			if !filter.Match(event) {
				t.Fatalf("matching %s/%s event was rejected", protocol, kind)
			}
			filter.ExcludeProtocols = Protocols(protocol)
			if filter.Match(event) {
				t.Fatalf("excluded %s protocol was accepted", protocol)
			}
			filter.ExcludeProtocols = 0
			filter.ExcludeKinds = EventKinds(kind)
			if filter.Match(event) {
				t.Fatalf("excluded %s event was accepted", kind)
			}
		}
	}
}

func TestIncludeOnlyEmptyMatchesNothing(t *testing.T) {
	event := Event{Protocol: ProtocolLong, Kind: EventSwap}
	if IncludeOnlyEvents().Match(event) {
		t.Fatal("empty event include-only filter accepted an event")
	}
	if IncludeOnlyEventProtocols().Match(event) {
		t.Fatal("empty protocol include-only filter accepted an event")
	}
	intent := TransactionIntent{Protocol: ProtocolLong, Kind: IntentBuy}
	if IncludeOnlyIntents().Match(intent) {
		t.Fatal("empty intent include-only filter accepted an intent")
	}
	if IncludeOnlyIntentProtocols().Match(intent) {
		t.Fatal("empty protocol include-only filter accepted an intent")
	}
	if !(EventFilter{}).Match(event) || !(IntentFilter{}).Match(intent) {
		t.Fatal("zero-value filter must remain unrestricted")
	}
	unknownInitialization := Event{Protocol: ProtocolUnknown, Kind: EventPoolInitialized}
	if !(EventFilter{}).Match(unknownInitialization) {
		t.Fatal("zero-value filter rejected an unattributed initialization")
	}
	if IncludeOnlyEventProtocols(ProtocolLong).Match(unknownInitialization) {
		t.Fatal("protocol filter accepted an unattributed initialization")
	}
}

func TestParseLogFilteredSkipsExcludedTradeDecode(t *testing.T) {
	parser := New()
	key := PoolKey{Currency0: common.Address{}, Currency1: common.HexToAddress("0x40"), Fee: 3000, TickSpacing: 60}
	id, err := PoolID(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := parser.Registry().RegisterPool(PoolRegistration{PoolID: id, Protocol: ProtocolLong, Token: key.Currency1, PoolKey: key}); err != nil {
		t.Fatal(err)
	}
	malformed := makeSwapLog(t, key, 1)
	malformed.Data = malformed.Data[:1]
	filter := EventFilter{IncludeProtocols: Protocols(ProtocolPonsV2), IncludeKinds: EventKinds(EventSwap)}
	if _, ok, err := parser.ParseLogFiltered(malformed, filter); err != nil || ok {
		t.Fatalf("excluded malformed swap was decoded: ok=%v err=%v", ok, err)
	}
	filter.IncludeProtocols = Protocols(ProtocolLong)
	if _, _, err := parser.ParseLogFiltered(malformed, filter); err == nil {
		t.Fatal("included malformed swap was not rejected")
	}
}

func TestParseLogFilteredStillRegistersHiddenLaunch(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0x81")
	curve := common.HexToAddress("0x82")
	creator := common.HexToAddress("0x83")
	data, err := abiArguments(t, "address", "uint256", "uint256").Pack(common.Address{}, big.NewInt(2), big.NewInt(3))
	if err != nil {
		t.Fatal(err)
	}
	launch := gethtypes.Log{Address: parser.Addresses().PonsFactory, Topics: []common.Hash{TopicPonsLaunch, addressTopic(token), addressTopic(curve), addressTopic(creator)}, Data: data}
	filter := EventFilter{IncludeProtocols: Protocols(ProtocolPonsV2), IncludeKinds: EventKinds(EventCurveBuy)}
	if _, ok, err := parser.ParseLogFiltered(launch, filter); err != nil || ok {
		t.Fatalf("hidden launch output: ok=%v err=%v", ok, err)
	}
	if registration, ok := parser.Registry().LookupCurve(curve); !ok || registration.Token != token {
		t.Fatalf("hidden launch did not register curve: %#v ok=%v", registration, ok)
	}
	tradeData, err := abiArguments(t, "uint256", "uint256", "uint256", "uint256").Pack(big.NewInt(100), big.NewInt(90), big.NewInt(5), big.NewInt(5))
	if err != nil {
		t.Fatal(err)
	}
	trade := gethtypes.Log{Address: curve, Topics: []common.Hash{TopicPonsCurveBuy, addressTopic(creator), addressTopic(creator)}, Data: tradeData}
	event, ok, err := parser.ParseLogFiltered(trade, filter)
	if err != nil || !ok || event.Kind != EventCurveBuy || event.Protocol != ProtocolPonsV2 {
		t.Fatalf("registered curve trade: event=%#v ok=%v err=%v", event, ok, err)
	}
}

func TestParseReceiptFilteredCommitsHiddenDiscoveryState(t *testing.T) {
	parser := New()
	quote := common.HexToAddress("0x10")
	token := common.HexToAddress("0x20")
	key := PoolKey{Currency0: quote, Currency1: token, Fee: 0x800000, TickSpacing: 8, Hooks: common.HexToAddress("0x4e3468951D49f2EEa976eD0D6e75fFCb44a9a544")}
	id, err := PoolID(key)
	if err != nil {
		t.Fatal(err)
	}
	longData, err := longEventABI.Events["LaunchCreated"].Inputs.NonIndexed().Pack(key.Hooks, common.HexToAddress("0x55"), [32]byte{1}, big.NewInt(100), big.NewInt(86500), "LONG")
	if err != nil {
		t.Fatal(err)
	}
	launch := gethtypes.Log{Address: parser.Addresses().LongLauncher, Topics: []common.Hash{TopicLongLaunch, addressTopic(token), addressTopic(token), addressTopic(quote)}, Data: longData, Index: 1}
	initialize := makeInitializeLog(t, key, 2)
	swap := makeSwapLog(t, key, 3)
	receipt := &gethtypes.Receipt{Logs: []*gethtypes.Log{&launch, &initialize, &swap}}
	filter := EventFilter{IncludeProtocols: Protocols(ProtocolLong), IncludeKinds: EventKinds(EventSwap)}
	events, err := parser.ParseReceiptFiltered(receipt, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != EventSwap || events[0].Protocol != ProtocolLong {
		t.Fatalf("filtered events = %#v", events)
	}
	if registration, ok := parser.Registry().LookupPool(id); !ok || registration.Protocol != ProtocolLong {
		t.Fatalf("hidden discovery state was not committed: %#v ok=%v", registration, ok)
	}
}

func BenchmarkParseExcludedRegisteredSwap(b *testing.B) {
	parser := New()
	key := PoolKey{Currency0: common.Address{}, Currency1: common.HexToAddress("0x40"), Fee: 3000, TickSpacing: 60}
	id, _ := PoolID(key)
	_ = parser.Registry().RegisterPool(PoolRegistration{PoolID: id, Protocol: ProtocolLong, Token: key.Currency1, PoolKey: key})
	log := makeSwapLog(b, key, 1)
	filter := EventFilter{IncludeProtocols: Protocols(ProtocolPonsV2), IncludeKinds: EventKinds(EventSwap)}
	b.ReportAllocs()
	for b.Loop() {
		if _, ok, err := parser.ParseLogFiltered(log, filter); err != nil || ok {
			b.Fatalf("filtered parse: ok=%v err=%v", ok, err)
		}
	}
}
