package sequencer

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

const verifiedFrame = `{"version":1,"messages":[{"blockHash":"0x665fa5e651d0c66da7978cf5a07b751c71589472fa7b13508a36f9edf9ef3911","blockMetadata":null,"message":{"delayedMessagesRead":68519,"message":{"header":{"baseFeeL1":0,"blockNumber":25625061,"kind":3,"requestId":null,"sender":"0xa4b000000000000000000073657175656e636572","timestamp":1785166353},"l2Msg":"AwAAAAAAAABwBPhtggqNhApLnsCCXxiUenZHY9E9F+Pp2tpbA3GygN7xBGqGASjsTTkfgIIkkqAMPhc21kyIBZR0RowXW6Ho9E3rGhTjQiS+EryRYl6gdKBPl78LaXMB2YwV8xUBdkB3ZDFbl6+EXT7JVGb/h+M/cwAAAAAAAAD4BAL49IISN4IOOIRZaC8AhF2t7wCDCSfAlHOZGiXIGL8fESjeqrFJLUVjjeDTgLiE/G94ZQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABmgYAAAAAAAAAAAAAAAAdLJuwnVNy9J+Q6KlJ+OABTWKdy4AAAAAAAAAAAAAAAAAAAAA/////////////////////wAAAAAAAAAAAAAAAAAAAAD/////////////////////wICg2/lv1vQh9OylhGUpTdmx+6hbxyeZcwn5b4PRxKzH6yKgIYXJkdpJTLtQ8tGJwln97FiADyZ56U5WVfyBTyvAn3A="}},"sequenceNumber":20842309,"signatureV2":"/ZnWFeNtaNHG8KCm2ucy2RayWXjwOYWnr+5f0pfesf02BHcSHZxOm7XIZC7rRdE30XneirTSNVhVxgYS3/CAlQA="}]}`

func TestDecodeVerifiedMainnetFrame(t *testing.T) {
	transactions, err := Decode([]byte(verifiedFrame))
	if err != nil {
		t.Fatal(err)
	}
	if len(transactions) != 2 {
		t.Fatalf("got %d transactions, want 2", len(transactions))
	}
	for index, transaction := range transactions {
		if transaction.SequenceNumber != 20_842_309 || transaction.L1BlockNumber != 25_625_061 {
			t.Fatalf("transaction %d has wrong block metadata: %#v", index, transaction)
		}
		if transaction.Transaction == nil || transaction.Transaction.ChainId().Int64() != RobinhoodChainID {
			t.Fatalf("transaction %d has wrong chain", index)
		}
		if transaction.Sender == [20]byte{} {
			t.Fatalf("transaction %d has no recovered sender", index)
		}
	}
}

func TestUnsignedLegacyTransactionIsClassifiedAsSystem(t *testing.T) {
	if !isNonUserChainID(big.NewInt(0)) || !isNonUserChainID(nil) {
		t.Fatal("zero/nil chain ID was not classified as system")
	}
	to := common.HexToAddress("0x1")
	user := gethtypes.NewTx(&gethtypes.DynamicFeeTx{ChainID: big.NewInt(RobinhoodChainID), To: &to})
	if isNonUserChainID(user.ChainId()) {
		t.Fatal("chain-bound user transaction was classified as system")
	}
}

func TestDecodeFilteredSelectsTransactionsBeforeSenderRecovery(t *testing.T) {
	all, err := Decode([]byte(verifiedFrame))
	if err != nil {
		t.Fatal(err)
	}
	wanted := all[0].Transaction.Hash()
	seen := 0
	transactions, err := DecodeFiltered([]byte(verifiedFrame), func(transaction *gethtypes.Transaction) bool {
		seen++
		return transaction.Hash() == wanted
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != len(all) || len(transactions) != 1 || transactions[0].Transaction.Hash() != wanted || transactions[0].Sender == [20]byte{} {
		t.Fatalf("seen=%d transactions=%#v", seen, transactions)
	}
}

func TestDecodeRejectsUntrustedFrame(t *testing.T) {
	corrupted := strings.Replace(verifiedFrame, "/ZnWFe", "AZnWFe", 1)
	_, err := Decode([]byte(corrupted))
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("got %v, want ErrInvalidSignature", err)
	}
}

func TestNewDefaultsToOfficialMainnet(t *testing.T) {
	client, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if client.config.URL != MainnetURL || client.config.ChainID != uint64(RobinhoodChainID) {
		t.Fatalf("unexpected defaults: %#v", client.config)
	}
	if _, ok := client.verifier.signers[mainnetSigner]; !ok {
		t.Fatal("official mainnet signer is not required")
	}
}

func TestDecodeRejectsOversizedDirectInput(t *testing.T) {
	if _, err := Decode(make([]byte, defaultMaxMessageBytes+1)); !errors.Is(err, ErrMalformedFeed) {
		t.Fatalf("error = %v, want ErrMalformedFeed", err)
	}
}
