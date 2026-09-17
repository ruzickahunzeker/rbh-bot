package feed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

const ChainID uint64 = 4663

type Source string

const (
	SourceSequencer Source = "sequencer"
	SourceRPC       Source = "rpc"
)

var ErrInvalidObservation = errors.New("invalid feed observation")

type Observation struct {
	ChainID          uint64
	Source           Source
	SourceSequence   uint64
	TransactionHash  common.Hash
	StableActionPath string
	PayloadJSON      json.RawMessage
	ObservedAt       time.Time
}

func (o Observation) Validate() error {
	if o.ChainID != ChainID || (o.Source != SourceSequencer && o.Source != SourceRPC) || o.TransactionHash == (common.Hash{}) {
		return ErrInvalidObservation
	}
	if strings.TrimSpace(o.StableActionPath) == "" || len(o.StableActionPath) > 256 || !json.Valid(o.PayloadJSON) {
		return ErrInvalidObservation
	}
	if o.ObservedAt.IsZero() {
		return ErrInvalidObservation
	}
	return nil
}

// ID is observation-level identity. Source is intentionally included so the
// same transaction may be observed through Sequencer and RPC without losing
// provenance; later economic dedupe deliberately uses a different key.
func (o Observation) ID() string {
	h := sha256.New()
	parts := []string{
		strconv.FormatUint(o.ChainID, 10),
		string(o.Source),
		strings.ToLower(o.TransactionHash.Hex()),
		o.StableActionPath,
	}
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

type OutboxItem struct {
	Offset           int64           `json:"offset"`
	ObservationID    string          `json:"observation_id"`
	Source           Source          `json:"source"`
	SourceSequence   uint64          `json:"source_sequence"`
	TransactionHash  common.Hash     `json:"transaction_hash"`
	StableActionPath string          `json:"stable_action_path"`
	PayloadJSON      json.RawMessage `json:"payload"`
}

type Progress struct {
	ObservedSequence   *uint64
	DurableEventOffset int64
	Degraded           bool
	DegradedReason     string
	UpdatedAt          time.Time
}

type CommitResult struct {
	InsertedObservations int
	DurableEventOffset   int64
}
