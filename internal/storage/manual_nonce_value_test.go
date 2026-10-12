package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestManualNonceValueScalarStorageClassAndReplacementConnection(t *testing.T) {
	d, err := Open(context.Background(), TradeOwner, filepath.Join(t.TempDir(), "nonce-values.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.db.SetMaxIdleConns(0)
	for i := 0; i < 3; i++ {
		for _, tc := range []struct {
			value any
			want  int
		}{
			{"0", 1}, {"23", 1}, {"9223372036854775808", 1}, {"18446744073709551614", 1},
			{nil, 0}, {[]byte("23"), 0}, {[]byte{}, 0}, {int64(23), 0}, {float64(23), 0},
			{"", 0}, {"023", 0}, {"+23", 0}, {"-1", 0}, {"23.5", 0}, {"18446744073709551615", 0}, {"18446744073709551616", 0},
		} {
			var got int
			if err := d.db.QueryRow(`SELECT manual_nonce_value_valid(?)`, tc.value).Scan(&got); err != nil || got != tc.want {
				t.Fatal(tc, got, err)
			}
		}
	}
}
