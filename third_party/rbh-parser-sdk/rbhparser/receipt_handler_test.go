package rbhparser

import (
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

func TestParseReceiptLogErrorHandlerIsolatesAcceptedMalformedLog(t *testing.T) {
	parser := New()
	addresses := parser.Addresses()
	implementation := common.HexToAddress("0x1234")
	malformed := &gethtypes.Log{
		Address: addresses.GMGNRouter,
		Topics:  []common.Hash{TopicGMGNSwap, common.Hash{}, common.Hash{}, common.Hash{}},
		Index:   3,
	}
	upgrade := &gethtypes.Log{
		Address: addresses.GMGNRouter,
		Topics:  []common.Hash{TopicProtocolUpgraded, addressTopic(implementation)},
		Index:   4,
	}
	accepted := 0
	events, err := parser.ParseReceiptWithLogErrorHandler(&gethtypes.Receipt{Logs: []*gethtypes.Log{malformed, upgrade}}, func(log gethtypes.Log, parseErr error) bool {
		if log.Address != addresses.GMGNRouter || len(log.Topics) == 0 || log.Topics[0] != TopicGMGNSwap || !errors.Is(parseErr, ErrMalformedLog) {
			return false
		}
		accepted++
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted != 1 || len(events) != 1 || events[0].Kind != EventProtocolUpgrade {
		t.Fatalf("accepted=%d events=%#v", accepted, events)
	}
}

func TestParseReceiptLogErrorHandlerCannotHideUnacceptedMalformedLog(t *testing.T) {
	parser := New()
	addresses := parser.Addresses()
	malformed := &gethtypes.Log{
		Address: addresses.PoolManager,
		Topics:  []common.Hash{TopicPoolInitialize},
		Index:   7,
	}
	_, err := parser.ParseReceiptWithLogErrorHandler(&gethtypes.Receipt{Logs: []*gethtypes.Log{malformed}}, func(gethtypes.Log, error) bool {
		return false
	})
	if !errors.Is(err, ErrMalformedLog) {
		t.Fatalf("error=%v, want ErrMalformedLog", err)
	}
}
