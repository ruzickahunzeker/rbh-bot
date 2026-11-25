package app

import (
	"net/http"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/arc"
	basechain "github.com/ruzickahunzeker/rbh-bot/internal/chains/base"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/elysium"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/ethereum"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/hyperevm"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/robinhood"
	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/aerodrome"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/flap"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/fourmeme"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/kyberswap"
	pancakev2 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/pancakeswap/v2"
	pancakev3 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/pancakeswap/v3"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/pons/v2curve"
	ponsv4 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/pons/v4"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
	univ3 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/uniswap/v3"
	univ4 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/uniswap/v4"
	"github.com/ruzickahunzeker/rbh-bot/internal/registry"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/tradeapi"
)

type routeRegistrar interface{ Handle(string, http.Handler) }

// Optional registration on the existing local UDS server. This adds no
// listener, execution dependency, readiness flag or authorization grant.
func configureUserTradeAPI(server routeRegistrar, database *storage.Database, cfg config.Config) error {
	_, err := configureUserTradeAPIWithHandle(server, database, cfg)
	return err
}

func configureUserTradeAPIWithHandle(server routeRegistrar, database *storage.Database, cfg config.Config) (*tradeapi.API, error) {
	if cfg.TradeAPIConfigFile == "" {
		if cfg.TradeAPIManualIntake || cfg.TradeAPIValidateOrders || cfg.TradeAPIManualAdmission {
			return nil, tradeapi.ErrInvalid
		}
		return nil, nil
	}
	if cfg.TradeAPIManualAdmission && (!cfg.TradeAPIManualIntake || !cfg.TradeAPIValidateOrders || cfg.LiveEnabled) {
		return nil, tradeapi.ErrInvalid
	}
	var queries tradeapi.ReadQueries
	if cfg.TradeAPIReadQueries {
		client, err := chainquery.New(cfg.RPCURL)
		if err != nil {
			return nil, err
		}
		queries = client
	}
	options := tradeapi.Options{ManualIntake: cfg.TradeAPIManualIntake, ManualAdmission: cfg.TradeAPIManualAdmission}
	if cfg.TradeAPIValidateOrders {
		if !cfg.TradeAPIManualIntake || cfg.TradeAPIValidationPolicy == 0 {
			return nil, tradeapi.ErrInvalid
		}
		validator, err := chainquery.NewValidator(cfg.RPCURL)
		if err != nil {
			return nil, err
		}
		options.ValidationBackend = validator
		options.ValidationPolicyVersion = cfg.TradeAPIValidationPolicy
	}
	userAPI, err := tradeapi.LoadFileWithOptions(database, cfg.TradeAPIConfigFile, cfg.InternalAuthSecret, queries, options)
	if err != nil {
		return nil, err
	}
	reg, err := buildChainRegistry(cfg.ChainID)
	if err != nil {
		return nil, err
	}
	var localProvider tradeapi.LocalV2Provider
	if cfg.TradeAPIReadQueries && cfg.RPCURL != "" {
		localProvider = tradeapi.NewRPCLocalV2Provider(cfg.RPCURL, reg, database, userAPI.WalletAddress(), 0)
	}
	ext, err := tradeapi.NewLocalV2Extension(reg, localProvider, nil)
	if err != nil {
		return nil, err
	}
	if err := userAPI.AttachLocalV2(ext); err != nil {
		return nil, err
	}
	server.Handle("/v1/", ipc.RequestContext(userAPI))
	return userAPI, nil
}

