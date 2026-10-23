package chainquery

import (
	"math/big"
	"strings"

	pons "github.com/0xfnzero/rbh-trade-sdk/adapters/pons"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// CheckResolution independently verifies arithmetic at the persistence
// boundary, even if an internal provider adapter is faulty or contradictory.
func CheckResolution(i ValidationIntent, r Resolution) error {
	if r.Wallet != strings.ToLower(i.Wallet) || r.Token.Address != strings.ToLower(i.Token) || r.Side != i.Side || r.Token.Chain != "robinhood" || r.Token.Protocol != "pons_v2_curve" || r.Token.QuoteAsset != "native" || r.Token.SendAuthorized || r.Token.Factory != FactoryAddress || r.Token.Block.Number == 0 || r.Token.Block.Number > 1<<63-1 || i.SlippageBPS >= 10000 {
		return ErrValidation
	}
	for _, address := range []string{r.Wallet, r.Token.Address, r.Token.Curve} {
		if _, err := Address(address); err != nil || address != strings.ToLower(address) {
			return ErrValidation
		}
	}
	for _, value := range []string{r.Token.Block.Hash, r.Token.RuntimeCodeHash, r.Token.CurveRuntimeCodeHash} {
		if len(value) != 66 || !strings.HasPrefix(value, "0x") || common.HexToHash(value) == (common.Hash{}) || common.HexToHash(value).Hex() != value {
			return ErrValidation
		}
	}
	balance, err := canonicalUint(r.TokenBalance, true)
	if err != nil {
		return err
	}
	allowance, err := canonicalUint(r.Allowance, true)
	if err != nil {
		return err
	}
	native, err := canonicalUint(r.NativeBalance, true)
	if err != nil {
		return err
	}
	amount, err := canonicalUint(r.AmountIn, false)
	if err != nil {
		return err
	}
	if i.Side != "buy" && i.Side != "sell" || (i.Amount == "") == (i.SellBPS == 0) || i.SellBPS > 10000 || (i.Side == "buy" && i.SellBPS != 0) {
		return ErrValidation
	}
	var expected *big.Int
	if i.SellBPS != 0 {
		expected = new(big.Int).Div(new(big.Int).Mul(balance, new(big.Int).SetUint64(i.SellBPS)), big.NewInt(10000))
	} else {
		d := r.Token.Decimals
		if i.Side == "buy" {
			d = 18
		}
		expected, err = human(i.Amount, d)
		if err != nil {
			return err
		}
	}
	if amount.Cmp(expected) != 0 || (i.Side == "sell" && (balance.Cmp(amount) < 0 || allowance.Cmp(amount) < 0)) || (i.Side == "buy" && native.Cmp(amount) < 0) {
		return ErrValidation
	}
	if i.MinReceive == "" {
		if r.MinReceiveUnits != "" {
			return ErrValidation
		}
	} else {
		d := uint8(18)
		if i.Side == "buy" {
			d = r.Token.Decimals
		}
		floor, err := human(i.MinReceive, d)
		if err != nil || floor.String() != r.MinReceiveUnits {
			return ErrValidation
		}
	}
	if i.TipWei != "" && i.MaxFeeWei == "" {
		return ErrValidation
	}
	return nil
}

func CheckValidation(i ValidationIntent, r Resolution, l ValidationLimits, o ValidationOutput) error {
	if err := CheckResolution(i, r); err != nil {
		return err
	}
	if o.Resolution != r || o.GasLimit == 0 || o.From != r.Wallet || o.To != r.Token.Curve {
		return ErrValidation
	}
	amount, _ := canonicalUint(r.AmountIn, false)
	out, err := canonicalUint(o.QuotedOut, false)
	if err != nil {
		return err
	}
	minimum, err := canonicalUint(o.MinimumOut, false)
	if err != nil || minimum.Cmp(out) > 0 {
		return ErrValidation
	}
	floor := new(big.Int).Div(new(big.Int).Mul(out, new(big.Int).SetUint64(10000-i.SlippageBPS)), big.NewInt(10000))
	if r.MinReceiveUnits != "" {
		user, _ := canonicalUint(r.MinReceiveUnits, false)
		if user.Cmp(floor) > 0 {
			floor = user
		}
	}
	if minimum.Cmp(floor) != 0 {
		return ErrValidation
	}
	curve := common.HexToAddress(r.Token.Curve)
	wallet := common.HexToAddress(r.Wallet)
	call, err := pons.BuildBuy(curve, amount, minimum, wallet, true)
	if i.Side == "sell" {
		call, err = pons.BuildSell(curve, amount, minimum, wallet)
	}
	if err != nil || o.Value != call.Value.String() || o.Data != hexutil.Encode(call.Data) {
		return ErrValidation
	}
	spent, err := canonicalUint(o.QuoteSpent, true)
	if err != nil || (i.Side == "buy" && spent.Cmp(amount) != 0) || (i.Side == "sell" && spent.Sign() != 0) {
		return ErrValidation
	}
	ret, err := hexutil.Decode(o.ReturnData)
	if err != nil || len(ret) != 32 || new(big.Int).SetBytes(ret).Cmp(out) != 0 {
		return ErrObservation
	}
	fee, err := canonicalUint(o.GasFeeCap, false)
	if err != nil {
		return err
	}
	tip, err := canonicalUint(o.GasTipCap, true)
	if err != nil || tip.Cmp(fee) > 0 {
		return ErrValidation
	}
	max, err := canonicalUint(l.MaxFee, false)
	if err != nil || fee.Cmp(max) > 0 {
		return ErrValidation
	}
	maxTip, err := canonicalUint(l.MaxTip, true)
	if err != nil || tip.Cmp(maxTip) > 0 {
		return ErrValidation
	}
	if i.MaxFeeWei != "" {
		user, err := canonicalUint(i.MaxFeeWei, false)
		if err != nil || fee.Cmp(user) != 0 {
			return ErrValidation
		}
	}
	if i.TipWei != "" {
		user, err := canonicalUint(i.TipWei, true)
		if err != nil || tip.Cmp(user) != 0 {
			return ErrValidation
		}
	}
	gasCap, err := canonicalUint(l.MaxGas, false)
	if err != nil || new(big.Int).SetUint64(o.GasLimit).Cmp(gasCap) > 0 {
		return ErrValidation
	}
	inputCap, err := canonicalUint(l.MaxInput, false)
	if err != nil || amount.Cmp(inputCap) > 0 {
		return ErrValidation
	}
	budget := new(big.Int).Mul(new(big.Int).SetUint64(o.GasLimit), fee)
	if budget.BitLen() > 256 || budget.String() != o.GasBudget {
		return ErrValidation
	}
	if i.MaxTotalWei != "" {
		cap, err := canonicalUint(i.MaxTotalWei, false)
		if err != nil || budget.Cmp(cap) > 0 {
			return ErrValidation
		}
	}
	native, _ := canonicalUint(r.NativeBalance, true)
	retained, err := canonicalUint(l.MinNative, true)
	if err != nil {
		return err
	}
	need := new(big.Int).Add(budget, call.Value)
	need.Add(need, retained)
	if need.BitLen() > 256 || native.Cmp(need) < 0 {
		return ErrValidation
	}
	return nil
}
