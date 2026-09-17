// Command find-pons-reverts performs a bounded, read-only block scan for failed Pons transactions.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

const fixtureCurve = "0x4197749b1b3bbb9e1d8a183f968cc54c42e882a7"

type rpcTransaction struct {
	Hash  common.Hash     `json:"hash"`
	To    *common.Address `json:"to"`
	Input hexutil.Bytes   `json:"input"`
}

type rpcBlock struct {
	Number       hexutil.Uint64   `json:"number"`
	Transactions []rpcTransaction `json:"transactions"`
}

type rpcReceipt struct {
	Status hexutil.Uint64 `json:"status"`
}

type candidate struct {
	blockNumber uint64
	tx          rpcTransaction
}

type failedMatch struct {
	BlockNumber      uint64 `json:"block_number"`
	TransactionHash  string `json:"transaction_hash"`
	Target           string `json:"target"`
	CalldataSelector string `json:"calldata_selector,omitempty"`
	ReceiptStatus    uint64 `json:"receipt_status"`
}

type report struct {
	SchemaVersion          int           `json:"schema_version"`
	Status                 string        `json:"status"`
	ChainID                uint64        `json:"chain_id"`
	FromBlock              uint64        `json:"from_block"`
	ToBlock                uint64        `json:"to_block"`
	LastBlockAttempted     uint64        `json:"last_block_attempted"`
	BlocksRequested        uint64        `json:"blocks_requested"`
	BlocksScanned          uint64        `json:"blocks_scanned"`
	BlockReadFailures      uint64        `json:"block_read_failures"`
	PonsTargetTransactions uint64        `json:"pons_target_transactions"`
	ReceiptReadFailures    uint64        `json:"receipt_read_failures"`
	FailedReceipts         uint64        `json:"failed_receipts"`
	Targets                []string      `json:"targets"`
	Matches                []failedMatch `json:"matches"`
	StartedAt              time.Time     `json:"started_at"`
	CompletedAt            time.Time     `json:"completed_at"`
	SignedOrBroadcast      bool          `json:"signed_or_broadcast"`
}

func main() {
	from := flag.Uint64("from-block", 0, "inclusive start block")
	to := flag.Uint64("to-block", 0, "inclusive end block")
	batchSize := flag.Uint64("batch-size", 10, "JSON-RPC calls per batch")
	interval := flag.Duration("request-interval", 250*time.Millisecond, "minimum interval between RPC batches")
	additionalTargets := flag.String("targets", fixtureCurve, "comma-separated additional target addresses")
	output := flag.String("output", "rbh-validation-package/runs/pons-revert-search.json", "output file")
	flag.Parse()
	if *from == 0 || *to < *from || *batchSize < 1 || *batchSize > 10 || *interval < 0 {
		fatal(errors.New("invalid scan parameters"))
	}
	rpcURL := os.Getenv("ROBINHOOD_RPC_URL")
	if rpcURL == "" {
		fatal(errors.New("ROBINHOOD_RPC_URL is required"))
	}
	addresses := parser.DefaultAddressBook()
	targetSet := map[common.Address]struct{}{addresses.PonsFactory: {}, addresses.PonsLaunchAndBuy: {}}
	for _, raw := range strings.Split(*additionalTargets, ",") {
		raw = strings.TrimSpace(raw)
		if !common.IsHexAddress(raw) {
			fatal(fmt.Errorf("invalid target %q", raw))
		}
		targetSet[common.HexToAddress(raw)] = struct{}{}
	}
	targetNames := make([]string, 0, len(targetSet))
	for address := range targetSet {
		targetNames = append(targetNames, strings.ToLower(address.Hex()))
	}
	sort.Strings(targetNames)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, err := rpc.DialContext(ctx, rpcURL)
	if err != nil {
		fatal(err)
	}
	defer client.Close()
	var chainID hexutil.Uint64
	if err := client.CallContext(ctx, &chainID, "eth_chainId"); err != nil || uint64(chainID) != 4663 {
		fatal(fmt.Errorf("wrong chain: %d: %w", chainID, err))
	}
	result := report{SchemaVersion: 1, Status: "HISTORICAL_NEGATIVE_NOT_OBSERVED", ChainID: 4663, FromBlock: *from, ToBlock: *to, BlocksRequested: *to - *from + 1, Targets: targetNames, StartedAt: time.Now().UTC(), Matches: []failedMatch{}}
	batchNumber := uint64(0)
	for start := *from; start <= *to; {
		end := min(start+*batchSize-1, *to)
		blocks, failures := readBlocks(ctx, client, start, end)
		result.BlockReadFailures += failures
		result.BlocksScanned += uint64(len(blocks))
		candidates := make([]candidate, 0)
		for _, block := range blocks {
			for _, transaction := range block.Transactions {
				if transaction.To == nil {
					continue
				}
				if _, ok := targetSet[*transaction.To]; ok {
					candidates = append(candidates, candidate{blockNumber: uint64(block.Number), tx: transaction})
				}
			}
		}
		result.PonsTargetTransactions += uint64(len(candidates))
		matches, receiptFailures := readReceipts(ctx, client, candidates)
		result.Matches = append(result.Matches, matches...)
		result.ReceiptReadFailures += receiptFailures
		result.LastBlockAttempted = end
		batchNumber++
		checkpoint := result
		checkpoint.Status = "IN_PROGRESS_CHECKPOINT"
		checkpoint.CompletedAt = time.Now().UTC()
		if err := writeReport(*output, checkpoint, false); err != nil {
			fatal(err)
		}
		if batchNumber%100 == 0 {
			_, _ = fmt.Fprintf(os.Stderr, "scanned=%d targeted=%d failed=%d block_errors=%d receipt_errors=%d\n", result.BlocksScanned, result.PonsTargetTransactions, len(result.Matches), result.BlockReadFailures, result.ReceiptReadFailures)
		}
		if end == *to {
			break
		}
		start = end + 1
		if *interval > 0 {
			time.Sleep(*interval)
		}
	}
	sort.Slice(result.Matches, func(i, j int) bool { return result.Matches[i].BlockNumber < result.Matches[j].BlockNumber })
	result.FailedReceipts = uint64(len(result.Matches))
	result.CompletedAt = time.Now().UTC()
	if result.BlockReadFailures != 0 || result.ReceiptReadFailures != 0 {
		result.Status = "INCOMPLETE_RPC_READ_FAILURES"
	} else if result.FailedReceipts != 0 {
		result.Status = "HISTORICAL_NEGATIVE_OBSERVED"
	}
	if err := writeReport(*output, result, true); err != nil {
		fatal(err)
	}
}

