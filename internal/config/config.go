package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	ChainID     uint64 = 4663
	BSCChainID  uint64 = 56
	BaseChainID uint64 = 8453
)

func IsSupportedChainID(chainID uint64) bool {
	return chainID == ChainID || chainID == BSCChainID || chainID == BaseChainID
}

func DefaultChainSlug(chainID uint64) string {
	switch chainID {
	case BSCChainID:
		return "bsc"
	case BaseChainID:
		return "base"
	default:
		return "rbh"
	}
}

const CanaryProductionModeDisabled = "DISABLED"

const (
	TradeProfileLegacy           = "LEGACY"
	TradeProfileRecoveryOnly     = "RECOVERY_ONLY"
	TradeProfileQueryAndRecovery = "QUERY_AND_RECOVERY"
)

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
	AdminSocket              string
	LogLevel                 string
	ChainID                  uint64
	ChainSlug                string
	NetworkKind              string
	EnforceChainIdentity     bool
	LiveEnabled              bool
	InternalAuthSecret       string
	RPCURL                   string
	DryRunWalletID           string
	DryRunFromAddress        string
	ExecutionPrivateKey      string
	ArtifactEncryptionKey    string
	ArtifactKeyVersion       string
	ArtifactHistoricalKeys   string // optional comma-separated "ver=hexKey" entries for keyring decryption
	CanaryProductionMode     string
	TradeAPIConfigFile       string
	TradeAPIReadQueries      bool
	TradeAPIManualIntake     bool
	TradeAPIValidateOrders   bool
	TradeAPIValidationPolicy uint64
	TradeAPIManualAdmission  bool
	TradeStartupProfile      string
	HTTPWriteTimeout         time.Duration
	BotClientTimeout         time.Duration
	PreSendRPCBudget         time.Duration
	BroadcastRPCTimeout      time.Duration
}

// ValidateTimeouts enforces the Step 0B timeout inequality whenever timeouts are explicitly configured:
// BotClientTimeout > HTTPWriteTimeout > (PreSendRPCBudget + BroadcastRPCTimeout + 2s).
func (c Config) ValidateTimeouts() error {
	if c.AdminSocket != "" && c.Socket != "" && filepath.Clean(c.AdminSocket) == filepath.Clean(c.Socket) {
		return fmt.Errorf("admin socket must be distinct from business socket")
	}
	if c.HTTPWriteTimeout < 0 || c.BotClientTimeout < 0 || c.PreSendRPCBudget < 0 || c.BroadcastRPCTimeout < 0 {
		return fmt.Errorf("timeout configuration must be non-negative")
	}
	if c.HTTPWriteTimeout > 0 && (c.PreSendRPCBudget > 0 || c.BroadcastRPCTimeout > 0) {
		minWrite := c.PreSendRPCBudget + c.BroadcastRPCTimeout + 2*time.Second
		if c.HTTPWriteTimeout <= minWrite {
			return fmt.Errorf("trade HTTPWriteTimeout (%s) must exceed PreSendRPCBudget+BroadcastRPCTimeout+2s (%s)", c.HTTPWriteTimeout, minWrite)
		}
	}
	if c.BotClientTimeout > 0 && c.HTTPWriteTimeout > 0 {
		if c.BotClientTimeout <= c.HTTPWriteTimeout {
			return fmt.Errorf("BotClientTimeout (%s) must exceed trade HTTPWriteTimeout (%s)", c.BotClientTimeout, c.HTTPWriteTimeout)
		}
	}
	return nil
}

// ParseArtifactKeyring decodes the active encryption key and any historical decryption keys.
func (c Config) ParseArtifactKeyring() (map[string][]byte, error) {
	if c.ArtifactKeyVersion == "" || c.ArtifactEncryptionKey == "" {
		return nil, fmt.Errorf("active artifact key version and key are required")
	}
	activeKey, err := hex.DecodeString(c.ArtifactEncryptionKey)
	if err != nil || len(activeKey) != 32 {
		return nil, fmt.Errorf("invalid active artifact encryption key")
	}
	ring := map[string][]byte{c.ArtifactKeyVersion: activeKey}
	if strings.TrimSpace(c.ArtifactHistoricalKeys) == "" {
		return ring, nil
	}
	for _, entry := range strings.Split(c.ArtifactHistoricalKeys, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		ver, hexKey, ok := strings.Cut(entry, "=")
		ver = strings.TrimSpace(ver)
		hexKey = strings.TrimSpace(hexKey)
		if !ok || ver == "" || hexKey == "" {
			return nil, fmt.Errorf("invalid historical keyring entry")
		}
		if _, dup := ring[ver]; dup {
			return nil, fmt.Errorf("duplicate keyring version %q", ver)
		}
		raw, err := hex.DecodeString(hexKey)
		if err != nil || len(raw) != 32 {
			return nil, fmt.Errorf("invalid historical key for version %q", ver)
		}
		ring[ver] = raw
	}
	return ring, nil
}

