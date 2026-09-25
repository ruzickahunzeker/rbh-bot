package rbhparser

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

func decodePonsLaunch(log gethtypes.Log) (Launch, error) {
	if err := validateLog(log, 4, 96); err != nil {
		return Launch{}, err
	}
	token, err := topicAddress(log.Topics[1])
	if err != nil {
		return Launch{}, err
	}
	curve, err := topicAddress(log.Topics[2])
	if err != nil {
		return Launch{}, err
	}
	if curve == (common.Address{}) {
		return Launch{}, ErrMalformedLog
	}
	creator, err := topicAddress(log.Topics[3])
	if err != nil {
		return Launch{}, err
	}
	quote, err := addressWord(log.Data, 0)
	if err != nil {
		return Launch{}, err
	}
	configID, err := uintWord(log.Data, 1, 256)
	if err != nil {
		return Launch{}, err
	}
	threshold, err := uintWord(log.Data, 2, 256)
	if err != nil {
		return Launch{}, err
	}
	return Launch{Protocol: ProtocolPonsV2, Token: token, Quote: quote, Creator: creator, Curve: curve, LaunchConfigID: configID, GraduationThreshold: threshold}, nil
}

func decodeLetsCashLaunch(log gethtypes.Log) (Launch, error) {
	if err := validateLog(log, 4, 160); err != nil {
		return Launch{}, err
	}
	token, err := topicAddress(log.Topics[1])
	if err != nil {
		return Launch{}, err
	}
	creator, err := topicAddress(log.Topics[2])
	if err != nil {
		return Launch{}, err
	}
	if log.Topics[3] == (common.Hash{}) {
		return Launch{}, ErrMalformedLog
	}
	configID, err := uintWord(log.Data, 0, 256)
	if err != nil {
		return Launch{}, err
	}
	hook, err := addressWord(log.Data, 3)
	if err != nil || hook == (common.Address{}) {
		return Launch{}, ErrMalformedLog
	}
	creatorWord, err := addressWord(log.Data, 4)
	if err != nil || creatorWord != creator {
		return Launch{}, ErrMalformedLog
	}
	return Launch{
		Protocol: ProtocolLetsCash, Token: token, Creator: creator,
		PoolID: log.Topics[3], PoolOrHook: hook, LaunchConfigID: configID,
	}, nil
}

func decodeLetsCashFeePolicy(log gethtypes.Log) (FeePolicy, error) {
	if err := validateLog(log, 3, 160); err != nil {
		return FeePolicy{}, err
	}
	if log.Topics[1] == (common.Hash{}) {
		return FeePolicy{}, ErrMalformedLog
	}
	rate, err := uintWord(log.Data, 2, 32)
	if err != nil || !rate.IsUint64() || rate.Uint64() > 1_000_000 {
		return FeePolicy{}, ErrMalformedLog
	}
	policy := FeePolicy{PoolID: log.Topics[1], Rate: uint32(rate.Uint64()), Denominator: 1_000_000}
	policy.BuyFeeBPS = policy.BPS()
	policy.SellFeeBPS = policy.BuyFeeBPS
	return policy, nil
}

func decodeFlapCreated(log gethtypes.Log) (Launch, error) {
	if len(log.Topics) != 1 || len(log.Data) > maxLogDataBytes {
		return Launch{}, ErrMalformedLog
	}
	values, err := flapEventABI.Events["TokenCreated"].Inputs.Unpack(log.Data)
	if err != nil || len(values) != 7 {
		return Launch{}, ErrMalformedLog
	}
	ts, ok0 := values[0].(*big.Int)
	creator, ok1 := values[1].(common.Address)
	token, ok2 := values[3].(common.Address)
	name, ok3 := values[4].(string)
	symbol, ok4 := values[5].(string)
	meta, ok5 := values[6].(string)
	if !ok0 || !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ts.IsUint64() || token == (common.Address{}) {
		return Launch{}, ErrMalformedLog
	}
	return Launch{Token: token, Creator: creator, Name: name, Symbol: symbol, MetadataURI: meta, DeployedAt: ts.Uint64()}, nil
}

