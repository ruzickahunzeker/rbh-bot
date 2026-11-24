package config

import (
	"strings"
	"testing"
	"time"
)

func TestValidateTimeoutsEnforcesInequality(t *testing.T) {
	valid := Config{
		PreSendRPCBudget:    5 * time.Second,
		BroadcastRPCTimeout: 10 * time.Second,
		HTTPWriteTimeout:    20 * time.Second, // > 5+10+2 = 17s
		BotClientTimeout:    25 * time.Second, // > 20s
	}
	if err := valid.ValidateTimeouts(); err != nil {
		t.Fatalf("expected valid timeouts to pass: %v", err)
	}

	badWrite := valid
	badWrite.HTTPWriteTimeout = 17 * time.Second
	if err := badWrite.ValidateTimeouts(); err == nil {
		t.Fatal("expected HTTPWriteTimeout <= pre_send+broadcast+2s to fail")
	}

	badBot := valid
	badBot.BotClientTimeout = 20 * time.Second
	if err := badBot.ValidateTimeouts(); err == nil {
		t.Fatal("expected BotClientTimeout <= HTTPWriteTimeout to fail")
	}
}

func TestParseArtifactKeyring(t *testing.T) {
	cfg := Config{
		ArtifactKeyVersion:     "v2",
		ArtifactEncryptionKey:  strings.Repeat("22", 32),
		ArtifactHistoricalKeys: "v1=" + strings.Repeat("11", 32),
	}
	ring, err := cfg.ParseArtifactKeyring()
	if err != nil {
		t.Fatalf("ParseArtifactKeyring failed: %v", err)
	}
	if len(ring) != 2 || len(ring["v1"]) != 32 || len(ring["v2"]) != 32 {
		t.Fatalf("unexpected keyring: %v", ring)
	}

	dup := cfg
	dup.ArtifactHistoricalKeys = "v2=" + strings.Repeat("33", 32)
	if _, err := dup.ParseArtifactKeyring(); err == nil {
		t.Fatal("expected duplicate version in keyring to fail")
	}
}

func TestTradeProfileQueryAndRecoveryValidation(t *testing.T) {
	valid := Config{
		Service:              TradeService,
		TradeStartupProfile:  TradeProfileQueryAndRecovery,
		CanaryProductionMode: CanaryProductionModeDisabled,
		TradeAPIConfigFile:   "/tmp/trade-api.json",
	}
	if err := valid.ValidateTradeStartupProfile(); err != nil {
		t.Fatalf("expected valid QUERY_AND_RECOVERY config to pass: %v", err)
	}

	cases := map[string]func(*Config){
		"missing_api_config": func(c *Config) { c.TradeAPIConfigFile = "" },
		"private_key":        func(c *Config) { c.ExecutionPrivateKey = "deadbeef" },
		"manual_intake":      func(c *Config) { c.TradeAPIManualIntake = true },
		"manual_admission":   func(c *Config) { c.TradeAPIManualAdmission = true },
		"live_enabled":       func(c *Config) { c.LiveEnabled = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := valid
			mutate(&c)
			if err := c.ValidateTradeStartupProfile(); err == nil {
				t.Fatalf("expected %s to fail closed", name)
			}
		})
	}
}
