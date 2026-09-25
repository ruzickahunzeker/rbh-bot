package rbhparser

import (
	"bytes"
	"encoding/binary"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/holiman/uint256"
)

type testPoolKeyABI struct {
	Currency0   common.Address `abi:"currency0"`
	Currency1   common.Address `abi:"currency1"`
	Fee         *big.Int       `abi:"fee"`
	TickSpacing *big.Int       `abi:"tickSpacing"`
	Hooks       common.Address `abi:"hooks"`
}

type testExactInputABI struct {
	PoolKey          testPoolKeyABI `abi:"poolKey"`
	ZeroForOne       bool           `abi:"zeroForOne"`
	AmountIn         *big.Int       `abi:"amountIn"`
	AmountOutMinimum *big.Int       `abi:"amountOutMinimum"`
	MinHopPriceX36   *big.Int       `abi:"minHopPriceX36"`
	HookData         []byte         `abi:"hookData"`
}

type testCallABI struct {
	Target common.Address `abi:"target"`
	Value  *big.Int       `abi:"value"`
	Data   []byte         `abi:"data"`
}

type testAuthorizedCallABI struct {
	Target   common.Address `abi:"target"`
	Value    *big.Int       `abi:"value"`
	Data     []byte         `abi:"data"`
	AuthData []byte         `abi:"authData"`
}

type testMulticall3ValueABI struct {
	Target       common.Address `abi:"target"`
	AllowFailure bool           `abi:"allowFailure"`
	Value        *big.Int       `abi:"value"`
	Data         []byte         `abi:"data"`
}

type testPonsSocialsABI struct {
	Twitter   string `abi:"twitter"`
	Telegram  string `abi:"telegram"`
	Discord   string `abi:"discord"`
	Website   string `abi:"website"`
	Farcaster string `abi:"farcaster"`
}

type testPonsTokenParamsABI struct {
	Name                string             `abi:"name"`
	Symbol              string             `abi:"symbol"`
	Logo                string             `abi:"logo"`
	Description         string             `abi:"description"`
	Socials             testPonsSocialsABI `abi:"socials"`
	CreatorFeeRecipient common.Address     `abi:"creatorFeeRecipient"`
	CreatorTaxBps       uint16             `abi:"creatorTaxBps"`
	BuybackEnabled      bool               `abi:"buybackEnabled"`
	ExpectedEconomics   [32]byte           `abi:"expectedEconomics"`
	Salt                [32]byte           `abi:"salt"`
}

type testLongCreateABI struct {
	InitialSupply         *big.Int       `abi:"initialSupply"`
	NumTokensToSell       *big.Int       `abi:"numTokensToSell"`
	Numeraire             common.Address `abi:"numeraire"`
	TokenFactory          common.Address `abi:"tokenFactory"`
	TokenFactoryData      []byte         `abi:"tokenFactoryData"`
	GovernanceFactory     common.Address `abi:"governanceFactory"`
	GovernanceFactoryData []byte         `abi:"governanceFactoryData"`
	PoolInitializer       common.Address `abi:"poolInitializer"`
	PoolInitializerData   []byte         `abi:"poolInitializerData"`
	LiquidityMigrator     common.Address `abi:"liquidityMigrator"`
	LiquidityMigratorData []byte         `abi:"liquidityMigratorData"`
	Integrator            common.Address `abi:"integrator"`
	Salt                  [32]byte       `abi:"salt"`
}

type testO1LaunchABI struct {
	TokenName             string         `abi:"tokenName"`
	TokenSymbol           string         `abi:"tokenSymbol"`
	TokenContractURI      string         `abi:"tokenContractURI"`
	CreatorSalt           [32]byte       `abi:"creatorSalt"`
	QuoteToken            common.Address `abi:"quoteToken"`
	ExpectedConfigVersion uint64         `abi:"expectedConfigVersion"`
	Deadline              uint64         `abi:"deadline"`
	MetadataEditable      bool           `abi:"metadataEditable"`
	MetadataKeys          []string       `abi:"metadataKeys"`
	MetadataValues        []string       `abi:"metadataValues"`
}

type testO1FeeRecipientABI struct {
	Id            [32]byte       `abi:"id"`
	RecipientType uint8          `abi:"recipientType"`
	Recipient     common.Address `abi:"recipient"`
	ShareBPS      uint16         `abi:"shareBps"`
}

type testO1FeeConfigurationABI struct {
	BaseFeeBPS             uint16                  `abi:"baseFeeBps"`
	AntiSnipeStartTotalBPS uint16                  `abi:"antiSnipeStartTotalBps"`
	AntiSnipeWindowSeconds uint32                  `abi:"antiSnipeWindowSeconds"`
	FeeRecipients          []testO1FeeRecipientABI `abi:"feeRecipients"`
}

