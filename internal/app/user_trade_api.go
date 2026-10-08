package app

import (
	"net/http"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/tradeapi"
)

type routeRegistrar interface{ Handle(string, http.Handler) }

// Optional registration on the existing local UDS server. This adds no
// listener, execution dependency, readiness flag or authorization grant.
func configureUserTradeAPI(server routeRegistrar, database *storage.Database, cfg config.Config) error {
	if cfg.TradeAPIConfigFile == "" {
		if cfg.TradeAPIManualIntake || cfg.TradeAPIValidateOrders {
			return tradeapi.ErrInvalid
		}
		return nil
	}
	var queries tradeapi.ReadQueries
	if cfg.TradeAPIReadQueries {
		client, err := chainquery.New(cfg.RPCURL)
		if err != nil {
			return err
		}
		queries = client
	}
	options := tradeapi.Options{ManualIntake: cfg.TradeAPIManualIntake}
	if cfg.TradeAPIValidateOrders {
		if !cfg.TradeAPIManualIntake || cfg.TradeAPIValidationPolicy == 0 {
			return tradeapi.ErrInvalid
		}
		validator, err := chainquery.NewValidator(cfg.RPCURL)
		if err != nil {
			return err
		}
		options.ValidationBackend = validator
		options.ValidationPolicyVersion = cfg.TradeAPIValidationPolicy
	}
	userAPI, err := tradeapi.LoadFileWithOptions(database, cfg.TradeAPIConfigFile, cfg.InternalAuthSecret, queries, options)
	if err != nil {
		return err
	}
	server.Handle("/v1/", ipc.RequestContext(userAPI))
	return nil
}
