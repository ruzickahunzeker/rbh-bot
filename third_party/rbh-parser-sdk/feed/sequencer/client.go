package sequencer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/websocket"
)

const (
	feedClientVersionHeader = "Arbitrum-Feed-Client-Version"
	requestedSequenceHeader = "Arbitrum-Requested-Sequence-Number"
	defaultMaxMessageBytes  = 16 << 20
	maximumMessageBytes     = 64 << 20
	maximumDedupeCapacity   = 1 << 20
	maximumReorgWindow      = 1 << 20
	maximumReconnectDelay   = time.Hour
	maximumIOTimeout        = 24 * time.Hour
	maximumLiveThreshold    = 365 * 24 * time.Hour
	maximumBacklogDuration  = 24 * time.Hour
)

type Client struct {
	config   Config
	verifier feedVerifier
	dialer   *websocket.Dialer
}

func New(config Config) (*Client, error) {
	config, err := withDefaults(config)
	if err != nil {
		return nil, err
	}
	dialer := *websocket.DefaultDialer
	dialer.EnableCompression = true
	return &Client{
		config:   config,
		verifier: newVerifier(config.ChainID, config.AllowedSigners),
		dialer:   &dialer,
	}, nil
}

func withDefaults(config Config) (Config, error) {
	if config.URL == "" {
		config.URL = MainnetURL
	}
	parsed, err := url.Parse(config.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.User != nil {
		return Config{}, fmt.Errorf("URL: %w", ErrInvalidConfig)
	}
	if config.ChainID == 0 {
		config.ChainID = uint64(RobinhoodChainID)
	}
	if len(config.AllowedSigners) == 0 {
		config.AllowedSigners = []common.Address{mainnetSigner}
	}
	for _, signer := range config.AllowedSigners {
		if signer == (common.Address{}) {
			return Config{}, fmt.Errorf("allowed signer: %w", ErrInvalidConfig)
		}
	}
	config.AllowedSigners = append([]common.Address(nil), config.AllowedSigners...)
	if config.InitialSequence != nil {
		initialSequence := *config.InitialSequence
		config.InitialSequence = &initialSequence
	}
	if config.ReconnectMinDelay == 0 {
		config.ReconnectMinDelay = 250 * time.Millisecond
	}
	if config.ReconnectMaxDelay == 0 {
		config.ReconnectMaxDelay = 10 * time.Second
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = 30 * time.Second
	}
	if config.PingInterval == 0 {
		config.PingInterval = 10 * time.Second
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = 5 * time.Second
	}
	if config.LiveThreshold == 0 {
		config.LiveThreshold = 5 * time.Second
	}
	if config.MaxBacklogDuration == 0 {
		config.MaxBacklogDuration = 2 * time.Minute
	}
	if config.MaxMessageBytes == 0 {
		config.MaxMessageBytes = defaultMaxMessageBytes
	}
	if config.DedupeCapacity == 0 {
		config.DedupeCapacity = 65_536
	}
	if config.ReorgWindow == 0 {
		config.ReorgWindow = 1_024
	}
	if config.ReconnectMinDelay < 0 || config.ReconnectMinDelay > maximumReconnectDelay ||
		config.ReconnectMaxDelay < config.ReconnectMinDelay || config.ReconnectMaxDelay > maximumReconnectDelay ||
		config.ReadTimeout < 0 || config.ReadTimeout > maximumIOTimeout ||
		config.PingInterval < 0 || config.PingInterval > maximumIOTimeout ||
		config.WriteTimeout <= 0 || config.WriteTimeout > maximumIOTimeout ||
		config.LiveThreshold < 0 || config.LiveThreshold > maximumLiveThreshold ||
		config.MaxBacklogDuration < 0 || config.MaxBacklogDuration > maximumBacklogDuration ||
		config.MaxMessageBytes < 1 || config.MaxMessageBytes > maximumMessageBytes ||
		config.DedupeCapacity < 1 || config.DedupeCapacity > maximumDedupeCapacity ||
		config.ReorgWindow < 1 || config.ReorgWindow > maximumReorgWindow {
		return Config{}, ErrInvalidConfig
	}
	return config, nil
}

func (c *Client) Run(ctx context.Context, handler Handler) error {
	if c == nil || c.dialer == nil || handler == nil || ctx == nil {
		return ErrInvalidConfig
	}
	state := streamState{
		dedupe: newDeduper(c.config.DedupeCapacity),
		hashes: make(map[uint64]common.Hash, c.config.ReorgWindow),
	}
	if c.config.InitialSequence != nil {
		state.hasSequence = true
		state.highestSequence = *c.config.InitialSequence
	}
	delay := c.config.ReconnectMinDelay
	for {
		c.reportStatus(ctx, ConnectionConnecting, nil)
		progress, err := c.consume(ctx, handler, &state)
		if err == nil {
			return nil
		}
		c.reportStatus(ctx, ConnectionDisconnected, err)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var retryable retryableError
		if !errors.As(err, &retryable) {
			return err
		}
		if progress {
			delay = c.config.ReconnectMinDelay
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
		delay = nextBackoff(delay, c.config.ReconnectMaxDelay)
	}
}

func nextBackoff(delay, maximum time.Duration) time.Duration {
	if delay >= maximum || delay > maximum-delay {
		return maximum
	}
	return delay * 2
}

func (c *Client) consume(ctx context.Context, handler Handler, state *streamState) (bool, error) {
	headers := http.Header{feedClientVersionHeader: []string{"2"}}
	if state.hasSequence {
		headers.Set(requestedSequenceHeader, fmt.Sprintf("%d", state.highestSequence))
	}
	connection, response, err := c.dialer.DialContext(ctx, c.config.URL, headers)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return false, retryableError{err: c.connectionError(err)}
	}
	defer connection.Close()
	c.reportStatus(ctx, ConnectionConnected, nil)
	connection.SetReadLimit(c.config.MaxMessageBytes)
	if c.config.ReadTimeout > 0 {
		connection.SetPongHandler(func(string) error {
			return connection.SetReadDeadline(time.Now().Add(c.config.ReadTimeout))
		})
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-done:
		}
	}()
	defer close(done)
	if c.config.PingInterval > 0 {
		go c.keepAlive(connection, done)
	}

	progress := false
	started := time.Now()
	live := false
	for {
		if c.config.ReadTimeout > 0 {
			if err := connection.SetReadDeadline(time.Now().Add(c.config.ReadTimeout)); err != nil {
				return progress, retryableError{err: c.connectionError(err)}
			}
		}
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			return progress, retryableError{err: c.connectionError(err)}
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}
		receivedAt := time.Now()
		envelope, err := decodeEnvelope(payload)
		if err != nil {
			return progress, err
		}
		progress = true
		for index := range envelope.Messages {
			wire := envelope.Messages[index]
			messageLive := c.isLive(wire, receivedAt, started)
			if live && !messageLive {
				return progress, retryableError{err: fmt.Errorf("%w: stale or future message received after live head", ErrConnection)}
			}
			decoded, recovered, err := decodeMessage(wire, c.verifier, messageLive, c.config.Filter)
			if errors.Is(err, ErrInvalidSignature) {
				if c.config.OnVerificationFailure != nil {
					var sequence uint64
					if wire.SequenceNumber != nil {
						sequence = *wire.SequenceNumber
					}
					if callbackErr := c.config.OnVerificationFailure(ctx, VerificationFailure{SequenceNumber: sequence, RecoveredSigner: recovered, Cause: err}); callbackErr != nil {
						return progress, callbackErr
					}
				}
				return progress, err
			}
			if err != nil {
				return progress, err
			}
			accepted, err := c.acceptSequence(ctx, state, decoded)
			if err != nil {
				return progress, err
			}
			if accepted && c.config.OnSequence != nil {
				if err := c.config.OnSequence(ctx, decoded.sequenceNumber); err != nil {
					return progress, err
				}
			}
			if !messageLive && receivedAt.Sub(started) >= c.config.MaxBacklogDuration {
				return progress, retryableError{err: fmt.Errorf("%w: backlog did not reach live head within %s", ErrConnection, c.config.MaxBacklogDuration)}
			}
			if !accepted || !messageLive {
				continue
			}
			if !live {
				live = true
				c.reportStatus(ctx, ConnectionLive, nil)
			}
			for _, transaction := range decoded.transactions {
				if !state.dedupe.add(transaction.Transaction.Hash()) {
					continue
				}
				transaction.ReceivedAt = receivedAt
				if err := handler(ctx, transaction); err != nil {
					return progress, err
				}
			}
		}
	}
}

