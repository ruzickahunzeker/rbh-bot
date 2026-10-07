package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainregistry"
)

var aliasTestPairs = []struct {
	current, legacy, value string
}{
	{"EVM_DATA_DIR", "RBH_DATA_DIR", "/tmp/evm-config-fixture/data"},
	{"EVM_SOCKET_DIR", "RBH_SOCKET_DIR", "/tmp/evm-config-fixture/sockets"},
	{"EVM_LOG_LEVEL", "RBH_LOG_LEVEL", "debug"},
	{"EVM_CHAIN_ID", "RBH_CHAIN_ID", "4663"},
	{"EVM_LIVE_ENABLED", "RBH_LIVE_ENABLED", "false"},
	{"EVM_CONTROLLED_CANARY_PRODUCTION_MODE", "RBH_CONTROLLED_CANARY_PRODUCTION_MODE", "DISABLED"},
	{"EVM_INTERNAL_AUTH_SECRET", "RBH_INTERNAL_AUTH_SECRET", "fixture-auth-value-never-print"},
	{"EVM_RPC_URL", "ROBINHOOD_RPC_URL", "http://127.0.0.1:1/fixture-only"},
	{"EVM_DRY_RUN_WALLET_ID", "RBH_DRY_RUN_WALLET_ID", "wallet-fixture"},
	{"EVM_DRY_RUN_FROM_ADDRESS", "RBH_DRY_RUN_FROM_ADDRESS", "0x1111111111111111111111111111111111111111"},
	{"EVM_EXECUTION_PRIVATE_KEY", "RBH_EXECUTION_PRIVATE_KEY", "fixture-key-not-a-private-key-never-print"},
	{"EVM_ARTIFACT_ENCRYPTION_KEY", "RBH_ARTIFACT_ENCRYPTION_KEY", "fixture-artifact-key-never-print"},
	{"EVM_ARTIFACT_KEY_VERSION", "RBH_ARTIFACT_KEY_VERSION", "fixture-version"},
}