func buildV4IntentData(t testing.TB, key PoolKey, zeroForOne bool, amountIn, minOut *big.Int) []byte {
	t.Helper()
	tupleType, err := abi.NewType("tuple", "ExactInputSingle", []abi.ArgumentMarshaling{
		{Name: "poolKey", Type: "tuple", Components: []abi.ArgumentMarshaling{{Name: "currency0", Type: "address"}, {Name: "currency1", Type: "address"}, {Name: "fee", Type: "uint24"}, {Name: "tickSpacing", Type: "int24"}, {Name: "hooks", Type: "address"}}},
		{Name: "zeroForOne", Type: "bool"}, {Name: "amountIn", Type: "uint128"}, {Name: "amountOutMinimum", Type: "uint128"}, {Name: "minHopPriceX36", Type: "uint256"}, {Name: "hookData", Type: "bytes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	swap, err := (abi.Arguments{{Type: tupleType}}).Pack(testExactInputABI{PoolKey: testPoolKeyABI{key.Currency0, key.Currency1, new(big.Int).SetUint64(uint64(key.Fee)), big.NewInt(int64(key.TickSpacing)), key.Hooks}, ZeroForOne: zeroForOne, AmountIn: amountIn, AmountOutMinimum: minOut, MinHopPriceX36: new(big.Int), HookData: []byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	planArgs := abiArguments(t, "bytes", "bytes[]")
	plan, err := planArgs.Pack([]byte{actionSwapExactInSingle}, [][]byte{swap})
	if err != nil {
		t.Fatal(err)
	}
	routerABI, err := abi.JSON(strings.NewReader(`[{"type":"function","name":"execute","inputs":[{"type":"bytes"},{"type":"bytes[]"},{"type":"uint256"}],"outputs":[]}]`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := routerABI.Pack("execute", []byte{commandV4Swap}, [][]byte{plan}, big.NewInt(1234))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func transactionTo(to common.Address, data []byte, value *big.Int) *gethtypes.Transaction {
	return gethtypes.NewTx(&gethtypes.DynamicFeeTx{ChainID: big.NewInt(4663), To: &to, Data: data, Value: value, Gas: 500_000, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2)})
}

func TestOversizedIntentCalldataIsRejectedBeforeDecode(t *testing.T) {
	parser := New()
	data := make([]byte, 4+maxLogDataBytes+1)
	copy(data[:4], selectorExecute[:])
	tx := transactionTo(parser.Addresses().UniversalRouter, data, new(big.Int))
	if parser.IsPotentialIntent(tx) {
		t.Fatal("oversized calldata passed the lightweight intent filter")
	}
	if _, ok, err := parser.ParseTransactionIntent(tx, common.HexToAddress("0x1")); err == nil || ok {
		t.Fatalf("oversized calldata decode: ok=%t err=%v", ok, err)
	}
}

func setCodeTransactionTo(to common.Address, data []byte, value *big.Int) *gethtypes.Transaction {
	return gethtypes.NewTx(&gethtypes.SetCodeTx{
		ChainID: uint256.NewInt(4663), GasTipCap: uint256.NewInt(1), GasFeeCap: uint256.NewInt(2),
		Gas: 500_000, To: to, Data: data, Value: uint256.MustFromBig(value),
	})
}

func ponsLaunchAndBuyData(t testing.TB, quote, recipient common.Address, amount *big.Int) []byte {
	t.Helper()
	paramsType := ponsParamsABIType(t)
	arrayType, err := abi.NewType("address[]", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	args, err := (abi.Arguments{{Type: paramsType}, {Type: mustABIType(t, "uint256")}, {Type: mustABIType(t, "address")}, {Type: mustABIType(t, "uint256")}, {Type: mustABIType(t, "uint256")}, {Type: mustABIType(t, "address")}, {Type: arrayType}}).Pack(testPonsParams(), big.NewInt(3), quote, amount, big.NewInt(90), recipient, []common.Address{common.HexToAddress("0x55")})
	if err != nil {
		t.Fatal(err)
	}
	return append(selectorPonsLaunchAndBuy[:], args...)
}

func extendedTargetCallData(t testing.TB, selector [4]byte, headWords int, target common.Address, data []byte, value *big.Int) []byte {
	t.Helper()
	if headWords < 3 {
		t.Fatal("extended target call head is too short")
	}
	args := make([]byte, headWords*abiWordSize)
	copy(args[abiWordSize-len(target):abiWordSize], target[:])
	big.NewInt(int64(headWords * abiWordSize)).FillBytes(args[abiWordSize : 2*abiWordSize])
	value.FillBytes(args[2*abiWordSize : 3*abiWordSize])
	length := make([]byte, abiWordSize)
	big.NewInt(int64(len(data))).FillBytes(length)
	args = append(args, length...)
	args = append(args, data...)
	if padding := (abiWordSize - len(data)%abiWordSize) % abiWordSize; padding > 0 {
		args = append(args, make([]byte, padding)...)
	}
	return append(selector[:], args...)
}

func ponsParamsABIType(t testing.TB) abi.Type {
	t.Helper()
	paramsType, err := abi.NewType("tuple", "", []abi.ArgumentMarshaling{
		{Name: "name", Type: "string"}, {Name: "symbol", Type: "string"}, {Name: "logo", Type: "string"}, {Name: "description", Type: "string"},
		{Name: "socials", Type: "tuple", Components: []abi.ArgumentMarshaling{{Name: "twitter", Type: "string"}, {Name: "telegram", Type: "string"}, {Name: "discord", Type: "string"}, {Name: "website", Type: "string"}, {Name: "farcaster", Type: "string"}}},
		{Name: "creatorFeeRecipient", Type: "address"}, {Name: "creatorTaxBps", Type: "uint16"}, {Name: "buybackEnabled", Type: "bool"}, {Name: "expectedEconomics", Type: "bytes32"}, {Name: "salt", Type: "bytes32"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return paramsType
}

func testPonsParams() testPonsTokenParamsABI {
	return testPonsTokenParamsABI{Name: "Pons", Symbol: "PNS", Logo: "ipfs://logo", Description: "launch", Socials: testPonsSocialsABI{Twitter: "x"}, CreatorFeeRecipient: common.HexToAddress("0x44"), CreatorTaxBps: 25, BuybackEnabled: true, ExpectedEconomics: [32]byte{1}, Salt: [32]byte{2}}
}

func longCreateData(t testing.TB, params testLongCreateABI) []byte {
	t.Helper()
	tupleType, err := abi.NewType("tuple", "", []abi.ArgumentMarshaling{
		{Name: "initialSupply", Type: "uint256"}, {Name: "numTokensToSell", Type: "uint256"},
		{Name: "numeraire", Type: "address"}, {Name: "tokenFactory", Type: "address"},
		{Name: "tokenFactoryData", Type: "bytes"}, {Name: "governanceFactory", Type: "address"},
		{Name: "governanceFactoryData", Type: "bytes"}, {Name: "poolInitializer", Type: "address"},
		{Name: "poolInitializerData", Type: "bytes"}, {Name: "liquidityMigrator", Type: "address"},
		{Name: "liquidityMigratorData", Type: "bytes"}, {Name: "integrator", Type: "address"},
		{Name: "salt", Type: "bytes32"},
	})
	if err != nil {
		t.Fatal(err)
	}
	args, err := (abi.Arguments{{Type: tupleType}}).Pack(params)
	if err != nil {
		t.Fatal(err)
	}
	return append(selectorLongCreate[:], args...)
}

func o1LaunchData(t testing.TB, params testO1LaunchABI) []byte {
	t.Helper()
	tupleType, err := abi.NewType("tuple", "", []abi.ArgumentMarshaling{
		{Name: "tokenName", Type: "string"}, {Name: "tokenSymbol", Type: "string"}, {Name: "tokenContractURI", Type: "string"},
		{Name: "creatorSalt", Type: "bytes32"}, {Name: "quoteToken", Type: "address"}, {Name: "expectedConfigVersion", Type: "uint64"},
		{Name: "deadline", Type: "uint64"}, {Name: "metadataEditable", Type: "bool"}, {Name: "metadataKeys", Type: "string[]"}, {Name: "metadataValues", Type: "string[]"},
	})
	if err != nil {
		t.Fatal(err)
	}
	args, err := (abi.Arguments{{Type: tupleType}}).Pack(params)
	if err != nil {
		t.Fatal(err)
	}
	return append(selectorO1Launch[:], args...)
}

func o1FeeConfigurationData(t testing.TB, configuration testO1FeeConfigurationABI) []byte {
	t.Helper()
	tupleType, err := abi.NewType("tuple", "", []abi.ArgumentMarshaling{
		{Name: "baseFeeBps", Type: "uint16"}, {Name: "antiSnipeStartTotalBps", Type: "uint16"},
		{Name: "antiSnipeWindowSeconds", Type: "uint32"},
		{Name: "feeRecipients", Type: "tuple[]", Components: []abi.ArgumentMarshaling{
			{Name: "id", Type: "bytes32"}, {Name: "recipientType", Type: "uint8"},
			{Name: "recipient", Type: "address"}, {Name: "shareBps", Type: "uint16"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	args, err := (abi.Arguments{{Type: tupleType}}).Pack(configuration)
	if err != nil {
		t.Fatal(err)
	}
	return append(selectorO1SetFeeConfig[:], args...)
}

func mustABIType(t testing.TB, name string) abi.Type {
	t.Helper()
	typ, err := abi.NewType(name, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}

func batchCallsData(t testing.TB, selector [4]byte, authorized bool, calls []testCallABI) []byte {
	t.Helper()
	components := []abi.ArgumentMarshaling{{Name: "target", Type: "address"}, {Name: "value", Type: "uint256"}, {Name: "data", Type: "bytes"}}
	var value any = calls
	if authorized {
		components = append(components, abi.ArgumentMarshaling{Name: "authData", Type: "bytes"})
		authorizedCalls := make([]testAuthorizedCallABI, len(calls))
		for index, call := range calls {
			authorizedCalls[index] = testAuthorizedCallABI{Target: call.Target, Value: call.Value, Data: call.Data}
		}
		value = authorizedCalls
	}
	tupleArray, err := abi.NewType("tuple[]", "", components)
	if err != nil {
		t.Fatal(err)
	}
	args, err := (abi.Arguments{{Type: tupleArray}}).Pack(value)
	if err != nil {
		t.Fatal(err)
	}
	return append(selector[:], args...)
}

func multicall3ValueData(t testing.TB, calls []testMulticall3ValueABI) []byte {
	t.Helper()
	tupleArray, err := abi.NewType("tuple[]", "", []abi.ArgumentMarshaling{
		{Name: "target", Type: "address"}, {Name: "allowFailure", Type: "bool"},
		{Name: "value", Type: "uint256"}, {Name: "data", Type: "bytes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	args, err := (abi.Arguments{{Type: tupleArray}}).Pack(calls)
	if err != nil {
		t.Fatal(err)
	}
	return append(selectorAggregate3Value[:], args...)
}

func TestParseRegisteredV4BuyIntent(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0x20")
	key := PoolKey{Currency0: common.Address{}, Currency1: token, Fee: 3000, TickSpacing: 60}
	id, err := PoolID(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := parser.Registry().RegisterPool(PoolRegistration{PoolID: id, Protocol: ProtocolLong, Token: token, Quote: common.Address{}, PoolKey: key}); err != nil {
		t.Fatal(err)
	}
	sender := common.HexToAddress("0x33")
	tx := transactionTo(parser.Addresses().UniversalRouter, buildV4IntentData(t, key, true, big.NewInt(100), big.NewInt(90)), big.NewInt(100))
	intent, ok, err := parser.ParseTransactionIntent(tx, sender)
	if err != nil || !ok {
		t.Fatalf("parse: ok=%v err=%v", ok, err)
	}
	if intent.Kind != IntentBuy || intent.Protocol != ProtocolLong || intent.Sender != sender || intent.Token != token || intent.CurrencyIn != (common.Address{}) || intent.CurrencyOut != token || intent.AmountIn.Cmp(big.NewInt(100)) != 0 || intent.MinimumAmountOut.Cmp(big.NewInt(90)) != 0 || intent.PoolID != id || intent.Deadline.Uint64() != 1234 || intent.Confirmed {
		t.Fatalf("unexpected intent: %#v", intent)
	}
}

func TestParseLetsCashCompactBuyIntent(t *testing.T) {
	parser := New()
	addresses := parser.Addresses()
	token := common.HexToAddress("0x7a5204507d139b7107a2370011749c11a9b37ecc")
	key := PoolKey{Currency0: common.Address{}, Currency1: token, TickSpacing: 200, Hooks: addresses.LetsCashHook}
	id, err := PoolID(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: token, Protocol: ProtocolLetsCash}); err != nil {
		t.Fatal(err)
	}
	if err := parser.Registry().RegisterPool(PoolRegistration{PoolID: id, Protocol: ProtocolLetsCash, Token: token, PoolKey: key}); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 68)
	copy(data[:4], selectorLetsCashBuy[:])
	copy(data[4:24], token[:])
	data[29] = 200
	copy(data[30:50], addresses.LetsCashHook[:])
	data[65] = 9
	binary.BigEndian.PutUint16(data[66:68], 300)
	sender := common.HexToAddress("0x33")
	intent, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().LetsCashRouter, data, big.NewInt(100)), sender)
	if err != nil || !ok {
		t.Fatalf("parse: ok=%v err=%v", ok, err)
	}
	if intent.Protocol != ProtocolLetsCash || intent.Kind != IntentBuy || intent.Token != token || intent.PoolID != id || intent.AmountIn.Uint64() != 100 || intent.MinimumAmountOut.Uint64() != 9 {
		t.Fatalf("unexpected letscash intent: %#v", intent)
	}
}

func TestParseDirectRouterIntents(t *testing.T) {
	parser := New()
	sender := common.HexToAddress("0x33")
	token := common.HexToAddress("0xc1a036b7252289b24fc3a6a778b18bd6f4182b06")
	quote := parser.Addresses().WETH
	varoPool := common.HexToAddress("0x225ef630877b53d70e1ddb2bd06c53918297fe14")
	virtualVenue := common.HexToAddress("0x325ef630877b53d70e1ddb2bd06c53918297fe14")
	varoABI := mustABI(`[{"type":"function","name":"buy","inputs":[{"type":"address"},{"type":"uint256"},{"type":"uint256"}]},{"type":"function","name":"sell","inputs":[{"type":"address"},{"type":"address"},{"type":"uint256"},{"type":"uint256"},{"type":"uint256"}]}]`)
	virtualsABI := mustABI(`[{"type":"function","name":"buy","inputs":[{"type":"address"},{"type":"uint256"},{"type":"uint256"},{"type":"uint256"},{"type":"uint256"}]},{"type":"function","name":"sell","inputs":[{"type":"address"},{"type":"uint256"},{"type":"uint256"},{"type":"uint256"},{"type":"uint256"}]}]`)

	if err := parser.Registry().RegisterToken(TokenRegistration{Token: token, Protocol: ProtocolVaro, Quote: quote, Venue: varoPool}); err != nil {
		t.Fatal(err)
	}
	varoData, _ := varoABI.Pack("buy", varoPool, big.NewInt(9), big.NewInt(100))
	intent, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().VaroRouter, varoData, big.NewInt(10)), sender)
	if err != nil || !ok || intent.Protocol != ProtocolVaro || intent.Kind != IntentBuy || intent.Venue != varoPool || intent.AmountIn.Uint64() != 10 || intent.MinimumAmountOut.Uint64() != 9 {
		t.Fatalf("unexpected Varo intent: %#v ok=%v err=%v", intent, ok, err)
	}

	virtualToken := common.HexToAddress("0xd1a036b7252289b24fc3a6a778b18bd6f4182b06")
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: virtualToken, Protocol: ProtocolVirtuals, Quote: parser.Addresses().VirtualToken, Venue: virtualVenue}); err != nil {
		t.Fatal(err)
	}
	virtualData, _ := virtualsABI.Pack("sell", virtualToken, big.NewInt(10), big.NewInt(8), big.NewInt(7), big.NewInt(100))
	intent, ok, err = parser.ParseTransactionIntent(transactionTo(parser.Addresses().VirtualsRouter, virtualData, new(big.Int)), sender)
	if err != nil || !ok || intent.Protocol != ProtocolVirtuals || intent.Kind != IntentSell || intent.Token != virtualToken || intent.AmountIn.Uint64() != 10 || intent.MinimumIntermediateAmountOut.Uint64() != 8 || intent.MinimumAmountOut.Uint64() != 7 || intent.Deadline.Uint64() != 100 || intent.MaximumFeeBPS != 0 {
		t.Fatalf("unexpected Virtuals intent: %#v ok=%v err=%v", intent, ok, err)
	}
	virtualData, _ = virtualsABI.Pack("buy", virtualToken, big.NewInt(7), big.NewInt(6), big.NewInt(100), big.NewInt(300))
	copy(virtualData[:4], selectorVirtualsBuy[:])
	intent, ok, err = parser.ParseTransactionIntent(transactionTo(parser.Addresses().VirtualsRouter, virtualData, big.NewInt(10)), sender)
	if err != nil || !ok || intent.Kind != IntentBuy || intent.AmountIn.Uint64() != 10 || intent.MinimumIntermediateAmountOut.Uint64() != 7 || intent.MinimumAmountOut.Uint64() != 6 || intent.MaximumFeeBPS != 300 || common.Bytes2Hex(virtualData[:4]) != "7346be70" {
		t.Fatalf("unexpected Virtuals buy intent: %#v ok=%v err=%v", intent, ok, err)
	}
}

func TestParseObservedVirtualsNativeSellCalldata(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0x222aeb6973866995f733f61c9ab1ed058986efcb")
	venue := common.HexToAddress("0x8a95c7239680e9c4fd8366ea9320fe2c86056c99")
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: token, Protocol: ProtocolVirtuals, Quote: parser.Addresses().VirtualToken, Venue: venue}); err != nil {
		t.Fatal(err)
	}
	data := common.FromHex("0x9be8cd61000000000000000000000000222aeb6973866995f733f61c9ab1ed058986efcb00000000000000000000000000000000000000000001d352afb579cce305390a00000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000079218ee4b0868c00000000000000000000000000000000000000000000000000000000f4865700")
	intent, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().VirtualsRouter, data, new(big.Int)), common.HexToAddress("0x94e90a9d6b3125ef6dd7238c3d5d2231ee1c52e6"))
	if err != nil || !ok {
		t.Fatalf("parse observed sell: ok=%t err=%v", ok, err)
	}
	if intent.Kind != IntentSell || intent.Token != token || intent.AmountIn.String() != "2206870441674016809105674" || intent.MinimumIntermediateAmountOut.Cmp(big.NewInt(1)) != 0 || intent.MinimumAmountOut.String() != "34095369787836044" || intent.Deadline.Uint64() != 4_102_444_800 || intent.MaximumFeeBPS != 0 {
		t.Fatalf("observed Virtuals sell intent=%#v", intent)
	}
}

func TestParseDirectNonpayableSellsRejectNativeValue(t *testing.T) {
	parser := New()
	varoABI := mustABI(`[{"type":"function","name":"sell","inputs":[{"type":"address"},{"type":"address"},{"type":"uint256"},{"type":"uint256"},{"type":"uint256"}]}]`)
	virtualsABI := mustABI(`[{"type":"function","name":"sell","inputs":[{"type":"address"},{"type":"uint256"},{"type":"uint256"},{"type":"uint256"},{"type":"uint256"}]}]`)
	token := common.HexToAddress("0x9100000000000000000000000000000000000009")
	varoVenue := common.HexToAddress("0x9200000000000000000000000000000000000009")
	virtualsVenue := common.HexToAddress("0x9300000000000000000000000000000000000009")
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: token, Protocol: ProtocolVaro, Quote: parser.Addresses().WETH, Venue: varoVenue}); err != nil {
		t.Fatal(err)
	}
	varoData, err := varoABI.Pack("sell", varoVenue, token, big.NewInt(10), big.NewInt(1), big.NewInt(100))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().VaroRouter, varoData, big.NewInt(1)), common.HexToAddress("0x1")); err == nil {
		t.Fatal("Varo nonpayable sell accepted native value")
	}

	virtualToken := common.HexToAddress("0x9400000000000000000000000000000000000009")
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: virtualToken, Protocol: ProtocolVirtuals, Quote: parser.Addresses().VirtualToken, Venue: virtualsVenue}); err != nil {
		t.Fatal(err)
	}
	virtualData, err := virtualsABI.Pack("sell", virtualToken, big.NewInt(10), big.NewInt(1), big.NewInt(1), big.NewInt(100))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().VirtualsRouter, virtualData, big.NewInt(1)), common.HexToAddress("0x1")); err == nil {
		t.Fatal("Virtuals nonpayable sell accepted native value")
	}
}