func decodeFlapTokenAndUint(log gethtypes.Log, bits int) (common.Address, *big.Int, error) {
	if err := validateLog(log, 1, 64); err != nil {
		return common.Address{}, nil, err
	}
	token, err := addressWord(log.Data, 0)
	if err != nil {
		return common.Address{}, nil, err
	}
	value, err := uintWord(log.Data, 1, bits)
	if err != nil {
		return common.Address{}, nil, err
	}
	return token, value, nil
}

func decodeFlapTaxPolicy(log gethtypes.Log, asymmetric bool) (FeePolicy, error) {
	want := 64
	if asymmetric {
		want = 96
	}
	if err := validateLog(log, 1, want); err != nil {
		return FeePolicy{}, err
	}
	token, err := addressWord(log.Data, 0)
	if err != nil {
		return FeePolicy{}, err
	}
	buy, err := uintWord(log.Data, 1, 256)
	if err != nil || !buy.IsUint64() || buy.Uint64() > uint64(^uint32(0)) {
		return FeePolicy{}, ErrMalformedLog
	}
	sell := buy
	if asymmetric {
		sell, err = uintWord(log.Data, 2, 256)
		if err != nil || !sell.IsUint64() || sell.Uint64() > uint64(^uint32(0)) {
			return FeePolicy{}, ErrMalformedLog
		}
	}
	return FeePolicy{Token: token, Rate: uint32(buy.Uint64()), Denominator: 10_000, BuyFeeBPS: uint32(buy.Uint64()), SellFeeBPS: uint32(sell.Uint64())}, nil
}

func decodeFlapGraduation(log gethtypes.Log) (Graduation, error) {
	if err := validateLog(log, 1, 128); err != nil {
		return Graduation{}, err
	}
	token, err := addressWord(log.Data, 0)
	if err != nil {
		return Graduation{}, err
	}
	venue, err := addressWord(log.Data, 1)
	if err != nil || venue == (common.Address{}) {
		return Graduation{}, ErrMalformedLog
	}
	tokenAmount, err := uintWord(log.Data, 2, 256)
	if err != nil {
		return Graduation{}, err
	}
	quoteAmount, err := uintWord(log.Data, 3, 256)
	if err != nil {
		return Graduation{}, err
	}
	return Graduation{Token: token, Venue: venue, TokenAmount: tokenAmount, QuoteAmount: quoteAmount}, nil
}

func decodeVaroLaunch(log gethtypes.Log) (Launch, error) {
	if len(log.Topics) != 4 || len(log.Data) < 12*32 || len(log.Data) > maxLogDataBytes {
		return Launch{}, ErrMalformedLog
	}
	token, err := topicAddress(log.Topics[2])
	if err != nil {
		return Launch{}, err
	}
	creator, err := topicAddress(log.Topics[3])
	if err != nil {
		return Launch{}, err
	}
	venue, err := addressWord(log.Data, 1)
	if err != nil || venue == (common.Address{}) {
		return Launch{}, ErrMalformedLog
	}
	quote, err := addressWord(log.Data, 2)
	if err != nil || quote == (common.Address{}) || quote == token {
		return Launch{}, ErrMalformedLog
	}
	protocolFee, err := uintWord(log.Data, 11, 16)
	if err != nil || !protocolFee.IsUint64() || protocolFee.Uint64() > 3_000 {
		return Launch{}, ErrMalformedLog
	}
	feeBPS := uint32(protocolFee.Uint64())
	return Launch{Protocol: ProtocolVaro, Token: token, Quote: quote, Creator: creator, Venue: venue, BuyFeeBPS: feeBPS, SellFeeBPS: feeBPS, FeesVerified: true}, nil
}

func decodeVaroFeePolicy(log gethtypes.Log, registration TokenRegistration) (FeePolicy, error) {
	if err := validateLog(log, 3, 64); err != nil {
		return FeePolicy{}, err
	}
	token, err := topicAddress(log.Topics[1])
	if err != nil || token != registration.Token {
		return FeePolicy{}, ErrMalformedLog
	}
	// The second indexed address identifies the launch protocol whose fee
	// changed. It is not the token's trading venue. The fixed launchpad emitter
	// and the already-registered token establish the authority for this update.
	launchProtocol, err := topicAddress(log.Topics[2])
	if err != nil || launchProtocol == (common.Address{}) {
		return FeePolicy{}, ErrMalformedLog
	}
	fee, err := uintWord(log.Data, 1, 16)
	if err != nil || !fee.IsUint64() || fee.Uint64() > 3_000 {
		return FeePolicy{}, ErrMalformedLog
	}
	feeBPS := uint32(fee.Uint64())
	return FeePolicy{Token: token, Rate: feeBPS, Denominator: 10_000, BuyFeeBPS: feeBPS, SellFeeBPS: feeBPS}, nil
}

