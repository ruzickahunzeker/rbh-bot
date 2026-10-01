package config

import "testing"

func TestLoadDefaultsFailClosed(t *testing.T) {
	t.Setenv("RBH_DATA_DIR", t.TempDir())
	t.Setenv("RBH_SOCKET_DIR", t.TempDir())
	t.Setenv("RBH_CHAIN_ID", "4663")
	t.Setenv("RBH_LIVE_ENABLED", "false")
	cfg, err := Load(TradeService)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChainID != ChainID || cfg.LiveEnabled {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if cfg.CanaryProductionMode != CanaryProductionModeDisabled {
		t.Fatalf("controlled canary production must default disabled: %#v", cfg)
	}
}

func TestLoadRejectsControlledCanaryProductionEnablementInW4A(t *testing.T) {
	t.Setenv("RBH_CONTROLLED_CANARY_PRODUCTION_MODE", "CONTROLLED_CANARY")
	if _, err := Load(TradeService); err == nil {
		t.Fatal("expected W4-A production enablement to fail closed")
	}
}

func TestLoadRejectsWrongChain(t *testing.T) {
	t.Setenv("RBH_CHAIN_ID", "1")
	if _, err := Load(FeedService); err == nil {
		t.Fatal("expected wrong chain to be rejected")
	}
}
