package rbhparser

import "testing"

func TestSupportedProtocolCapabilities(t *testing.T) {
	protocols := SupportedProtocols()
	if len(protocols) != 11 {
		t.Fatalf("supported protocol count = %d", len(protocols))
	}
	protocols[0].Protocol = ProtocolUnknown
	if SupportedProtocols()[0].Protocol != ProtocolPonsV2 {
		t.Fatal("SupportedProtocols returned mutable package state")
	}
	for _, name := range []string{"pons-v2", "LONG", "bankr", "long.xyz", " o1 ", "pools.trade", "pair", "bags-v2", "letscash", "flap-tax", "flap-stocks", "varo", "virtuals"} {
		protocol, err := ParseProtocol(name)
		if err != nil || !protocol.Valid() {
			t.Fatalf("ParseProtocol(%q) = %s, %v", name, protocol, err)
		}
	}
	if _, err := ParseProtocol("unknown"); err == nil {
		t.Fatal("unknown protocol was accepted")
	}
	if !SupportedProtocols()[0].Capabilities.PendingCurveIntents || SupportedProtocols()[1].Capabilities.PendingCurveIntents {
		t.Fatal("curve intent capabilities do not reflect protocol behavior")
	}
}
