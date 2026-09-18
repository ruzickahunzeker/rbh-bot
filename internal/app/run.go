package app

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	botcore "github.com/ruzickahunzeker/rbh-bot/internal/bot"
	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	feedcore "github.com/ruzickahunzeker/rbh-bot/internal/feed"
	"github.com/ruzickahunzeker/rbh-bot/internal/health"
	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
	"github.com/ruzickahunzeker/rbh-bot/internal/observability"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	tradecore "github.com/ruzickahunzeker/rbh-bot/internal/trade"
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
	if service == config.BotService {
		gates = append(gates, "bot_feed_consumer_configured")
	}
	if service == config.TradeService {
		gates = append(gates, "pons_curve_dry_run_configured")
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
	var botConsumer *botcore.Consumer
	var authenticator *ipc.Authenticator
	if service == config.FeedService || service == config.BotService || service == config.TradeService {
		authenticator, err = ipc.NewAuthenticator([]byte(cfg.InternalAuthSecret), 30*time.Second)
		if err != nil {
			return fmt.Errorf("configure business IPC authentication: %w", err)
		}
	}
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
		server.Handle("GET /internal/feed/outbox", authenticator.Middleware(feedcore.NewOutboxHTTPHandler(store)))
	}
	if service == config.BotService {
		store, err := botcore.NewStore(database)
		if err != nil {
			return err
		}
		source, err := botcore.NewHTTPOutboxSource(filepath.Join(cfg.SocketDir, string(config.FeedService)+".sock"), authenticator)
		if err != nil {
			return err
		}
		botConsumer, err = botcore.NewConsumer(store, source, 100)
		if err != nil {
			return err
		}
		ready.Set("bot_feed_consumer_configured", true)
	}
	var tradeBackend *tradecore.RPCBackend
	if service == config.TradeService {
		if cfg.RPCURL == "" || cfg.DryRunWalletID == "" || !common.IsHexAddress(cfg.DryRunFromAddress) || common.HexToAddress(cfg.DryRunFromAddress) == (common.Address{}) || cfg.ExecutionPrivateKey == "" || cfg.ArtifactEncryptionKey == "" || cfg.ArtifactKeyVersion == "" {
			return errors.New("trade execution kernel configuration is incomplete")
		}
		store, err := tradecore.NewStore(database)
		if err != nil {
			return err
		}
		if err := store.RegisterDryRunWallet(ctx, cfg.DryRunWalletID, common.HexToAddress(cfg.DryRunFromAddress)); err != nil {
			return err
		}
		tradeBackend, err = tradecore.DialRPCBackend(ctx, cfg.RPCURL)
		if err != nil {
			return err
		}
		defer tradeBackend.Close()
		engine, err := tradecore.NewEngine(store, tradeBackend)
		if err != nil {
			return err
		}
		if _, err := engine.Recover(ctx); err != nil {
			return fmt.Errorf("recover trade dry-runs: %w", err)
		}
		server.Handle("POST /internal/trade/dry-run", authenticator.Middleware(tradecore.NewDryRunHTTPHandler(engine)))
		privateKey, err := crypto.HexToECDSA(cfg.ExecutionPrivateKey)
		if err != nil {
			return errors.New("invalid execution signer configuration")
		}
		signer, err := tradecore.NewLocalSigner(privateKey)
		if err != nil || signer.Address() != common.HexToAddress(cfg.DryRunFromAddress) {
			return errors.New("execution signer does not match configured wallet")
		}
		encryptionKey, err := hex.DecodeString(cfg.ArtifactEncryptionKey)
		if err != nil {
			return errors.New("invalid artifact encryption configuration")
		}
		artifactCipher, err := tradecore.NewAESGCMCipher(cfg.ArtifactKeyVersion, encryptionKey)
		if err != nil {
			return errors.New("invalid artifact encryption configuration")
		}
		kernel, err := tradecore.NewExecutionKernel(store, tradeBackend, signer, artifactCipher)
		if err != nil {
			return err
		}
		server.Handle("POST /internal/trade/prepare-execution", authenticator.Middleware(tradecore.NewPrepareExecutionHandler(kernel)))
		ready.Set("pons_curve_dry_run_configured", true)
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
	if botConsumer != nil {
		go func() { errCh <- botConsumer.Run(ctx, 100*time.Millisecond) }()
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
