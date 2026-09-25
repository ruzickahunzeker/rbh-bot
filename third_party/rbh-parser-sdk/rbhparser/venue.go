package rbhparser

import (
	"bytes"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

type gmgnRouteWire struct {
	Kind        uint8          `abi:"kind"`
	TokenIn     common.Address `abi:"tokenIn"`
	TokenOut    common.Address `abi:"tokenOut"`
	Pool        common.Address `abi:"pool"`
	Fee         *big.Int       `abi:"fee"`
	TickSpacing *big.Int       `abi:"tickSpacing"`
	Hook        common.Address `abi:"hook"`
	HookData    []byte         `abi:"hookData"`
	Router      common.Address `abi:"router"`
	PoolID      common.Hash    `abi:"poolId"`
}

func decodeFlapV2Swap(log gethtypes.Log, registration TokenRegistration, weth common.Address) (VenueSwap, error) {
	state, err := decodeVirtualsPairSwapState(log)
	if err != nil || registration.Venue != log.Address || registration.Token == (common.Address{}) || weth == (common.Address{}) {
		return VenueSwap{}, ErrMalformedLog
	}
	state.Token = registration.Token
	swap := VenueSwap{Token: registration.Token, Quote: registration.Quote, Venue: registration.Venue, State: &state}
	tokenIs0 := bytes.Compare(registration.Token.Bytes(), weth.Bytes()) < 0
	if tokenIs0 {
		switch {
		case state.Amount0Out.Sign() > 0 && state.Amount1In.Sign() > 0 && state.Amount0In.Sign() == 0 && state.Amount1Out.Sign() == 0:
			swap.Buy, swap.AmountIn, swap.AmountOut = true, state.Amount1In, state.Amount0Out
		case state.Amount0In.Sign() > 0 && state.Amount1Out.Sign() > 0 && state.Amount0Out.Sign() == 0 && state.Amount1In.Sign() == 0:
			swap.AmountIn, swap.AmountOut = state.Amount0In, state.Amount1Out
		default:
			return VenueSwap{}, ErrMalformedLog
		}
	} else {
		switch {
		case state.Amount1Out.Sign() > 0 && state.Amount0In.Sign() > 0 && state.Amount1In.Sign() == 0 && state.Amount0Out.Sign() == 0:
			swap.Buy, swap.AmountIn, swap.AmountOut = true, state.Amount0In, state.Amount1Out
		case state.Amount1In.Sign() > 0 && state.Amount0Out.Sign() > 0 && state.Amount1Out.Sign() == 0 && state.Amount0In.Sign() == 0:
			swap.AmountIn, swap.AmountOut = state.Amount1In, state.Amount0Out
		default:
			return VenueSwap{}, ErrMalformedLog
		}
	}
	return swap, nil
}

type gmgnSwapWire struct {
	AmountIn  *big.Int        `abi:"amountIn"`
	AmountOut *big.Int        `abi:"amountOut"`
	Routes    []gmgnRouteWire `abi:"routes"`
}

type gmgnCallWire struct {
	Routes       []gmgnRouteWire `abi:"routes"`
	Recipient    common.Address  `abi:"recipient"`
	AmountIn     *big.Int        `abi:"amountIn"`
	MinAmountOut *big.Int        `abi:"minAmountOut"`
	Deadline     *big.Int        `abi:"deadline"`
}

type v2BuyCallWire struct {
	MinAmountOut *big.Int         `abi:"amountOutMin"`
	Path         []common.Address `abi:"path"`
	Recipient    common.Address   `abi:"to"`
	Deadline     *big.Int         `abi:"deadline"`
}

type v2SellCallWire struct {
	AmountIn     *big.Int         `abi:"amountIn"`
	MinAmountOut *big.Int         `abi:"amountOutMin"`
	Path         []common.Address `abi:"path"`
	Recipient    common.Address   `abi:"to"`
	Deadline     *big.Int         `abi:"deadline"`
}

const gmgnRouterABIJSON = `[{"type":"function","name":"swap","stateMutability":"payable","inputs":[{"name":"routes","type":"tuple[]","components":[{"name":"kind","type":"uint8"},{"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},{"name":"pool","type":"address"},{"name":"fee","type":"uint24"},{"name":"tickSpacing","type":"int24"},{"name":"hook","type":"address"},{"name":"hookData","type":"bytes"},{"name":"router","type":"address"},{"name":"poolId","type":"bytes32"}]},{"name":"recipient","type":"address"},{"name":"amountIn","type":"uint256"},{"name":"minAmountOut","type":"uint256"},{"name":"deadline","type":"uint256"}],"outputs":[]}]`
const uniswapV2RouterABIJSON = `[{"type":"function","name":"swapExactETHForTokensSupportingFeeOnTransferTokens","stateMutability":"payable","inputs":[{"name":"amountOutMin","type":"uint256"},{"name":"path","type":"address[]"},{"name":"to","type":"address"},{"name":"deadline","type":"uint256"}],"outputs":[]},{"type":"function","name":"swapExactTokensForETHSupportingFeeOnTransferTokens","stateMutability":"nonpayable","inputs":[{"name":"amountIn","type":"uint256"},{"name":"amountOutMin","type":"uint256"},{"name":"path","type":"address[]"},{"name":"to","type":"address"},{"name":"deadline","type":"uint256"}],"outputs":[]}]`

var (
	gmgnSwapEventArgs  = newGMGNSwapEventArgs()
	gmgnRouterABI      = mustABI(gmgnRouterABIJSON)
	uniswapV2RouterABI = mustABI(uniswapV2RouterABIJSON)
)

func (p *Parser) parseFlapV2Intent(call intentCall, selector [4]byte) (TransactionIntent, bool, error) {
	var token common.Address
	var amountIn, minOut, deadline *big.Int
	var recipient common.Address
	var path []common.Address
	kind := IntentUnknown
	switch selector {
	case selectorV2BuyFeeOnTransfer:
		method := uniswapV2RouterABI.Methods["swapExactETHForTokensSupportingFeeOnTransferTokens"]
		values, err := method.Inputs.Unpack(call.data[4:])
		var wire v2BuyCallWire
		if err != nil || method.Inputs.Copy(&wire, values) != nil {
			return TransactionIntent{}, true, malformedCalldata("invalid V2 fee-on-transfer buy")
		}
		amountIn, minOut, deadline, recipient, path, kind = call.value, wire.MinAmountOut, wire.Deadline, wire.Recipient, wire.Path, IntentBuy
		if len(path) == 2 {
			token = path[1]
		}
	case selectorV2SellFeeOnTransfer:
		method := uniswapV2RouterABI.Methods["swapExactTokensForETHSupportingFeeOnTransferTokens"]
		values, err := method.Inputs.Unpack(call.data[4:])
		var wire v2SellCallWire
		if err != nil || method.Inputs.Copy(&wire, values) != nil {
			return TransactionIntent{}, true, malformedCalldata("invalid V2 fee-on-transfer sell")
		}
		amountIn, minOut, deadline, recipient, path, kind = wire.AmountIn, wire.MinAmountOut, wire.Deadline, wire.Recipient, wire.Path, IntentSell
		if call.value != nil && call.value.Sign() != 0 {
			return TransactionIntent{}, true, malformedCalldata("V2 fee-on-transfer sell cannot carry native value")
		}
		if len(path) == 2 {
			token = path[0]
		}
	default:
		return TransactionIntent{}, false, nil
	}
	if len(path) != 2 {
		return TransactionIntent{}, false, nil
	}
	if kind == IntentBuy && path[0] != p.addresses.WETH || kind == IntentSell && path[1] != p.addresses.WETH {
		return TransactionIntent{}, false, nil
	}
	registration, ok := p.registry.LookupTokenVenue(token)
	if !ok || (registration.Protocol != ProtocolFlapTax && registration.Protocol != ProtocolFlapStocks) || registration.Venue == (common.Address{}) {
		return TransactionIntent{}, false, nil
	}
	venue, ok := p.registry.LookupVenue(registration.Venue)
	if !ok || venue.Token != token || venue.Protocol != registration.Protocol {
		return TransactionIntent{}, false, nil
	}
	if token == (common.Address{}) || recipient == (common.Address{}) || amountIn == nil || amountIn.Sign() <= 0 || minOut == nil || minOut.Sign() <= 0 || deadline == nil || deadline.Sign() <= 0 {
		return TransactionIntent{}, true, malformedCalldata("invalid V2 fee-on-transfer values")
	}
	intent := baseIntent(call, registration.Protocol, kind)
	intent.Token, intent.Quote, intent.Venue, intent.Recipient = token, registration.Quote, registration.Venue, recipient
	intent.CurrencyIn, intent.CurrencyOut = token, common.Address{}
	if kind == IntentBuy {
		intent.CurrencyIn, intent.CurrencyOut = common.Address{}, token
	}
	intent.AmountIn, intent.MinimumAmountOut, intent.Deadline = new(big.Int).Set(amountIn), new(big.Int).Set(minOut), new(big.Int).Set(deadline)
	return intent, true, nil
}

func newGMGNSwapEventArgs() abi.Arguments {
	uintType, err := abi.NewType("uint256", "", nil)
	if err != nil {
		panic(err)
	}
	routesType, err := abi.NewType("tuple[]", "", []abi.ArgumentMarshaling{
		{Name: "kind", Type: "uint8"}, {Name: "tokenIn", Type: "address"}, {Name: "tokenOut", Type: "address"},
		{Name: "pool", Type: "address"}, {Name: "fee", Type: "uint24"}, {Name: "tickSpacing", Type: "int24"},
		{Name: "hook", Type: "address"}, {Name: "hookData", Type: "bytes"}, {Name: "router", Type: "address"}, {Name: "poolId", Type: "bytes32"},
	})
	if err != nil {
		panic(err)
	}
	return abi.Arguments{{Name: "amountIn", Type: uintType}, {Name: "amountOut", Type: uintType}, {Name: "routes", Type: routesType}}
}

func decodeVaroBuy(log gethtypes.Log, registration TokenRegistration) (VenueSwap, error) {
	if err := validateLog(log, 3, 96); err != nil {
		return VenueSwap{}, err
	}
	pool, err := topicAddress(log.Topics[1])
	if err != nil || pool == (common.Address{}) || pool != registration.Venue {
		return VenueSwap{}, ErrMalformedLog
	}
	token, err := topicAddress(log.Topics[2])
	if err != nil || token != registration.Token {
		return VenueSwap{}, ErrMalformedLog
	}
	amountIn, err := uintWord(log.Data, 0, 256)
	if err != nil || amountIn.Sign() <= 0 {
		return VenueSwap{}, ErrMalformedLog
	}
	amountOut, err := uintWord(log.Data, 1, 256)
	if err != nil || amountOut.Sign() <= 0 {
		return VenueSwap{}, ErrMalformedLog
	}
	recipient, err := addressWord(log.Data, 2)
	if err != nil {
		return VenueSwap{}, err
	}
	return VenueSwap{Token: token, Quote: registration.Quote, Venue: pool, Recipient: recipient, Buy: true, AmountIn: amountIn, AmountOut: amountOut}, nil
}

func decodeVirtualsBuy(log gethtypes.Log, registration TokenRegistration) (VenueSwap, error) {
	if err := validateLog(log, 1, 128); err != nil || log.Address != registration.Venue {
		return VenueSwap{}, ErrMalformedLog
	}
	state, err := decodeVirtualsPairSwapState(log)
	if err != nil {
		return VenueSwap{}, err
	}
	swap := VenueSwap{Token: registration.Token, Quote: registration.Quote, Venue: registration.Venue, State: &state}
	switch {
	case state.Amount0In.Sign() == 0 && state.Amount0Out.Sign() > 0 && state.Amount1In.Sign() > 0 && state.Amount1Out.Sign() == 0:
		swap.Buy, swap.AmountIn, swap.AmountOut = true, state.Amount1In, state.Amount0Out
	case state.Amount0In.Sign() > 0 && state.Amount0Out.Sign() == 0 && state.Amount1In.Sign() == 0 && state.Amount1Out.Sign() > 0:
		swap.AmountIn, swap.AmountOut = state.Amount0In, state.Amount1Out
	default:
		return VenueSwap{}, ErrMalformedLog
	}
	return swap, nil
}

func decodeVirtualsAbsoluteState(log gethtypes.Log, bits int) (VenueStateChange, error) {
	if err := validateLog(log, 1, 64); err != nil {
		return VenueStateChange{}, err
	}
	reserve0, err := uintWord(log.Data, 0, bits)
	if err != nil {
		return VenueStateChange{}, err
	}
	reserve1, err := uintWord(log.Data, 1, bits)
	if err != nil || reserve0.Sign() <= 0 || reserve1.Sign() <= 0 {
		return VenueStateChange{}, ErrMalformedLog
	}
	return VenueStateChange{Venue: log.Address, Reserve0: reserve0, Reserve1: reserve1, Absolute: true}, nil
}

func decodeVirtualsPairSwapState(log gethtypes.Log) (VenueStateChange, error) {
	if err := validateLog(log, 1, 128); err != nil {
		return VenueStateChange{}, err
	}
	values := make([]*big.Int, 4)
	for index := range values {
		value, err := uintWord(log.Data, index, 256)
		if err != nil {
			return VenueStateChange{}, err
		}
		values[index] = value
	}
	if values[0].Sign() == 0 && values[1].Sign() == 0 && values[2].Sign() == 0 && values[3].Sign() == 0 {
		return VenueStateChange{}, ErrMalformedLog
	}
	return VenueStateChange{
		Venue: log.Address, Amount0In: values[0], Amount0Out: values[1],
		Amount1In: values[2], Amount1Out: values[3],
	}, nil
}

func (p *Parser) decodeGMGNSwap(log gethtypes.Log) (VenueSwap, Protocol, bool, error) {
	if len(log.Topics) != 4 || len(log.Data) < 4*32 || len(log.Data) > maxLogDataBytes {
		return VenueSwap{}, ProtocolUnknown, false, ErrMalformedLog
	}
	values, err := gmgnSwapEventArgs.Unpack(log.Data)
	if err != nil {
		return VenueSwap{}, ProtocolUnknown, false, ErrMalformedLog
	}
	var wire gmgnSwapWire
	if err := gmgnSwapEventArgs.Copy(&wire, values); err != nil || len(wire.Routes) == 0 || len(wire.Routes) > maxNestedCalls {
		return VenueSwap{}, ProtocolUnknown, false, ErrMalformedLog
	}
	if !gmgnRoutesSupported(wire.Routes) {
		return VenueSwap{}, ProtocolUnknown, false, nil
	}
	registration, buy, found := p.classifyGMGNWireRoutes(wire.Routes)
	if !found {
		return VenueSwap{}, ProtocolUnknown, false, nil
	}
	routes, err := decodeGMGNRoutes(normalizeGMGNNativeInput(wire.Routes, buy, p.addresses.WETH))
	if err != nil {
		return VenueSwap{}, ProtocolUnknown, false, err
	}
	if wire.AmountIn == nil || wire.AmountIn.Sign() <= 0 || wire.AmountOut == nil || wire.AmountOut.Sign() <= 0 {
		return VenueSwap{}, ProtocolUnknown, false, ErrMalformedLog
	}
	first, last := routes[0], routes[len(routes)-1]
	recipient, err := topicAddress(log.Topics[2])
	if err != nil {
		return VenueSwap{}, ProtocolUnknown, false, err
	}
	quote := last.TokenOut
	if buy {
		quote = first.TokenIn
	}
	return VenueSwap{Token: registration.Token, Quote: quote, Venue: registration.Venue, Recipient: recipient, Buy: buy, AmountIn: wire.AmountIn, AmountOut: wire.AmountOut, Routes: routes}, registration.Protocol, true, nil
}

func (p *Parser) parseGMGNIntent(call intentCall) (TransactionIntent, bool, error) {
	if len(call.data) < 4 || len(call.data)-4 > maxLogDataBytes {
		return TransactionIntent{}, true, malformedCalldata("invalid GMGN swap payload")
	}
	method := gmgnRouterABI.Methods["swap"]
	values, err := method.Inputs.Unpack(call.data[4:])
	if err != nil {
		return TransactionIntent{}, true, malformedCalldata("invalid GMGN swap ABI")
	}
	var wire gmgnCallWire
	if err := method.Inputs.Copy(&wire, values); err != nil || len(wire.Routes) == 0 || len(wire.Routes) > maxNestedCalls {
		return TransactionIntent{}, true, malformedCalldata("invalid GMGN swap values")
	}
	if !gmgnRoutesSupported(wire.Routes) {
		return TransactionIntent{}, false, nil
	}
	registration, buy, found := p.classifyGMGNWireRoutes(wire.Routes)
	if !found {
		return TransactionIntent{}, false, nil
	}
	nativeInput := buy && wire.Routes[0].TokenIn == (common.Address{})
	routes, err := decodeGMGNRoutes(normalizeGMGNNativeInput(wire.Routes, buy, p.addresses.WETH))
	if err != nil {
		return TransactionIntent{}, true, malformedCalldata("invalid GMGN routes")
	}
	if wire.Recipient == (common.Address{}) || wire.AmountIn == nil || wire.AmountIn.Sign() <= 0 || wire.MinAmountOut == nil || wire.MinAmountOut.Sign() <= 0 || wire.Deadline == nil || wire.Deadline.Sign() <= 0 {
		return TransactionIntent{}, true, malformedCalldata("invalid GMGN swap values")
	}
	first, last := routes[0], routes[len(routes)-1]
	if buy {
		value := call.value
		if value == nil {
			value = new(big.Int)
		}
		weth := p.addresses.WETH
		switch {
		case value.Sign() > 0 && (first.TokenIn != weth || value.Cmp(wire.AmountIn) < 0):
			return TransactionIntent{}, true, malformedCalldata("GMGN native buy funding is inconsistent")
		case value.Sign() == 0 && nativeInput:
			return TransactionIntent{}, true, malformedCalldata("GMGN ERC20 buy has no input token")
		}
	}
	kind := IntentSell
	currencyIn, currencyOut := registration.Token, last.TokenOut
	if buy {
		kind, currencyIn, currencyOut = IntentBuy, first.TokenIn, registration.Token
	}
	intent := baseIntent(call, registration.Protocol, kind)
	intent.Recipient, intent.Token, intent.Quote, intent.Venue = wire.Recipient, registration.Token, currencyIn, registration.Venue
	if !buy {
		intent.Quote = currencyOut
	}
	intent.CurrencyIn, intent.CurrencyOut = currencyIn, currencyOut
	intent.AmountIn, intent.MinimumAmountOut, intent.Deadline = new(big.Int).Set(wire.AmountIn), new(big.Int).Set(wire.MinAmountOut), new(big.Int).Set(wire.Deadline)
	intent.Routes = routes
	return intent, true, nil
}

func normalizeGMGNNativeInput(routes []gmgnRouteWire, buy bool, weth common.Address) []gmgnRouteWire {
	if !buy || len(routes) == 0 || routes[0].TokenIn != (common.Address{}) || weth == (common.Address{}) {
		return routes
	}
	normalized := append([]gmgnRouteWire(nil), routes...)
	normalized[0].TokenIn = weth
	return normalized
}

func decodeGMGNRoutes(wire []gmgnRouteWire) ([]VenueRoute, error) {
	if len(wire) == 0 || len(wire) > maxNestedCalls {
		return nil, ErrMalformedLog
	}
	routes := make([]VenueRoute, len(wire))
	for i, route := range wire {
		if (route.Kind != 1 && route.Kind != 6) || route.TokenIn == (common.Address{}) || route.TokenOut == (common.Address{}) || route.TokenIn == route.TokenOut || route.Pool == (common.Address{}) || route.Fee == nil || !route.Fee.IsUint64() || route.Fee.Uint64() > 0xffffff || route.TickSpacing == nil || !route.TickSpacing.IsInt64() || route.TickSpacing.Int64() < -8388608 || route.TickSpacing.Int64() > 8388607 || len(route.HookData) > maxLogDataBytes {
			return nil, ErrMalformedLog
		}
		routes[i] = VenueRoute{Kind: route.Kind, TokenIn: route.TokenIn, TokenOut: route.TokenOut, Pool: route.Pool, Fee: uint32(route.Fee.Uint64()), TickSpacing: int32(route.TickSpacing.Int64()), Hook: route.Hook, HookData: append([]byte(nil), route.HookData...), Router: route.Router, PoolID: route.PoolID}
		if i > 0 && routes[i-1].TokenOut != routes[i].TokenIn {
			return nil, ErrMalformedLog
		}
	}
	return routes, nil
}

func gmgnRoutesSupported(routes []gmgnRouteWire) bool {
	if len(routes) == 0 || len(routes) > maxNestedCalls {
		return false
	}
	for _, route := range routes {
		if route.Kind != 1 && route.Kind != 6 {
			return false
		}
	}
	return true
}

func (p *Parser) classifyGMGNWireRoutes(routes []gmgnRouteWire) (TokenRegistration, bool, bool) {
	if len(routes) == 0 {
		return TokenRegistration{}, false, false
	}
	first, last := routes[0], routes[len(routes)-1]
	if candidate, ok := p.registry.LookupToken(last.TokenOut); ok && (candidate.Protocol == ProtocolFlapTax || candidate.Protocol == ProtocolFlapStocks) && first.TokenIn != candidate.Token && last.Kind == 6 && last.Pool == candidate.Token {
		return candidate, true, true
	}
	if candidate, ok := p.registry.LookupToken(first.TokenIn); ok && (candidate.Protocol == ProtocolFlapTax || candidate.Protocol == ProtocolFlapStocks) && last.TokenOut != candidate.Token && first.Kind == 6 && first.Pool == candidate.Token {
		return candidate, false, true
	}
	return TokenRegistration{}, false, false
}
