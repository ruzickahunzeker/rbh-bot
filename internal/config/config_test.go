package config

import "testing"

func TestHTTPC2ValidationConfigurationFailClosed(t *testing.T) {
	t.Setenv("RBH_TRADE_API_VALIDATE_ORDERS", "")
	t.Setenv("RBH_TRADE_API_VALIDATION_POLICY_VERSION", "")
	t.Setenv("RBH_TRADE_API_MANUAL_INTAKE", "")
	t.Setenv("RBH_TRADE_API_CONFIG_FILE", "")
	cfg, err := Load(TradeService)
	if err != nil || cfg.TradeAPIValidateOrders || cfg.TradeAPIValidationPolicy != 0 {
		t.Fatal("validation not default off", err)
	}
	t.Setenv("RBH_TRADE_API_VALIDATE_ORDERS", "true")
	if _, err = Load(TradeService); err == nil {
		t.Fatal("validation without scoped intake")
	}
	t.Setenv("RBH_TRADE_API_MANUAL_INTAKE", "true")
	t.Setenv("RBH_TRADE_API_CONFIG_FILE", "/test-only/api.json")
	for _, value := range []string{"0", "-1", "abc", "9223372036854775808"} {
		t.Setenv("RBH_TRADE_API_VALIDATION_POLICY_VERSION", value)
		if _, err = Load(TradeService); err == nil {
			t.Fatal("invalid policy accepted", value)
		}
	}
	t.Setenv("RBH_TRADE_API_VALIDATION_POLICY_VERSION", "1")
	cfg, err = Load(TradeService)
	if err != nil || !cfg.TradeAPIValidateOrders || cfg.LiveEnabled {
		t.Fatal("validation configuration enabled live", err)
	}
	t.Setenv("RBH_TRADE_API_VALIDATE_ORDERS", "invalid")
	if _, err = Load(TradeService); err == nil {
		t.Fatal("malformed validation flag")
	}
}

func TestHTTPC1IntakeDefaultOffAndInvalidConfiguration(t *testing.T) {
	t.Setenv("RBH_TRADE_API_MANUAL_INTAKE", "")
	t.Setenv("RBH_TRADE_API_CONFIG_FILE", "")
	cfg, err := Load(TradeService)
	if err != nil || cfg.TradeAPIManualIntake {
		t.Fatal("intake not default off")
	}
	t.Setenv("RBH_TRADE_API_MANUAL_INTAKE", "true")
	if _, err = Load(TradeService); err == nil {
		t.Fatal("intake without authentication config")
	}
	t.Setenv("RBH_TRADE_API_CONFIG_FILE", "/test-only/api.json")
	cfg, err = Load(TradeService)
	if err != nil || !cfg.TradeAPIManualIntake || cfg.LiveEnabled {
		t.Fatal("intake enables live")
	}
	t.Setenv("RBH_TRADE_API_MANUAL_INTAKE", "invalid")
	if _, err = Load(TradeService); err == nil {
		t.Fatal("invalid intake flag accepted")
	}
}

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

func TestHTTPBReadQueriesDefaultOffAndMalformedRejected(t *testing.T) {
	t.Setenv("RBH_TRADE_API_READ_QUERIES", "false")
	cfg, err := Load(TradeService)
	if err != nil || cfg.TradeAPIReadQueries {
		t.Fatal("read queries enabled by default")
	}
	t.Setenv("RBH_TRADE_API_READ_QUERIES", "true")
	cfg, err = Load(TradeService)
	if err != nil || !cfg.TradeAPIReadQueries {
		t.Fatal("read query setting unavailable")
	}
	t.Setenv("RBH_TRADE_API_READ_QUERIES", "not-a-bool")
	if _, err = Load(TradeService); err == nil {
		t.Fatal("invalid query setting accepted")
	}
}
