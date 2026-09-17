package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const ChainID uint64 = 4663

type Service string

const (
	FeedService  Service = "feed-service"
	BotService   Service = "bot-service"
	TradeService Service = "trade-service"
)

type Config struct {
	Service            Service
	DataDir            string
	SocketDir          string
	Database           string
	Socket             string
	LogLevel           string
	ChainID            uint64
	LiveEnabled        bool
	InternalAuthSecret string
	RPCURL             string
	DryRunWalletID     string
	DryRunFromAddress  string
}

func Load(service Service) (Config, error) {
	dataDir := env("RBH_DATA_DIR", "./data")
	socketDir := env("RBH_SOCKET_DIR", filepath.Join(dataDir, "run"))
	chainID, err := strconv.ParseUint(env("RBH_CHAIN_ID", "4663"), 10, 64)
	if err != nil || chainID != ChainID {
		return Config{}, fmt.Errorf("RBH_CHAIN_ID must be %d", ChainID)
	}
	live, err := strconv.ParseBool(env("RBH_LIVE_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("RBH_LIVE_ENABLED: %w", err)
	}
	name := strings.TrimSuffix(string(service), "-service")
	return Config{
		Service: service, DataDir: dataDir, SocketDir: socketDir,
		Database: filepath.Join(dataDir, name+".db"),
		Socket:   filepath.Join(socketDir, string(service)+".sock"),
		LogLevel: strings.ToUpper(env("RBH_LOG_LEVEL", "INFO")),
		ChainID:  chainID, LiveEnabled: live,
		InternalAuthSecret: os.Getenv("RBH_INTERNAL_AUTH_SECRET"),
		RPCURL:             os.Getenv("ROBINHOOD_RPC_URL"),
		DryRunWalletID:     os.Getenv("RBH_DRY_RUN_WALLET_ID"),
		DryRunFromAddress:  os.Getenv("RBH_DRY_RUN_FROM_ADDRESS"),
	}, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
