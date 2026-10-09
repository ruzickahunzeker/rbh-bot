package manualrisk

import (
	"go/parser"
	"go/token"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
)

const testAsset = "0x2000000000000000000000000000000000000002"

func TestManualRiskNoCrossAssetChainDecimalsOrKindAddition(t *testing.T) {
	a := Quantity{Unit{4663, "native", 18, TradeInput}, "5"}
	if sum, err := Add(a, a); err != nil || sum.Units != "10" {
		t.Fatal(sum, err)
	}
	for _, u := range []Unit{
		{4663, testAsset, 6, TradeInput}, {4663, "native", 6, TradeInput},
		{8453, "native", 18, TradeInput}, {4663, "native", 18, NativeGas},
		{4663, "native", 18, "UNKNOWN"}, {4663, "0x" + strings.Repeat("0", 40), 18, TradeInput},
		{4663, "0x" + strings.Repeat("G", 40), 18, TradeInput},
	} {
		if _, err := Add(a, Quantity{u, "5"}); err == nil {
			t.Fatal("unlike/invalid units added", u)
		}
	}
	for _, d := range []uint8{0, 6, 18, 255} {
		if (Unit{4663, testAsset, d, TradeInput}).Validate() != nil {
			t.Fatal("explicit token decimals rejected", d)
		}
	}
}

func TestManualRiskUint256BoundaryCanonicalAndCaps(t *testing.T) {
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).String()
	u := Unit{4663, "native", 18, TradeInput}
	if CheckCap(Quantity{u, "9"}, Quantity{u, "1"}, Quantity{u, "10"}) != nil {
		t.Fatal("equality rejected")
	}
	if CheckCap(Quantity{u, "9"}, Quantity{u, "2"}, Quantity{u, "10"}) == nil {
		t.Fatal("+1 over cap accepted")
	}
	if _, err := Add(Quantity{u, max}, Quantity{u, "0"}); err != nil {
		t.Fatal("max256 rejected")
	}
	if _, err := Add(Quantity{u, max}, Quantity{u, "1"}); err == nil {
		t.Fatal("overflow accepted")
	}
	for _, bad := range []string{"", "00", "01", "+1", "-1", "1.0", "1e3", " 1", strings.Repeat("9", 79)} {
		if _, err := Add(Quantity{u, bad}, Quantity{u, "0"}); err == nil {
			t.Fatal("noncanonical input accepted", bad)
		}
	}
	if CheckCap(Quantity{u, "0"}, Quantity{u, "0"}, Quantity{u, "0"}) == nil {
		t.Fatal("zero cap accepted")
	}
}

func TestManualRiskZeroTipGasNativeOutlayRetainedBalance(t *testing.T) {
	gas, err := GasBudget("100000", "1000000000", "0")
	if err != nil || gas.Units != "100000000000000" {
		t.Fatal("zero tip or gas derivation", gas, err)
	}
	value := Quantity{Unit{4663, "native", 18, NativeValue}, "10000000000000000"}
	outlay, err := Outlay(value, gas)
	if err != nil || outlay.Units != "10100000000000000" || outlay.Unit.Kind != NativeOutlay {
		t.Fatal(outlay, err)
	}
	if CheckRetainedBalance("10100000000000001", outlay.Units, "1") != nil || CheckRetainedBalance("10100000000000000", outlay.Units, "1") == nil {
		t.Fatal("retained balance boundary")
	}
	if sell, err := Outlay(Quantity{value.Unit, "0"}, gas); err != nil || sell.Units != gas.Units {
		t.Fatal("sell included token input", sell, err)
	}
	if _, err := Outlay(Quantity{Unit{4663, testAsset, 6, TradeInput}, "1"}, gas); err == nil {
		t.Fatal("token input added to gas")
	}
	for _, tc := range [][3]string{{"0", "1", "0"}, {"1", "1", "2"}, {"1", "01", "0"}, {"18446744073709551616", "1", "0"}, {"2", new(big.Int).Lsh(big.NewInt(1), 255).String(), "0"}} {
		if _, err := GasBudget(tc[0], tc[1], tc[2]); err == nil {
			t.Fatal("invalid fee/gas/overflow accepted", tc)
		}
	}
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).String()
	if CheckRetainedBalance(max, max, "1") == nil {
		t.Fatal("retained addition overflow accepted")
	}
}

func TestManualRiskFixedSellExactCrossMultiplication(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		bps  uint64
		pass bool
	}{{"750250000", "1500500000", 5000, true}, {"750250001", "1500500000", 5000, false}, {"1", "3", 3333, false}, {"1", "3", 3334, true}, {"1", "1", 10000, true}, {"1", "1", 0, false}, {"0", "1", 5000, false}, {"1", "0", 10000, false}} {
		if err := CheckFixedSell(tc.a, tc.b, tc.bps); (err == nil) != tc.pass {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).String()
	if err := CheckFixedSell(max, max, 10000); err != nil {
		t.Fatal("intermediate exact product incorrectly uint256 bounded", err)
	}
}

func TestManualRiskSingleUnitAuthorizationBudgetPureCheck(t *testing.T) {
	u := Unit{4663, "native", 18, TradeInput}
	b := Budget{u, 2, "10"}
	if b.Check(1, Quantity{u, "9"}, Quantity{u, "1"}) != nil {
		t.Fatal("exact count/amount cap rejected")
	}
	for _, tc := range []struct {
		count       uint64
		used, input Quantity
	}{{2, Quantity{u, "9"}, Quantity{u, "1"}}, {1, Quantity{u, "9"}, Quantity{u, "2"}}, {1, Quantity{u, "9"}, Quantity{Unit{4663, testAsset, 6, TradeInput}, "1"}}, {1, Quantity{u, "9"}, Quantity{u, "0"}}, {^uint64(0), Quantity{u, "0"}, Quantity{u, "1"}}} {
		if b.Check(tc.count, tc.used, tc.input) == nil {
			t.Fatal("budget overrun/unit mismatch accepted", tc)
		}
	}
}

func TestManualRiskHasOnlyPureStandardLibraryDependencies(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"\"encoding/hex\"": true, "\"errors\"": true, "\"math/big\"": true, "\"strings\"": true, "\"bytes\"": true, "\"crypto/sha256\"": true, "\"encoding/json\"": true}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if !allowed[imp.Path.Value] {
				t.Errorf("non-pure dependency %s in %s", imp.Path.Value, name)
			}
		}
	}
}
