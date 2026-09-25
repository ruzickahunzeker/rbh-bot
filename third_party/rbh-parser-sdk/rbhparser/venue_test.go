package rbhparser

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

func TestParseVaroBuyFromRegisteredLaunch(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0xc1a036b7252289b24fc3a6a778b18bd6f4182b06")
	pool := common.HexToAddress("0x225ef630877b53d70e1ddb2bd06c53918297fe14")
	recipient := common.HexToAddress("0x94e90a9d6b3125ef6dd7238c3d5d2231ee1c52e6")
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: token, Protocol: ProtocolVaro, Quote: parser.Addresses().WETH, Venue: pool}); err != nil {
		t.Fatal(err)
	}
	data, err := abiArguments(t, "uint256", "uint256", "address").Pack(big.NewInt(150), big.NewInt(300), recipient)
	if err != nil {
		t.Fatal(err)
	}
	event, ok, err := parser.ParseLog(gethtypes.Log{Address: parser.Addresses().VaroRouter, Topics: []common.Hash{TopicVaroBuy, addressTopic(pool), addressTopic(token)}, Data: data})
	if err != nil || !ok || event.Kind != EventVenueSwap || event.Protocol != ProtocolVaro {
		t.Fatalf("event=%#v ok=%v err=%v", event, ok, err)
	}
	swap := event.Data.(VenueSwap)
	if !swap.Buy || swap.AmountIn.Uint64() != 150 || swap.AmountOut.Uint64() != 300 || swap.Recipient != recipient {
		t.Fatalf("unexpected Varo swap: %#v", swap)
	}
}

func TestParseGMGNFlapBuy(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0x1347d0f767b69c7ce7cedece46f6b1f742567777")
	venue := common.HexToAddress("0x118047fe71e8ed682e5f02d00ac3924317883ca5")
	quote := parser.Addresses().WETH
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: token, Protocol: ProtocolFlapTax, Venue: venue}); err != nil {
		t.Fatal(err)
	}
	routes := []gmgnRouteWire{{Kind: 6, TokenIn: quote, TokenOut: token, Pool: token, Fee: new(big.Int), TickSpacing: new(big.Int)}}
	data, err := gmgnSwapEventArgs.Pack(big.NewInt(100), big.NewInt(200), routes)
	if err != nil {
		t.Fatal(err)
	}
	recipient := common.HexToAddress("0x94e90a9d6b3125ef6dd7238c3d5d2231ee1c52e6")
	event, ok, err := parser.ParseLog(gethtypes.Log{Address: parser.Addresses().GMGNRouter, Topics: []common.Hash{TopicGMGNSwap, addressTopic(recipient), addressTopic(recipient), common.Hash{}}, Data: data})
	if err != nil || !ok || event.Kind != EventVenueSwap || event.Protocol != ProtocolFlapTax {
		t.Fatalf("event=%#v ok=%v err=%v", event, ok, err)
	}
	swap := event.Data.(VenueSwap)
	if !swap.Buy || swap.Token != token || swap.Quote != quote || len(swap.Routes) != 1 {
		t.Fatalf("unexpected GMGN swap: %#v", swap)
	}
}

func TestParseGMGNIgnoresStructurallyValidUnsupportedRoute(t *testing.T) {
	parser := New()
	routes := []gmgnRouteWire{
		{
			Kind: 1, TokenIn: common.HexToAddress("0x0bd7d308f8e1639fab988df18a8011f41eacad73"), TokenOut: common.HexToAddress("0x5fc5360d0400a0fd4f2af552add042d716f1d168"),
			Pool: common.HexToAddress("0x52e65b17fb6e5ba00ed806f37afcd2daa50271ca"), Fee: big.NewInt(100), TickSpacing: big.NewInt(1),
		},
		{
			Kind: 2, TokenIn: common.HexToAddress("0x5fc5360d0400a0fd4f2af552add042d716f1d168"), TokenOut: common.HexToAddress("0x1cdb289befdfac8af945a288bcdccc382cb34d32"),
			Fee: new(big.Int), TickSpacing: big.NewInt(200), Hook: common.HexToAddress("0xe5e702641ea86f4ae6cc3cdaed2b886f976be044"), Router: common.HexToAddress("0x8366a39cc670b4001a1121b8f6a443a643e40951"),
		},
	}
	amountOut, ok := new(big.Int).SetString("11400000000000000000000", 10)
	if !ok {
		t.Fatal("parse amount out")
	}
	data, err := gmgnSwapEventArgs.Pack(big.NewInt(500_000_000_000_000_000), amountOut, routes)
	if err != nil {
		t.Fatal(err)
	}
	recipient := common.HexToAddress("0x4e93eca37b9558384d3933c39c1fac5e066c3298")
	event, parsed, err := parser.ParseLog(gethtypes.Log{
		Address: parser.Addresses().GMGNRouter,
		Topics:  []common.Hash{TopicGMGNSwap, addressTopic(recipient), addressTopic(recipient), common.Hash{}},
		Data:    data,
	})
	if err != nil || parsed {
		t.Fatalf("unsupported GMGN route event=%#v ok=%v err=%v", event, parsed, err)
	}
}

