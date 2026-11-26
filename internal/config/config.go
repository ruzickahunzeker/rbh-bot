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
	ETHChainID      uint64 = 1
	BSCChainID      uint64 = 56
	HyperEVMChainID uint64 = 999
	ChainID         uint64 = 4663
	ArcChainID      uint64 = 5042
	BaseChainID     uint64 = 8453
	ElysiumChainID  uint64 = 99901
)

func IsSupportedChainID(chainID uint64) bool {
	switch chainID {
	case ETHChainID, BSCChainID, HyperEVMChainID, ChainID, ArcChainID, BaseChainID, ElysiumChainID:
		return true
	default:
		return false
	}
}

func DefaultChainSlug(chainID uint64) string {
	switch chainID {
	case ETHChainID:
		return "eth"
	case BSCChainID:
		return "bsc"
	case HyperEVMChainID:
		return "hype"
	case ArcChainID:
		return "arc"
	case BaseChainID:
		return "base"
	case ElysiumChainID:
		return "ely"
	default:
		return "rbh"
	}
}

func DefaultNativeSymbol(chainID uint64) string {
	switch chainID {
	case BSCChainID:
		return "BNB"
	case HyperEVMChainID, ElysiumChainID:
		return "HYPE"
	case ArcChainID:
		return "USDC"
	default:
		return "ETH"
	}
}

