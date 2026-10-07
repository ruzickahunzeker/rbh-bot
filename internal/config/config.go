package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainregistry"
)

// ChainID remains the legacy Robinhood runtime identity, not a configurable
// global chain constant for future workers.
const ChainID uint64 = chainregistry.RobinhoodID

const CanaryProductionModeDisabled = "DISABLED"

type Service string

const (
	FeedService  Service = "feed-service"
	BotService   Service = "bot-service"
	TradeService Service = "trade-service"
)

type Config struct {
	Service               Service
	DataDir               string
	SocketDir             string
	Database              string
	Socket                string
	LogLevel              string
	ChainID               uint64
	LiveEnabled           bool
	InternalAuthSecret    string
	RPCURL                string
	DryRunWalletID        string
	DryRunFromAddress     string
	ExecutionPrivateKey   string
	ArtifactEncryptionKey string
	ArtifactKeyVersion    string
	CanaryProductionMode  string
}

func Load(service Service) (Config, error) {
	if service != FeedService && service != BotService && service != TradeService {
		return Config{}, fmt.Errorf("unknown EVM service")
	}
	settings := []setting{
		{"EVM_DATA_DIR", "RBH_DATA_DIR", "./data"},
		{"EVM_SOCKET_DIR", "RBH_SOCKET_DIR", ""},
		{"EVM_LOG_LEVEL", "RBH_LOG_LEVEL", "INFO"},
		{"EVM_CHAIN_ID", "RBH_CHAIN_ID", "4663"},
		{"EVM_LIVE_ENABLED", "RBH_LIVE_ENABLED", "false"},
		{"EVM_CONTROLLED_CANARY_PRODUCTION_MODE", "RBH_CONTROLLED_CANARY_PRODUCTION_MODE", CanaryProductionModeDisabled},
		{"EVM_INTERNAL_AUTH_SECRET", "RBH_INTERNAL_AUTH_SECRET", ""},
		{"EVM_RPC_URL", "ROBINHOOD_RPC_URL", ""},
		{"EVM_DRY_RUN_WALLET_ID", "RBH_DRY_RUN_WALLET_ID", ""},
		{"EVM_DRY_RUN_FROM_ADDRESS", "RBH_DRY_RUN_FROM_ADDRESS", ""},
		{"EVM_EXECUTION_PRIVATE_KEY", "RBH_EXECUTION_PRIVATE_KEY", ""},
		{"EVM_ARTIFACT_ENCRYPTION_KEY", "RBH_ARTIFACT_ENCRYPTION_KEY", ""},
		{"EVM_ARTIFACT_KEY_VERSION", "RBH_ARTIFACT_KEY_VERSION", "v1"},
	}
	values := make(map[string]string, len(settings))
	for _, entry := range settings {
		if entry.key == "EVM_SOCKET_DIR" {
			entry.fallback = filepath.Join(values["EVM_DATA_DIR"], "run")
		}
		value, err := compatibleSetting(entry)
		if err != nil {
			return Config{}, err
		}
		values[entry.key] = value
	}
	chainID, err := strconv.ParseUint(values["EVM_CHAIN_ID"], 10, 64)
	if err != nil {
		return Config{}, fmt.Errorf("invalid EVM_CHAIN_ID/RBH_CHAIN_ID")
	}
	if _, err := chainregistry.RequireRuntime(chainID); err != nil {
		return Config{}, err
	}
	live, err := strconv.ParseBool(values["EVM_LIVE_ENABLED"])
	if err != nil || live {
		return Config{}, fmt.Errorf("EVM_LIVE_ENABLED/RBH_LIVE_ENABLED must be false")
	}
	canaryMode := values["EVM_CONTROLLED_CANARY_PRODUCTION_MODE"]
	if service == TradeService && canaryMode != CanaryProductionModeDisabled {
		return Config{}, fmt.Errorf("controlled canary production mode must be %s", CanaryProductionModeDisabled)
	}
	dataDir := values["EVM_DATA_DIR"]
	socketDir := values["EVM_SOCKET_DIR"]
	name := strings.TrimSuffix(string(service), "-service")
	return Config{
		Service: service, DataDir: dataDir, SocketDir: socketDir,
		Database: filepath.Join(dataDir, name+".db"),
		Socket:   filepath.Join(socketDir, string(service)+".sock"),
		LogLevel: strings.ToUpper(values["EVM_LOG_LEVEL"]),
		ChainID:  chainID, LiveEnabled: live,
		InternalAuthSecret:    values["EVM_INTERNAL_AUTH_SECRET"],
		RPCURL:                values["EVM_RPC_URL"],
		DryRunWalletID:        values["EVM_DRY_RUN_WALLET_ID"],
		DryRunFromAddress:     values["EVM_DRY_RUN_FROM_ADDRESS"],
		ExecutionPrivateKey:   values["EVM_EXECUTION_PRIVATE_KEY"],
		ArtifactEncryptionKey: values["EVM_ARTIFACT_ENCRYPTION_KEY"],
		ArtifactKeyVersion:    values["EVM_ARTIFACT_KEY_VERSION"],
		CanaryProductionMode:  canaryMode,
	}, nil
}

type setting struct {
	key, legacy, fallback string
}

// Comparing configured values byte-for-byte prevents silent precedence, including
// secrets and paths. Errors contain names only, never configured values.
func compatibleSetting(entry setting) (string, error) {
	current, currentSet := os.LookupEnv(entry.key)
	legacy, legacySet := os.LookupEnv(entry.legacy)
	if currentSet && legacySet && current != legacy {
		return "", fmt.Errorf("conflicting configuration: %s and %s", entry.key, entry.legacy)
	}
	if currentSet {
		if current == "" && entry.fallback != "" {
			return "", fmt.Errorf("%s must not be explicitly empty", entry.key)
		}
		return current, nil
	}
	if legacy != "" {
		return legacy, nil
	}
	return entry.fallback, nil
}