func TestParseVaroProtocolFeeIntent(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0xc1a036b7252289b24fc3a6a778b18bd6f4182b06")
	venue := common.HexToAddress("0x225ef630877b53d70e1ddb2bd06c53918297fe14")
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: token, Protocol: ProtocolVaro, Quote: parser.Addresses().WETH, Venue: venue}); err != nil {
		t.Fatal(err)
	}
	args, err := abiArguments(t, "address", "uint16").Pack(token, uint16(301))
	if err != nil {
		t.Fatal(err)
	}
	data := append(selectorVaroSetFee[:], args...)
	intent, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().VaroLaunchpad, data, new(big.Int)), common.HexToAddress("0x33"))
	if err != nil || !ok || intent.Kind != IntentFeePolicy || intent.Protocol != ProtocolVaro || intent.FeePolicy == nil || intent.FeePolicy.BuyFeeBPS != 301 {
		t.Fatalf("unexpected Varo fee intent: %#v ok=%v err=%v", intent, ok, err)
	}

	tooHighArgs, err := abiArguments(t, "address", "uint16").Pack(token, uint16(3_001))
	if err != nil {
		t.Fatal(err)
	}
	tooHigh := append(selectorVaroSetFee[:], tooHighArgs...)
	if _, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().VaroLaunchpad, tooHigh, new(big.Int)), common.HexToAddress("0x33")); err == nil || ok {
		t.Fatalf("Varo fee above contract maximum accepted: ok=%v err=%v", ok, err)
	}
	if _, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().VaroLaunchpad, data[:len(data)-1], new(big.Int)), common.HexToAddress("0x33")); err == nil || ok {
		t.Fatalf("truncated Varo fee update accepted: ok=%v err=%v", ok, err)
	}
}

