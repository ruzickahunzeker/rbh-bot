package blockrazor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/websocket"
)

const (
	defaultReconnectMinDelay = 100 * time.Millisecond
	defaultReconnectMaxDelay = 5 * time.Second
	defaultReadTimeout       = 30 * time.Second
	defaultPingInterval      = 10 * time.Second
	defaultWriteTimeout      = 5 * time.Second
	defaultMaxMessageBytes   = 16 << 20
	maximumMessageBytes      = 64 << 20
	defaultDedupeCapacity    = 65_536
	defaultReorgWindow       = 1_024
	maximumDedupeCapacity    = 1 << 20
	maximumReorgWindow       = 1 << 20
	maximumReconnectDelay    = time.Hour
	maximumIOTimeout         = 24 * time.Hour
)

type Client struct {
	config   Config
	endpoint string
	dialer   *websocket.Dialer
}

func New(config Config) (*Client, error) {
	config, err := withDefaults(config)
	if err != nil {
		return nil, err
	}
	host, err := regionHost(config.Region)
	if err != nil {
		return nil, err
	}
	endpoint := url.URL{Scheme: "wss", Host: host, Path: "/ws/direct/" + config.AuthToken}
	dialer := *websocket.DefaultDialer
	return &Client{config: config, endpoint: endpoint.String(), dialer: &dialer}, nil
}

func withDefaults(config Config) (Config, error) {
	if !validToken(config.AuthToken) {
		return Config{}, fmt.Errorf("auth token: %w", ErrInvalidConfig)
	}
	if config.ReconnectMinDelay == 0 {
		config.ReconnectMinDelay = defaultReconnectMinDelay
	}
	if config.ReconnectMaxDelay == 0 {
		config.ReconnectMaxDelay = defaultReconnectMaxDelay
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = defaultReadTimeout
	}
	if config.PingInterval == 0 {
		config.PingInterval = defaultPingInterval
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = defaultWriteTimeout
	}
	if config.MaxMessageBytes == 0 {
		config.MaxMessageBytes = defaultMaxMessageBytes
	}
	if config.DedupeCapacity == 0 {
		config.DedupeCapacity = defaultDedupeCapacity
	}
	if config.ReorgWindow == 0 {
		config.ReorgWindow = defaultReorgWindow
	}
	if config.ReconnectMinDelay < 0 || config.ReconnectMinDelay > maximumReconnectDelay ||
		config.ReconnectMaxDelay < config.ReconnectMinDelay || config.ReconnectMaxDelay > maximumReconnectDelay ||
		config.ReadTimeout < 0 || config.ReadTimeout > maximumIOTimeout ||
		config.PingInterval < 0 || config.PingInterval > maximumIOTimeout ||
		config.WriteTimeout <= 0 || config.WriteTimeout > maximumIOTimeout ||
		config.MaxMessageBytes < 1 || config.MaxMessageBytes > maximumMessageBytes ||
		config.DedupeCapacity < 1 || config.DedupeCapacity > maximumDedupeCapacity ||
		config.ReorgWindow < 1 || config.ReorgWindow > maximumReorgWindow {
		return Config{}, ErrInvalidConfig
	}
	return config, nil
}

func validToken(token string) bool {
	if token == "" || len(token) > 4096 {
		return false
	}
	for _, value := range token {
		if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune("-._~", value) {
			continue
		}
		return false
	}
	return true
}

func regionHost(region Region) (string, error) {
	switch region {
	case RegionOhio:
		return "us.robinhood-feeder.blockrazor.io", nil
	case RegionTokyo:
		return "jp.robinhood-feeder.blockrazor.io", nil
	default:
		return "", fmt.Errorf("region %q: %w", region, ErrInvalidConfig)
	}
}

func (c *Client) Run(ctx context.Context, handler Handler) error {
	if c == nil || c.dialer == nil || c.endpoint == "" || handler == nil {
		return ErrInvalidConfig
	}
	if ctx == nil {
		return fmt.Errorf("nil context: %w", ErrInvalidConfig)
	}
	state := streamState{dedupe: newDeduper(c.config.DedupeCapacity), hashes: make(map[uint64]common.Hash, c.config.ReorgWindow)}
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
	connection, response, err := c.dialer.DialContext(ctx, c.endpoint, nil)
	if err != nil && response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			return false, ErrAuthentication
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
		receivedAt := time.Now()
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}
		frame, err := decodeFrame(payload, c.config.Filter)
		if err != nil {
			return progress, err
		}
		for _, message := range frame.messages {
			accepted, err := c.acceptSequence(ctx, state, message)
			if err != nil {
				return progress, err
			}
			if !accepted {
				continue
			}
			if !progress {
				c.reportStatus(ctx, ConnectionLive, nil)
			}
			progress = true
			for _, transaction := range message.transactions {
				hash := transaction.Transaction.Hash()
				if !state.dedupe.add(hash) {
					continue
				}
				transaction.Region = c.config.Region
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

func (c *Client) acceptSequence(ctx context.Context, state *streamState, message decodedMessage) (bool, error) {
	if state.hasSequence && message.sequenceNumber <= state.lastSequence {
		previous, known := state.hashes[message.sequenceNumber]
		if !known || previous == message.blockHash || message.blockHash == (common.Hash{}) {
			return false, nil
		}
		reorg := Reorg{SequenceNumber: message.sequenceNumber, PreviousHash: previous, ReplacementHash: message.blockHash}
		if c.config.OnReorg != nil {
			if err := c.config.OnReorg(ctx, reorg); err != nil {
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
		state.lastSequence = 0
		if message.sequenceNumber > 0 {
			state.lastSequence = message.sequenceNumber - 1
		}
	}
	if state.hasSequence && message.sequenceNumber > state.lastSequence && message.sequenceNumber-state.lastSequence > 1 {
		gap := SequenceGap{Expected: state.lastSequence + 1, Received: message.sequenceNumber}
		if c.config.OnGap != nil {
			if err := c.config.OnGap(ctx, gap); err != nil {
				return false, err
			}
		}
		if !c.config.AllowGaps {
			return false, fmt.Errorf("%w: expected=%d received=%d", ErrSequenceGap, gap.Expected, gap.Received)
		}
	}
	state.lastSequence = message.sequenceNumber
	state.hasSequence = true
	if message.blockHash != (common.Hash{}) {
		state.hashes[message.sequenceNumber] = message.blockHash
	}
	if len(state.hashes) > c.config.ReorgWindow*2 {
		cutoff := uint64(0)
		if state.lastSequence > uint64(c.config.ReorgWindow) {
			cutoff = state.lastSequence - uint64(c.config.ReorgWindow)
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
			deadline := time.Now().Add(c.config.WriteTimeout)
			if err := connection.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				_ = connection.Close()
				return
			}
		}
	}
}

func (c *Client) connectionError(err error) error {
	message := strings.ReplaceAll(err.Error(), c.config.AuthToken, "[REDACTED]")
	return fmt.Errorf("%w: %s", ErrConnection, message)
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

type streamState struct {
	hasSequence  bool
	lastSequence uint64
	hashes       map[uint64]common.Hash
	dedupe       *deduper
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
