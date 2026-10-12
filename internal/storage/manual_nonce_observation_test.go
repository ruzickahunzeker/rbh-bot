package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
)

func TestManualNonceSchemaScalarReplacementConnectionFailClosed(t *testing.T) {
	d, err := Open(context.Background(), TradeOwner, filepath.Join(t.TempDir(), "scalar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	f := manualnonce.Facts{OperationID: "op", RequestID: "request", BindingHash: strings.Repeat("a", 64), ChainID: 4663, WalletID: "wallet",
		WalletAddress: "0x" + strings.Repeat("1", 40), ContractAddress: "0x" + strings.Repeat("2", 40), RuntimeCodeHash: "0x" + strings.Repeat("3", 64),
		QuoteBlock: 1, QuoteHash: "0x" + strings.Repeat("4", 64), BoundAt: "2026-10-12T00:00:00Z", ExpiresAt: "2026-10-12T00:01:00Z"}
	o := manualnonce.Observation{OperationID: f.OperationID, RequestID: f.RequestID, BindingHash: f.BindingHash, ChainID: f.ChainID, WalletID: f.WalletID,
		WalletAddress: f.WalletAddress, ContractAddress: f.ContractAddress, RuntimeCodeHash: f.RuntimeCodeHash, BlockNumber: 1, BlockHash: f.QuoteHash,
		BlockCanonical: true, ConfirmedNonce: "0", PendingValue: "0", ObservedAt: f.BoundAt}
	raw := manualnonce.Encode(o)
	facts, _ := json.Marshal(f)
	d.db.SetMaxIdleConns(0)
	for i := 0; i < 3; i++ {
		for _, tc := range []struct {
			raw, hash string
			want      int
		}{{raw, manualnonce.Digest(raw), 1}, {raw, "bad", 0}, {"invalid", "bad", 0}} {
			var got int
			if err := d.db.QueryRow(`SELECT manual_nonce_observation_valid(?,?,'0',?,?)`, tc.raw, tc.hash, string(facts), f.BoundAt).Scan(&got); err != nil || got != tc.want {
				t.Fatal("scalar replaced connection failure", got, err)
			}
		}
	}
	var zero int
	if err := d.db.QueryRow(`SELECT manual_nonce_observation_valid(NULL,NULL,NULL,NULL,NULL)`).Scan(&zero); err != nil || zero != 0 {
		t.Fatal("NULL scalar did not reject", zero, err)
	}
}
