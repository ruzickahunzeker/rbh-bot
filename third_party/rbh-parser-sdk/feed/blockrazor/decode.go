package blockrazor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

type wireEnvelope struct {
	Version  uint64        `json:"version"`
	Messages []wireMessage `json:"messages"`
}

type wireMessage struct {
	SequenceNumber *uint64 `json:"sequenceNumber"`
	Message        struct {
		Message struct {
			Header struct {
				BlockNumber uint64 `json:"blockNumber"`
				Timestamp   uint64 `json:"timestamp"`
			} `json:"header"`
			L2Msg struct {
				Items []wireItem `json:"items"`
			} `json:"l2Msg"`
		} `json:"message"`
	} `json:"message"`
	BlockHash string `json:"blockHash"`
}

type wireItem struct {
	Index       uint64           `json:"index"`
	MessageKind uint64           `json:"messageKind"`
	Transaction *wireTransaction `json:"transaction"`
}

type wireTransaction struct {
	Hash           string `json:"hash"`
	From           string `json:"from"`
	RawTransaction string `json:"rawTransaction"`
}

type decodedFrame struct {
	messages []decodedMessage
}

type decodedMessage struct {
	sequenceNumber uint64
	blockHash      common.Hash
	transactions   []FeedTransaction
}

func Decode(data []byte) ([]FeedTransaction, error) {
	return DecodeFiltered(data, nil)
}

// DecodeFiltered applies filter before chain/signature recovery so callers can
// discard unrelated traffic at the earliest safe transaction boundary.
func DecodeFiltered(data []byte, filter TransactionFilter) ([]FeedTransaction, error) {
	if int64(len(data)) > defaultMaxMessageBytes {
		return nil, fmt.Errorf("%w: message exceeds %d bytes", ErrMalformedFeed, defaultMaxMessageBytes)
	}
	frame, err := decodeFrame(data, filter)
	if err != nil {
		return nil, err
	}
	count := 0
	for _, message := range frame.messages {
		count += len(message.transactions)
	}
	transactions := make([]FeedTransaction, 0, count)
	for _, message := range frame.messages {
		transactions = append(transactions, message.transactions...)
	}
	return transactions, nil
}

func decodeFrame(data []byte, filter TransactionFilter) (decodedFrame, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var envelope wireEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return decodedFrame{}, fmt.Errorf("%w: decode JSON: %v", ErrMalformedFeed, err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return decodedFrame{}, err
	}
	if envelope.Version != 1 {
		return decodedFrame{}, fmt.Errorf("version %d: %w", envelope.Version, ErrUnsupportedVersion)
	}

	frame := decodedFrame{messages: make([]decodedMessage, 0, len(envelope.Messages))}
	for _, message := range envelope.Messages {
		if message.SequenceNumber == nil {
			return decodedFrame{}, fmt.Errorf("%w: missing sequence number", ErrMalformedFeed)
		}
		blockHash, err := decodeRequiredHash(message.BlockHash, "block hash")
		if err != nil {
			return decodedFrame{}, err
		}
		decoded := decodedMessage{sequenceNumber: *message.SequenceNumber, blockHash: blockHash}
		for _, item := range message.Message.Message.L2Msg.Items {
			if item.MessageKind != 4 {
				continue
			}
			if item.Transaction == nil {
				return decodedFrame{}, fmt.Errorf("%w: signed transaction item %d has no transaction", ErrMalformedFeed, item.Index)
			}
			tx, sender, include, err := decodeTransaction(*item.Transaction, filter)
			if err != nil {
				return decodedFrame{}, fmt.Errorf("item %d: %w", item.Index, err)
			}
			if !include {
				continue
			}
			decoded.transactions = append(decoded.transactions, FeedTransaction{
				SequenceNumber: *message.SequenceNumber,
				BlockNumber:    message.Message.Message.Header.BlockNumber,
				Timestamp:      message.Message.Message.Header.Timestamp,
				BlockHash:      blockHash,
				BatchIndex:     item.Index,
				Sender:         sender,
				Transaction:    tx,
			})
		}
		frame.messages = append(frame.messages, decoded)
	}
	return frame, nil
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: multiple JSON values", ErrMalformedFeed)
		}
		return fmt.Errorf("%w: trailing JSON: %v", ErrMalformedFeed, err)
	}
	return nil
}

func decodeOptionalHash(value string) (common.Hash, error) {
	if value == "" {
		return common.Hash{}, nil
	}
	decoded, err := hexutil.Decode(value)
	if err != nil || len(decoded) != common.HashLength {
		return common.Hash{}, fmt.Errorf("%w: invalid block hash", ErrMalformedFeed)
	}
	return common.BytesToHash(decoded), nil
}

func decodeRequiredHash(value, name string) (common.Hash, error) {
	hash, err := decodeOptionalHash(value)
	if err != nil {
		return common.Hash{}, err
	}
	if hash == (common.Hash{}) {
		return common.Hash{}, fmt.Errorf("%w: missing or zero %s", ErrMalformedFeed, name)
	}
	return hash, nil
}

func decodeTransaction(wire wireTransaction, filter TransactionFilter) (*gethtypes.Transaction, common.Address, bool, error) {
	raw, err := hexutil.Decode(wire.RawTransaction)
	if err != nil || len(raw) == 0 {
		return nil, common.Address{}, false, fmt.Errorf("%w: invalid raw transaction", ErrMalformedFeed)
	}
	var tx gethtypes.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return nil, common.Address{}, false, fmt.Errorf("%w: decode raw transaction: %v", ErrMalformedFeed, err)
	}
	if filter != nil && !filter(&tx) {
		return nil, common.Address{}, false, nil
	}
	chainID := tx.ChainId()
	if chainID == nil || chainID.Cmp(big.NewInt(RobinhoodChainID)) != 0 {
		return nil, common.Address{}, false, fmt.Errorf("got %v, want %d: %w", chainID, RobinhoodChainID, ErrWrongChainID)
	}
	sender, err := gethtypes.Sender(gethtypes.LatestSignerForChainID(chainID), &tx)
	if err != nil {
		return nil, common.Address{}, false, fmt.Errorf("%w: recover sender: %v", ErrMalformedFeed, err)
	}
	if !common.IsHexAddress(wire.From) || common.HexToAddress(wire.From) != sender {
		return nil, common.Address{}, false, fmt.Errorf("%w: sender does not match signature", ErrMalformedFeed)
	}
	hash, err := decodeOptionalHash(wire.Hash)
	if err != nil || hash == (common.Hash{}) || hash != tx.Hash() {
		return nil, common.Address{}, false, fmt.Errorf("%w: transaction hash mismatch", ErrMalformedFeed)
	}
	return &tx, sender, true, nil
}
