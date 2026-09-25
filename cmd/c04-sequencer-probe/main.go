// Command c04-sequencer-probe performs read-only raw feed boundary observations.
// It never constructs, signs, or broadcasts a transaction.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

const (
	mainnetFeed   = "wss://feed.mainnet.chain.robinhood.com"
	requestHeader = "Arbitrum-Requested-Sequence-Number"
	versionHeader = "Arbitrum-Feed-Client-Version"
)

type envelope struct {
	Messages []struct {
		SequenceNumber *uint64 `json:"sequenceNumber"`
	} `json:"messages"`
}

type observation struct {
	CapturedAtUTC      string  `json:"captured_at_utc"`
	EndpointClass      string  `json:"endpoint_class"`
	Requested          *uint64 `json:"requested_sequence,omitempty"`
	First              *uint64 `json:"first_sequence,omitempty"`
	Last               *uint64 `json:"last_sequence,omitempty"`
	Classification     string  `json:"classification"`
	HTTPStatus         int     `json:"http_status,omitempty"`
	WebSocketExtension string  `json:"websocket_extension,omitempty"`
	CompressionOffered bool    `json:"compression_offered"`
	ErrorClass         string  `json:"error_class,omitempty"`
	SignedOrBroadcast  bool    `json:"signed_or_broadcast"`
}

func main() {
	requested := flag.Uint64("initial-sequence", 0, "requested sequence; zero omits the header")
	timeout := flag.Duration("timeout", 20*time.Second, "read-only connection timeout")
	output := flag.String("output", "", "optional JSON output path")
	flag.Parse()
	if *timeout <= 0 || *timeout > time.Minute {
		fatal("invalid timeout")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	headers := http.Header{versionHeader: []string{"2"}}
	var requestedPtr *uint64
	if *requested != 0 {
		value := *requested
		requestedPtr = &value
		headers.Set(requestHeader, fmt.Sprintf("%d", value))
	}
	result := observation{CapturedAtUTC: time.Now().UTC().Format(time.RFC3339Nano), EndpointClass: "public_robinhood_mainnet_sequencer", Requested: requestedPtr, Classification: "SEMANTICS_NOT_PROVEN", CompressionOffered: true, SignedOrBroadcast: false}
	dialer := websocket.Dialer{EnableCompression: true, Proxy: http.ProxyFromEnvironment, HandshakeTimeout: *timeout}
	connection, response, err := dialer.DialContext(ctx, mainnetFeed, headers)
	if err != nil {
		if response != nil {
			result.HTTPStatus = response.StatusCode
			result.WebSocketExtension = response.Header.Get("Sec-WebSocket-Extensions")
		}
		result.Classification = "TRANSPORT_ERROR"
		result.ErrorClass = fmt.Sprintf("%T", err)
		write(result, *output)
		return
	}
	defer connection.Close()
	if response != nil {
		result.HTTPStatus = response.StatusCode
		result.WebSocketExtension = response.Header.Get("Sec-WebSocket-Extensions")
	}
	_ = connection.SetReadDeadline(time.Now().Add(*timeout))
	_, payload, err := connection.ReadMessage()
	if err != nil {
		result.Classification = "TRANSPORT_ERROR"
		result.ErrorClass = fmt.Sprintf("%T", err)
		write(result, *output)
		return
	}
	var frame envelope
	if json.Unmarshal(payload, &frame) != nil || len(frame.Messages) == 0 {
		result.Classification = "MALFORMED"
		write(result, *output)
		return
	}
	for _, message := range frame.Messages {
		if message.SequenceNumber == nil {
			result.Classification = "MALFORMED"
			write(result, *output)
			return
		}
		if result.First == nil {
			value := *message.SequenceNumber
			result.First = &value
		}
		value := *message.SequenceNumber
		result.Last = &value
	}
	result.Classification = "SUPPORTED_AND_OBSERVED"
	write(result, *output)
}

func write(value observation, path string) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fatal("encode result")
	}
	encoded = append(encoded, '\n')
	if path != "" {
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			fatal("write result")
		}
	}
	_, _ = os.Stdout.Write(encoded)
}

func fatal(message string) {
	_, _ = fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