func TestParseVirtualsLaunchAndTaxUpdateIntents(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0xed6cac056c3127b89da25d6b06d7aaa0f0f37766")
	venue := common.HexToAddress("0x8a95c7239680e9c4fd8366ea9320fe2c86056c99")
	sender := common.HexToAddress("0xe220329659d41b2a9f26e83816b424bdacf62567")
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: token, Protocol: ProtocolVirtuals, Quote: parser.Addresses().VirtualToken, Venue: venue}); err != nil {
		t.Fatal(err)
	}
	launchData := append(selectorVirtualsLaunch[:], common.LeftPadBytes(token.Bytes(), 32)...)
	intent, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().VirtualsLaunchpad, launchData, new(big.Int)), sender)
	if err != nil || !ok || intent.Kind != IntentLaunch || intent.Protocol != ProtocolVirtuals || intent.Token != token {
		t.Fatalf("unexpected Virtuals launch intent: %#v ok=%v err=%v", intent, ok, err)
	}
	taxArgs, err := abiArguments(t, "uint16", "uint16").Pack(uint16(9900), uint16(200))
	if err != nil {
		t.Fatal(err)
	}
	taxData := append(selectorVirtualsSetTax[:], taxArgs...)
	intent, ok, err = parser.ParseTransactionIntent(transactionTo(token, taxData, new(big.Int)), sender)
	if err != nil || !ok || intent.Kind != IntentFeePolicy || intent.FeePolicy == nil || intent.FeePolicy.BuyFeeBPS != 9900 || intent.FeePolicy.SellFeeBPS != 200 {
		t.Fatalf("unexpected Virtuals tax intent: %#v ok=%v err=%v", intent, ok, err)
	}
	factoryArgs, err := abiArguments(t, "address", "uint256", "uint256", "uint256", "address").Pack(common.HexToAddress("0x100"), big.NewInt(1), big.NewInt(2), big.NewInt(99), common.HexToAddress("0x200"))
	if err != nil {
		t.Fatal(err)
	}
	factoryData := append(selectorVirtualsFactorySetTax[:], factoryArgs...)
	intent, ok, err = parser.ParseTransactionIntent(transactionTo(parser.Addresses().VirtualsFactory, factoryData, new(big.Int)), sender)
	if err != nil || !ok || intent.Kind != IntentFeePolicy || intent.FeePolicy == nil || intent.FeePolicy.BuyFeeBPS != 100 || intent.FeePolicy.SellFeeBPS != 200 || intent.FeePolicy.AntiSnipeStartBPS != 9900 {
		t.Fatalf("unexpected Virtuals factory tax intent: %#v ok=%v err=%v", intent, ok, err)
	}
}

