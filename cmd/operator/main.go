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

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "db":
		if len(os.Args) < 3 || os.Args[2] != "bind-chain" {
			usage()
			os.Exit(2)
		}
		if err := runBindChain(os.Args[3:]); err != nil {
			fmt.Fprintf(os.Stderr, "operator db bind-chain: %v\n", err)
			os.Exit(1)
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
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  operator db bind-chain --db <path> --owner <trade-service|feed-service|bot-service> --expected-chain-id <id> --chain-slug <slug> [--network-kind mainnet]
  operator admin emergency-stop --socket <admin.sock> [--reason <text>]
  operator admin resume --socket <admin.sock>
  operator admin backup --socket <admin.sock> --dest <absolute-path>`)
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