func TestParseGMGNIgnoresObservedKind27RouteWithEmptyInputToken(t *testing.T) {
	parser := New()
	data := common.FromHex("0x00000000000000000000000000000000000000000000000000470de4df8200000000000000000000000000000000000000000000000382c1a687d99597649746000000000000000000000000000000000000000000000000000000000000006000000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000020000000000000000000000000000000000000000000000000000000000000001b0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000ed0a53f31fd54e9d2f9d2364afed8761877c7131000000000000000000000000788bd92776765ee4e0405e10505abb2818e6612000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001400000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000200000000000000000000000000000000000000000000000000000000000000001")
	recipient := common.HexToAddress("0x3cb1de9d4cc393dcbfe62fcb372b5245bad03c62")
	event, parsed, err := parser.ParseLog(gethtypes.Log{
		Address: parser.Addresses().GMGNRouter,
		Topics:  []common.Hash{TopicGMGNSwap, addressTopic(recipient), addressTopic(recipient), common.Hash{}},
		Data:    data,
	})
	if err != nil || parsed {
		t.Fatalf("observed unsupported GMGN route event=%#v ok=%v err=%v", event, parsed, err)
	}
}

func TestParseGMGNIgnoresObservedUntrackedKind6PlaceholderRoute(t *testing.T) {
	parser := New()
	data := common.FromHex("0x000000000000000000000000000000000000000000000000002aa1efb94e0000000000000000000000000000000000000000000000057fa192aba35a68dedfbb0000000000000000000000000000000000000000000000000000000000000060000000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000200000000000000000000000000000000000000000000000000000000000000006000000000000000000000000000000000000000000000000000000000000000000000000000000000000000028f1107c9e062aa72c5ed814d968486bc9da777700000000000000000000000028f1107c9e062aa72c5ed814d968486bc9da77770000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000140000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000")
	recipient := common.HexToAddress("0x76a69588e66ca5f736e1afab7e74aa27b51471d4")
	event, parsed, err := parser.ParseLog(gethtypes.Log{
		Address: parser.Addresses().GMGNRouter,
		Topics:  []common.Hash{TopicGMGNSwap, addressTopic(recipient), addressTopic(recipient), common.Hash{}},
		Data:    data,
	})
	if err != nil || parsed {
		t.Fatalf("observed untracked GMGN placeholder route event=%#v ok=%v err=%v", event, parsed, err)
	}
}

func TestParseGMGNNormalizesObservedNativeInputForRegisteredToken(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0x37dfb1efd3425c6436a038ab0466e120aab17777")
	if err := parser.Registry().RegisterToken(TokenRegistration{
		Token: token, Protocol: ProtocolFlapTax, Quote: parser.Addresses().WETH, Venue: parser.Addresses().FlapController,
	}); err != nil {
		t.Fatal(err)
	}
	data := common.FromHex("0x000000000000000000000000000000000000000000000000002386f26fc1000000000000000000000000000000000000000000000002e10d27199fea4b7dc09c0000000000000000000000000000000000000000000000000000000000000060000000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000200000000000000000000000000000000000000000000000000000000000000006000000000000000000000000000000000000000000000000000000000000000000000000000000000000000037dfb1efd3425c6436a038ab0466e120aab1777700000000000000000000000037dfb1efd3425c6436a038ab0466e120aab177770000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000140000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000")
	recipient := common.HexToAddress("0xc45a1dc261a4ab0338e7308bb0dfd72465c5c46a")
	event, parsed, err := parser.ParseLog(gethtypes.Log{
		Address: parser.Addresses().GMGNRouter,
		Topics:  []common.Hash{TopicGMGNSwap, addressTopic(recipient), addressTopic(recipient), common.Hash{}},
		Data:    data,
	})
	if err != nil || !parsed {
		t.Fatalf("native GMGN event: ok=%v err=%v", parsed, err)
	}
	swap := event.Data.(VenueSwap)
	if !swap.Buy || swap.Token != token || swap.Quote != parser.Addresses().WETH || len(swap.Routes) != 1 || swap.Routes[0].TokenIn != parser.Addresses().WETH {
		t.Fatalf("unexpected normalized GMGN swap: %#v", swap)
	}
}

