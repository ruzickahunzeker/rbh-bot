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
		gates = append(gates, "pons_curve_dry_run_configured", "recovery_ready")
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
	var canaryProduction *tradecore.ProductionComposition
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
		recoveryRPC, err := tradecore.DialReadOnlyRecoveryRPC(ctx, cfg.RPCURL)
		if err != nil {
			return fmt.Errorf("configure read-only recovery RPC: %w", err)
		}
		defer recoveryRPC.Close()
		effectResolver, err := tradecore.NewPonsCurveEffectResolver(store)
		if err != nil {
			return err
		}
		recoveryService, err := tradecore.NewRecoveryServiceWithCipher(store, artifactCipher, recoveryRPC, effectResolver, tradecore.CanonicalPolicy{})
		if err != nil {
			return err
		}
		recoveryQuerier, err := tradecore.NewProductionRecoveryQuerier(store, artifactCipher, recoveryRPC)
		if err != nil {
			return err
		}
		canaryProduction, err = tradecore.NewProductionComposition(store, cfg.CanaryProductionMode)
		if err != nil {
			return fmt.Errorf("configure controlled canary production lifecycle: %w", err)
		}
		hostname, _ := os.Hostname()
		holder := fmt.Sprintf("%s:%d", hostname, os.Getpid())
		if err = canaryProduction.ConfigureRecovery(recoveryService, recoveryQuerier, recoveryRPC, tradecore.ProductionRecoveryConfig{Environment: "production", HolderID: holder, LeaseTTL: 30 * time.Second, ScanInterval: time.Second}); err != nil {
			return fmt.Errorf("configure production recovery lifecycle: %w", err)
		}
		if err = canaryProduction.Start(ctx); err != nil {
			return fmt.Errorf("start production recovery lifecycle: %w", err)
		}
		server.SetMetricsWriter(canaryProduction.WriteMetrics)
		ready.Set("recovery_ready", canaryProduction.Readiness().RecoveryReady)
		ready.Set("pons_curve_dry_run_configured", true)
	}

	listener, err := server.Listen()
	if err != nil {
		return err
	}
	ready.Set("socket_bound", true)
	errCh := make(chan error, 4)
	go func() { errCh <- server.Serve(listener) }()
	if runner != nil {
		go func() { errCh <- runner.Run(ctx) }()
		go monitorFeedReadiness(ctx, ready, runner)
	}
	if botConsumer != nil {
		go func() { errCh <- botConsumer.Run(ctx, 100*time.Millisecond) }()
	}
	if canaryProduction != nil {
		go func() { errCh <- canaryProduction.Run(ctx) }()
		go monitorRecoveryReadiness(ctx, ready, canaryProduction)
	}

	select {
	case err := <-errCh:
		wasCanceled := ctx.Err() != nil
		stop()
		if wasCanceled {
			return shutdownApplication(server, canaryProduction)
		}
		if shutdownErr := shutdownApplication(server, canaryProduction); shutdownErr != nil {
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
		return shutdownApplication(server, canaryProduction)
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

func monitorRecoveryReadiness(ctx context.Context, ready *health.Readiness, lifecycle *tradecore.ProductionComposition) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready.Set("recovery_ready", lifecycle.Readiness().RecoveryReady)
		select {
		case <-ctx.Done():
			ready.Set("recovery_ready", false)
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

func shutdownApplication(server *health.Server, recovery *tradecore.ProductionComposition) error {
	shutdown := func() error { return shutdownServer(server) }
	if recovery == nil {
		return shutdown()
	}
	return shutdownAndDrain(shutdown, recovery.Wait)
}

// RPC calls have bounded deadlines, but a durable scan can span multiple calls
// and items. Never close the DB/RPC handles while that scan is still draining,
// including when HTTP shutdown itself fails.
func shutdownAndDrain(shutdown func() error, wait func(context.Context) error) error {
	shutdownErr := shutdown()
	if err := wait(context.Background()); err != nil {
		return errors.Join(shutdownErr, fmt.Errorf("drain production recovery: %w", err))
	}
	return shutdownErr
}