// t.Setenv records restoration; Unsetenv then models absence rather than an
// explicitly empty new setting. No parallel tests may mutate process config.
func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, pair := range aliasTestPairs {
		for _, key := range []string{pair.current, pair.legacy} {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestEVMConfigLegacyAndNewAliasesAreEquivalent(t *testing.T) {
	for _, service := range []Service{FeedService, BotService, TradeService} {
		t.Run(string(service), func(t *testing.T) {
			clearConfigEnvironment(t)
			for _, pair := range aliasTestPairs {
				t.Setenv(pair.legacy, pair.value)
			}
			legacy, err := Load(service)
			if err != nil {
				t.Fatal(err)
			}
			clearConfigEnvironment(t)
			for _, pair := range aliasTestPairs {
				t.Setenv(pair.current, pair.value)
			}
			current, err := Load(service)
			if err != nil || !reflect.DeepEqual(legacy, current) {
				t.Fatalf("configuration differs (values redacted), err=%v", err)
			}
			for _, pair := range aliasTestPairs {
				t.Setenv(pair.legacy, pair.value)
			}
			both, err := Load(service)
			if err != nil || !reflect.DeepEqual(legacy, both) {
				t.Fatalf("equal aliases rejected (values redacted), err=%v", err)
			}
		})
	}
}

func TestEVMConfigConflictingAliasesFailClosedWithoutValues(t *testing.T) {
	for _, pair := range aliasTestPairs {
		t.Run(pair.current, func(t *testing.T) {
			clearConfigEnvironment(t)
			conflict := pair.value + "-conflicting-fixture"
			t.Setenv(pair.current, pair.value)
			t.Setenv(pair.legacy, conflict)
			_, err := Load(TradeService)
			if err == nil || !strings.Contains(err.Error(), pair.current) || !strings.Contains(err.Error(), pair.legacy) {
				t.Fatalf("alias conflict not rejected: %v", err)
			}
			if strings.Contains(err.Error(), pair.value) || strings.Contains(err.Error(), conflict) {
				t.Fatal("configuration conflict leaked a value")
			}
		})
	}
}

func TestEVMConfigNoNewDefaultsChangeStorageOrServiceIdentity(t *testing.T) {
	clearConfigEnvironment(t)
	for _, service := range []Service{FeedService, BotService, TradeService} {
		cfg, err := Load(service)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Service != service || cfg.ChainID != chainregistry.RobinhoodID || cfg.DataDir != "./data" || cfg.SocketDir != "data/run" || cfg.Database != filepath.Join("data", strings.TrimSuffix(string(service), "-service")+".db") || cfg.Socket != filepath.Join("data/run", string(service)+".sock") || cfg.ArtifactKeyVersion != "v1" || cfg.LiveEnabled || cfg.CanaryProductionMode != CanaryProductionModeDisabled {
			t.Fatal("legacy storage/service or disabled identity changed")
		}
	}
	if _, err := Load(Service("../other-chain")); err == nil {
		t.Fatal("unregistered service accepted")
	}
}

func TestEVMConfigUnavailableChainCannotSelectLegacyRuntime(t *testing.T) {
	for _, service := range []Service{FeedService, BotService, TradeService} {
		for _, key := range []string{"EVM_CHAIN_ID", "RBH_CHAIN_ID"} {
			for _, value := range []string{"56", "8453", "1", "97", "0", "invalid", "18446744073709551616"} {
				t.Run(string(service)+"/"+key+"/"+value, func(t *testing.T) {
					clearConfigEnvironment(t)
					t.Setenv(key, value)
					if cfg, err := Load(service); err == nil || cfg != (Config{}) {
						t.Fatal("unavailable chain returned usable runtime config")
					}
				})
			}
		}
	}
}

func TestEVMConfigExplicitEmptyAndLegacyDefaults(t *testing.T) {
	for _, pair := range aliasTestPairs {
		switch pair.current {
		case "EVM_DATA_DIR", "EVM_SOCKET_DIR", "EVM_LOG_LEVEL", "EVM_CHAIN_ID", "EVM_LIVE_ENABLED", "EVM_CONTROLLED_CANARY_PRODUCTION_MODE", "EVM_ARTIFACT_KEY_VERSION":
			t.Run(pair.current, func(t *testing.T) {
				clearConfigEnvironment(t)
				t.Setenv(pair.current, "")
				if _, err := Load(TradeService); err == nil {
					t.Fatal("explicitly empty defaulted setting accepted")
				}
				clearConfigEnvironment(t)
				t.Setenv(pair.legacy, "")
				if _, err := Load(TradeService); err != nil {
					t.Fatalf("legacy empty no longer uses default: %v", err)
				}
			})
		}
	}
	clearConfigEnvironment(t)
	for _, key := range []string{"EVM_INTERNAL_AUTH_SECRET", "EVM_RPC_URL", "EVM_EXECUTION_PRIVATE_KEY", "EVM_ARTIFACT_ENCRYPTION_KEY"} {
		t.Setenv(key, "")
	}
	if _, err := Load(TradeService); err != nil {
		t.Fatal("optional fields must remain subject to existing startup checks")
	}
}

func TestEVMConfigCannotEnableLiveOrCanary(t *testing.T) {
	for _, key := range []string{"EVM_LIVE_ENABLED", "RBH_LIVE_ENABLED"} {
		for _, value := range []string{"true", "1", "TRUE", "malformed"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				clearConfigEnvironment(t)
				t.Setenv(key, value)
				if _, err := Load(TradeService); err == nil {
					t.Fatal("live configuration accepted")
				}
			})
		}
	}
	for _, key := range []string{"EVM_CONTROLLED_CANARY_PRODUCTION_MODE", "RBH_CONTROLLED_CANARY_PRODUCTION_MODE"} {
		for _, value := range []string{"CONTROLLED_CANARY", "UNRESTRICTED_LIVE", "unknown"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				clearConfigEnvironment(t)
				t.Setenv(key, value)
				if _, err := Load(TradeService); err == nil {
					t.Fatal("production enablement accepted")
				}
			})
		}
	}
}
