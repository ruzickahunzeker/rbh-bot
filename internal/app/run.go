package app

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	"github.com/ruzickahunzeker/rbh-bot/internal/health"
	"github.com/ruzickahunzeker/rbh-bot/internal/observability"
)

func Run(service config.Service) error {
	cfg, err := config.Load(service)
	if err != nil {
		return err
	}
	log := observability.Logger(string(service), cfg.LogLevel)
	if cfg.LiveEnabled {
		return errors.New("live execution is disabled in PR-001")
	}
	server := health.New(string(service), cfg.Socket, log)
	server.SetReady(true)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
