package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
)

func TestManualNonceObservationBytesReplacementConnection(t *testing.T) {
	d, err := Open(context.Background(), TradeOwner, filepath.Join(t.TempDir(), "whole-byte.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	f := manualnonce.Facts{OperationID: "op", RequestID: "request", BindingHash: strings.Repeat("a", 64), ChainID: 4663, WalletID: "wallet", WalletAddress: "0x" + strings.Repeat("1", 40), ContractAddress: "0x" + strings.Repeat("2", 40), RuntimeCodeHash: "0x" + strings.Repeat("3", 64), QuoteBlock: 1, QuoteHash: "0x" + strings.Repeat("4", 64), BoundAt: "2026-10-14T00:00:00Z", ExpiresAt: "2026-10-14T00:01:00Z"}
	o := manualnonce.Observation{OperationID: f.OperationID, RequestID: f.RequestID, BindingHash: f.BindingHash, ChainID: f.ChainID, WalletID: f.WalletID, WalletAddress: f.WalletAddress, ContractAddress: f.ContractAddress, RuntimeCodeHash: f.RuntimeCodeHash, BlockNumber: 1, BlockHash: f.QuoteHash, BlockCanonical: true, ConfirmedNonce: "0", PendingValue: "0", ObservedAt: f.BoundAt}
	raw := manualnonce.Encode(o)
	facts, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	valid := []any{raw, manualnonce.Digest(raw), "0", string(facts), f.BoundAt}
	query := `SELECT manual_nonce_observation_bytes_valid(hex(?),typeof(?),hex(?),typeof(?),hex(?),typeof(?),hex(?),typeof(?),hex(?),typeof(?))`
	check := func(values []any, want int) {
		t.Helper()
		args := make([]any, 0, 10)
		for _, v := range values {
			args = append(args, v, v)
		}
		var got int
		if err := d.db.QueryRow(query, args...).Scan(&got); err != nil || got != want {
			t.Fatal("bytes scalar", got, want, err)
		}
	}
	d.db.SetMaxIdleConns(0)
	for repeat := 0; repeat < 3; repeat++ {
		check(valid, 1)
		for i, v := range valid {
			for _, bad := range []any{nil, []byte(v.(string)), v.(string) + "\x00junk"} {
				values := append([]any(nil), valid...)
				values[i] = bad
				check(values, 0)
			}
		}
		values := append([]any(nil), valid...)
		values[4] = f.ExpiresAt
		check(values, 0)
	}
}
