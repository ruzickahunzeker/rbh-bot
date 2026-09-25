package main

import (
	"context"
	"fmt"
	"log"
	"os"

	sdk "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	rpcURL := os.Getenv("ROBINHOOD_RPC_URL")
	txHash := os.Getenv("TX_HASH")
	if rpcURL == "" || txHash == "" {
		log.Fatal("set ROBINHOOD_RPC_URL and TX_HASH")
	}
	ctx := context.Background()
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	receipt, err := client.TransactionReceipt(ctx, common.HexToHash(txHash))
	if err != nil {
		log.Fatal(err)
	}
	events, err := sdk.New().ParseReceipt(receipt)
	if err != nil {
		log.Fatal(err)
	}
	for _, event := range events {
		fmt.Printf("protocol=%s kind=%d data=%#v\n", event.Protocol, event.Kind, event.Data)
	}
}
