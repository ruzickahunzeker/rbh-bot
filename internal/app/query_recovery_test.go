package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestQueryAndRecoveryStartupWithAdminSocketAndMetrics(t *testing.T) {
	rpc := &recoveryRPCFixture{calls: map[string]int{}}
	rpcServer := httptest.NewServer(rpc)
	defer rpcServer.Close()

	root := t.TempDir()
	dbPath := filepath.Join(root, "trade.db")
	// Pre-seed migrated DB with registered wallet so user trade API binds cleanly.
	seedDB, err := storage.Open(context.Background(), storage.TradeOwner, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedDB.Migrate(context.Background()); err != nil {
		_ = seedDB.Close()
		t.Fatal(err)
	}
	sqlDB, err := seedDB.SQLDB(storage.TradeOwner)
	if err != nil {
		_ = seedDB.Close()
		t.Fatal(err)
	}
	const walletAddr = "0x1000000000000000000000000000000000000001"
	if _, err := sqlDB.Exec(`INSERT INTO dry_run_wallets VALUES('wallet-01',4663,?,1)`, walletAddr); err != nil {
		_ = seedDB.Close()
		t.Fatal(err)
	}
	if err := seedDB.Close(); err != nil {
		t.Fatal(err)
	}

	apiCfgPath := filepath.Join(root, "trade-api.json")
	apiCfg := `{"principal_id":"tg-bot","wallet_id":"wallet-01","wallet_address":"` + walletAddr + `","token":"secret-user-token-0123456789abcdef"}`
	if err := os.WriteFile(apiCfgPath, []byte(apiCfg), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Service:                config.TradeService,
		TradeStartupProfile:    config.TradeProfileQueryAndRecovery,
		DataDir:                root,
		Database:               dbPath,
		Socket:                 filepath.Join(root, "trade.sock"),
		AdminSocket:            filepath.Join(root, "trade-admin.sock"),
		LogLevel:               "ERROR",
		ChainID:                4663,
		InternalAuthSecret:     strings.Repeat("s", 32),
		RPCURL:                 rpcServer.URL,
		CanaryProductionMode:   config.CanaryProductionModeDisabled,
		ArtifactKeyVersion:     "v2",
		ArtifactEncryptionKey:  strings.Repeat("22", 32),
		ArtifactHistoricalKeys: "v1=" + strings.Repeat("11", 32),
		TradeAPIConfigFile:     apiCfgPath,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runConfigured(ctx, cfg) }()

	udsClient := func(sock string) *http.Client {
		return &http.Client{
			Transport: &http.Transport{
				DialContext: func(c context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(c, "unix", sock)
				},
			},
			Timeout: 3 * time.Second,
		}
	}
	bizClient := udsClient(cfg.Socket)
	adminClient := udsClient(cfg.AdminSocket)

	// Wait until /ready reports 200 with both trade_api_query_ready and recovery_ready.
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := bizClient.Get("http://unix/ready")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"trade_api_query_ready":true`) && strings.Contains(string(body), `"recovery_ready":true`) {
				break
			}
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("timed out waiting for QUERY_AND_RECOVERY readiness; run err=%v", <-done)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Verify admin socket has strict 0600 permissions.
	info, err := os.Stat(cfg.AdminSocket)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		cancel()
		t.Fatalf("admin socket mode=%04o, want 0600", got)
	}

	// Verify /metrics emits rbh_sqlite_wal_bytes and rbh_ipc_replay_cache_size.
	mResp, err := bizClient.Get("http://unix/metrics")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	mBody, _ := io.ReadAll(mResp.Body)
	_ = mResp.Body.Close()
	if !strings.Contains(string(mBody), "rbh_sqlite_wal_bytes") || !strings.Contains(string(mBody), "rbh_ipc_replay_cache_size") {
		cancel()
		t.Fatalf("missing runtime metrics in /metrics:\n%s", string(mBody))
	}

	// Verify admin /ready returns 200 OK.
	adminReadyResp, err := adminClient.Get("http://unix/ready")
	if err != nil || adminReadyResp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("admin /ready failed: err=%v resp=%v", err, adminReadyResp)
	}
	_ = adminReadyResp.Body.Close()

	// Verify admin emergency-stop persists emergency_stopped=1 into authoritative canary_control_state.
	stopResp, err := adminClient.Post("http://unix/admin/v1/control/emergency-stop", "application/json", bytes.NewReader([]byte(`{"reason":"drill"}`)))
	if err != nil || stopResp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("admin emergency-stop failed: err=%v resp=%v", err, stopResp)
	}
	var stopPayload map[string]any
	_ = json.NewDecoder(stopResp.Body).Decode(&stopPayload)
	_ = stopResp.Body.Close()
	if stopPayload["emergency_stopped"] != true {
		cancel()
		t.Fatalf("expected emergency_stopped=true, got %#v", stopPayload)
	}

	// Verify durable SQLite canary_control_state reflects emergency_stopped=1.
	verifyDB, err := storage.Open(context.Background(), storage.TradeOwner, dbPath)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	readPool, err := verifyDB.ReadDB(storage.TradeOwner)
	if err != nil {
		_ = verifyDB.Close()
		cancel()
		t.Fatal(err)
	}
	var dbStopped int
	if err := readPool.QueryRow(`SELECT emergency_stopped FROM canary_control_state WHERE singleton = 1`).Scan(&dbStopped); err != nil || dbStopped != 1 {
		_ = verifyDB.Close()
		cancel()
		t.Fatalf("expected canary_control_state.emergency_stopped=1, got %d err=%v", dbStopped, err)
	}
	// Verify resume clears canary_control_state.emergency_stopped=0.
	resumeResp, err := adminClient.Post("http://unix/admin/v1/control/resume", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil || resumeResp.StatusCode != http.StatusOK {
		_ = verifyDB.Close()
		cancel()
		t.Fatalf("admin resume failed: err=%v resp=%v", err, resumeResp)
	}
	_ = resumeResp.Body.Close()
	if err := readPool.QueryRow(`SELECT emergency_stopped FROM canary_control_state WHERE singleton = 1`).Scan(&dbStopped); err != nil || dbStopped != 0 {
		_ = verifyDB.Close()
		cancel()
		t.Fatalf("expected canary_control_state.emergency_stopped=0 after resume, got %d err=%v", dbStopped, err)
	}
	_ = verifyDB.Close()

	// Verify admin online VACUUM INTO backup over 0600 admin socket.
	backupDest := filepath.Join(root, "backup", "trade-snap.db")
	bReq, _ := json.Marshal(map[string]string{"dest": backupDest})
	bResp, err := adminClient.Post("http://unix/admin/v1/backup", "application/json", bytes.NewReader(bReq))
	if err != nil || bResp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("admin backup failed: err=%v resp=%v", err, bResp)
	}
	var bPayload map[string]any
	_ = json.NewDecoder(bResp.Body).Decode(&bPayload)
	_ = bResp.Body.Close()
	if _, err := os.Stat(backupDest); err != nil {
		cancel()
		t.Fatalf("backup file not created: %v", err)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("graceful shutdown failed: %v", err)
	}
}

func TestRunConfiguredEnforcesChainIdentityBeforeMigration(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "bsc.db")
	// Bind database to BSC (56).
	db, err := storage.Open(context.Background(), storage.TradeOwner, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.VerifyOrInitChainIdentity(context.Background(), storage.ChainIdentity{ChainID: 56, ChainSlug: "bsc", NetworkKind: "mainnet"}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	// Starting an RBH (4663) instance with EnforceChainIdentity=true must fail before migrations.
	cfg := config.Config{
		Service:               config.TradeService,
		TradeStartupProfile:   config.TradeProfileRecoveryOnly,
		DataDir:               root,
		Database:              dbPath,
		Socket:                filepath.Join(root, "trade.sock"),
		LogLevel:              "ERROR",
		ChainID:               4663,
		ChainSlug:             "rbh",
		NetworkKind:           "mainnet",
		EnforceChainIdentity:  true,
		InternalAuthSecret:    strings.Repeat("s", 32),
		RPCURL:                "http://127.0.0.1:1",
		CanaryProductionMode:  config.CanaryProductionModeDisabled,
		ArtifactKeyVersion:    "v1",
		ArtifactEncryptionKey: strings.Repeat("11", 32),
	}
	err = runConfigured(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "chain identity") {
		t.Fatalf("expected wrong-chain database rejection before migration, got %v", err)
	}
}
