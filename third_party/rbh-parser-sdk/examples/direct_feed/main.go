package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/0xfnzero/rbh-parser-sdk/feed/sequencer"
	sdk "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

const defaultRPCURL = "https://rpc.mainnet.chain.robinhood.com"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	parser := sdk.New()
	// Restore the finalized pool/curve registry snapshot before starting the feed.
	if err := bootstrapReceipts(ctx, parser, os.Getenv("BOOTSTRAP_TXS")); err != nil {
		log.Fatal(err)
	}
	watched, err := parseWatchedSenders(os.Getenv("WATCH_SENDERS"))
	if err != nil {
		log.Fatal(err)
	}
	initialSequence, err := parseInitialSequence(ctx, os.Getenv("START_SEQUENCE"))
	if err != nil {
		log.Fatal(err)
	}
	filter := sdk.IntentFilter{
		IncludeProtocols: sdk.Protocols(
			sdk.ProtocolPonsV2,
			sdk.ProtocolLong,
			sdk.ProtocolO1,
			sdk.ProtocolPoolsTrade,
			sdk.ProtocolPAIR,
			sdk.ProtocolBagsV2,
		),
		IncludeKinds: sdk.IntentKinds(sdk.IntentBuy, sdk.IntentLaunch, sdk.IntentLaunchAndBuy),
	}
	feed, err := sequencer.New(sequencer.Config{
		InitialSequence: initialSequence,
		Filter: func(transaction *gethtypes.Transaction) bool {
			return parser.IsPotentialIntentWithFilter(transaction, filter)
		},
		OnGap: func(_ context.Context, gap sequencer.SequenceGap) error {
			log.Printf("feed gap: expected=%d received=%d", gap.Expected, gap.Received)
			return nil
		},
		OnReorg: func(_ context.Context, reorg sequencer.Reorg) error {
			log.Printf("feed reorg: sequence=%d old=%s new=%s", reorg.SequenceNumber, reorg.PreviousHash, reorg.ReplacementHash)
			return nil
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	err = feed.Run(ctx, func(_ context.Context, received sequencer.FeedTransaction) error {
		_, parseErr := parser.VisitTransactionIntentsFiltered(received.Transaction, received.Sender, filter, func(intent sdk.TransactionIntent) error {
			category, report := classify(intent, watched)
			if !report {
				return nil
			}
			fmt.Printf("category=%s sequence=%d hash=%s protocol=%s kind=%s token=%s pool=%s sender=%s amount_in=%s\n",
				category, received.SequenceNumber, intent.TransactionHash, intent.Protocol, intent.Kind,
				intent.Token, intent.PoolID, intent.Sender, formatAmount(intent.AmountIn))
			return nil
		})
		if parseErr != nil {
			log.Printf("skip malformed target transaction %s: %v", received.Transaction.Hash(), parseErr)
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func parseInitialSequence(_ context.Context, value string) (*uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if value == "latest" {
		return nil, nil
	}
	sequence, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid START_SEQUENCE %q: %w", value, err)
	}
	return &sequence, nil
}

func parseWatchedSenders(value string) (sdk.AddressSet, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	addresses := make([]common.Address, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !common.IsHexAddress(part) {
			return nil, fmt.Errorf("invalid WATCH_SENDERS address %q", part)
		}
		addresses = append(addresses, common.HexToAddress(part))
	}
	return sdk.Addresses(addresses...), nil
}

func classify(intent sdk.TransactionIntent, watched sdk.AddressSet) (string, bool) {
	_, isWatched := watched[intent.Sender]
	switch intent.Kind {
	case sdk.IntentLaunch:
		return "new-pool-intent", true
	case sdk.IntentLaunchAndBuy:
		if len(watched) > 0 && isWatched {
			return "watched-new-pool-and-buy", true
		}
		return "new-pool-and-buy-intent", true
	case sdk.IntentBuy:
		if len(watched) == 0 {
			return "buy-intent", true
		}
		return "watched-buy-intent", isWatched
	default:
		return "", false
	}
}

func formatAmount(value *big.Int) string {
	if value == nil {
		return "unknown"
	}
	return value.String()
}

func bootstrapReceipts(ctx context.Context, parser *sdk.Parser, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	rpcURL := strings.TrimSpace(os.Getenv("ROBINHOOD_RPC_URL"))
	if rpcURL == "" {
		rpcURL = defaultRPCURL
	}
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return fmt.Errorf("connect bootstrap RPC: %w", err)
	}
	defer client.Close()

	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if len(part) != 66 || !strings.HasPrefix(part, "0x") {
			return fmt.Errorf("invalid BOOTSTRAP_TXS hash %q", part)
		}
		hash := common.HexToHash(part)
		receipt, err := client.TransactionReceipt(ctx, hash)
		if err != nil {
			return fmt.Errorf("bootstrap receipt %s: %w", hash, err)
		}
		events, err := parser.ParseReceipt(receipt)
		if err != nil {
			return fmt.Errorf("parse bootstrap receipt %s: %w", hash, err)
		}
		log.Printf("bootstrapped transaction=%s events=%d", hash, len(events)) // #nosec G706 -- hash is a fixed-size encoded value.
	}
	return nil
}
