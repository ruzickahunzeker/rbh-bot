package chainquery

import (
	"bytes"
	"context"
	"math/big"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Identity is copied from the existing C02_PONS_CURVE_PASS lock. It is a
// query-classification baseline, NOT a canary attestation or send permission.
const FactoryAddress = "0x7ed598bcef8bd9edd8c97a195c6d13f40801ec7e"
const FactoryCodeHash = "0x89a27da6f703e0a7cdd4f233e7cb57604ff75b164530962d3ff7cf8483a67d84"
const CurveCodeHash = "0xf2b593a629a279392a33bc0e8be50393024c614a5e48ff24a13ae274582a1b78"

type Token struct {
	Chain                string `json:"chain"`
	Address              string `json:"address"`
	Name                 string `json:"name"`
	Symbol               string `json:"symbol"`
	Decimals             uint8  `json:"decimals"`
	Protocol             string `json:"protocol"`
	Curve                string `json:"curve"`
	Factory              string `json:"factory"`
	RuntimeCodeHash      string `json:"runtime_code_hash"`
	CurveRuntimeCodeHash string `json:"curve_runtime_code_hash"`
	QuoteAsset           string `json:"quote_asset"`
	Block                Block  `json:"block"`
	SendAuthorized       bool   `json:"send_authorized"`
}

func selector(method string) []byte { return crypto.Keccak256([]byte(method))[:4] }
func wordAddress(data []byte) (common.Address, error) {
	if len(data) != 32 || !bytes.Equal(data[:12], make([]byte, 12)) {
		return common.Address{}, ErrObservation
	}
	return common.BytesToAddress(data[12:]), nil
}
func wordUint(data []byte) (*big.Int, error) {
	if len(data) != 32 {
		return nil, ErrObservation
	}
	return new(big.Int).SetBytes(data), nil
}
func wordBool(data []byte) (bool, error) {
	x, err := wordUint(data)
	if err != nil || !x.IsUint64() || x.Uint64() > 1 {
		return false, ErrObservation
	}
	return x.Uint64() == 1, nil
}

func (c *Client) tokenAt(ctx context.Context, address common.Address, s snapshot) (Token, error) {
	factory := common.HexToAddress(FactoryAddress)
	code, err := c.code(ctx, factory, s)
	if err != nil {
		return Token{}, err
	}
	if crypto.Keccak256Hash(code) != c.factoryCodeHash {
		return Token{}, ErrObservation
	}
	data := append(selector("getLaunchedToken(address)"), common.LeftPadBytes(address.Bytes(), 32)...)
	launch, err := c.call(ctx, factory, data, s)
	if err != nil {
		return Token{}, err
	}
	if len(launch) != 15*32 {
		return Token{}, ErrObservation
	}
	word := func(i int) []byte { return launch[i*32 : (i+1)*32] }
	for _, i := range []int{0, 1, 2, 3, 4} {
		if _, err := wordAddress(word(i)); err != nil {
			return Token{}, err
		}
	}
	if new(big.Int).SetBytes(word(6)).BitLen() > 24 || new(big.Int).SetBytes(word(8)).BitLen() > 16 || new(big.Int).SetBytes(word(10)).BitLen() > 8 {
		return Token{}, ErrObservation
	}
	extension := byte(0)
	if word(7)[29]&0x80 != 0 {
		extension = 0xff
	}
	if !bytes.Equal(word(7)[:29], bytes.Repeat([]byte{extension}, 29)) {
		return Token{}, ErrObservation
	}
	if _, err := wordBool(word(9)); err != nil {
		return Token{}, err
	}
	registered, err := wordAddress(word(0))
	if err != nil {
		return Token{}, err
	}
	curve, err := wordAddress(word(1))
	if err != nil {
		return Token{}, err
	}
	pair, err := wordAddress(word(4))
	if err != nil {
		return Token{}, err
	}
	exists, err := wordBool(word(14))
	if err != nil {
		return Token{}, err
	}
	phase, err := wordUint(word(10))
	if err != nil {
		return Token{}, err
	}
	if registered != address || curve == (common.Address{}) || pair != (common.Address{}) || !exists || phase.Sign() != 0 {
		return Token{}, ErrUnsupported
	}
	curveCode, err := c.code(ctx, curve, s)
	if err != nil {
		return Token{}, err
	}
	if crypto.Keccak256Hash(curveCode) != c.curveCodeHash {
		return Token{}, ErrObservation
	}
	for _, binding := range []struct {
		method string
		want   common.Address
	}{{"token()", address}, {"factory()", factory}, {"pairToken()", common.Address{}}} {
		result, err := c.call(ctx, curve, selector(binding.method), s)
		if err != nil {
			return Token{}, err
		}
		value, err := wordAddress(result)
		if err != nil || value != binding.want {
			return Token{}, ErrObservation
		}
	}
	graduatedData, err := c.call(ctx, curve, selector("graduated()"), s)
	if err != nil {
		return Token{}, err
	}
	graduated, err := wordBool(graduatedData)
	if err != nil {
		return Token{}, err
	}
	if graduated {
		return Token{}, ErrUnsupported
	}
	nativeData, err := c.call(ctx, curve, selector("isNativeQuote()"), s)
	if err != nil {
		return Token{}, err
	}
	native, err := wordBool(nativeData)
	if err != nil {
		return Token{}, err
	}
	if !native {
		return Token{}, ErrUnsupported
	}
	tokenCode, err := c.code(ctx, address, s)
	if err != nil {
		return Token{}, err
	}
	decimalsData, err := c.call(ctx, address, selector("decimals()"), s)
	if err != nil {
		return Token{}, err
	}
	decimals, err := wordUint(decimalsData)
	if err != nil || !decimals.IsUint64() || decimals.Uint64() > 255 {
		return Token{}, ErrObservation
	}
	metadata := make([]string, 2)
	for i, method := range []string{"name()", "symbol()"} {
		value, err := c.call(ctx, address, selector(method), s)
		if err != nil {
			return Token{}, err
		}
		metadata[i], err = abiText(value)
		if err != nil {
			return Token{}, err
		}
	}
	return Token{Chain: "robinhood", Address: strings.ToLower(address.Hex()), Name: metadata[0], Symbol: metadata[1], Decimals: uint8(decimals.Uint64()), Protocol: "pons_v2_curve", Curve: strings.ToLower(curve.Hex()), Factory: FactoryAddress, RuntimeCodeHash: crypto.Keccak256Hash(tokenCode).Hex(), CurveRuntimeCodeHash: c.curveCodeHash.Hex(), QuoteAsset: "native", Block: s.block}, nil
}

func abiText(data []byte) (string, error) {
	var text []byte
	if len(data) == 32 {
		text = bytes.TrimRight(data, "\x00")
	} else {
		if len(data) < 64 {
			return "", ErrObservation
		}
		offset, _ := wordUint(data[:32])
		n, _ := wordUint(data[32:64])
		if offset.Cmp(big.NewInt(32)) != 0 || !n.IsUint64() || n.Uint64() > 128 {
			return "", ErrObservation
		}
		length := int(n.Uint64())
		end := 64 + length
		if len(data) != 64+((length+31)/32)*32 || !bytes.Equal(data[end:], make([]byte, len(data)-end)) {
			return "", ErrObservation
		}
		text = data[64:end]
	}
	if len(text) == 0 || len(text) > 128 || !utf8.Valid(text) {
		return "", ErrObservation
	}
	for _, r := range string(text) {
		if unicode.IsControl(r) {
			return "", ErrObservation
		}
	}
	return string(text), nil
}

func (c *Client) Token(ctx context.Context, value string) (Token, error) {
	address, err := Address(value)
	if err != nil {
		return Token{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	s, err := c.begin(ctx)
	if err != nil {
		return Token{}, err
	}
	result, err := c.tokenAt(ctx, address, s)
	if err != nil {
		return Token{}, err
	}
	if err = c.finish(ctx, s); err != nil {
		return Token{}, err
	}
	return result, nil
}

// Units formats exact quantities without float conversion or rounding.
func Units(value *big.Int, decimals uint8) string {
	if value == nil {
		return ""
	}
	text := value.String()
	d := int(decimals)
	if d == 0 {
		return text
	}
	if len(text) <= d {
		text = strings.Repeat("0", d-len(text)+1) + text
	}
	i := len(text) - d
	fraction := strings.TrimRight(text[i:], "0")
	if fraction == "" {
		return text[:i]
	}
	return text[:i] + "." + fraction
}

type Balances struct {
	Block                       Block  `json:"block"`
	NativeUnits                 string `json:"native_units"`
	NativeAmount                string `json:"native_amount"`
	Token                       *Token `json:"token,omitempty"`
	TokenUnits                  string `json:"token_units,omitempty"`
	TokenAmount                 string `json:"token_amount,omitempty"`
	AllowanceUnits              string `json:"allowance_units,omitempty"`
	AllowanceAmount             string `json:"allowance_amount,omitempty"`
	AllowanceLimitedTokenAmount string `json:"allowance_limited_token_amount,omitempty"`
}

func (c *Client) Balances(ctx context.Context, walletValue, tokenValue string) (Balances, error) {
	wallet, err := Address(walletValue)
	if err != nil {
		return Balances{}, err
	}
	var token common.Address
	if tokenValue != "" {
		token, err = Address(tokenValue)
		if err != nil {
			return Balances{}, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	s, err := c.begin(ctx)
	if err != nil {
		return Balances{}, err
	}
	var nativeHex string
	if err = c.rpc(ctx, "eth_getBalance", []any{strings.ToLower(wallet.Hex()), blockParam(s)}, &nativeHex); err != nil {
		return Balances{}, err
	}
	native, err := quantity(nativeHex)
	if err != nil {
		return Balances{}, err
	}
	r := Balances{Block: s.block, NativeUnits: native.String(), NativeAmount: Units(native, 18)}
	if tokenValue != "" {
		info, err := c.tokenAt(ctx, token, s)
		if err != nil {
			return Balances{}, err
		}
		data := append(selector("balanceOf(address)"), common.LeftPadBytes(wallet.Bytes(), 32)...)
		balanceData, err := c.call(ctx, token, data, s)
		if err != nil {
			return Balances{}, err
		}
		balance, err := wordUint(balanceData)
		if err != nil {
			return Balances{}, err
		}
		r.Token = &info
		r.TokenUnits = balance.String()
		r.TokenAmount = Units(balance, info.Decimals)
		allowanceData := append(selector("allowance(address,address)"), common.LeftPadBytes(wallet.Bytes(), 32)...)
		allowanceData = append(allowanceData, common.LeftPadBytes(common.HexToAddress(info.Curve).Bytes(), 32)...)
		allowanceResult, err := c.call(ctx, token, allowanceData, s)
		if err != nil {
			return Balances{}, err
		}
		allowance, err := wordUint(allowanceResult)
		if err != nil {
			return Balances{}, err
		}
		r.AllowanceUnits = allowance.String()
		r.AllowanceAmount = Units(allowance, info.Decimals)
		limited := balance
		if allowance.Cmp(limited) < 0 {
			limited = allowance
		}
		r.AllowanceLimitedTokenAmount = Units(limited, info.Decimals)
	}
	if err = c.finish(ctx, s); err != nil {
		return Balances{}, err
	}
	return r, nil
}

type Fees struct {
	Block              Block  `json:"block"`
	Model              string `json:"model"`
	BaseGwei           string `json:"base_gwei"`
	SuggestedMaxGwei   string `json:"suggested_max_gwei"`
	SuggestedTipGwei   string `json:"suggested_tip_gwei"`
	GasBudgetAvailable bool   `json:"gas_budget_available"`
	ReferenceOnly      bool   `json:"reference_only"`
	TipSource          string `json:"tip_source"`
}

func (c *Client) Fees(ctx context.Context) (Fees, error) {
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	s, err := c.begin(ctx)
	if err != nil {
		return Fees{}, err
	}
	var tipHex string
	if err = c.rpc(ctx, "eth_maxPriorityFeePerGas", []any{}, &tipHex); err != nil {
		return Fees{}, err
	}
	tip, err := quantity(tipHex)
	if err != nil {
		return Fees{}, err
	}
	max := new(big.Int).Add(new(big.Int).Mul(s.base, big.NewInt(2)), tip)
	if max.Sign() <= 0 || max.BitLen() > 256 {
		return Fees{}, ErrObservation
	}
	if err = c.finish(ctx, s); err != nil {
		return Fees{}, err
	}
	return Fees{Block: s.block, Model: "EIP1559", BaseGwei: Units(s.base, 9), SuggestedMaxGwei: Units(max, 9), SuggestedTipGwei: Units(tip, 9), ReferenceOnly: true, TipSource: "PROVIDER_ESTIMATE_NOT_BLOCK_PINNED"}, nil
}
