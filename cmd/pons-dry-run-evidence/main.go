package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/bot"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	tradecore "github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

const defaultRPC = "https://rpc.mainnet.chain.robinhood.com"

type fixedBackend struct {
	*tradecore.RPCBackend
	block tradecore.BlockRef
}

func (b fixedBackend) Snapshot(context.Context) (tradecore.BlockRef, error) { return b.block, nil }

type evidence struct {
	SchemaVersion int                      `json:"schema_version"`
	Status        string                   `json:"status"`
	Scope         string                   `json:"scope"`
	ChainID       uint64                   `json:"chain_id"`
	RPCSource     string                   `json:"rpc_source"`
	SDKVersion    string                   `json:"trade_sdk_version"`
	CapturedAt    time.Time                `json:"captured_at"`
	Buy           tradecore.DryRunResult   `json:"curve_buy"`
	Sell          tradecore.DryRunResult   `json:"curve_sell"`
	Revert        tradecore.DryRunResult   `json:"revert_simulation"`
	Duplicate     tradecore.DryRunResult   `json:"duplicate_replay"`
	Recovery      []tradecore.DryRunResult `json:"restart_recovery"`
	Counts        tradecore.SafetyCounts   `json:"durable_counts"`
	Safety        map[string]bool          `json:"safety_assertions"`
}

func main() {
	rpcURL := flag.String("rpc", env("ROBINHOOD_RPC_URL", defaultRPC), "Robinhood Chain RPC URL")
	output := flag.String("output", "", "optional evidence JSON path")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	backend, err := tradecore.DialRPCBackend(ctx, *rpcURL)
	fatalIf(err)
	defer backend.Close()

	temporary, err := os.MkdirTemp("", "rbh-c06-")
	fatalIf(err)
	defer os.RemoveAll(temporary)
	database, err := storage.Open(ctx, storage.TradeOwner, filepath.Join(temporary, "trade.db"))
	fatalIf(err)
	defer database.Close()
	fatalIf(database.Migrate(ctx))
	store, err := tradecore.NewStore(database)
	fatalIf(err)

	// The snapshot immediately precedes a direct canonical Pons v2 Curve sell
	// in transaction
	// 0xf76f612eba92e93275b3006eeda1038f68f045689609933160515836ec59af38.
	token := common.HexToAddress("0xe278bb96d14482f161148aeed8ba3fa482fc78d9")
	buyWallet := common.HexToAddress("0x727d2ec9c2517f02508dce569f795012bf5974d0")
	emptyWallet := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
	fatalIf(store.RegisterDryRunWallet(ctx, "buy-wallet", buyWallet))
	fatalIf(store.RegisterDryRunWallet(ctx, "empty-wallet", emptyWallet))

	block, err := backend.SnapshotAt(ctx, new(big.Int).SetUint64(0x3e871ef))
	fatalIf(err)
	engine, _ := tradecore.NewEngine(store, fixedBackend{RPCBackend: backend, block: block})

	buyRequest := request("c06-curve-buy", "buy-wallet", "copy_buy", "fixed_input", "1000000000000", token)
	sellRequest := request("c06-curve-sell", "buy-wallet", "copy_sell", "balance_bps", "10000", token)
	revertRequest := request("c06-revert", "empty-wallet", "copy_buy", "fixed_input", "1000000000000000000000000", token)
	buy, err := engine.DryRun(ctx, buyRequest)
	fatalIf(err)
	sell, err := engine.DryRun(ctx, sellRequest)
	fatalIf(err)
	reverted, err := engine.DryRun(ctx, revertRequest)
	fatalIf(err)
	duplicate, err := engine.DryRun(ctx, buyRequest)
	fatalIf(err)

	recoveryRequest := request("c06-restart-recovery", "buy-wallet", "copy_buy", "fixed_input", "1000000000000", token)
	_, err = store.Admit(ctx, recoveryRequest, time.Now().UTC())
	fatalIf(err)
	restarted, _ := tradecore.NewEngine(store, fixedBackend{RPCBackend: backend, block: block})
	recovery, err := restarted.Recover(ctx)
	fatalIf(err)
	counts, err := store.Counts(ctx)
	fatalIf(err)

	report := evidence{
		SchemaVersion: 1, Scope: "PONS_V2_CURVE_DRY_RUN_ONLY", ChainID: tradecore.ChainID,
		RPCSource: "official Robinhood Chain mainnet RPC (URL redacted)", SDKVersion: "github.com/0xfnzero/rbh-trade-sdk v0.4.0",
		CapturedAt: time.Now().UTC(), Buy: buy, Sell: sell, Revert: reverted, Duplicate: duplicate, Recovery: recovery, Counts: counts,
		Safety: map[string]bool{
			"eth_call_only": true, "nonce_allocated": false, "signed_transaction_created": false,
			"eth_sendRawTransaction_called": false, "broadcast_called": false,
			"transaction_attempts_zero": counts.TransactionAttempts == 0,
		},
	}
	if buy.Status == "success" && sell.Status == "success" && reverted.Status == "fail_closed" && reverted.FailureCode == "SIMULATION_REVERTED" && duplicate.Duplicate && len(recovery) == 1 && recovery[0].Status == "success" && counts.TransactionAttempts == 0 {
		report.Status = "C06_PONS_CURVE_PASS"
	} else {
		report.Status = "C06_PONS_CURVE_FAIL"
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	fatalIf(err)
	encoded = append(encoded, '\n')
	if *output == "" {
		_, err = os.Stdout.Write(encoded)
	} else {
		err = os.WriteFile(*output, encoded, 0o644)
	}
	fatalIf(err)
	if report.Status != "C06_PONS_CURVE_PASS" {
		fatalIf(errors.New("C06 evidence did not pass"))
	}
}

func request(id, wallet, kind, mode, amount string, token common.Address) tradecore.DryRunRequest {
	return tradecore.DryRunRequest{WalletID: wallet, Intent: bot.OperationIntent{
		ID: id, IdempotencyKey: id, StrategyID: "c06-evidence", WatchedWalletID: "historical-source",
		SourceEventID: id, SourceObservationID: id, SourceTxHash: common.HexToHash("0x1").Hex(),
		Kind: kind, Token: token.Hex(), AmountMode: mode, AmountValue: amount, PolicyVersion: 1, Status: "created",
	}}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
