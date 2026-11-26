package config

import (
	"strings"
	"testing"
)

func TestRBHEnvironmentAliasesAndConflicts(t *testing.T) {
	t.Setenv("RBH_CHAIN_ID", "4663")
	t.Setenv("EVM_CHAIN_ID", "")
	t.Setenv("RBH_RPC_URL", "https://example.invalid/rbh")
	t.Setenv("EVM_RPC_URL", "")
	t.Setenv("ROBINHOOD_RPC_URL", "")
	t.Setenv("RBH_WS_URL", "wss://example.invalid/rbh")
	t.Setenv("EVM_WS_URL", "")
	t.Setenv("RBH_READONLY_QUERY_ONLY", "true")
	t.Setenv("RBH_FEED_SOURCE", "rpc_ws")
	cfg, err := Load(FeedService)
	if err != nil || cfg.RPCURL != "https://example.invalid/rbh" || cfg.WSURL != "wss://example.invalid/rbh" || !cfg.ReadOnlyQueryOnly || cfg.FeedSource != "rpc_ws" {
		t.Fatalf("RBH aliases not loaded: %v", err)
	}
	t.Setenv("EVM_WS_URL", "wss://secret.invalid/credential")
	_, err = Load(FeedService)
	if err == nil || !strings.Contains(err.Error(), "RBH_WS_URL") || strings.Contains(err.Error(), "credential") {
		t.Fatalf("conflict not safely rejected: %v", err)
	}
}

func TestRBHEnvironmentRejectsInheritedOtherChain(t *testing.T) {
	t.Setenv("RBH_CHAIN_ID", "4663")
	t.Setenv("EVM_CHAIN_ID", "56")
	if _, err := Load(FeedService); err == nil {
		t.Fatal("inherited chain collision accepted")
	}
}

func TestRPCWSConfigFailsClosed(t *testing.T) {
	t.Setenv("RBH_CHAIN_ID", "4663")
	t.Setenv("EVM_CHAIN_ID", "")
	t.Setenv("RBH_FEED_SOURCE", "rpc_ws")
	t.Setenv("RBH_RPC_URL", "https://example.invalid")
	t.Setenv("EVM_RPC_URL", "")
	t.Setenv("ROBINHOOD_RPC_URL", "")
	t.Setenv("RBH_WS_URL", "")
	t.Setenv("EVM_WS_URL", "")
	if _, err := Load(FeedService); err == nil {
		t.Fatal("missing WS accepted")
	}
	t.Setenv("RBH_WS_URL", "wss://example.invalid")
	t.Setenv("RBH_FEED_GAP_POLICY", "skip")
	if _, err := Load(FeedService); err == nil {
		t.Fatal("implicit history skip accepted")
	}
}