func TestParseGMGNRouterIntent(t *testing.T) {
	parser := New()
	sender := common.HexToAddress("0x33")
	token := common.HexToAddress("0xc1a036b7252289b24fc3a6a778b18bd6f4182b06")
	quote := parser.Addresses().WETH
	pool := common.HexToAddress("0x225ef630877b53d70e1ddb2bd06c53918297fe14")
	if err := parser.Registry().RegisterToken(TokenRegistration{Token: token, Protocol: ProtocolFlapTax, Quote: quote, Venue: pool}); err != nil {
		t.Fatal(err)
	}
	routes := []gmgnRouteWire{{Kind: 6, TokenIn: quote, TokenOut: token, Pool: token, Fee: new(big.Int), TickSpacing: new(big.Int)}}
	data, err := gmgnRouterABI.Pack("swap", routes, sender, big.NewInt(10), big.NewInt(8), big.NewInt(100))
	if err != nil {
		t.Fatal(err)
	}
	intent, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().GMGNRouter, data, big.NewInt(10)), sender)
	if err != nil || !ok {
		t.Fatalf("parse: ok=%v err=%v", ok, err)
	}
	if intent.Protocol != ProtocolFlapTax || intent.Kind != IntentBuy || intent.Token != token || intent.AmountIn.Uint64() != 10 || intent.MinimumAmountOut.Uint64() != 8 || len(intent.Routes) != 1 || intent.Routes[0].Kind != 6 {
		t.Fatalf("unexpected GMGN intent: %#v", intent)
	}
	if _, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().GMGNRouter, data, big.NewInt(9)), sender); err == nil || ok {
		t.Fatalf("underfunded native GMGN buy was accepted: ok=%v err=%v", ok, err)
	}
	nativeRoutes := []gmgnRouteWire{{Kind: 6, TokenOut: token, Pool: token, Fee: new(big.Int), TickSpacing: new(big.Int)}}
	nativeData, err := gmgnRouterABI.Pack("swap", nativeRoutes, sender, big.NewInt(10), big.NewInt(8), big.NewInt(100))
	if err != nil {
		t.Fatal(err)
	}
	intent, ok, err = parser.ParseTransactionIntent(transactionTo(parser.Addresses().GMGNRouter, nativeData, big.NewInt(10)), sender)
	if err != nil || !ok || intent.CurrencyIn != quote || intent.Quote != quote || len(intent.Routes) != 1 || intent.Routes[0].TokenIn != quote {
		t.Fatalf("native-sentinel GMGN buy was not normalized: intent=%#v ok=%v err=%v", intent, ok, err)
	}
	if _, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().GMGNRouter, nativeData, new(big.Int)), sender); err == nil || ok {
		t.Fatalf("unfunded native-sentinel GMGN buy was accepted: ok=%v err=%v", ok, err)
	}

	usdg := common.HexToAddress("0x5fc5360D0400a0Fd4f2af552ADD042D716F1d168")
	erc20Routes := []gmgnRouteWire{{Kind: 6, TokenIn: usdg, TokenOut: token, Pool: token, Fee: new(big.Int), TickSpacing: new(big.Int)}}
	erc20Data, err := gmgnRouterABI.Pack("swap", erc20Routes, sender, big.NewInt(10), big.NewInt(8), big.NewInt(100))
	if err != nil {
		t.Fatal(err)
	}
	intent, ok, err = parser.ParseTransactionIntent(transactionTo(parser.Addresses().GMGNRouter, erc20Data, new(big.Int)), sender)
	if err != nil || !ok || intent.Kind != IntentBuy || intent.CurrencyIn != usdg || intent.TransactionValue.Sign() != 0 {
		t.Fatalf("ERC20-funded GMGN buy was not parsed: intent=%#v ok=%v err=%v", intent, ok, err)
	}
	if _, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().GMGNRouter, erc20Data, big.NewInt(10)), sender); err == nil || ok {
		t.Fatalf("ERC20-funded GMGN buy accepted native value: ok=%v err=%v", ok, err)
	}
}

func TestParseUntrackedGMGNInvalidAmountsIsIgnored(t *testing.T) {
	parser := New()
	sender := common.HexToAddress("0x100")
	routes := []gmgnRouteWire{{
		Kind: 6, TokenIn: parser.Addresses().WETH, TokenOut: common.HexToAddress("0x200"),
		Pool: common.HexToAddress("0x200"), Fee: new(big.Int), TickSpacing: new(big.Int),
	}}
	data, err := gmgnRouterABI.Pack("swap", routes, sender, new(big.Int), new(big.Int), big.NewInt(100))
	if err != nil {
		t.Fatal(err)
	}
	intent, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().GMGNRouter, data, new(big.Int)), sender)
	if err != nil || ok {
		t.Fatalf("untracked GMGN route intent=%#v ok=%v err=%v", intent, ok, err)
	}
}

func TestParseUntrackedV2MultihopIsIgnored(t *testing.T) {
	parser := New()
	sender := common.HexToAddress("0x100")
	path := []common.Address{parser.Addresses().WETH, common.HexToAddress("0x200"), common.HexToAddress("0x300")}
	data, err := uniswapV2RouterABI.Pack("swapExactETHForTokensSupportingFeeOnTransferTokens", big.NewInt(1), path, sender, big.NewInt(100))
	if err != nil {
		t.Fatal(err)
	}
	intent, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().UniswapV2Router, data, big.NewInt(1)), sender)
	if err != nil || ok {
		t.Fatalf("untracked V2 multihop intent=%#v ok=%v err=%v", intent, ok, err)
	}
}

func TestParseFlapGraduatedV2RouterIntent(t *testing.T) {
	parser := New()
	sender := common.HexToAddress("0x33")
	token := common.HexToAddress("0x6c6b6fd36d28715be2d241d36c5521f98e5e7777")
	pair := common.HexToAddress("0x946b3c8932794088cc259b4b5159484904d8b63e")
	registration := TokenRegistration{Token: token, Protocol: ProtocolFlapTax, Venue: parser.Addresses().FlapController}
	if err := parser.Registry().RegisterToken(registration); err != nil {
		t.Fatal(err)
	}
	registration.Venue = pair
	if err := parser.Registry().RegisterVenue(registration); err != nil {
		t.Fatal(err)
	}
	data, err := uniswapV2RouterABI.Pack("swapExactETHForTokensSupportingFeeOnTransferTokens", big.NewInt(8), []common.Address{parser.Addresses().WETH, token}, sender, big.NewInt(100))
	if err != nil {
		t.Fatal(err)
	}
	tx := transactionTo(parser.Addresses().UniswapV2Router, data, big.NewInt(10))
	if !parser.IsPotentialIntent(tx) {
		t.Fatal("direct Flap V2 buy was not recognized by the feed prefilter")
	}
	intent, ok, err := parser.ParseTransactionIntent(tx, sender)
	if err != nil || !ok || intent.Protocol != ProtocolFlapTax || intent.Kind != IntentBuy || intent.Token != token || intent.Venue != pair || intent.AmountIn.Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("Flap V2 intent=%#v ok=%t err=%v", intent, ok, err)
	}
}

func TestParseLongCreateIntent(t *testing.T) {
	parser := New()
	sender := common.HexToAddress("0x11")
	params := testLongCreateABI{
		InitialSupply:         big.NewInt(1_000_000),
		NumTokensToSell:       big.NewInt(750_000),
		Numeraire:             common.HexToAddress("0x20"),
		TokenFactory:          common.HexToAddress("0x30"),
		TokenFactoryData:      []byte{1, 2},
		GovernanceFactory:     common.HexToAddress("0x40"),
		GovernanceFactoryData: []byte{3, 4},
		PoolInitializer:       common.HexToAddress("0x50"),
		PoolInitializerData:   []byte{5, 6},
		LiquidityMigrator:     common.HexToAddress("0x60"),
		LiquidityMigratorData: []byte{7, 8},
		Integrator:            common.HexToAddress("0x70"),
		Salt:                  [32]byte{9},
	}
	tx := transactionTo(parser.Addresses().LongLauncher, longCreateData(t, params), new(big.Int))
	intent, ok, err := parser.ParseTransactionIntent(tx, sender)
	if err != nil || !ok {
		t.Fatalf("parse: ok=%v err=%v", ok, err)
	}
	long := intent.LongLaunch
	if intent.Kind != IntentLaunch || intent.Protocol != ProtocolLong || intent.Quote != params.Numeraire || long == nil ||
		long.InitialSupply.Cmp(params.InitialSupply) != 0 || long.NumTokensToSell.Cmp(params.NumTokensToSell) != 0 ||
		long.Numeraire != params.Numeraire || long.TokenFactory != params.TokenFactory || !bytes.Equal(long.TokenFactoryData, params.TokenFactoryData) ||
		long.GovernanceFactory != params.GovernanceFactory || !bytes.Equal(long.GovernanceFactoryData, params.GovernanceFactoryData) ||
		long.PoolInitializer != params.PoolInitializer || !bytes.Equal(long.PoolInitializerData, params.PoolInitializerData) ||
		long.LiquidityMigrator != params.LiquidityMigrator || !bytes.Equal(long.LiquidityMigratorData, params.LiquidityMigratorData) ||
		long.Integrator != params.Integrator || long.Salt != common.BytesToHash(params.Salt[:]) {
		t.Fatalf("unexpected intent: %#v", intent)
	}
}

