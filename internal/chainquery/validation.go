package chainquery

// Validation is a read-only preparation boundary. SDK usage is restricted to
// pure quote arithmetic and calldata packing, never clients/signers/submission.
import (
	"context"
	"errors"
	"math/big"
	"strings"
	"time"

	pons "github.com/0xfnzero/rbh-trade-sdk/adapters/pons"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

var ErrValidation = errors.New("manual trade validation rejected")
var ErrExpired = errors.New("application TTL expired or unverifiable")

// Validator has no send capability. Client's original three query methods
// remain unchanged; this separate type never exports arbitrary eth_call.
type Validator struct {
	client *Client
	now    func() time.Time
}

func NewValidator(endpoint string) (*Validator, error) {
	c, err := New(endpoint)
	if err != nil {
		return nil, err
	}
	c.estimates = true
	return &Validator{client: c, now: time.Now}, nil
}

// Intent is constructed from immutable durable MANUAL source, not HTTP RPC
// parameters. Optional values are empty iff UNSET. Policy caps are integer wei.
type ValidationIntent struct {
	Wallet, Token, Side, Amount, MinReceive, MaxFeeWei, TipWei, MaxTotalWei, ExpiresAt string
	SellBPS, SlippageBPS                                                               uint64
}
type ValidationLimits struct{ MaxInput, MaxGas, MaxFee, MaxTip, MinNative string }
type Resolution struct {
	Token           Token  `json:"token"`
	Wallet          string `json:"wallet"`
	Side            string `json:"side"`
	AmountIn        string `json:"amount_in"`
	NativeBalance   string `json:"native_balance"`
	TokenBalance    string `json:"token_balance"`
	Allowance       string `json:"allowance"`
	MinReceiveUnits string `json:"min_receive_units,omitempty"`
}
type ValidationOutput struct {
	Resolution Resolution `json:"resolution"`
	QuotedOut  string     `json:"quoted_out"`
	MinimumOut string     `json:"minimum_out"`
	QuoteSpent string     `json:"quote_spent"`
	To         string     `json:"to"`
	From       string     `json:"from"`
	Value      string     `json:"value"`
	Data       string     `json:"data"`
	GasLimit   uint64     `json:"gas_limit"`
	GasFeeCap  string     `json:"gas_fee_cap"`
	GasTipCap  string     `json:"gas_tip_cap"`
	GasBudget  string     `json:"gas_budget"`
	ReturnData string     `json:"return_data"`
}

func canonicalUint(s string, zero bool) (*big.Int, error) {
	x, ok := new(big.Int).SetString(s, 10)
	if !ok || x.Sign() < 0 || (!zero && x.Sign() == 0) || x.BitLen() > 256 || x.String() != s {
		return nil, ErrValidation
	}
	return x, nil
}
func human(s string, decimals uint8) (*big.Int, error) {
	if s == "" || len(s) > 256 {
		return nil, ErrValidation
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts[0]) > 1 && parts[0][0] == '0') {
		return nil, ErrValidation
	}
	f := ""
	if len(parts) == 2 {
		f = parts[1]
		if f == "" || len(f) > int(decimals) {
			return nil, ErrValidation
		}
	}
	for _, p := range parts {
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil, ErrValidation
			}
		}
	}
	x, ok := new(big.Int).SetString(parts[0]+f+strings.Repeat("0", int(decimals)-len(f)), 10)
	if !ok || x.Sign() <= 0 || x.BitLen() > 256 {
		return nil, ErrValidation
	}
	return x, nil
}
func (v *Validator) ttl(i ValidationIntent) error {
	e, err := time.Parse(time.RFC3339Nano, i.ExpiresAt)
	if v == nil || v.client == nil || v.now == nil || err != nil || e.UTC().Format(time.RFC3339Nano) != i.ExpiresAt {
		return ErrExpired
	}
	n := v.now().UTC()
	if n.IsZero() || !n.Before(e) {
		return ErrExpired
	}
	return nil
}
func (c *Client) balanceAt(ctx context.Context, wallet common.Address, s snapshot) (*big.Int, error) {
	var raw string
	if err := c.rpc(ctx, "eth_getBalance", []any{strings.ToLower(wallet.Hex()), blockParam(s)}, &raw); err != nil {
		return nil, err
	}
	return quantity(raw)
}
func (c *Client) uintCall(ctx context.Context, address common.Address, data []byte, s snapshot) (*big.Int, error) {
	b, err := c.call(ctx, address, data, s)
	if err != nil {
		return nil, err
	}
	return wordUint(b)
}

