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
}

func TestLoadRejectsWrongChain(t *testing.T) {
	t.Setenv("RBH_CHAIN_ID", "1")
	if _, err := Load(FeedService); err == nil {
		t.Fatal("expected wrong chain to be rejected")
	}
}
