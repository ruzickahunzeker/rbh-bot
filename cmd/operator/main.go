package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	"github.com/ruzickahunzeker/rbh-bot/internal/preflight"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	tradecore "github.com/ruzickahunzeker/rbh-bot/internal/trade"
	"github.com/ruzickahunzeker/rbh-bot/internal/tradeapi"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "db":
		if len(os.Args) < 3 {
			usage()
			os.Exit(2)
		}
		switch os.Args[2] {
		case "bind-chain":
			if err := runBindChain(os.Args[3:]); err != nil {
				fmt.Fprintf(os.Stderr, "operator db bind-chain: %v\n", err)
				os.Exit(1)
			}
		case "register-wallet":
			if err := runRegisterWallet(os.Args[3:]); err != nil {
				fmt.Fprintf(os.Stderr, "operator db register-wallet: %v\n", err)
				os.Exit(1)
			}
		default:
			usage()
			os.Exit(2)
		}
	case "admin":
		if len(os.Args) < 3 {
			usage()
			os.Exit(2)
		}
		if err := runAdmin(os.Args[2], os.Args[3:]); err != nil {
			fmt.Fprintf(os.Stderr, "operator admin %s: %v\n", os.Args[2], err)
			os.Exit(1)
		}
	case "preflight":
		if err := runPreflight(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "operator preflight: %v\n", err)
			os.Exit(1)
		}
	case "wallet":
		if len(os.Args) < 3 {
			usage()
			os.Exit(2)
		}
		if err := runWallet(os.Args[2], os.Args[3:]); err != nil {
			fmt.Fprintf(os.Stderr, "operator wallet %s: %v\n", os.Args[2], err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  operator db bind-chain --db <path> --owner <trade-service|feed-service|bot-service> --expected-chain-id <id> --chain-slug <slug> [--network-kind mainnet]
  operator db register-wallet --db <path> --chain-id <4663|56|8453> --wallet-id <id> --address <0x...>
  operator wallet create --dir <keystore-dir> --wallet-id <id> --chain-id <56|4663> [--db <trade.db>]
  operator wallet import --dir <keystore-dir> --wallet-id <id> --chain-id <56|4663> [--db <trade.db>]
  operator wallet list --dir <keystore-dir>
  operator admin emergency-stop --socket <admin.sock> [--reason <text>]
  operator admin resume --socket <admin.sock>
  operator admin backup --socket <admin.sock> --dest <absolute-path>
  operator preflight --db <path> --chain-id <4663|56|8453> --chain-slug <rbh|bsc|base> --backup-dir <dir>`)
}

func runWallet(action string, args []string) error {
	fs := flag.NewFlagSet("wallet-"+action, flag.ContinueOnError)
	dir := fs.String("dir", "", "directory for AES-256-GCM 0600 wallet keystores")
	walletID := fs.String("wallet-id", "", "wallet identifier")
	chainID := fs.Uint64("chain-id", 0, "EIP-155 chain_id (56 or 4663)")
	dbPath := fs.String("db", "", "optional trade.db path to also register wallet metadata")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("--dir is required")
	}
	switch action {
	case "list":
		list, err := tradecore.ListEncryptedWallets(*dir)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("no encrypted wallets found")
			return nil
		}
		for _, w := range list {
			fmt.Printf("%s chain_id=%d address=%s file=%s\n", w.WalletID, w.ChainID, w.Address.Hex(), w.FilePath)
		}
		return nil
	case "create", "import":
		pass := os.Getenv("EVM_WALLET_PASSPHRASE")
		if len(pass) < 8 {
			return fmt.Errorf("set EVM_WALLET_PASSPHRASE (>= 8 chars) in environment; never pass passphrases as CLI flags")
		}
		var sum tradecore.KeystoreSummary
		var err error
		if action == "create" {
			sum, err = tradecore.CreateEncryptedWallet(*dir, *walletID, *chainID, pass)
		} else {
			privHex := os.Getenv("EVM_IMPORT_PRIVATE_KEY")
			if privHex == "" {
				return fmt.Errorf("set EVM_IMPORT_PRIVATE_KEY in environment; never pass private keys as CLI flags")
			}
			sum, err = tradecore.ImportEncryptedWallet(*dir, *walletID, *chainID, privHex, pass)
		}
		if err != nil {
			return err
		}
		fmt.Printf("encrypted wallet %s (chain_id=%d address=%s) saved to %s (mode 0600)\n", sum.WalletID, sum.ChainID, sum.Address.Hex(), sum.FilePath)
		if *dbPath != "" {
			return runRegisterWallet([]string{"--db", *dbPath, "--chain-id", fmt.Sprintf("%d", *chainID), "--wallet-id", *walletID, "--address", sum.Address.Hex()})
		}
		return nil
	default:
		return fmt.Errorf("unsupported wallet action %q (use create|import|list)", action)
	}
}

func runRegisterWallet(args []string) error {
	fs := flag.NewFlagSet("register-wallet", flag.ContinueOnError)
	dbPath := fs.String("db", "", "path to trade SQLite database")
	chainID := fs.Uint64("chain-id", 0, "EIP-155 chain_id (4663, 56, or 8453)")
	walletID := fs.String("wallet-id", "", "wallet identifier")
	addrHex := fs.String("address", "", "wallet 0x hex address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" || *chainID == 0 || *walletID == "" || !common.IsHexAddress(*addrHex) {
		return fmt.Errorf("--db, --chain-id, --wallet-id, and valid --address are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := storage.Open(ctx, storage.TradeOwner, *dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	addr := common.HexToAddress(*addrHex)
	if *chainID == config.ChainID {
		store, err := tradecore.NewStore(db)
		if err != nil {
			return err
		}
		if err := store.RegisterDryRunWallet(ctx, *walletID, addr); err != nil {
			return err
		}
	} else {
		store, err := tradeapi.NewStore(db)
		if err != nil {
			return err
		}
		if err := store.RegisterMultiChainWallet(ctx, *chainID, *walletID, addr, time.Now().UTC()); err != nil {
			return err
		}
	}
	fmt.Printf("registered wallet %s (%s) on chain_id=%d in %s\n", *walletID, addr.Hex(), *chainID, *dbPath)
	return nil
}

func runPreflight(args []string) error {
	fs := flag.NewFlagSet("preflight", flag.ContinueOnError)
	dbPath := fs.String("db", "", "path to SQLite database")
	chainID := fs.Uint64("chain-id", 4663, "target EIP-155 chain_id (4663, 56, or 8453)")
	chainSlug := fs.String("chain-slug", "rbh", "target chain slug (rbh, bsc, or base)")
	backupDir := fs.String("backup-dir", "", "directory for VACUUM INTO restore drill")
	socketPath := fs.String("socket", "", "optional business socket path")
	adminSocket := fs.String("admin-socket", "", "optional admin socket path")
	liveEnabled := fs.Bool("live-enabled", false, "configured live_enabled flag")
	preSendBudget := fs.Duration("pre-send-budget", 3*time.Second, "PreSendRPCBudget")
	broadcastTimeout := fs.Duration("broadcast-timeout", 5*time.Second, "BroadcastRPCTimeout")
	httpWriteTimeout := fs.Duration("http-write-timeout", 15*time.Second, "HTTPWriteTimeout")
	botClientTimeout := fs.Duration("bot-client-timeout", 25*time.Second, "BotClientTimeout")
	targetPublic := fs.Bool("target-public", false, "whether configured broadcast target is public mempool")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" || *backupDir == "" {
		return fmt.Errorf("--db and --backup-dir are required")
	}
	rep, err := preflight.Run(context.Background(), preflight.Input{
		Config: config.Config{
			Service:              config.TradeService,
			Database:             *dbPath,
			Socket:               *socketPath,
			AdminSocket:          *adminSocket,
			ChainID:              *chainID,
			ChainSlug:            *chainSlug,
			NetworkKind:          "mainnet",
			LiveEnabled:          *liveEnabled,
			CanaryProductionMode: config.CanaryProductionModeDisabled,
			PreSendRPCBudget:     *preSendBudget,
			BroadcastRPCTimeout:  *broadcastTimeout,
			HTTPWriteTimeout:     *httpWriteTimeout,
			BotClientTimeout:     *botClientTimeout,
		},
		BackupDir:    *backupDir,
		TargetPublic: *targetPublic,
	})
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rep)
	return err
}

func runBindChain(args []string) error {
	fs := flag.NewFlagSet("bind-chain", flag.ContinueOnError)
	dbPath := fs.String("db", "", "path to SQLite database")
	owner := fs.String("owner", string(storage.TradeOwner), "database owner (trade-service|feed-service|bot-service)")
	chainID := fs.Uint64("expected-chain-id", 0, "expected EIP-155 chain_id")
	chainSlug := fs.String("chain-slug", "", "canonical chain slug (e.g. rbh, bsc)")
	networkKind := fs.String("network-kind", "mainnet", "network kind (mainnet|testnet)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		return fmt.Errorf("--db is required")
	}
	info, err := os.Stat(*dbPath)
	if err != nil {
		return fmt.Errorf("existing database file not found: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("--db path %q is a directory", *dbPath)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := storage.Open(ctx, storage.Owner(*owner), *dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	identity := storage.ChainIdentity{ChainID: *chainID, ChainSlug: *chainSlug, NetworkKind: *networkKind}
	if err := db.BindLegacyChainIdentity(ctx, identity); err != nil {
		return err
	}
	fmt.Printf("bound %s to chain_id=%d chain_slug=%s network_kind=%s\n", *dbPath, identity.ChainID, identity.ChainSlug, identity.NetworkKind)
	return nil
}

func runAdmin(action string, args []string) error {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	socket := fs.String("socket", "", "path to 0600 admin unix socket")
	reason := fs.String("reason", "", "operator reason")
	dest := fs.String("dest", "", "absolute backup destination path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *socket == "" {
		return fmt.Errorf("--socket is required")
	}
	var endpoint string
	payload := map[string]any{}
	switch action {
	case "emergency-stop":
		endpoint = "/admin/v1/control/emergency-stop"
		payload["reason"] = *reason
	case "resume":
		endpoint = "/admin/v1/control/resume"
	case "backup":
		endpoint = "/admin/v1/backup"
		payload["dest"] = *dest
	default:
		return fmt.Errorf("unknown admin action %q", action)
	}
	raw, _ := json.Marshal(payload)
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", *socket)
			},
		},
		Timeout: 30 * time.Second,
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://unix"+endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	fmt.Println(string(body))
	return nil
}