func (v *Validator) quoteCall(ctx context.Context, i ValidationIntent, address common.Address, data []byte, s snapshot) ([]byte, error) {
	if err := v.ttl(i); err != nil {
		return nil, err
	}
	result, err := v.client.call(ctx, address, data, s)
	if err != nil {
		return nil, err
	}
	if err = v.ttl(i); err != nil {
		return nil, err
	}
	return result, nil
}
func (v *Validator) quoteUint(ctx context.Context, i ValidationIntent, address common.Address, data []byte, s snapshot) (*big.Int, error) {
	result, err := v.quoteCall(ctx, i, address, data, s)
	if err != nil {
		return nil, err
	}
	return wordUint(result)
}

// Resolve captures identity/balances/units once. Caller MUST persist this
// snapshot before Complete; later attempts never resolve percentage anew.
func (v *Validator) Resolve(ctx context.Context, i ValidationIntent) (Resolution, error) {
	if ctx == nil {
		return Resolution{}, ErrValidation
	}
	if err := v.ttl(i); err != nil {
		return Resolution{}, err
	}
	w, err := Address(i.Wallet)
	if err != nil {
		return Resolution{}, err
	}
	t, err := Address(i.Token)
	if err != nil {
		return Resolution{}, err
	}
	if (i.Side != "buy" && i.Side != "sell") || i.SlippageBPS >= 10000 || (i.Amount == "") == (i.SellBPS == 0) || i.SellBPS > 10000 || (i.Side == "buy" && i.SellBPS != 0) {
		return Resolution{}, ErrValidation
	}
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	s, err := v.client.begin(ctx)
	if err != nil {
		return Resolution{}, err
	}
	info, err := v.client.tokenAt(ctx, t, s)
	if err != nil {
		return Resolution{}, err
	}
	native, err := v.client.balanceAt(ctx, w, s)
	if err != nil {
		return Resolution{}, err
	}
	balance, err := v.client.uintCall(ctx, t, append(selector("balanceOf(address)"), common.LeftPadBytes(w.Bytes(), 32)...), s)
	if err != nil {
		return Resolution{}, err
	}
	allowance, err := v.client.uintCall(ctx, t, append(append(selector("allowance(address,address)"), common.LeftPadBytes(w.Bytes(), 32)...), common.LeftPadBytes(common.HexToAddress(info.Curve).Bytes(), 32)...), s)
	if err != nil {
		return Resolution{}, err
	}
	var amount *big.Int
	if i.SellBPS != 0 {
		amount = new(big.Int).Div(new(big.Int).Mul(balance, new(big.Int).SetUint64(i.SellBPS)), big.NewInt(10000))
	} else {
		d := info.Decimals
		if i.Side == "buy" {
			d = 18
		}
		amount, err = human(i.Amount, d)
		if err != nil {
			return Resolution{}, err
		}
	}
	if amount.Sign() <= 0 || amount.BitLen() > 256 || (i.Side == "sell" && (balance.Cmp(amount) < 0 || allowance.Cmp(amount) < 0)) || (i.Side == "buy" && native.Cmp(amount) < 0) {
		return Resolution{}, ErrValidation
	}
	r := Resolution{Token: info, Wallet: strings.ToLower(w.Hex()), Side: i.Side, AmountIn: amount.String(), NativeBalance: native.String(), TokenBalance: balance.String(), Allowance: allowance.String()}
	if i.MinReceive != "" {
		d := uint8(18)
		if i.Side == "buy" {
			d = info.Decimals
		}
		floor, err := human(i.MinReceive, d)
		if err != nil {
			return Resolution{}, err
		}
		r.MinReceiveUnits = floor.String()
	}
	if err = v.client.finish(ctx, s); err != nil {
		return Resolution{}, err
	}
	if err = v.ttl(i); err != nil {
		return Resolution{}, err
	}
	return r, nil
}