func TestParseGMGNIgnoresUntrackedSupportedRouteBeforeAmountValidation(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0x200")
	routes := []gmgnRouteWire{{
		Kind: 6, TokenIn: parser.Addresses().WETH, TokenOut: token,
		Pool: token, Fee: new(big.Int), TickSpacing: new(big.Int),
	}}
	data, err := gmgnSwapEventArgs.Pack(new(big.Int), new(big.Int), routes)
	if err != nil {
		t.Fatal(err)
	}
	recipient := common.HexToAddress("0x100")
	event, parsed, err := parser.ParseLog(gethtypes.Log{
		Address: parser.Addresses().GMGNRouter,
		Topics:  []common.Hash{TopicGMGNSwap, addressTopic(recipient), addressTopic(recipient), common.Hash{}},
		Data:    data,
	})
	if err != nil || parsed {
		t.Fatalf("untracked supported GMGN route event=%#v ok=%v err=%v", event, parsed, err)
	}
}

func TestParseVirtualsVenueBuy(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0x222aeb6973866995f733f61c9ab1ed058986efcb")
	venue := common.HexToAddress("0x8a95c7239680e9c4fd8366ea9320fe2c86056c99")
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: token, Protocol: ProtocolVirtuals, Quote: parser.Addresses().VirtualToken, Venue: venue}); err != nil {
		t.Fatal(err)
	}
	data, err := abiArguments(t, "uint256", "uint256", "uint256", "uint256").Pack(new(big.Int), big.NewInt(200), big.NewInt(100), new(big.Int))
	if err != nil {
		t.Fatal(err)
	}
	event, ok, err := parser.ParseLog(gethtypes.Log{Address: venue, Topics: []common.Hash{TopicVirtualsBuy}, Data: data})
	if err != nil || !ok || event.Protocol != ProtocolVirtuals {
		t.Fatalf("event=%#v ok=%v err=%v", event, ok, err)
	}
	swap := event.Data.(VenueSwap)
	if !swap.Buy || swap.AmountIn.Uint64() != 100 || swap.AmountOut.Uint64() != 200 {
		t.Fatalf("unexpected Virtuals swap: %#v", swap)
	}
}

func TestParseVirtualsPairReserveEvents(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0x1000000000000000000000000000000000000001")
	pair := common.HexToAddress("0x2000000000000000000000000000000000000002")
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: token, Protocol: ProtocolVirtuals, Quote: parser.Addresses().VirtualToken, Venue: pair}); err != nil {
		t.Fatal(err)
	}
	packWords := func(values ...int64) []byte {
		data := make([]byte, len(values)*32)
		for index, value := range values {
			new(big.Int).SetInt64(value).FillBytes(data[index*32 : (index+1)*32])
		}
		return data
	}
	for _, test := range []struct {
		address  common.Address
		topic    common.Hash
		data     []byte
		absolute bool
	}{
		{pair, TopicVirtualsPairMint, packWords(900, 300), true},
		{parser.Addresses().WETHVirtualPair, TopicV2PairSync, packWords(100, 200), true},
	} {
		event, ok, err := parser.ParseLog(gethtypes.Log{Address: test.address, Topics: []common.Hash{test.topic}, Data: test.data})
		if err != nil || !ok || event.Kind != EventVenueState || event.Protocol != ProtocolVirtuals {
			t.Fatalf("topic=%s event=%#v ok=%t err=%v", test.topic, event, ok, err)
		}
		state := event.Data.(VenueStateChange)
		if state.Absolute != test.absolute || state.Venue != test.address {
			t.Fatalf("unexpected state: %#v", state)
		}
	}
	event, ok, err := parser.ParseLog(gethtypes.Log{Address: pair, Topics: []common.Hash{TopicVirtualsPairSwap}, Data: packWords(0, 30, 10, 0)})
	if err != nil || !ok || event.Kind != EventVenueSwap {
		t.Fatalf("Virtuals swap event=%#v ok=%t err=%v", event, ok, err)
	}
	swap := event.Data.(VenueSwap)
	if !swap.Buy || swap.AmountIn.Cmp(big.NewInt(10)) != 0 || swap.AmountOut.Cmp(big.NewInt(30)) != 0 || swap.State == nil {
		t.Fatalf("unexpected Virtuals buy/state: %#v", swap)
	}
}

