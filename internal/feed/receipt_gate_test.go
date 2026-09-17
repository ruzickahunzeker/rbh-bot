package feed

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

func TestReceiptGateFailsClosedWithoutAuditSink(t *testing.T) {
	path := filepath.Join("..", "..", "rbh-validation-package", "fixtures", "historical", "candidates", "0x45142a829ca4ce83909c0f87408b27f70da8a1853a101921fffc20ff5fc08b10.json")
	gate := NewReceiptGate(parser.New())
	result, err := gate.Process(loadReceipt(t, path))
	if !errors.Is(err, ErrAuditSinkRequired) {
		t.Fatalf("error=%v, want %v", err, ErrAuditSinkRequired)
	}
	if result.AuditPersisted || len(result.EconomicEvents) != 0 || result.CopyEligible {
		t.Fatalf("receipt escaped without durable audit: %#v", result)
	}
}

type capturedReceipt struct {
	Receipt json.RawMessage `json:"receipt"`
}

type syntheticSpec struct {
	Provenance string `json:"provenance"`
	Expected   struct {
		AuditReason           string `json:"audit_reason"`
		EconomicEvents        int    `json:"normalized_economic_events"`
		CopyEligible          bool   `json:"copy_eligible"`
		ParserRegistryMutated bool   `json:"parser_registry_mutated"`
	} `json:"expected"`
}

type recordingAuditSink struct {
	records []ReceiptAudit
}

func (sink *recordingAuditSink) PersistReceiptAudit(value ReceiptAudit) error {
	sink.records = append(sink.records, value)
	return nil
}

func TestRevertedReceiptCannotEmitOrMutateRegistry(t *testing.T) {
	root := filepath.Join("..", "..", "rbh-validation-package", "fixtures")
	var spec syntheticSpec
	decodeJSON(t, filepath.Join(root, "synthetic", "pons-reverted-receipt.json"), &spec)
	if spec.Provenance != "SYNTHETIC_LOCAL_SAFETY_TEST_NOT_HISTORICAL" {
		t.Fatal("synthetic fixture is not explicitly distinguished from history")
	}
	launch := loadReceipt(t, filepath.Join(root, "historical", "candidates", "0x45142a829ca4ce83909c0f87408b27f70da8a1853a101921fffc20ff5fc08b10.json"))
	launch.Status = gethtypes.ReceiptStatusFailed
	sink := new(recordingAuditSink)
	gate := NewReceiptGateWithAuditSink(parser.New(), sink)
	result, err := gate.Process(launch)
	if err != nil {
		t.Fatal(err)
	}
	if result.AuditReason != spec.Expected.AuditReason || len(result.EconomicEvents) != spec.Expected.EconomicEvents || result.CopyEligible != spec.Expected.CopyEligible {
		t.Fatalf("failed receipt escaped gate: %#v", result)
	}
	if !result.AuditPersisted || len(sink.records) != 1 {
		t.Fatalf("failed receipt audit was not persisted: %#v", result)
	}

	// The failed launch carried real launch logs. If Process had invoked parser, this later curve
	// trade would resolve through the polluted registry and produce a curve-buy event.
	buy := loadReceipt(t, filepath.Join(root, "historical", "candidates", "0x04fc83e1a45cbbc7a0dc6e18dd996f91225c7d97158412560dc0d2fbec5c9d2d.json"))
	after, err := gate.Process(buy)
	if err != nil {
		t.Fatal(err)
	}
	registryMutated := len(after.EconomicEvents) != 0
	if registryMutated != spec.Expected.ParserRegistryMutated {
		t.Fatalf("parser registry mutation=%v, events=%d", registryMutated, len(after.EconomicEvents))
	}
}

func TestSuccessfulReceiptRemainsEligible(t *testing.T) {
	path := filepath.Join("..", "..", "rbh-validation-package", "fixtures", "historical", "candidates", "0x45142a829ca4ce83909c0f87408b27f70da8a1853a101921fffc20ff5fc08b10.json")
	result, err := NewReceiptGateWithAuditSink(parser.New(), new(recordingAuditSink)).Process(loadReceipt(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if result.AuditReason != "receipt_successful" || !result.CopyEligible || len(result.EconomicEvents) != 2 {
		t.Fatalf("successful receipt unexpectedly blocked: %#v", result)
	}
}

func TestHistoricalRevertIsAuditOnlyAndRegistryUnchanged(t *testing.T) {
	root := filepath.Join("..", "..", "rbh-validation-package", "fixtures")
	failedPath := filepath.Join(root, "historical", "candidates", "0xa056fd548d5251ba0ccbbf005932d1ee665eca0dc5fdcb6d2e79451b32954e4e.json")
	sink := new(recordingAuditSink)
	gate := NewReceiptGateWithAuditSink(parser.New(), sink)
	result, err := gate.Process(loadReceipt(t, failedPath))
	if err != nil {
		t.Fatal(err)
	}
	if result.AuditReason != "receipt_reverted" || !result.AuditPersisted || len(result.EconomicEvents) != 0 || result.CopyEligible || len(sink.records) != 1 {
		t.Fatalf("historical revert escaped gate: %#v", result)
	}

	// A trade on the known fixture curve depends on the launch registry context. Feeding it after
	// the failed factory transaction must still produce no attributed event.
	buyPath := filepath.Join(root, "historical", "candidates", "0x04fc83e1a45cbbc7a0dc6e18dd996f91225c7d97158412560dc0d2fbec5c9d2d.json")
	after, err := gate.Process(loadReceipt(t, buyPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(after.EconomicEvents) != 0 || after.CopyEligible {
		t.Fatalf("historical revert polluted parser registry: %#v", after)
	}
}

func loadReceipt(t *testing.T, path string) *gethtypes.Receipt {
	t.Helper()
	var captured capturedReceipt
	decodeJSON(t, path, &captured)
	var receipt gethtypes.Receipt
	if err := json.Unmarshal(captured.Receipt, &receipt); err != nil {
		t.Fatal(err)
	}
	return &receipt
}

func decodeJSON(t *testing.T, path string, target any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatal(err)
	}
}
