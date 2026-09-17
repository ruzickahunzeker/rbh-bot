// Command replay-fixtures replays captured receipts through the locked parser.
// Its output is observed evidence, not an independently authored golden file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

type capture struct {
	Provenance string          `json:"provenance"`
	ChainID    uint64          `json:"chain_id"`
	TxHash     string          `json:"tx_hash"`
	Receipt    json.RawMessage `json:"receipt"`
}

type item struct {
	path    string
	capture capture
	receipt gethtypes.Receipt
}

type replay struct {
	TxHash           string         `json:"tx_hash"`
	BlockNumber      uint64         `json:"block_number"`
	TransactionIndex uint           `json:"transaction_index"`
	Outcome          string         `json:"outcome"`
	EventCount       int            `json:"event_count"`
	EventKinds       []string       `json:"event_kinds"`
	Protocols        []string       `json:"protocols"`
	V4Direction      string         `json:"v4_direction,omitempty"`
	DirectionBasis   string         `json:"direction_basis,omitempty"`
	Events           []parser.Event `json:"events"`
}

func main() {
	input := flag.String("input", "rbh-validation-package/fixtures/historical/candidates", "candidate directory")
	output := flag.String("output", "rbh-validation-package/runs/parser-replay-observed.json", "output file")
	flag.Parse()
	paths, err := filepath.Glob(filepath.Join(*input, "*.json"))
	if err != nil || len(paths) == 0 {
		fatal(fmt.Errorf("candidate fixtures: %w", err))
	}
	items := make([]item, 0, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			fatal(err)
		}
		var value item
		value.path = path
		if err := json.Unmarshal(raw, &value.capture); err != nil {
			fatal(fmt.Errorf("decode %s: %w", path, err))
		}
		if value.capture.Provenance != "RPC_CAPTURE_NOT_GOLDEN_EXPECTATION" || value.capture.ChainID != 4663 {
			fatal(fmt.Errorf("untrusted capture %s", path))
		}
		if err := json.Unmarshal(value.capture.Receipt, &value.receipt); err != nil {
			fatal(fmt.Errorf("decode receipt %s: %w", path, err))
		}
		items = append(items, value)
	}
	sort.Slice(items, func(i, j int) bool {
		left, right := receiptBlock(&items[i].receipt), receiptBlock(&items[j].receipt)
		if left != right {
			return left < right
		}
		return items[i].receipt.TransactionIndex < items[j].receipt.TransactionIndex
	})
	p := parser.New()
	result := struct {
		Status          string    `json:"status"`
		CreatedAt       time.Time `json:"created_at"`
		ParserModule    string    `json:"parser_module"`
		ParserCommit    string    `json:"parser_commit"`
		IndependentGold bool      `json:"independent_golden"`
		Replays         []replay  `json:"replays"`
	}{
		Status: "PARSER_OBSERVED_REQUIRES_INDEPENDENT_REVIEW", CreatedAt: time.Now().UTC(),
		ParserModule: "github.com/0xfnzero/rbh-parser-sdk", ParserCommit: "72cf9cee394bbd2566fca1d03202b05981eb51b4",
	}
	for i := range items {
		events, err := p.ParseReceipt(&items[i].receipt)
		if err != nil {
			fatal(fmt.Errorf("parse %s: %w", items[i].capture.TxHash, err))
		}
		outcome := "NORMALIZED_EVENTS_OBSERVED"
		if len(events) == 0 {
			outcome = "NO_NORMALIZED_EVENTS"
		}
		entry := replay{TxHash: strings.ToLower(items[i].capture.TxHash), BlockNumber: receiptBlock(&items[i].receipt), TransactionIndex: items[i].receipt.TransactionIndex, Outcome: outcome, EventCount: len(events), Events: events}
		for _, event := range events {
			entry.EventKinds = append(entry.EventKinds, event.Kind.String())
			entry.Protocols = append(entry.Protocols, event.Protocol.String())
			if event.Kind == parser.EventSwap {
				entry.V4Direction, entry.DirectionBasis = classifyV4Swap(event.Data)
			}
		}
		result.Replays = append(result.Replays, entry)
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o750); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*output, append(encoded, '\n'), 0o640); err != nil {
		fatal(err)
	}
}

func classifyV4Swap(data any) (string, string) {
	swap, ok := data.(parser.Swap)
	if !ok || swap.Amount0 == nil || swap.Amount1 == nil {
		return "", ""
	}
	tokenDelta := swap.Amount0
	tokenPosition := "currency0"
	if swap.Registration.Token == swap.Registration.PoolKey.Currency1 {
		tokenDelta = swap.Amount1
		tokenPosition = "currency1"
	} else if swap.Registration.Token != swap.Registration.PoolKey.Currency0 {
		return "unclassified", "registered token is absent from PoolKey"
	}
	basis := fmt.Sprintf("PoolManager pool delta: registered token is %s; negative token delta means token leaves pool (buy), positive means token enters pool (sell)", tokenPosition)
	switch tokenDelta.Sign() {
	case -1:
		return "buy", basis
	case 1:
		return "sell", basis
	default:
		return "unclassified", basis + "; token delta is zero"
	}
}

func receiptBlock(receipt *gethtypes.Receipt) uint64 {
	if receipt == nil || receipt.BlockNumber == nil || !receipt.BlockNumber.IsUint64() {
		return 0
	}
	return receipt.BlockNumber.Uint64()
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