func TestParseO1CreateLaunchQuote(t *testing.T) {
	parser := New()
	sender := common.HexToAddress("0x11")
	quote := common.HexToAddress("0x22")
	data := o1LaunchData(t, testO1LaunchABI{
		TokenName: "O1", TokenSymbol: "ONE", CreatorSalt: [32]byte{1}, QuoteToken: quote,
		ExpectedConfigVersion: 1, Deadline: 100, MetadataKeys: []string{"site"}, MetadataValues: []string{"example"},
	})
	intent, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().O1Factory, data, new(big.Int)), sender)
	if err != nil || !ok {
		t.Fatalf("parse: ok=%v err=%v", ok, err)
	}
	if intent.Protocol != ProtocolO1 || intent.Kind != IntentLaunch || intent.Quote != quote || intent.Recipient != sender {
		t.Fatalf("unexpected O1 launch intent: %#v", intent)
	}
}

func TestParseO1PendingFeeConfiguration(t *testing.T) {
	parser := New()
	sender := common.HexToAddress("0x11")
	data := o1FeeConfigurationData(t, testO1FeeConfigurationABI{
		BaseFeeBPS: 100, AntiSnipeStartTotalBPS: 9_900, AntiSnipeWindowSeconds: 120,
		FeeRecipients: []testO1FeeRecipientABI{{Id: [32]byte{1}, RecipientType: 1, Recipient: common.HexToAddress("0x22"), ShareBPS: 100}},
	})
	intent, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().O1Factory, data, new(big.Int)), sender)
	if err != nil || !ok || intent.Protocol != ProtocolO1 || intent.Kind != IntentFeePolicy || intent.FeePolicy == nil {
		t.Fatalf("o1 fee intent=%#v ok=%v err=%v", intent, ok, err)
	}
	policy := intent.FeePolicy
	if policy.BaseFeeBPS != 100 || policy.AntiSnipeStartTotalBPS != 9_900 || policy.AntiSnipeWindowSeconds != 120 {
		t.Fatalf("unexpected o1 pending fee policy: %#v", policy)
	}
	invalid := o1FeeConfigurationData(t, testO1FeeConfigurationABI{BaseFeeBPS: 301, AntiSnipeStartTotalBPS: 300, AntiSnipeWindowSeconds: 120})
	if _, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().O1Factory, invalid, new(big.Int)), sender); err == nil || ok {
		t.Fatalf("invalid o1 pending fee configuration accepted: ok=%v err=%v", ok, err)
	}
	if _, ok, err := parser.ParseTransactionIntent(transactionTo(parser.Addresses().O1Factory, data[:4+64], new(big.Int)), sender); err == nil || ok {
		t.Fatalf("truncated o1 pending fee configuration accepted: ok=%v err=%v", ok, err)
	}
}

func TestParseLongCreateRejectsHeadOffsetAsDynamicData(t *testing.T) {
	parser := New()
	params := testLongCreateABI{InitialSupply: big.NewInt(1), NumTokensToSell: big.NewInt(1)}
	data := longCreateData(t, params)
	poolDataOffsetWord := 4 + abiWordSize + 8*abiWordSize
	clear(data[poolDataOffsetWord : poolDataOffsetWord+abiWordSize])
	tx := transactionTo(parser.Addresses().LongLauncher, data, new(big.Int))
	if _, ok, err := parser.ParseTransactionIntent(tx, common.HexToAddress("0x11")); err == nil || ok {
		t.Fatalf("malformed Long create accepted: ok=%v err=%v", ok, err)
	}
}

func TestParseLongCreateRejectsNonCanonicalTupleOffset(t *testing.T) {
	parser := New()
	params := testLongCreateABI{InitialSupply: big.NewInt(1), NumTokensToSell: big.NewInt(1)}
	data := longCreateData(t, params)
	data[4+abiWordSize-1] = 2 * abiWordSize
	tx := transactionTo(parser.Addresses().LongLauncher, data, new(big.Int))
	if _, ok, err := parser.ParseTransactionIntent(tx, common.HexToAddress("0x11")); err == nil || ok {
		t.Fatalf("non-canonical Long tuple offset accepted: ok=%v err=%v", ok, err)
	}
}

func TestUnknownV4PoolIsIgnored(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0x20")
	key := PoolKey{Currency0: common.Address{}, Currency1: token, Fee: 3000, TickSpacing: 60}
	tx := transactionTo(parser.Addresses().UniversalRouter, buildV4IntentData(t, key, true, big.NewInt(1), new(big.Int)), big.NewInt(1))
	if _, ok, err := parser.ParseTransactionIntent(tx, common.HexToAddress("0x1")); err != nil || ok {
		t.Fatalf("unknown pool parsed: ok=%v err=%v", ok, err)
	}
}

func TestParsePonsAndBagsCurveIntents(t *testing.T) {
	parser := New()
	sender := common.HexToAddress("0x11")
	recipient := common.HexToAddress("0x12")
	token := common.HexToAddress("0x13")
	quote := common.HexToAddress("0x14")
	ponsCurve := common.HexToAddress("0x15")
	bagsCurve := common.HexToAddress("0x16")
	if err := parser.Registry().RegisterCurve(CurveRegistration{Curve: ponsCurve, Protocol: ProtocolPonsV2, Token: token, Quote: quote}); err != nil {
		t.Fatal(err)
	}
	if err := parser.Registry().RegisterCurve(CurveRegistration{Curve: bagsCurve, Protocol: ProtocolBagsV2, Token: token, Quote: common.Address{}}); err != nil {
		t.Fatal(err)
	}
	ponsArgs, _ := abiArguments(t, "uint256", "uint256", "address").Pack(big.NewInt(100), big.NewInt(90), recipient)
	ponsData := append(selectorPonsBuy[:], ponsArgs...)
	intent, ok, err := parser.ParseTransactionIntent(transactionTo(ponsCurve, ponsData, new(big.Int)), sender)
	if err != nil || !ok || intent.Kind != IntentBuy || intent.AmountIn.Uint64() != 100 || intent.Recipient != recipient || intent.Quote != quote {
		t.Fatalf("Pons intent: %#v ok=%v err=%v", intent, ok, err)
	}
	bagsArgs, _ := abiArguments(t, "address", "uint256").Pack(recipient, big.NewInt(77))
	bagsData := append(selectorBagsBuyFor[:], bagsArgs...)
	intent, ok, err = parser.ParseTransactionIntent(transactionTo(bagsCurve, bagsData, big.NewInt(88)), sender)
	if err != nil || !ok || intent.Kind != IntentBuy || intent.AmountIn.Uint64() != 88 || intent.MinimumAmountOut.Uint64() != 77 || intent.Recipient != recipient {
		t.Fatalf("Bags intent: %#v ok=%v err=%v", intent, ok, err)
	}
}

func TestMalformedRouterCalldataIsRejected(t *testing.T) {
	data := make([]byte, 4+3*abiWordSize)
	copy(data, selectorExecute[:])
	for i := 4; i < 4+abiWordSize; i++ {
		data[i] = 0xff
	}
	_, _, err := New().ParseTransactionIntent(transactionTo(DefaultAddressBook().UniversalRouter, data, new(big.Int)), common.HexToAddress("0x1"))
	if err == nil {
		t.Fatal("malformed calldata accepted")
	}
}

