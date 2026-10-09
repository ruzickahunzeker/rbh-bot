package manualrisk

import (
	"strings"
	"testing"
)

func testPolicy() Policy {
	return Policy{ID: "manual-policy-1", SourcePolicyVersion: 1, SourcePolicyHash: strings.Repeat("a", 64), ChainID: 4663, Protocol: "PONS_V2_CURVE",
		InputLimits:              []InputLimit{{Unit{4663, testAsset, 6, TradeInput}, "100", "200", "300"}, {Unit{4663, "native", 18, TradeInput}, "100", "200", "300"}},
		MaxNativeGasPerOperation: "10", MaxNativeWalletOutlay: "200", MaxNativeGlobalOutlay: "300", MinNativeBalance: "0", MaxSellBPS: 5000,
		MaxWalletOperationsWindow: 10, MaxGlobalOperationsWindow: 100, WindowSeconds: 3600}
}

func TestManualRiskCanonicalPolicyHashIdentityAndUnitLookup(t *testing.T) {
	p := testPolicy()
	raw, hash, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ParsePolicy(raw, hash, p.SourcePolicyVersion, p.SourcePolicyHash)
	if err != nil || loaded.ID != p.ID {
		t.Fatal(err)
	}
	if _, err := loaded.Limit(Unit{4663, testAsset, 6, TradeInput}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []Unit{{4663, testAsset, 18, TradeInput}, {4663, testAsset, 6, NativeGas}, {4663, "0x3000000000000000000000000000000000000003", 6, TradeInput}} {
		if _, err := loaded.Limit(u); err == nil {
			t.Fatal("missing/mismatched limit accepted", u)
		}
	}
	for _, tc := range [][4]string{{" " + raw, hash, "1", p.SourcePolicyHash}, {raw + raw, hash, "1", p.SourcePolicyHash}, {raw, strings.Repeat("0", 64), "1", p.SourcePolicyHash}, {raw, hash, "1", strings.Repeat("b", 64)}, {strings.Replace(raw, `"id":`, `"unknown":0,"id":`, 1), hash, "1", p.SourcePolicyHash}} {
		if _, err := ParsePolicy(tc[0], tc[1], 1, tc[3]); err == nil {
			t.Fatal("noncanonical/unknown/conflicting policy accepted")
		}
	}
	if _, err := ParsePolicy(raw, hash, 2, p.SourcePolicyHash); err == nil {
		t.Fatal("source version drift accepted")
	}
}

func TestManualRiskPolicyRejectsUnknownUnitsDuplicateUnsortedAndInvalidCaps(t *testing.T) {
	for _, mutate := range []func(*Policy){
		func(p *Policy) { p.ID = "bad id" }, func(p *Policy) { p.ChainID = 8453 },
		func(p *Policy) { p.Protocol = "FLAP" }, func(p *Policy) { p.SourcePolicyVersion = 0 },
		func(p *Policy) { p.SourcePolicyHash = strings.Repeat("A", 64) },
		func(p *Policy) { p.InputLimits = nil },
		func(p *Policy) { p.InputLimits[0].Unit.Kind = NativeGas },
		func(p *Policy) { p.InputLimits[0].MaxOperation = "201" },
		func(p *Policy) { p.InputLimits[0].MaxWalletActive = "301" },
		func(p *Policy) { p.InputLimits[0].MaxOperation = "0" },
		func(p *Policy) { p.InputLimits[0].MaxGlobalActive = "01" },
		func(p *Policy) { p.InputLimits[1] = p.InputLimits[0] },
		func(p *Policy) { p.InputLimits[0], p.InputLimits[1] = p.InputLimits[1], p.InputLimits[0] },
		func(p *Policy) { p.MaxSellBPS = 10001 }, func(p *Policy) { p.WindowSeconds = 0 },
		func(p *Policy) { p.WindowSeconds = 31536001 }, func(p *Policy) { p.MaxWalletOperationsWindow = 0 },
		func(p *Policy) { p.MaxGlobalOperationsWindow = 9 },
		func(p *Policy) { p.MaxGlobalOperationsWindow = 1 << 63 },
		func(p *Policy) { p.MaxNativeGasPerOperation = "201" },
		func(p *Policy) { p.MinNativeBalance = "-1" },
	} {
		p := testPolicy()
		mutate(&p)
		if _, _, err := p.Canonical(); err == nil {
			t.Fatal("invalid policy accepted", p)
		}
	}
}