func DefaultBuyPresetsForChain(chainID uint64) string {
	switch chainID {
	case ArcChainID:
		return "10,25,50,100,250"
	case HyperEVMChainID, ElysiumChainID:
		return "0.5,1,2,5,10"
	case ETHChainID, BaseChainID:
		return "0.02,0.05,0.1,0.25,0.5"
	default:
		return "0.05,0.1,0.25,0.5,1"
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
	WSURL                    string
	FeedSource               string
	FeedStartMode            string
	FeedStartBlock           uint64
	FeedMaxCatchup           uint64
	FeedMaxLag               uint64
	FeedRequestInterval      time.Duration
	TelegramBotToken         string
	TelegramAllowedChatIDs   []int64
	PaperShadowEnabled       bool
	TradeSocketRBH           string
	TradeAPITokenRBH         string
	FeedSocketRBH            string
	RPCURLRBH                string
	TradeSocketBSC           string
	TradeAPITokenBSC         string
	FeedSocketBSC            string
	ChainsManifestPath       string   // optional EVM_CHAINS_MANIFEST JSON path for dynamic N-chain topology
	ReadOnlyQueryOnly        bool     // EVM_READONLY_QUERY_ONLY=true skips V1 canary recovery worker
	PrivateRelayURLs         []string // comma-separated EVM_PRIVATE_RELAY_URLS for BSC private_only
	MaxOrderNativeWei        string   // EVM_MAX_ORDER_WEI hard safety cap per live order
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
	reader := newChainEnv()
	envPref := reader.pref
	env := func(key, fallback string) string { return reader.pref(key, "", fallback) }

	dataDir := envPref("EVM_DATA_DIR", "RBH_DATA_DIR", "./data")
	socketDir := envPref("EVM_SOCKET_DIR", "RBH_SOCKET_DIR", filepath.Join(dataDir, "run"))
	chainID, err := strconv.ParseUint(envPref("EVM_CHAIN_ID", "RBH_CHAIN_ID", "4663"), 10, 64)
	if err != nil || !IsSupportedChainID(chainID) {
		return Config{}, fmt.Errorf("EVM_CHAIN_ID/RBH_CHAIN_ID must be a supported chain (%d, %d, or %d)", ChainID, BSCChainID, BaseChainID)
	}
	live, err := strconv.ParseBool(envPref("EVM_LIVE_ENABLED", "RBH_LIVE_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("EVM_LIVE_ENABLED: %w", err)
	}
	canaryMode := envPref("EVM_CONTROLLED_CANARY_PRODUCTION_MODE", "RBH_CONTROLLED_CANARY_PRODUCTION_MODE", CanaryProductionModeDisabled)
	readQueries, err := strconv.ParseBool(envPref("EVM_TRADE_API_READ_QUERIES", "RBH_TRADE_API_READ_QUERIES", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid EVM_TRADE_API_READ_QUERIES")
	}
	manualIntake, err := strconv.ParseBool(envPref("EVM_TRADE_API_MANUAL_INTAKE", "RBH_TRADE_API_MANUAL_INTAKE", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid EVM_TRADE_API_MANUAL_INTAKE")
	}
	tradeAPIConfigFile := envPref("EVM_TRADE_API_CONFIG_FILE", "RBH_TRADE_API_CONFIG_FILE", "")
	if service == TradeService && manualIntake && tradeAPIConfigFile == "" {
		return Config{}, fmt.Errorf("manual intake requires EVM_TRADE_API_CONFIG_FILE")
	}
	validateOrders, err := strconv.ParseBool(envPref("EVM_TRADE_API_VALIDATE_ORDERS", "RBH_TRADE_API_VALIDATE_ORDERS", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid EVM_TRADE_API_VALIDATE_ORDERS")
	}
	validationPolicy, err := strconv.ParseUint(envPref("EVM_TRADE_API_VALIDATION_POLICY_VERSION", "RBH_TRADE_API_VALIDATION_POLICY_VERSION", "0"), 10, 63)
	if err != nil {
		return Config{}, fmt.Errorf("invalid EVM_TRADE_API_VALIDATION_POLICY_VERSION")
	}
	if service == TradeService && validateOrders && (!manualIntake || validationPolicy == 0 || tradeAPIConfigFile == "") {
		return Config{}, fmt.Errorf("validation requires scoped manual intake and explicit durable policy version")
	}
	manualAdmission, err := strconv.ParseBool(envPref("EVM_TRADE_API_MANUAL_ADMISSION", "RBH_TRADE_API_MANUAL_ADMISSION", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid EVM_TRADE_API_MANUAL_ADMISSION")
	}
	if service == TradeService && manualAdmission && (!manualIntake || !validateOrders || validationPolicy == 0 || tradeAPIConfigFile == "" || live) {
		return Config{}, fmt.Errorf("manual admission requires scoped intake/validation configuration and live disabled")
	}
	if service == TradeService && canaryMode != CanaryProductionModeDisabled {
		return Config{}, fmt.Errorf("EVM_CONTROLLED_CANARY_PRODUCTION_MODE must be %s in W4-A", CanaryProductionModeDisabled)
	}
	rpcURL := envPref("EVM_RPC_URL", "ROBINHOOD_RPC_URL", "")
	paperShadow, _ := strconv.ParseBool(env("EVM_PAPER_SHADOW_ENABLED", "false"))
	allowedChats, err := parseInt64CSV(os.Getenv("TELEGRAM_ALLOWED_CHAT_IDS"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid TELEGRAM_ALLOWED_CHAT_IDS: %w", err)
	}
	tradeStartupProfile := envPref("EVM_TRADE_STARTUP_PROFILE", "RBH_TRADE_STARTUP_PROFILE", TradeProfileLegacy)
	name := strings.TrimSuffix(string(service), "-service")
	cfg := Config{
		Service: service, DataDir: dataDir, SocketDir: socketDir,
		Database:                 filepath.Join(dataDir, name+".db"),
		Socket:                   filepath.Join(socketDir, string(service)+".sock"),
		AdminSocket:              envPref("EVM_ADMIN_SOCKET", "RBH_ADMIN_SOCKET", ""),
		LogLevel:                 strings.ToUpper(envPref("EVM_LOG_LEVEL", "RBH_LOG_LEVEL", "INFO")),
		ChainID:                  chainID,
		ChainSlug:                envPref("EVM_CHAIN_SLUG", "RBH_CHAIN_SLUG", DefaultChainSlug(chainID)),
		NetworkKind:              envPref("EVM_NETWORK_KIND", "RBH_NETWORK_KIND", "mainnet"),
		EnforceChainIdentity:     service == TradeService || service == FeedService,
		LiveEnabled:              live,
		InternalAuthSecret:       envPref("EVM_INTERNAL_AUTH_SECRET", "RBH_INTERNAL_AUTH_SECRET", ""),
		RPCURL:                   rpcURL,
		WSURL:                    envPref("EVM_WS_URL", "RBH_WS_URL", ""),
		TelegramBotToken:         strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		TelegramAllowedChatIDs:   allowedChats,
		PaperShadowEnabled:       paperShadow,
		TradeSocketRBH:           os.Getenv("EVM_TRADE_SOCKET_RBH"),
		TradeAPITokenRBH:         os.Getenv("EVM_TRADE_API_TOKEN_RBH"),
		FeedSocketRBH:            os.Getenv("EVM_FEED_SOCKET_RBH"),
		RPCURLRBH:                os.Getenv("EVM_RPC_URL_RBH"),
		TradeSocketBSC:           os.Getenv("EVM_TRADE_SOCKET_BSC"),
		TradeAPITokenBSC:         os.Getenv("EVM_TRADE_API_TOKEN_BSC"),
		FeedSocketBSC:            os.Getenv("EVM_FEED_SOCKET_BSC"),
		ChainsManifestPath:       os.Getenv("EVM_CHAINS_MANIFEST"),
		ReadOnlyQueryOnly:        strings.EqualFold(env("EVM_READONLY_QUERY_ONLY", "false"), "true"),
		PrivateRelayURLs:         splitNonEmptyCSV(os.Getenv("EVM_PRIVATE_RELAY_URLS")),
		MaxOrderNativeWei:        env("EVM_MAX_ORDER_WEI", "50000000000000000"), // default 0.05 native unit cap
		DryRunWalletID:           envPref("EVM_DRY_RUN_WALLET_ID", "RBH_DRY_RUN_WALLET_ID", ""),
		DryRunFromAddress:        envPref("EVM_DRY_RUN_FROM_ADDRESS", "RBH_DRY_RUN_FROM_ADDRESS", ""),
		ExecutionPrivateKey:      envPref("EVM_EXECUTION_PRIVATE_KEY", "RBH_EXECUTION_PRIVATE_KEY", ""),
		ArtifactEncryptionKey:    envPref("EVM_ARTIFACT_ENCRYPTION_KEY", "RBH_ARTIFACT_ENCRYPTION_KEY", ""),
		ArtifactKeyVersion:       envPref("EVM_ARTIFACT_KEY_VERSION", "RBH_ARTIFACT_KEY_VERSION", "v1"),
		ArtifactHistoricalKeys:   envPref("EVM_ARTIFACT_HISTORICAL_KEYS", "RBH_ARTIFACT_HISTORICAL_KEYS", ""),
		CanaryProductionMode:     canaryMode,
		TradeAPIConfigFile:       tradeAPIConfigFile,
		TradeAPIReadQueries:      readQueries,
		TradeAPIManualIntake:     manualIntake,
		TradeAPIValidateOrders:   validateOrders,
		TradeAPIValidationPolicy: validationPolicy,
		TradeAPIManualAdmission:  manualAdmission,
		TradeStartupProfile:      tradeStartupProfile,
	}

	defaultSource := "sequencer"
	if chainID != ChainID {
		defaultSource = "rpc_poll"
	}
	cfg.FeedSource = env("EVM_FEED_SOURCE", defaultSource)
	cfg.FeedStartMode = env("EVM_FEED_START_MODE", "latest")
	var parseErr error
	for _, field := range []struct {
		key, fallback string
		target        *uint64
	}{
		{"EVM_FEED_START_BLOCK", "0", &cfg.FeedStartBlock},
		{"EVM_FEED_MAX_CATCHUP_BLOCKS", "200", &cfg.FeedMaxCatchup},
		{"EVM_FEED_MAX_LAG_BLOCKS", "8", &cfg.FeedMaxLag},
	} {
		*field.target, parseErr = strconv.ParseUint(env(field.key, field.fallback), 10, 63)
		if parseErr != nil {
			return Config{}, fmt.Errorf("invalid %s", field.key)
		}
	}
	cfg.FeedRequestInterval, parseErr = time.ParseDuration(env("EVM_FEED_REQUEST_INTERVAL", "50ms"))
	if parseErr != nil || cfg.FeedRequestInterval < 0 {
		return Config{}, fmt.Errorf("invalid feed request interval")
	}
	if reader.err != nil {
		return Config{}, reader.err
	}
	if service == FeedService {
		switch cfg.FeedSource {
		case "sequencer", "rpc_poll", "rpc_ws":
		default:
			return Config{}, fmt.Errorf("invalid feed source")
		}
		if cfg.FeedSource == "sequencer" && chainID != ChainID {
			return Config{}, fmt.Errorf("sequencer feed requires chain 4663")
		}
		if cfg.FeedSource == "rpc_poll" && chainID == ChainID {
			return Config{}, fmt.Errorf("RBH RPC feed requires rpc_ws with independent block checkpoints")
		}
		if cfg.FeedSource == "rpc_ws" {
			if cfg.WSURL == "" || cfg.RPCURL == "" {
				return Config{}, fmt.Errorf("rpc_ws requires RPC_URL and WS_URL")
			}
			if cfg.FeedMaxCatchup == 0 || cfg.FeedMaxCatchup > 10000 || cfg.FeedMaxLag > cfg.FeedMaxCatchup {
				return Config{}, fmt.Errorf("invalid feed catchup/lag limits")
			}
			if cfg.FeedStartMode != "latest" && cfg.FeedStartMode != "block" {
				return Config{}, fmt.Errorf("invalid feed start mode")
			}
			if env("EVM_FEED_GAP_POLICY", "halt") != "halt" {
				return Config{}, fmt.Errorf("only halt feed gap policy is supported")
			}
		}
	}
	if err := cfg.ValidateTradeStartupProfile(); err != nil {
		return Config{}, err
	}
	if reader.err != nil {
		return Config{}, reader.err
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

func envPref(primary, legacy, fallback string) string {
	if value := os.Getenv(primary); value != "" {
		return value
	}
	if legacy != "" {
		if value := os.Getenv(legacy); value != "" {
			return value
		}
	}
	return fallback
}

func parseInt64CSV(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseInt(p, 10, 64)
		if err != nil || v == 0 {
			return nil, fmt.Errorf("invalid chat id %q", p)
		}
		out = append(out, v)
	}
	return out, nil
}

func splitNonEmptyCSV(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