func TestIntentProtocolAndKindFilters(t *testing.T) {
	parser := New()
	token := common.HexToAddress("0x20")
	key := PoolKey{Currency0: common.Address{}, Currency1: token, Fee: 3000, TickSpacing: 60}
	id, _ := PoolID(key)
	_ = parser.Registry().RegisterPool(PoolRegistration{PoolID: id, Protocol: ProtocolLong, Token: token, Quote: common.Address{}, PoolKey: key})
	tx := transactionTo(parser.Addresses().UniversalRouter, buildV4IntentData(t, key, true, big.NewInt(100), big.NewInt(90)), big.NewInt(100))
	sender := common.HexToAddress("0x33")
	buyLong := IntentFilter{IncludeProtocols: Protocols(ProtocolLong), IncludeKinds: IntentKinds(IntentBuy), Senders: Addresses(sender), Tokens: Addresses(token), Pools: PoolIDs(id)}
	if !parser.IsPotentialIntentWithFilter(tx, buyLong) {
		t.Fatal("valid v4 buy was rejected by early filter")
	}
	if _, ok, err := parser.ParseTransactionIntentFiltered(tx, sender, buyLong); err != nil || !ok {
		t.Fatalf("filtered parse: ok=%v err=%v", ok, err)
	}
	wrongKind := IntentFilter{IncludeKinds: IntentKinds(IntentLaunch)}
	if parser.IsPotentialIntentWithFilter(tx, wrongKind) {
		t.Fatal("v4 swap passed launch-only early filter")
	}
	if _, ok, err := parser.ParseTransactionIntentFiltered(tx, sender, wrongKind); err != nil || ok {
		t.Fatalf("wrong kind output: ok=%v err=%v", ok, err)
	}
	wrongSender := buyLong
	wrongSender.Senders = Addresses(common.HexToAddress("0x99"))
	if _, ok, err := parser.ParseTransactionIntentFiltered(tx, sender, wrongSender); err != nil || ok {
		t.Fatalf("wrong sender output: ok=%v err=%v", ok, err)
	}
}

func TestParsePonsFactoryLaunchOverloads(t *testing.T) {
	parser := New()
	sender := common.HexToAddress("0x11")
	quote := common.HexToAddress("0x12")
	for _, test := range []struct {
		name       string
		selector   [4]byte
		exemptions bool
	}{
		{"simple", selectorPonsLaunch, false},
		{"exemptions", selectorPonsLaunchExempt, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			arguments := abi.Arguments{{Type: ponsParamsABIType(t)}, {Type: mustABIType(t, "uint256")}, {Type: mustABIType(t, "address")}}
			values := []any{testPonsParams(), big.NewInt(3), quote}
			if test.exemptions {
				arguments = append(arguments, abi.Argument{Type: mustABIType(t, "address[]")})
				values = append(values, []common.Address{common.HexToAddress("0x55")})
			}
			args, err := arguments.Pack(values...)
			if err != nil {
				t.Fatal(err)
			}
			data := append(test.selector[:], args...)
			tx := transactionTo(parser.Addresses().PonsFactory, data, big.NewInt(1))
			filter := IntentFilter{IncludeProtocols: Protocols(ProtocolPonsV2), IncludeKinds: IntentKinds(IntentLaunch)}
			if !parser.IsPotentialIntentWithFilter(tx, filter) {
				t.Fatal("Pons launch rejected by early filter")
			}
			intent, ok, err := parser.ParseTransactionIntentFiltered(tx, sender, filter)
			if err != nil || !ok || intent.Protocol != ProtocolPonsV2 || intent.Kind != IntentLaunch || intent.Sender != sender || intent.Quote != quote {
				t.Fatalf("intent=%#v ok=%v err=%v", intent, ok, err)
			}
			if intent.PonsLaunch == nil || intent.PonsLaunch.Params.Symbol != "PNS" || intent.PonsLaunch.OriginalDeployer != sender {
				t.Fatalf("payload=%#v", intent.PonsLaunch)
			}
		})
	}
}

func TestKnownPonsLaunchSelectors(t *testing.T) {
	tests := []struct {
		name string
		got  [4]byte
		want [4]byte
	}{
		{"launchToken", selectorPonsLaunch, [4]byte{0xf3, 0x5a, 0xbb, 0xcf}},
		{"launchToken with exemptions", selectorPonsLaunchExempt, [4]byte{0xa7, 0x21, 0x01, 0xaf}},
		{"launchAndBuy", selectorPonsLaunchAndBuy, [4]byte{0xf8, 0x5f, 0x8e, 0x41}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("selector = 0x%x, want 0x%x", test.got, test.want)
			}
		})
	}
}

func TestParseNestedPonsLaunchAndBuy(t *testing.T) {
	parser := New()
	wallet := common.HexToAddress("0x31")
	quote := common.HexToAddress("0x32")
	innerValue := big.NewInt(7)
	inner := ponsLaunchAndBuyData(t, quote, wallet, big.NewInt(100))
	calls := []testCallABI{
		{Target: common.HexToAddress("0x99"), Value: new(big.Int), Data: []byte{1, 2, 3, 4}},
		{Target: parser.Addresses().PonsLaunchAndBuy, Value: innerValue, Data: inner},
	}
	standard := batchCallsData(t, selectorExecuteBatch, false, calls)
	authorized := batchCallsData(t, selectorExecuteBatchAuth, true, calls)
	targetArgs, err := abiArguments(t, "address", "bytes", "uint256").Pack(parser.Addresses().PonsLaunchAndBuy, inner, innerValue)
	if err != nil {
		t.Fatal(err)
	}
	target := append(selectorTargetCall[:], targetArgs...)
	targetV2 := append(selectorTargetCallV2[:], targetArgs...)
	extendedTarget := extendedTargetCallData(t, selectorExtendedTargetCall, 18, parser.Addresses().PonsLaunchAndBuy, inner, innerValue)
	extendedTargetV2 := extendedTargetCallData(t, selectorExtendedTargetCallV2, 15, parser.Addresses().PonsLaunchAndBuy, inner, innerValue)
	pons7702Args, err := abiArguments(t, "bytes", "uint256", "address", "bytes", "uint256").Pack([]byte{1}, big.NewInt(2), quote, inner, innerValue)
	if err != nil {
		t.Fatal(err)
	}
	pons7702 := append(selectorPons7702Launch[:], pons7702Args...)
	executeModeArgs, err := abiArguments(t, "bytes32", "bytes").Pack([32]byte{1}, standard[4:])
	if err != nil {
		t.Fatal(err)
	}
	executeMode := append(selectorExecuteMode[:], executeModeArgs...)
	multicall := multicall3ValueData(t, []testMulticall3ValueABI{{Target: parser.Addresses().PonsLaunchAndBuy, Value: innerValue, Data: inner}})

	tests := []struct {
		name             string
		tx               *gethtypes.Transaction
		originalDeployer common.Address
	}{
		{"executeBatch", setCodeTransactionTo(wallet, standard, big.NewInt(999)), wallet},
		{"authorized batch", setCodeTransactionTo(wallet, authorized, big.NewInt(999)), wallet},
		{"target envelope", transactionTo(common.HexToAddress("0x41"), target, big.NewInt(999)), wallet},
		{"target envelope v2", transactionTo(common.HexToAddress("0x41"), targetV2, big.NewInt(999)), wallet},
		{"extended target envelope", transactionTo(common.HexToAddress("0x6319141dcc29e7dceaab6c4563cca1fb58e8cce2"), extendedTarget, big.NewInt(999)), testPonsParams().CreatorFeeRecipient},
		{"extended target envelope v2", transactionTo(common.HexToAddress("0x6319141dcc29e7dceaab6c4563cca1fb58e8cce2"), extendedTargetV2, big.NewInt(999)), common.Address{}},
		{"Pons 7702 envelope", setCodeTransactionTo(wallet, pons7702, big.NewInt(999)), wallet},
		{"execute mode", setCodeTransactionTo(wallet, executeMode, big.NewInt(999)), wallet},
		{"Multicall3 value", transactionTo(common.HexToAddress("0x42"), multicall, big.NewInt(999)), wallet},
	}
	filter := IntentFilter{
		IncludeProtocols: Protocols(ProtocolPonsV2),
		IncludeKinds:     IntentKinds(IntentLaunchAndBuy),
		Contracts:        Addresses(parser.Addresses().PonsLaunchAndBuy),
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !parser.IsPotentialIntentWithFilter(test.tx, filter) {
				t.Fatal("nested launch rejected by early filter")
			}
			intent, ok, err := parser.ParseTransactionIntentFiltered(test.tx, wallet, filter)
			if err != nil || !ok {
				t.Fatalf("parse: ok=%v err=%v", ok, err)
			}
			if intent.Protocol != ProtocolPonsV2 || intent.Kind != IntentLaunchAndBuy || intent.Sender != wallet ||
				intent.Contract != parser.Addresses().PonsLaunchAndBuy || intent.Quote != quote ||
				intent.AmountIn.Cmp(big.NewInt(100)) != 0 || intent.TransactionValue.Cmp(innerValue) != 0 {
				t.Fatalf("unexpected nested intent: %#v", intent)
			}
			if intent.PonsLaunch == nil || intent.PonsLaunch.OriginalDeployer != test.originalDeployer || intent.PonsLaunch.LaunchConfigID.Uint64() != 3 ||
				intent.PonsLaunch.Params.Name != "Pons" || intent.PonsLaunch.Params.CreatorTaxBPS != 25 ||
				intent.PonsLaunch.Params.Salt != (common.Hash{2}) || len(intent.PonsLaunch.SnipeTaxExemptions) != 1 {
				t.Fatalf("incomplete Pons launch payload: %#v", intent.PonsLaunch)
			}
		})
	}
}

