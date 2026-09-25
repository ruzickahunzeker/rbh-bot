package sequencer

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

const (
	l2BatchKind    = byte(3)
	l2SignedTxKind = byte(4)
	maxBatchDepth  = 16
	maxBatchTxs    = 16_384
)

type wireEnvelope struct {
	Version  *uint64       `json:"version"`
	Messages []wireMessage `json:"messages"`
}

type wireMessage struct {
	SequenceNumber *uint64             `json:"sequenceNumber"`
	Message        *wireMessageWrapper `json:"message"`
	BlockHash      string              `json:"blockHash"`
	BlockMetadata  string              `json:"blockMetadata"`
	SignatureV2    string              `json:"signatureV2"`
}

type wireMessageWrapper struct {
	Message             *wireIncomingMessage `json:"message"`
	DelayedMessagesRead *uint64              `json:"delayedMessagesRead"`
}

type wireIncomingMessage struct {
	Header *wireHeader `json:"header"`
	L2Msg  string      `json:"l2Msg"`
}

type wireHeader struct {
	Kind        *uint8          `json:"kind"`
	Sender      string          `json:"sender"`
	BlockNumber *uint64         `json:"blockNumber"`
	Timestamp   *uint64         `json:"timestamp"`
	RequestID   *string         `json:"requestId"`
	BaseFeeL1   json.RawMessage `json:"baseFeeL1"`
}

type decodedMessage struct {
	sequenceNumber uint64
	l1BlockNumber  uint64
	timestamp      uint64
	blockHash      common.Hash
	transactions   []FeedTransaction
}

// Decode verifies and decodes one Robinhood mainnet feed frame.
func Decode(data []byte) ([]FeedTransaction, error) {
	return DecodeFiltered(data, nil)
}

// DecodeFiltered verifies the feed signature, then applies filter before the
// per-transaction chain ID check and sender recovery. Rejected transactions
// are decoded only far enough to inspect their unsigned destination and data.
func DecodeFiltered(data []byte, filter TransactionFilter) ([]FeedTransaction, error) {
	if int64(len(data)) > defaultMaxMessageBytes {
		return nil, fmt.Errorf("%w: message exceeds %d bytes", ErrMalformedFeed, defaultMaxMessageBytes)
	}
	envelope, err := decodeEnvelope(data)
	if err != nil {
		return nil, err
	}
	verifier := newVerifier(uint64(RobinhoodChainID), []common.Address{mainnetSigner})
	transactions := make([]FeedTransaction, 0)
	for i := range envelope.Messages {
		message, _, err := decodeMessage(envelope.Messages[i], verifier, true, filter)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		transactions = append(transactions, message.transactions...)
	}
	return transactions, nil
}

func decodeEnvelope(data []byte) (wireEnvelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var envelope wireEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return wireEnvelope{}, fmt.Errorf("%w: decode JSON: %v", ErrMalformedFeed, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return wireEnvelope{}, fmt.Errorf("%w: multiple JSON values", ErrMalformedFeed)
		}
		return wireEnvelope{}, fmt.Errorf("%w: trailing JSON: %v", ErrMalformedFeed, err)
	}
	if envelope.Version == nil || *envelope.Version != 1 {
		var version uint64
		if envelope.Version != nil {
			version = *envelope.Version
		}
		return wireEnvelope{}, fmt.Errorf("version %d: %w", version, ErrUnsupportedVersion)
	}
	return envelope, nil
}