func TestParseVirtualsPairSell(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0x3000000000000000000000000000000000000003")
	pair := common.HexToAddress("0x4000000000000000000000000000000000000004")
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: token, Protocol: ProtocolVirtuals, Quote: parser.Addresses().VirtualToken, Venue: pair}); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 128)
	big.NewInt(20).FillBytes(data[:32])
	big.NewInt(7).FillBytes(data[96:128])
	event, ok, err := parser.ParseLog(gethtypes.Log{Address: pair, Topics: []common.Hash{TopicVirtualsPairSwap}, Data: data})
	if err != nil || !ok || event.Kind != EventVenueSwap {
		t.Fatalf("event=%#v ok=%t err=%v", event, ok, err)
	}
	swap := event.Data.(VenueSwap)
	if swap.Buy || swap.AmountIn.Cmp(big.NewInt(20)) != 0 || swap.AmountOut.Cmp(big.NewInt(7)) != 0 || swap.State == nil {
		t.Fatalf("unexpected Virtuals sell/state: %#v", swap)
	}
}

func TestFlapGraduationRegistersV2VenueAndParsesState(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0x6c6b6fd36d28715be2d241d36c5521f98e5e7777")
	pair := common.HexToAddress("0x946b3c8932794088cc259b4b5159484904d8b63e")
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: token, Protocol: ProtocolFlapTax, Venue: parser.Addresses().FlapController}); err != nil {
		t.Fatal(err)
	}
	graduationData, err := abiArguments(t, "address", "address", "uint256", "uint256").Pack(token, pair, big.NewInt(900), big.NewInt(300))
	if err != nil {
		t.Fatal(err)
	}
	event, ok, err := parser.ParseLog(gethtypes.Log{Address: parser.Addresses().FlapController, Topics: []common.Hash{TopicFlapGraduated}, Data: graduationData})
	if err != nil || !ok || event.Kind != EventGraduation || event.Protocol != ProtocolFlapTax {
		t.Fatalf("graduation event=%#v ok=%t err=%v", event, ok, err)
	}
	registration, found := parser.Registry().LookupVenue(pair)
	if !found || registration.Token != token || registration.Venue != pair {
		t.Fatalf("graduated venue was not indexed: %#v found=%t", registration, found)
	}
	stateData, err := abiArguments(t, "uint112", "uint112").Pack(big.NewInt(300), big.NewInt(900))
	if err != nil {
		t.Fatal(err)
	}
	event, ok, err = parser.ParseLog(gethtypes.Log{Address: pair, Topics: []common.Hash{TopicV2PairSync}, Data: stateData})
	if err != nil || !ok || event.Kind != EventVenueState || event.Protocol != ProtocolFlapTax || event.Data.(VenueStateChange).Token != token {
		t.Fatalf("Flap V2 state event=%#v ok=%t err=%v", event, ok, err)
	}
	// WETH is token0 for this real pair, so WETH-in/token-out is a buy.
	swapData, err := abiArguments(t, "uint256", "uint256", "uint256", "uint256").Pack(big.NewInt(10), new(big.Int), new(big.Int), big.NewInt(20))
	if err != nil {
		t.Fatal(err)
	}
	event, ok, err = parser.ParseLog(gethtypes.Log{Address: pair, Topics: []common.Hash{TopicVirtualsPairSwap}, Data: swapData})
	if err != nil || !ok || event.Kind != EventVenueSwap {
		t.Fatalf("Flap V2 swap event=%#v ok=%t err=%v", event, ok, err)
	}
	swap := event.Data.(VenueSwap)
	if !swap.Buy || swap.AmountIn.Cmp(big.NewInt(10)) != 0 || swap.AmountOut.Cmp(big.NewInt(20)) != 0 {
		t.Fatalf("unexpected Flap V2 swap: %#v", swap)
	}
}

func TestParseFlapV2NonpayableSellRejectsNativeValue(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0x9500000000000000000000000000000000007777")
	pair := common.HexToAddress("0x9600000000000000000000000000000000000009")
	registration := TokenRegistration{Token: token, Protocol: ProtocolFlapTax, Venue: parser.Addresses().FlapController}
	if err := parser.Registry().RegisterToken(registration); err != nil {
		t.Fatal(err)
	}
	registration.Venue = pair
	if err := parser.Registry().RegisterVenue(registration); err != nil {
		t.Fatal(err)
	}
	data, err := uniswapV2RouterABI.Pack("swapExactTokensForETHSupportingFeeOnTransferTokens", big.NewInt(10), big.NewInt(1), []common.Address{token, parser.Addresses().WETH}, common.HexToAddress("0x1"), big.NewInt(100))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().UniswapV2Router, data, big.NewInt(1)), common.HexToAddress("0x1")); err == nil {
		t.Fatal("V2 nonpayable sell accepted native value")
	}
}

