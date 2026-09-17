package feed

import (
	"errors"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

var ErrNilReceipt = errors.New("nil receipt")
var ErrAuditSinkRequired = errors.New("receipt audit sink is required")

type ReceiptAudit struct {
	TransactionHash common.Hash
	BlockNumber     uint64
	ReceiptStatus   uint64
	Reason          string
}

type ReceiptAuditSink interface {
	PersistReceiptAudit(ReceiptAudit) error
}

// ReceiptResult separates the durable audit fact from events eligible for economic consumers.
// A reverted transaction remains auditable but can never produce an economic observation.
type ReceiptResult struct {
	TransactionHash common.Hash
	BlockNumber     uint64
	ReceiptStatus   uint64
	AuditReason     string
	AuditPersisted  bool
	EconomicEvents  []parser.Event
	CopyEligible    bool
}

// ReceiptGate enforces receipt success before invoking the stateful parser. This ordering is
// security-critical: logs attached to a synthetic or malformed failed receipt must not mutate the
// parser registry and cannot become a later copy-trading signal.
type ReceiptGate struct {
	parser *parser.Parser
	audit  ReceiptAuditSink
}

func NewReceiptGate(value *parser.Parser) *ReceiptGate {
	if value == nil {
		value = parser.New()
	}
	return &ReceiptGate{parser: value}
}

func NewReceiptGateWithAuditSink(value *parser.Parser, audit ReceiptAuditSink) *ReceiptGate {
	gate := NewReceiptGate(value)
	gate.audit = audit
	return gate
}

func (gate *ReceiptGate) Process(receipt *gethtypes.Receipt) (ReceiptResult, error) {
	if receipt == nil {
		return ReceiptResult{}, ErrNilReceipt
	}
	result := ReceiptResult{TransactionHash: receipt.TxHash, ReceiptStatus: receipt.Status}
	if receipt.BlockNumber != nil && receipt.BlockNumber.IsUint64() {
		result.BlockNumber = receipt.BlockNumber.Uint64()
	}
	if receipt.Status != gethtypes.ReceiptStatusSuccessful {
		result.AuditReason = "receipt_reverted"
		result.EconomicEvents = []parser.Event{}
		if err := gate.persistAudit(result); err != nil {
			return ReceiptResult{}, err
		}
		result.AuditPersisted = true
		return result, nil
	}
	result.AuditReason = "receipt_successful"
	if err := gate.persistAudit(result); err != nil {
		return ReceiptResult{}, err
	}
	result.AuditPersisted = true
	events, err := gate.parser.ParseReceipt(receipt)
	if err != nil {
		return ReceiptResult{}, err
	}
	result.EconomicEvents = events
	result.CopyEligible = len(events) != 0
	return result, nil
}

func (gate *ReceiptGate) persistAudit(result ReceiptResult) error {
	if gate.audit == nil {
		return ErrAuditSinkRequired
	}
	return gate.audit.PersistReceiptAudit(ReceiptAudit{TransactionHash: result.TransactionHash, BlockNumber: result.BlockNumber, ReceiptStatus: result.ReceiptStatus, Reason: result.AuditReason})
}
