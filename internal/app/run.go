package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	feedcore "github.com/ruzickahunzeker/rbh-bot/internal/feed"
	"github.com/ruzickahunzeker/rbh-bot/internal/health"
	"github.com/ruzickahunzeker/rbh-bot/internal/observability"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
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
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	gates := []string{"config_valid", "database_open", "migrations_current", "socket_bound", "live_disabled"}
	if service == config.FeedService {
		gates = append(gates, "feed_stream_healthy")
	}
	ready := health.NewReadiness(gates...)
	ready.Set("config_valid", true)
	ready.Set("live_disabled", true)
	database, err := storage.Open(context.Background(), storage.Owner(service), cfg.Database)
	if err != nil {
		return err
	}
	defer database.Close()
	ready.Set("database_open", true)
	if err := database.Migrate(context.Background()); err != nil {
		return err
	}
	ready.Set("migrations_current", true)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	server := health.New(string(service), cfg.Socket, log, ready)
	var runner *feedcore.SequencerRunner
	if service == config.FeedService {
		store, err := feedcore.NewStore(database)
		if err != nil {
			return err
		}
		value := parser.New()
		snapshot, found, err := store.LoadRegistrySnapshot(ctx)
		if err != nil {
			return err
		}
		if found {
			if err := value.Registry().Restore(snapshot); err != nil {
				return fmt.Errorf("restore feed parser registry: %w", err)
			}
		}
		runner, err = feedcore.NewSequencerRunner(value, store)
		if err != nil {
			return err
		}
		server.SetMetricsWriter(func(metricCtx context.Context, writer io.Writer) error {
			return writeFeedMetrics(metricCtx, writer, store)
		})
	}

	listener, err := server.Listen()
	if err != nil {
		return err
	}
	ready.Set("socket_bound", true)
	errCh := make(chan error, 2)
	go func() { errCh <- server.Serve(listener) }()
	if runner != nil {
		go func() { errCh <- runner.Run(ctx) }()
		go monitorFeedReadiness(ctx, ready, runner)
	}

	select {
	case err := <-errCh:
		if ctx.Err() != nil {
			return shutdownServer(server)
		}
		if shutdownErr := shutdownServer(server); shutdownErr != nil {
			if err == nil {
				return shutdownErr
			}
			return fmt.Errorf("service error: %v; shutdown: %w", err, shutdownErr)
		}
		if err == nil {
			return errors.New("service component stopped unexpectedly")
		}
		return err
	case <-ctx.Done():
		return shutdownServer(server)
	}
}

func writeFeedMetrics(ctx context.Context, writer io.Writer, store *feedcore.Store) error {
	metrics, err := store.Metrics(ctx)
	if err != nil {
		return err
	}
	degraded := 0
	if metrics.Degraded {
		degraded = 1
	}
	_, err = fmt.Fprintf(writer,
		"# TYPE rbh_feed_observations_total gauge\nrbh_feed_observations_total %d\n"+
			"# TYPE rbh_feed_orphaned_total gauge\nrbh_feed_orphaned_total %d\n"+
			"# TYPE rbh_feed_outbox_rows gauge\nrbh_feed_outbox_rows %d\n"+
			"# TYPE rbh_feed_receipt_audits_total gauge\nrbh_feed_receipt_audits_total %d\n"+
			"# TYPE rbh_feed_durable_event_offset gauge\nrbh_feed_durable_event_offset %d\n"+
			"# TYPE rbh_feed_degraded gauge\nrbh_feed_degraded %d\n",
		metrics.Observations, metrics.Orphaned, metrics.OutboxRows, metrics.ReceiptAudits, metrics.DurableEventOffset, degraded,
	)
	return err
}

func monitorFeedReadiness(ctx context.Context, ready *health.Readiness, runner *feedcore.SequencerRunner) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready.Set("feed_stream_healthy", runner.Ready(ctx))
		select {
		case <-ctx.Done():
			ready.Set("feed_stream_healthy", false)
			return
		case <-ticker.C:
		}
	}
}

func shutdownServer(server *health.Server) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
