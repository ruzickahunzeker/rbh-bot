// evm-bot is an E1 naming alias for the existing Robinhood trade-service.
// It is not a generic multi-chain API/worker and adds no execution permission.
package main

import (
	"errors"
	"log"
	"os"

	"github.com/ruzickahunzeker/rbh-bot/internal/app"
	"github.com/ruzickahunzeker/rbh-bot/internal/config"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	// Never silently ignore a --chain/--live argument and start the legacy
	// worker on a different chain than the caller intended.
	if len(args) != 0 {
		return errors.New("evm-bot accepts no command-line arguments in E1; use environment configuration")
	}
	return app.Run(config.TradeService)
}
