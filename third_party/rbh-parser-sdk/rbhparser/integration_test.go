package rbhparser

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func TestRobinhoodLongReceipt(t *testing.T) {
	rpcURL := os.Getenv("ROBINHOOD_RPC_URL")
	txHash := os.Getenv("LONG_TX_HASH")
	if rpcURL == "" || txHash == "" {
		t.Skip("set ROBINHOOD_RPC_URL and LONG_TX_HASH to run the live check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	receipt, err := client.TransactionReceipt(ctx, common.HexToHash(txHash))
	if err != nil {
		t.Fatal(err)
	}
	events, err := New().ParseReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind != EventLaunch || event.Protocol != ProtocolLong {
			continue
		}
		launch := event.Data.(Launch)
		if launch.Pool == nil || launch.PoolID == (common.Hash{}) || launch.Token == (common.Address{}) || launch.Quote == (common.Address{}) {
			t.Fatalf("incomplete Long launch: %#v", launch)
		}
		return
	}
	t.Fatal("Long launch event not found")
}
