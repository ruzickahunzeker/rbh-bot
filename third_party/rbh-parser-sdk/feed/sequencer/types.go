// Package sequencer consumes Robinhood Chain's official Nitro sequencer feed.
package sequencer

import (
	"context"
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

const (
	RobinhoodChainID     int64 = 4663
	MainnetURL                 = "wss://feed.mainnet.chain.robinhood.com"
	MainnetSignerAddress       = "0xDaa526086787d9DEbE1D7F3FFdb1fE50cf8687F4"
)

var (
	mainnetSigner         = common.HexToAddress(MainnetSignerAddress)
	ErrConnection         = errors.New("sequencer feed connection failed")
	ErrInvalidConfig      = errors.New("invalid sequencer feed configuration")
	ErrInvalidSignature   = errors.New("invalid sequencer feed signature")
	ErrMalformedFeed      = errors.New("malformed sequencer feed message")
	ErrUnsupportedVersion = errors.New("unsupported sequencer feed version")
	ErrWrongChainID       = errors.New("unexpected transaction chain id")
	ErrSequenceGap        = errors.New("sequencer feed sequence gap")
	ErrReorg              = errors.New("sequencer feed reorg")
)

type SequenceGap struct {
	Expected uint64
	Received uint64
}

type Reorg struct {
	SequenceNumber  uint64
	PreviousHash    common.Hash
	ReplacementHash common.Hash
}

type VerificationFailure struct {
	SequenceNumber  uint64
	RecoveredSigner common.Address
	Cause           error
}

type ConnectionState uint8

const (
	ConnectionConnecting ConnectionState = iota + 1
	ConnectionConnected
	ConnectionLive
	ConnectionDisconnected
)

type ConnectionStatus struct {
	State ConnectionState
	At    time.Time
	Cause error
}

type FeedTransaction struct {
	ReceivedAt     time.Time
	SequenceNumber uint64
	L1BlockNumber  uint64
	Timestamp      uint64
	BlockHash      common.Hash
	BatchIndex     uint64
	Sender         common.Address
	Transaction    *gethtypes.Transaction
}

type Handler func(context.Context, FeedTransaction) error
type GapHandler func(context.Context, SequenceGap) error
type ReorgHandler func(context.Context, Reorg) error
type VerificationFailureHandler func(context.Context, VerificationFailure) error
type TransactionFilter func(*gethtypes.Transaction) bool
type ConnectionStatusHandler func(context.Context, ConnectionStatus)
type SequenceHandler func(context.Context, uint64) error

type Config struct {
	URL                   string
	ChainID               uint64
	AllowedSigners        []common.Address
	InitialSequence       *uint64
	ReconnectMinDelay     time.Duration
	ReconnectMaxDelay     time.Duration
	ReadTimeout           time.Duration
	PingInterval          time.Duration
	WriteTimeout          time.Duration
	LiveThreshold         time.Duration
	MaxBacklogDuration    time.Duration
	MaxMessageBytes       int64
	DedupeCapacity        int
	ReorgWindow           int
	AllowGaps             bool
	AllowReorgs           bool
	Filter                TransactionFilter
	OnGap                 GapHandler
	OnReorg               ReorgHandler
	OnVerificationFailure VerificationFailureHandler
	OnStatus              ConnectionStatusHandler
	OnSequence            SequenceHandler
}
