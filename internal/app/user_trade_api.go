package app

import (
	"net/http"

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
		return nil
	}
	userAPI, err := tradeapi.LoadFile(database, cfg.TradeAPIConfigFile, cfg.InternalAuthSecret)
	if err != nil {
		return err
	}
	server.Handle("/v1/", ipc.RequestContext(userAPI))
	return nil
}
