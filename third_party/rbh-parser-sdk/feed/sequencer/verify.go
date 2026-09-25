package sequencer

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var feedSignaturePrefix = []byte("Arbitrum Nitro Feed:")

type feedVerifier struct {
	chainID uint64
	signers map[common.Address]struct{}
}

func newVerifier(chainID uint64, signers []common.Address) feedVerifier {
	allowed := make(map[common.Address]struct{}, len(signers))
	for _, signer := range signers {
		if signer != (common.Address{}) {
			allowed[signer] = struct{}{}
		}
	}
	return feedVerifier{chainID: chainID, signers: allowed}
}

func (v feedVerifier) verify(message wireMessage) (common.Address, error) {
	signature, err := base64.StdEncoding.Strict().DecodeString(message.SignatureV2)
	if err != nil || len(signature) != crypto.SignatureLength {
		return common.Address{}, fmt.Errorf("%w: malformed signature", ErrInvalidSignature)
	}
	switch {
	case signature[64] >= 27 && signature[64] <= 28:
		signature[64] -= 27
	case signature[64] > 1:
		return common.Address{}, fmt.Errorf("%w: invalid recovery id", ErrInvalidSignature)
	}
	payload, err := signaturePayload(message, v.chainID)
	if err != nil {
		return common.Address{}, err
	}
	publicKey, err := crypto.SigToPub(crypto.Keccak256(payload), signature)
	if err != nil {
		return common.Address{}, fmt.Errorf("%w: recover signer: %v", ErrInvalidSignature, err)
	}
	signer := crypto.PubkeyToAddress(*publicKey)
	if _, allowed := v.signers[signer]; !allowed {
		return signer, fmt.Errorf("%w: signer %s is not allowed", ErrInvalidSignature, signer)
	}
	return signer, nil
}

func signaturePayload(message wireMessage, chainID uint64) ([]byte, error) {
	if message.SequenceNumber == nil || message.Message == nil || message.Message.Message == nil || message.Message.Message.Header == nil || message.Message.DelayedMessagesRead == nil {
		return nil, fmt.Errorf("%w: missing signed fields", ErrMalformedFeed)
	}
	header := message.Message.Message.Header
	if header.Kind == nil || header.BlockNumber == nil || header.Timestamp == nil {
		return nil, fmt.Errorf("%w: missing signed header fields", ErrMalformedFeed)
	}
	blockHash, err := decodeFixedHex(message.BlockHash, common.HashLength, "block hash")
	if err != nil {
		return nil, err
	}
	sender, err := decodeFixedHex(header.Sender, common.AddressLength, "l1 sender")
	if err != nil {
		return nil, err
	}
	l2Message, err := decodeBase64(message.Message.Message.L2Msg, "l2Msg")
	if err != nil {
		return nil, err
	}
	var metadata []byte
	if message.BlockMetadata != "" {
		metadata, err = decodeBase64(message.BlockMetadata, "block metadata")
		if err != nil {
			return nil, err
		}
	}
	buffer := bytes.NewBuffer(make([]byte, 0, len(l2Message)+160))
	buffer.Write(feedSignaturePrefix)
	writeUint64(buffer, chainID)
	writeUint64(buffer, *message.SequenceNumber)
	buffer.Write(blockHash)
	buffer.Write(metadata)
	writeUint64(buffer, *message.Message.DelayedMessagesRead)
	buffer.WriteByte(*header.Kind)
	buffer.Write(sender)
	writeUint64(buffer, *header.BlockNumber)
	writeUint64(buffer, *header.Timestamp)
	if header.RequestID != nil {
		requestID, err := decodeFixedHex(*header.RequestID, common.HashLength, "request id")
		if err != nil {
			return nil, err
		}
		buffer.Write(requestID)
	}
	baseFee, present, err := parseOptionalBig(header.BaseFeeL1)
	if err != nil {
		return nil, err
	}
	if present {
		buffer.Write(baseFee.Bytes())
	}
	buffer.Write(l2Message)
	return buffer.Bytes(), nil
}

func parseOptionalBig(raw json.RawMessage) (*big.Int, bool, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return nil, false, nil
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		var decoded string
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, false, fmt.Errorf("%w: invalid baseFeeL1", ErrMalformedFeed)
		}
		value = decoded
	}
	base := 10
	if strings.HasPrefix(value, "0x") {
		base = 16
		value = strings.TrimPrefix(value, "0x")
	}
	fee, ok := new(big.Int).SetString(value, base)
	if !ok || fee.Sign() < 0 {
		return nil, false, fmt.Errorf("%w: invalid baseFeeL1", ErrMalformedFeed)
	}
	return fee, true, nil
}

func writeUint64(buffer *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	buffer.Write(encoded[:])
}
