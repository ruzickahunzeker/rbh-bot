package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/arc"
	basechain "github.com/ruzickahunzeker/rbh-bot/internal/chains/base"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/elysium"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/ethereum"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/hyperevm"
	botcore "github.com/ruzickahunzeker/rbh-bot/internal/bot"
	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	feedcore "github.com/ruzickahunzeker/rbh-bot/internal/feed"
	"github.com/ruzickahunzeker/rbh-bot/internal/health"
	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
	"github.com/ruzickahunzeker/rbh-bot/internal/observability"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/telegram"
	tradecore "github.com/ruzickahunzeker/rbh-bot/internal/trade"
	"github.com/ruzickahunzeker/rbh-bot/internal/tradeapi"
)

func Run(service config.Service) error {
	cfg, err := config.Load(service)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runConfigured(ctx, cfg)
}

// runConfigured is the same production startup path with caller-owned lifetime.
// Tests supply cancellation instead of sending process-wide signals.
func runConfigured(parent context.Context, cfg config.Config) error {
	if parent == nil {
		return errors.New("application context is required")
	}
	if err := cfg.ValidateTradeStartupProfile(); err != nil {
		return err
	}
	if err := cfg.ValidateTimeouts(); err != nil {
		return err
	}
	service := cfg.Service
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
		gates = append(gates, "recovery_ready")
		switch cfg.TradeStartupProfile {
		case config.TradeProfileRecoveryOnly:
		case config.TradeProfileQueryAndRecovery:
			gates = append(gates, "trade_api_query_ready")
		default:
			gates = append(gates, "pons_curve_dry_run_configured")
		}
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
	if cfg.EnforceChainIdentity && (service == config.TradeService || service == config.FeedService) {
		slug := cfg.ChainSlug
		if slug == "" && cfg.ChainID == config.ChainID {
			slug = "rbh"
		}
		netKind := cfg.NetworkKind
		if netKind == "" {
			netKind = "mainnet"
		}
		if err := database.VerifyOrInitChainIdentity(context.Background(), storage.ChainIdentity{
			ChainID:     cfg.ChainID,
			ChainSlug:   slug,
			NetworkKind: netKind,
		}); err != nil {
			return err
		}
	}
	if err := database.Migrate(context.Background()); err != nil {
		return err
	}
	ready.Set("migrations_current", true)

	ctx, stop := context.WithCancel(parent)
	defer stop()
	server := health.New(string(service), cfg.Socket, log, ready)
	var runner *feedcore.SequencerRunner
	var evmRunner *feedcore.EVMReceiptRunner
	var botConsumer *botcore.Consumer
	var botMultiRuntime *botcore.MultiChainRuntime
	var priceTickWorker *botcore.PriceTickWorker
	var tgPoller *telegram.Poller
	var canaryProduction *tradecore.ProductionComposition
	var recoveryLoop *recoveryLifetime
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
			if err := writeFeedMetrics(metricCtx, writer, store); err != nil {
				return err
			}
			return writeRuntimeMetrics(writer, database, authenticator)
		})
		watchlistStore, err := feedcore.NewWatchlistStore(database)
		if err != nil {
			return fmt.Errorf("initialize feed watchlist store: %w", err)
		}
		runner.SetWatchlist(watchlistStore)
		if cfg.ChainID != config.ChainID && cfg.RPCURL != "" {
			evmRunner, err = feedcore.NewEVMReceiptRunner(cfg.ChainID, wrappedNativeForChain(cfg.ChainID), cfg.RPCURL, store, watchlistStore, 0)
			if err != nil {
				return err
			}
			runner = nil
		}
		watchlistHandler := ipc.RequestContext(authenticator.Middleware(feedcore.NewWatchlistHTTPHandler(watchlistStore)))
		server.Handle("GET /internal/feed/outbox", ipc.RequestContext(authenticator.Middleware(feedcore.NewOutboxHTTPHandler(store))))
		server.Handle("GET /internal/v1/watchlist", watchlistHandler)
		server.Handle("PUT /internal/v1/watchlist", watchlistHandler)
	}
	if service == config.BotService {
		store, err := botcore.NewStore(database)
		if err != nil {
			return err
		}
		feedSock := filepath.Join(cfg.SocketDir, string(config.FeedService)+".sock")
		feedSockBSC := cfg.FeedSocketBSC
		if feedSockBSC == "" && cfg.ChainID == config.BSCChainID {
			feedSockBSC = feedSock
		}
		feedSockRBH := cfg.FeedSocketRBH
		if feedSockRBH == "" && cfg.ChainID == config.ChainID {
			feedSockRBH = feedSock
		}
		source, err := botcore.NewHTTPOutboxSource(feedSock, authenticator)
		if err != nil {
			return err
		}
		botConsumer, err = botcore.NewConsumer(store, source, 100)
		if err != nil {
			return err
		}
		var udsBackend *telegram.UDSBackend
		if cfg.TelegramBotToken != "" || cfg.PaperShadowEnabled {
			udsBackend = telegram.NewUDSBackendWithWatchlist(map[uint64]telegram.ChainUDSConfig{
				config.ChainID: {
					ChainSlug:   "rbh",
					TradeSocket: cfg.TradeSocketRBH,
					BearerToken: cfg.TradeAPITokenRBH,
					FeedSocket:  feedSockRBH,
					RPCURL:      cfg.RPCURLRBH,
				},
				config.BSCChainID: {
					ChainSlug:   "bsc",
					TradeSocket: cfg.TradeSocketBSC,
					BearerToken: cfg.TradeAPITokenBSC,
					FeedSocket:  feedSockBSC,
					RPCURL:      cfg.RPCURL,
				},
			}, database, authenticator)
		}
		if cfg.TelegramBotToken != "" {
			tgRouter, err := telegram.NewRouter(database, udsBackend, nil)
			if err != nil {
				return fmt.Errorf("initialize telegram router: %w", err)
			}
			for _, chatID := range cfg.TelegramAllowedChatIDs {
				if err := tgRouter.AllowChat(ctx, chatID, "configured-operator", "operator", cfg.ChainID, cfg.DryRunWalletID); err != nil {
					return fmt.Errorf("seed telegram allowlist chat %d: %w", chatID, err)
				}
			}
			tgPoller, err = telegram.NewPoller(cfg.TelegramBotToken, tgRouter, log)
			if err != nil {
				return fmt.Errorf("initialize telegram poller: %w", err)
			}
		}
		if cfg.PaperShadowEnabled {
			shadowEngine, err := botcore.NewPaperShadowEngine(store, true)
			if err != nil {
				return fmt.Errorf("initialize paper shadow engine: %w", err)
			}
			walletID := cfg.DryRunWalletID
			if walletID == "" {
				walletID = "wallet-readonly"
			}
			var streams []botcore.FeedStreamConfig
			if feedSockBSC != "" {
				bscSrc, err := botcore.NewHTTPOutboxSource(feedSockBSC, authenticator)
				if err != nil {
					return err
				}
				bscWallet := walletID
				if cfg.ChainID != config.BSCChainID {
					bscWallet = "wallet-bsc-readonly"
				}
				streams = append(streams, botcore.FeedStreamConfig{
					ChainID:        config.BSCChainID,
					FeedInstanceID: "feed-bsc",
					Source:         bscSrc,
					PrincipalID:    "operator-local",
					StrategyID:     "shadow-bsc",
					WalletID:       bscWallet,
					FixedAmountRaw: big.NewInt(10_000_000_000_000_000), // 0.01 BNB
					MaxSignalAge:   60 * time.Second,
				})
			}
			if feedSockRBH != "" {
				rbhSrc, err := botcore.NewHTTPOutboxSource(feedSockRBH, authenticator)
				if err != nil {
					return err
				}
				rbhWallet := walletID
				if cfg.ChainID != config.ChainID {
					rbhWallet = "wallet-rbh-readonly"
				}
				streams = append(streams, botcore.FeedStreamConfig{
					ChainID:        config.ChainID,
					FeedInstanceID: "feed-rbh",
					Source:         rbhSrc,
					PrincipalID:    "operator-local",
					StrategyID:     "shadow-rbh",
					WalletID:       rbhWallet,
					FixedAmountRaw: big.NewInt(10_000_000_000_000_000), // 0.01 ETH
					MaxSignalAge:   60 * time.Second,
				})
			}
			if len(streams) == 0 {
				streams = append(streams, botcore.FeedStreamConfig{
					ChainID:        cfg.ChainID,
					FeedInstanceID: "feed-" + cfg.ChainSlug,
					Source:         source,
					PrincipalID:    "operator-local",
					StrategyID:     "shadow-" + cfg.ChainSlug,
					WalletID:       walletID,
					FixedAmountRaw: big.NewInt(10_000_000_000_000_000),
					MaxSignalAge:   60 * time.Second,
				})
			}
			botMultiRuntime, err = botcore.NewMultiChainRuntime(
				store,
				streams,
				shadowEngine,
				udsBackend,
				nil,
				100,
				nil,
			)
			if err != nil {
				return fmt.Errorf("initialize multi-chain shadow runtime: %w", err)
			}
			if tgPoller != nil && len(cfg.TelegramAllowedChatIDs) > 0 {
				allowed := append([]int64(nil), cfg.TelegramAllowedChatIDs...)
				pollerRef := tgPoller
				botMultiRuntime.SetOnShadowFill(func(chainID uint64, wID string, tok common.Address, side string, amtIn, outRaw *big.Int) {
					msg := fmt.Sprintf("[SHADOW COPY] chain=%d wallet=%s\nside=%s token=%s\nin=%s quoted_out=%s",
						chainID, wID, side, strings.ToLower(tok.Hex()), amtIn.String(), outRaw.String())
					for _, cid := range allowed {
						_ = pollerRef.SendMessage(context.Background(), cid, msg)
					}
				})
				priceTickWorker, err = botcore.NewPriceTickWorker(store, shadowEngine, udsBackend, func(ord botcore.ConditionalOrder, spotWei, simOut *big.Int) {
					msg := fmt.Sprintf("🎯 [%s 已触发执行] 链=%d 订单=%s\n合约=%s\n现价=%s 模拟成交=%s",
						strings.ToUpper(string(ord.Kind)), ord.ChainID, ord.OrderID, strings.ToLower(ord.Token.Hex()), spotWei.String(), simOut.String())
					for _, cid := range allowed {
						_ = pollerRef.SendMessage(context.Background(), cid, msg)
					}
				})
				if err != nil {
					return fmt.Errorf("initialize price tick worker: %w", err)
				}
				if sniperEng, sErr := botcore.NewSniperEngine(store, shadowEngine, udsBackend, func(task botcore.SniperTask, simOut *big.Int) {
					msg := fmt.Sprintf("🎯 [狙击已触发 %s] 链=%d 任务=%s\n合约=%s\n投入=%s 模拟成交=%s",
						strings.ToUpper(string(task.TriggerKind)), task.ChainID, task.TaskID, strings.ToLower(task.Token.Hex()), task.AmountInWei.String(), simOut.String())
					for _, cid := range allowed {
						_ = pollerRef.SendMessage(context.Background(), cid, msg)
					}
				}); sErr == nil {
					botMultiRuntime.SetSniperEngine(sniperEng)
				}
			} else {
				priceTickWorker, err = botcore.NewPriceTickWorker(store, shadowEngine, udsBackend, nil)
				if err != nil {
					return fmt.Errorf("initialize price tick worker: %w", err)
				}
			}
			botConsumer = nil
		}
		server.SetMetricsWriter(func(_ context.Context, writer io.Writer) error {
			return writeRuntimeMetrics(writer, database, authenticator)
		})
		ready.Set("bot_feed_consumer_configured", true)
	}
	var tradeBackend *tradecore.RPCBackend
	var userTradeAPI *tradeapi.API
	if service == config.TradeService {
		store, err := tradecore.NewStore(database)
		if err != nil {
			return err
		}
		var artifactCipher tradecore.ArtifactCipher
		if cfg.TradeStartupProfile == config.TradeProfileRecoveryOnly {
			artifactCipher, err = loadRecoveryCipher(cfg)
			if err != nil {
				return err
			}
		} else if cfg.TradeStartupProfile == config.TradeProfileQueryAndRecovery {
			artifactCipher, err = loadRecoveryCipher(cfg)
			if err != nil {
				return err
			}
			userTradeAPI, err = configureUserTradeAPIWithHandle(server, database, cfg)
			if err != nil {
				return err
			}
			ready.Set("trade_api_query_ready", true)
		} else {
			if cfg.RPCURL == "" || cfg.DryRunWalletID == "" || !common.IsHexAddress(cfg.DryRunFromAddress) || common.HexToAddress(cfg.DryRunFromAddress) == (common.Address{}) || cfg.ExecutionPrivateKey == "" || cfg.ArtifactEncryptionKey == "" || cfg.ArtifactKeyVersion == "" {
				return errors.New("trade execution kernel configuration is incomplete")
			}
			if err := store.RegisterDryRunWallet(ctx, cfg.DryRunWalletID, common.HexToAddress(cfg.DryRunFromAddress)); err != nil {
				return err
			}
			userTradeAPI, err = configureUserTradeAPIWithHandle(server, database, cfg)
			if err != nil {
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
			server.Handle("POST /internal/trade/dry-run", ipc.RequestContext(authenticator.Middleware(tradecore.NewDryRunHTTPHandler(engine))))
			privateKey, err := crypto.HexToECDSA(cfg.ExecutionPrivateKey)
			if err != nil {
				return errors.New("invalid execution signer configuration")
			}
			signer, err := tradecore.NewLocalSigner(privateKey)
			if err != nil || signer.Address() != common.HexToAddress(cfg.DryRunFromAddress) {
				return errors.New("execution signer does not match configured wallet")
			}
			keys, err := cfg.ParseArtifactKeyring()
			if err != nil {
				return errors.New("invalid artifact encryption configuration")
			}
			artifactCipher, err = tradecore.NewAESGCMKeyring(cfg.ArtifactKeyVersion, keys)
			for _, k := range keys {
				clear(k)
			}
			if err != nil {
				return errors.New("invalid artifact encryption configuration")
			}
			kernel, err := tradecore.NewExecutionKernel(store, tradeBackend, signer, artifactCipher)
			if err != nil {
				return err
			}
			server.Handle("POST /internal/trade/prepare-execution", ipc.RequestContext(authenticator.Middleware(tradecore.NewPrepareExecutionHandler(kernel))))
			ready.Set("pons_curve_dry_run_configured", true)
		}
		if cfg.TradeStartupProfile == config.TradeProfileQueryAndRecovery && (cfg.ChainID != config.ChainID || cfg.ReadOnlyQueryOnly) {
			// Read-only QUERY_AND_RECOVERY instances on non-RBH chains or with explicit EVM_READONLY_QUERY_ONLY=true
			// do not bind to the Robinhood V1 Pons-Curve canary_control_state(4663) recovery worker.
			server.SetMetricsWriter(func(_ context.Context, writer io.Writer) error {
				return writeRuntimeMetrics(writer, database, authenticator)
			})
			ready.Set("recovery_ready", true)
		} else {
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
			recoveryLoop, err = startRecoveryLifetime(ctx, canaryProduction)
			if err != nil {
				return fmt.Errorf("start production recovery lifecycle: %w", err)
			}
			// Registered after RPC.Close: always drain before closing RPC/SQLite,
			// including a listener failure after the durable lease was acquired.
			defer recoveryLoop.stopAndWait()
			server.SetMetricsWriter(func(metricCtx context.Context, writer io.Writer) error {
				if err := canaryProduction.WriteMetrics(metricCtx, writer); err != nil {
					return err
				}
				return writeRuntimeMetrics(writer, database, authenticator)
			})
			ready.Set("recovery_ready", canaryProduction.Readiness().RecoveryReady)
		}
	}

	if cfg.HTTPWriteTimeout > 0 {
		server.SetTimeouts(0, cfg.HTTPWriteTimeout, 0)
	}
	listener, err := server.Listen()
	if err != nil {
		return err
	}
	var adminServer *health.Server
	var adminListener net.Listener
	if cfg.AdminSocket != "" {
		adminControl := &runtimeEmergencyControl{}
		var isInFlight func(string) bool
		if userTradeAPI != nil {
			isInFlight = userTradeAPI.IsOperationInFlight
		}
		adminServer = newAdminServer(string(service), cfg.AdminSocket, cfg.ChainID, log, database, adminControl, isInFlight)
		adminListener, err = adminServer.Listen()
		if err != nil {
			_ = listener.Close()
			return fmt.Errorf("listen admin socket: %w", err)
		}
		defer func() { _ = adminListener.Close() }()
	}

	ready.Set("socket_bound", true)
	errCh := make(chan error, 8)
	reportErr := func(e error) {
		select {
		case errCh <- e:
		default:
		}
	}
	var bgWG sync.WaitGroup
	bgWG.Add(1)
	go func() {
		defer bgWG.Done()
		reportErr(server.Serve(listener))
	}()
	if adminServer != nil && adminListener != nil {
		bgWG.Add(1)
		go func() {
			defer bgWG.Done()
			reportErr(adminServer.Serve(adminListener))
		}()
	}
	if runner != nil {
		bgWG.Add(2)
		go func() {
			defer bgWG.Done()
			reportErr(runner.Run(ctx))
		}()
		go func() {
			defer bgWG.Done()
			monitorFeedReadiness(ctx, ready, runner)
		}()
	}
	if evmRunner != nil {
		bgWG.Add(2)
		go func() {
			defer bgWG.Done()
			reportErr(evmRunner.Run(ctx, 500*time.Millisecond))
		}()
		go func() {
			defer bgWG.Done()
			monitorEVMFeedReadiness(ctx, ready, evmRunner)
		}()
	}
	if botConsumer != nil {
		bgWG.Add(1)
		go func() {
			defer bgWG.Done()
			reportErr(botConsumer.Run(ctx, 100*time.Millisecond))
		}()
	}
	if botMultiRuntime != nil {
		bgWG.Add(1)
		go func() {
			defer bgWG.Done()
			reportErr(botMultiRuntime.Run(ctx, 500*time.Millisecond))
		}()
	}
	if priceTickWorker != nil {
		bgWG.Add(1)
		go func() {
			defer bgWG.Done()
			reportErr(priceTickWorker.Run(ctx, 10*time.Second))
		}()
	}
	if tgPoller != nil {
		bgWG.Add(1)
		go func() {
			defer bgWG.Done()
			reportErr(tgPoller.Run(ctx))
		}()
	}
	if canaryProduction != nil {
		bgWG.Add(2)
		go func() {
			defer bgWG.Done()
			<-recoveryLoop.done
			reportErr(recoveryLoop.err)
		}()
		go func() {
			defer bgWG.Done()
			monitorRecoveryReadiness(ctx, ready, canaryProduction)
		}()
	}

	drainAll := func() error {
		stop()
		err := shutdownApplication(server, canaryProduction)
		if adminServer != nil {
			if adminErr := shutdownServer(adminServer); adminErr != nil {
				err = errors.Join(err, adminErr)
			}
		}
		if recoveryLoop != nil {
			recoveryLoop.stopAndWait()
		}
		bgWG.Wait()
		return err
	}

	select {
	case err := <-errCh:
		wasCanceled := ctx.Err() != nil
		if wasCanceled {
			return drainAll()
		}
		if shutdownErr := drainAll(); shutdownErr != nil {
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
		return drainAll()
	}
}

func writeRuntimeMetrics(writer io.Writer, database *storage.Database, authenticator *ipc.Authenticator) error {
	if database != nil {
		if _, err := fmt.Fprintf(writer, "# TYPE rbh_sqlite_wal_bytes gauge\nrbh_sqlite_wal_bytes %d\n", database.WALSizeBytes()); err != nil {
			return err
		}
	}
	if authenticator != nil {
		size, capacity := authenticator.ReplayCacheStats()
		if _, err := fmt.Fprintf(writer, "# TYPE rbh_ipc_replay_cache_size gauge\nrbh_ipc_replay_cache_size %d\n# TYPE rbh_ipc_replay_cache_capacity gauge\nrbh_ipc_replay_cache_capacity %d\n", size, capacity); err != nil {
			return err
		}
	}
	return nil
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

func monitorEVMFeedReadiness(ctx context.Context, ready *health.Readiness, runner *feedcore.EVMReceiptRunner) {
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

func wrappedNativeForChain(chainID uint64) common.Address {
	switch chainID {
	case config.ETHChainID:
		return ethereum.WETHAddress
	case config.BSCChainID:
		return bsc.WBNBAddress
	case config.HyperEVMChainID:
		return hyperevm.WHYPEAddress
	case config.ArcChainID:
		return arc.WrappedUSDCAddress
	case config.BaseChainID:
		return basechain.WETHAddress
	case config.ElysiumChainID:
		return elysium.WHYPEAddress
	default:
		return common.Address{}
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
