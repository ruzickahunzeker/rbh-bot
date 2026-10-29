package config

import "testing"

func TestA1RecoveryOnlyProfileValidation(t *testing.T) {
	valid := Config{Service: TradeService, TradeStartupProfile: TradeProfileRecoveryOnly, CanaryProductionMode: CanaryProductionModeDisabled}
	if err := valid.ValidateTradeStartupProfile(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Config){
		"wrong_service": func(c *Config) { c.Service = BotService },
		"unknown":       func(c *Config) { c.TradeStartupProfile = "LIVE" },
		"live":          func(c *Config) { c.LiveEnabled = true },
		"mode":          func(c *Config) { c.CanaryProductionMode = "CONTROLLED_CANARY" },
		"api":           func(c *Config) { c.TradeAPIConfigFile = "must-not-be-opened" },
		"queries":       func(c *Config) { c.TradeAPIReadQueries = true },
		"intake":        func(c *Config) { c.TradeAPIManualIntake = true },
		"validate":      func(c *Config) { c.TradeAPIValidateOrders = true },
		"policy":        func(c *Config) { c.TradeAPIValidationPolicy = 1 },
		"admission":     func(c *Config) { c.TradeAPIManualAdmission = true },
		"key":           func(c *Config) { c.ExecutionPrivateKey = "must-not-be-parsed" },
		"wallet":        func(c *Config) { c.DryRunWalletID = "unreviewed" },
		"address":       func(c *Config) { c.DryRunFromAddress = "unreviewed" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			change(&cfg)
			if err := cfg.ValidateTradeStartupProfile(); err == nil {
				t.Fatal("incompatible capability accepted")
			}
		})
	}
	for _, profile := range []string{"", TradeProfileLegacy} {
		if err := (Config{Service: TradeService, TradeStartupProfile: profile, ExecutionPrivateKey: "legacy"}).ValidateTradeStartupProfile(); err != nil {
			t.Fatal("legacy contract changed", err)
		}
	}
}

func TestA1RecoveryOnlyProfileEnvironment(t *testing.T) {
	for _, name := range []string{"RBH_TRADE_STARTUP_PROFILE", "RBH_LIVE_ENABLED", "RBH_TRADE_API_CONFIG_FILE", "RBH_TRADE_API_READ_QUERIES", "RBH_TRADE_API_MANUAL_INTAKE", "RBH_TRADE_API_VALIDATE_ORDERS", "RBH_TRADE_API_VALIDATION_POLICY_VERSION", "RBH_TRADE_API_MANUAL_ADMISSION", "RBH_EXECUTION_PRIVATE_KEY", "RBH_DRY_RUN_WALLET_ID", "RBH_DRY_RUN_FROM_ADDRESS", "RBH_CONTROLLED_CANARY_PRODUCTION_MODE"} {
		t.Setenv(name, "")
	}
	cfg, err := Load(TradeService)
	if err != nil || cfg.TradeStartupProfile != TradeProfileLegacy {
		t.Fatal("legacy default changed", err)
	}
	t.Setenv("RBH_TRADE_STARTUP_PROFILE", TradeProfileRecoveryOnly)
	cfg, err = Load(TradeService)
	if err != nil || cfg.TradeStartupProfile != TradeProfileRecoveryOnly || cfg.ExecutionPrivateKey != "" {
		t.Fatal("recovery-only config requires signer", err)
	}
	if _, err := Load(FeedService); err == nil {
		t.Fatal("trade profile accepted on feed service")
	}
	t.Setenv("RBH_TRADE_STARTUP_PROFILE", "recovery_only")
	if _, err := Load(TradeService); err == nil {
		t.Fatal("noncanonical profile accepted")
	}
}