func decodeVirtualsLaunch(log gethtypes.Log, quote common.Address, launched bool) (Launch, error) {
	dataBytes := 224
	paramsOffset := 2
	if launched {
		dataBytes = 256
		paramsOffset = 3
	}
	if err := validateLog(log, 3, dataBytes); err != nil {
		return Launch{}, err
	}
	token, err := topicAddress(log.Topics[1])
	if err != nil {
		return Launch{}, err
	}
	venue, err := topicAddress(log.Topics[2])
	if err != nil || venue == (common.Address{}) || quote == (common.Address{}) || token == quote {
		return Launch{}, ErrMalformedLog
	}
	launchMode, err := uintWord(log.Data, paramsOffset, 8)
	if err != nil || !launchMode.IsUint64() {
		return Launch{}, ErrMalformedLog
	}
	airdropBPS, err := uintWord(log.Data, paramsOffset+1, 16)
	if err != nil || !airdropBPS.IsUint64() {
		return Launch{}, ErrMalformedLog
	}
	needACF, err := uintWord(log.Data, paramsOffset+2, 8)
	if err != nil || !needACF.IsUint64() || needACF.Uint64() > 1 {
		return Launch{}, ErrMalformedLog
	}
	antiSniper, err := uintWord(log.Data, paramsOffset+3, 8)
	if err != nil || !antiSniper.IsUint64() || antiSniper.Uint64() > 5 {
		return Launch{}, ErrMalformedLog
	}
	project60Days, err := uintWord(log.Data, paramsOffset+4, 8)
	if err != nil || !project60Days.IsUint64() || project60Days.Uint64() > 1 {
		return Launch{}, ErrMalformedLog
	}
	return Launch{
		Protocol: ProtocolVirtuals, Token: token, Quote: quote, Venue: venue, PreLaunch: !launched,
		LaunchMode: uint8(launchMode.Uint64()), AirdropBPS: uint16(airdropBPS.Uint64()), NeedACF: needACF.Sign() != 0,
		AntiSniperTaxType: uint8(antiSniper.Uint64()), IsProject60Days: project60Days.Sign() != 0,
	}, nil
}

func decodeVirtualsFeePolicy(log gethtypes.Log) (FeePolicy, error) {
	if err := validateLog(log, 1, 128); err != nil {
		return FeePolicy{}, err
	}
	buy, err := uintWord(log.Data, 1, 16)
	if err != nil || !buy.IsUint64() {
		return FeePolicy{}, ErrMalformedLog
	}
	sell, err := uintWord(log.Data, 3, 16)
	if err != nil || !sell.IsUint64() {
		return FeePolicy{}, ErrMalformedLog
	}
	return FeePolicy{Token: log.Address, Rate: uint32(buy.Uint64()), Denominator: 10_000, BuyFeeBPS: uint32(buy.Uint64()), SellFeeBPS: uint32(sell.Uint64()), EffectiveAt: log.BlockNumber}, nil
}

func decodeLongLaunch(log gethtypes.Log) (Launch, error) {
	if len(log.Topics) != 4 || len(log.Data) > maxLogDataBytes {
		return Launch{}, ErrMalformedLog
	}
	poolOrHook, err := topicAddress(log.Topics[1])
	if err != nil {
		return Launch{}, err
	}
	token, err := topicAddress(log.Topics[2])
	if err != nil {
		return Launch{}, err
	}
	quote, err := topicAddress(log.Topics[3])
	if err != nil {
		return Launch{}, err
	}
	values, err := longEventABI.Events["LaunchCreated"].Inputs.NonIndexed().Unpack(log.Data)
	if err != nil || len(values) != 6 {
		return Launch{}, ErrMalformedLog
	}
	poolInitializer, ok0 := values[0].(common.Address)
	creator, ok1 := values[1].(common.Address)
	ticker, ok2 := values[2].([32]byte)
	deployedAt, ok3 := values[3].(*big.Int)
	reservedUntil, ok4 := values[4].(*big.Int)
	normalizedTicker, ok5 := values[5].(string)
	if !ok0 || !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !deployedAt.IsUint64() || !reservedUntil.IsUint64() || deployedAt.BitLen() > 48 || reservedUntil.BitLen() > 48 {
		return Launch{}, ErrMalformedLog
	}
	return Launch{Protocol: ProtocolLong, Token: token, Quote: quote, Creator: creator, PoolOrHook: poolOrHook, PoolInitializer: poolInitializer, TickerKey: ticker, DeployedAt: deployedAt.Uint64(), ReservedUntil: reservedUntil.Uint64(), NormalizedTicker: normalizedTicker}, nil
}