func decodeMessage(message wireMessage, verifier feedVerifier, decodeTransactions bool, filter TransactionFilter) (decodedMessage, common.Address, error) {
	if message.SequenceNumber == nil || message.Message == nil || message.Message.Message == nil || message.Message.Message.Header == nil || message.Message.DelayedMessagesRead == nil {
		return decodedMessage{}, common.Address{}, fmt.Errorf("%w: missing required envelope fields", ErrMalformedFeed)
	}
	header := message.Message.Message.Header
	if header.Kind == nil || header.BlockNumber == nil || header.Timestamp == nil || message.Message.Message.L2Msg == "" {
		return decodedMessage{}, common.Address{}, fmt.Errorf("%w: missing required message fields", ErrMalformedFeed)
	}
	blockHash, err := decodeFixedHex(message.BlockHash, common.HashLength, "block hash")
	if err != nil {
		return decodedMessage{}, common.Address{}, err
	}
	signer, err := verifier.verify(message)
	if err != nil {
		return decodedMessage{}, signer, err
	}
	decoded := decodedMessage{
		sequenceNumber: *message.SequenceNumber,
		l1BlockNumber:  *header.BlockNumber,
		timestamp:      *header.Timestamp,
		blockHash:      common.BytesToHash(blockHash),
	}
	if !decodeTransactions {
		return decoded, signer, nil
	}
	l2Message, err := decodeBase64(message.Message.Message.L2Msg, "l2Msg")
	if err != nil {
		return decodedMessage{}, signer, err
	}
	transactions, err := decodeL2Message(l2Message, 0, nil)
	if err != nil {
		return decodedMessage{}, signer, err
	}
	decoded.transactions = make([]FeedTransaction, 0, len(transactions))
	for index, transaction := range transactions {
		if filter != nil && !filter(transaction) {
			continue
		}
		// Nitro may place unsigned/system legacy transactions (chain ID zero)
		// in an otherwise signed sequencer batch. They have no recoverable user
		// sender and cannot be copy-trade opportunities, so skip them without
		// tearing down the authenticated Feed connection.
		if isNonUserChainID(transaction.ChainId()) {
			continue
		}
		if transaction.ChainId().Cmp(new(big.Int).SetUint64(verifier.chainID)) != 0 {
			return decodedMessage{}, signer, fmt.Errorf("transaction %d: got %v, want %d: %w", index, transaction.ChainId(), verifier.chainID, ErrWrongChainID)
		}
		sender, err := transactionSender(transaction, verifier.chainID)
		if err != nil {
			return decodedMessage{}, signer, fmt.Errorf("transaction %d: %w", index, err)
		}
		decoded.transactions = append(decoded.transactions, FeedTransaction{
			SequenceNumber: decoded.sequenceNumber,
			L1BlockNumber:  decoded.l1BlockNumber,
			Timestamp:      decoded.timestamp,
			BlockHash:      decoded.blockHash,
			BatchIndex:     uint64(index),
			Sender:         sender,
			Transaction:    transaction,
		})
	}
	return decoded, signer, nil
}

func isNonUserChainID(chainID *big.Int) bool {
	return chainID == nil || chainID.Sign() == 0
}

func decodeL2Message(payload []byte, depth int, output []*gethtypes.Transaction) ([]*gethtypes.Transaction, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: empty l2 message", ErrMalformedFeed)
	}
	if depth > maxBatchDepth {
		return nil, fmt.Errorf("%w: batch nesting exceeds %d", ErrMalformedFeed, maxBatchDepth)
	}
	switch payload[0] {
	case l2SignedTxKind:
		var transaction gethtypes.Transaction
		if err := transaction.UnmarshalBinary(payload[1:]); err != nil {
			return nil, fmt.Errorf("%w: decode transaction: %v", ErrMalformedFeed, err)
		}
		if len(output) >= maxBatchTxs {
			return nil, fmt.Errorf("%w: transaction count exceeds %d", ErrMalformedFeed, maxBatchTxs)
		}
		return append(output, &transaction), nil
	case l2BatchKind:
		offset := 1
		for offset < len(payload) {
			if len(payload)-offset < 8 {
				return nil, fmt.Errorf("%w: truncated batch length", ErrMalformedFeed)
			}
			length := binary.BigEndian.Uint64(payload[offset : offset+8])
			offset += 8
			if length == 0 {
				return nil, fmt.Errorf("%w: invalid nested message length", ErrMalformedFeed)
			}
			nestedLength, ok := checkedInt(length)
			if !ok || nestedLength > len(payload)-offset {
				return nil, fmt.Errorf("%w: invalid nested message length", ErrMalformedFeed)
			}
			var err error
			output, err = decodeL2Message(payload[offset:offset+nestedLength], depth+1, output)
			if err != nil {
				return nil, err
			}
			offset += nestedLength
		}
		return output, nil
	default:
		return output, nil
	}
}

func checkedInt(value uint64) (int, bool) {
	converted := int(value)                                        // #nosec G115 -- round-trip checked before use.
	return converted, converted >= 0 && uint64(converted) == value // #nosec G115 -- non-negative int always fits uint64.
}

func transactionSender(transaction *gethtypes.Transaction, chainID uint64) (common.Address, error) {
	sender, err := gethtypes.Sender(gethtypes.LatestSignerForChainID(new(big.Int).SetUint64(chainID)), transaction)
	if err != nil {
		return common.Address{}, fmt.Errorf("%w: recover sender: %v", ErrMalformedFeed, err)
	}
	return sender, nil
}

func decodeFixedHex(value string, length int, name string) ([]byte, error) {
	decoded, err := hexutil.Decode(value)
	if err != nil || len(decoded) != length {
		return nil, fmt.Errorf("%w: invalid %s", ErrMalformedFeed, name)
	}
	return decoded, nil
}

func decodeBase64(value, name string) ([]byte, error) {
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid %s", ErrMalformedFeed, name)
	}
	return decoded, nil
}