func (c *Client) reportStatus(ctx context.Context, state ConnectionState, cause error) {
	if c.config.OnStatus != nil {
		c.config.OnStatus(ctx, ConnectionStatus{State: state, At: time.Now(), Cause: cause})
	}
}

func (c *Client) isLive(message wireMessage, receivedAt, started time.Time) bool {
	if message.Message == nil || message.Message.Message == nil || message.Message.Message.Header == nil || message.Message.Message.Header.Timestamp == nil {
		return false
	}
	timestamp := *message.Message.Message.Header.Timestamp
	if timestamp > uint64(math.MaxInt64) {
		return false
	}
	stamp := time.Unix(int64(timestamp), 0) // #nosec G115 -- bounded by math.MaxInt64 above.
	age := receivedAt.Sub(stamp)
	return age >= -c.config.LiveThreshold && age <= c.config.LiveThreshold
}

func (c *Client) acceptSequence(ctx context.Context, state *streamState, message decodedMessage) (bool, error) {
	if state.hasSequence && message.sequenceNumber <= state.highestSequence {
		previous, known := state.hashes[message.sequenceNumber]
		if !known || previous == message.blockHash {
			return false, nil
		}
		if c.config.OnReorg != nil {
			if err := c.config.OnReorg(ctx, Reorg{SequenceNumber: message.sequenceNumber, PreviousHash: previous, ReplacementHash: message.blockHash}); err != nil {
				return false, err
			}
		}
		if !c.config.AllowReorgs {
			return false, fmt.Errorf("%w at sequence %d: old=%s new=%s", ErrReorg, message.sequenceNumber, previous, message.blockHash)
		}
		for sequence := range state.hashes {
			if sequence >= message.sequenceNumber {
				delete(state.hashes, sequence)
			}
		}
		state.highestSequence = 0
		if message.sequenceNumber > 0 {
			state.highestSequence = message.sequenceNumber - 1
		}
	}
	if state.hasSequence && message.sequenceNumber > state.highestSequence && message.sequenceNumber-state.highestSequence > 1 {
		gap := SequenceGap{Expected: state.highestSequence + 1, Received: message.sequenceNumber}
		if c.config.OnGap != nil {
			if err := c.config.OnGap(ctx, gap); err != nil {
				return false, err
			}
		}
		if !c.config.AllowGaps {
			return false, fmt.Errorf("%w: expected=%d received=%d", ErrSequenceGap, gap.Expected, gap.Received)
		}
	}
	state.highestSequence = message.sequenceNumber
	state.hasSequence = true
	state.hashes[message.sequenceNumber] = message.blockHash
	if len(state.hashes) > c.config.ReorgWindow*2 {
		cutoff := uint64(0)
		if state.highestSequence > uint64(c.config.ReorgWindow) { // #nosec G115 -- ReorgWindow is validated positive.
			cutoff = state.highestSequence - uint64(c.config.ReorgWindow) // #nosec G115 -- ReorgWindow is validated positive.
		}
		for sequence := range state.hashes {
			if sequence < cutoff {
				delete(state.hashes, sequence)
			}
		}
	}
	return true, nil
}