// ValidateTradeStartupProfile checks capability selection, not authorization.
// An empty profile preserves callers of the original Config API as legacy.
func (c Config) ValidateTradeStartupProfile() error {
	if c.TradeStartupProfile == "" || c.TradeStartupProfile == TradeProfileLegacy {
		return nil
	}
	if c.Service != TradeService {
		return fmt.Errorf("invalid trade startup profile")
	}
	switch c.TradeStartupProfile {
	case TradeProfileRecoveryOnly:
		if c.LiveEnabled || c.CanaryProductionMode != CanaryProductionModeDisabled ||
			c.TradeAPIConfigFile != "" || c.TradeAPIReadQueries || c.TradeAPIManualIntake ||
			c.TradeAPIValidateOrders || c.TradeAPIValidationPolicy != 0 || c.TradeAPIManualAdmission ||
			c.ExecutionPrivateKey != "" || c.DryRunWalletID != "" || c.DryRunFromAddress != "" {
			return fmt.Errorf("recovery-only startup rejects execution and user API configuration")
		}
		return nil
	case TradeProfileQueryAndRecovery:
		if c.LiveEnabled || c.CanaryProductionMode != CanaryProductionModeDisabled ||
			c.TradeAPIConfigFile == "" || c.TradeAPIManualIntake ||
			c.TradeAPIValidateOrders || c.TradeAPIValidationPolicy != 0 || c.TradeAPIManualAdmission ||
			c.ExecutionPrivateKey != "" || c.DryRunWalletID != "" || c.DryRunFromAddress != "" {
			return fmt.Errorf("query-and-recovery startup requires user API config and rejects execution/admission configuration")
		}
		return nil
	default:
		return fmt.Errorf("invalid trade startup profile")
	}
}

func Load(service Service) (Config, error) {
	dataDir := env("RBH_DATA_DIR", "./data")
	socketDir := env("RBH_SOCKET_DIR", filepath.Join(dataDir, "run"))
	chainID, err := strconv.ParseUint(env("RBH_CHAIN_ID", "4663"), 10, 64)
	if err != nil || !IsSupportedChainID(chainID) {
		return Config{}, fmt.Errorf("RBH_CHAIN_ID must be a supported chain (%d or %d)", ChainID, BSCChainID)
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
	manualAdmission, err := strconv.ParseBool(env("RBH_TRADE_API_MANUAL_ADMISSION", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid RBH_TRADE_API_MANUAL_ADMISSION")
	}
	if service == TradeService && manualAdmission && (!manualIntake || !validateOrders || validationPolicy == 0 || os.Getenv("RBH_TRADE_API_CONFIG_FILE") == "" || live) {
		return Config{}, fmt.Errorf("manual admission requires scoped intake/validation configuration and live disabled")
	}
	if service == TradeService && canaryMode != CanaryProductionModeDisabled {
		return Config{}, fmt.Errorf("RBH_CONTROLLED_CANARY_PRODUCTION_MODE must be %s in W4-A", CanaryProductionModeDisabled)
	}
	name := strings.TrimSuffix(string(service), "-service")
	cfg := Config{
		Service: service, DataDir: dataDir, SocketDir: socketDir,
		Database: filepath.Join(dataDir, name+".db"),
		Socket:                   filepath.Join(socketDir, string(service)+".sock"),
		AdminSocket:              os.Getenv("RBH_ADMIN_SOCKET"),
		LogLevel:                 strings.ToUpper(env("RBH_LOG_LEVEL", "INFO")),
		ChainID:                  chainID,
		ChainSlug:                env("RBH_CHAIN_SLUG", DefaultChainSlug(chainID)),
		NetworkKind:              env("RBH_NETWORK_KIND", "mainnet"),
		EnforceChainIdentity:     service == TradeService || service == FeedService,
		LiveEnabled:              live,
		InternalAuthSecret:       os.Getenv("RBH_INTERNAL_AUTH_SECRET"),
		RPCURL:                   os.Getenv("ROBINHOOD_RPC_URL"),
		DryRunWalletID:           os.Getenv("RBH_DRY_RUN_WALLET_ID"),
		DryRunFromAddress:        os.Getenv("RBH_DRY_RUN_FROM_ADDRESS"),
		ExecutionPrivateKey:      os.Getenv("RBH_EXECUTION_PRIVATE_KEY"),
		ArtifactEncryptionKey:    os.Getenv("RBH_ARTIFACT_ENCRYPTION_KEY"),
		ArtifactKeyVersion:       env("RBH_ARTIFACT_KEY_VERSION", "v1"),
		ArtifactHistoricalKeys:   os.Getenv("RBH_ARTIFACT_HISTORICAL_KEYS"),
		CanaryProductionMode:     canaryMode,
		TradeAPIConfigFile:       os.Getenv("RBH_TRADE_API_CONFIG_FILE"),
		TradeAPIReadQueries:      readQueries,
		TradeAPIManualIntake:     manualIntake,
		TradeAPIValidateOrders:   validateOrders,
		TradeAPIValidationPolicy: validationPolicy,
		TradeAPIManualAdmission:  manualAdmission,
		TradeStartupProfile:      env("RBH_TRADE_STARTUP_PROFILE", TradeProfileLegacy),
	}
	if err := cfg.ValidateTradeStartupProfile(); err != nil {
		return Config{}, err
	}
	if err := cfg.ValidateTimeouts(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
