package blockrazor

import (
	"context"
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

const RobinhoodChainID int64 = 4663

var (
	ErrAuthentication     = errors.New("direct feed authentication failed")
	ErrConnection         = errors.New("direct feed connection failed")
	ErrInvalidConfig      = errors.New("invalid direct feed configuration")
	ErrMalformedFeed      = errors.New("malformed direct feed message")
	ErrUnsupportedVersion = errors.New("unsupported direct feed version")
	ErrWrongChainID       = errors.New("unexpected transaction chain id")
	ErrSequenceGap        = errors.New("direct feed sequence gap")
	ErrReorg              = errors.New("direct feed reorg")
)

type Region string

const (
	RegionOhio  Region = "ohio"
	RegionTokyo Region = "tokyo"
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
	Region         Region
	ReceivedAt     time.Time
	SequenceNumber uint64
	BlockNumber    uint64
	Timestamp      uint64
	BlockHash      common.Hash
	BatchIndex     uint64
	Sender         common.Address
	Transaction    *gethtypes.Transaction
}

type Handler func(context.Context, FeedTransaction) error

type GapHandler func(context.Context, SequenceGap) error

type ReorgHandler func(context.Context, Reorg) error

type TransactionFilter func(*gethtypes.Transaction) bool

type ConnectionStatusHandler func(context.Context, ConnectionStatus)

type Config struct {
	Region            Region
	AuthToken         string
	ReconnectMinDelay time.Duration
	ReconnectMaxDelay time.Duration
	ReadTimeout       time.Duration
	PingInterval      time.Duration
	WriteTimeout      time.Duration
	MaxMessageBytes   int64
	DedupeCapacity    int
	ReorgWindow       int
	AllowGaps         bool
	AllowReorgs       bool
	Filter            TransactionFilter
	OnGap             GapHandler
	OnReorg           ReorgHandler
	OnStatus          ConnectionStatusHandler
}
