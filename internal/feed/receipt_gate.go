package feed

import (
	"errors"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

var ErrNilReceipt = errors.New("nil receipt")

// ReceiptResult separates the durable audit fact from events eligible for economic consumers.
// A reverted transaction remains auditable but can never produce an economic observation.
type ReceiptResult struct {
	TransactionHash common.Hash
	BlockNumber     uint64
	ReceiptStatus   uint64
	AuditReason     string
	EconomicEvents  []parser.Event
	CopyEligible    bool
}

// ReceiptGate enforces receipt success before invoking the stateful parser. This ordering is
// security-critical: logs attached to a synthetic or malformed failed receipt must not mutate the
// parser registry and cannot become a later copy-trading signal.
type ReceiptGate struct {
	parser *parser.Parser
}

func NewReceiptGate(value *parser.Parser) *ReceiptGate {
	if value == nil {
		value = parser.New()
	}
	return &ReceiptGate{parser: value}
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
		return result, nil
	}
	events, err := gate.parser.ParseReceipt(receipt)
	if err != nil {
		return ReceiptResult{}, err
	}
	result.AuditReason = "receipt_successful"
	result.EconomicEvents = events
	result.CopyEligible = len(events) != 0
	return result, nil
}