func decodeO1Launch(log gethtypes.Log) (Launch, error) {
	if err := validateLog(log, 4, 96); err != nil {
		return Launch{}, err
	}
	token, err := topicAddress(log.Topics[1])
	if err != nil {
		return Launch{}, err
	}
	creator, err := topicAddress(log.Topics[3])
	if err != nil {
		return Launch{}, err
	}
	quote, err := addressWord(log.Data, 0)
	if err != nil {
		return Launch{}, err
	}
	supply, err := uintWord(log.Data, 1, 256)
	if err != nil {
		return Launch{}, err
	}
	tickSpacing, err := int24Word(log.Data, 2)
	if err != nil || tickSpacing <= 0 {
		return Launch{}, ErrMalformedLog
	}
	if log.Topics[2] == (common.Hash{}) {
		return Launch{}, ErrMalformedLog
	}
	return Launch{Protocol: ProtocolO1, Token: token, Quote: quote, Creator: creator, PoolID: log.Topics[2], LaunchSupply: supply, TickSpacing: tickSpacing}, nil
}

func decodeO1FeePolicy(log gethtypes.Log) (FeePolicy, error) {
	if err := validateLog(log, 2, 96); err != nil {
		return FeePolicy{}, err
	}
	base, err := uintWord(log.Data, 0, 16)
	if err != nil {
		return FeePolicy{}, err
	}
	start, err := uintWord(log.Data, 1, 16)
	if err != nil {
		return FeePolicy{}, err
	}
	window, err := uintWord(log.Data, 2, 32)
	if err != nil {
		return FeePolicy{}, err
	}
	if base.Uint64() > 1_000 || start.Cmp(base) < 0 || start.Uint64() > 9_900 || window.Sign() <= 0 {
		return FeePolicy{}, ErrMalformedLog
	}
	return FeePolicy{
		BaseFeeBPS: uint32(base.Uint64()), AntiSnipeStartTotalBPS: uint32(start.Uint64()),
		AntiSnipeWindowSeconds: uint32(window.Uint64()), EffectiveAt: log.BlockNumber,
	}, nil
}

func decodePoolsLaunch(log gethtypes.Log) (Launch, error) {
	if err := validateLog(log, 2, 0); err != nil {
		return Launch{}, err
	}
	token, err := topicAddress(log.Topics[1])
	if err != nil {
		return Launch{}, err
	}
	return Launch{Protocol: ProtocolPoolsTrade, Token: token}, nil
}

func decodePAIRLaunch(log gethtypes.Log) (Launch, error) {
	if err := validateLog(log, 4, 224); err != nil {
		return Launch{}, err
	}
	token, err := topicAddress(log.Topics[1])
	if err != nil {
		return Launch{}, err
	}
	quote, err := topicAddress(log.Topics[2])
	if err != nil {
		return Launch{}, err
	}
	if log.Topics[3] == (common.Hash{}) {
		return Launch{}, ErrMalformedLog
	}
	for _, field := range [...]struct{ index, bits int }{{0, 256}, {1, 16}, {2, 256}, {5, 160}, {6, 256}} {
		if _, err := uintWord(log.Data, field.index, field.bits); err != nil {
			return Launch{}, err
		}
	}
	if _, err := signedWord(log.Data, 3, 24); err != nil {
		return Launch{}, err
	}
	if _, err := signedWord(log.Data, 4, 24); err != nil {
		return Launch{}, err
	}
	return Launch{Protocol: ProtocolPAIR, Token: token, Quote: quote, PoolID: log.Topics[3]}, nil
}

