package app

import (
	"net/http"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
	basechain "github.com/ruzickahunzeker/rbh-bot/internal/chains/base"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/robinhood"
	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/flap"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/fourmeme"
	pancakev2 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/pancakeswap/v2"
	pancakev3 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/pancakeswap/v3"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/pons/v2curve"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
	univ3 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/uniswap/v3"
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
				fourmeme.NewAdapter(),
				flap.NewAdapter(),
			},
			map[string]registry.TrustedSpender{
				pancakev2.ProtocolID: {Address: bsc.PancakeV2Router},
				pancakev3.ProtocolID: {Address: bsc.PancakeV3SmartRouter},
				fourmeme.ProtocolID:  {Address: bsc.FourMemeTokenManager2},
				flap.ProtocolID:      {Address: bsc.FlapPortal},
			},
		)
	case config.BaseChainID:
		return registry.New(
			basechain.Profile(),
			[]ports.ProtocolAdapter{univ3.NewBaseAdapter()},
			map[string]registry.TrustedSpender{
				univ3.ProtocolID: {Address: basechain.UniswapV3SwapRouter02},
			},
		)
	default:
		return registry.New(
			robinhood.Profile(),
			[]ports.ProtocolAdapter{v2curve.NewAdapter()},
			nil,
		)
	}
}
