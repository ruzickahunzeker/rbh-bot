package main

import (
	"github.com/ruzickahunzeker/rbh-bot/internal/app"
	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	"log"
)

func main() {
	if err := app.Run(config.FeedService); err != nil {
		log.Fatal(err)
	}
}