func TestParseAtomicLaunchV2(t *testing.T) {
	parser := New()
	wallet := common.HexToAddress("0x31")
	quote := common.HexToAddress("0x32")
	standard := ponsLaunchAndBuyData(t, quote, wallet, big.NewInt(100))
	paramsOffset := int(new(big.Int).SetBytes(standard[4 : 4+abiWordSize]).Int64())
	tuple := make([]byte, 12*abiWordSize)
	big.NewInt(12 * abiWordSize).FillBytes(tuple[:abiWordSize])
	big.NewInt(3).FillBytes(tuple[abiWordSize : 2*abiWordSize])
	copy(tuple[2*abiWordSize+12:3*abiWordSize], quote.Bytes())
	tuple = append(tuple, standard[4+paramsOffset:]...)
	args := make([]byte, abiWordSize, abiWordSize+len(tuple))
	big.NewInt(abiWordSize).FillBytes(args)
	args = append(args, tuple...)
	data := append(selectorAtomicLaunchV2[:], args...)
	tx := transactionTo(wallet, data, new(big.Int))
	filter := IntentFilter{IncludeProtocols: Protocols(ProtocolPonsV2), IncludeKinds: IntentKinds(IntentLaunch), Contracts: Addresses(parser.Addresses().PonsFactory)}
	if !parser.IsPotentialIntentWithFilter(tx, filter) {
		t.Fatal("atomic launch rejected by early filter")
	}
	intent, ok, err := parser.ParseTransactionIntentFiltered(tx, wallet, filter)
	if err != nil || !ok || intent.Kind != IntentLaunch || intent.Protocol != ProtocolPonsV2 || intent.Contract != parser.Addresses().PonsFactory || intent.Quote != quote ||
		intent.PonsLaunch == nil || intent.PonsLaunch.OriginalDeployer != (common.Address{}) || intent.PonsLaunch.LaunchConfigID.Uint64() != 3 || intent.PonsLaunch.Params.Symbol != "PNS" {
		t.Fatalf("intent=%#v ok=%v err=%v", intent, ok, err)
	}
}

func TestParseEmbeddedPonsLaunchEnvelopes(t *testing.T) {
	parser := New()
	wallet := common.HexToAddress("0x31")
	quote := common.HexToAddress("0x32")
	inner := ponsLaunchAndBuyData(t, quote, wallet, big.NewInt(100))
	for _, selector := range [][4]byte{selectorEmbeddedPonsLaunchV1, selectorEmbeddedPonsLaunchV2} {
		data := make([]byte, 4+4*abiWordSize)
		copy(data[:4], selector[:])
		data = append(data, inner...)
		tx := transactionTo(wallet, data, new(big.Int))
		intent, ok, err := parser.ParseTransactionIntent(tx, wallet)
		if err != nil || !ok || intent.Kind != IntentLaunch || intent.Protocol != ProtocolPonsV2 || intent.Contract != parser.Addresses().PonsLaunchAndBuy || intent.Quote != quote ||
			intent.PonsLaunch == nil || intent.PonsLaunch.OriginalDeployer != (common.Address{}) || intent.PonsLaunch.LaunchConfigID.Uint64() != 3 || intent.PonsLaunch.Params.Symbol != "PNS" {
			t.Fatalf("selector=0x%x intent=%#v ok=%v err=%v", selector, intent, ok, err)
		}
	}
}

func TestNestedCallLimits(t *testing.T) {
	parser := New()
	wallet := common.HexToAddress("0x31")
	args := make([]byte, 2*abiWordSize)
	big.NewInt(abiWordSize).FillBytes(args[:abiWordSize])
	big.NewInt(maxNestedCalls + 1).FillBytes(args[abiWordSize:])
	data := append(selectorExecuteBatch[:], args...)
	tx := setCodeTransactionTo(wallet, data, new(big.Int))
	if parser.IsPotentialIntent(tx) {
		t.Fatal("oversized nested batch passed early filter")
	}
	if _, _, err := parser.ParseTransactionIntent(tx, wallet); err == nil {
		t.Fatal("oversized nested batch was accepted")
	}
}

func TestEventFilter(t *testing.T) {
	event := Event{Kind: EventSwap, Protocol: ProtocolLong}
	filter := EventFilter{IncludeProtocols: Protocols(ProtocolLong), IncludeKinds: EventKinds(EventSwap)}
	if !filter.Match(event) {
		t.Fatal("matching event rejected")
	}
	filter.ExcludeKinds = EventKinds(EventSwap)
	if filter.Match(event) {
		t.Fatal("excluded event accepted")
	}
}

func BenchmarkParseRegisteredV4Intent(b *testing.B) {
	parser := New()
	token := common.HexToAddress("0x20")
	key := PoolKey{Currency0: common.Address{}, Currency1: token, Fee: 3000, TickSpacing: 60}
	id, _ := PoolID(key)
	_ = parser.Registry().RegisterPool(PoolRegistration{PoolID: id, Protocol: ProtocolLong, Token: token, Quote: common.Address{}, PoolKey: key})
	tx := transactionTo(parser.Addresses().UniversalRouter, buildV4IntentData(b, key, true, big.NewInt(100), big.NewInt(90)), big.NewInt(100))
	sender := common.HexToAddress("0x33")
	b.ReportAllocs()
	for b.Loop() {
		if _, ok, err := parser.ParseTransactionIntent(tx, sender); err != nil || !ok {
			b.Fatal(err)
		}
	}
}

func BenchmarkEarlyFilterNestedPonsLaunch(b *testing.B) {
	parser := New()
	wallet := common.HexToAddress("0x31")
	inner := ponsLaunchAndBuyData(b, common.Address{}, wallet, big.NewInt(100))
	data := batchCallsData(b, selectorExecuteBatch, false, []testCallABI{{Target: parser.Addresses().PonsLaunchAndBuy, Value: big.NewInt(7), Data: inner}})
	tx := setCodeTransactionTo(wallet, data, big.NewInt(7))
	filter := IntentFilter{IncludeProtocols: Protocols(ProtocolPonsV2), IncludeKinds: IntentKinds(IntentLaunchAndBuy)}
	b.ReportAllocs()
	for b.Loop() {
		if !parser.IsPotentialIntentWithFilter(tx, filter) {
			b.Fatal("nested Pons launch rejected")
		}
	}
}

func BenchmarkParseNestedPonsLaunch(b *testing.B) {
	parser := New()
	wallet := common.HexToAddress("0x31")
	inner := ponsLaunchAndBuyData(b, common.Address{}, wallet, big.NewInt(100))
	data := batchCallsData(b, selectorExecuteBatch, false, []testCallABI{{Target: parser.Addresses().PonsLaunchAndBuy, Value: big.NewInt(7), Data: inner}})
	tx := setCodeTransactionTo(wallet, data, big.NewInt(7))
	b.ReportAllocs()
	for b.Loop() {
		if _, ok, err := parser.ParseTransactionIntent(tx, wallet); err != nil || !ok {
			b.Fatal(err)
		}
	}
}