func buildChainRegistry(chainID uint64) (*registry.Registry, error) {
	switch chainID {
	case config.BSCChainID:
		return registry.New(
			bsc.Profile(),
			[]ports.ProtocolAdapter{
				pancakev2.NewAdapter(),
				pancakev3.NewAdapter(),
				pancakev2.NewCustomAdapter(bsc.ChainID, "uniswap-v2-bsc", "BNB", bsc.UniswapV2Factory, bsc.UniswapV2Router02, bsc.WBNBAddress),
				univ3.NewBSCAdapter(),
				univ4.NewBSCAdapter(),
				fourmeme.NewAdapter(),
				flap.NewAdapter(),
				kyberswap.NewBSCAdapter(),
			},
			map[string]registry.TrustedSpender{
				pancakev2.ProtocolID:  {Address: bsc.PancakeV2Router},
				pancakev3.ProtocolID:  {Address: bsc.PancakeV3SmartRouter},
				"uniswap-v2-bsc":      {Address: bsc.UniswapV2Router02},
				univ3.ProtocolIDBSC:   {Address: bsc.UniswapV3SwapRouter02},
				univ4.ProtocolIDBSC:   {Address: bsc.UniswapV4UniversalRouter},
				fourmeme.ProtocolID:   {Address: bsc.FourMemeTokenManager2},
				flap.ProtocolID:       {Address: bsc.FlapPortal},
				kyberswap.ProtocolID:  {Address: bsc.KyberMetaRouter},
			},
		)
	case config.ETHChainID:
		return registry.New(
			ethereum.Profile(),
			[]ports.ProtocolAdapter{
				pancakev2.NewCustomAdapter(ethereum.ChainID, "uniswap-v2-eth", "ETH", ethereum.UniswapV2Factory, ethereum.UniswapV2Router02, ethereum.WETHAddress),
				univ3.NewCustomAdapter(ethereum.ChainID, "uniswap-v3-eth", "ETH", ethereum.UniswapV3Factory, ethereum.UniswapV3QuoterV2, ethereum.UniswapV3SwapRouter02, ethereum.WETHAddress),
				univ4.NewETHAdapter(),
			},
			map[string]registry.TrustedSpender{
				"uniswap-v2-eth":    {Address: ethereum.UniswapV2Router02},
				"uniswap-v3-eth":    {Address: ethereum.UniswapV3SwapRouter02},
				univ4.ProtocolIDETH: {Address: ethereum.UniswapV4UniversalRouter},
			},
		)
	case config.BaseChainID:
		return registry.New(
			basechain.Profile(),
			[]ports.ProtocolAdapter{
				aerodrome.NewBaseAdapter(),
				univ3.NewBaseAdapter(),
				univ4.NewBaseAdapter(),
				pancakev2.NewCustomAdapter(basechain.ChainID, "uniswap-v2-base", "ETH", basechain.UniswapV2Factory, basechain.UniswapV2Router02, basechain.WETHAddress),
			},
			map[string]registry.TrustedSpender{
				aerodrome.ProtocolID: {Address: basechain.AerodromeRouter},
				univ3.ProtocolID:     {Address: basechain.UniswapV3SwapRouter02},
				univ4.ProtocolIDBase: {Address: basechain.UniswapV4UniversalRouter},
				"uniswap-v2-base":    {Address: basechain.UniswapV2Router02},
			},
		)
	case config.HyperEVMChainID:
		return registry.New(
			hyperevm.Profile(),
			[]ports.ProtocolAdapter{
				pancakev2.NewCustomAdapter(hyperevm.ChainID, "hyperswap-v2", "HYPE", hyperevm.HyperSwapV2Factory, hyperevm.HyperSwapV2Router, hyperevm.WHYPEAddress),
				univ3.NewCustomAdapter(hyperevm.ChainID, "hyperswap-v3", "HYPE", hyperevm.HyperSwapV3Factory, hyperevm.HyperSwapV3Quoter, hyperevm.HyperSwapV3Router, hyperevm.WHYPEAddress),
			},
			map[string]registry.TrustedSpender{
				"hyperswap-v2": {Address: hyperevm.HyperSwapV2Router},
				"hyperswap-v3": {Address: hyperevm.HyperSwapV3Router},
			},
		)
	case config.ArcChainID:
		return registry.New(
			arc.Profile(),
			nil,
			nil,
		)
	case config.ElysiumChainID:
		return registry.New(
			elysium.Profile(),
			nil,
			nil,
		)
	default:
		return registry.New(
			robinhood.Profile(),
			[]ports.ProtocolAdapter{
				v2curve.NewAdapter(),
				flap.NewRBHAdapter(),
				univ3.NewCustomAdapter(robinhood.ChainID, "uniswap-v3-rbh", "ETH", robinhood.V3Factory, robinhood.V3QuoterV2, robinhood.V3SwapRouter02, robinhood.WETHAddress),
				univ4.NewRBHAdapter(),
				ponsv4.NewAdapter(),
			},
			map[string]registry.TrustedSpender{
				flap.ProtocolID:     {Address: robinhood.FlapPortal},
				"uniswap-v3-rbh":    {Address: robinhood.V3SwapRouter02},
				univ4.ProtocolIDRBH: {Address: robinhood.V4UniversalRouter},
				ponsv4.ProtocolID:   {Address: robinhood.V4UniversalRouter},
			},
		)
	}
}