func writeReport(path string, result report, print bool) error {
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0o640); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	if print {
		fmt.Println(string(encoded))
	}
	return nil
}

func readBlocks(ctx context.Context, client *rpc.Client, start, end uint64) ([]*rpcBlock, uint64) {
	results := make([]*rpcBlock, end-start+1)
	batch := make([]rpc.BatchElem, len(results))
	for index := range results {
		results[index] = new(rpcBlock)
		batch[index] = rpc.BatchElem{Method: "eth_getBlockByNumber", Args: []any{hexutil.EncodeUint64(start + uint64(index)), true}, Result: results[index]}
	}
	if err := retryBatch(ctx, client, batch); err != nil {
		return nil, uint64(len(batch))
	}
	valid := results[:0]
	var failures uint64
	for index, element := range batch {
		if element.Error != nil || uint64(results[index].Number) == 0 {
			failures++
			continue
		}
		valid = append(valid, results[index])
	}
	return valid, failures
}

func readReceipts(ctx context.Context, client *rpc.Client, candidates []candidate) ([]failedMatch, uint64) {
	if len(candidates) == 0 {
		return nil, 0
	}
	receipts := make([]*rpcReceipt, len(candidates))
	batch := make([]rpc.BatchElem, len(candidates))
	for index := range candidates {
		receipts[index] = new(rpcReceipt)
		batch[index] = rpc.BatchElem{Method: "eth_getTransactionReceipt", Args: []any{candidates[index].tx.Hash}, Result: receipts[index]}
	}
	if err := retryBatch(ctx, client, batch); err != nil {
		return nil, uint64(len(batch))
	}
	matches := make([]failedMatch, 0)
	var failures uint64
	for index, element := range batch {
		if element.Error != nil {
			failures++
			continue
		}
		if uint64(receipts[index].Status) != 0 {
			continue
		}
		transaction := candidates[index].tx
		match := failedMatch{BlockNumber: candidates[index].blockNumber, TransactionHash: transaction.Hash.Hex(), Target: strings.ToLower(transaction.To.Hex()), ReceiptStatus: 0}
		if len(transaction.Input) >= 4 {
			match.CalldataSelector = "0x" + hex.EncodeToString(transaction.Input[:4])
		}
		matches = append(matches, match)
	}
	return matches, failures
}

func retryBatch(ctx context.Context, client *rpc.Client, batch []rpc.BatchElem) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		err = client.BatchCallContext(ctx, batch)
		if err == nil {
			return nil
		}
		time.Sleep(time.Duration(1<<attempt) * time.Second)
	}
	return err
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
