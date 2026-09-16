package main

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

type expectedFile struct {
	Provenance string          `json:"provenance"`
	TxHash     string          `json:"tx_hash"`
	Events     []expectedEvent `json:"events"`
}

type expectedEvent struct {
	Kind                string `json:"kind"`
	Protocol            string `json:"protocol"`
	SourceLogIndex      uint   `json:"source_log_index"`
	SourceTopic0        string `json:"source_topic0"`
	Token               string `json:"token,omitempty"`
	Quote               string `json:"quote,omitempty"`
	Curve               string `json:"curve,omitempty"`
	Creator             string `json:"creator,omitempty"`
	LaunchConfigID      string `json:"launch_config_id,omitempty"`
	GraduationThreshold string `json:"graduation_threshold,omitempty"`
	BuyerOrSeller       string `json:"buyer_or_seller,omitempty"`
	Recipient           string `json:"recipient,omitempty"`
	AmountIn            string `json:"amount_in,omitempty"`
	AmountOut           string `json:"amount_out,omitempty"`
	Fee                 string `json:"fee,omitempty"`
	Tax                 string `json:"tax,omitempty"`
}

type goldenReplay struct {
	expected expectedFile
	receipt  gethtypes.Receipt
}

func TestLockedParserMatchesIndependentCurveExpectations(t *testing.T) {
	root := filepath.Join("..", "..", "rbh-validation-package", "fixtures", "historical")
	paths, err := filepath.Glob(filepath.Join(root, "expected", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("load expectations: %v", err)
	}
	items := make([]goldenReplay, 0, len(paths))
	for _, path := range paths {
		var expected expectedFile
		decodeFile(t, path, &expected)
		if expected.Provenance != "INDEPENDENT_RAW_LOG_ABI_REVIEW" {
			t.Fatalf("%s has invalid provenance", path)
		}
		var captured capture
		decodeFile(t, filepath.Join(root, "candidates", expected.TxHash+".json"), &captured)
		var receipt gethtypes.Receipt
		if err := json.Unmarshal(captured.Receipt, &receipt); err != nil {
			t.Fatalf("decode receipt %s: %v", expected.TxHash, err)
		}
		items = append(items, goldenReplay{expected: expected, receipt: receipt})
	}
	sort.Slice(items, func(i, j int) bool {
		left, right := receiptBlock(&items[i].receipt), receiptBlock(&items[j].receipt)
		if left != right {
			return left < right
		}
		return items[i].receipt.TransactionIndex < items[j].receipt.TransactionIndex
	})
	p := parser.New()
	for i := range items {
		events, err := p.ParseReceipt(&items[i].receipt)
		if err != nil {
			t.Fatalf("parse %s: %v", items[i].expected.TxHash, err)
		}
		actual := make([]expectedEvent, 0, len(events))
		for _, event := range events {
			actual = append(actual, projectEvent(t, event))
		}
		if !reflect.DeepEqual(actual, items[i].expected.Events) {
			t.Fatalf("%s mismatch\nactual: %#v\nexpected: %#v", items[i].expected.TxHash, actual, items[i].expected.Events)
		}
	}
}

func decodeFile(t *testing.T, path string, target any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func projectEvent(t *testing.T, event parser.Event) expectedEvent {
	t.Helper()
	result := expectedEvent{Kind: event.Kind.String(), Protocol: event.Protocol.String(), SourceLogIndex: event.Log.Index}
	if len(event.Log.Topics) == 0 {
		t.Fatal("normalized event has no source topic")
	}
	result.SourceTopic0 = event.Log.Topics[0].Hex()
	switch data := event.Data.(type) {
	case parser.Launch:
		result.Token, result.Quote, result.Curve, result.Creator = data.Token.Hex(), data.Quote.Hex(), data.Curve.Hex(), data.Creator.Hex()
		result.LaunchConfigID = decimal(data.LaunchConfigID)
		result.GraduationThreshold = decimal(data.GraduationThreshold)
	case parser.CurveTrade:
		result.BuyerOrSeller, result.Recipient = data.BuyerOrSeller.Hex(), data.Recipient.Hex()
		result.AmountIn, result.AmountOut = decimal(data.AmountIn), decimal(data.AmountOut)
		result.Fee, result.Tax = decimal(data.Fee), decimal(data.Tax)
	default:
		t.Fatalf("unsupported golden event data %T", event.Data)
	}
	return lowerAddresses(result)
}

func decimal(value *big.Int) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func lowerAddresses(value expectedEvent) expectedEvent {
	for _, target := range []*string{
		&value.Token, &value.Quote, &value.Curve, &value.Creator, &value.BuyerOrSeller, &value.Recipient,
	} {
		if *target != "" {
			*target = strings.ToLower(*target)
		}
	}
	return value
}
