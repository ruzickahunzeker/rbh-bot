package blockrazor

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/websocket"
)

func signedTestTransaction(t testing.TB, chainID int64, nonce uint64) (*gethtypes.Transaction, common.Address) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress("0x1234")
	tx := gethtypes.NewTx(&gethtypes.DynamicFeeTx{ChainID: big.NewInt(chainID), Nonce: nonce, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(10), Gas: 100_000, To: &to, Value: big.NewInt(7), Data: []byte{1, 2, 3, 4}})
	signed, err := gethtypes.SignTx(tx, gethtypes.LatestSignerForChainID(big.NewInt(chainID)), key)
	if err != nil {
		t.Fatal(err)
	}
	return signed, crypto.PubkeyToAddress(key.PublicKey)
}

func testEnvelope(t testing.TB, version, sequence uint64, tx *gethtypes.Transaction, sender common.Address) []byte {
	t.Helper()
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"version": version,
		"messages": []any{map[string]any{
			"sequenceNumber": sequence,
			"blockHash":      common.HexToHash("0xbeef").Hex(),
			"message": map[string]any{"message": map[string]any{
				"header": map[string]any{"blockNumber": 88, "timestamp": 99},
				"l2Msg": map[string]any{"items": []any{map[string]any{
					"index": 3, "messageKind": 4,
					"transaction": map[string]any{"hash": tx.Hash().Hex(), "from": sender.Hex(), "rawTransaction": hexutil.Encode(raw)},
				}}},
			}},
		}},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestDecodeSignedTransaction(t *testing.T) {
	tx, sender := signedTestTransaction(t, RobinhoodChainID, 1)
	transactions, err := Decode(testEnvelope(t, 1, 42, tx, sender))
	if err != nil {
		t.Fatal(err)
	}
	if len(transactions) != 1 {
		t.Fatalf("transactions = %d", len(transactions))
	}
	got := transactions[0]
	if got.SequenceNumber != 42 || got.BlockNumber != 88 || got.Timestamp != 99 || got.BatchIndex != 3 || got.Sender != sender || got.Transaction.Hash() != tx.Hash() || got.BlockHash != common.HexToHash("0xbeef") {
		t.Fatalf("unexpected transaction: %#v", got)
	}
}

func TestDecodeRejectsOversizedDirectInput(t *testing.T) {
	if _, err := Decode(make([]byte, defaultMaxMessageBytes+1)); !errors.Is(err, ErrMalformedFeed) {
		t.Fatalf("error = %v, want ErrMalformedFeed", err)
	}
}

func TestDecodeRejectsUntrustedEnvelopeFields(t *testing.T) {
	tx, sender := signedTestTransaction(t, RobinhoodChainID, 1)
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{"unsupported version", testEnvelope(t, 2, 1, tx, sender), ErrUnsupportedVersion},
		{"wrong sender", testEnvelope(t, 1, 1, tx, common.HexToAddress("0x9999")), ErrMalformedFeed},
		{"trailing JSON", append(testEnvelope(t, 1, 1, tx, sender), []byte(` {}`)...), ErrMalformedFeed},
	}
	wrongChain, wrongSender := signedTestTransaction(t, 1, 2)
	tests = append(tests, struct {
		name string
		data []byte
		want error
	}{"wrong chain", testEnvelope(t, 1, 1, wrongChain, wrongSender), ErrWrongChainID})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode(test.data); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDecodeRequiresContinuityMetadata(t *testing.T) {
	tx, sender := signedTestTransaction(t, RobinhoodChainID, 1)
	for _, field := range []string{"sequenceNumber", "blockHash"} {
		t.Run(field, func(t *testing.T) {
			var envelope map[string]any
			if err := json.Unmarshal(testEnvelope(t, 1, 42, tx, sender), &envelope); err != nil {
				t.Fatal(err)
			}
			messages := envelope["messages"].([]any)
			delete(messages[0].(map[string]any), field)
			payload, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(payload); !errors.Is(err, ErrMalformedFeed) {
				t.Fatalf("error=%v, want ErrMalformedFeed", err)
			}
		})
	}
}

func TestClientReportsConnectionLifecycle(t *testing.T) {
	tx, sender := signedTestTransaction(t, RobinhoodChainID, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.WriteMessage(websocket.TextMessage, testEnvelope(t, 1, 10, tx, sender))
		<-request.Context().Done()
	}))
	defer server.Close()

	var states []ConnectionState
	client, err := New(Config{
		Region: RegionOhio, AuthToken: "token", ReadTimeout: time.Second,
		OnStatus: func(_ context.Context, status ConnectionStatus) { states = append(states, status.State) },
	})
	if err != nil {
		t.Fatal(err)
	}
	client.endpoint = "ws" + strings.TrimPrefix(server.URL, "http")
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