// Complete rechecks the SAME canonical snapshot and builds/simulates only the
// fixed curve call; it cannot change resolved units or allocate a nonce.
func (v *Validator) Complete(ctx context.Context, i ValidationIntent, r Resolution, l ValidationLimits) (ValidationOutput, error) {
	if ctx == nil {
		return ValidationOutput{}, ErrValidation
	}
	if err := CheckResolution(i, r); err != nil {
		return ValidationOutput{}, err
	}
	if err := v.ttl(i); err != nil {
		return ValidationOutput{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	if strings.ToLower(i.Wallet) != r.Wallet || strings.ToLower(i.Token) != r.Token.Address || i.Side != r.Side || i.SlippageBPS >= 10000 {
		return ValidationOutput{}, ErrValidation
	}
	if err := v.client.chain(ctx); err != nil {
		return ValidationOutput{}, err
	}
	s, err := v.client.header(ctx, hexutil.EncodeUint64(r.Token.Block.Number))
	if err != nil || s.block != r.Token.Block {
		return ValidationOutput{}, ErrObservation
	}
	info, err := v.client.tokenAt(ctx, common.HexToAddress(r.Token.Address), s)
	if err != nil {
		return ValidationOutput{}, err
	}
	if info != r.Token {
		return ValidationOutput{}, ErrObservation
	}
	// Re-read balances at the persisted hash, never at a fresh snapshot. A
	// contradictory provider must not turn stale resolution into success.
	wallet := common.HexToAddress(r.Wallet)
	token := common.HexToAddress(r.Token.Address)
	nativeAt, err := v.client.balanceAt(ctx, wallet, s)
	if err != nil {
		return ValidationOutput{}, err
	}
	balanceAt, err := v.client.uintCall(ctx, token, append(selector("balanceOf(address)"), common.LeftPadBytes(wallet.Bytes(), 32)...), s)
	if err != nil {
		return ValidationOutput{}, err
	}
	allowanceAt, err := v.client.uintCall(ctx, token, append(append(selector("allowance(address,address)"), common.LeftPadBytes(wallet.Bytes(), 32)...), common.LeftPadBytes(common.HexToAddress(info.Curve).Bytes(), 32)...), s)
	if err != nil {
		return ValidationOutput{}, err
	}
	if nativeAt.String() != r.NativeBalance || balanceAt.String() != r.TokenBalance || allowanceAt.String() != r.Allowance {
		return ValidationOutput{}, ErrObservation
	}
	amount, err := canonicalUint(r.AmountIn, false)
	if err != nil {
		return ValidationOutput{}, err
	}
	curve := common.HexToAddress(info.Curve)
	reservesData, err := v.quoteCall(ctx, i, curve, selector("getReserves()"), s)
	if err != nil {
		return ValidationOutput{}, err
	}
	if len(reservesData) != 64 {
		return ValidationOutput{}, ErrObservation
	}
	qr, _ := wordUint(reservesData[:32])
	tr, _ := wordUint(reservesData[32:])
	reserves := pons.CurveReserves{QuoteReserve: qr, TokenReserve: tr}
	fee, err := v.quoteUint(ctx, i, curve, selector("feeBps()"), s)
	if err != nil {
		return ValidationOutput{}, err
	}
	tax, err := v.quoteUint(ctx, i, curve, selector("creatorTaxBps()"), s)
	if err != nil {
		return ValidationOutput{}, err
	}
	var out, min, spent *big.Int
	if i.Side == "buy" {
		sellable, err := v.quoteUint(ctx, i, curve, selector("sellableTokens()"), s)
		if err != nil {
			return ValidationOutput{}, err
		}
		snipe, err := v.quoteUint(ctx, i, curve, append(selector("currentSnipeTaxBps(address)"), common.LeftPadBytes(wallet.Bytes(), 32)...), s)
		if err != nil {
			return ValidationOutput{}, err
		}
		q, err := pons.QuoteBuyFromState(reserves, sellable, amount, fee, tax, snipe)
		if err != nil {
			return ValidationOutput{}, ErrValidation
		}
		out, spent = q.TokensOut, q.Spent
		min, err = pons.MinTokensOutForBuy(amount, q, i.SlippageBPS)
		if err != nil {
			return ValidationOutput{}, ErrValidation
		}
		// Partial-spend buy calldata uses ratio-based protection. C2 rejects
		// partial spend rather than overclaiming an absolute user output floor.
		if spent.Cmp(amount) != 0 {
			return ValidationOutput{}, ErrValidation
		}
	} else {
		q, err := pons.QuoteSellFromState(reserves, amount, fee, tax)
		if err != nil {
			return ValidationOutput{}, ErrValidation
		}
		out = q.QuoteOut
		spent = big.NewInt(0)
		min = new(big.Int).Div(new(big.Int).Mul(out, new(big.Int).SetUint64(10000-i.SlippageBPS)), big.NewInt(10000))
	}
	if r.MinReceiveUnits != "" {
		floor, err := canonicalUint(r.MinReceiveUnits, false)
		if err != nil {
			return ValidationOutput{}, err
		}
		if floor.Cmp(min) > 0 {
			min = floor
		}
	}
	if min.Sign() <= 0 || min.Cmp(out) > 0 {
		return ValidationOutput{}, ErrValidation
	}
	call, err := pons.BuildBuy(curve, amount, min, wallet, true)
	if i.Side == "sell" {
		call, err = pons.BuildSell(curve, amount, min, wallet)
	}
	if err != nil {
		return ValidationOutput{}, ErrValidation
	}
	maxFee, err := canonicalUint(l.MaxFee, false)
	if err != nil {
		return ValidationOutput{}, err
	}
	tipCap, err := canonicalUint(l.MaxTip, true)
	if err != nil {
		return ValidationOutput{}, err
	}
	if i.MaxFeeWei != "" {
		x, e := canonicalUint(i.MaxFeeWei, false)
		if e != nil || x.Cmp(maxFee) > 0 {
			return ValidationOutput{}, ErrValidation
		}
		maxFee = x
	}
	var tip *big.Int
	if i.TipWei != "" {
		tip, err = canonicalUint(i.TipWei, true)
	} else {
		var raw string
		err = v.client.rpc(ctx, "eth_maxPriorityFeePerGas", []any{}, &raw)
		if err == nil {
			tip, err = quantity(raw)
		}
	}
	if err != nil {
		return ValidationOutput{}, err
	}
	if tip.Cmp(tipCap) > 0 || tip.Cmp(maxFee) > 0 {
		return ValidationOutput{}, ErrValidation
	}
	if i.MaxFeeWei == "" {
		auto := new(big.Int).Add(new(big.Int).Mul(s.base, big.NewInt(2)), tip)
		if auto.Cmp(maxFee) > 0 {
			return ValidationOutput{}, ErrValidation
		}
		maxFee = auto
	}
	if maxFee.Cmp(new(big.Int).Add(s.base, tip)) < 0 {
		return ValidationOutput{}, ErrValidation
	}
	params := map[string]any{"from": r.Wallet, "to": strings.ToLower(call.To.Hex()), "value": hexutil.EncodeBig(call.Value), "data": hexutil.Encode(call.Data), "maxFeePerGas": hexutil.EncodeBig(maxFee), "maxPriorityFeePerGas": hexutil.EncodeBig(tip)}
	if err = v.ttl(i); err != nil {
		return ValidationOutput{}, err
	}
	var rawGas string
	if err = v.client.rpc(ctx, "eth_estimateGas", []any{params, blockParam(s)}, &rawGas); err != nil {
		return ValidationOutput{}, err
	}
	if err = v.ttl(i); err != nil {
		return ValidationOutput{}, err
	}
	gas, err := quantity(rawGas)
	if err != nil || !gas.IsUint64() || gas.Sign() <= 0 {
		return ValidationOutput{}, ErrObservation
	}
	gasCap, err := canonicalUint(l.MaxGas, false)
	if err != nil || gas.Cmp(gasCap) > 0 {
		return ValidationOutput{}, ErrValidation
	}
	inputCap, err := canonicalUint(l.MaxInput, false)
	if err != nil || amount.Cmp(inputCap) > 0 {
		return ValidationOutput{}, ErrValidation
	}
	budget := new(big.Int).Mul(gas, maxFee)
	if budget.BitLen() > 256 {
		return ValidationOutput{}, ErrValidation
	}
	if i.MaxTotalWei != "" {
		cap, err := canonicalUint(i.MaxTotalWei, false)
		if err != nil || budget.Cmp(cap) > 0 {
			return ValidationOutput{}, ErrValidation
		}
	}
	native, err := canonicalUint(r.NativeBalance, true)
	if err != nil {
		return ValidationOutput{}, err
	}
	retained, err := canonicalUint(l.MinNative, true)
	if err != nil {
		return ValidationOutput{}, err
	}
	required := new(big.Int).Add(budget, call.Value)
	required.Add(required, retained)
	if required.BitLen() > 256 || native.Cmp(required) < 0 {
		return ValidationOutput{}, ErrValidation
	}
	params["gas"] = hexutil.EncodeBig(gas)
	if err = v.ttl(i); err != nil {
		return ValidationOutput{}, err
	}
	var simulation string
	if err = v.client.rpc(ctx, "eth_call", []any{params, blockParam(s)}, &simulation); err != nil {
		return ValidationOutput{}, err
	}
	if err = v.ttl(i); err != nil {
		return ValidationOutput{}, err
	}
	ret, err := hexutil.Decode(simulation)
	if err != nil || len(ret) != 32 {
		return ValidationOutput{}, ErrObservation
	}
	actualOut, _ := wordUint(ret)
	if actualOut.Cmp(out) != 0 {
		return ValidationOutput{}, ErrObservation
	}
	if err = v.client.finish(ctx, s); err != nil {
		return ValidationOutput{}, err
	}
	if err = v.ttl(i); err != nil {
		return ValidationOutput{}, err
	}
	return ValidationOutput{Resolution: r, QuotedOut: out.String(), MinimumOut: min.String(), QuoteSpent: spent.String(), To: strings.ToLower(call.To.Hex()), From: r.Wallet, Value: call.Value.String(), Data: hexutil.Encode(call.Data), GasLimit: gas.Uint64(), GasFeeCap: maxFee.String(), GasTipCap: tip.String(), GasBudget: budget.String(), ReturnData: simulation}, nil
}
