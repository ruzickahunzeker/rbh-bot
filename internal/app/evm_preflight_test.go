package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainregistry"
	"github.com/ruzickahunzeker/rbh-bot/internal/config"
)

func TestEVMUnavailableChainRejectedBeforeDataOrRPCStartup(t *testing.T) {
	for _, chain := range []string{"56", "8453"} {
		for _, service := range []config.Service{config.TradeService, config.FeedService, config.BotService} {
			t.Run(chain+"/"+string(service), func(t *testing.T) {
				dataDir := filepath.Join(t.TempDir(), "must-not-be-created")
				t.Setenv("EVM_DATA_DIR", dataDir)
				t.Setenv("RBH_DATA_DIR", dataDir)
				t.Setenv("EVM_CHAIN_ID", chain)
				t.Setenv("RBH_CHAIN_ID", chain)
				t.Setenv("EVM_RPC_URL", "invalid-rpc-must-not-be-dialed")
				t.Setenv("ROBINHOOD_RPC_URL", "invalid-rpc-must-not-be-dialed")
				if err := Run(service); !errors.Is(err, chainregistry.ErrRuntimeUnavailable) {
					t.Fatalf("wrong preflight rejection: %v", err)
				}
				if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unavailable chain touched data directory: %v", err)
				}
			})
		}
	}
}
