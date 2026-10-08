package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const ChainID uint64 = 4663

const CanaryProductionModeDisabled = "DISABLED"

type Service string

const (
	FeedService  Service = "feed-service"
	BotService   Service = "bot-service"
	TradeService Service = "trade-service"
)

type Config struct {
	Service                  Service
	DataDir                  string
	SocketDir                string
	Database                 string
	Socket                   string
	LogLevel                 string
	ChainID                  uint64
	LiveEnabled              bool
	InternalAuthSecret       string
	RPCURL                   string
	DryRunWalletID           string
	DryRunFromAddress        string
	ExecutionPrivateKey      string
	ArtifactEncryptionKey    string
	ArtifactKeyVersion       string
	CanaryProductionMode     string
	TradeAPIConfigFile       string
	TradeAPIReadQueries      bool
	TradeAPIManualIntake     bool
	TradeAPIValidateOrders   bool
	TradeAPIValidationPolicy uint64
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
	canaryMode := env("RBH_CONTROLLED_CANARY_PRODUCTION_MODE", CanaryProductionModeDisabled)
	readQueries, err := strconv.ParseBool(env("RBH_TRADE_API_READ_QUERIES", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid RBH_TRADE_API_READ_QUERIES")
	}
	manualIntake, err := strconv.ParseBool(env("RBH_TRADE_API_MANUAL_INTAKE", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid RBH_TRADE_API_MANUAL_INTAKE")
	}
	if service == TradeService && manualIntake && os.Getenv("RBH_TRADE_API_CONFIG_FILE") == "" {
		return Config{}, fmt.Errorf("manual intake requires RBH_TRADE_API_CONFIG_FILE")
	}
	validateOrders, err := strconv.ParseBool(env("RBH_TRADE_API_VALIDATE_ORDERS", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid RBH_TRADE_API_VALIDATE_ORDERS")
	}
	validationPolicy, err := strconv.ParseUint(env("RBH_TRADE_API_VALIDATION_POLICY_VERSION", "0"), 10, 63)
	if err != nil {
		return Config{}, fmt.Errorf("invalid RBH_TRADE_API_VALIDATION_POLICY_VERSION")
	}
	if service == TradeService && validateOrders && (!manualIntake || validationPolicy == 0 || os.Getenv("RBH_TRADE_API_CONFIG_FILE") == "") {
		return Config{}, fmt.Errorf("validation requires scoped manual intake and explicit durable policy version")
	}
	if service == TradeService && canaryMode != CanaryProductionModeDisabled {
		return Config{}, fmt.Errorf("RBH_CONTROLLED_CANARY_PRODUCTION_MODE must be %s in W4-A", CanaryProductionModeDisabled)
	}
	name := strings.TrimSuffix(string(service), "-service")
	return Config{
		Service: service, DataDir: dataDir, SocketDir: socketDir,
		Database: filepath.Join(dataDir, name+".db"),
		Socket:   filepath.Join(socketDir, string(service)+".sock"),
		LogLevel: strings.ToUpper(env("RBH_LOG_LEVEL", "INFO")),
		ChainID:  chainID, LiveEnabled: live,
		InternalAuthSecret:       os.Getenv("RBH_INTERNAL_AUTH_SECRET"),
		RPCURL:                   os.Getenv("ROBINHOOD_RPC_URL"),
		DryRunWalletID:           os.Getenv("RBH_DRY_RUN_WALLET_ID"),
		DryRunFromAddress:        os.Getenv("RBH_DRY_RUN_FROM_ADDRESS"),
		ExecutionPrivateKey:      os.Getenv("RBH_EXECUTION_PRIVATE_KEY"),
		ArtifactEncryptionKey:    os.Getenv("RBH_ARTIFACT_ENCRYPTION_KEY"),
		ArtifactKeyVersion:       env("RBH_ARTIFACT_KEY_VERSION", "v1"),
		CanaryProductionMode:     canaryMode,
		TradeAPIConfigFile:       os.Getenv("RBH_TRADE_API_CONFIG_FILE"),
		TradeAPIReadQueries:      readQueries,
		TradeAPIManualIntake:     manualIntake,
		TradeAPIValidateOrders:   validateOrders,
		TradeAPIValidationPolicy: validationPolicy,
	}, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