func decodeBagsLaunch(log gethtypes.Log, quote common.Address) (Launch, error) {
	if len(log.Topics) != 4 || len(log.Data) > maxLogDataBytes {
		return Launch{}, ErrMalformedLog
	}
	token, err := topicAddress(log.Topics[1])
	if err != nil {
		return Launch{}, err
	}
	curve, err := topicAddress(log.Topics[2])
	if err != nil {
		return Launch{}, err
	}
	if curve == (common.Address{}) {
		return Launch{}, ErrMalformedLog
	}
	creator, err := topicAddress(log.Topics[3])
	if err != nil {
		return Launch{}, err
	}
	values, err := bagsEventABI.Events["TokenCreated"].Inputs.NonIndexed().Unpack(log.Data)
	if err != nil || len(values) != 6 {
		return Launch{}, ErrMalformedLog
	}
	poolID, ok0 := values[2].([32]byte)
	name, ok1 := values[3].(string)
	symbol, ok2 := values[4].(string)
	metadataURI, ok3 := values[5].(string)
	if _, ok := values[0].(common.Address); !ok || !ok0 || !ok1 || !ok2 || !ok3 || poolID == ([32]byte{}) {
		return Launch{}, ErrMalformedLog
	}
	if _, ok := values[1].(common.Address); !ok {
		return Launch{}, ErrMalformedLog
	}
	return Launch{Protocol: ProtocolBagsV2, Token: token, Quote: quote, Creator: creator, Curve: curve, PoolID: poolID, Name: name, Symbol: symbol, MetadataURI: metadataURI}, nil
}

func decodePonsPool(log gethtypes.Log) (Launch, error) {
	if err := validateLog(log, 2, 96); err != nil {
		return Launch{}, err
	}
	token, err := addressWord(log.Data, 0)
	if err != nil {
		return Launch{}, err
	}
	quote, err := addressWord(log.Data, 1)
	if err != nil {
		return Launch{}, err
	}
	creator, err := addressWord(log.Data, 2)
	if err != nil {
		return Launch{}, err
	}
	if log.Topics[1] == (common.Hash{}) {
		return Launch{}, ErrMalformedLog
	}
	return Launch{Protocol: ProtocolPonsV2, Token: token, Quote: quote, Creator: creator, PoolID: log.Topics[1]}, nil
}

func decodeCurveTrade(log gethtypes.Log) (CurveTrade, error) {
	if err := validateLog(log, 3, 128); err != nil {
		return CurveTrade{}, err
	}
	actor, err := topicAddress(log.Topics[1])
	if err != nil {
		return CurveTrade{}, err
	}
	recipient, err := topicAddress(log.Topics[2])
	if err != nil {
		return CurveTrade{}, err
	}
	amountIn, err := uintWord(log.Data, 0, 256)
	if err != nil {
		return CurveTrade{}, err
	}
	amountOut, err := uintWord(log.Data, 1, 256)
	if err != nil {
		return CurveTrade{}, err
	}
	fee, err := uintWord(log.Data, 2, 256)
	if err != nil {
		return CurveTrade{}, err
	}
	tax, err := uintWord(log.Data, 3, 256)
	if err != nil {
		return CurveTrade{}, err
	}
	return CurveTrade{BuyerOrSeller: actor, Recipient: recipient, AmountIn: amountIn, AmountOut: amountOut, Fee: fee, Tax: tax}, nil
}

func decodeBagsCurveTrade(log gethtypes.Log, buy bool) (CurveTrade, error) {
	dataWords := 9
	if buy {
		dataWords = 10
	}
	if err := validateLog(log, 3, dataWords*32); err != nil {
		return CurveTrade{}, err
	}
	actor, err := topicAddress(log.Topics[1])
	if err != nil {
		return CurveTrade{}, err
	}
	recipient, err := topicAddress(log.Topics[2])
	if err != nil {
		return CurveTrade{}, err
	}
	amountIn, err := uintWord(log.Data, 0, 256)
	if err != nil {
		return CurveTrade{}, err
	}
	amountOut, err := uintWord(log.Data, 2, 256)
	if err != nil {
		return CurveTrade{}, err
	}
	fee, err := uintWord(log.Data, 3, 256)
	if err != nil {
		return CurveTrade{}, err
	}
	return CurveTrade{BuyerOrSeller: actor, Recipient: recipient, AmountIn: amountIn, AmountOut: amountOut, Fee: fee, Tax: new(big.Int)}, nil
}
