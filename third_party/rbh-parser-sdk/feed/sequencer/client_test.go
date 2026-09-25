package sequencer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/gorilla/websocket"
)

func TestClientRequestsInitialSequenceAndEmitsVerifiedTransactions(t *testing.T) {
	const initialSequence = uint64(20_842_308)
	requestSeen := make(chan string, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestSeen <- request.Header.Get(requestedSequenceHeader)
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.WriteMessage(websocket.TextMessage, []byte(verifiedFrame))
		<-request.Context().Done()
	}))
	defer server.Close()

	stop := errors.New("stop")
	client, err := New(Config{
		URL:             "ws" + strings.TrimPrefix(server.URL, "http"),
		InitialSequence: pointer(initialSequence),
		LiveThreshold:   365 * 24 * time.Hour,
		ReadTimeout:     time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	err = client.Run(context.Background(), func(context.Context, FeedTransaction) error {
		count++
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("got %v, want stop", err)
	}
	if count != 1 {
		t.Fatalf("got %d callback invocations, want 1", count)
	}
	select {
	case requested := <-requestSeen:
		if requested != "20842308" {
			t.Fatalf("requested sequence %q", requested)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive feed connection")
	}
}

func TestClientOffersPerMessageDeflate(t *testing.T) {
	offered := make(chan string, 1)
	upgrader := websocket.Upgrader{EnableCompression: true, CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		extension := request.Header.Get("Sec-WebSocket-Extensions")
		offered <- extension
		if !strings.Contains(extension, "permessage-deflate") {
			http.Error(writer, "compression required", http.StatusBadRequest)
			return
		}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.WriteMessage(websocket.TextMessage, []byte(verifiedFrame))
		<-request.Context().Done()
	}))
	defer server.Close()

	stop := errors.New("stop")
	client, err := New(Config{
		URL:           "ws" + strings.TrimPrefix(server.URL, "http"),
		LiveThreshold: 365 * 24 * time.Hour,
		ReadTimeout:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Run(context.Background(), func(context.Context, FeedTransaction) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("run error=%v", err)
	}
	select {
	case extension := <-offered:
		if !strings.Contains(extension, "permessage-deflate") {
			t.Fatalf("compression extension not offered: %q", extension)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not observe handshake")
	}
}

func TestClientReportsVerifiedConnectionLifecycle(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.WriteMessage(websocket.TextMessage, []byte(verifiedFrame))
		<-request.Context().Done()
	}))
	defer server.Close()

	var states []ConnectionState
	client, err := New(Config{
		URL:           "ws" + strings.TrimPrefix(server.URL, "http"),
		LiveThreshold: 365 * 24 * time.Hour,
		ReadTimeout:   time.Second,
		OnStatus: func(_ context.Context, status ConnectionStatus) {
			states = append(states, status.State)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop")
	if err := client.Run(context.Background(), func(context.Context, FeedTransaction) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("run error=%v", err)
	}
	want := []ConnectionState{ConnectionConnecting, ConnectionConnected, ConnectionLive, ConnectionDisconnected}
	if len(states) != len(want) {
		t.Fatalf("states=%v, want=%v", states, want)
	}
	for index := range want {
		if states[index] != want[index] {
			t.Fatalf("states=%v, want=%v", states, want)
		}
	}
}

func TestSequenceCallbackAdvancesWithoutMatchingTransactions(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.WriteMessage(websocket.TextMessage, []byte(verifiedFrame))
		<-request.Context().Done()
	}))
	defer server.Close()

	stop := errors.New("stop")
	var sequence uint64
	handled := 0
	client, err := New(Config{
		URL:           "ws" + strings.TrimPrefix(server.URL, "http"),
		LiveThreshold: 365 * 24 * time.Hour,
		ReadTimeout:   time.Second,
		Filter:        func(*gethtypes.Transaction) bool { return false },
		OnSequence: func(_ context.Context, value uint64) error {
			sequence = value
			return stop
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Run(context.Background(), func(context.Context, FeedTransaction) error {
		handled++
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("run error=%v", err)
	}
	if sequence != 20_842_309 || handled != 0 {
		t.Fatalf("sequence=%d handled=%d", sequence, handled)
	}
}

func TestNewRejectsCredentialBearingURL(t *testing.T) {
	_, err := New(Config{URL: "wss://secret@example.com/feed"})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("got %v, want ErrInvalidConfig", err)
	}
}

func TestConnectionErrorDoesNotExposeURLCredential(t *testing.T) {
	const secret = "super-secret-provider-token"
	endpoint := "wss://feed.example.invalid/v2/" + secret + "?key=" + secret
	client, err := New(Config{URL: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	connectionErr := client.connectionError(errors.New("dial " + endpoint))
	if !errors.Is(connectionErr, ErrConnection) {
		t.Fatalf("connection error = %v, want ErrConnection", connectionErr)
	}
	if strings.Contains(connectionErr.Error(), secret) || strings.Contains(connectionErr.Error(), endpoint) {
		t.Fatalf("connection error exposed URL credentials: %v", connectionErr)
	}
}

func TestNewCopiesCallerOwnedConfiguration(t *testing.T) {
	initialSequence := uint64(42)
	allowedSigners := []common.Address{mainnetSigner}
	client, err := New(Config{InitialSequence: &initialSequence, AllowedSigners: allowedSigners})
	if err != nil {
		t.Fatal(err)
	}

	initialSequence = 99
	allowedSigners[0] = common.HexToAddress("0x1")
	if got := *client.config.InitialSequence; got != 42 {
		t.Fatalf("initial sequence = %d, want 42", got)
	}
	if got := client.config.AllowedSigners[0]; got != mainnetSigner {
		t.Fatalf("allowed signer = %s, want %s", got, mainnetSigner)
	}
}

func TestNewRejectsUnboundedResourceConfiguration(t *testing.T) {
	tests := []Config{
		{DedupeCapacity: maximumDedupeCapacity + 1},
		{ReorgWindow: maximumReorgWindow + 1},
		{ReconnectMinDelay: maximumReconnectDelay + 1, ReconnectMaxDelay: maximumReconnectDelay + 1},
		{ReconnectMaxDelay: maximumReconnectDelay + 1},
		{ReadTimeout: maximumIOTimeout + 1},
		{PingInterval: maximumIOTimeout + 1},
		{WriteTimeout: maximumIOTimeout + 1},
		{LiveThreshold: maximumLiveThreshold + 1},
		{MaxBacklogDuration: maximumBacklogDuration + 1},
	}
	for index, config := range tests {
		if _, err := New(config); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d error=%v, want ErrInvalidConfig", index, err)
		}
	}
}

func TestNextBackoffSaturatesWithoutOverflow(t *testing.T) {
	if got := nextBackoff(6*time.Second, 10*time.Second); got != 10*time.Second {
		t.Fatalf("backoff=%s, want 10s", got)
	}
}

func TestSequenceContinuityFailsClosed(t *testing.T) {
	client, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	state := streamState{hasSequence: true, highestSequence: 10, hashes: make(map[uint64]common.Hash), dedupe: newDeduper(8)}
	if _, err := client.acceptSequence(context.Background(), &state, decodedMessage{sequenceNumber: 12}); !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("gap error = %v", err)
	}
	oldHash, newHash := common.HexToHash("0x1"), common.HexToHash("0x2")
	state.hashes[10] = oldHash
	if _, err := client.acceptSequence(context.Background(), &state, decodedMessage{sequenceNumber: 10, blockHash: newHash}); !errors.Is(err, ErrReorg) {
		t.Fatalf("reorg error = %v", err)
	}
}

func TestSequenceContinuityCanBeExplicitlyHandled(t *testing.T) {
	client, err := New(Config{AllowGaps: true, AllowReorgs: true})
	if err != nil {
		t.Fatal(err)
	}
	oldHash, newHash := common.HexToHash("0x1"), common.HexToHash("0x2")
	state := streamState{hasSequence: true, highestSequence: 10, hashes: map[uint64]common.Hash{10: oldHash}, dedupe: newDeduper(8)}
	if accepted, err := client.acceptSequence(context.Background(), &state, decodedMessage{sequenceNumber: 10, blockHash: newHash}); err != nil || !accepted {
		t.Fatalf("handled reorg: accepted=%v err=%v", accepted, err)
	}
	if accepted, err := client.acceptSequence(context.Background(), &state, decodedMessage{sequenceNumber: 12, blockHash: common.HexToHash("0x3")}); err != nil || !accepted {
		t.Fatalf("handled gap: accepted=%v err=%v", accepted, err)
	}
}

func TestLiveWindowNeverAcceptsStaleOrFutureTimestamp(t *testing.T) {
	client, err := New(Config{LiveThreshold: 5 * time.Second, MaxBacklogDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	message := func(stamp time.Time) wireMessage {
		timestamp := uint64(stamp.Unix())
		return wireMessage{Message: &wireMessageWrapper{Message: &wireIncomingMessage{Header: &wireHeader{Timestamp: &timestamp}}}}
	}
	if !client.isLive(message(now), now, now.Add(-2*time.Second)) {
		t.Fatal("current timestamp was rejected")
	}
	if client.isLive(message(now.Add(-time.Minute)), now, now.Add(-2*time.Second)) {
		t.Fatal("stale timestamp was accepted after backlog deadline")
	}
	if client.isLive(message(now.Add(time.Minute)), now, now.Add(-2*time.Second)) {
		t.Fatal("future timestamp was accepted after backlog deadline")
	}
}

func TestVerificationFailureCallbackCannotAllowInvalidSignature(t *testing.T) {
	corrupted := strings.Replace(verifiedFrame, "/ZnWFe", "AZnWFe", 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.WriteMessage(websocket.TextMessage, []byte(corrupted))
		<-request.Context().Done()
	}))
	defer server.Close()

	callbacks := 0
	handled := 0
	client, err := New(Config{
		URL:           "ws" + strings.TrimPrefix(server.URL, "http"),
		LiveThreshold: 365 * 24 * time.Hour,
		ReadTimeout:   time.Second,
		OnVerificationFailure: func(context.Context, VerificationFailure) error {
			callbacks++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Run(context.Background(), func(context.Context, FeedTransaction) error {
		handled++
		return nil
	})
	if !errors.Is(err, ErrInvalidSignature) || callbacks != 1 || handled != 0 {
		t.Fatalf("error=%v callbacks=%d handled=%d", err, callbacks, handled)
	}
}

func pointer[T any](value T) *T { return &value }