func TestFlapSharedPortalIsNotVenueAndGraduatedVenueSurvivesSnapshot(t *testing.T) {
	registry := NewRegistry()
	token := common.HexToAddress("0x6100000000000000000000000000000000000006")
	portal := DefaultAddressBook().FlapController
	pair := common.HexToAddress("0x6200000000000000000000000000000000000006")
	registration := TokenRegistration{Token: token, Protocol: ProtocolFlapTax, Venue: portal}
	if err := registry.RegisterToken(registration); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.LookupVenue(portal); ok {
		t.Fatal("shared Flap Portal was indexed as a unique token venue")
	}
	registration.Venue = pair
	if err := registry.RegisterVenue(registration); err != nil {
		t.Fatal(err)
	}
	restored, err := NewRegistryFromSnapshot(registry.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := restored.LookupTokenVenue(token); !ok || got != registration {
		t.Fatalf("graduated venue was not restored: %#v ok=%t", got, ok)
	}
	if got, ok := restored.LookupVenue(pair); !ok || got != registration {
		t.Fatalf("reverse venue index was not restored: %#v ok=%t", got, ok)
	}
}

func TestRegistryOverlayCommitRejectsConcurrentTokenVenueConflict(t *testing.T) {
	registry := NewRegistry()
	token := common.HexToAddress("0x7100000000000000000000000000000000000007")
	registration := TokenRegistration{Token: token, Protocol: ProtocolFlapStocks}
	if err := registry.RegisterToken(registration); err != nil {
		t.Fatal(err)
	}
	overlay := NewOverlayRegistry(registry)
	overlayVenue := registration
	overlayVenue.Venue = common.HexToAddress("0x7200000000000000000000000000000000000007")
	if err := overlay.RegisterVenue(overlayVenue); err != nil {
		t.Fatal(err)
	}
	confirmedVenue := registration
	confirmedVenue.Venue = common.HexToAddress("0x7300000000000000000000000000000000000007")
	if err := registry.RegisterVenue(confirmedVenue); err != nil {
		t.Fatal(err)
	}
	if err := registry.commit(overlay); !errors.Is(err, ErrConflictingToken) {
		t.Fatalf("commit error = %v, want %v", err, ErrConflictingToken)
	}
	if got, ok := registry.LookupTokenVenue(token); !ok || got != confirmedVenue {
		t.Fatalf("failed overlay changed confirmed venue: %#v ok=%t", got, ok)
	}
	if _, ok := registry.LookupVenue(overlayVenue.Venue); ok {
		t.Fatal("failed overlay published its conflicting venue")
	}
}

func TestRegistryOverlayCommitRejectsStaleVenueQuote(t *testing.T) {
	registry := NewRegistry()
	token := common.HexToAddress("0x7400000000000000000000000000000000000007")
	base := TokenRegistration{Token: token, Protocol: ProtocolFlapStocks}
	if err := registry.RegisterToken(base); err != nil {
		t.Fatal(err)
	}
	overlay := NewOverlayRegistry(registry)
	venue := base
	venue.Venue = common.HexToAddress("0x7500000000000000000000000000000000000007")
	if err := overlay.RegisterVenue(venue); err != nil {
		t.Fatal(err)
	}
	enriched := base
	enriched.Quote = common.HexToAddress("0x7600000000000000000000000000000000000007")
	if err := registry.RegisterToken(enriched); err != nil {
		t.Fatal(err)
	}
	if err := registry.commit(overlay); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("commit error = %v, want %v", err, ErrInvalidRegistration)
	}
	if _, ok := registry.LookupVenue(venue.Venue); ok {
		t.Fatal("stale venue quote was published")
	}
}

func TestRegisterTokenVenueConflictIsAtomic(t *testing.T) {
	registry := NewRegistry()
	venue := common.HexToAddress("0x8100000000000000000000000000000000000008")
	first := TokenRegistration{
		Token:    common.HexToAddress("0x8200000000000000000000000000000000000008"),
		Protocol: ProtocolVirtuals, Quote: DefaultAddressBook().VirtualToken, Venue: venue,
	}
	if err := registry.RegisterToken(first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Token = common.HexToAddress("0x8300000000000000000000000000000000000008")
	if err := registry.RegisterToken(second); !errors.Is(err, ErrConflictingToken) {
		t.Fatalf("conflicting token error = %v", err)
	}
	if _, ok := registry.LookupToken(second.Token); ok {
		t.Fatal("failed venue registration polluted the token index")
	}
	if got, ok := registry.LookupVenue(venue); !ok || got != first {
		t.Fatalf("venue owner changed after failed registration: %#v ok=%t", got, ok)
	}
}
