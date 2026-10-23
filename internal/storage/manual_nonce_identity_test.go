package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestManualNonceIdentityRelationStorageClassAndReplacementConnection(t *testing.T) {
	d, err := Open(context.Background(), TradeOwner, filepath.Join(t.TempDir(), "nonce-identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.db.SetMaxIdleConns(0)
	for i := 0; i < 3; i++ {
		for _, tc := range []struct {
			value      any
			target     string
			fold, want int
		}{
			{"wallet-01", "wallet-01", 0, 1}, {[]byte("wallet-01"), "wallet-01", 0, 2},
			{"WALLET-01", "wallet-01", 0, 0}, {[]byte("other"), "wallet-01", 0, 0},
			{nil, "wallet-01", 0, 0}, {23, "23", 0, 0}, {23.0, "23", 0, 0},
			{"0xAB", "0xab", 1, 1}, {[]byte("0xAB"), "0xab", 1, 2},
			{[]byte{}, "wallet-01", 0, 0}, {"wallet-01\x00", "wallet-01", 0, 0},
		} {
			var got int
			if err := d.db.QueryRow(`SELECT manual_nonce_identity_relation(hex(?),typeof(?),hex(?),?)`, tc.value, tc.value, tc.target, tc.fold).Scan(&got); err != nil || got != tc.want {
				t.Fatal(tc, got, err)
			}
		}
		for _, query := range []string{`SELECT manual_nonce_identity_relation('61','text',NULL,0)`, `SELECT manual_nonce_identity_relation('61',X'74657874','61',0)`, `SELECT manual_nonce_identity_relation('61','text','61',2)`, `SELECT manual_nonce_identity_relation('GG','text','61',0)`} {
			var got int
			if err := d.db.QueryRow(query).Scan(&got); err == nil {
				t.Fatal("invalid comparator accepted", query, got)
			}
		}
		for _, tc := range []struct {
			value any
			want  int
		}{
			{"0", 1}, {"23", 1}, {"9223372036854775808", 1}, {"18446744073709551614", 1},
			{nil, 0}, {[]byte("23"), 0}, {23, 0}, {23.0, 0}, {"23\x00junk", 0}, {"023", 0}, {"18446744073709551615", 0},
		} {
			var got int
			if err := d.db.QueryRow(`SELECT manual_nonce_stored_value_valid(hex(?),typeof(?))`, tc.value, tc.value).Scan(&got); err != nil || got != tc.want {
				t.Fatal("stored nonce validation", tc, got, err)
			}
		}
	}
}