func (c *Client) keepAlive(connection *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(c.config.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if err := connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(c.config.WriteTimeout)); err != nil {
				_ = connection.Close()
				return
			}
		}
	}
}

func (c *Client) connectionError(err error) error {
	message := err.Error()
	if c != nil && c.config.URL != "" {
		message = strings.ReplaceAll(message, c.config.URL, "[REDACTED]")
		if parsed, parseErr := url.Parse(c.config.URL); parseErr == nil {
			for _, credential := range []string{parsed.EscapedPath(), parsed.RawPath, parsed.RawQuery} {
				if credential != "" {
					message = strings.ReplaceAll(message, credential, "[REDACTED]")
				}
			}
		}
	}
	return fmt.Errorf("%w: %s", ErrConnection, message)
}

type streamState struct {
	hasSequence     bool
	highestSequence uint64
	hashes          map[uint64]common.Hash
	dedupe          *deduper
}

type retryableError struct{ err error }

func (e retryableError) Error() string { return e.err.Error() }
func (e retryableError) Unwrap() error { return e.err }

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type deduper struct {
	seen  map[common.Hash]struct{}
	ring  []common.Hash
	next  int
	limit int
}

func newDeduper(limit int) *deduper {
	return &deduper{seen: make(map[common.Hash]struct{}, limit), ring: make([]common.Hash, 0, limit), limit: limit}
}

func (d *deduper) add(hash common.Hash) bool {
	if _, exists := d.seen[hash]; exists {
		return false
	}
	if len(d.ring) < d.limit {
		d.ring = append(d.ring, hash)
	} else {
		delete(d.seen, d.ring[d.next])
		d.ring[d.next] = hash
		d.next = (d.next + 1) % d.limit
	}
	d.seen[hash] = struct{}{}
	return true
}