func TestDecodeFilteredSkipsSenderRecoveryForRejectedTransaction(t *testing.T) {
	tx, _ := signedTestTransaction(t, RobinhoodChainID, 1)
	data := testEnvelope(t, 1, 1, tx, common.HexToAddress("0x9999"))
	transactions, err := DecodeFiltered(data, func(*gethtypes.Transaction) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if len(transactions) != 0 {
		t.Fatalf("transactions = %d, want 0", len(transactions))
	}
	if _, err := DecodeFiltered(data, func(*gethtypes.Transaction) bool { return true }); !errors.Is(err, ErrMalformedFeed) {
		t.Fatalf("included invalid sender error = %v, want ErrMalformedFeed", err)
	}
}

func TestClientReconnectDeduplicatesAndReportsGap(t *testing.T) {
	tx1, sender1 := signedTestTransaction(t, RobinhoodChainID, 1)
	tx2, sender2 := signedTestTransaction(t, RobinhoodChainID, 2)
	frame1 := testEnvelope(t, 1, 10, tx1, sender1)
	frame3 := testEnvelope(t, 1, 12, tx2, sender2)
	var connections atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		if connections.Add(1) == 1 {
			_ = connection.WriteMessage(websocket.TextMessage, frame1)
			return
		}
		_ = connection.WriteMessage(websocket.TextMessage, frame1)
		_ = connection.WriteMessage(websocket.TextMessage, frame3)
		<-request.Context().Done()
	}))
	defer server.Close()

	var gaps []SequenceGap
	client, err := New(Config{Region: RegionOhio, AuthToken: "secret-token", ReconnectMinDelay: time.Millisecond, ReconnectMaxDelay: time.Millisecond, ReadTimeout: time.Second, PingInterval: time.Hour, AllowGaps: true, OnGap: func(_ context.Context, gap SequenceGap) error {
		gaps = append(gaps, gap)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	client.endpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	stop := errors.New("stop")
	var mu sync.Mutex
	var received []FeedTransaction
	err = client.Run(context.Background(), func(_ context.Context, transaction FeedTransaction) error {
		mu.Lock()
		received = append(received, transaction)
		count := len(received)
		mu.Unlock()
		if count == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("run error = %v", err)
	}
	if len(received) != 2 || received[0].Transaction.Hash() != tx1.Hash() || received[1].Transaction.Hash() != tx2.Hash() || received[0].ReceivedAt.IsZero() || received[1].Region != RegionOhio {
		t.Fatalf("unexpected deliveries: %#v", received)
	}
	if len(gaps) != 1 || gaps[0] != (SequenceGap{Expected: 11, Received: 12}) {
		t.Fatalf("gaps = %#v", gaps)
	}
}

func TestClientConfigurationAndRedaction(t *testing.T) {
	if _, err := New(Config{Region: Region("invalid"), AuthToken: "token"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid region error = %v", err)
	}
	if _, err := New(Config{Region: RegionOhio, AuthToken: "bad/token"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid token error = %v", err)
	}
	client, err := New(Config{Region: RegionTokyo, AuthToken: "private.token"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(client.connectionError(errors.New("private.token leaked")).Error(), "private.token") {
		t.Fatal("authentication token was not redacted")
	}
}

func TestClientRejectsUnboundedResourceConfiguration(t *testing.T) {
	tests := []Config{
		{DedupeCapacity: maximumDedupeCapacity + 1},
		{ReorgWindow: maximumReorgWindow + 1},
		{ReconnectMinDelay: maximumReconnectDelay + 1, ReconnectMaxDelay: maximumReconnectDelay + 1},
		{ReconnectMaxDelay: maximumReconnectDelay + 1},
		{ReadTimeout: maximumIOTimeout + 1},
		{PingInterval: maximumIOTimeout + 1},
		{WriteTimeout: maximumIOTimeout + 1},
	}
	for index, config := range tests {
		config.Region, config.AuthToken = RegionOhio, "token"
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
	client, err := New(Config{Region: RegionOhio, AuthToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	state := streamState{hasSequence: true, lastSequence: 10, hashes: make(map[uint64]common.Hash), dedupe: newDeduper(8)}
	if _, err := client.acceptSequence(context.Background(), &state, decodedMessage{sequenceNumber: 12}); !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("gap error = %v", err)
	}
	oldHash, newHash := common.HexToHash("0x1"), common.HexToHash("0x2")
	state.hashes[10] = oldHash
	if _, err := client.acceptSequence(context.Background(), &state, decodedMessage{sequenceNumber: 10, blockHash: newHash}); !errors.Is(err, ErrReorg) {
		t.Fatalf("reorg error = %v", err)
	}
}

func TestSequenceReorgCanBeExplicitlyHandled(t *testing.T) {
	oldHash, newHash := common.HexToHash("0x1"), common.HexToHash("0x2")
	callbacks := 0
	client, err := New(Config{
		Region: RegionOhio, AuthToken: "token", AllowReorgs: true,
		OnReorg: func(_ context.Context, reorg Reorg) error {
			callbacks++
			if reorg.SequenceNumber != 10 || reorg.PreviousHash != oldHash || reorg.ReplacementHash != newHash {
				t.Fatalf("unexpected reorg: %#v", reorg)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := streamState{hasSequence: true, lastSequence: 10, hashes: map[uint64]common.Hash{10: oldHash}, dedupe: newDeduper(8)}
	accepted, err := client.acceptSequence(context.Background(), &state, decodedMessage{sequenceNumber: 10, blockHash: newHash})
	if err != nil || !accepted || callbacks != 1 || state.hashes[10] != newHash || state.lastSequence != 10 {
		t.Fatalf("accepted=%v callbacks=%d state=%#v err=%v", accepted, callbacks, state, err)
	}
}

func TestMultiClientKeepsHealthyRegionRunning(t *testing.T) {
	tx, sender := signedTestTransaction(t, RobinhoodChainID, 1)
	goodFrame := testEnvelope(t, 1, 10, tx, sender)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := func(payload []byte, delay time.Duration) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			connection, err := upgrader.Upgrade(writer, request, nil)
			if err != nil {
				return
			}
			defer connection.Close()
			time.Sleep(delay)
			_ = connection.WriteMessage(websocket.TextMessage, payload)
			<-request.Context().Done()
		}))
	}
	bad := server([]byte(`{"version":2,"messages":[]}`), 0)
	defer bad.Close()
	good := server(goodFrame, 20*time.Millisecond)
	defer good.Close()
	multi, err := NewMulti(
		Config{Region: RegionOhio, AuthToken: "token-a", ReadTimeout: time.Second},
		Config{Region: RegionTokyo, AuthToken: "token-b", ReadTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	multi.clients[0].endpoint = "ws" + strings.TrimPrefix(bad.URL, "http")
	multi.clients[1].endpoint = "ws" + strings.TrimPrefix(good.URL, "http")
	stop := errors.New("stop")
	count := 0
	err = multi.Run(context.Background(), func(context.Context, FeedTransaction) error {
		count++
		return stop
	})
	if !errors.Is(err, stop) || count != 1 {
		t.Fatalf("error=%v count=%d", err, count)
	}
}
